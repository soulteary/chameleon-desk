package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	chameleon "github.com/soulteary/chameleon-desk"
	"net/http"
	"strings"
	"sync"
	"time"
)

type taskKey struct{}
type webTask struct {
	chameleon.TaskSnapshot
	started bool
	cancel  context.CancelFunc
}
type taskReceipt struct {
	hash [32]byte
	id   string
}
type webTasks struct {
	journalClosed bool
	journal       *taskJournal
	journalError  error
	receiptKey    [32]byte
	storeID       string
	mu            sync.Mutex
	once          sync.Once
	records       map[string]*webTask
	receipts      map[string]taskReceipt
	order         []string
	serverID      string
	stopped       bool
	wg            sync.WaitGroup
}

func (ts *webTasks) init() {
	ts.once.Do(func() {
		ts.records = make(map[string]*webTask)
		ts.receipts = make(map[string]taskReceipt)
		ts.serverID = newTaskID()
		ts.storeID = ts.serverID
		ts.newReceiptKey()
	})
}
func newTaskID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func taskTerminal(t *webTask) bool {
	return t.State == "succeeded" || t.State == "failed" || t.State == "canceled" || t.State == "interrupted"
}
func (ts *webTasks) stop() {
	ts.init()
	ts.mu.Lock()
	ts.stopped = true
	for _, t := range ts.records {
		if !taskTerminal(t) {
			t.CancelRequested = true
			t.cancel()
			_ = ts.persistLocked(t)
		}
	}
	ts.mu.Unlock()
	ts.wg.Wait()
	ts.mu.Lock()
	if ts.journal != nil && !ts.journalClosed {
		ts.journalClosed = true
		_ = ts.journal.file.Close()
	}
	ts.mu.Unlock()
}
func taskPath(path string) (string, bool) {
	if o, ok := chameleon.OperationForPath(path); ok {
		return o.Method, o.Write
	}
	return "", false
}
func (ws *webServer) handleTasks(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	ts := &ws.tasks
	ts.init()
	id := strings.TrimPrefix(r.URL.Path, "/api/tasks/")
	if r.Method == "GET" {
		ts.mu.Lock()
		if r.URL.Path == "/api/tasks" && r.URL.Query().Has("request_id") {
			receipt, ok := ts.receipts[r.URL.Query().Get("request_id")]
			if !ok {
				ts.mu.Unlock()
				writeErr(w, 404, fmt.Errorf("request receipt not found; do not resubmit a write"))
				return
			}
			if t := ts.records[receipt.id]; t != nil {
				snapshot := *t
				ts.mu.Unlock()
				writeJSON(w, snapshot)
				return
			}
			ts.mu.Unlock()
			writeErr(w, 410, fmt.Errorf("task result expired; request will not be replayed"))
			return
		}
		if r.URL.Path == "/api/tasks" {
			rows := []webTask{}
			for i := len(ts.order) - 1; i >= 0; i-- {
				if t := ts.records[ts.order[i]]; t != nil {
					snapshot := *t
					snapshot.Result = nil
					rows = append(rows, snapshot)
				}
			}
			serverID := ts.serverID
			storeID, durable, journalError := ts.storeID, ts.journal != nil, ""
			if ts.journalError != nil {
				journalError = "Task journal failed; new submissions are blocked. Inspect device state before further writes."
			}
			ts.mu.Unlock()
			writeJSON(w, map[string]any{"server_id": serverID, "store_id": storeID, "durable": durable, "journal_error": journalError, "tasks": rows})
			return
		}
		if t := ts.records[id]; t != nil {
			snapshot := *t
			ts.mu.Unlock()
			writeJSON(w, snapshot)
			return
		}
		ts.mu.Unlock()
		writeErr(w, 404, fmt.Errorf("task result is not retained; do not resubmit a write"))
		return
	}
	if strings.HasSuffix(id, "/cancel") {
		var empty struct{}
		if err := decodeBody(r, &empty); err != nil {
			writeErr(w, 400, err)
			return
		}
		ts.mu.Lock()
		t := ts.records[strings.TrimSuffix(id, "/cancel")]
		if t == nil {
			ts.mu.Unlock()
			writeErr(w, 404, fmt.Errorf("task not found"))
			return
		}
		if !taskTerminal(t) {
			t.CancelRequested = true
			t.Updated = time.Now().UTC()
			t.cancel()
			_ = ts.persistLocked(t)
		}
		snapshot := *t
		ts.mu.Unlock()
		writeJSON(w, snapshot)
		return
	}
	if r.URL.Path != "/api/tasks" {
		writeErr(w, 404, fmt.Errorf("unknown task action"))
		return
	}
	var input chameleon.TaskSubmission
	if err := decodeBody(r, &input); err != nil {
		writeErr(w, 400, err)
		return
	}
	var operation chameleon.OperationDefinition
	if input.OperationID != "" {
		var err error
		operation, err = chameleon.LookupOperation(input.OperationID)
		if err != nil || (input.Path != "" && input.Path != operation.Path) {
			writeErr(w, 400, fmt.Errorf("unknown or conflicting operation"))
			return
		}
		input.Path = operation.Path
	} else {
		operation, _ = chameleon.OperationForPath(input.Path)
	}
	input.OperationID = operation.ID
	method, write := taskPath(input.Path)
	if method == "" || len(input.RequestID) < 16 || len(input.RequestID) > 128 || input.SessionID == "" {
		writeErr(w, 400, fmt.Errorf("supported path, request_id (16–128 bytes), and session_id are required"))
		return
	}
	if method == "GET" && len(input.Body) > 0 {
		writeErr(w, 400, fmt.Errorf("read tasks do not accept a body"))
		return
	}
	if write && !ws.needWrites(w) {
		return
	}
	hash := ts.digest(input)
	ts.mu.Lock()
	if prior, ok := ts.receipts[input.RequestID]; ok {
		if prior.hash != hash {
			ts.mu.Unlock()
			writeErr(w, 409, fmt.Errorf("request_id was already used with different input"))
			return
		}
		if t := ts.records[prior.id]; t != nil {
			snapshot := *t
			ts.mu.Unlock()
			writeJSON(w, snapshot)
		} else {
			ts.mu.Unlock()
			writeErr(w, 410, fmt.Errorf("task result expired; request will not be replayed"))
		}
		return
	}
	if input.SessionID != ws.currentSessionID() {
		ts.mu.Unlock()
		writeErr(w, 409, fmt.Errorf("device session changed"))
		return
	}
	if ts.journalError != nil {
		ts.mu.Unlock()
		writeErr(w, 503, fmt.Errorf("task journal unavailable; no work was started"))
		return
	}
	active := 0
	for _, task := range ts.records {
		if !taskTerminal(task) {
			active++
		}
	}
	if ts.stopped || active >= 16 || len(ts.receipts) >= 4096 {
		ts.mu.Unlock()
		writeErr(w, 429, fmt.Errorf("task capacity reached; no work was submitted"))
		return
	}
	// Evict terminal results only after accepting capacity; receipts prevent replay.
	if len(ts.records) >= 128 {
		for _, key := range ts.order {
			if t := ts.records[key]; t != nil && taskTerminal(t) {
				delete(ts.records, key)
				break
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	ctx = context.WithValue(ctx, sessionContextKey{}, input.SessionID)
	t := &webTask{TaskSnapshot: chameleon.TaskSnapshot{ID: newTaskID(), RequestID: input.RequestID, SessionID: input.SessionID, OperationID: operation.ID, Path: input.Path, Write: write, State: "queued", Phase: "queued", Outcome: "not_started", Created: time.Now().UTC()}, cancel: cancel}
	t.Updated = t.Created
	ts.records[t.ID] = t
	ts.receipts[input.RequestID] = taskReceipt{hash, t.ID}
	ts.order = append(ts.order, t.ID)
	if err := ts.persistLocked(t); err != nil {
		t.State = "failed"
		t.Phase = "finished"
		t.Status = 503
		cancel()
		ts.mu.Unlock()
		writeErr(w, 503, fmt.Errorf("could not durably record task; no work was started; query request_id before further action"))
		return
	}
	ctx = context.WithValue(ctx, taskKey{}, t)
	ts.wg.Add(1)
	snapshot := *t
	ts.mu.Unlock()
	body := append([]byte(nil), input.Body...)
	go func() {
		defer ts.wg.Done()
		defer cancel()
		request, _ := http.NewRequestWithContext(ctx, method, input.Path, bytes.NewReader(body))
		out := &taskResponse{header: make(http.Header)}
		defer func() {
			if recover() != nil {
				out.status = 500
				out.body.Reset()
				writeJSON(out, map[string]string{"error": "task execution interrupted by an internal error"})
			}
			ts.mu.Lock()
			defer ts.mu.Unlock()
			defer func() { _ = ts.persistLocked(t) }()
			t.Status = out.status
			if t.Status == 0 {
				t.Status = 200
			}
			t.Result = append(json.RawMessage(nil), out.body.Bytes()...)
			t.ResultRetained = len(t.Result) > 0
			t.Phase = "finished"
			t.Updated = time.Now().UTC()
			if t.Status < 400 {
				t.State = "succeeded"
				t.Outcome = "completed"
				return
			}
			t.State = "failed"
			if ctx.Err() != nil {
				if t.started {
					t.State = "interrupted"
				} else {
					t.State = "canceled"
				}
			}
			if t.started {
				t.Outcome = "read_interrupted"
				if t.Write {
					t.Outcome = "write_outcome_unknown"
					if t.WriteAttempted != nil && !*t.WriteAttempted {
						t.Outcome = "write_not_started"
					}
				}
			}
		}()

		ws.operationHandler(input.Path)(out, request)

	}()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	writeJSON(w, snapshot)
}

type taskResponse struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (w *taskResponse) Header() http.Header { return w.header }
func (w *taskResponse) WriteHeader(n int) {
	if w.status == 0 {
		w.status = n
	}
}
func (w *taskResponse) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	return w.body.Write(b)
}
func (ws *webServer) taskProgress(ctx context.Context, c *chameleon.Client) (func(), error) {
	t, _ := ctx.Value(taskKey{}).(*webTask)
	if t == nil {
		return func() {}, nil
	}
	ts := &ws.tasks
	base := c.Stats()
	ts.mu.Lock()
	t.started = true
	t.State = "running"
	t.Outcome = "in_progress"
	t.Phase = "device"
	t.Updated = time.Now().UTC()
	err := ts.persistLocked(t)
	if err != nil {
		t.started = false
		t.State = "queued"
		t.Phase = "queued"
		t.Outcome = "not_started"
	}
	ts.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("could not record task start; device callback was not entered: %w", err)
	}
	done, finished := make(chan struct{}), make(chan struct{})
	update := func() {
		s := c.Stats()
		ts.mu.Lock()
		if t.Commands != s.Requests-base.Requests || t.Responses != s.Responses-base.Responses {
			t.Commands = s.Requests - base.Requests
			t.Responses = s.Responses - base.Responses
			t.Updated = time.Now().UTC()
		}
		ts.mu.Unlock()
	}
	go func() {
		defer close(finished)
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				update()
				return
			case <-ticker.C:
				update()
			}
		}
	}()
	return func() { close(done); <-finished }, nil
}

