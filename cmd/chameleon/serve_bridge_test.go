package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	chameleon "github.com/soulteary/chameleon-desk"
)

func TestServeBridgeControlRequiresSessionTokenAndTrustedOrigin(t *testing.T) {
	secret := strings.Repeat("a", 64)
	var requests atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+secret || r.Header.Get("Origin") != "" {
			t.Error("credential or Origin forwarding is incorrect")
		}
		if r.URL.Path == "/v1/scan" {
			_ = json.NewEncoder(w).Encode(chameleon.BridgeScan{APIVersion: 1, Devices: []chameleon.BridgeDevice{}})
			return
		}
		_ = json.NewEncoder(w).Encode(chameleon.BridgeStatus{APIVersion: 1, State: "disconnected", DataAddress: "127.0.0.1:12345", Transport: chameleon.TransportInfo{Connection: "tcp", Medium: "ble", Managed: true}})
	}))
	defer remote.Close()
	manager, err := chameleon.NewBLEBridge(remote.URL, secret)
	if err != nil {
		t.Fatal(err)
	}
	ws := &webServer{bridge: manager, timeout: time.Second}
	h := offlineWeb(t, ws)
	for _, test := range []struct {
		method, path, token, origin, body string
		status                            int
	}{
		{"POST", "scan", "", "", `{}`, 403},
		{"POST", "connect", "wrong", "", `{}`, 403},
		{"POST", "disconnect", ws.token, "http://evil.example", `{}`, 403},
		{"GET", "scan", ws.token, "", ``, 405},
		{"POST", "status", ws.token, "", `{}`, 405},
		{"POST", "select", ws.token, "", `{"device_id":"x"} trailing`, 400},
		{"POST", "scan", ws.token, "", `{"extra":true}`, 400},
		{"GET", "status", "", "", ``, 200},
		{"POST", "scan", ws.token, "", `{}`, 200},
	} {
		r := httptest.NewRequest(test.method, "http://127.0.0.1:8088/api/bridge/"+test.path, strings.NewReader(test.body))
		r.Header.Set("X-Chameleon-Token", test.token)
		r.Header.Set("Origin", test.origin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != test.status {
			t.Fatalf("%s %s: %d %s", test.method, test.path, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("bridge credential exposed")
		}
	}
	if requests.Load() != 2 {
		t.Fatalf("untrusted request reached bridge: %d", requests.Load())
	}
}

func TestServeBridgeDisabledAndRemoteError(t *testing.T) {
	ws := &webServer{}
	h := offlineWeb(t, ws)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "http://127.0.0.1/api/bridge/status", nil))
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
	secret := strings.Repeat("b", 64)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, secret, 401) }))
	defer remote.Close()
	manager, err := chameleon.NewBLEBridge(remote.URL, secret)
	if err != nil {
		t.Fatal(err)
	}
	ws.bridge = manager
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "http://127.0.0.1/api/bridge/status", nil))
	if w.Code != 502 || strings.Contains(w.Body.String(), secret) {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestBridgeTokenFileValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bridge.key")
	secret := strings.Repeat("c", 64)
	if err := os.WriteFile(path, []byte(secret+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := readBridgeToken(path); err != nil || got != secret {
		t.Fatal(got, err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := readBridgeToken(path); err == nil {
			t.Fatal("public token file accepted")
		}
	}
}

func TestUSBBridgeWebUsesSharedAuthenticationAndSessionIdentity(t *testing.T) {
	secret := strings.Repeat("d", 64)
	var requests atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+secret {
			t.Error("missing bridge token")
		}
		if r.URL.Path == "/v1/scan" {
			_ = json.NewEncoder(w).Encode(chameleon.BridgeScan{APIVersion: 1, Devices: []chameleon.BridgeDevice{}})
			return
		}
		_ = json.NewEncoder(w).Encode(chameleon.BridgeStatus{APIVersion: 1, State: "disconnected", DataAddress: "127.0.0.1:12345", Transport: chameleon.TransportInfo{Connection: "tcp", Medium: "usb-serial", Managed: true}})
	}))
	defer remote.Close()
	manager, err := chameleon.NewUSBBridge(remote.URL, secret)
	if err != nil {
		t.Fatal(err)
	}
	ws := &webServer{bridge: manager, sessionEnabled: true, timeout: time.Second}
	ws.rotateSessionID()
	h := offlineWeb(t, ws)
	for _, tc := range []struct {
		path, token, session string
		want                 int
	}{
		{"/api/bridge/scan", "", ws.currentSessionID(), 403},
		{"/api/bridge/scan", ws.token, "old-session", 409},
		{"/api/bridge/scan", ws.token, ws.currentSessionID(), 200},
		{"/api/session/connect", ws.token, ws.currentSessionID(), 409},
	} {
		r := httptest.NewRequest("POST", "http://127.0.0.1"+tc.path, strings.NewReader(`{}`))
		r.Header.Set("X-Chameleon-Token", tc.token)
		r.Header.Set("X-Chameleon-Session", tc.session)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatal(tc, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("credential exposed")
		}
	}
	if requests.Load() != 1 {
		t.Fatal("stale or untrusted request reached bridge", requests.Load())
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "http://127.0.0.1/api/capabilities", nil))
	var caps map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &caps); err != nil {
		t.Fatal(err)
	}
	if caps["usb_bridge_enabled"] != true || caps["ble_bridge_enabled"] != false {
		t.Fatal(caps)
	}
	status := ws.sessionStatus().(map[string]any)
	if status["kind"] != "usb-bridge" || status["connected"] != false {
		t.Fatal(status)
	}
}

func TestServeUSBBridgeFlagsRejectAmbiguousConfiguration(t *testing.T) {
	for _, args := range [][]string{
		{"serve", "--usb-bridge", "http://127.0.0.1:12346"},
		{"serve", "--usb-bridge", "http://127.0.0.1:12346", "--bridge-token-file", "missing", "--port", "COM7"},
		{"serve", "--usb-bridge", "http://127.0.0.1:12346", "--ble-bridge", "http://127.0.0.1:12347", "--bridge-token-file", "missing"},
	} {
		if err := runServe(args); err == nil {
			t.Fatal("ambiguous configuration accepted", args)
		}
	}
}
