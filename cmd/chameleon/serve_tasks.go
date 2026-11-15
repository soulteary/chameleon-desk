package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
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
	ID              string          `json:"id"`
	RequestID       string          `json:"request_id"`
	SessionID       string          `json:"session_id"`
	Path            string          `json:"path"`
	Write           bool            `json:"write"`
	State           string          `json:"state"`
	Phase           string          `json:"phase"`
	Outcome         string          `json:"outcome"`
	Created         time.Time       `json:"created_at"`
	Updated         time.Time       `json:"updated_at"`
	Commands        uint64          `json:"commands_attempted"`
	Responses       uint64          `json:"responses_received"`
	Status          int             `json:"http_status,omitempty"`
	Result          json.RawMessage `json:"result,omitempty"`
	CancelRequested bool            `json:"cancel_requested"`
	started         bool
	cancel          context.CancelFunc
}
type taskReceipt struct {
	hash [32]byte
	id   string
}
type webTasks struct {
	mu       sync.Mutex
	once     sync.Once
	records  map[string]*webTask
	receipts map[string]taskReceipt
	order    []string
	serverID string
	stopped  bool
	wg       sync.WaitGroup
}

func (ts *webTasks) init() {
	ts.once.Do(func() {
		ts.records = make(map[string]*webTask)
		ts.receipts = make(map[string]taskReceipt)
		ts.serverID = newTaskID()
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
		}
	}
	ts.mu.Unlock()
	ts.wg.Wait()
}
func taskPath(path string) (string, bool) {
	switch path {
	case "/api/info", "/api/scan/hf", "/api/scan/lf", "/api/features/device":
		return "GET", false
	case "/api/slot/activate", "/api/slot/load-card":
		return "POST", true
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
			ts.mu.Unlock()
			writeJSON(w, map[string]any{"server_id": serverID, "tasks": rows})
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
	var input struct {
		RequestID string          `json:"request_id"`
		SessionID string          `json:"session_id"`
		Path      string          `json:"path"`
		Body      json.RawMessage `json:"body,omitempty"`
	}
	if err := decodeBody(r, &input); err != nil {
		writeErr(w, 400, err)
		return
	}
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
	payload, _ := json.Marshal(input)
	hash := sha256.Sum256(payload)
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
	t := &webTask{ID: newTaskID(), RequestID: input.RequestID, SessionID: input.SessionID, Path: input.Path, Write: write, State: "queued", Phase: "queued", Outcome: "not_started", Created: time.Now().UTC(), cancel: cancel}
	t.Updated = t.Created
	ts.records[t.ID] = t
	ts.receipts[input.RequestID] = taskReceipt{hash, t.ID}
	ts.order = append(ts.order, t.ID)
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
			t.Status = out.status
			if t.Status == 0 {
				t.Status = 200
			}
			t.Result = append(json.RawMessage(nil), out.body.Bytes()...)
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
				}
			}
		}()

		switch input.Path {
		case "/api/info":
			ws.handleInfo(out, request)
		case "/api/features/device":
			ws.handleFeatureDevice(out, request)
		case "/api/scan/hf":
			ws.handleScanHF(out, request)
		case "/api/scan/lf":
			ws.handleScanLF(out, request)
		case "/api/slot/activate":
			ws.handleSlotActivate(out, request)
		case "/api/slot/load-card":
			ws.handleSlotLoadCard(out, request)
		}

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
func (ws *webServer) taskProgress(ctx context.Context, c *chameleon.Client) func() {
	t, _ := ctx.Value(taskKey{}).(*webTask)
	if t == nil {
		return func() {}
	}
	ts := &ws.tasks
	base := c.Stats()
	ts.mu.Lock()
	t.started = true
	t.State = "running"
	t.Outcome = "in_progress"
	t.Phase = "device"
	t.Updated = time.Now().UTC()
	ts.mu.Unlock()
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
	return func() { close(done); <-finished }
}
