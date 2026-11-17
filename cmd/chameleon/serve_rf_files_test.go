package main

import (
	"context"
	"encoding/json"
	"fmt"
	chameleon "github.com/soulteary/chameleon-desk"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func offlineRFBody() string {
	return `{"format":"lf-u8","name":"source.bin","data":"00FF00FF","start":0,"count":0,"decode":false,"clock":64,"phase":0,"invert":false}`
}
func TestRFFileTasksUseOfflineScopeWithoutDeviceOrWritePermission(t *testing.T) {
	ws := &webServer{sessionEnabled: true, timeout: time.Second}
	ws.rotateSessionID()
	h := offlineWeb(t, ws)
	t.Cleanup(ws.tasks.stop)
	opened := false
	ws.openDevice = func(context.Context, string, time.Duration) (*chameleon.Client, error) {
		opened = true
		return nil, fmt.Errorf("must not open")
	}
	// Device work may own its gate throughout file analysis.
	ws.gateOnce.Do(func() { ws.gate = make(chan struct{}, 1) })
	ws.gate <- struct{}{}
	defer func() { <-ws.gate }()
	body := `{"operation_id":"files.rf-analyze","request_id":"offline-file-analysis-0001","body":` + offlineRFBody() + `}`
	w := taskCall(h, ws.token, "POST", "/api/tasks", body)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	var task webTask
	_ = json.Unmarshal(w.Body.Bytes(), &task)
	ws.rotateSessionID()
	done := waitTask(t, ws, task.ID, func(v webTask) bool { return taskTerminal(&v) })
	if done.State != "succeeded" || done.SessionID != "offline" || done.Write || done.Commands != 0 || opened {
		t.Fatal(done)
	}
	var report chameleon.RFFileAnalysis
	if err := json.Unmarshal(done.Result, &report); err != nil || report.LF == nil || report.Source.SHA256 == "" {
		t.Fatal(report, err)
	}
	again := taskCall(h, ws.token, "POST", "/api/tasks", body)
	var repeat webTask
	_ = json.Unmarshal(again.Body.Bytes(), &repeat)
	if again.Code != 200 || repeat.ID != task.ID {
		t.Fatal("receipt replayed", again.Code, repeat)
	}
	for _, tc := range []struct {
		method, path, body, token string
		code                      int
	}{
		{"POST", "/api/files/rf/analyze", offlineRFBody(), "", 403},
		{"GET", "/api/files/rf/analyze", "", ws.token, 405},
		{"POST", "/api/files/rf/analyze?path=/tmp/file", offlineRFBody(), ws.token, 400},
		{"POST", "/api/files/rf/analyze", `{"format":"lf-u8","data":"00","path":"/private/file"}`, ws.token, 400},
		{"POST", "/api/files/rf/analyze", `{"format":"lf-u8","data":"00","decode":true,"clock":64}`, ws.token, 422},
	} {
		reply := taskCall(h, tc.token, tc.method, tc.path, tc.body)
		if reply.Code != tc.code {
			t.Fatal(tc, reply.Code, reply.Body.String())
		}
	}
	if opened {
		t.Fatal("file route reached device")
	}
}
func TestOfflineRFTaskCancellationAndRestartKeepReceipts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.jsonl")
	ws := &webServer{sessionEnabled: true}
	ws.rotateSessionID()
	if err := ws.tasks.openJournal(path); err != nil {
		t.Fatal(err)
	}
	h := offlineWeb(t, ws)
	ws.rfOnce.Do(func() { ws.rfGate = make(chan struct{}, 2) })
	ws.rfGate <- struct{}{}
	ws.rfGate <- struct{}{}
	body := `{"operation_id":"files.rf-analyze","request_id":"offline-cancel-attempt-0001","body":` + offlineRFBody() + `}`
	w := taskCall(h, ws.token, "POST", "/api/tasks", body)
	var queued webTask
	_ = json.Unmarshal(w.Body.Bytes(), &queued)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	cancel := taskCall(h, ws.token, "POST", "/api/tasks/"+queued.ID+"/cancel", "{}")
	if cancel.Code != 200 {
		t.Fatal(cancel.Code)
	}
	done := waitTask(t, ws, queued.ID, func(v webTask) bool { return taskTerminal(&v) })
	if done.State != "canceled" || done.Outcome != "not_started" {
		t.Fatal(done)
	}
	<-ws.rfGate
	<-ws.rfGate
	body = `{"operation_id":"files.rf-analyze","request_id":"offline-success-attempt-001","body":` + offlineRFBody() + `}`
	w = taskCall(h, ws.token, "POST", "/api/tasks", body)
	var completed webTask
	_ = json.Unmarshal(w.Body.Bytes(), &completed)
	waitTask(t, ws, completed.ID, func(v webTask) bool { return taskTerminal(&v) })
	ws.tasks.stop()
	next := &webServer{sessionEnabled: true}
	next.rotateSessionID()
	if err := next.tasks.openJournal(path); err != nil {
		t.Fatal(err)
	}
	defer next.tasks.stop()
	h = offlineWeb(t, next)
	w = taskCall(h, next.token, "POST", "/api/tasks", body)
	var recovered webTask
	_ = json.Unmarshal(w.Body.Bytes(), &recovered)
	if w.Code != 200 || recovered.ID != completed.ID || !recovered.Recovered || recovered.ResultRetained || len(recovered.Result) != 0 {
		t.Fatal("restart replayed/lost receipt", w.Code, recovered)
	}
	if entryStarted(chameleon.TaskSnapshot{Phase: "offline-analysis"}) != true || !validJournalPhase("offline-analysis") {
		t.Fatal("offline durable phase unavailable")
	}
}
func TestOfflineRFTaskClientNeedsNoSessionAndPrunesOnlyOfflineBodies(t *testing.T) {
	ws := &webServer{sessionEnabled: true}
	ws.rotateSessionID()
	h := offlineWeb(t, ws)
	server := httptest.NewServer(h)
	defer server.Close()
	defer ws.tasks.stop()
	client, err := chameleon.NewTaskClient(server.URL, ws.token)
	if err != nil {
		t.Fatal(err)
	}
	task, err := client.Submit(context.Background(), chameleon.TaskSubmission{RequestID: "sdk-offline-attempt-0001", OperationID: "files.rf-analyze", Body: json.RawMessage(offlineRFBody())})
	if err != nil {
		t.Fatal(err)
	}
	final, err := client.Wait(context.Background(), task.ID, time.Millisecond)
	if err != nil || final.State != "succeeded" || final.SessionID != "offline" {
		t.Fatal(final, err)
	}
	if _, err = client.Submit(context.Background(), chameleon.TaskSubmission{RequestID: "sdk-device-session-0001", SessionID: "stale-device", OperationID: "files.rf-analyze", Body: json.RawMessage(offlineRFBody())}); err == nil {
		t.Fatal("offline task accepted a device binding")
	}
	ts := new(webTasks)
	ts.init()
	for i, path := range []string{"/api/files/rf/analyze", "/api/slot/activate", "/api/files/rf/analyze"} {
		id := fmt.Sprint(i)
		ts.order = append(ts.order, id)
		ts.records[id] = &webTask{TaskSnapshot: chameleon.TaskSnapshot{ID: id, Path: path, State: "succeeded", Result: json.RawMessage(strings.Repeat("x", 8)), ResultRetained: true}}
	}
	ts.pruneOfflineResultsLocked(10)
	if ts.records["0"].ResultRetained || !ts.records["1"].ResultRetained || !ts.records["2"].ResultRetained || len(ts.records) != 3 {
		t.Fatal("wrong result retention", ts.records)
	}
}