func (ws *webServer) operationHandler(path string) http.HandlerFunc {
	switch path {
	case "/api/environment":
		return ws.handleEnvironment
	case "/api/bridge/status", "/api/bridge/scan", "/api/bridge/select", "/api/bridge/connect", "/api/bridge/disconnect":
		return ws.handleBridge
	case "/api/device/mode", "/api/reader/type2-batch-plan", "/api/reader/type2-batch-write", "/api/reader/value-plan", "/api/reader/value-write", "/api/reader/magic-inspect", "/api/reader/config-read", "/api/reader/config-update", "/api/slot/emulator/read", "/api/slot/emulator/configure", "/api/slot/emulator/logs":
		return ws.handleExtendedWorkflow

	case "/api/slot/read-current":
		return ws.handleSlotReadCurrent
	case "/api/reader/classic-dictionary":
		return ws.handleClassicDictionary
	case "/api/reader/lf-edit":
		return ws.handleLFIdentityEdit
	case "/api/reader/classic-repair":
		return ws.handleClassicRepair
	case "/api/reader/classic-verify":
		return ws.handleClassicVerify
	case "/api/reader/classic-recover":
		return ws.handleClassicRecover
	case "/api/reader/classic-write-plan":
		return ws.handleClassicWritePlan
	case "/api/reader/classic-write":
		return ws.handleClassicWrite
	case "/api/reader/type2-workflow":
		return ws.handleType2WorkflowRead
	case "/api/reader/type2-write-plan":
		return ws.handleType2WritePlan
	case "/api/reader/type2-write":
		return ws.handleType2UserPageWrite
	case "/api/physical/lf/plan":
		return ws.handleLFWritePlan
	case "/api/physical/lf/write":
		return ws.handleLFWrite
	case "/api/reader/classic":
		return ws.handleClassicRead
	case "/api/reader/type2":
		return ws.handleType2Read
	case "/api/reader/lf":
		return ws.handleLFRead
	case "/api/settings/read":
		return ws.handleSettingsRead
	case "/api/settings/configure":
		return ws.handleSettingsConfigure
	case "/api/slot/details":
		return ws.handleSlotDetails
	case "/api/slot/edit":
		return ws.handleSlotEdit
	case "/api/info":
		return ws.handleInfo
	case "/api/features/device":
		return ws.handleFeatureDevice
	case "/api/scan/hf":
		return ws.handleScanHF
	case "/api/scan/lf":
		return ws.handleScanLF
	case "/api/slot/activate":
		return ws.handleSlotActivate
	case "/api/slot/load-card":
		return ws.handleSlotLoadCard
	}
	return nil
}
