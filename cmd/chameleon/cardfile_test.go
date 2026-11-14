package main

import (
	"encoding/json"
	chameleon "github.com/soulteary/chameleon-desk"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCardfileCLI(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "dict.json")
	text := filepath.Join(dir, "keys.dic")
	output := filepath.Join(dir, "updated.json")
	if e := os.WriteFile(input, []byte(`{"id":"d","name":"keys","keyLength":12,"keys":[[255,255,255,255,255,255]],"folderId":"f"}`), 0600); e != nil {
		t.Fatal(e)
	}
	if e := runCardfile([]string{"dict-to-text", "--input", input, "--output", text}); e != nil {
		t.Fatal(e)
	}
	if e := runCardfile([]string{"dict-from-text", "--input", text, "--template", input, "--output", output}); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(output)
	if e != nil {
		t.Fatal(e)
	}
	var d chameleon.GUIDictionary
	if e = json.Unmarshal(b, &d); e != nil || len(d.Keys) != 1 {
		t.Fatal(e)
	}
	if e := runCardfile([]string{"--help"}); e != nil {
		t.Fatal(e)
	}
}
func TestFirmwareFlashRequiresConfirmationBeforeOpen(t *testing.T) {
	opened := false
	e := runExtendedWith([]string{"firmware", "flash", "--input", "missing.zip"}, func(string, time.Duration) (*chameleon.Client, error) { opened = true; return nil, nil })
	if e == nil || opened {
		t.Fatal(e, opened)
	}
}
