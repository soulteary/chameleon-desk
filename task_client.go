package chameleon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// TaskSnapshot contains metadata and, while retained in server memory, the
// original typed handler response. The durable journal excludes result payloads.
type TaskSnapshot struct {
	ID              string          `json:"id"`
	RequestID       string          `json:"request_id"`
	SessionID       string          `json:"session_id"`
	OperationID     string          `json:"operation_id"`
	Path            string          `json:"path"`
	WriteAttempted  *bool           `json:"write_attempted,omitempty"`
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
	ResultRetained  bool            `json:"result_retained"`
	Recovered       bool            `json:"recovered"`
	CancelRequested bool            `json:"cancel_requested"`
}

func (t TaskSnapshot) Terminal() bool {
	switch t.State {
	case "succeeded", "failed", "canceled", "interrupted":
		return true
	}
	return false
}

type TaskList struct {
	ServerID     string         `json:"server_id"`
	StoreID      string         `json:"store_id"`
	Durable      bool           `json:"durable"`
	JournalError string         `json:"journal_error,omitempty"`
	Tasks        []TaskSnapshot `json:"tasks"`
}
type TaskSubmission struct {
	RequestID   string          `json:"request_id"`
	SessionID   string          `json:"session_id"`
	OperationID string          `json:"operation_id,omitempty"`
	Path        string          `json:"path,omitempty"` // compatibility with existing Web clients
	Body        json.RawMessage `json:"body,omitempty"`
}

// TaskRequestError preserves the request ID even if the acceptance response was
// lost. Ambiguous means query by RequestID before any further explicit action.
// Neither this error nor a canceled client context cancels a server-owned task.
type TaskRequestError struct {
	RequestID string
	Status    int
	Ambiguous bool
	Err       error
}

func (e *TaskRequestError) Error() string {
	if e.Ambiguous {
		return fmt.Sprintf("request %s: acceptance is unknown; query task history; do not resubmit: %v", e.RequestID, e.Err)
	}
	return fmt.Sprintf("task request %s (HTTP %d): %v", e.RequestID, e.Status, e.Err)
}
func (e *TaskRequestError) Unwrap() error { return e.Err }

// TaskHTTPError exposes definitive query/rejection status without parsing text.
type TaskHTTPError struct {
	Status  int
	Message string
}

func (e *TaskHTTPError) Error() string { return fmt.Sprintf("Desk HTTP %d: %s", e.Status, e.Message) }

// TaskClient talks to an existing loopback Desk server; it never opens the
// bridge/device, follows redirects or automatically retries a submission.
type TaskClient struct {
	base, token string
	http        *http.Client
}

