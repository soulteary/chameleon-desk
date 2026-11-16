package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	chameleon "github.com/soulteary/chameleon-desk"
)

func readerDatabaseFixture(t *testing.T) func(string, any, int) []byte {
	t.Helper()
	ws := &webServer{allowWrites: false, openDevice: func(context.Context, string, time.Duration) (*chameleon.Client, error) {
		t.Error("offline database operation opened device")
		return nil, io.EOF
	}}
	h := offlineWeb(t, ws)
	return func(path string, payload any, status int) []byte {
		t.Helper()
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "http://127.0.0.1"+path, bytes.NewReader(raw)))
		if w.Code != status {
			t.Fatalf("%s: %d: %s", path, w.Code, w.Body.String())
		}
		return append([]byte(nil), w.Body.Bytes()...)
	}
}
func readerDatabase(t *testing.T, cards ...chameleon.GUICard) chameleon.GUIDatabase {
	t.Helper()
	db, err := chameleon.ParseGUIDatabase([]byte(`{"flutter.cards":[],"flutter.theme":"dark","custom":{"keep":42}}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, card := range cards {
		db, err = db.AddCard(card)
		if err != nil {
			t.Fatal(err)
		}
	}
	return db
}
func readerCardExtra(t *testing.T, card chameleon.GUICard) chameleon.GUICard {
	t.Helper()
	raw, _ := json.Marshal(card)
	var fields map[string]json.RawMessage
	json.Unmarshal(raw, &fields)
	fields["custom_card"] = json.RawMessage(`{"keep":true}`)
	raw, _ = json.Marshal(fields)
	card, err := chameleon.ParseGUICard(raw)
	if err != nil {
		t.Fatal(err)
	}
	return card
}
func readerDatabaseResult(t *testing.T, raw []byte) (chameleon.GUIDatabase, chameleon.GUICard) {
	t.Helper()
	var result struct {
		Database json.RawMessage   `json:"database"`
		Card     chameleon.GUICard `json:"card"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	db, err := chameleon.ParseGUIDatabase(result.Database)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(result.Database, []byte(`"keep":42`)) || !bytes.Contains(result.Database, []byte(`"flutter.theme":"dark"`)) {
		t.Fatal("database metadata discarded", string(raw))
	}
	return db, result.Card
}
func webType2PromotionRead() chameleon.ReadResult {
	uid := chameleon.HexBytes{4, 1, 2, 3, 4, 5, 6}
	sak := byte(0)
	ats := chameleon.HexBytes{}
	data := make(chameleon.HexBytes, 45*4)
	copy(data[:3], uid[:3])
	data[3] = 0x88 ^ uid[0] ^ uid[1] ^ uid[2]
	copy(data[4:8], uid[3:])
	data[8] = uid[3] ^ uid[4] ^ uid[5] ^ uid[6]
	meta := &chameleon.Type2ReadMetadata{Schema: 1, Identity: &chameleon.ClassicIdentity{Schema: 1, UID: uid, ATQA: chameleon.HexBytes{0, 0x44}, SAK: &sak, ATS: &ats}, Model: "NTAG213", TagType: 1100, Version: chameleon.HexBytes{0, 4, 4, 2, 1, 0, 0x0f, 3}, Pages: 45, UserFirst: 4, UserLast: 39, Unknown: []chameleon.Type2UnknownBytes{{Page: 43, Offset: 0, Length: 4, Reason: "hidden_credentials"}, {Page: 44, Offset: 0, Length: 4, Reason: "hidden_credentials"}}}
	return chameleon.ReadResult{Schema: 1, Kind: "type2", Complete: true, Requested: 45, Read: 45, UID: uid, Data: data, Type2: meta}
}
func webReadDraft(t *testing.T, r chameleon.ReadResult, message string) chameleon.GUICard {
	t.Helper()
	card, err := chameleon.NewGUICardFromRead("read source", r, message)
	if err != nil {
		t.Fatal(err)
	}
	return readerCardExtra(t, card)
}
func TestWebReaderDatabaseLFAndType2PromotionPreservesSources(t *testing.T) {
	call := readerDatabaseFixture(t)
	present := true
	record, err := chameleon.DecodeLFRecord("em410x", 100, chameleon.HexBytes{1, 2, 3, 4, 5})
	if err != nil {
		t.Fatal(err)
	}
	lf := webReadDraft(t, chameleon.ReadResult{Schema: 1, Kind: "lf", Complete: true, Requested: 1, Read: 1, Present: &present, Record: &record}, "")
	type2 := webReadDraft(t, webType2PromotionRead(), "")
	options := chameleon.Type2PromotionOptions{EmulatorPassword: chameleon.HexBytes{1, 2, 3, 4}, EmulatorPACK: chameleon.HexBytes{5, 6}, ConfirmEmulatorCredentials: true}
	for _, tc := range []struct {
		op      string
		card    chameleon.GUICard
		tag     uint16
		options any
	}{{"promote-lf", lf, 100, nil}, {"promote-type2", type2, 1100, options}} {
		t.Run(tc.op, func(t *testing.T) {
			db := readerDatabase(t, tc.card)
			before, _ := json.Marshal(tc.card)
			feature := call("/api/features/check", map[string]any{"id": "library." + tc.op, "card": tc.card, "type2_promotion": tc.options}, 200)
			if !bytes.Contains(feature, []byte(`"state":"available"`)) {
				t.Fatal(string(feature))
			}
			raw := call("/api/database", map[string]any{"op": tc.op, "database": db, "id": tc.card.ID, "type2_promotion": tc.options}, 200)
			next, card := readerDatabaseResult(t, raw)
			if card.ID == tc.card.ID || card.Tag != tc.tag {
				t.Fatal(string(raw))
			}
			records, _ := next.Records(false, "", "all")
			if len(records) != 2 {
				t.Fatal(records)
			}
			source, _ := next.Card(tc.card.ID)
			after, _ := json.Marshal(source)
			if !bytes.Equal(before, after) {
				t.Fatal("source changed")
			}
			converted, _ := json.Marshal(card)
			if !bytes.Contains(converted, []byte(`"custom_card":{"keep":true}`)) || !bytes.Contains(converted, []byte(`"source_card_id":"`+tc.card.ID+`"`)) {
				t.Fatal("card metadata/origin lost", string(converted))
			}
			if tc.op == "promote-type2" {
				if chameleon.HexBytes(card.Data[43]).String() != "01020304" || chameleon.HexBytes(card.Data[44]).String() != "05060000" {
					t.Fatal(card.Data[43:])
				}
				draft, _ := source.ReadDraft()
				if draft.Rows[43] != "????????" || draft.Rows[44] != "????????" {
					t.Fatal("hidden source credentials filled")
				}
			}
			call("/api/database", map[string]any{"op": tc.op, "database": next, "id": card.ID, "type2_promotion": tc.options}, 422)
		})
	}
}
func TestWebReaderDatabaseMergeAndSelectedRepairAreOfflineAppendOnly(t *testing.T) {
	call := readerDatabaseFixture(t)
	leftRead := webPromotionResult()
	leftRead.Dump.UnknownKeys = []chameleon.ClassicUnknownKey{{Sector: 0, Type: chameleon.KeyB}}
	left := webReadDraft(t, leftRead, "")
	rightRead := webPromotionResult()
	rightRead.Complete = false
	rightRead.Read = 4
	rightRead.Dump.Data = append(chameleon.HexBytes(nil), rightRead.Dump.Data[:64]...)
	sum := sha256.Sum256(rightRead.Dump.Data)
	rightRead.Dump.SHA256 = hex.EncodeToString(sum[:])
	right := webReadDraft(t, rightRead, "interrupted read")
	db := readerDatabase(t, left, right)
	before, _ := json.Marshal(left)
	options := chameleon.ClassicMergeOptions{ConfirmSameCard: true}
	preview := call("/api/database", map[string]any{"op": "merge-classic-preview", "database": db, "id": left.ID, "other_id": right.ID}, 200)
	if !bytes.Contains(preview, []byte(`"filled_bytes":6`)) {
		t.Fatal(string(preview))
	}
	denied := call("/api/features/check", map[string]any{"id": "library.merge-classic", "card": left, "other_card": right, "merge_options": map[string]any{"confirm_same_card": false}}, 200)
	if !bytes.Contains(denied, []byte(`"state":"unavailable"`)) {
		t.Fatal(string(denied))
	}
	call("/api/database", map[string]any{"op": "merge-classic", "database": db, "id": left.ID, "other_id": right.ID}, 422)
	raw := call("/api/database", map[string]any{"op": "merge-classic", "database": db, "id": left.ID, "other_id": right.ID, "merge_options": options}, 200)
	next, merged := readerDatabaseResult(t, raw)
	if merged.ID == left.ID || merged.ID == right.ID {
		t.Fatal("merge overwrote source")
	}
	records, _ := next.Records(false, "", "all")
	if len(records) != 3 {
		t.Fatal(records)
	}
	source, _ := next.Card(left.ID)
	after, _ := json.Marshal(source)
	if !bytes.Equal(before, after) {
		t.Fatal("source changed")
	}
	if !bytes.Contains(raw, []byte("interrupted read")) {
		t.Fatal("source error lost")
	}
	if err := merged.ClassicPromotionError(); err != nil {
		t.Fatal(err)
	}
	selected := chameleon.ClassicSelectedRepairResult{Schema: 1, Identity: *leftRead.Identity, TagType: 1000, Requested: 20, Blocks: []int{1}, Observations: []chameleon.ClassicSelectedBlock{{Block: 1, Data: bytes.Repeat([]byte{0xaa}, 16)}}, Complete: true}
	preview = call("/api/database", map[string]any{"op": "repair-classic-preview", "database": db, "id": left.ID, "selected_repair": selected}, 200)
	var conflicts chameleon.ClassicMergePreview
	json.Unmarshal(preview, &conflicts)
	if len(conflicts.Conflicts) != 16 {
		t.Fatal(string(preview))
	}
	call("/api/database", map[string]any{"op": "repair-classic", "database": db, "id": left.ID, "selected_repair": selected, "merge_options": options}, 422)
	for at := 0; at < 16; at++ {
		options.Resolutions = append(options.Resolutions, chameleon.ClassicMergeResolution{Block: 1, Byte: at, Source: "right"})
	}
	raw = call("/api/database", map[string]any{"op": "repair-classic", "database": db, "id": left.ID, "selected_repair": selected, "merge_options": options}, 200)
	next, repaired := readerDatabaseResult(t, raw)
	if repaired.ID == left.ID {
		t.Fatal("repair overwrote source")
	}
	draft, err := repaired.ReadDraft()
	if err != nil || draft.Rows[1] != strings.Repeat("AA", 16) || draft.Rows[3][20:] != "????????????" {
		t.Fatal(draft, err)
	}
	if repaired.ClassicPromotionError() == nil {
		t.Fatal("unverified source key became known")
	}
	call("/api/database", map[string]any{"op": "promote-classic", "database": next, "id": repaired.ID}, 422)
}
func TestWebReaderDatabaseRejectsUnknownFlagsAndMaskBypasses(t *testing.T) {
	call := readerDatabaseFixture(t)
	r := webType2PromotionRead()
	source := webReadDraft(t, r, "")
	db := readerDatabase(t, source)
	call("/api/database", map[string]any{"op": "promote-type2", "database": db, "id": source.ID}, 422)
	call("/api/database", map[string]any{"op": "promote-type2", "database": db, "id": source.ID, "allow_unknown_keys": true}, 400)
	call("/api/database", map[string]any{"op": "promote-type2", "database": db, "id": source.ID, "type2_promotion": map[string]any{"emulator_password": "01020304", "emulator_pack": "0506", "confirm_emulator_credentials": true, "assume_read_credentials": true}}, 400)
	call("/api/database", map[string]any{"op": "repair-classic", "database": db, "id": source.ID}, 400)
	call("/api/database", map[string]any{"op": "promote-type2", "database": db, "id": "missing"}, 404)
	rows, _ := source.ReadDraft()
	rows.Rows[43] = "01020304"
	call("/api/database", map[string]any{"op": "edit-data", "database": db, "id": source.ID, "rows": rows.Rows}, 422)
	bad := r
	metadata := *r.Type2
	metadata.Unknown = nil
	bad.Type2 = &metadata
	call("/api/database", map[string]any{"op": "import-read", "database": db, "read_result": bad}, 422)
	bad = r
	bad.Data = append(chameleon.HexBytes(nil), r.Data...)
	bad.Data[43*4] = 1
	call("/api/database", map[string]any{"op": "import-read", "database": db, "read_result": bad}, 422)
	raw := call("/api/features/check", map[string]any{"id": "library.promote-type2", "card": source, "type2_promotion": map[string]any{"emulator_password": "01020304", "emulator_pack": "0506", "confirm_emulator_credentials": false}}, 200)
	if !bytes.Contains(raw, []byte(`"state":"unavailable"`)) {
		t.Fatal(string(raw))
	}
}
