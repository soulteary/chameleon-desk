package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDatabaseCLIAndPlistRoundTrip(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "db.json")
	output := filepath.Join(dir, "db-out.json")
	if e := os.WriteFile(input, []byte(`{"flutter.cards":[],"flutter.card_folders":[],"flutter.dictionaries":[],"flutter.dictionary_folders":[],"flutter.theme":"dark"}`), 0600); e != nil {
		t.Fatal(e)
	}
	if e := runDatabase([]string{"export", "--input", input, "--output", output}); e != nil {
		t.Fatal(e)
	}
	if _, e := readGUIDB(output); e != nil {
		t.Fatal(e)
	}
	if runtime.GOOS == "darwin" {
		plist := filepath.Join(dir, "db.plist")
		if e := runDatabase([]string{"export", "--input", input, "--output", plist, "--format", "plist"}); e != nil {
			t.Fatal(e)
		}
		d, e := readGUIDB(plist)
		if e != nil || d.Prefix != "flutter." {
			t.Fatal(d, e)
		}
	}
}
func TestBackupCLIRejectsUnconfirmedOperation(t *testing.T) {
	if e := runBackup([]string{"create", "--port", "not-a-device", "--output", filepath.Join(t.TempDir(), "backup.json")}); e == nil {
		t.Fatal("unconfirmed slot switching accepted")
	}
}
func TestDatabaseDoesNotOverwriteSource(t *testing.T) {
	p := filepath.Join(t.TempDir(), "db.json")
	if e := os.WriteFile(p, []byte(`{"cards":[]}`), 0600); e != nil {
		t.Fatal(e)
	}
	if e := runDatabase([]string{"export", "--input", p, "--output", p}); e == nil {
		t.Fatal("source overwrite accepted")
	}
}
