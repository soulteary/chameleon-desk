package chameleon

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func testReadResult() ReadResult {
	return ReadResult{Schema: 1, Kind: "type2", Start: 5, Requested: 4, Read: 2, UID: HexBytes{1, 2, 3, 4}, Data: HexBytes{1, 2, 3, 4, 5, 6, 7, 8}}
}

func TestReadDraftPreservesOffsetsSourceAndEdits(t *testing.T) {
	r := testReadResult()
	c, err := NewGUICardFromRead("range", r, "interrupted")
	if err != nil {
		t.Fatal(err)
	}
	draft, _ := c.ReadDraft()
	if len(draft.Rows) != 9 || draft.Rows[4] != "????????" || draft.Rows[5] != "01020304" || draft.Rows[7] != "????????" {
		t.Fatal(draft)
	}
	var extension map[string]json.RawMessage
	json.Unmarshal(c.fields["desk_read"], &extension)
	extension["custom"] = json.RawMessage(`{"keep":"extension"}`)
	c = c.withField("desk_read", extension)
	rows := append([]string(nil), draft.Rows...)
	rows[5] = "AA 02 03 04"
	c, err = c.EditData("edited", rows)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	back, err := ParseGUICard(raw)
	if err != nil {
		t.Fatal(err)
	}
	edited, _ := back.ReadDraft()
	if edited.Rows[5] != "AA020304" || !bytes.Equal(edited.Result.Data, r.Data) || edited.Error != "interrupted" || back.Name != "edited" || !bytes.Contains(raw, []byte(`"keep":"extension"`)) {
		t.Fatal(edited)
	}
	rows[7] = "00000000"
	if _, err = back.EditData("fill", rows); err == nil {
		t.Fatal("unknown page filled")
	}
	if _, err = back.Dump(); err == nil {
		t.Fatal("binary export permitted")
	}
	if _, err = back.EmulatorImage(); err == nil {
		t.Fatal("image export permitted")
	}
	if _, err = back.WithImage(EmulatorImage{}); err == nil {
		t.Fatal("image replacement permitted")
	}
	if _, err = PlanGUICardLoad(back, 1); err == nil {
		t.Fatal("slot load permitted")
	}
	db, _ := ParseGUIDatabase([]byte(`{"flutter.cards":[],"custom":{"keep":true}}`))
	db, err = db.AddCard(back)
	if err != nil {
		t.Fatal(err)
	}
	records, err := db.Records(false, "", "hf")
	if err != nil || len(records) != 1 || records[0].Units != 9 || !strings.Contains(records[0].TagName, "read draft") {
		t.Fatal(records, err)
	}
	dbRaw, _ := json.Marshal(db)
	if !bytes.Contains(dbRaw, []byte(`"keep":true`)) {
		t.Fatal(string(dbRaw))
	}
}

func TestClassicDraftMasksUnknownKeysAndUnreturnedRows(t *testing.T) {
	data := bytes.Repeat([]byte{0x42}, 64)
	r := ReadResult{Schema: 1, Kind: "classic", Requested: 20, Read: 4, UID: HexBytes{1, 2, 3, 4}, Dump: &ClassicCardDump{Schema: 2, UID: HexBytes{1, 2, 3, 4}, TagType: 1000, Data: data, SHA256: imageDigest(data), UnknownKeys: []ClassicUnknownKey{{0, KeyB}, {1, KeyA}}}}
	c, err := NewGUICardFromRead("classic", r, "cancelled")
	if err != nil {
		t.Fatal(err)
	}
	draft, _ := c.ReadDraft()
	if draft.Rows[3] != "42424242424242424242????????????" || draft.Rows[4] != strings.Repeat("??", 16) {
		t.Fatal(draft.Rows)
	}
	rows := append([]string(nil), draft.Rows...)
	rows[3] = "41424242424242424242????????????"
	c, err = c.EditData("known byte edit", rows)
	if err != nil {
		t.Fatal(err)
	}
	rows[3] = strings.Repeat("00", 16)
	if _, err = c.EditData("invent key", rows); err == nil {
		t.Fatal("unknown key overwritten")
	}
	r.Dump.SHA256 = "wrong"
	if _, err = NewGUICardFromRead("bad", r, ""); err == nil {
		t.Fatal("bad checksum accepted")
	}
}

