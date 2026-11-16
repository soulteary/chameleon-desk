package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	chameleon "github.com/soulteary/chameleon-desk"
)

func webSlotPromotionResult(t *testing.T, lf bool) chameleon.ReadResult {
	t.Helper()
	r := webPromotionResult()
	s := &chameleon.SlotSnapshot{Schema: 1, Source: "emulator_slot", MetadataRead: true, Slot: 2, Sense: chameleon.SenseHF, TagType: 1000, Enabled: true,
		Identity: r.Identity, UnitBytes: 16, Units: 20, Read: 20, Data: r.Dump.Data, Complete: true, Unknown: []chameleon.SlotUnknownRange{},
		Uncaptured: []string{"emulator_configuration", "version_signature_counters", "flash_persistence", "physical_source_provenance"}}
	if lf {
		s.Sense, s.TagType, s.Identity, s.UnitBytes, s.Units, s.Read = chameleon.SenseLF, 150, nil, 8, 1, 1
		s.Data = chameleon.HexBytes("12345678")
		record, err := chameleon.DecodeLFRecord("pac", 150, s.Data)
		if err != nil {
			t.Fatal(err)
		}
		s.Record = &record
	}
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	s.SHA256 = hex.EncodeToString(sum[:])
	out := chameleon.ReadResult{Schema: 1, Kind: "slot", Slot: s, Complete: true, Requested: s.Units, Read: s.Read}
	if s.Identity != nil {
		out.UID = s.Identity.UID
	}
	return out
}

func TestWebSlotPromotionIsOfflineAppendOnlyAndRetainsPhysicalWriteBoundary(t *testing.T) {
	ws := &webServer{openDevice: func(context.Context, string, time.Duration) (*chameleon.Client, error) {
		t.Error("offline conversion opened a device")
		return nil, io.EOF
	}}
	h := offlineWeb(t, ws)
	call := func(path string, payload any, status int) []byte {
		t.Helper()
		raw, _ := json.Marshal(payload)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "http://127.0.0.1"+path, bytes.NewReader(raw)))
		if w.Code != status {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	for _, lf := range []bool{false, true} {
		t.Run(fmt.Sprint(lf), func(t *testing.T) {
			source, err := chameleon.NewGUICardFromRead("slot source", webSlotPromotionResult(t, lf), "")
			if err != nil {
				t.Fatal(err)
			}
			db, _ := chameleon.ParseGUIDatabase([]byte(`{"flutter.cards":[],"custom":42}`))
			db, err = db.AddCard(source)
			if err != nil {
				t.Fatal(err)
			}
			state := call("/api/features/check", map[string]any{"id": "library.promote-slot", "card": source}, 200)
			if !bytes.Contains(state, []byte(`"state":"available"`)) {
				t.Fatal(string(state))
			}
			raw := call("/api/database", map[string]any{"op": "promote-slot", "database": db, "id": source.ID}, 200)
			var out struct {
				Database json.RawMessage   `json:"database"`
				Card     chameleon.GUICard `json:"card"`
			}
			if err := json.Unmarshal(raw, &out); err != nil {
				t.Fatal(err)
			}
			next, err := chameleon.ParseGUIDatabase(out.Database)
			if err != nil {
				t.Fatal(err)
			}
			if out.Card.ID == source.ID || out.Card.Tag == 0 || !bytes.Contains(raw, []byte(`"source":"emulator_slot"`)) || !bytes.Contains(raw, []byte(`"physical_write_allowed":false`)) || !bytes.Contains(out.Database, []byte(`"custom":42`)) {
				t.Fatal(string(raw))
			}
			original, _ := next.Card(source.ID)
			before, _ := json.Marshal(source)
			after, _ := json.Marshal(original)
			if !bytes.Equal(before, after) {
				t.Fatal("source draft changed")
			}
			records, _ := next.Records(false, "", "all")
			if len(records) != 2 {
				t.Fatal(records)
			}
			call("/api/database", map[string]any{"op": "promote-slot", "database": next, "id": out.Card.ID}, 422)
			call("/api/database", map[string]any{"op": "promote-slot", "database": next, "id": "missing"}, 404)
			call("/api/database", map[string]any{"op": "promote-slot", "database": db, "id": source.ID, "allow_unknown": true}, 400)
			call("/api/database", map[string]any{"op": "promote-slot", "database": db, "id": source.ID, "type2_promotion": map[string]any{}}, 400)
			preview := call("/api/features/check", map[string]any{"id": "library.promote-slot", "card": source, "type2_promotion": map[string]any{}}, 200)
			if !bytes.Contains(preview, []byte(`"state":"unavailable"`)) || !bytes.Contains(preview, []byte("credential-assignment")) {
				t.Fatal(string(preview))
			}

			if lf {
				request := chameleon.LFPhysicalWriteRequest{Schema: 1, Source: out.Card, Protocol: "pac", TargetIsolated: true, NewPassword: chameleon.HexBytes{0, 0, 0, 0}, OldPasswords: []chameleon.HexBytes{{0, 0, 0, 0}}}
				w := type2HandlerCall(ws.handleLFWritePlan, lfPhysicalWriteBody{Request: request})
				if w.Code != 400 || !strings.Contains(w.Body.String(), "slot-derived") {
					t.Fatal(w.Code, w.Body.String())
				}
			}
		})
	}
	bad := webSlotPromotionResult(t, false)
	bad.Complete = false
	bad.Slot.Complete = false
	bad.Slot.SHA256 = ""
	raw, _ := json.Marshal(bad.Slot)
	sum := sha256.Sum256(raw)
	bad.Slot.SHA256 = hex.EncodeToString(sum[:])
	c, err := chameleon.NewGUICardFromRead("interrupted", bad, "")
	if err != nil {
		t.Fatal(err)
	}
	state := call("/api/features/check", map[string]any{"id": "library.promote-slot", "card": c}, 200)
	if !bytes.Contains(state, []byte(`"state":"unavailable"`)) || !bytes.Contains(state, []byte("complete snapshot")) {
		t.Fatal(string(state))
	}
}

