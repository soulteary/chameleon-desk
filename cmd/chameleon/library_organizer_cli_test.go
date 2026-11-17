package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	chameleon "github.com/soulteary/chameleon-desk"
)

func TestLibraryOrganizerCLIIsOfflineAndPreservesSources(t *testing.T) {
	dir := t.TempDir()
	p := func(n string) string { return filepath.Join(dir, n) }
	original := []byte(`{"flutter.cards":[],"custom":42}`)
	if err := os.WriteFile(p("db.json"), original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p("keys.txt"), []byte("FFFFFFFFFFFF\nFFFFFFFFFFFF\nA0A1A2A3A4A5\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		if !isLibrary(args) {
			t.Fatal("not routed", args)
		}
		if err := runLibrary(args); err != nil {
			t.Fatal(err)
		}
	}
	run("library", "change", "--input", p("db.json"), "--dictionaries", "--action", "dictionary-create", "--name", "keys", "--source", p("keys.txt"), "--output", p("next.json"))
	db, err := readGUIDB(p("next.json"))
	if err != nil {
		t.Fatal(err)
	}
	records, err := db.Records(true, "", "all")
	if err != nil || len(records) != 1 || records[0].Keys != 2 || db.Preferences["custom"] == nil {
		t.Fatal(records, err)
	}
	run("library", "view", "--input", p("next.json"), "--dictionaries", "--output", p("view.json"))
	file := "+Sector: 0\n01020304000000000000000000000000\n--------------------------------\n"
	if err = os.WriteFile(p("dump.mct"), []byte(file), 0600); err != nil {
		t.Fatal(err)
	}
	run("library", "import-file", "--source", p("dump.mct"), "--preview", "--output", p("preview.json"))
	var obs chameleon.CardFileObservation
	readReaderFixture(t, p("preview.json"), &obs)
	if obs.Raw != file || obs.UnknownBytes != 16 {
		t.Fatal(obs)
	}
	run("library", "import-file", "--input", p("next.json"), "--source", p("dump.mct"), "--output", p("imported.json"))
	db, err = readGUIDB(p("imported.json"))
	if err != nil {
		t.Fatal(err)
	}
	records, err = db.Records(false, "", "all")
	if err != nil || len(records) != 1 {
		t.Fatal(records, err)
	}
	card, err := db.Card(records[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	draft, err := card.ReadDraft()
	if err != nil || draft.Result.Kind != "file" || draft.Result.File.Raw != file || card.RequireCardImage() == nil {
		t.Fatal(draft, err)
	}
	writeReaderFixture(t, p("card.json"), card)
	run("library", "diff", "--input", p("card.json"), "--source", p("card.json"), "--output", p("diff.json"))
	var diff chameleon.CardComparison
	readReaderFixture(t, p("diff.json"), &diff)
	if diff.UnknownComparisons != 16 {
		t.Fatal(diff)
	}
	for _, args := range [][]string{
		{"library", "change", "--input", p("db.json"), "--action", "folder-create", "--name", "x", "--output", p("db.json")},
		{"library", "import-file", "--source", p("dump.mct"), "--preview", "--output", p("dump.mct")},
		{"library", "view", "--input", p("db.json"), "--output", p("view.json")},
		{"library", "view", "--input", p("db.json"), "unexpected"},
	} {
		if err := runLibrary(args); err == nil {
			t.Fatal("unsafe or invalid output accepted", args)
		}
	}
	after, _ := os.ReadFile(p("db.json"))
	if !bytes.Equal(original, after) {
		t.Fatal("input overwritten")
	}
}

func TestEvidenceCLIPlansAndRequiresExplicitSameCard(t *testing.T) {
	dir := t.TempDir()
	p := func(n string) string { return filepath.Join(dir, n) }
	card, err := chameleon.NewGUICardFromRead("source", webPromotionResult(), "")
	if err != nil {
		t.Fatal(err)
	}
	writeReaderFixture(t, p("left.json"), card)
	writeReaderFixture(t, p("right.json"), card)
	args := []string{"read-draft", "merge", "--input", p("left.json"), "--other", p("right.json")}
	if !isReaderWorkflowCLI(args) {
		t.Fatal("not routed")
	}
	if err = runReaderWorkflowCLI(append(append([]string{}, args...), "--plan", "--output", p("plan.json"))); err != nil {
		t.Fatal(err)
	}
	if err = runReaderWorkflowCLI(append(append([]string{}, args...), "--output", p("rejected.json"))); err == nil {
		t.Fatal("unconfirmed merge accepted")
	}
	if err = runReaderWorkflowCLI(append(append([]string{}, args...), "--confirm-same-card", "--output", p("merged.json"))); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p("merged.json"))
	merged, err := chameleon.ParseGUICard(raw)
	if err != nil || merged.ID == card.ID {
		t.Fatal(merged, err)
	}
	var envelope map[string]json.RawMessage
	if err = json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	for _, tail := range [][]string{{"--confirm-same-card", "--output", p("left.json")}, {"--resolution", "0:0:invalid", "--confirm-same-card", "--output", p("bad.json")}} {
		if err = runReaderWorkflowCLI(append(append([]string{}, args...), tail...)); err == nil {
			t.Fatal("invalid merge accepted", tail)
		}
	}
}

func TestEvidenceCLIRejectsMalformedResultEnvelope(t *testing.T) {
	dir := t.TempDir()
	p := func(n string) string { return filepath.Join(dir, n) }
	card, err := chameleon.NewGUICardFromRead("source", webPromotionResult(), "")
	if err != nil {
		t.Fatal(err)
	}
	writeReaderFixture(t, p("source.json"), card)
	for _, text := range []string{`{"schema":2,"result":{},"error":null}`, `{"schema":1,"result":{},"error":false}`, `{"schema":1,"result":{},"error":null,"extra":true}`} {
		if err = os.WriteFile(p("repair.json"), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		if err = runReaderWorkflowCLI([]string{"read-draft", "apply-repair", "--input", p("source.json"), "--repair", p("repair.json"), "--plan", "--output", p("plan.json")}); err == nil {
			t.Fatal("malformed envelope accepted")
		}
	}
}
