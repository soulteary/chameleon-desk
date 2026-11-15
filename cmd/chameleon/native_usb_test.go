package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	chameleon "github.com/soulteary/chameleon-desk"
)

func TestNativeCLIRequiresExplicitMaintenanceIntent(t *testing.T) {
	old := openNativeUSB
	defer func() { openNativeUSB = old }()
	calls := 0
	openNativeUSB = func(context.Context, string, time.Duration) (*chameleon.Client, error) {
		calls++
		t.Error("native USB opened unexpectedly")
		return nil, errors.New("unexpected native open")
	}
	for _, args := range [][]string{
		{"slot", "select", "--slot", "2", "--native-usb", "--yes", "--port", "COM7"},
		{"settings", "set", "--name", "animation", "--native-usb", "--yes", "--port", "COM7"},
		{"settings", "set", "--name", "ble-key", "--key", "123456", "--native-usb", "--port", "COM7"},
		{"settings", "set", "--name", "ble-key", "--key", "bad", "--native-usb", "--yes", "--port", "COM7"},
		{"firmware", "enter", "--native-usb", "--yes"},
		{"call", "ENTER_BOOTLOADER", "--force", "--native-usb", "--yes", "--port", "COM7"},
	} {
		if err := runExtendedWith(args, noOpen(t)); err == nil {
			t.Fatal(args)
		}
	}
	if err := runExtended([]string{"mode", "--yes", "--port", "COM7"}); !errors.Is(err, chameleon.ErrUSBBridgeRequired) {
		t.Fatal(err)
	}
	if _, err := autoPort(); !errors.Is(err, chameleon.ErrUSBBridgeRequired) {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal(calls)
	}
}

func TestNativeCLIPairingUsesOnlyExplicitMaintenanceOpener(t *testing.T) {
	old := openNativeUSB
	defer func() { openNativeUSB = old }()
	calls := 0
	openNativeUSB = func(ctx context.Context, path string, timeout time.Duration) (*chameleon.Client, error) {
		calls++
		if path != "COM7" {
			t.Fatal(path)
		}
		return mockDevice(t, []step{{cmd: 1030, payload: []byte("123456"), status: 0x68}})(path, timeout)
	}
	if err := runExtendedWith([]string{"settings", "set", "--name", "ble-key", "--key", "123456", "--native-usb", "--yes", "--port", "COM7"}, noOpen(t)); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal(calls)
	}
}

func TestRestoreRejectsBridgeAndMissingNativeOptInBeforeOpen(t *testing.T) {
	for _, args := range [][]string{
		{"restore", "--port", "COM7", "--yes"},
		{"restore", "--native-usb", "--port", "tcp:127.0.0.1:12345", "--yes"},
		{"restore", "--native-usb", "--port", "ble:old", "--yes"},
		{"create", "--native-usb", "--port", "COM7", "--yes"},
	} {
		if err := runBackup(args); err == nil {
			t.Fatal(args)
		}
	}
}

func TestRoutineWebSessionRejectsPhysicalSerialTargets(t *testing.T) {
	ws, opens := sessionFixture(t)
	h := offlineWeb(t, ws)
	for _, target := range []string{"COM7", "/dev/ttyACM0", "native-usb:COM7"} {
		req := httptest.NewRequest("POST", "http://127.0.0.1/api/session/connect", strings.NewReader(`{"target":"`+target+`","session_id":"`+ws.currentSessionID()+`"}`))
		req.Header.Set("X-Chameleon-Token", ws.token)
		out := httptest.NewRecorder()
		h.ServeHTTP(out, req)
		if out.Code != 400 || opens.Load() != 0 {
			t.Fatal(target, out.Code, out.Body.String())
		}
	}
}

func TestNativeWebModeFixesPortAndBlocksApplicationEndpoints(t *testing.T) {
	ws, _ := sessionFixture(t)
	ws.nativePort = "COM7"
	ws.allowWrites = true
	h := offlineWeb(t, ws)
	if err := ws.connectSession(context.Background(), "COM8"); err == nil {
		t.Fatal("arbitrary maintenance port accepted")
	}
	for _, path := range []string{"/api/info", "/api/scan/hf", "/api/slot/details"} {
		out := httptest.NewRecorder()
		h.ServeHTTP(out, httptest.NewRequest("GET", "http://127.0.0.1"+path, nil))
		if out.Code != 502 || !strings.Contains(out.Body.String(), "Python bridge") {
			t.Fatal(path, out.Code, out.Body.String())
		}
	}
	old := openNativeUSB
	defer func() { openNativeUSB = old }()
	calls := 0
	openNativeUSB = func(context.Context, string, time.Duration) (*chameleon.Client, error) {
		calls++
		return nil, errors.New("native port unavailable")
	}
	request := func(target string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://127.0.0.1/api/session/connect", strings.NewReader(`{"session_id":"`+ws.currentSessionID()+`","target":"`+target+`"}`))
		r.Header.Set("X-Chameleon-Token", ws.token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := request("COM8"); w.Code != 400 || calls != 0 {
		t.Fatal(w.Code, calls)
	}
	if w := request(""); w.Code != 502 || calls != 1 {
		t.Fatal(w.Code, calls)
	}
	status := ws.sessionStatus().(map[string]any)
	if status["kind"] != "native-usb-maintenance" || status["connected"] != false {
		t.Fatal(status)
	}
	// Offline caller facts cannot change a server's native-maintenance policy.
	model := byte(0)
	state, err := chameleon.EvaluateFeature("reader.hf-scan", "web", ws.featureContext(&chameleon.FeatureDevice{ModelID: &model, Capabilities: []uint16{1035, 1002, 1001, 2000}, Transport: chameleon.TransportInfo{Connection: "tcp"}}), nil)
	if err != nil || state.State != "unavailable" {
		t.Fatal(state, err)
	}
}

func TestServeRejectsMixedMaintenanceAndApplicationOptions(t *testing.T) {
	for _, args := range [][]string{
		{"serve", "--native-usb-maintenance", "COM7", "--port", "tcp:127.0.0.1:12345"},
		{"serve", "--native-usb-maintenance", "COM7", "--usb-bridge", "http://127.0.0.1:12346", "--bridge-token-file", "unused"},
		{"serve", "--native-usb-maintenance", "COM7", "--ble-bridge", "http://127.0.0.1:12346", "--bridge-token-file", "unused"},
	} {
		if err := runServe(args); err == nil {
			t.Fatal(args)
		}
	}
	old := os.Args
	defer func() { os.Args = old }()
	os.Args = []string{"chameleon", "devices"}
	if err := run(); err == nil || !strings.Contains(err.Error(), "usb scan") {
		t.Fatal(err)
	}
}

func TestUSBFeatureCLIUsesExplicitBridgeMedium(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "features-")
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stdout
	os.Stdout = out
	defer func() { os.Stdout = previous; out.Close() }()
	if err := runFeatures([]string{"--id", "bridge.usb-scan", "--surface", "cli", "--bridge-configured", "--bridge-medium", "usb-serial"}); err != nil {
		t.Fatal(err)
	}
	if _, err := out.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	var state chameleon.FeatureAvailability
	if err := json.NewDecoder(out).Decode(&state); err != nil || state.State != "available" {
		t.Fatal(state, err)
	}
	if err := runFeatures([]string{"--bridge-medium", "serial", "--port", "must-not-open"}); err == nil || !strings.Contains(err.Error(), "bridge-medium") {
		t.Fatal(err)
	}
}
