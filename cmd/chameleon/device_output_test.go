package main

import (
	"encoding/json"
	"errors"
	chameleon "github.com/soulteary/chameleon-desk"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDeviceCLIsRejectOutputBeforeOpening(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "existing")
	if e := os.WriteFile(existing, []byte("original"), 0600); e != nil {
		t.Fatal(e)
	}
	for _, output := range []string{existing, filepath.Join(dir, "missing", "out")} {
		commands := [][]string{
			{"slot", "select", "--yes", "--port", "must-not-open"},
			{"mfc", "clone", "--yes", "--port", "must-not-open"},
			{"ulc", "ulcg-collect", "--yes", "--erase-key", "--port", "must-not-open"},
			{"gen3", "lock", "--yes", "--permanent-lock", "--port", "must-not-open"},
			{"settings", "configure", "--yes", "--port", "must-not-open"},
			{"backup", "restore", "--native-usb", "--yes", "--port", "must-not-open"},
			{"slot", "load-card", "--yes", "--port", "must-not-open"},
			{"scan", "--port", "must-not-open"},
			{"stress", "--port", "must-not-open"},
			{"firmware", "download", "--port", "must-not-open"},
		}
		for _, args := range commands {
			t.Run(strings.Join(args[:2], "-")+filepath.Base(output), func(t *testing.T) {
				prior := os.Args
				os.Args = append([]string{"chameleon"}, append(args, "--output", output)...)
				defer func() { os.Args = prior }()
				e := run()
				var path *os.PathError
				if e == nil || !errors.As(e, &path) {
					t.Fatalf("did not fail at output preflight: %v", e)
				}
			})
		}
	}
	if e := runExtendedWith([]string{"slot", "select", "--slot", "2", "--yes", "--output", existing}, noOpen(t)); !errors.Is(e, os.ErrExist) {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(existing)
	if string(b) != "original" {
		t.Fatal("preflight overwrote file")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatal("probe files leaked", entries)
	}
}
func TestDeviceOperationReportsLateOutputConflict(t *testing.T) {
	p := filepath.Join(t.TempDir(), "result.json")
	open := mockDevice(t, []step{{cmd: 1003, payload: []byte{1}, status: 0x68}})
	e := runExtendedWith([]string{"slot", "select", "--slot", "2", "--yes", "--port", "mock", "--output", p}, func(s string, d time.Duration) (*chameleon.Client, error) {
		if e := os.WriteFile(p, []byte("external"), 0600); e != nil {
			t.Fatal(e)
		}
		return open(s, d)
	})
	if !errors.Is(e, os.ErrExist) || !strings.Contains(e.Error(), "device operation already ran") {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "external" {
		t.Fatal("late collision overwritten")
	}
}
func TestRecoveryResumeKeepsCompletedOutput(t *testing.T) {
	dir := t.TempDir()
	capture := filepath.Join(dir, "capture.json")
	output := filepath.Join(dir, "offline.json")
	b := chameleon.RecoveryBundle{Schema: 1, Kind: "nested", UID: chameleon.HexBytes{1, 2, 3, 4}, TargetBlock: 4, TargetType: chameleon.KeyA, Tool: "staticnested", Arguments: []string{"16909060", "96", "1", "2"}}
	if e := b.Seal(); e != nil {
		t.Fatal(e)
	}
	if e := chameleon.WriteRecoveryBundle(capture, b); e != nil {
		t.Fatal(e)
	}
	prior := offlineRecoveryCheckpoint{OfflineRecovery: chameleon.OfflineRecovery{Kind: b.Kind, CaptureSHA256: b.Checksum, UID: b.UID, Block: b.TargetBlock, KeyType: b.TargetType, Candidates: []chameleon.HexBytes{{1, 2, 3, 4, 5, 6}}, Tool: chameleon.RecoveryResult{Tool: b.Tool}}, Completed: true}
	if e := result(prior, output, false); e != nil {
		t.Fatal(e)
	}
	before, _ := os.ReadFile(output)
	if e := runWorkflow([]string{"mfc", "recover-compute", "--resume", "--input", capture, "--output", output, "--tools-dir", "must-not-run"}); e != nil {
		t.Fatal(e)
	}
	after, _ := os.ReadFile(output)
	if string(before) != string(after) {
		t.Fatal("cached output changed")
	}
}

func TestRecoveryResumeRejectsFailedAndIncompleteResults(t *testing.T) {
	for _, name := range []string{"missing-helper", "failed-helper", "legacy-result", "wrong-target", "invalid-candidate"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			capture, output := filepath.Join(dir, "capture.json"), filepath.Join(dir, "offline.json")
			bundle := chameleon.RecoveryBundle{Schema: 1, Kind: "nested", UID: chameleon.HexBytes{1, 2, 3, 4}, TargetBlock: 4, TargetType: chameleon.KeyA, Tool: "staticnested", Arguments: []string{"16909060", "96", "1", "2"}}
			if err := bundle.Seal(); err != nil {
				t.Fatal(err)
			}
			if err := chameleon.WriteRecoveryBundle(capture, bundle); err != nil {
				t.Fatal(err)
			}
			tools := filepath.Join(dir, "tools")
			if err := os.Mkdir(tools, 0700); err != nil {
				t.Fatal(err)
			}
			args := []string{"mfc", "recover-compute", "--input", capture, "--output", output, "--tools-dir", tools}
			if name == "failed-helper" {
				if err := os.WriteFile(filepath.Join(tools, "staticnested"), []byte("#!/bin/sh\nprintf 'Key 1... 010203040506\\n'\nexit 1\n"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if name == "missing-helper" || name == "failed-helper" {
				if err := runWorkflow(args); err == nil {
					t.Fatal("helper failure reported success")
				}
				var failed offlineRecoveryCheckpoint
				raw, err := os.ReadFile(output)
				if err != nil || json.Unmarshal(raw, &failed) != nil || failed.Completed || failed.Error == "" {
					t.Fatalf("failed run did not persist an incomplete checkpoint: %s %v", raw, err)
				}
			} else {
				prior := offlineRecoveryCheckpoint{OfflineRecovery: chameleon.OfflineRecovery{Kind: bundle.Kind, CaptureSHA256: bundle.Checksum, UID: bundle.UID, Block: bundle.TargetBlock, KeyType: bundle.TargetType, Candidates: []chameleon.HexBytes{{1, 2, 3, 4, 5, 6}}, Tool: chameleon.RecoveryResult{Tool: bundle.Tool}}, Completed: true}
				var value any = prior
				switch name {
				case "legacy-result":
					value = prior.OfflineRecovery
				case "wrong-target":
					prior.Block++
					value = prior
				case "invalid-candidate":
					prior.Candidates = []chameleon.HexBytes{{1}}
					value = prior
				}
				if err := result(value, output, false); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(output)
			if err := runWorkflow(append(args, "--resume")); err == nil || !strings.Contains(err.Error(), "incomplete or does not match") {
				t.Fatalf("incomplete checkpoint resumed successfully: %v", err)
			}
			after, _ := os.ReadFile(output)
			if string(before) != string(after) {
				t.Fatal("rejected checkpoint was overwritten")
			}
		})
	}
}

func TestRecoveryResumeAcceptsCompletedSearchWithoutCandidates(t *testing.T) {
	dir := t.TempDir()
	capture, output := filepath.Join(dir, "capture.json"), filepath.Join(dir, "offline.json")
	bundle := chameleon.RecoveryBundle{Schema: 1, Kind: "nested", UID: chameleon.HexBytes{1, 2, 3, 4}, TargetBlock: 4, TargetType: chameleon.KeyA, Tool: "staticnested", Arguments: []string{"16909060", "96", "1", "2"}}
	if err := chameleon.WriteRecoveryBundle(capture, bundle); err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(dir, "staticnested")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nprintf 'No matching key\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	args := []string{"mfc", "recover-compute", "--input", capture, "--output", output, "--tools-dir", dir}
	if err := runWorkflow(args); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(helper); err != nil {
		t.Fatal(err)
	}
	if err := runWorkflow(append(args, "--resume")); err != nil {
		t.Fatalf("completed empty result did not resume: %v", err)
	}
}
