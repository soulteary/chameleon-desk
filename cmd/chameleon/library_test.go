package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestLibraryCLIRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := func(n string) string { return filepath.Join(dir, n) }
	dump := make([]byte, 1024)
	copy(dump, []byte{1, 2, 3, 4})
	if e := os.WriteFile(p("dump.bin"), dump, 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p("db.json"), []byte(`{"flutter.cards":[],"flutter.app_theme":2}`), 0600); e != nil {
		t.Fatal(e)
	}
	run := func(args ...string) error {
		if !isLibrary(args) {
			t.Fatalf("%v not routed", args)
		}
		return runLibrary(args)
	}
	if e := run("cardfile", "from-bin", "--input", p("dump.bin"), "--type", "1001", "--name", "Office", "--output", p("card.json")); e != nil {
		t.Fatal(e)
	}
	if e := run("database", "add", "--input", p("db.json"), "--source", p("card.json"), "--output", p("db1.json")); e != nil {
		t.Fatal(e)
	}
	if e := run("database", "add", "--input", p("db1.json"), "--source", p("card.json"), "--output", p("dup.json")); e == nil {
		t.Fatal("duplicate accepted")
	}
	if e := run("database", "add", "--input", p("db1.json"), "--source", p("card.json"), "--new-id", "--output", p("db2.json")); e != nil {
		t.Fatal(e)
	}
	if e := run("database", "edit", "--input", p("db1.json"), "--id", "Office", "--name", "Lab", "--color", "#00ff00", "--output", p("db3.json")); e != nil {
		t.Fatal(e)
	}
	if e := run("database", "edit", "--input", p("db1.json"), "--id", "Office", "--uid", "0102", "--output", p("bad.json")); e == nil {
		t.Fatal("two-byte HF UID accepted")
	}
	if e := run("database", "show", "--input", p("db3.json"), "--id", "Lab", "--output", p("lab.json")); e != nil {
		t.Fatal(e)
	}
	if e := run("cardfile", "to-bin", "--input", p("lab.json"), "--output", p("back.bin")); e != nil {
		t.Fatal(e)
	}
	back, _ := os.ReadFile(p("back.bin"))
	if !bytes.Equal(back, dump) {
		t.Fatal("dump changed in round trip")
	}
	if e := run("database", "list", "--input", p("db3.json"), "--query", "lab", "--sense", "hf", "--output", p("list.json")); e != nil {
		t.Fatal(e)
	}
	if b, _ := os.ReadFile(p("list.json")); !bytes.Contains(b, []byte(`"MIFARE Classic 1K"`)) || bytes.Contains(b, []byte(`"data"`)) {
		t.Fatal(string(b))
	}
	if e := run("database", "delete", "--input", p("db3.json"), "--id", "Lab", "--output", p("db4.json")); e != nil {
		t.Fatal(e)
	}
	d, e := readGUIDB(p("db4.json"))
	if e != nil || d.Prefix != "flutter." || d.CollectionCounts()["cards"] != 0 || d.Preferences["flutter.app_theme"] == nil {
		t.Fatal(d.CollectionCounts(), e)
	}
	if e := run("database", "delete", "--input", p("db3.json"), "--id", "Lab", "--output", p("db3.json")); e == nil {
		t.Fatal("in-place overwrite accepted")
	}
}

func TestSlotLoadCardPlanIsOfflineAndWriteNeedsYes(t *testing.T) {
	dir := t.TempDir()
	card := filepath.Join(dir, "card.json")
	if e := os.WriteFile(card, []byte(`{"id":"a","name":"EM","uid":"0102030405","tag":100,"sak":0,"atqa":[],"ats":[],"data":[]}`), 0600); e != nil {
		t.Fatal(e)
	}
	plan := filepath.Join(dir, "plan.json")
	if e := runLibrary([]string{"slot", "load-card", "--slot", "4", "--input", card, "--plan", "--output", plan}); e != nil {
		t.Fatal(e)
	}
	if b, _ := os.ReadFile(plan); !bytes.Contains(b, []byte(`"EM410X_SET_EMU_ID"`)) || !bytes.Contains(b, []byte(`"sense": "lf"`)) {
		t.Fatal(string(b))
	}
	if e := runLibrary([]string{"slot", "load-card", "--slot", "4", "--input", card, "--port", "not-a-device"}); e == nil {
		t.Fatal("unconfirmed slot replacement accepted")
	}
	if e := runLibrary([]string{"slot", "load-card", "--slot", "0", "--input", card, "--plan"}); e == nil {
		t.Fatal("slot 0 accepted")
	}
}
