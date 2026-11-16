package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	chameleon "github.com/soulteary/chameleon-desk"
)

func TestLibraryManagerPreviewImportAndAtomicChangesStayOffline(t *testing.T) {
	ws := &webServer{openDevice: func(context.Context, string, time.Duration) (*chameleon.Client, error) {
		t.Error("offline operation opened a device")
		return nil, io.EOF
	}}
	h := offlineWeb(t, ws)
	call := func(payload any, status int) []byte {
		t.Helper()
		raw, _ := json.Marshal(payload)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "http://127.0.0.1/api/library/manage", bytes.NewReader(raw)))
		if w.Code != status {
			t.Fatal(w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	db := json.RawMessage(`{"flutter.cards":[],"custom":42}`)
	raw := call(map[string]any{"op": "change", "database": db, "change": map[string]any{"action": "folder-create", "name": "Cards"}}, 200)
	var out struct {
		Database json.RawMessage `json:"database"`
	}
	json.Unmarshal(raw, &out)
	db = out.Database
	if !bytes.Contains(db, []byte(`"custom":42`)) {
		t.Fatal(string(db))
	}
	text := "+Sector: 0\n01020304000000000000000000000000\n--------------------------------\n"
	raw = call(map[string]any{"op": "file-preview", "format": "auto", "name": "test.mct", "text": text}, 200)
	var preview chameleon.CardFileObservation
	if err := json.Unmarshal(raw, &preview); err != nil {
		t.Fatal(err)
	}
	call(map[string]any{"op": "file-import", "database": db, "format": "auto", "name": "test.mct", "text": text, "sha256": "stale"}, 409)
	raw = call(map[string]any{"op": "file-import", "database": db, "format": "auto", "name": "test.mct", "text": text, "sha256": preview.SHA256}, 200)
	var imported struct {
		Database json.RawMessage   `json:"database"`
		Card     chameleon.GUICard `json:"card"`
	}
	if err := json.Unmarshal(raw, &imported); err != nil {
		t.Fatal(err)
	}
	draft, err := imported.Card.ReadDraft()
	if err != nil || draft.Result.File.Raw != text || draft.Rows[1] != strings.Repeat("??", 16) {
		t.Fatal(draft, err)
	}
	call(map[string]any{"op": "change", "database": imported.Database, "change": map[string]any{"action": "move", "ids": []string{imported.Card.ID}, "folder_id": "missing"}}, 422)
	call(map[string]any{"op": "change", "database": imported.Database, "change": map[string]any{"action": "move", "ids": []string{imported.Card.ID}, "folder_id": "", "assume_complete": true}}, 400)
}

func TestType2BatchPlanBindsVersionAndRejectsChangedConfirmation(t *testing.T) {
	ws, h := configFixture(t, webType2PlanSteps())
	ws.allowWrites = false
	input := chameleon.Type2BatchWrite{UID: webType2WriteInput().UID, Pages: []chameleon.Type2PageChange{{Page: 4, Expected: chameleon.HexBytes{1, 2, 3, 4}, Data: chameleon.HexBytes{4, 3, 2, 1}}}}
	raw, _ := json.Marshal(map[string]any{"request": input})
	w := taskCall(h, ws.token, "POST", "/api/reader/type2-batch-plan", string(raw))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var out struct {
		Plan         chameleon.Type2BatchPlan `json:"plan"`
		Confirmation string                   `json:"confirmation"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	input.ExpectedVersion = out.Plan.Version
	if err := ws.verifyPhysicalConfirmation("type2-batch", input, out.Confirmation); err != nil {
		t.Fatal(err)
	}
	input.ExpectedVersion = nil
	if err := ws.verifyPhysicalConfirmation("type2-batch", input, out.Confirmation); err == nil {
		t.Fatal("plan version was not bound")
	}
	w = taskCall(h, ws.token, "POST", "/api/reader/type2-batch-write", string(raw))
	if w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestBatchWriteTaskBoundaryDedupAndUnknownOutcome(t *testing.T) {
	steps := webType2PlanSteps()
	repeat := webType2PlanSteps()
	steps = append(steps, repeat[3:len(repeat)-1]...)
	steps = append(steps, webType2Raw([]byte{0xa2, 4, 4, 3, 2, 1}, 0xf8, []byte{0}), step{cmd: 2101, status: 0x68})
	ws, h := configFixture(t, steps)
	input := chameleon.Type2BatchWrite{UID: webType2WriteInput().UID, ExpectedVersion: chameleon.HexBytes{0, 4, 4, 2, 1, 0, 0x0f, 3}, Pages: []chameleon.Type2PageChange{{Page: 4, Expected: chameleon.HexBytes{1, 2, 3, 4}, Data: chameleon.HexBytes{4, 3, 2, 1}}}}
	token, err := ws.physicalConfirmation("type2-batch", input)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"request": input, "confirmation": token, "confirm": true})
	task := submitTask(t, ws, h, "/api/reader/type2-batch-write", string(raw), "r2-batch-write-001")
	finished := waitTask(t, ws, task.ID, func(v webTask) bool { return taskTerminal(&v) })
	if finished.State != "failed" || finished.Outcome != "write_outcome_unknown" || finished.WriteAttempted == nil || !*finished.WriteAttempted {
		t.Fatal(finished)
	}
	var envelope struct {
		Result chameleon.Type2BatchWriteResult `json:"result"`
	}
	json.Unmarshal(finished.Result, &envelope)
	if envelope.Result.Complete || envelope.Result.Pages[0].Outcome != "write_outcome_unknown" {
		t.Fatal(string(finished.Result))
	}
	before := ws.sessionClient.Stats().Requests
	duplicate := submitTask(t, ws, h, "/api/reader/type2-batch-write", string(raw), "r2-batch-write-001")
	if duplicate.ID != task.ID || ws.sessionClient.Stats().Requests != before {
		t.Fatal("write replayed")
	}
}

func TestNewDeviceRoutesEnforceMethodTokenSessionAndInputsBeforeIO(t *testing.T) {
	ws, h := configFixture(t, nil)
	paths := []string{"/api/reader/type2-batch-plan", "/api/reader/type2-batch-write", "/api/reader/value-plan", "/api/reader/value-write", "/api/reader/magic-inspect", "/api/slot/emulator/read", "/api/slot/emulator/configure", "/api/slot/emulator/logs", "/api/device/mode"}
	for _, path := range paths {
		if w := taskCall(h, ws.token, "GET", path, ""); w.Code != 405 {
			t.Fatal(path, w.Code)
		}
		if w := taskCall(h, "", "POST", path, `{}`); w.Code != 403 {
			t.Fatal(path, w.Code)
		}
		r := httptest.NewRequest("POST", "http://127.0.0.1"+path, strings.NewReader(`{}`))
		r.Header.Set("X-Chameleon-Token", ws.token)
		r.Header.Set("X-Chameleon-Session", "old")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 409 {
			t.Fatal(path, w.Code)
		}
		if w := taskCall(h, ws.token, "POST", path, `{"unsupported":true}`); w.Code < 400 {
			t.Fatal(path, w.Code)
		}
	}
	if ws.sessionClient.Stats().Requests != 0 {
		t.Fatal("invalid request reached device")
	}
}

func TestEveryNewParameterizedFeatureHasAnHTTPPreview(t *testing.T) {
	ws := &webServer{}
	h := offlineWeb(t, ws)
	cases := map[string]map[string]any{
		"device.mode":              {"mode_change": map[string]any{"expected": 0, "mode": 1}},
		"reader.type2-batch-plan":  {"type2_batch": map[string]any{"uid": "04112233445566", "pages": []any{}}},
		"reader.type2-batch-write": {"type2_batch": map[string]any{"uid": "04112233445566", "pages": []any{}}},
		"reader.value-plan":        {"value": map[string]any{}}, "reader.value-write": {"value": map[string]any{}},
		"reader.magic-inspect": {"magic": map[string]any{}}, "reader.config-update": {"reader_config": map[string]any{}},
		"slot.emulator-read": {"current_emulator": map[string]any{}}, "slot.emulator-configure": {"current_emulator": map[string]any{}}, "slot.emulator-logs": {"current_emulator": map[string]any{}},
	}
	for id, body := range cases {
		body["id"] = id
		raw, _ := json.Marshal(body)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "http://127.0.0.1/api/features/check", bytes.NewReader(raw)))
		if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"id":"`+id+`"`)) {
			t.Fatal(id, w.Code, w.Body.String())
		}
	}
}
