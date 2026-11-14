package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	chameleon "github.com/soulteary/chameleon-desk"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func offlineWeb(t *testing.T, ws *webServer) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	if e := ws.routes(mux); e != nil {
		t.Fatal(e)
	}
	return ws.logging(mux)
}
func TestServeHostOriginMethodAndToken(t *testing.T) {
	ws := &webServer{listenPort: "8088", allowWrites: true, port: "mock", openDevice: func(context.Context, string, time.Duration) (*chameleon.Client, error) {
		t.Error("untrusted request opened a device")
		return nil, io.EOF
	}}
	h := offlineWeb(t, ws)
	tests := []struct {
		name, host, origin, site, method, path, token string
		status                                        int
	}{
		{"local", "127.0.0.1:8088", "http://127.0.0.1:8088", "same-origin", "GET", "/api/capabilities", "", 200},
		{"localhost", "localhost:8088", "", "", "GET", "/api/capabilities", "", 200},
		{"ipv6", "[::1]:8088", "http://[::1]:8088", "", "GET", "/api/capabilities", "", 200},
		{"rebound", "attacker.example:8088", "http://attacker.example:8088", "", "GET", "/api/capabilities", "", 403},
		{"rebound-no-origin", "attacker.example:8088", "", "", "GET", "/api/capabilities", "", 403},
		{"wrong-port", "127.0.0.1:8089", "", "", "GET", "/api/capabilities", "", 403},
		{"origin-suffix", "127.0.0.1:8088", "http://evil.example/path//127.0.0.1:8088", "", "GET", "/api/capabilities", "", 403},
		{"wrong-scheme", "127.0.0.1:8088", "https://127.0.0.1:8088", "", "GET", "/api/capabilities", "", 403},
		{"cross-site-get", "127.0.0.1:8088", "", "cross-site", "GET", "/api/scan/hf", "", 403},
		{"write-get", "127.0.0.1:8088", "", "", "GET", "/api/slot/activate", ws.token, 405},
		{"write-no-token", "127.0.0.1:8088", "", "", "POST", "/api/slot/activate", "", 403},
		{"write-bad-token", "127.0.0.1:8088", "", "", "POST", "/api/slot/load-card", "bad", 403},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(tt.method, "http://"+tt.host+tt.path, strings.NewReader(`{"slot":1}`))
			r.Header.Set("Origin", tt.origin)
			r.Header.Set("Sec-Fetch-Site", tt.site)
			r.Header.Set("X-Chameleon-Token", tt.token)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tt.status {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
		})
	}
	r := httptest.NewRequest("GET", "http://127.0.0.1:8088/api/capabilities", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var caps struct {
		Token string `json:"csrf_token"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &caps); e != nil || len(caps.Token) != 64 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(caps, e)
	}
	other := &webServer{}
	offlineWeb(t, other)
	if other.token == ws.token {
		t.Fatal("shared token across sessions")
	}
	for _, addr := range []string{"0.0.0.0:8088", "[::]:8088", "attacker.example:8088"} {
		if e := runServe([]string{"serve", "--addr", addr}); e == nil {
			t.Fatal("non-loopback bind accepted")
		}
	}
}
func TestServeAuthorizedWriteUsesSimulator(t *testing.T) {
	open := mockDevice(t, []step{{cmd: 1003, payload: []byte{1}, status: 0x68}})
	ws := &webServer{port: "mock", timeout: time.Second, allowWrites: true, openDevice: func(_ context.Context, p string, d time.Duration) (*chameleon.Client, error) { return open(p, d) }}
	h := offlineWeb(t, ws)
	r := httptest.NewRequest("POST", "http://127.0.0.1:8088/api/slot/activate", strings.NewReader(`{"slot":2}`))
	r.Header.Set("X-Chameleon-Token", ws.token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestServeScanDoesNotHideRestoreFailure(t *testing.T) {
	for _, kind := range []string{"hf", "lf"} {
		for _, restore := range []uint16{0x68, 0x66} {
			t.Run(kind+chameleon.HexBytes{byte(restore)}.String(), func(t *testing.T) {
				steps := []step{}
				if kind == "hf" {
					steps = append(steps, step{cmd: 1035, status: 0x68, data: []byte{3, 0xe9, 3, 0xea, 7, 0xd0}})
				}
				steps = append(steps, step{cmd: 1002, status: 0x68, data: []byte{0}}, step{cmd: 1001, payload: []byte{1}, status: 0x68})
				if kind == "hf" {
					steps = append(steps, step{cmd: 2000, status: 1})
				} else {
					steps = append(steps, step{cmd: 3000, status: 0x41})
				}
				steps = append(steps, step{cmd: 1001, payload: []byte{0}, status: restore})
				open := mockDevice(t, steps)
				ws := &webServer{port: "mock", timeout: time.Second, openDevice: func(_ context.Context, p string, d time.Duration) (*chameleon.Client, error) { return open(p, d) }}
				h := offlineWeb(t, ws)
				r := httptest.NewRequest("GET", "http://127.0.0.1:8088/api/scan/"+kind, nil)
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if restore == 0x68 {
					if w.Code != 200 || !strings.Contains(w.Body.String(), `"present":false`) {
						t.Fatal(w.Code, w.Body.String())
					}
				} else {
					if w.Code != 502 || !strings.Contains(w.Body.String(), "restore original device mode") {
						t.Fatal(w.Code, w.Body.String())
					}
				}
			})
		}
	}
}
func TestServeCanceledQueueNeverOpensDevice(t *testing.T) {
	var opens atomic.Int32
	ws := &webServer{port: "mock", timeout: time.Second, openDevice: func(ctx context.Context, _ string, _ time.Duration) (*chameleon.Client, error) {
		opens.Add(1)
		a, b := net.Pipe()
		b.Close()
		return chameleon.NewClient(a, time.Second), nil
	}}
	entered, release := make(chan struct{}), make(chan struct{})
	first := make(chan error, 1)
	go func() {
		first <- ws.withDevice(context.Background(), func(context.Context, *chameleon.Client) error { close(entered); <-release; return nil })
	}()
	<-entered
	for _, deadline := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		if deadline {
			cancel()
			ctx, cancel = context.WithTimeout(context.Background(), 10*time.Millisecond)
		} else {
			cancel()
		}
		e := ws.withDevice(ctx, func(context.Context, *chameleon.Client) error { t.Error("canceled callback executed"); return nil })
		cancel()
		if !errors.Is(e, context.Canceled) && !errors.Is(e, context.DeadlineExceeded) {
			t.Fatal(e)
		}
	}
	if opens.Load() != 1 {
		t.Fatal("canceled request opened device", opens.Load())
	}
	close(release)
	if e := <-first; e != nil {
		t.Fatal(e)
	}
	// A canceled request that acquires a free gate also must not open anything.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = ws.withDevice(ctx, func(context.Context, *chameleon.Client) error { t.Error("canceled work executed"); return nil })
	if opens.Load() != 1 {
		t.Fatal(opens.Load())
	}
}
func TestServeRequestCancellationReachesOperation(t *testing.T) {
	ws := &webServer{port: "mock", timeout: time.Second, openDevice: func(_ context.Context, _ string, _ time.Duration) (*chameleon.Client, error) {
		a, b := net.Pipe()
		b.Close()
		return chameleon.NewClient(a, time.Second), nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := httptest.NewRequest("GET", "http://127.0.0.1:8088/api/info", nil).WithContext(ctx)
	done := make(chan struct{})
	entered := make(chan struct{})
	go func() {
		defer close(done)
		ws.device(httptest.NewRecorder(), r, func(op context.Context, _ *chameleon.Client) (any, error) {
			close(entered)
			<-op.Done()
			return nil, op.Err()
		})
	}()
	<-entered
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("request cancellation ignored")
	}
}
func TestDecodeBodyStrictAndEndpointLimits(t *testing.T) {
	for _, body := range []string{`{"slot":1} garbage`, `{"slot":1}{"slot":2}`, `{"slot":1,"unknown":1}`, `null`, `[]`, ``, strings.Repeat(" ", 1<<20+1)} {
		r := httptest.NewRequest("POST", "http://127.0.0.1:8088/api/slot/activate", strings.NewReader(body))
		var v struct {
			Slot int `json:"slot"`
		}
		if e := decodeBody(r, &v); e == nil {
			t.Fatalf("invalid JSON accepted: %.40s", body)
		}
	}
	for _, path := range []string{"/api/slot/load-card", "/api/ndef/write", "/api/database"} {
		// Large nested values are legal within the SDK-aligned envelope limits.
		key := "card"
		if path == "/api/database" {
			key = "database"
		}
		body := `{"` + key + `":{"metadata":"` + strings.Repeat("x", 2<<20) + `"}}`
		var v struct {
			Card     json.RawMessage `json:"card"`
			Database json.RawMessage `json:"database"`
		}
		if e := decodeBody(httptest.NewRequest("POST", "http://127.0.0.1:8088"+path, strings.NewReader(body)), &v); e != nil {
			t.Fatal(path, e)
		}
	}
	for _, n := range []int{8, 9} {
		r := httptest.NewRequest("POST", "http://127.0.0.1:8088", bytes.NewReader(bytes.Repeat([]byte{'x'}, n)))
		b, e := readBody(r, 8)
		if n == 8 && (e != nil || len(b) != 8) {
			t.Fatal(b, e)
		}
		if n == 9 && e == nil {
			t.Fatal("oversize body accepted")
		}
	}
	// Firmware JSON errors must be rejected before any attempt to auto-open a device.
	ws := &webServer{openDevice: func(context.Context, string, time.Duration) (*chameleon.Client, error) {
		t.Fatal("opened on bad JSON")
		return nil, io.EOF
	}}
	w := httptest.NewRecorder()
	ws.handleFirmwareCheck(w, httptest.NewRequest("POST", "http://127.0.0.1:8088/api/firmware/check", strings.NewReader(`{} garbage`)))
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
}

func TestServeDisabledWritesWithValidToken(t *testing.T) {
	ws := &webServer{port: "mock", openDevice: func(context.Context, string, time.Duration) (*chameleon.Client, error) {
		t.Error("disabled write opened device")
		return nil, io.EOF
	}}
	h := offlineWeb(t, ws)
	r := httptest.NewRequest("POST", "http://127.0.0.1:8088/api/slot/activate", strings.NewReader(`{"slot":1}`))
	r.Header.Set("X-Chameleon-Token", ws.token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 || !strings.Contains(w.Body.String(), "writes are disabled") {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestServeCanceledDuringOpenDoesNotRunOperation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stream net.Conn
	ws := &webServer{port: "mock", openDevice: func(context.Context, string, time.Duration) (*chameleon.Client, error) {
		a, b := net.Pipe()
		b.Close()
		stream = a
		cancel()
		return chameleon.NewClient(a, time.Second), nil
	}}
	e := ws.withDevice(ctx, func(context.Context, *chameleon.Client) error { t.Error("canceled operation executed"); return nil })
	if !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if _, e = stream.Write([]byte{1}); e == nil {
		t.Fatal("client was not closed")
	}
}

func TestServeBusyPortThenReadRetryRecovers(t *testing.T) {
	var opens int
	open := mockDevice(t, []step{
		{cmd: 1035, status: 0x68, data: []byte{3, 0xe9, 3, 0xea, 7, 0xd0}},
		{cmd: 1002, status: 0x68, data: []byte{0}},
		{cmd: 1001, status: 0x68, payload: []byte{1}},
		{cmd: 2000, status: 0, data: []byte{4, 1, 2, 3, 4, 4, 0, 8, 0}},
		{cmd: 1001, status: 0x68, payload: []byte{0}},
	})
	ws := &webServer{port: "mock", timeout: time.Second, openDevice: func(_ context.Context, p string, d time.Duration) (*chameleon.Client, error) {
		opens++
		if opens == 1 {
			return nil, fmt.Errorf("device or resource busy")
		}
		return open(p, d)
	}}
	h := offlineWeb(t, ws)
	for _, status := range []int{http.StatusBadGateway, http.StatusOK} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "http://127.0.0.1:8088/api/scan/hf", nil))
		if w.Code != status {
			t.Fatalf("retry response %d %s", w.Code, w.Body.String())
		}
		if status == http.StatusOK && !strings.Contains(w.Body.String(), `"present":true`) {
			t.Fatalf("retry did not scan: %s", w.Body.String())
		}
		if len(ws.gate) != 0 {
			t.Fatal("request left device gate locked")
		}
	}
	if opens != 2 {
		t.Fatal("unexpected opens", opens)
	}
}
