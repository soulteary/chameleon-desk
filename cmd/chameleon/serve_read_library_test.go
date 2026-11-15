package main

import (
	"context"
	"encoding/json"
	chameleon "github.com/soulteary/chameleon-desk"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestReadLibraryOfflineImportEditAndRestrictions(t *testing.T) {
	ws := &webServer{openDevice: func(context.Context, string, time.Duration) (*chameleon.Client, error) {
		t.Error("offline library opened hardware")
		return nil, io.EOF
	}}
	h := offlineWeb(t, ws)
	call := func(path string, payload any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(payload)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "http://127.0.0.1"+path, strings.NewReader(string(raw))))
		if w.Code != want {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	r := chameleon.ReadResult{Schema: 1, Kind: "type2", Start: 3, Requested: 2, Read: 1, UID: chameleon.HexBytes{1, 2, 3, 4}, Data: chameleon.HexBytes{1, 2, 3, 4}}
	raw := call("/api/database", map[string]any{"op": "import-read", "database": map[string]any{"flutter.cards": []string{}, "custom": 42}, "read_result": r, "read_error": "cancelled", "name": "range"}, 200)
	var out struct {
		Database json.RawMessage   `json:"database"`
		Card     chameleon.GUICard `json:"card"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	draft, _ := out.Card.ReadDraft()
	rows := draft.Rows
	rows[3] = "AA020304"
	raw = call("/api/database", map[string]any{"op": "edit-data", "database": out.Database, "id": out.Card.ID, "name": "edited", "rows": rows}, 200)
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.Card.Name != "edited" || !strings.Contains(string(out.Database), `"custom":42`) {
		t.Fatal(string(raw))
	}
	call("/api/database", map[string]any{"op": "validate", "database": out.Database}, 200)
	call("/api/database", map[string]any{"op": "show", "database": out.Database, "id": out.Card.ID}, 200)
	rows[0] = "00000000"
	call("/api/database", map[string]any{"op": "edit-data", "database": out.Database, "id": out.Card.ID, "rows": rows}, 422)
	call("/api/database", map[string]any{"op": "import-read", "database": out.Database}, 400)
	r.Read = 0
	r.Data = nil
	call("/api/database", map[string]any{"op": "import-read", "database": out.Database, "read_result": r}, 422)
	raw = call("/api/features/check", map[string]any{"id": "slot.load-card", "slot": 1, "card": out.Card, "device": map[string]any{"model_id": 0, "capabilities": []int{1000}, "transport": map[string]any{"medium": "usb", "connection": "tcp"}}}, 200)
	if !strings.Contains(string(raw), "Desk read drafts") {
		t.Fatal(string(raw))
	}
	call("/api/ndef/write", map[string]any{"card": out.Card, "text": "hello", "language": "en"}, 422)
	call("/api/ndef/read", out.Card, 422)
}