func TestSlotPromotionCLIPlanConvertAndDatabaseAppend(t *testing.T) {
	dir := t.TempDir()
	file := func(name string) string { return filepath.Join(dir, name) }
	writeReaderFixture(t, file("read.json"), map[string]any{"schema": 1, "read_result": webSlotPromotionResult(t, false), "error": ""})
	if err := runReaderWorkflowCLI([]string{"read-draft", "import", "--input", file("read.json"), "--output", file("draft.json")}); err != nil {
		t.Fatal(err)
	}
	if err := runReaderWorkflowCLI([]string{"read-draft", "promote", "--input", file("draft.json"), "--kind", "slot", "--plan", "--output", file("plan.json")}); err != nil {
		t.Fatal(err)
	}
	var plan map[string]any
	readReaderFixture(t, file("plan.json"), &plan)
	if plan["kind"] != "slot" || plan["available"] != true {
		t.Fatal(plan)
	}
	if err := runReaderWorkflowCLI([]string{"read-draft", "promote", "--input", file("draft.json"), "--output", file("standard.json")}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(file("standard.json"))
	card, err := chameleon.ParseGUICard(raw)
	if err != nil || card.Tag != 1000 || !bytes.Contains(raw, []byte(`"emulator_slot"`)) {
		t.Fatal(string(raw), err)
	}
	if err := runReaderWorkflowCLI([]string{"read-draft", "promote", "--input", file("draft.json"), "--emulator-password", "01020304", "--output", file("rejected.json")}); err == nil {
		t.Fatal("slot conversion accepted physical Type2 credential options")
	}
	writeReaderFixture(t, file("db.json"), map[string]any{"flutter.cards": []string{}, "custom": 42})
	if err := runReaderWorkflowCLI([]string{"database", "import-read", "--input", file("db.json"), "--source", file("read.json"), "--output", file("imported.json")}); err != nil {
		t.Fatal(err)
	}
	db, err := readGUIDB(file("imported.json"))
	if err != nil {
		t.Fatal(err)
	}
	rows, _ := db.Records(false, "", "all")
	if err := runReaderWorkflowCLI([]string{"database", "promote-read", "--input", file("imported.json"), "--id", rows[0].ID, "--kind", "slot", "--output", file("converted.json")}); err != nil {
		t.Fatal(err)
	}
	db, err = readGUIDB(file("converted.json"))
	if err != nil {
		t.Fatal(err)
	}
	rows, _ = db.Records(false, "", "all")
	if len(rows) != 2 {
		t.Fatal(rows)
	}
	if _, err := db.Card(plan["source_card_id"].(string)); err == nil {
		t.Fatal("independent imports reused a draft ID")
	}
}
