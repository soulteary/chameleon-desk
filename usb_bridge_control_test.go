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

func usbBridgeStatus() BridgeStatus {
	return BridgeStatus{APIVersion: 1, State: "disconnected", Transport: TransportInfo{Connection: "tcp", Medium: "usb-serial", Managed: true}, DataAddress: "127.0.0.1:12345"}
}

func TestUSBBridgeManagementRequestsAndPortMetadata(t *testing.T) {
	secret := strings.Repeat("a", 64)
	paths := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secret || r.Header.Get("Origin") != "" {
			t.Error("invalid credentials or origin")
		}
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/v1/scan" {
			_, _ = w.Write([]byte(`{"api_version":1,"devices":[{"id":"COM7","name":"Application","vid":26728,"pid":34438,"serial_number":"serial","location":"usb-1"}]}`))
			return
		}
		_ = json.NewEncoder(w).Encode(usbBridgeStatus())
	}))
	defer server.Close()
	b, err := NewUSBBridge(server.URL, secret)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if b.Medium() != "usb-serial" {
		t.Fatal(b.Medium())
	}
	scan, err := b.Scan(ctx)
	if err != nil || len(scan.Devices) != 1 || scan.Devices[0].VID == nil || *scan.Devices[0].VID != 26728 || scan.Devices[0].SerialNumber != "serial" {
		t.Fatal(scan, err)
	}
	if _, err = b.Select(ctx, "COM7"); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Status(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 5 {
		t.Fatal(paths)
	}
}

func TestUSBBridgeRejectsWrongMediumPrivilegesAndDestinations(t *testing.T) {
	for _, mutate := range []func(*BridgeStatus){
		func(s *BridgeStatus) { s.Transport.Medium = "ble" },
		func(s *BridgeStatus) { s.Transport.Managed = false },
		func(s *BridgeStatus) { s.Transport.AllowsPairingChanges = true },
		func(s *BridgeStatus) { s.Transport.AllowsDeviceRestore = true },
		func(s *BridgeStatus) { s.Transport.SupportsDFU = true },
		func(s *BridgeStatus) { s.DataAddress = "192.0.2.1:12345" },
		func(s *BridgeStatus) { s.State = "unknown" },
	} {
		status := usbBridgeStatus()
		mutate(&status)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(status) }))
		b, err := NewUSBBridge(server.URL, strings.Repeat("a", 64))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = b.Status(context.Background()); err == nil {
			t.Fatalf("accepted %+v", status)
		}
		server.Close()
	}
	if _, err := NewUSBBridge("http://192.0.2.1:12346", strings.Repeat("a", 64)); err == nil {
		t.Fatal("remote control origin accepted")
	}
}

func TestUSBBridgeSessionBindingAndRestrictions(t *testing.T) {
	for _, variant := range []string{"selected", "other", "ble", "unmanaged"} {
		t.Run(variant, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			status := usbBridgeStatus()
			status.State = "connected"
			status.SelectedDevice = &BridgeDevice{ID: "COM7"}
			status.DataAddress = listener.Addr().String()
			transport := status.Transport
			id := "COM7"
			switch variant {
			case "other":
				id = "COM8"
			case "ble":
				transport.Medium = "ble"
			case "unmanaged":
				transport.Managed = false
			}
			done := make(chan struct{})
			go func() {
				defer close(done)
				conn, e := listener.Accept()
				if e != nil {
					return
				}
				defer conn.Close()
				data, _ := json.Marshal(map[string]any{"api_version": 1, "transport": transport, "device_id": id})
				_, _ = conn.Write(append(append([]byte("CHAMELEON_BRIDGE/1 "), data...), '\n'))
				var buf [1]byte
				_, _ = conn.Read(buf[:])
			}()
			b := &USBBridge{control: &BLEBridge{medium: "usb-serial"}}
			c, err := b.OpenSession(context.Background(), status, time.Second)
			if variant != "selected" {
				if err == nil {
					c.Close()
					t.Fatal("wrong session accepted")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if c.TransportInfo() != status.Transport {
					t.Fatal(c.TransportInfo())
				}
				for _, cmd := range []Command{CmdSetBlePairingKey, CmdResetSettings, CmdEnterBootloader} {
					if _, err := c.Execute(context.Background(), cmd, nil); err == nil {
						t.Fatal(cmd)
					}
				}
				if err := c.EnterBootloader(context.Background()); err == nil {
					t.Fatal("typed bootloader entry accepted")
				}
				if _, err := c.RestoreDevice(context.Background(), DeviceBackup{}, RestoreOptions{}); err == nil {
					t.Fatal("restore accepted")
				}
				if c.Stats().Requests != 0 {
					t.Fatal(c.Stats())
				}
				c.Close()
			}
			<-done
		})
	}
}

func TestUSBBridgeFeatureConfigurationIsSeparateFromBLE(t *testing.T) {
	for _, medium := range []string{"", "ble", "usb-serial"} {
		ctx := featureTestContext()
		ctx.BridgeConfigured = true
		ctx.BridgeMedium = medium
		usb, err := EvaluateFeature("bridge.usb-scan", "web", ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		ble, err := EvaluateFeature("bridge.scan", "web", ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if (usb.State == "available") != (medium == "usb-serial") || (ble.State == "available") == (medium == "usb-serial") {
			t.Fatal(medium, usb, ble)
		}
		ctx.Device.Transport = usbBridgeStatus().Transport
		for _, id := range []string{"device.pairing", "backup.restore", "firmware.flash", "firmware.enter"} {
			state, err := EvaluateFeature(id, "sdk", ctx, nil)
			if err != nil || state.State != "unavailable" || !hasFeatureReason(state, "transport_restricted") {
				t.Fatal(id, state, err)
			}
		}
	}
}
