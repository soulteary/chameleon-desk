package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	chameleon "github.com/soulteary/chameleon-desk"
)

func TestTaskJournalCrashHelper(t *testing.T) {
	path := os.Getenv("CHAMELEON_TASK_CRASH_JOURNAL")
	if path == "" {
		return
	}
	ts := new(webTasks)
	if e := ts.openJournal(path); e != nil {
		t.Fatal(e)
	}
	for _, state := range []string{"queued", "running"} {
		for _, write := range []bool{false, true} {
			path, id := "/api/info", "device.info"
			if write {
				path, id = "/api/slot/activate", "slot.activate"
			}
			v := chameleon.TaskSnapshot{ID: newTaskID(), RequestID: "crash-request-" + state + "-" + id, SessionID: "old-session", OperationID: id, Path: path, Write: write, State: state, Phase: "queued", Outcome: "not_started", Created: time.Now().UTC()}
			if state == "running" {
				v.Phase = "device"
				v.Outcome = "in_progress"
			}
			ts.records[v.ID] = &webTask{TaskSnapshot: v}
			ts.order = append(ts.order, v.ID)
			ts.receipts[v.RequestID] = taskReceipt{sha256.Sum256([]byte(v.ID)), v.ID}
			if e := ts.persistLocked(ts.records[v.ID]); e != nil {
				t.Fatal(e)
			}
		}
	}
	// Skip stop(), leaving durable pending tasks as a crashed server would.
	os.Exit(0)
}
func TestTaskJournalRestartsWithoutExecutingPendingWork(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.jsonl")
	child := exec.Command(os.Args[0], "-test.run=^TestTaskJournalCrashHelper$")
	child.Env = append(os.Environ(), "CHAMELEON_TASK_CRASH_JOURNAL="+path)
	if b, e := child.CombinedOutput(); e != nil {
		t.Fatalf("%v %s", e, b)
	}
	ts := new(webTasks)
	if e := ts.openJournal(path); e != nil {
		t.Fatal(e)
	}
	defer ts.stop()
	if len(ts.records) != 4 {
		t.Fatal(len(ts.records))
	}
	for _, v := range ts.records {
		want := "not_started"
		if strings.Contains(v.RequestID, "running") {
			want = "read_interrupted"
			if v.Write {
				want = "write_outcome_unknown"
			}
		}
		if v.State != "interrupted" || v.Outcome != want || !v.Recovered || v.ResultRetained || v.cancel != nil {
			t.Fatalf("%+v", v)
		}
	}
	other := new(webTasks)
	if e := other.openJournal(path); e == nil {
		other.stop()
		t.Fatal("two owners accepted")
	}
}
func TestDurableTaskClientsShareSessionAndDeduplicateAfterRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.jsonl")
	ws := &webServer{sessionEnabled: true, allowWrites: true, timeout: time.Second}
	ws.rotateSessionID()
	if e := ws.tasks.openJournal(path); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(ws.tasks.stop)
	handler := offlineWeb(t, ws)
	srv := httptest.NewServer(handler)
	defer srv.Close()
	client, e := chameleon.NewTaskClient(srv.URL, ws.token)
	if e != nil {
		t.Fatal(e)
	}
	input := chameleon.TaskSubmission{RequestID: "durable-client-001", SessionID: ws.currentSessionID(), OperationID: "slot.configure", Body: json.RawMessage(`{"slot":0,"action":"nickname","name":"PRIVATE-CARD-CONTENT"}`)}
	accepted, e := client.Submit(context.Background(), input)
	if e != nil {
		t.Fatal(e)
	}
	finished, e := client.Wait(context.Background(), accepted.ID, time.Millisecond)
	if e != nil || finished.Outcome != "not_started" {
		t.Fatal(finished, e)
	}
	data, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(data), "PRIVATE-CARD-CONTENT") || strings.Contains(string(data), `"body"`) || strings.Contains(string(data), `"result"`) {
		t.Fatal("payload leaked to journal")
	}
	storeID := ws.tasks.storeID
	ws.tasks.stop()
	next := &webServer{sessionEnabled: true, allowWrites: true, timeout: time.Second}
	next.rotateSessionID()
	if e := next.tasks.openJournal(path); e != nil {
		t.Fatal(e)
	}
	defer next.tasks.stop()
	nextHandler := offlineWeb(t, next)
	// Preserve old session/input deliberately. Deduplication precedes the new
	// session check, and works for legacy path and stable operation forms alike.
	b, _ := json.Marshal(input)
	duplicate := taskCall(nextHandler, next.token, "POST", "/api/tasks", string(b))
	if duplicate.Code != 200 {
		t.Fatal(duplicate.Code, duplicate.Body.String())
	}
	var recovered chameleon.TaskSnapshot
	if e := json.Unmarshal(duplicate.Body.Bytes(), &recovered); e != nil {
		t.Fatal(e)
	}
	if recovered.ID != accepted.ID || !recovered.Recovered || recovered.ResultRetained || len(recovered.Result) != 0 || next.tasks.storeID != storeID {
		t.Fatal(recovered)
	}
	input.Body = json.RawMessage(`{"slot":1}`)
	b, _ = json.Marshal(input)
	if w := taskCall(nextHandler, next.token, "POST", "/api/tasks", string(b)); w.Code != 409 {
		t.Fatal(w.Code)
	}
	lookup := taskCall(nextHandler, next.token, "GET", "/api/tasks?request_id=durable-client-001", "")
	if lookup.Code != 200 || !strings.Contains(lookup.Body.String(), accepted.ID) {
		t.Fatal(lookup.Body.String())
	}
	next.tasks.mu.Lock()
	delete(next.tasks.records, accepted.ID)
	next.tasks.mu.Unlock()
	if w := taskCall(nextHandler, next.token, "GET", "/api/tasks?request_id=durable-client-001", ""); w.Code != 410 {
		t.Fatal(w.Code)
	}
}
func TestTaskJournalFailureBlocksAdmissionAndDeviceCallback(t *testing.T) {
	for _, phase := range []string{"admission", "start"} {
		t.Run(phase, func(t *testing.T) {
			ws, opens := sessionFixture(t)
			ws.rotateSessionID()
			path := filepath.Join(t.TempDir(), "tasks.jsonl")
			if e := ws.tasks.openJournal(path); e != nil {
				t.Fatal(e)
			}
			defer ws.tasks.stop()
			h := offlineWeb(t, ws)
			if phase == "start" {
				if e := ws.connectSession(context.Background(), "COM7"); e != nil {
					t.Fatal(e)
				}
				ws.gateOnce.Do(func() { ws.gate = make(chan struct{}, 1) })
				ws.gate <- struct{}{}
				accepted := submitTask(t, ws, h, "/api/info", "", "failed-start-001")
				if e := ws.tasks.journal.file.Close(); e != nil {
					t.Fatal(e)
				}
				<-ws.gate
				ended := waitTask(t, ws, accepted.ID, func(v webTask) bool { return taskTerminal(&v) })
				if ended.Outcome != "not_started" || ws.sessionClient.Stats().Requests != 0 {
					t.Fatalf("%+v", ended)
				}
			} else {
				if e := ws.tasks.journal.file.Close(); e != nil {
					t.Fatal(e)
				}
				body := `{"request_id":"failed-admission-1","session_id":"` + ws.currentSessionID() + `","operation_id":"device.info"}`
				if w := taskCall(h, ws.token, "POST", "/api/tasks", body); w.Code != 503 {
					t.Fatal(w.Code, w.Body.String())
				}
				if opens.Load() != 0 {
					t.Fatal("opened device after journal failure")
				}
			}
			list := taskCall(h, ws.token, "GET", "/api/tasks", "")
			if !strings.Contains(list.Body.String(), "Task journal failed") {
				t.Fatal(list.Body.String())
			}
		})
	}
}
func TestTaskJournalRejectsCorruptionPermissionsAndReplacement(t *testing.T) {
	for _, kind := range []string{"partial", "corrupt", "permissions", "replacement"} {
		t.Run(kind, func(t *testing.T) {
			if kind == "permissions" && runtime.GOOS == "windows" {
				t.Skip("Windows uses inherited ACLs")
			}
			path := filepath.Join(t.TempDir(), "tasks.jsonl")
			ts := new(webTasks)
			if e := ts.openJournal(path); e != nil {
				t.Fatal(e)
			}
			if kind == "replacement" {
				// Move the locked file and create a replacement. Windows may deny renaming
				// an open file; rejection by the OS also prevents this replacement.
				if e := os.Rename(path, path+".old"); e != nil {
					ts.stop()
					if runtime.GOOS == "windows" {
						return
					}
					t.Fatal(e)
				}
				if e := os.WriteFile(path, []byte("replacement"), 0600); e != nil {
					t.Fatal(e)
				}
				if e := ts.journal.append(struct{}{}); e == nil {
					t.Fatal("replaced journal accepted")
				}
				ts.stop()
				return
			}
			ts.stop()
			switch kind {
			case "permissions":
				if e := os.Chmod(path, 0644); e != nil {
					t.Fatal(e)
				}
			case "corrupt":
				f, e := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
				if e != nil {
					t.Fatal(e)
				}
				_, _ = f.WriteString("broken\n")
				f.Close()
			case "partial":
				b, _ := os.ReadFile(path)
				if e := os.WriteFile(path, b[:len(b)-1], 0600); e != nil {
					t.Fatal(e)
				}
			}
			next := new(webTasks)
			if e := next.openJournal(path); e == nil {
				next.stop()
				t.Fatal("invalid journal accepted")
			}
		})
	}
}
func TestOperationRegistrationAndTaskMethodGuards(t *testing.T) {
	ws := &webServer{}
	h := offlineWeb(t, ws)
	for _, operation := range chameleon.Operations() {
		if ws.operationHandler(operation.Path) == nil {
			t.Fatal(operation.ID)
		}
		wrong := "GET"
		if operation.Method == "GET" {
			wrong = "POST"
		}
		if w := taskCall(h, ws.token, wrong, operation.Path, "{}"); w.Code != http.StatusMethodNotAllowed {
			t.Fatal(operation.ID, w.Code)
		}
	}
}

