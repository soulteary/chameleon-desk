package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	chameleon "github.com/soulteary/chameleon-desk"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRawBootloaderPayloadRejectedBeforeOpen(t *testing.T) {
	opened := false
	err := runExtendedWith([]string{"call", "ENTER_BOOTLOADER", "--data", "00", "--yes", "--force", "--port", "simulated"}, func(string, time.Duration) (*chameleon.Client, error) {
		opened = true
		return nil, fmt.Errorf("unexpected transport")
	})
	if opened || err == nil || !strings.Contains(err.Error(), "payload") {
		t.Fatalf("invalid bootloader payload reached transport: opened=%v err=%v", opened, err)
	}
}

func TestUnknownWorkflowActionsRejectedBeforeOpening(t *testing.T) {
	for _, group := range []string{"mfc", "ulc", "rf", "emv", "des", "seos", "type2"} {
		err := runWorkflow([]string{group, "unknown", "--port", "tcp:127.0.0.1:0"})
		if err == nil || !strings.Contains(err.Error(), "unknown workflow action") {
			t.Fatalf("%s unknown action reached transport: %v", group, err)
		}
	}
	if err := runWorkflow(nil); err == nil {
		t.Fatal("empty workflow accepted")
	}
}

func TestRecoveryRunLimitRejectedBeforeOpening(t *testing.T) {
	for _, runs := range []string{"0", "1001"} {
		err := runWorkflow([]string{"mfc", "recover-collect", "--kind", "darkside", "--max-runs", runs, "--port", "tcp:127.0.0.1:0", "--output", filepath.Join(t.TempDir(), "capture.json")})
		if err == nil || !strings.Contains(err.Error(), "max-runs") {
			t.Fatalf("run count %s reached transport: %v", runs, err)
		}
	}
}

func TestMemoryFormatRejectedBeforeOpening(t *testing.T) {
	for _, group := range []string{"mfc", "type2"} {
		for _, action := range []string{"esave", "eload"} {
			for _, format := range []string{"", "invalid"} {
				args := []string{group, action, "--yes", "--slot", "2", "--port", "tcp:127.0.0.1:0", "--format", format}
				flag := "--output"
				if action == "eload" {
					flag = "--input"
				}
				args = append(args, flag, filepath.Join(t.TempDir(), "memory.invalid"))
				if err := runMappedWorkflow(args); err == nil || !strings.Contains(err.Error(), "format") {
					t.Fatalf("invalid %s %s format reached transport: %v", group, action, err)
				}
			}
		}
	}
}

func TestOfflineComputeCancellationWritesFailedCheckpoint(t *testing.T) {
	dir := t.TempDir()
	capture := filepath.Join(dir, "capture.json")
	output := filepath.Join(dir, "result.json")
	b := chameleon.RecoveryBundle{Schema: 1, Kind: "nested", UID: chameleon.HexBytes{1, 2, 3, 4}, TargetBlock: 4, TargetType: chameleon.KeyA, Tool: "staticnested", Arguments: []string{"16909060", "96", "1", "2"}}
	if err := chameleon.WriteRecoveryBundle(capture, b); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := runWorkflowWithContext(ctx, []string{"mfc", "recover-compute", "--input", capture, "--output", output, "--tools-dir", dir})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("offline workflow lost cancellation: %v", err)
	}
	raw, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var checkpoint offlineRecoveryCheckpoint
	if err := json.Unmarshal(raw, &checkpoint); err != nil || checkpoint.Completed || checkpoint.Error == "" || checkpoint.VerifiedOnCard || len(checkpoint.Candidates) != 0 {
		t.Fatalf("failed operation checkpoint: %+v %v", checkpoint, err)
	}
	err = runWorkflow([]string{"mfc", "recover-compute", "--input", capture, "--output", output, "--resume", "--tools-dir", dir})
	if err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("cancelled result reused: %v", err)
	}
}

func TestOfflineComputeSignal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires POSIX signals")
	}
	const childEnv = "CHAMELEON_OFFLINE_SIGNAL_CHILD"
	if dir := os.Getenv(childEnv); dir != "" {
		err := runWorkflow([]string{"mfc", "recover-compute", "--input", filepath.Join(dir, "capture.json"), "--output", filepath.Join(dir, "result.json"), "--tools-dir", dir})
		if !errors.Is(err, context.Canceled) {
			fmt.Fprintln(os.Stderr, "unexpected signal result:", err)
			os.Exit(2)
		}
		os.Exit(0)
	}
	dir := t.TempDir()
	b := chameleon.RecoveryBundle{Schema: 1, Kind: "nested", UID: chameleon.HexBytes{1, 2, 3, 4}, TargetBlock: 4, TargetType: chameleon.KeyA, Tool: "staticnested", Arguments: []string{"16909060", "96", "1", "2"}}
	if err := chameleon.WriteRecoveryBundle(filepath.Join(dir, "capture.json"), b); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "staticnested"), []byte("#!/bin/sh\necho ready > \"$0.ready\"\nexec sleep 30\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestOfflineComputeSignal$")
	cmd.Env = append(os.Environ(), childEnv+"="+dir)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, "staticnested.ready")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("recovery helper did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal("offline CLI did not handle SIGTERM:", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "result.json"))
	if err != nil {
		t.Fatal(err)
	}
	var checkpoint offlineRecoveryCheckpoint
	if err := json.Unmarshal(raw, &checkpoint); err != nil || checkpoint.Completed || checkpoint.Error != context.Canceled.Error() || checkpoint.VerifiedOnCard {
		t.Fatalf("signal checkpoint: %+v %v", checkpoint, err)
	}
}
