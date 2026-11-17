package main

import (
	"encoding/json"
	chameleon "github.com/soulteary/chameleon-desk"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestEnvironmentTaskIsReadOnlyAndSharesSessionGate(t *testing.T) {
	var calls atomic.Int32
	secret := strings.Repeat("b", 64)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v1/status" || r.Method != "GET" || r.Header.Get("Authorization") != "Bearer "+secret {
			t.Error("diagnostic changed bridge state", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(chameleon.BridgeStatus{APIVersion: 1, State: "disconnected", DataAddress: "127.0.0.1:12345", Error: "private backend secret", Transport: chameleon.TransportInfo{Connection: "tcp", Medium: "usb-serial", Managed: true}})
	}))
	defer remote.Close()
	bridge, err := chameleon.NewUSBBridge(remote.URL, secret)
	if err != nil {
		t.Fatal(err)
	}
	ws := &webServer{bridge: bridge, sessionEnabled: true, recoveryTools: t.TempDir(), timeout: time.Second}
	ws.rotateSessionID()
	h := offlineWeb(t, ws)
	t.Cleanup(ws.tasks.stop)
	ws.gateOnce.Do(func() { ws.gate = make(chan struct{}, 1) })
	ws.gate <- struct{}{}
	task := submitTask(t, ws, h, "/api/environment", "", "environment-check-0001")
	if calls.Load() != 0 {
		t.Fatal("diagnosis bypassed common gate")
	}
	<-ws.gate
	done := waitTask(t, ws, task.ID, func(v webTask) bool { return taskTerminal(&v) })
	if done.State != "succeeded" || done.Write || done.Commands != 0 || calls.Load() != 1 {
		t.Fatal(done, calls.Load())
	}
	var report chameleon.EnvironmentDiagnostic
	if err = json.Unmarshal(done.Result, &report); err != nil || report.Bridge == nil || len(report.Recovery.Workers) != 11 {
		t.Fatal(report, err)
	}
	if strings.Contains(string(done.Result), secret) || strings.Contains(string(done.Result), "private backend secret") || ws.getBridgeClient() != nil {
		t.Fatal("diagnosis exposed secret or claimed data")
	}
	// Query/deduplicate the completed task without repeating management I/O.
	again := submitTask(t, ws, h, "/api/environment", "", "environment-check-0001")
	if again.ID != task.ID || calls.Load() != 1 {
		t.Fatal("diagnosis was replayed")
	}
	ws.gate <- struct{}{}
	stale := submitTask(t, ws, h, "/api/environment", "", "environment-check-0002")
	ws.rotateSessionID()
	<-ws.gate
	done = waitTask(t, ws, stale.ID, func(v webTask) bool { return taskTerminal(&v) })
	if done.Status != 409 || calls.Load() != 1 {
		t.Fatal("stale host task reached bridge", done)
	}
	ws.gate <- struct{}{}
	canceled := submitTask(t, ws, h, "/api/environment", "", "environment-cancel-0001")
	if w := taskCall(h, ws.token, "POST", "/api/tasks/"+canceled.ID+"/cancel", "{}"); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	<-ws.gate
	done = waitTask(t, ws, canceled.ID, func(v webTask) bool { return taskTerminal(&v) })
	if done.State != "canceled" || done.Outcome != "not_started" || calls.Load() != 1 {
		t.Fatal("canceled task reached host", done, calls.Load())
	}
	for _, tc := range []struct {
		method, path, body string
		want               int
	}{{"POST", "/api/environment", "{}", 405}, {"GET", "/api/environment?recovery-tools-dir=/evil", "", 400}} {
		w := taskCall(h, ws.token, tc.method, tc.path, tc.body)
		if w.Code != tc.want {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
func TestBridgeCLIUsesTaskSubmissionWithExplicitIdentity(t *testing.T) {
	var submitted chameleon.TaskSubmission
	var calls int
	desk := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/tasks" || r.Method != "POST" {
			t.Error(r.Method, r.URL.Path)
		}
		if r.Header.Get("X-Chameleon-Token") != "desk-secret" {
			t.Error("missing Desk token")
		}
		if err := json.NewDecoder(r.Body).Decode(&submitted); err != nil {
			t.Error(err)
		}
		_ = json.NewEncoder(w).Encode(chameleon.TaskSnapshot{ID: strings.Repeat("a", 32), RequestID: submitted.RequestID, OperationID: submitted.OperationID, SessionID: submitted.SessionID, State: "queued"})
	}))
	defer desk.Close()
	token := filepath.Join(t.TempDir(), "desk.token")
	if err := os.WriteFile(token, []byte("desk-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"status", "scan", "select", "connect", "disconnect"} {
		args := []string{action, "--server", desk.URL, "--token-file", token, "--session-id", "observed", "--request-id", "bridge-request-" + action + "-001"}
		if action == "select" {
			args = append(args, "--device-id", "COM7")
		}
		if err := runBridgeCLI(args); err != nil {
			t.Fatal(err)
		}
		if submitted.OperationID != "bridge."+action || submitted.SessionID != "observed" {
			t.Fatal(submitted)
		}
	}
	if calls != 5 {
		t.Fatal("submission repeated", calls)
	}
	for _, args := range [][]string{{"connect"}, {"scan", "--device-id", "COM7"}, {"status", "--server", desk.URL, "--token-file", token, "--session-id", "observed", "--request-id", "short"}} {
		if err := runBridgeCLI(args); err == nil {
			t.Fatal("implicit identity or invalid request accepted", args)
		}
	}
	if calls != 5 {
		t.Fatal("invalid action reached server")
	}
}
func TestBridgeManagementTaskRejectsStaleSessionAndDoesNotNeedWritePermission(t *testing.T) {
	var calls atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(chameleon.BridgeScan{APIVersion: 1, Devices: []chameleon.BridgeDevice{}})
	}))
	defer remote.Close()
	b, err := chameleon.NewBLEBridge(remote.URL, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	ws := &webServer{bridge: b, sessionEnabled: true}
	ws.rotateSessionID()
	h := offlineWeb(t, ws)
	t.Cleanup(ws.tasks.stop)
	task := submitTask(t, ws, h, "/api/bridge/scan", "{}", "bridge-scan-once-001")
	done := waitTask(t, ws, task.ID, func(v webTask) bool { return taskTerminal(&v) })
	if done.State != "succeeded" || done.Write || done.Commands != 0 || calls.Load() != 1 {
		t.Fatal(done)
	}
	body := `{"operation_id":"bridge.scan","session_id":"old","request_id":"bridge-stale-0001","body":{}}`
	if w := taskCall(h, ws.token, "POST", "/api/tasks", body); w.Code != 409 || calls.Load() != 1 {
		t.Fatal(w.Code, w.Body.String())
	}
}
