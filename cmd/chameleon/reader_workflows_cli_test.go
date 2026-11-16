package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	chameleon "github.com/soulteary/chameleon-desk"
)

func TestReaderWorkflowRoutingAndOfflineRoundTrip(t *testing.T) {
	for _, args := range [][]string{{"reader", "classic"}, {"reader", "lf"}, {"read-draft", "import"}, {"read-draft", "promote"}, {"database", "import-read"}, {"database", "promote-read"}} {
		if !isReaderWorkflowCLI(args) {
			t.Fatal(args)
		}
	}
	if isReaderWorkflowCLI([]string{"database", "show"}) || isReaderWorkflowCLI([]string{"lf", "clone"}) {
		t.Fatal("legacy operation intercepted")
	}
	dir := t.TempDir()
	file := func(name string) string { return filepath.Join(dir, name) }
	r := webPromotionResult()
	writeReaderFixture(t, file("read.json"), map[string]any{"schema": 1, "read_result": r, "error": nil})
	if err := runReaderWorkflowCLI([]string{"read-draft", "import", "--input", file("read.json"), "--name", "source", "--output", file("draft.json")}); err != nil {
		t.Fatal(err)
	}
	if err := runReaderWorkflowCLI([]string{"read-draft", "promote", "--input", file("draft.json"), "--plan", "--output", file("plan.json")}); err != nil {
		t.Fatal(err)
	}
	var plan map[string]any
	readReaderFixture(t, file("plan.json"), &plan)
	if plan["available"] != true || plan["kind"] != "classic" {
		t.Fatal(plan)
	}
	if err := runReaderWorkflowCLI([]string{"read-draft", "promote", "--input", file("draft.json"), "--output", file("standard.json")}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(file("standard.json"))
	standard, err := chameleon.ParseGUICard(raw)
	if err != nil || standard.Tag != 1000 {
		t.Fatal(string(raw), err)
	}
	sourceRaw, _ := os.ReadFile(file("draft.json"))
	source, _ := chameleon.ParseGUICard(sourceRaw)
	if source.ID == standard.ID {
		t.Fatal("source overwritten")
	}
	writeReaderFixture(t, file("database.json"), map[string]any{"flutter.cards": []string{}, "custom": 42})
	if err = runReaderWorkflowCLI([]string{"database", "import-read", "--input", file("database.json"), "--source", file("read.json"), "--output", file("imported.json")}); err != nil {
		t.Fatal(err)
	}
	db, err := readGUIDB(file("imported.json"))
	if err != nil {
		t.Fatal(err)
	}
	records, _ := db.Records(false, "", "all")
	if len(records) != 1 {
		t.Fatal(records)
	}
	if err = runReaderWorkflowCLI([]string{"database", "promote-read", "--input", file("imported.json"), "--id", records[0].ID, "--output", file("converted.json")}); err != nil {
		t.Fatal(err)
	}
	db, err = readGUIDB(file("converted.json"))
	records, _ = db.Records(false, "", "all")
	if err != nil || len(records) != 2 || db.Preferences["custom"] == nil {
		t.Fatal(records, err)
	}
	if _, err = db.Card(records[0].ID); err != nil {
		t.Fatal("source draft missing", err)
	}
}
func TestReaderWorkflowLFEnvelopeAndEditedIDPromotion(t *testing.T) {
	dir := t.TempDir()
	file := func(name string) string { return filepath.Join(dir, name) }
	present := true
	record, _ := chameleon.DecodeLFRecord("pac", 0, []byte("12345678"))
	r := chameleon.ReadResult{Schema: 1, Kind: "lf", Complete: true, Requested: 1, Read: 1, Present: &present, Record: &record}
	writeReaderFixture(t, file("read.json"), map[string]any{"schema": 1, "read_result": r, "error": nil})
	if err := runReaderWorkflowCLI([]string{"read-draft", "import", "--input", file("read.json"), "--output", file("draft.json")}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(file("draft.json"))
	draft, err := chameleon.ParseGUICard(raw)
	if err != nil {
		t.Fatal(err)
	}
	draft, err = draft.EditData("edited", []string{"4142434431323334"})
	if err != nil {
		t.Fatal(err)
	}
	writeReaderFixture(t, file("edited.json"), draft)
	if err = runReaderWorkflowCLI([]string{"read-draft", "promote", "--input", file("edited.json"), "--kind", "lf", "--output", file("standard.json")}); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(file("standard.json"))
	card, err := chameleon.ParseGUICard(raw)
	if err != nil || card.Tag != 150 || card.UID != "4142434431323334" {
		t.Fatal(card, err)
	}
}
func TestReaderWorkflowType2RequiresExplicitEmulatorCredentials(t *testing.T) {
	dir := t.TempDir()
	file := func(n string) string { return filepath.Join(dir, n) }
	uid := chameleon.HexBytes{4, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66}
	sak := byte(0)
	ats := chameleon.HexBytes{}
	data := make(chameleon.HexBytes, 45*4)
	copy(data, uid[:3])
	data[3] = 0x88 ^ uid[0] ^ uid[1] ^ uid[2]
	copy(data[4:8], uid[3:])
	data[8] = uid[3] ^ uid[4] ^ uid[5] ^ uid[6]
	data[9] = 0x48
	copy(data[12:16], []byte{0xe1, 0x10, 0x12, 0})
	r := chameleon.ReadResult{Schema: 1, Kind: "type2", UID: uid, Requested: 45, Read: 45, Complete: true, Data: data, Type2: &chameleon.Type2ReadMetadata{Schema: 1, Identity: &chameleon.ClassicIdentity{Schema: 1, UID: uid, ATQA: chameleon.HexBytes{0, 0x44}, SAK: &sak, ATS: &ats}, Model: "NTAG213", TagType: 1100, Version: chameleon.HexBytes{0, 4, 4, 2, 1, 0, 0x0f, 3}, Pages: 45, UserFirst: 4, UserLast: 39, Unknown: []chameleon.Type2UnknownBytes{{Page: 43, Offset: 0, Length: 4, Reason: "hidden_credentials"}, {Page: 44, Offset: 0, Length: 4, Reason: "hidden_credentials"}}}}
	writeReaderFixture(t, file("read.json"), readerWorkflowEnvelope{Schema: 1, Result: &r})
	if err := runReaderWorkflowCLI([]string{"read-draft", "import", "--input", file("read.json"), "--output", file("draft.json")}); err != nil {
		t.Fatal(err)
	}
	if err := runReaderWorkflowCLI([]string{"read-draft", "promote", "--input", file("draft.json"), "--plan", "--output", file("rejected.json")}); err == nil {
		t.Fatal("implicit credentials accepted")
	}
	if _, err := os.Stat(file("rejected.json")); !os.IsNotExist(err) {
		t.Fatal("rejected plan published", err)
	}
	flags := []string{"--emulator-password", "01020304", "--emulator-pack", "ABCD", "--confirm-emulator-credentials"}
	args := append([]string{"read-draft", "promote", "--input", file("draft.json"), "--output", file("card.json")}, flags...)
	if err := runReaderWorkflowCLI(args); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(file("card.json"))
	card, err := chameleon.ParseGUICard(raw)
	if err != nil || card.Tag != 1100 || !strings.EqualFold(chameleon.HexBytes(card.Data[43]).String(), "01020304") || chameleon.HexBytes(card.Data[44]).String() != "ABCD0000" {
		t.Fatal(card, err)
	}
	var draft chameleon.GUICard
	readReaderFixture(t, file("draft.json"), &draft)
	if draft.ID == card.ID {
		t.Fatal("source card replaced")
	}
	saved, _ := draft.ReadDraft()
	if saved.Rows[43] != "????????" || saved.Rows[44] != "????????" {
		t.Fatal(saved.Rows)
	}
}
func TestReaderWorkflowType2CaptureUsesExpectedUIDAndNoImplicitAuth(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "request.json")
	output := filepath.Join(dir, "read.json")
	uid := chameleon.HexBytes{4, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66}
	writeReaderFixture(t, input, chameleon.Type2WorkflowRead{UID: uid, Start: 4, Count: 1})
	var mu sync.Mutex
	rawReads := 0
	port := workflowSimulator(t, func(cmd uint16, payload []byte) (uint16, []byte) {
		switch cmd {
		case 1033:
			return 0x68, []byte{0}
		case 1035:
			return 0x68, configCaps(1001, 1002, 2000, 2010, 2101)
		case 1002:
			return 0x68, []byte{1}
		case 2000:
			return 0, append(append([]byte{7}, uid...), 0x44, 0, 0, 0)
		case 2010:
			if len(payload) < 6 {
				t.Error("short raw request")
				return 1, nil
			}
			if payload[5] == 0x60 {
				return 0, []byte{0, 4, 4, 2, 1, 0, 0x0f, 3}
			}
			if payload[5] == 0x3a {
				mu.Lock()
				rawReads++
				mu.Unlock()
				if len(payload) != 8 || payload[6] != 4 || payload[7] != 4 {
					t.Error(payload)
				}
				return 0, []byte{1, 2, 3, 4}
			}
			t.Error("unexpected authentication or raw operation", payload)
			return 1, nil
		case 2101:
			return 0x68, nil
		default:
			t.Error("unexpected operation", cmd)
			return 0x67, nil
		}
	})
	if err := runReaderWorkflowCLI([]string{"reader", "type2", "--port", port, "--input", input, "--output", output}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if rawReads != 1 {
		t.Fatal("range was replayed", rawReads)
	}
	var envelope readerWorkflowEnvelope
	readReaderFixture(t, output, &envelope)
	if envelope.Result == nil || !envelope.Result.Complete || envelope.Result.Type2 == nil || envelope.Result.Type2.Authenticated || envelope.Result.Start != 4 || envelope.Result.Data.String() != "01020304" {
		t.Fatal(envelope)
	}
}
func TestReaderWorkflowRejectsOutputAliasesAndMalformedInputBeforeOpen(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{"uid":"01020304","sectors":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	old := openReaderWorkflowClient
	defer func() { openReaderWorkflowClient = old }()
	calls := 0
	openReaderWorkflowClient = func(ctx context.Context, port string, timeout time.Duration) (*chameleon.Client, error) {
		calls++
		return nil, errors.New("unexpected opener")
	}
	for _, args := range [][]string{
		{"reader", "classic", "--input", bad, "--output", bad, "--port", "not-a-device"},
		{"reader", "classic", "--input", bad, "--output", filepath.Join(dir, "result.json"), "--port", "not-a-device"},
		{"reader", "lf", "--protocol", "pac", "--password", "00000000", "--output", filepath.Join(dir, "result.json"), "--port", "not-a-device"},
		{"reader", "lf", "--format", "256", "--output", filepath.Join(dir, "result.json")},
	} {
		if err := runReaderWorkflowCLI(args); err == nil {
			t.Fatal(args)
		}
	}
	if calls != 0 {
		t.Fatal("invalid request opened transport", calls)
	}
	envelope := filepath.Join(dir, "envelope.json")
	if err := os.WriteFile(envelope, []byte(`{"schema":1,"read_result":{},"unexpected":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runReaderWorkflowCLI([]string{"read-draft", "import", "--input", envelope}); err == nil {
		t.Fatal("unknown envelope field accepted")
	}
}
func TestReaderWorkflowClassicCaptureIncludesIdentityAndKeepsNewOutput(t *testing.T) {
	r := webPromotionResult()
	var commands []uint16
	var mu sync.Mutex
	caps := []uint16{1001, 1002, 2000, 2008}
	port := workflowSimulator(t, func(cmd uint16, payload []byte) (uint16, []byte) {
		mu.Lock()
		commands = append(commands, cmd)
		mu.Unlock()
		switch cmd {
		case 1033:
			return 0x68, []byte{0}
		case 1035:
			b := make([]byte, len(caps)*2)
			for i, c := range caps {
				binary.BigEndian.PutUint16(b[i*2:], c)
			}
			return 0x68, b
		case 1002:
			return 0x68, []byte{1}
		case 2000:
			return 0, []byte{4, 1, 2, 3, 4, 4, 0, 9, 0}
		case 2008:
			block := int(payload[1])
			return 0, append([]byte(nil), r.Dump.Data[block*16:(block+1)*16]...)
		default:
			t.Errorf("unexpected command %d", cmd)
			return 0x67, nil
		}
	})
	dir := t.TempDir()
	input := filepath.Join(dir, "keys.json")
	output := filepath.Join(dir, "read.json")
	request := webClassicInput()
	writeReaderFixture(t, input, request)
	if err := runReaderWorkflowCLI([]string{"reader", "classic", "--input", input, "--output", output, "--port", port}); err != nil {
		t.Fatal(err)
	}
	envelope, err := readWorkflowEnvelope(output)
	if err != nil || envelope.Result == nil || !envelope.Result.Complete || envelope.Result.Identity == nil || envelope.Result.Identity.ATQA.String() != "0004" {
		t.Fatal(envelope, err)
	}
	mu.Lock()
	defer mu.Unlock()
	scans := 0
	for _, cmd := range commands {
		if cmd == 2000 {
			scans++
		}
		if cmd == 2009 {
			t.Fatal("capture wrote a card")
		}
	}
	if scans != 6 {
		t.Fatal("identity scans", scans)
	}
	if err = runReaderWorkflowCLI([]string{"reader", "classic", "--input", input, "--output", output, "--port", port}); err == nil {
		t.Fatal("existing output replaced")
	}
}
func TestReaderWorkflowLFCapturePreflightAndNoCard(t *testing.T) {
	for _, noCard := range []bool{false, true} {
		t.Run(map[bool]string{false: "present", true: "absent"}[noCard], func(t *testing.T) {
			port := workflowSimulator(t, func(cmd uint16, payload []byte) (uint16, []byte) {
				switch cmd {
				case 1033:
					return 0x68, []byte{0}
				case 1035:
					return 0x68, []byte{3, 0xe9, 3, 0xea, 0x0b, 0xbc}
				case 1002:
					return 0x68, []byte{1}
				case 3004:
					if noCard {
						return 0x41, nil
					}
					return 0x40, []byte{1, 2, 3, 4}
				default:
					t.Errorf("unexpected %d", cmd)
					return 0x67, nil
				}
			})
			output := filepath.Join(t.TempDir(), "lf.json")
			err := runReaderWorkflowCLI([]string{"reader", "lf", "--protocol", "viking", "--port", port, "--output", output})
			if err != nil {
				t.Fatal(err)
			}
			envelope, err := readWorkflowEnvelope(output)
			if err != nil || envelope.Result.Present == nil || *envelope.Result.Present == noCard || !envelope.Result.Complete {
				t.Fatal(envelope, err)
			}
		})
	}
}
func writeReaderFixture(t *testing.T, path string, value any) {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
}
func readReaderFixture(t *testing.T, path string, value any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(b, value); err != nil {
		t.Fatal(err)
	}
}

func TestReaderWorkflowPromotionRejectsPartialAndDiagnosticResults(t *testing.T) {
	source := webPromotionResult()
	source.Complete = false
	draft, err := chameleon.NewGUICardFromRead("partial", source, "cancelled")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	input := filepath.Join(dir, "draft.json")
	writeReaderFixture(t, input, draft)
	if err = runReaderWorkflowCLI([]string{"read-draft", "promote", "--input", input, "--output", filepath.Join(dir, "out.json")}); err == nil || !strings.Contains(err.Error(), "complete read") {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(dir, "out.json")); !os.IsNotExist(err) {
		t.Fatal("rejected conversion published output")
	}
}
