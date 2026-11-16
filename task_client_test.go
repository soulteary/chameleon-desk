package chameleon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestTaskClientLostAcceptanceDoesNotReplay(t *testing.T) {
	var posts atomic.Int32
	requestID := "lost-response-0001"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Chameleon-Token") != "private" {
			t.Error("missing token")
		}
		if r.Method == "POST" {
			posts.Add(1)
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
			return
		}
		if r.URL.Query().Get("request_id") != requestID {
			t.Error("missing recovery key")
		}
		_ = json.NewEncoder(w).Encode(TaskSnapshot{ID: strings.Repeat("a", 32), RequestID: requestID, OperationID: "slot.activate", State: "interrupted", Outcome: "write_outcome_unknown"})
	}))
	defer srv.Close()
	c, e := NewTaskClient(srv.URL, "private")
	if e != nil {
		t.Fatal(e)
	}
	_, e = c.Submit(context.Background(), TaskSubmission{RequestID: requestID, SessionID: "session", OperationID: "slot.activate", Body: json.RawMessage(`{"slot":1}`)})
	var failure *TaskRequestError
	if !errors.As(e, &failure) || !failure.Ambiguous || failure.RequestID != requestID {
		t.Fatal(e)
	}
	found, e := c.Find(context.Background(), requestID)
	if e != nil || found.Outcome != "write_outcome_unknown" || posts.Load() != 1 {
		t.Fatal(found, e, posts.Load())
	}
}
func TestTaskClientRedirectAndDefinitiveRejection(t *testing.T) {
	for _, status := range []int{307, 400, 403, 409, 410, 429, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Location", "/other")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"error":"rejected"}`))
			}))
			defer srv.Close()
			c, _ := NewTaskClient(srv.URL, "private")
			_, e := c.Submit(context.Background(), TaskSubmission{RequestID: "rejection-test-01", SessionID: "s", OperationID: "slot.activate"})
			var failure *TaskRequestError
			if !errors.As(e, &failure) || failure.Ambiguous != (status == 307 || status == 503) || requests.Load() != 1 {
				t.Fatal(e, requests.Load())
			}
		})
	}
}
func TestTaskClientWaitAndValidation(t *testing.T) {
	var posts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			posts.Add(1)
		}
		_ = json.NewEncoder(w).Encode(TaskSnapshot{State: "running"})
	}))
	defer srv.Close()
	c, _ := NewTaskClient(srv.URL, "private")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, e := c.Wait(ctx, strings.Repeat("a", 32), time.Millisecond); e == nil {
		t.Fatal("wait ignored deadline")
	}
	if posts.Load() != 0 {
		t.Fatal("poll canceled/replayed remote work")
	}
	for _, u := range []string{"https://127.0.0.1", "http://example.com", "http://127.0.0.1/path", "http://user@localhost", "http://localhost?x=1", "http://localhost#f"} {
		if _, e := NewTaskClient(u, "private"); e == nil {
			t.Fatal(u)
		}
	}
	if _, e := c.Get(context.Background(), "../capabilities"); e == nil {
		t.Fatal("unvalidated ID")
	}
	if _, e := c.Submit(context.Background(), TaskSubmission{RequestID: "valid-request-01", SessionID: "s", OperationID: "device.info", Body: json.RawMessage(`{}`)}); e == nil {
		t.Fatal("GET body accepted")
	}
}

func TestTaskClientRejectsMalformedBodiesBeforeSubmission(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer srv.Close()
	client, _ := NewTaskClient(srv.URL, "private")
	for _, body := range []string{`{`, `null`, `[]`, `"text"`} {
		_, e := client.Submit(context.Background(), TaskSubmission{RequestID: "invalid-body-0001", SessionID: "session", OperationID: "slot.activate", Body: json.RawMessage(body)})
		var ambiguous *TaskRequestError
		if e == nil || errors.As(e, &ambiguous) {
			t.Fatal("local validation misreported acceptance ambiguity", e)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("malformed input reached server")
	}
}
