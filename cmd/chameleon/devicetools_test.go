package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDeviceToolRouting(t *testing.T) {
	ok := [][]string{
		{"gen4", "config"}, {"gen4", "write-block"}, {"gen4", "set-uid"}, {"t55", "clean"},
		{"gen1a", "detect"}, {"gen1a", "write-block"}, {"gen1a", "set-uid"},
		{"gen3", "detect"}, {"gen3", "block0"}, {"gen3", "set-uid"}, {"gen3", "lock"},
	}
	for _, a := range ok {
		if !isDeviceTool(a) {
			t.Fatalf("%v not routed", a)
		}
	}
	for _, a := range [][]string{{"gen4"}, {"gen4", "bogus"}, {"t55", "write"}, {"ndef", "read"}, {"gen1a", "bogus"}, {"gen3", "wipe"}} {
		if isDeviceTool(a) {
			t.Fatalf("%v wrongly routed", a)
		}
	}
}

func TestDeviceToolWriteNeedsYes(t *testing.T) {
	// Write actions are refused before any device is opened when --yes is absent.
	for _, a := range [][]string{
		{"gen4", "write-block", "--block", "4", "--data", "00112233445566778899AABBCCDDEEFF"},
		{"gen4", "set-uid", "--uid", "01020304"},
		{"gen4", "set-password", "--new-password", "11223344"},
		{"t55", "clean", "--probe-id", "1122334455", "--new-password", "00000000", "--passwords", "00000000"},
		{"gen1a", "write-block", "--block", "0", "--data", "00112233445566778899AABBCCDDEEFF"},
		{"gen1a", "set-uid", "--uid", "01020304"},
		{"gen3", "block0", "--data", "00112233445566778899AABBCCDDEEFF"},
		{"gen3", "set-uid", "--uid", "01020304"},
		{"gen3", "lock"},
	} {
		if e := runDeviceTool(a); e == nil {
			t.Fatalf("%v ran without --yes", a)
		}
	}
	// gen3 lock needs the extra irreversible-lock confirmation even with --yes.
	if e := runDeviceTool([]string{"gen3", "lock", "--yes"}); e == nil {
		t.Fatal("gen3 lock ran without --permanent-lock")
	}
}

func TestT55Candidates(t *testing.T) {
	dir := t.TempDir()
	c, e := t55Candidates("", "51243648, 00000000")
	if e != nil || len(c) != 2 || len(c[0]) != 4 {
		t.Fatalf("%v %v", c, e)
	}
	if _, e := t55Candidates("", "zz"); e == nil {
		t.Fatal("bad password accepted")
	}
	// A dictionary of 8-hex (4-byte) keys parses; 12-hex keys are rejected.
	dict := filepath.Join(dir, "d.json")
	os.WriteFile(dict, []byte(`{"id":"x","name":"t55","keyLength":8,"keys":[[0,0,0,0],[81,36,54,72]]}`), 0600)
	c, e = t55Candidates(dict, "")
	if e != nil || len(c) != 2 {
		t.Fatalf("%v %v", c, e)
	}
	bad := filepath.Join(dir, "classic.json")
	os.WriteFile(bad, []byte(`{"id":"y","name":"mfc","keyLength":12,"keys":[[1,2,3,4,5,6]]}`), 0600)
	if _, e := t55Candidates(bad, ""); e == nil {
		t.Fatal("12-digit dictionary accepted for T55xx")
	}
	if _, e := t55Candidates("", ""); e == nil {
		t.Fatal("no source accepted")
	}
}