func TestFailedTerminalCheckpointRecoversAsUnknown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.jsonl")
	ts := new(webTasks)
	if e := ts.openJournal(path); e != nil {
		t.Fatal(e)
	}
	task := &webTask{TaskSnapshot: chameleon.TaskSnapshot{ID: newTaskID(), RequestID: "completion-save-fail-1", SessionID: "old", OperationID: "slot.activate", Path: "/api/slot/activate", Write: true, State: "running", Phase: "device", Outcome: "in_progress"}}
	ts.records[task.ID] = task
	ts.order = append(ts.order, task.ID)
	ts.receipts[task.RequestID] = taskReceipt{sha256.Sum256([]byte("input")), task.ID}
	if e := ts.persistLocked(task); e != nil {
		t.Fatal(e)
	}
	ts.journal.file.Close()
	task.State = "succeeded"
	task.Phase = "finished"
	task.Outcome = "completed"
	if e := ts.persistLocked(task); e == nil {
		t.Fatal("closed journal saved completion")
	}
	ts.stop()
	next := new(webTasks)
	if e := next.openJournal(path); e != nil {
		t.Fatal(e)
	}
	defer next.stop()
	if got := next.records[task.ID]; got.Outcome != "write_outcome_unknown" || got.State != "interrupted" {
		t.Fatalf("%+v", got)
	}
}

func TestHostTaskPhasesRecoverWithoutReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "host-tasks.jsonl")
	ts := new(webTasks)
	if err := ts.openJournal(path); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct{ operation, path, phase string }{{"environment.diagnostics", "/api/environment", "environment"}, {"bridge.scan", "/api/bridge/scan", "bridge-management"}} {
		snapshot := chameleon.TaskSnapshot{ID: newTaskID(), RequestID: "interrupted-host-" + item.phase, SessionID: "old-session", OperationID: item.operation, Path: item.path, State: "running", Phase: item.phase, Outcome: "in_progress", Created: time.Now().UTC()}
		task := &webTask{TaskSnapshot: snapshot, cancel: func() {}}
		ts.records[snapshot.ID] = task
		ts.order = append(ts.order, snapshot.ID)
		ts.receipts[snapshot.RequestID] = taskReceipt{sha256.Sum256([]byte(snapshot.ID)), snapshot.ID}
		if err := ts.persistLocked(task); err != nil {
			t.Fatal(err)
		}
	}
	ts.stop()
	recovered := new(webTasks)
	if err := recovered.openJournal(path); err != nil {
		t.Fatal(err)
	}
	defer recovered.stop()
	if len(recovered.records) != 2 {
		t.Fatal(recovered.records)
	}
	for _, task := range recovered.records {
		if task.State != "interrupted" || task.Outcome != "read_interrupted" || !task.Recovered || task.cancel != nil || task.ResultRetained {
			t.Fatal(task)
		}
	}
}
