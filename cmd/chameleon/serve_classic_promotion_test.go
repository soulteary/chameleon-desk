package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	chameleon "github.com/soulteary/chameleon-desk"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func webPromotionResult() chameleon.ReadResult {
	data := make([]byte, 320)
	copy(data, []byte{1, 2, 3, 4, 4, 9, 4, 0})
	for i := 3; i < 20; i += 4 {
		copy(data[i*16+6:], []byte{255, 7, 128})
	}
	hash := sha256.Sum256(data)
	sak := byte(9)
	ats := chameleon.HexBytes{}
	return chameleon.ReadResult{Schema: 1, Kind: "classic", Complete: true, Requested: 20, Read: 20, UID: chameleon.HexBytes{1, 2, 3, 4}, Identity: &chameleon.ClassicIdentity{Schema: 1, UID: chameleon.HexBytes{1, 2, 3, 4}, ATQA: chameleon.HexBytes{0, 4}, SAK: &sak, ATS: &ats}, Dump: &chameleon.ClassicCardDump{Schema: 2, UID: chameleon.HexBytes{1, 2, 3, 4}, TagType: 1000, Data: data, SHA256: hex.EncodeToString(hash[:])}}
}
func TestWebClassicPromotionIsOfflineAppendOnlyAndValidated(t *testing.T) {
	ws := &webServer{allowWrites: false, openDevice: func(context.Context, string, time.Duration) (*chameleon.Client, error) {
		t.Error("conversion opened device")
		return nil, io.EOF
	}}
	h := offlineWeb(t, ws)
	call := func(path string, payload any, want int) []byte {
		t.Helper()
		b, _ := json.Marshal(payload)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "http://127.0.0.1"+path, strings.NewReader(string(b))))
		if w.Code != want {
			t.Fatalf("%s %d %s", path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	source, err := chameleon.NewGUICardFromRead("source", webPromotionResult(), "")
	if err != nil {
		t.Fatal(err)
	}
	db, _ := chameleon.ParseGUIDatabase([]byte(`{"flutter.cards":[],"custom":42}`))
	db, err = db.AddCard(source)
	if err != nil {
		t.Fatal(err)
	}
	raw := call("/api/features/check", map[string]any{"id": "library.promote-classic", "card": source}, 200)
	if !strings.Contains(string(raw), `"state":"available"`) {
		t.Fatal(string(raw))
	}
	raw = call("/api/database", map[string]any{"op": "promote-classic", "database": db, "id": source.ID}, 200)
	var out struct {
		Database chameleon.GUIDatabase `json:"database"`
		Card     chameleon.GUICard     `json:"card"`
	}
	var wire struct {
		Database json.RawMessage   `json:"database"`
		Card     chameleon.GUICard `json:"card"`
	}
	if err = json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	out.Card = wire.Card
	out.Database, err = chameleon.ParseGUIDatabase(wire.Database)
	if err != nil {
		t.Fatal(err)
	}
	if out.Card.ID == source.ID || out.Card.Tag != 1000 || out.Card.SAK != 9 || out.Card.ATQA.String() != "0004" {
		t.Fatal(string(raw))
	}
	records, _ := out.Database.Records(false, "", "all")
	if len(records) != 2 || !strings.Contains(string(wire.Database), `"custom":42`) {
		t.Fatal(string(raw))
	}
	original, _ := out.Database.Card(source.ID)
	before, _ := json.Marshal(source)
	after, _ := json.Marshal(original)
	if string(before) != string(after) {
		t.Fatal("source changed")
	}
	call("/api/database", map[string]any{"op": "validate", "database": out.Database}, 200)
	call("/api/database", map[string]any{"op": "promote-classic", "database": out.Database, "id": out.Card.ID}, 422)
	call("/api/database", map[string]any{"op": "promote-classic", "database": out.Database, "id": "missing"}, 404)
	for _, kind := range []string{"missing identity", "unknown key", "partial", "cleanup"} {
		t.Run(kind, func(t *testing.T) {
			r := webPromotionResult()
			readError := ""
			switch kind {
			case "missing identity":
				r.Identity = nil
			case "unknown key":
				r.Dump.UnknownKeys = []chameleon.ClassicUnknownKey{{Sector: 0, Type: chameleon.KeyB}}
			case "partial":
				r.Complete = false
			case "cleanup":
				r.Complete = false
				readError = "mode restore failed"
			}
			c, e := chameleon.NewGUICardFromRead(kind, r, readError)
			if e != nil {
				t.Fatal(e)
			}
			d, e := db.AddCard(c)
			if e != nil {
				t.Fatal(e)
			}
			call("/api/database", map[string]any{"op": "promote-classic", "database": d, "id": c.ID}, 422)
			raw := call("/api/features/check", map[string]any{"id": "library.promote-classic", "card": c}, 200)
			if !strings.Contains(string(raw), `"state":"unavailable"`) {
				t.Fatal(string(raw))
			}
		})
	}
	// The promoted file's slot-plan preview still checks command/model/write policy.
	raw = call("/api/features/check", map[string]any{"id": "slot.load-card", "card": out.Card, "slot": 1, "device": map[string]any{"model_id": 0, "capabilities": []int{}}}, 200)
	if !strings.Contains(string(raw), "writes_disabled") {
		t.Fatal(fmt.Sprint(string(raw)))
	}
}