func TestReadDraftValidationRejectsMalformedAndEmptyObservations(t *testing.T) {
	good := testReadResult()
	for _, change := range []func(*ReadResult){
		func(r *ReadResult) { r.Read = 0; r.Data = nil }, func(r *ReadResult) { r.Complete = true }, func(r *ReadResult) { r.Start = 254 }, func(r *ReadResult) { r.UID = nil }, func(r *ReadResult) { r.Data = r.Data[:1] }, func(r *ReadResult) { r.Schema = 2 },
	} {
		r := good
		change(&r)
		if _, err := NewGUICardFromRead("bad", r, ""); err == nil {
			t.Fatal(r)
		}
	}
	c, _ := NewGUICardFromRead("draft", good, "")
	c.Tag = 1100
	if c.Validate() == nil {
		t.Fatal("draft promoted by changing tag")
	}
	c.Tag = 0
	c = c.withField("desk_read", nil)
	if c.Validate() != nil {
		t.Fatal("normal card compatibility")
	}
	c = c.withField("desk_read", map[string]any{"schema": 99})
	if c.Validate() == nil {
		t.Fatal("invalid extension ignored")
	}
}

func TestLFDraftEditsValidateSelectedCodec(t *testing.T) {
	yes := true
	record, _ := DecodeLFRecord("pac", 150, []byte("12345678"))
	r := ReadResult{Schema: 1, Kind: "lf", Requested: 1, Read: 1, Complete: true, Present: &yes, Record: &record}
	c, err := NewGUICardFromRead("PAC", r, "")
	if err != nil {
		t.Fatal(err)
	}
	c, err = c.EditData("edited", []string{"4142434431323334"})
	if err != nil {
		t.Fatal(err)
	}
	draft, _ := c.ReadDraft()
	if string(draft.Result.Record.ID) != "12345678" || draft.Rows[0] != "4142434431323334" {
		t.Fatal(draft)
	}
	db, _ := ParseGUIDatabase([]byte(`{}`))
	db, err = db.AddCard(c)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := db.Records(false, "414243", "lf")
	if err != nil || len(summary) != 1 || summary[0].UID != "4142434431323334" {
		t.Fatal(summary, err)
	}
	if _, err = c.EditData("invalid", []string{strings.Repeat("00", 8)}); err == nil {
		t.Fatal("invalid PAC draft")
	}
	yes = false
	if _, err = NewGUICardFromRead("absent", r, ""); err == nil {
		t.Fatal("absent read imported")
	}
}

func TestOrdinaryCardDataEditingPreservesMetadataAndEmptyRows(t *testing.T) {
	c, err := ParseGUICard([]byte(`{"id":"c","name":"C","uid":"01020304","tag":1100,"atqa":[0,68],"sak":0,"ats":[],"data":[[1,2,3,4],[]],"extra":{"vendor":"keep"},"custom":42}`))
	if err != nil {
		t.Fatal(err)
	}
	next, err := c.EditData("new", []string{"AA 02 03 04", ""})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(next)
	if !bytes.Contains(raw, []byte(`"custom":42`)) || !bytes.Contains(raw, []byte(`"vendor":"keep"`)) || next.Data[0][0] != 0xAA || c.Data[0][0] != 1 {
		t.Fatal(string(raw))
	}
	for _, rows := range [][]string{{"00", ""}, {"AA020304"}, {"AA020304", "00000000"}} {
		if _, err = c.EditData("bad", rows); err == nil {
			t.Fatal(rows)
		}
	}
}