func NewTaskClient(baseURL, token string) (*TaskClient, error) {
	u, e := url.Parse(baseURL)
	if e != nil || u.Scheme != "http" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery || u.Opaque != "" {
		return nil, fmt.Errorf("Desk URL must be an HTTP loopback origin")
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return nil, fmt.Errorf("Desk URL must use a loopback host")
	}
	if strings.TrimSpace(token) == "" || strings.ContainsAny(token, "\r\n") {
		return nil, fmt.Errorf("Desk session token is required")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	// Fresh connections and no GetBody prevent net/http from replaying a POST.
	transport.DisableKeepAlives = true
	return &TaskClient{strings.TrimRight(baseURL, "/"), token, &http.Client{Transport: transport, Timeout: 65 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *TaskClient) request(ctx context.Context, method, path string, body any, out any) (int, error) {
	var data []byte
	var e error
	if body != nil {
		data, e = json.Marshal(body)
		if e != nil {
			return 0, e
		}
	}
	r, e := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(data))
	if e != nil {
		return 0, e
	}
	r.GetBody = nil
	r.Header.Set("X-Chameleon-Token", c.token)
	r.Header.Set("Accept", "application/json")
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	response, e := c.http.Do(r)
	if e != nil {
		return 0, e
	}
	defer response.Body.Close()
	b, e := io.ReadAll(io.LimitReader(response.Body, 8<<20+1))
	if e != nil {
		return response.StatusCode, e
	}
	if len(b) > 8<<20 {
		return response.StatusCode, fmt.Errorf("task response exceeds limit")
	}
	if response.StatusCode >= 300 {
		var v struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(b, &v)
		if v.Error == "" {
			v.Error = http.StatusText(response.StatusCode)
		}
		return response.StatusCode, &TaskHTTPError{response.StatusCode, v.Error}
	}
	return response.StatusCode, json.Unmarshal(b, out)
}
func (c *TaskClient) Submit(ctx context.Context, input TaskSubmission) (out TaskSnapshot, err error) {
	if len(input.RequestID) < 16 || len(input.RequestID) > 128 {
		return out, fmt.Errorf("request_id must contain 16–128 bytes")
	}
	var o OperationDefinition
	if input.OperationID != "" {
		o, err = LookupOperation(input.OperationID)
	} else {
		var ok bool
		o, ok = OperationForPath(input.Path)
		if !ok {
			err = fmt.Errorf("unsupported task path")
		}
	}
	if err != nil {
		return out, err
	}
	if o.Scope == "offline" {
		if input.SessionID != "" && input.SessionID != "offline" {
			return out, fmt.Errorf("offline tasks do not accept a device session")
		}
		input.SessionID = "offline"
	} else if input.SessionID == "" {
		return out, fmt.Errorf("session_id is required")
	}
	if input.Path != "" && input.Path != o.Path {
		return out, fmt.Errorf("operation and path differ")
	}
	input.OperationID = o.ID
	input.Path = o.Path
	if o.Method == "GET" && len(input.Body) > 0 {
		return out, fmt.Errorf("GET operations do not accept a body")
	}
	if len(input.Body) > 0 {
		var body map[string]json.RawMessage
		if err := json.Unmarshal(input.Body, &body); err != nil || body == nil {
			return out, fmt.Errorf("task body must contain a JSON object")
		}
	}
	status, e := c.request(ctx, "POST", "/api/tasks", input, &out)
	if e != nil {
		definitive := status == 400 || status == 403 || status == 404 || status == 405 || status == 409 || status == 410 || status == 413 || status == 429
		return out, &TaskRequestError{input.RequestID, status, !definitive, e}
	}
	if out.ID == "" || out.RequestID != input.RequestID || out.OperationID != o.ID {
		return out, &TaskRequestError{input.RequestID, status, true, fmt.Errorf("invalid task acceptance response")}
	}
	return out, nil
}
func (c *TaskClient) List(ctx context.Context) (out TaskList, err error) {
	_, err = c.request(ctx, "GET", "/api/tasks", nil, &out)
	return
}
func (c *TaskClient) Find(ctx context.Context, requestID string) (out TaskSnapshot, err error) {
	_, err = c.request(ctx, "GET", "/api/tasks?request_id="+url.QueryEscape(requestID), nil, &out)
	return
}
func taskIDPath(id string) (string, error) {
	if len(id) != 32 || strings.IndexFunc(id, func(r rune) bool { return !strings.ContainsRune("0123456789abcdef", r) }) >= 0 {
		return "", fmt.Errorf("invalid task ID")
	}
	return "/api/tasks/" + id, nil
}
func (c *TaskClient) Get(ctx context.Context, id string) (out TaskSnapshot, err error) {
	p, e := taskIDPath(id)
	if e != nil {
		return out, e
	}
	_, err = c.request(ctx, "GET", p, nil, &out)
	return
}
func (c *TaskClient) Cancel(ctx context.Context, id string) (out TaskSnapshot, err error) {
	p, e := taskIDPath(id)
	if e != nil {
		return out, e
	}
	_, err = c.request(ctx, "POST", p+"/cancel", struct{}{}, &out)
	return
}

// Wait polls reads only. A polling failure returns without canceling or replaying.
func (c *TaskClient) Wait(ctx context.Context, id string, interval time.Duration) (TaskSnapshot, error) {
	if interval <= 0 {
		return TaskSnapshot{}, fmt.Errorf("positive polling interval required")
	}
	for {
		t, e := c.Get(ctx, id)
		if e != nil || t.Terminal() {
			return t, e
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return t, ctx.Err()
		case <-timer.C:
		}
	}
}

type TaskSession struct {
	SessionID string `json:"session_id"`
	Connected bool   `json:"connected"`
}

func (c *TaskClient) Session(ctx context.Context) (out TaskSession, err error) {
	_, err = c.request(ctx, "GET", "/api/session/status", nil, &out)
	return
}
