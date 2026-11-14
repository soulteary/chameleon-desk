package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	chameleon "github.com/soulteary/chameleon-desk"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type step struct {
	cmd     uint16
	payload []byte
	status  uint16
	data    []byte
}

func mockDevice(t *testing.T, steps []step) func(string, time.Duration) (*chameleon.Client, error) {
	t.Helper()
	local, peer := net.Pipe()
	c := chameleon.NewClient(local, 3*time.Second)
	done := make(chan struct{})
	consumed := 0
	go func() {
		defer close(done)
		for _, s := range steps {
			header := make([]byte, 9)
			if _, e := io.ReadFull(peer, header); e != nil {
				return
			}
			payload := make([]byte, int(binary.BigEndian.Uint16(header[6:8]))+1)
			if _, e := io.ReadFull(peer, payload); e != nil {
				return
			}
			cmd := binary.BigEndian.Uint16(header[2:4])
			if cmd != s.cmd || !bytes.Equal(payload[:len(payload)-1], s.payload) {
				t.Errorf("got %d/%x expected %d/%x", cmd, payload[:len(payload)-1], s.cmd, s.payload)
			}
			consumed++
			b := make([]byte, len(s.data)+10)
			b[0] = 0x11
			b[1] = 0xef
			binary.BigEndian.PutUint16(b[2:], s.cmd)
			binary.BigEndian.PutUint16(b[4:], s.status)
			binary.BigEndian.PutUint16(b[6:], uint16(len(s.data)))
			for _, v := range b[:8] {
				b[8] -= v
			}
			copy(b[9:], s.data)
			for _, v := range s.data {
				b[len(b)-1] -= v
			}
			if _, e := peer.Write(b); e != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		_ = c.Close()
		_ = peer.Close()
		<-done
		if consumed != len(steps) {
			t.Errorf("consumed %d/%d", consumed, len(steps))
		}
	})
	return func(string, time.Duration) (*chameleon.Client, error) { return c, nil }
}
func noOpen(t *testing.T) func(string, time.Duration) (*chameleon.Client, error) {
	return func(string, time.Duration) (*chameleon.Client, error) {
		t.Error("hardware opened unexpectedly")
		return nil, fmt.Errorf("unexpected open")
	}
}
func TestMutationsRequireIntentBeforeOpen(t *testing.T) {
	for _, args := range [][]string{{"slot", "select", "--port", "mock"}, {"settings", "factory-reset", "--port", "mock"}, {"mode", "--port", "mock"}, {"mfc", "write", "--port", "mock"}, {"image", "load", "--port", "mock"}, {"call", "SET_ACTIVE_SLOT", "--data", "00", "--port", "mock"}, {"firmware", "enter", "--port", "mock"}, {"raw", "--data", "60", "--port", "mock"}, {"sniff", "--port", "mock"}} {
		if e := runExtendedWith(args, noOpen(t)); e == nil || !strings.Contains(e.Error(), "--yes") {
			t.Fatalf("%v: %v", args, e)
		}
	}
}
func TestOfflineCommandCatalog(t *testing.T) {
	out := filepath.Join(t.TempDir(), "catalog.json")
	if e := runExtendedWith([]string{"commands", "--output", out}, noOpen(t)); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(out)
	if e != nil {
		t.Fatal(e)
	}
	var data []chameleon.CommandInfo
	if e = json.Unmarshal(b, &data); e != nil || len(data) != 158 {
		t.Fatal(len(data), e)
	}
}
func TestCallResponseAndCapabilityPreflight(t *testing.T) {
	out := filepath.Join(t.TempDir(), "response.json")
	open := mockDevice(t, []step{{cmd: 1035, status: 0x68, data: []byte{3, 0xe8}}, {cmd: 1000, status: 0x68, data: []byte{2, 2}}})
	if e := runExtendedWith([]string{"call", "GET_APP_VERSION", "--port", "mock", "--output", out}, open); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(out)
	var r chameleon.Response
	if e := json.Unmarshal(b, &r); e != nil || r.Data.String() != "0202" {
		t.Fatalf("%s %v", b, e)
	}
}
func TestCallUnsupportedDoesNotSend(t *testing.T) {
	open := mockDevice(t, []step{{cmd: 1035, status: 0x68, data: []byte{3, 0xe8}}})
	if e := runExtendedWith([]string{"call", "GET_BATTERY_INFO_EX", "--port", "mock"}, open); e == nil {
		t.Fatal("unsupported command sent")
	}
}
func TestSettingsSecretsRedacted(t *testing.T) {
	data := make([]byte, 13)
	data[0] = 7
	copy(data[7:], "456789")
	out := filepath.Join(t.TempDir(), "settings.json")
	open := mockDevice(t, []step{{cmd: 1034, status: 0x68, data: data}})
	if e := runExtendedWith([]string{"settings", "get", "--port", "mock", "--output", out}, open); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(out)
	if bytes.Contains(b, []byte("456789")) || bytes.Contains(b, []byte("ble_key")) || bytes.Contains(b, []byte("raw")) {
		t.Fatalf("secret leaked: %s", b)
	}
}
func TestSlotSetterEncoding(t *testing.T) {
	open := mockDevice(t, []step{{cmd: 1006, status: 0x68, payload: []byte{7, 1, 0}}})
	if e := runExtendedWith([]string{"slot", "enable", "--port", "mock", "--slot", "8", "--sense", "lf", "--enabled=false", "--yes"}, open); e != nil {
		t.Fatal(e)
	}
}
func TestBinaryOutputGuardBeforeMutation(t *testing.T) {
	if e := runExtendedWith([]string{"slot", "select", "--yes", "--binary", "--port", "mock"}, noOpen(t)); e == nil {
		t.Fatal("invalid binary output accepted")
	}
}
func TestEmulatorBinaryRead(t *testing.T) {
	types := make([]byte, 32)
	types[0] = 3
	types[1] = 0xe9
	expected := bytes.Repeat([]byte{0xaa}, 16)
	out := filepath.Join(t.TempDir(), "data.bin")
	open := mockDevice(t, []step{{cmd: 1018, status: 0x68, data: []byte{0}}, {cmd: 1019, status: 0x68, data: types}, {cmd: 4008, status: 0x68, payload: []byte{0, 1}, data: expected}})
	if e := runExtendedWith([]string{"emulator", "read", "--port", "mock", "--binary", "--output", out}, open); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(out)
	if e != nil || !bytes.Equal(b, expected) {
		t.Fatal(e)
	}
}
func TestDecodeHexForms(t *testing.T) {
	for _, s := range []string{"0xAABB", "aa:bb", "aa bb\r\n"} {
		b, e := decodeHex(s)
		if e != nil || !bytes.Equal(b, []byte{0xaa, 0xbb}) {
			t.Fatalf("%q %x %v", s, b, e)
		}
	}
	for _, s := range []string{"a", "xx"} {
		if _, e := decodeHex(s); e == nil {
			t.Fatal(s)
		}
	}
}
func TestAtomicFilePrivate(t *testing.T) {
	p := filepath.Join(t.TempDir(), "test.json")
	if e := atomicFile(p, []byte("a")); e != nil {
		t.Fatal(e)
	}
	if e := atomicFile(p, []byte("new")); !os.IsExist(e) {
		t.Fatalf("expected existing-file error, got %v", e)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "a" {
		t.Fatal(string(b))
	}
	_ = context.Background()
}

func TestSlotDeleteAlsoClearsNickname(t *testing.T) {
	open := mockDevice(t, []step{{cmd: 1024, payload: []byte{1, 1}, status: 0x68}, {cmd: 1008, payload: []byte{1, 1}, status: 0x68, data: []byte("old")}, {cmd: 1021, payload: []byte{1, 1}, status: 0x68}})
	if e := runExtendedWith([]string{"slot", "delete", "--slot", "2", "--sense", "lf", "--yes", "--port", "mock"}, open); e != nil {
		t.Fatal(e)
	}
}

func TestSettingActionsGolden(t *testing.T) {
	cases := []struct {
		args    []string
		cmd     uint16
		payload []byte
	}{
		{[]string{"settings", "set", "--name", "animation", "--value", "3"}, 1015, []byte{3}},
		{[]string{"settings", "set", "--name", "sleep", "--value", "30"}, 1040, []byte{30}},
		{[]string{"settings", "set", "--name", "long-press", "--value", "500"}, 1042, []byte{1, 0xf4}},
		{[]string{"settings", "set", "--name", "button", "--button", "B", "--long", "--value", "4"}, 1029, []byte{'B', 4}},
		{[]string{"settings", "set", "--name", "ble-key", "--key", "123456"}, 1030, []byte("123456")},
		{[]string{"settings", "set", "--name", "ble-pairing", "--enabled"}, 1037, []byte{1}},
		{[]string{"settings", "save"}, 1013, nil},
		{[]string{"settings", "reset"}, 1014, nil},
		{[]string{"settings", "factory-reset"}, 1020, nil},
		{[]string{"settings", "clear-bonds"}, 1032, nil},
		{[]string{"mode", "--reader"}, 1001, []byte{1}},
		{[]string{"slot", "select", "--slot", "8"}, 1003, []byte{7}},
		{[]string{"slot", "type", "--slot", "2", "--type", "1001"}, 1004, []byte{1, 3, 0xe9}},
		{[]string{"slot", "init", "--slot", "2", "--type", "1001"}, 1005, []byte{1, 3, 0xe9}},
		{[]string{"slot", "nickname", "--slot", "2", "--sense", "hf", "--name", "demo"}, 1007, []byte{1, 2, 'd', 'e', 'm', 'o'}},
		{[]string{"slot", "save"}, 1009, nil},
		{[]string{"emulator", "flag", "--name", "gen1a", "--enabled"}, 4011, []byte{1}},
		{[]string{"emulator", "write-mode", "--kind", "type2", "--value", "2"}, 4032, []byte{2}},
		{[]string{"emulator", "identity", "--uid", "11223344", "--atqa", "0x0004", "--sak", "0x08"}, 4001, []byte{4, 0x11, 0x22, 0x33, 0x44, 4, 0, 8, 0}},
		{[]string{"emulator", "lf-set", "--protocol", "em410x", "--data", "0102030405"}, 5000, []byte{1, 2, 3, 4, 5}},
	}
	for _, v := range cases {
		v := v
		t.Run(strings.Join(v.args, "_"), func(t *testing.T) {
			open := mockDevice(t, []step{{cmd: v.cmd, payload: v.payload, status: 0x68}})
			args := append(append([]string(nil), v.args...), "--yes", "--port", "mock")
			if e := runExtendedWith(args, open); e != nil {
				t.Fatal(e)
			}
		})
	}
}
func TestType2CLIReadSlicing(t *testing.T) {
	data := make([]byte, 16)
	for i := range data {
		data[i] = byte(i)
	}
	out := filepath.Join(t.TempDir(), "pages.bin")
	open := mockDevice(t, []step{{cmd: 1002, status: 0x68, data: []byte{1}}, {cmd: 2010, status: 0, payload: []byte{0xf4, 1, 0xf4, 0, 16, 0x30, 4}, data: data}})
	if e := runExtendedWith([]string{"type2", "read", "--port", "mock", "--start", "5", "--count", "2", "--binary", "--output", out}, open); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(out)
	if !bytes.Equal(b, data[4:12]) {
		t.Fatalf("%x", b)
	}
}
func TestClassicCLIReadAndWrite(t *testing.T) {
	for _, write := range []bool{false, true} {
		name := "read"
		cmd := uint16(2008)
		payload := []byte{0x60, 4, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
		data := make([]byte, 16)
		args := []string{"mfc", name, "--block", "4", "--key", "FFFFFFFFFFFF", "--port", "mock"}
		if write {
			name = "write"
			cmd = 2009
			payload = append(payload, data...)
			args = []string{"mfc", name, "--block", "4", "--key", "FFFFFFFFFFFF", "--data", strings.Repeat("00", 16), "--yes", "--port", "mock"}
			data = nil
		}
		open := mockDevice(t, []step{{cmd: 1002, status: 0x68, data: []byte{1}}, {cmd: cmd, payload: payload, status: 0, data: data}})
		if e := runExtendedWith(args, open); e != nil {
			t.Fatal(e)
		}
	}
}
func TestCLIReaderCallAndRecoveryListing(t *testing.T) {
	open := mockDevice(t, []step{{cmd: 1035, status: 0x68, data: []byte{7, 0xd8}}, {cmd: 1002, status: 0x68, data: []byte{0}}, {cmd: 1001, status: 0x68, payload: []byte{1}}, {cmd: 2008, status: 0, data: make([]byte, 16), payload: []byte{0x60, 4, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}}, {cmd: 1001, status: 0x68, payload: []byte{0}}})
	if e := runExtendedWith([]string{"call", "MF1_READ_ONE_BLOCK", "--reader", "--yes", "--data", "6004FFFFFFFFFFFF", "--port", "mock"}, open); e != nil {
		t.Fatal(e)
	}
	if e := runExtendedWith([]string{"recover"}, noOpen(t)); e != nil {
		t.Fatal(e)
	}
}
func TestT55CLIKnownPasswords(t *testing.T) {
	p := []byte{1, 2, 3, 4, 5, 0x20, 0x20, 0x66, 0x66, 0x51, 0x24, 0x36, 0x48}
	open := mockDevice(t, []step{{cmd: 1002, status: 0x68, data: []byte{1}}, {cmd: 3001, status: 0x40, payload: p}})
	if e := runExtendedWith([]string{"t55", "--protocol", "em410x", "--data", "0102030405", "--new-key", "20206666", "--old-keys", "51243648", "--yes", "--port", "mock"}, open); e != nil {
		t.Fatal(e)
	}
}
func TestEmulatorQueries(t *testing.T) {
	for _, v := range []struct {
		args []string
		cmd  uint16
		data []byte
	}{{[]string{"emulator", "get"}, 4009, []byte{0, 1, 0, 0, 0}}, {[]string{"emulator", "identity"}, 4018, []byte{4, 1, 2, 3, 4, 4, 0, 8, 0}}, {[]string{"emulator", "lf-get"}, 5001, []byte{1, 2, 3, 4, 5}}} {
		args := append(v.args, "--port", "mock")
		if e := runExtendedWith(args, mockDevice(t, []step{{cmd: v.cmd, status: 0x68, data: v.data}})); e != nil {
			t.Fatal(e)
		}
	}
}

func TestCallUnregisteredCommandsRefusedWithHint(t *testing.T) {
	cases := map[string]string{
		"GENERIC_READ":      "ADC_GENERIC_READ",
		"CORR_GENERIC_READ": "ADC_GENERIC_READ",
		"EM4X05_READSNIFF":  "EM4X05_SCAN",
	}
	for name, alt := range cases {
		e := runExtendedWith([]string{"call", name, "--yes", "--port", "mock"}, noOpen(t))
		if e == nil || !strings.Contains(e.Error(), "no handler in the pinned firmware") || !strings.Contains(e.Error(), alt) {
			t.Fatalf("%s: expected an unregistered-command hint naming %s, got %v", name, alt, e)
		}
	}
	// --force bypasses the refusal (forward-compatible), so it reaches device
	// open instead of the unregistered-command message.
	failOpen := func(string, time.Duration) (*chameleon.Client, error) { return nil, fmt.Errorf("device unavailable") }
	if e := runExtendedWith([]string{"call", "GENERIC_READ", "--force", "--yes", "--port", "mock"}, failOpen); e == nil || strings.Contains(e.Error(), "no handler") {
		t.Fatalf("--force should bypass the unregistered refusal, got %v", e)
	}
}
