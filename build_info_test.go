package chameleon

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBuildPairingAndLegacyCompatibility(t *testing.T) {
	core := CoreBuildInfo()
	if core.Release != SDKVersion || core.Compatibility != 1 {
		t.Fatal(core)
	}
	if err := CheckBridgeVersion(nil); err != nil {
		t.Fatal(err)
	}
	if err := CheckBridgeVersion(&core); err != nil {
		t.Fatal(err)
	}
	for _, v := range []BuildInfo{{Release: "other", Compatibility: 1}, {Release: core.Release, Compatibility: 2}, {}} {
		if !errors.Is(CheckBridgeVersion(&v), ErrBridgeVersionMismatch) {
			t.Fatal(v)
		}
	}
	core.Revision = strings.Repeat("a", 40)
	peer := core
	peer.Revision = strings.Repeat("b", 40)
	if !errors.Is(checkBridgeVersion(core, &peer), ErrBridgeVersionMismatch) {
		t.Fatal("different stamped revisions accepted")
	}
	peer.Revision = ""
	if err := checkBridgeVersion(core, &peer); err != nil {
		t.Fatal("source revision must remain unverified", err)
	}
}

func TestBridgeDoctorOnlyReadsStatus(t *testing.T) {
	for _, medium := range []string{"ble", "usb-serial"} {
		t.Run(medium, func(t *testing.T) {
			requests := 0
			status := testBridgeStatus()
			status.Transport.Medium = medium
			build := CoreBuildInfo()
			status.Build = &build
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Method != "GET" || r.URL.Path != "/v1/status" {
					t.Error(r.Method, r.URL.Path)
				}
				_ = json.NewEncoder(w).Encode(status)
			}))
			defer server.Close()
			b, _ := NewBLEBridge(server.URL, strings.Repeat("a", 64))
			b.medium = medium
			report := b.Diagnose(context.Background())
			if len(report.Checks) == 0 || requests != 1 {
				t.Fatal(report, requests)
			}
			for _, c := range report.Checks {
				if c.Status == "fail" {
					t.Fatal(report)
				}
			}
			status.Build = &BuildInfo{Release: "wrong", Compatibility: 1}
			report = b.Diagnose(context.Background())
			if report.Checks[0].Code != "version_mismatch" || requests != 2 {
				t.Fatal(report, requests)
			}
		})
	}
}

func TestBridgeDoctorSanitizesAuthenticationAndBackendErrors(t *testing.T) {
	secret := "sensitive backend text"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, secret, http.StatusUnauthorized) }))
	defer server.Close()
	b, _ := NewBLEBridge(server.URL, strings.Repeat("a", 64))
	v := b.Diagnose(context.Background())
	data, _ := json.Marshal(v)
	if v.Checks[0].Code != "authentication_failed" || strings.Contains(string(data), secret) {
		t.Fatal(string(data))
	}
}

func TestVersionMismatchOnDataStreamSendsNoFirmwareCommands(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan int, 1)
	go func() {
		conn, e := listener.Accept()
		if e != nil {
			done <- -1
			return
		}
		defer conn.Close()
		payload, _ := json.Marshal(map[string]any{"api_version": 1, "bridge_version": BuildInfo{Release: "wrong", Compatibility: 1}, "transport": bleTransport("tcp", false)})
		_, _ = conn.Write(append(append([]byte("CHAMELEON_BRIDGE/1 "), payload...), '\n'))
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		buf := make([]byte, 1)
		n, _ := conn.Read(buf)
		done <- n
	}()
	c, err := OpenContext(context.Background(), "tcp:"+listener.Addr().String(), time.Second)
	if c != nil || !errors.Is(err, ErrBridgeVersionMismatch) {
		t.Fatal(c, err)
	}
	if n := <-done; n != 0 {
		t.Fatalf("sent %d bytes to incompatible bridge", n)
	}
}
