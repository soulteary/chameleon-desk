package chameleon

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestTransportRestrictionsRejectBeforeAnyRequest(t *testing.T) {
	for _, info := range []TransportInfo{bleTransport("ble", false), bleTransport("tcp", true), {Connection: "tcp", Medium: "unknown"}, {Connection: "tcp", Medium: "usb-serial"}} {
		t.Run(info.Connection+"-"+info.Medium, func(t *testing.T) {
			a, b := net.Pipe()
			c := newClient(a, 30*time.Millisecond)
			c.transportInfo = info
			defer c.Close()
			defer b.Close()
			for _, command := range []Command{CmdSetBlePairingKey, CmdSetBlePairingEnable, CmdDeleteAllBleBonds, CmdResetSettings, CmdWipeFds} {
				if _, err := c.Execute(context.Background(), command, nil); err == nil || !strings.Contains(err.Error(), "direct serial") {
					t.Fatalf("%s: %v", command, err)
				}
			}
			key := "123456"
			animation := byte(1)
			if _, err := c.ConfigureSettings(context.Background(), SettingsPatch{Animation: &animation, BLEKey: &key}, false); err == nil {
				t.Fatal("mixed patch accepted")
			}
			if _, err := c.RestoreDevice(context.Background(), DeviceBackup{}, RestoreOptions{}); err == nil || !strings.Contains(err.Error(), "direct serial") {
				t.Fatalf("restore: %v", err)
			}
			if c.Stats().Requests != 0 || c.IsClosed() {
				t.Fatalf("restriction changed connection state: %+v", c.Stats())
			}
		})
	}
}

func bridgeDeclaration(managed bool, id string) []byte {
	b, _ := json.Marshal(map[string]any{"api_version": 1, "transport": bleTransport("tcp", managed), "device_id": id})
	return append(append([]byte("CHAMELEON_BRIDGE/1 "), b...), '\n')
}

func TestTCPDeclarationRetainsFragmentedProtocolResponse(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	go func() {
		header := bridgeDeclaration(true, "device")
		for _, part := range [][]byte{header[:3], header[3:]} {
			if _, err := b.Write(part); err != nil {
				return
			}
		}
		request := make([]byte, 10)
		if _, err := io.ReadFull(b, request); err == nil {
			_, _ = b.Write(response(1000, 0x68, []byte{2, 0}))
		}
	}()
	c, err := tcpClient(context.Background(), a, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.TransportInfo() != bleTransport("tcp", true) || c.bridgeDeviceID != "device" {
		t.Fatalf("%+v", c.TransportInfo())
	}
	if version, err := c.Firmware(context.Background()); err != nil || version != (Version{2, 0}) {
		t.Fatalf("%+v %v", version, err)
	}
}

func TestLegacyTCPWithoutDeclarationRemainsRestrictedAndUsable(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	go func() {
		request := make([]byte, 10)
		if _, err := io.ReadFull(b, request); err == nil {
			_, _ = b.Write(response(1000, 0x68, []byte{2, 0}))
		}
	}()
	c, err := tcpClient(context.Background(), a, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if info := c.TransportInfo(); info.Medium != "unknown" || info.AllowsDeviceRestore || info.AllowsPairingChanges {
		t.Fatalf("%+v", info)
	}
	if _, err := c.Firmware(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestTCPDeclarationCannotGrantSerialPermissions(t *testing.T) {
	for _, header := range []string{
		"CHAMELEON_BRIDGE/2 {}\n",
		"CHAMELEON_BRIDGE/1 {\"api_version\":1,\"transport\":{\"connection\":\"tcp\",\"medium\":\"serial\",\"allows_pairing_changes\":true}}\n",
		"CHAMELEON_BRIDGE/1 {bad}\n",
		"CHAMELEON_BRIDGE/1 " + strings.Repeat("x", 5000) + "\n",
	} {
		a, b := net.Pipe()
		go func() { defer b.Close(); _, _ = io.WriteString(b, header) }()
		c, err := tcpClient(context.Background(), a, time.Second)
		if err == nil {
			c.Close()
			t.Fatal("accepted invalid declaration")
		}
	}
}

func TestTCPDeclarationPreservesEarlyLegacyFrame(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	go func() { _, _ = b.Write(response(1000, 0x68, []byte{2, 0})) }()
	c, err := tcpClient(context.Background(), a, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	data := make([]byte, 12)
	if _, err := io.ReadFull(c.port, data); err != nil {
		t.Fatal(err)
	}
	if string(data) != string(response(1000, 0x68, []byte{2, 0})) {
		t.Fatal("legacy bytes consumed")
	}
}

func TestNewClientDetectsAnExistingTCPStream(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	peer, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	c := NewClient(conn, time.Second)
	defer c.Close()
	if c.TransportInfo().AllowsPairingChanges || c.TransportInfo().AllowsDeviceRestore {
		t.Fatal("TCP custom stream elevated permissions")
	}
}

func TestUSBSerialDeclarationIsDescriptiveAndRestricted(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	go func() {
		_, _ = io.WriteString(b, `CHAMELEON_BRIDGE/1 {"api_version":1,"transport":{"connection":"tcp","medium":"usb-serial"},"device_id":"COM7"}`+"\n")
		buf := make([]byte, 10)
		if _, err := io.ReadFull(b, buf); err == nil {
			_, _ = b.Write(response(1000, 0x68, []byte{2, 0}))
		}
	}()
	c, err := tcpClient(context.Background(), a, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.TransportInfo() != (TransportInfo{Connection: "tcp", Medium: "usb-serial"}) || c.bridgeDeviceID != "COM7" {
		t.Fatal(c.TransportInfo())
	}
	if _, err := c.Firmware(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := c.Stats().Requests
	if err := c.EnterBootloader(context.Background()); err == nil || !strings.Contains(err.Error(), "USB serial TCP bridge") {
		t.Fatal(err)
	}
	if c.Stats().Requests != before {
		t.Fatal("bootloader request reached bridge")
	}
	ctx := featureTestContext()
	ctx.Device.Transport = c.TransportInfo()
	aState, err := EvaluateFeature("firmware.enter", "cli", ctx, nil)
	if err != nil || aState.State != "unavailable" || !hasFeatureReason(aState, "transport_restricted") {
		t.Fatal(aState, err)
	}
}

func TestUSBDeclarationCannotAdvertisePrivileges(t *testing.T) {
	for _, extra := range []string{`"allows_pairing_changes":true`, `"allows_device_restore":true`, `"supports_dfu":true`} {
		a, b := net.Pipe()
		go func() {
			defer b.Close()
			_, _ = io.WriteString(b, `CHAMELEON_BRIDGE/1 {"api_version":1,"transport":{"connection":"tcp","medium":"usb-serial",`+extra+`}}`+"\n")
		}()
		c, err := tcpClient(context.Background(), a, time.Second)
		if err == nil {
			c.Close()
			t.Fatal(extra)
		}
	}
}
