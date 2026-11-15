package main

import (
	"context"
	"encoding/json"
	"fmt"
	chameleon "github.com/soulteary/chameleon-desk"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func taskCall(h http.Handler, token, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://127.0.0.1"+path, strings.NewReader(body))
	r.Header.Set("X-Chameleon-Token", token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func waitTask(t *testing.T, ws *webServer, id string, ready func(webTask) bool) webTask {
	t.Helper()
	until := time.Now().Add(3 * time.Second)
	for time.Now().Before(until) {
		ws.tasks.mu.Lock()
		v := *ws.tasks.records[id]
		ws.tasks.mu.Unlock()
		if ready(v) {
			return v
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("task did not reach expected state")
	return webTask{}
}
func submitTask(t *testing.T, ws *webServer, h http.Handler, path, body, key string) webTask {
	t.Helper()
	input := map[string]any{"request_id": key, "session_id": ws.currentSessionID(), "path": path}
	if body != "" {
		input["body"] = json.RawMessage(body)
	}
	b, _ := json.Marshal(input)
	w := taskCall(h, ws.token, "POST", "/api/tasks", string(b))
	if w.Code != 202 && w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var v webTask
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return v
}
func TestBackgroundTaskSurvivesRequestAndDeduplicates(t *testing.T) {
	ws := &webServer{sessionEnabled: true, timeout: time.Second}
	ws.rotateSessionID()
	c, err := mockDevice(t, []step{{cmd: 1033, status: 0x68, data: []byte{0}}, {cmd: 1035, status: 0x68, data: []byte{3, 235}}})("mock", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ws.sessionClient = c
	t.Cleanup(ws.closeSession)
	t.Cleanup(ws.tasks.stop)
	h := offlineWeb(t, ws)
	ws.gateOnce.Do(func() { ws.gate = make(chan struct{}, 1) })
	ws.gate <- struct{}{}
	submitBody := fmt.Sprintf(`{"request_id":"same-request-0001","session_id":%q,"path":"/api/features/device"}`, ws.currentSessionID())
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	request := httptest.NewRequest("POST", "http://127.0.0.1/api/tasks", strings.NewReader(submitBody)).WithContext(requestCtx)
	request.Header.Set("X-Chameleon-Token", ws.token)
	accepted := httptest.NewRecorder()
	h.ServeHTTP(accepted, request)
	cancelRequest()
	if accepted.Code != 202 {
		t.Fatal(accepted.Code, accepted.Body.String())
	}
	var task webTask
	if err := json.Unmarshal(accepted.Body.Bytes(), &task); err != nil {
		t.Fatal(err)
	}
	if w := taskCall(h, "", "GET", "/api/tasks", ""); w.Code != 403 {
		t.Fatal(w.Code)
	}
	again := submitTask(t, ws, h, "/api/features/device", "", "same-request-0001")
	if again.ID != task.ID {
		t.Fatal("duplicate submission created new work")
	}
	body := fmt.Sprintf(`{"request_id":"same-request-0001","session_id":%q,"path":"/api/info"}`, ws.currentSessionID())
	if w := taskCall(h, ws.token, "POST", "/api/tasks", body); w.Code != 409 {
		t.Fatal(w.Code)
	}
	<-ws.gate
	v := waitTask(t, ws, task.ID, func(v webTask) bool { return taskTerminal(&v) })
	if v.State != "succeeded" || v.Commands != 2 || v.Responses != 2 || v.Outcome != "completed" {
		t.Fatalf("%+v", v)
	}
	if w := taskCall(h, ws.token, "GET", "/api/tasks/"+task.ID, ""); w.Code != 200 || !strings.Contains(w.Body.String(), "model_id") {
		t.Fatal(w.Code, w.Body.String())
	}
	// A completed task remains completed after a late cancellation request.
	if w := taskCall(h, ws.token, "POST", "/api/tasks/"+task.ID+"/cancel", "{}"); w.Code != 200 || !strings.Contains(w.Body.String(), `"succeeded"`) {
		t.Fatal(w.Body.String())
	}
}
func TestBackgroundQueuedCancelAndSessionReplacement(t *testing.T) {
	for _, cancel := range []bool{true, false} {
		t.Run(fmt.Sprint(cancel), func(t *testing.T) {
			ws, _ := sessionFixture(t)
			ws.rotateSessionID()
			t.Cleanup(ws.tasks.stop)
			h := offlineWeb(t, ws)
			ws.gateOnce.Do(func() { ws.gate = make(chan struct{}, 1) })
			ws.gate <- struct{}{}
			task := submitTask(t, ws, h, "/api/info", "", "queued-request-01")
			if cancel {
				if w := taskCall(h, ws.token, "POST", "/api/tasks/"+task.ID+"/cancel", "{}"); w.Code != 200 {
					t.Fatal(w.Code)
				}
			} else {
				ws.rotateSessionID()
			}
			<-ws.gate
			v := waitTask(t, ws, task.ID, func(v webTask) bool { return taskTerminal(&v) })
			if v.Outcome != "not_started" || v.Commands != 0 {
				t.Fatalf("%+v", v)
			}
			if cancel && v.State != "canceled" {
				t.Fatal(v.State)
			}
			if !cancel && v.State != "failed" {
				t.Fatal(v.State)
			}
		})
	}
}
func TestBackgroundRunningWriteCancelRemainsUnknown(t *testing.T) {
	ws, _ := sessionFixture(t)
	ws.allowWrites = true
	if err := ws.connectSession(context.Background(), "COM7"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ws.tasks.stop)
	h := offlineWeb(t, ws)
	task := submitTask(t, ws, h, "/api/slot/activate", `{"slot":1}`, "write-request-001")
	running := waitTask(t, ws, task.ID, func(v webTask) bool { return v.Commands > 0 })
	if running.Outcome != "in_progress" {
		t.Fatal(running.Outcome)
	}
	if w := taskCall(h, ws.token, "POST", "/api/tasks/"+task.ID+"/cancel", "{}"); w.Code != 200 {
		t.Fatal(w.Code)
	}
	v := waitTask(t, ws, task.ID, func(v webTask) bool { return taskTerminal(&v) })
	if v.State != "interrupted" || v.Outcome != "write_outcome_unknown" || !v.CancelRequested {
		t.Fatalf("%+v", v)
	}
	if !ws.sessionClient.IsClosed() {
		t.Fatal("uncertain wire connection was reused")
	}
	again := submitTask(t, ws, h, "/api/slot/activate", `{"slot":1}`, "write-request-001")
	if again.ID != task.ID {
		t.Fatal("write replayed")
	}
}
func TestBackgroundValidationAndShutdown(t *testing.T) {
	ws, _ := sessionFixture(t)
	ws.rotateSessionID()
	t.Cleanup(ws.tasks.stop)
	h := offlineWeb(t, ws)
	if w := taskCall(h, ws.token, "POST", "/api/tasks", fmt.Sprintf(`{"request_id":"read-only-write-1","session_id":%q,"path":"/api/slot/activate","body":{"slot":1}}`, ws.currentSessionID())); w.Code != 403 {
		t.Fatal(w.Code)
	}
	ws.allowWrites = true
	task := submitTask(t, ws, h, "/api/slot/load-card", `{"slot":0,"card":{}}`, "invalid-card-0001")
	v := waitTask(t, ws, task.ID, func(v webTask) bool { return taskTerminal(&v) })
	if v.Outcome != "not_started" || v.State != "failed" {
		t.Fatal(v)
	}
	ws.gateOnce.Do(func() { ws.gate = make(chan struct{}, 1) })
	ws.gate <- struct{}{}
	queued := submitTask(t, ws, h, "/api/info", "", "shutdown-task-01")
	ws.tasks.stop()
	<-ws.gate
	v = waitTask(t, ws, queued.ID, func(v webTask) bool { return taskTerminal(&v) })
	if v.State != "canceled" {
		t.Fatal(v.State)
	}
}
func TestBackgroundEvictedReceiptCannotReplay(t *testing.T) {
	ws, _ := sessionFixture(t)
	ws.rotateSessionID()
	h := offlineWeb(t, ws)
	t.Cleanup(ws.tasks.stop)
	task := submitTask(t, ws, h, "/api/info", "", "retained-key-001")
	waitTask(t, ws, task.ID, func(v webTask) bool { return taskTerminal(&v) })
	ws.tasks.mu.Lock()
	delete(ws.tasks.records, task.ID)
	ws.tasks.mu.Unlock()
	body := fmt.Sprintf(`{"request_id":"retained-key-001","session_id":%q,"path":"/api/info"}`, ws.currentSessionID())
	if w := taskCall(h, ws.token, "POST", "/api/tasks", body); w.Code != 410 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestBackgroundPartialLoadRetainsAcknowledgedSteps(t *testing.T) {
	raw := `{"id":"card","name":"LF","tag":100,"uid":"0102030405","sak":0,"atqa":[0,0],"ats":[],"data":[]}`
	card, err := chameleon.ParseGUICard([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := chameleon.PlanGUICardLoad(card, 1)
	if err != nil {
		t.Fatal(err)
	}
	required, err := chameleon.GUICardLoadRequirements(card, 1, chameleon.GUICardLoadOptions{Activate: true})
	if err != nil {
		t.Fatal(err)
	}
	var caps []byte
	for _, cmd := range required {
		caps = append(caps, byte(cmd>>8), byte(cmd))
	}
	steps := []step{{cmd: 1002, status: 0x68, data: []byte{0}}, {cmd: 1035, status: 0x68, data: caps}, {cmd: 1018, status: 0x68, data: []byte{0}}}
	for _, v := range plan.Steps {
		steps = append(steps, step{cmd: uint16(v.Command), payload: v.Payload, status: 0x68})
	}
	steps = append(steps, step{cmd: uint16(chameleon.CmdEm410xGetEmuId), status: 0x68, data: []byte{0, 100, 9, 9, 9, 9, 9}})
	c, err := mockDevice(t, steps)("mock", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ws := &webServer{sessionEnabled: true, allowWrites: true, sessionClient: c, timeout: time.Second}
	ws.rotateSessionID()
	t.Cleanup(ws.closeSession)
	t.Cleanup(ws.tasks.stop)
	h := offlineWeb(t, ws)
	task := submitTask(t, ws, h, "/api/slot/load-card", `{"slot":1,"activate":true,"card":`+raw+`}`, "partial-load-001")
	result := waitTask(t, ws, task.ID, func(v webTask) bool { return taskTerminal(&v) })
	var payload struct {
		Result chameleon.GUICardLoadResult `json:"result"`
	}
	if err := json.Unmarshal(result.Result, &payload); err != nil {
		t.Fatal(err)
	}
	if result.Outcome != "write_outcome_unknown" || payload.Result.CompletedSteps != len(plan.Steps) || payload.Result.Verified {
		t.Fatalf("%+v %s", result, result.Result)
	}
}
