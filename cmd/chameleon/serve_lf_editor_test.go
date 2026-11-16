package main

import (
	"bytes"
	"encoding/json"
	"testing"

	chameleon "github.com/soulteary/chameleon-desk"
)

func webLFEditorDatabase(t *testing.T, tag uint16, uid string) json.RawMessage {
	t.Helper()
	cardRaw, _ := json.Marshal(map[string]any{"id": "saved", "name": "original", "uid": uid, "tag": tag, "data": [][]int{}, "atqa": []int{}, "ats": []int{}, "sak": 0, "custom_card": true})
	card, err := chameleon.ParseGUICard(cardRaw)
	if err != nil {
		t.Fatal(err)
	}
	db, err := chameleon.ParseGUIDatabase([]byte(`{"custom_database":42}`))
	if err != nil {
		t.Fatal(err)
	}
	db, err = db.AddCard(card)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(db)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func TestWebLFStructuredOfflineEditPreservesDatabaseAndRefusesIOProx(t *testing.T) {
	ws, h := configFixture(t, nil)
	ws.allowWrites = false
	db := webLFEditorDatabase(t, 150, "3132333435363738")
	body, _ := json.Marshal(map[string]any{"op": "edit-lf", "database": db, "id": "saved", "name": "edited", "lf_identity": chameleon.LFIdentityInput{Protocol: "pac", TagType: 150, PAC: "ABCDEFGH"}})
	w := configCall(ws, h, "/api/database", string(body))
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"custom_database":42`)) || !bytes.Contains(w.Body.Bytes(), []byte(`"custom_card":true`)) {
		t.Fatal(w.Code, w.Body.String())
	}
	var out struct {
		Card chameleon.GUICard `json:"card"`
	}
	json.Unmarshal(w.Body.Bytes(), &out)
	if out.Card.UID != "4142434445464748" || out.Card.Tag != 150 || out.Card.ID != "saved" {
		t.Fatal(out)
	}
	encoded := "01531234007854E03A5D65AB00000000"
	db = webLFEditorDatabase(t, 201, encoded)
	body, _ = json.Marshal(map[string]any{"op": "edit-lf", "database": db, "id": "saved", "name": "invalid", "lf_identity": chameleon.LFIdentityInput{Protocol: "ioprox", TagType: 201, ID: chameleon.HexBytes{1, 83, 0x12, 0x34, 0, 0x78, 0x54, 0xe0, 0x3a, 0x5d, 0x65, 0xab, 0, 0, 0, 0}}})
	w = configCall(ws, h, "/api/database", string(body))
	if w.Code != 422 || ws.sessionClient.Stats().Requests != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestWebLFStructuredIOProxEditUsesFirmwareWithWritesDisabledAndNoReplay(t *testing.T) {
	encoded := chameleon.HexBytes{1, 83, 0x12, 0x34, 0, 0x78, 0x54, 0xe0, 0x3a, 0x5d, 0x65, 0xab, 0, 0, 0, 0}
	steps := []step{{cmd: 1033, status: 0x68, data: []byte{1}}, {cmd: 1035, status: 0x68, data: configCaps(3012, 3013)}, {cmd: 3012, status: 0x68, payload: encoded[4:12], data: encoded}}
	ws, h := configFixture(t, steps)
	ws.allowWrites = false
	body, _ := json.Marshal(map[string]any{"database": webLFEditorDatabase(t, 201, encoded.String()), "id": "saved", "name": "validated", "identity": chameleon.LFIdentityInput{Protocol: "ioprox", TagType: 201, ID: encoded}})
	task := submitTask(t, ws, h, "/api/reader/lf-edit", string(body), "lf-structured-file-edit-001")
	out := waitTask(t, ws, task.ID, func(v webTask) bool { return taskTerminal(&v) })
	if out.State != "succeeded" || out.Write || !bytes.Contains(out.Result, []byte(`"name":"validated"`)) || !bytes.Contains(out.Result, []byte(`"custom_database":42`)) {
		t.Fatal(out.State, string(out.Result))
	}
	requests := ws.sessionClient.Stats().Requests
	repeated := submitTask(t, ws, h, "/api/reader/lf-edit", string(body), "lf-structured-file-edit-001")
	if repeated.ID != task.ID || ws.sessionClient.Stats().Requests != requests {
		t.Fatal("firmware codec task was replayed")
	}
}
