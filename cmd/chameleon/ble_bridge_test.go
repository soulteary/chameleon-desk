package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	chameleon "github.com/soulteary/chameleon-desk"
)

func TestBLEScanUsesAuthenticatedBridge(t *testing.T) {
	token := strings.Repeat("a", 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/scan" || r.Header.Get("Authorization") != "Bearer "+token {
			t.Errorf("unexpected bridge request: %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(chameleon.BridgeScan{APIVersion: 1, Devices: []chameleon.BridgeDevice{{ID: "peripheral", Name: "Chameleon"}}})
	}))
	defer server.Close()
	dir := t.TempDir()
	key, output := filepath.Join(dir, "bridge.key"), filepath.Join(dir, "scan.json")
	if err := os.WriteFile(key, []byte(token), 0600); err != nil {
		t.Fatal(err)
	}
	open := func(string, time.Duration) (*chameleon.Client, error) {
		t.Error("scan opened a device")
		return nil, fmt.Errorf("unexpected device access")
	}
	if err := runExtendedWith([]string{"ble", "scan", "--ble-bridge", server.URL, "--bridge-token-file", key, "--output", output}, open); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var scan chameleon.BridgeScan
	if err := json.Unmarshal(data, &scan); err != nil || len(scan.Devices) != 1 || scan.Devices[0].ID != "peripheral" {
		t.Fatalf("invalid scan: %s %v", data, err)
	}
	if strings.Contains(string(data), token) {
		t.Fatal("credential exposed")
	}
}

func TestBLEScanRequiresExplicitBridgeConfiguration(t *testing.T) {
	open := func(string, time.Duration) (*chameleon.Client, error) {
		t.Error("scan opened a device")
		return nil, fmt.Errorf("unexpected device access")
	}
	for _, args := range [][]string{{"ble", "scan"}, {"ble", "scan", "--ble-bridge", "http://127.0.0.1:12346"}} {
		if err := runExtendedWith(args, open); err == nil || !strings.Contains(err.Error(), "--bridge-token-file") {
			t.Fatal(err)
		}
	}
}
