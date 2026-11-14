package main

import (
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
			{"backup", "restore", "--yes", "--port", "must-not-open"},
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
	prior := chameleon.OfflineRecovery{CaptureSHA256: b.Checksum, UID: b.UID, Candidates: []chameleon.HexBytes{{1, 2, 3, 4, 5, 6}}}
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
