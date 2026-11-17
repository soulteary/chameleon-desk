package chameleon

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testBridgeStatus() BridgeStatus {
	return BridgeStatus{APIVersion: 1, State: "disconnected", Transport: bleTransport("tcp", true), DataAddress: "127.0.0.1:12345"}
}

func TestBLEBridgeManagementRequests(t *testing.T) {
	token := strings.Repeat("a", 64)
	paths := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token || r.Header.Get("Origin") != "" {
			t.Error("invalid management headers")
		}
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/v1/scan" {
			_ = json.NewEncoder(w).Encode(BridgeScan{APIVersion: 1, Devices: []BridgeDevice{{ID: "device", Name: "Chameleon"}}})
			return
		}
		if r.URL.Path == "/v1/select" {
			var input map[string]string
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input["device_id"] != "device" {
				t.Error("invalid selection")
			}
		}
		_ = json.NewEncoder(w).Encode(testBridgeStatus())
	}))
	defer server.Close()
	b, err := NewBLEBridge(server.URL, token)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := b.Status(ctx); err != nil {
		t.Fatal(err)
	}
	if result, err := b.Scan(ctx); err != nil || len(result.Devices) != 1 {
		t.Fatal(result, err)
	}
	if _, err := b.Select(ctx, "device"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 5 {
		t.Fatal(paths)
	}
}

func TestBLEBridgeRejectsRemoteURLsAndBadCredentials(t *testing.T) {
	for _, address := range []string{"https://127.0.0.1:12346", "http://localhost:12346", "http://192.0.2.1:12346", "http://user@127.0.0.1:12346", "http://127.0.0.1:12346/path", "http://127.0.0.1:12346?", "http://127.0.0.1:0"} {
		if _, err := NewBLEBridge(address, strings.Repeat("a", 64)); err == nil {
			t.Fatal(address)
		}
	}
	if _, err := NewBLEBridge("http://127.0.0.1:12346", "secret"); err == nil {
		t.Fatal("invalid token accepted")
	}
}

func TestBLEBridgeDoesNotFollowRedirectsOrReflectErrors(t *testing.T) {
	called := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer server.Close()
	b, err := NewBLEBridge(server.URL, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Status(context.Background()); err == nil || called {
		t.Fatalf("redirect followed: %v %v", called, err)
	}
}

func TestBLEBridgeValidatesTransportAndDataDestination(t *testing.T) {
	for _, mutate := range []func(*BridgeStatus){
		func(s *BridgeStatus) { s.Transport.AllowsPairingChanges = true },
		func(s *BridgeStatus) { s.Transport.AllowsDeviceRestore = true },
		func(s *BridgeStatus) { s.Transport.Medium = "serial" },
		func(s *BridgeStatus) { s.DataAddress = "192.0.2.1:12345" },
		func(s *BridgeStatus) { s.APIVersion = 2 },
		func(s *BridgeStatus) { s.State = "unknown" },
	} {
		status := testBridgeStatus()
		mutate(&status)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(status) }))
		b, err := NewBLEBridge(server.URL, strings.Repeat("a", 64))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := b.Status(context.Background()); err == nil {
			t.Fatalf("accepted %+v", status)
		}
		server.Close()
	}
}

func TestBLEBridgeSessionMustMatchTheSelectedDevice(t *testing.T) {
	for _, deviceID := range []string{"selected", "other"} {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
			_, _ = conn.Write(bridgeDeclaration(true, deviceID))
			buffer := make([]byte, 1)
			_, _ = conn.Read(buffer)
		}()
		status := testBridgeStatus()
		status.State = "connected"
		status.SelectedDevice = &BridgeDevice{ID: "selected"}
		status.DataAddress = listener.Addr().String()
		b := &BLEBridge{}
		c, err := b.OpenSession(context.Background(), status, time.Second)
		if deviceID == "selected" {
			if err != nil {
				t.Fatal(err)
			}
			c.Close()
		} else if err == nil {
			c.Close()
			t.Fatal("wrong selected device accepted")
		}
		listener.Close()
	}
}

func TestBridgeHostDiagnosticsAreNegotiatedAndRedacted(t *testing.T) {
	token := strings.Repeat("e", 64)
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/v1/status" {
			_ = json.NewEncoder(w).Encode(BridgeStatus{APIVersion: 1, DiagnosticsVersion: 1, State: "disconnected", DataAddress: "127.0.0.1:12345", Transport: TransportInfo{Connection: "tcp", Medium: "ble", Managed: true}})
			return
		}
		if r.URL.Path != "/v1/diagnostics" || r.Method != "GET" {
			t.Error("unexpected mutation", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"schema_version": 1, "backend": "ble", "bridge_version": CoreBuildInfo(), "checks": []BridgeDiagnosticCheck{{Code: "dependency_load_failed", Status: "fail", Message: token}, {Code: "untrusted-path", Status: "fail", Message: "/private/secret"}}})
	}))
	defer server.Close()
	bridge, err := NewBLEBridge(server.URL, token)
	if err != nil {
		t.Fatal(err)
	}
	report := bridge.Diagnose(context.Background())
	raw, _ := json.Marshal(report)
	if len(paths) != 2 || !strings.Contains(string(raw), "bridge_host_dependency_load_failed") || strings.Contains(string(raw), token) || strings.Contains(string(raw), "/private/secret") {
		t.Fatal(string(raw), paths)
	}
}
