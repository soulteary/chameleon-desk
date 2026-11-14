package chameleon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func testNestedBundle() RecoveryBundle {
	return RecoveryBundle{Schema: RecoveryBundleSchema, Kind: "nested", UID: HexBytes{1, 2, 3, 4}, TargetBlock: 4, TargetType: KeyB, Tool: "staticnested", Arguments: []string{"16909060", "96", "1", "2"}}
}
func TestRecoveryBundleIntegrityAndExclusiveSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capture.json")
	b := testNestedBundle()
	if err := WriteRecoveryBundle(path, b); err != nil {
		t.Fatal(err)
	}
	loaded, err := ReadRecoveryBundle(path)
	if err != nil || loaded.Checksum == "" {
		t.Fatal(loaded, err)
	}
	if err = WriteRecoveryBundle(path, b); err == nil {
		t.Fatal("existing capture overwritten")
	}
	data, _ := os.ReadFile(path)
	var raw map[string]any
	_ = json.Unmarshal(data, &raw)
	raw["target_block"] = float64(5)
	data, _ = json.Marshal(raw)
	_ = os.WriteFile(path, data, 0600)
	if _, err = ReadRecoveryBundle(path); err == nil {
		t.Fatal("modified capture accepted")
	}
}
func TestOfflineComputeRemainsUnverified(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "staticnested"), []byte("#!/bin/sh\nprintf 'Key 1... 010203040506\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	b := testNestedBundle()
	if err := b.Seal(); err != nil {
		t.Fatal(err)
	}
	out, err := ComputeRecovery(context.Background(), dir, b)
	if err != nil || out.VerifiedOnCard || len(out.Candidates) != 1 || out.Candidates[0].String() != "010203040506" {
		t.Fatal(out, err)
	}
}
func TestRecoveryBundleRejectsBadKindAndOversizedInput(t *testing.T) {
	b := testNestedBundle()
	b.Kind = "unknown"
	if err := b.Seal(); err == nil {
		t.Fatal("unknown capture kind accepted")
	}
	p := filepath.Join(t.TempDir(), "large.json")
	if err := os.WriteFile(p, make([]byte, maxRecoveryBundle+1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRecoveryBundle(p); err == nil {
		t.Fatal("oversized bundle accepted")
	}
}
