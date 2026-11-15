package main

import (
	"context"
	"encoding/json"
	"errors"
	chameleon "github.com/soulteary/chameleon-desk"
	"net"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func sessionFixture(t *testing.T) (*webServer, *atomic.Int32) {
	t.Helper()
	opens := new(atomic.Int32)
	ws := &webServer{sessionEnabled: true, timeout: time.Second}
	ws.openDevice = func(context.Context, string, time.Duration) (*chameleon.Client, error) {
		opens.Add(1)
		a, b := net.Pipe()
		t.Cleanup(func() { _ = b.Close() })
		return chameleon.NewClient(a, time.Second), nil
	}
	t.Cleanup(ws.closeSession)
	return ws, opens
}
func TestDirectSessionLifecycleAndAuthorization(t *testing.T) {
	ws, opens := sessionFixture(t)
	h := offlineWeb(t, ws)
	call := func(method, path, body, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://127.0.0.1"+path, strings.NewReader(body))
		r.Header.Set("X-Chameleon-Token", token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := call("GET", "/api/session/ports", "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"ports":[]`) {
		t.Fatal(w)
	}
	if w := call("POST", "/api/session/connect", `{"target":"COM7","session_id":""}`, ""); w.Code != 403 {
		t.Fatal(w)
	}
	if opens.Load() != 0 {
		t.Fatal("unauthorized open")
	}
	for _, target := range []string{"tcp:127.0.0.1:1234", "tcp:127.0.0.1:2345"} {
		old := ws.currentSessionID()
		body, _ := json.Marshal(map[string]string{"target": target, "session_id": old})
		w := call("POST", "/api/session/connect", string(body), ws.token)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		id := ws.currentSessionID()
		if id == old || id == "" {
			t.Fatal("identity reused")
		}
		for i := 0; i < 2; i++ {
			if err := ws.withDevice(context.Background(), func(context.Context, *chameleon.Client) error { return nil }); err != nil {
				t.Fatal(err)
			}
		}
		if w := call("POST", "/api/session/disconnect", `{"session_id":"stale"}`, ws.token); w.Code != 409 {
			t.Fatal(w)
		}
		body, _ = json.Marshal(map[string]string{"session_id": id})
		if w := call("POST", "/api/session/disconnect", string(body), ws.token); w.Code != 200 {
			t.Fatal(w)
		}
		if err := ws.withDevice(context.Background(), func(context.Context, *chameleon.Client) error { t.Fatal("disconnected callback"); return nil }); err == nil {
			t.Fatal("disconnected succeeded")
		}
	}
	if opens.Load() != 2 {
		t.Fatal("session was reopened", opens.Load())
	}
}
func TestQueuedOldSessionNeverRunsOnReplacement(t *testing.T) {
	ws, opens := sessionFixture(t)
	if err := ws.connectSession(context.Background(), "COM7"); err != nil {
		t.Fatal(err)
	}
	old := ws.currentSessionID()
	ctx := context.WithValue(context.Background(), sessionContextKey{}, old)
	ws.gateOnce.Do(func() { ws.gate = make(chan struct{}, 1) })
	ws.gate <- struct{}{}
	done := make(chan error, 1)
	go func() {
		done <- ws.withDevice(ctx, func(context.Context, *chameleon.Client) error { t.Error("stale request executed"); return nil })
	}()
	if err := ws.connectSession(context.Background(), "COM7"); err != nil {
		t.Fatal(err)
	}
	<-ws.gate
	if err := <-done; err == nil || !strings.Contains(err.Error(), "session changed") {
		t.Fatal(err)
	}
	if opens.Load() != 2 {
		t.Fatal(opens.Load())
	}
}
func TestSessionGateAndFailedReplacement(t *testing.T) {
	ws, _ := sessionFixture(t)
	if err := ws.connectSession(context.Background(), "COM7"); err != nil {
		t.Fatal(err)
	}
	old := ws.currentSessionID()
	var active, max atomic.Int32
	done := make(chan error, 4)
	for i := 0; i < 4; i++ {
		go func() {
			done <- ws.withDevice(context.Background(), func(context.Context, *chameleon.Client) error {
				n := active.Add(1)
				if n > max.Load() {
					max.Store(n)
				}
				time.Sleep(time.Millisecond)
				active.Add(-1)
				return nil
			})
		}()
	}
	for i := 0; i < 4; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if max.Load() != 1 {
		t.Fatal("operations overlapped")
	}
	ws.openDevice = func(context.Context, string, time.Duration) (*chameleon.Client, error) {
		return nil, errors.New("open failed")
	}
	if ws.connectSession(context.Background(), "bad") == nil {
		t.Fatal("expected error")
	}
	if ws.currentSessionID() == old {
		t.Fatal("failure retained identity")
	}
	if ws.sessionStatus().(map[string]any)["connected"] != false {
		t.Fatal("failed connection retained client")
	}
}
func TestSessionTargetValidation(t *testing.T) {
	for _, target := range []string{"tcp:localhost:1234", "tcp:[::1]:1"} {
		if err := validateSessionTarget(target); err != nil {
			t.Fatal(target, err)
		}
	}
	for _, target := range []string{"", "COM7", "/etc/passwd", "ble:device", "tcp::4", "tcp:localhost:0", "tcp:localhost:65536"} {
		if validateSessionTarget(target) == nil {
			t.Fatal(target)
		}
	}
}

func TestSessionDisconnectWaitsForActiveWork(t *testing.T) {
	ws, _ := sessionFixture(t)
	if err := ws.connectSession(context.Background(), "COM7"); err != nil {
		t.Fatal(err)
	}
	h := offlineWeb(t, ws)
	id := ws.currentSessionID()
	stale := httptest.NewRequest("GET", "http://127.0.0.1/api/features/device", nil)
	stale.Header.Set("X-Chameleon-Session", "old")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, stale)
	if w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	entered, release := make(chan struct{}), make(chan struct{})
	active := make(chan error, 1)
	go func() {
		active <- ws.withDevice(context.Background(), func(context.Context, *chameleon.Client) error { close(entered); <-release; return nil })
	}()
	<-entered
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		r := httptest.NewRequest("POST", "http://127.0.0.1/api/session/disconnect", strings.NewReader(`{"session_id":"`+id+`"}`))
		r.Header.Set("X-Chameleon-Token", ws.token)
		out := httptest.NewRecorder()
		h.ServeHTTP(out, r)
		done <- out
	}()
	select {
	case <-done:
		close(release)
		t.Fatal("disconnect bypassed active work")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-active; err != nil {
		t.Fatal(err)
	}
	if out := <-done; out.Code != 200 {
		t.Fatal(out.Code, out.Body.String())
	}
	if ws.currentSessionID() == id {
		t.Fatal("disconnect retained identity")
	}
}
