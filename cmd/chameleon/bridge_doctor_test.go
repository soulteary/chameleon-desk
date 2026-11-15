package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	chameleon "github.com/soulteary/chameleon-desk"
)

func TestBridgeDoctorConfigurationAndReadOnlyRequest(t *testing.T) {
	key := filepath.Join(t.TempDir(), "token.key")
	report := bridgeDoctorReport(context.Background(), "http://127.0.0.1:12346", "", key)
	if report.Checks[0].Code != "token_file_invalid" {
		t.Fatal(report)
	}
	if err := os.WriteFile(key, []byte(strings.Repeat("a", 64)), 0600); err != nil {
		t.Fatal(err)
	}
	report = bridgeDoctorReport(context.Background(), "http://192.0.2.1:12346", "", key)
	if report.Checks[0].Code != "bridge_configuration_invalid" {
		t.Fatal(report)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != "GET" || r.URL.Path != "/v1/status" {
			t.Error(r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(chameleon.BridgeStatus{APIVersion: 1, State: "disconnected", DataAddress: "127.0.0.1:12345", Transport: chameleon.TransportInfo{Connection: "tcp", Medium: "usb-serial", Managed: true}})
	}))
	defer server.Close()
	report = bridgeDoctorReport(context.Background(), server.URL, "", key)
	if requests != 1 {
		t.Fatal(requests)
	}
	for _, c := range report.Checks {
		if c.Status == "fail" {
			t.Fatal(report)
		}
	}
}

func TestBridgeDoctorRejectsAmbiguousTargets(t *testing.T) {
	for _, args := range [][]string{{}, {"--usb-bridge", "http://127.0.0.1:1", "--ble-bridge", "http://127.0.0.1:2", "--bridge-token-file", "unused"}, {"--usb-bridge", "http://127.0.0.1:1", "--bridge-token-file", "unused", "--timeout", "0s"}} {
		if runBridgeDoctor(args) == nil {
			t.Fatal(args)
		}
	}
}
