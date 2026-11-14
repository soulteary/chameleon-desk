package chameleon

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTypedSettingPayloads(t *testing.T) {
	cases := []struct {
		name    string
		cmd     uint16
		payload []byte
		run     func(*Client) error
	}{
		{"slot-select", 1003, []byte{7}, func(c *Client) error { return c.SetActiveSlot(context.Background(), 8) }},
		{"slot-type", 1004, []byte{0, 3, 0xe9}, func(c *Client) error { return c.SetSlotType(context.Background(), 1, 1001) }},
		{"slot-enable", 1006, []byte{2, 2, 1}, func(c *Client) error { return c.EnableSlot(context.Background(), 3, SenseHF, true) }},
		{"nickname", 1007, []byte{1, 1, 'a', 'b'}, func(c *Client) error { return c.SetSlotNickname(context.Background(), 2, SenseLF, "ab") }},
		{"threshold", 1042, []byte{0x12, 0x34}, func(c *Client) error { return c.SetLongPressThreshold(context.Background(), 0x1234) }},
		{"button", 1029, []byte{'B', 5}, func(c *Client) error { return c.SetButton(context.Background(), 'B', true, 5) }},
		{"ble-key", 1030, []byte("654321"), func(c *Client) error { return c.SetBLEKey(context.Background(), "654321") }},
		{"counter", 4028, []byte{0x82, 0x56, 0x34, 0x12}, func(c *Client) error { return c.SetEmulatorCounter(context.Background(), 2, 0x123456, true) }},
	}
	for _, v := range cases {
		v := v
		t.Run(v.name, func(t *testing.T) {
			c := readerSimulator(t, []readerStep{{command: v.cmd, request: v.payload, status: 0x68}})
			if e := v.run(c); e != nil {
				t.Fatal(e)
			}
		})
	}
}
func geometry(tag uint16) []readerStep {
	b := make([]byte, 32)
	b[0] = byte(tag >> 8)
	b[1] = byte(tag)
	return []readerStep{{command: 1018, status: 0x68, data: []byte{0}}, {command: 1019, status: 0x68, data: b}}
}
func TestMemoryReadChunking(t *testing.T) {
	steps := geometry(1001)
	steps = append(steps, readerStep{command: 4008, request: []byte{0, 32}, status: 0x68, data: bytes.Repeat([]byte{0xaa}, 512)}, readerStep{command: 4008, request: []byte{32, 32}, status: 0x68, data: bytes.Repeat([]byte{0xbb}, 512)})
	c := readerSimulator(t, steps)
	data, e := c.ReadEmulatorBlocks(context.Background(), 0, 64)
	if e != nil || len(data) != 1024 || data[511] != 0xaa || data[512] != 0xbb {
		t.Fatalf("%d %v", len(data), e)
	}
}
func TestMemoryWrongKindNoWrite(t *testing.T) {
	c := readerSimulator(t, geometry(1001))
	if e := c.WriteEmulatorPages(context.Background(), 0, []byte{1, 2, 3, 4}); e == nil {
		t.Fatal("wrote Type2 data to Classic slot")
	}
}
func TestImageValidationBeforeIO(t *testing.T) {
	c := readerSimulator(t, nil)
	bad := EmulatorImage{Schema: 1, TagType: 1001, UnitBytes: 16, Data: make([]byte, 1024), SHA256: "bad"}
	if e := c.ImportEmulator(context.Background(), bad); e == nil {
		t.Fatal("bad checksum")
	}
	if c.Stats().Requests != 0 {
		t.Fatal("validation performed after IO")
	}
}
func TestImageImportReadback(t *testing.T) {
	data := bytes.Repeat([]byte{0x55}, 1024)
	image := EmulatorImage{1, 1001, 16, 1, data, imageDigest(data)}
	steps := geometry(1001)
	for _, pos := range []int{0, 32} {
		steps = append(steps, readerStep{command: 4000, request: append([]byte{byte(pos)}, data[pos*16:(pos+32)*16]...), status: 0x68})
	}
	for _, pos := range []int{0, 32} {
		steps = append(steps, readerStep{command: 4008, request: []byte{byte(pos), 32}, status: 0x68, data: data[pos*16 : (pos+32)*16]})
	}
	c := readerSimulator(t, steps)
	if e := c.ImportEmulator(context.Background(), image); e != nil {
		t.Fatal(e)
	}
}
func TestImageGeometryMismatchDoesNotWrite(t *testing.T) {
	data := make([]byte, 1024)
	image := EmulatorImage{1, 1001, 16, 1, data, imageDigest(data)}
	c := readerSimulator(t, geometry(1003))
	if e := c.ImportEmulator(context.Background(), image); e == nil {
		t.Fatal("wrong slot geometry accepted")
	}
}
func TestKnownKeyReadRestoresMode(t *testing.T) {
	key := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	p, _ := keyPayload(4, KeyB, key)
	c := readerSimulator(t, []readerStep{{command: 1002, status: 0x68, data: []byte{0}}, {command: 1001, request: []byte{1}, status: 0x68}, {command: 2008, request: p, status: 0, data: make([]byte, 16)}, {command: 1001, request: []byte{0}, status: 0x68}})
	b, e := c.ReadClassicBlock(context.Background(), 4, KeyB, key)
	if e != nil || len(b) != 16 {
		t.Fatal(e)
	}
}
func TestReaderFailureStillRestores(t *testing.T) {
	c := readerSimulator(t, []readerStep{{command: 1002, status: 0x68, data: []byte{0}}, {command: 1001, request: []byte{1}, status: 0x68}, {command: 2008, request: []byte{0x60, 0, 1, 2, 3, 4, 5, 6}, status: 6}, {command: 1001, request: []byte{0}, status: 0x68}})
	var status *StatusError
	_, e := c.ReadClassicBlock(context.Background(), 0, KeyA, []byte{1, 2, 3, 4, 5, 6})
	if !errors.As(e, &status) || status.Status != 6 {
		t.Fatal(e)
	}
}
func TestValidationNoRequests(t *testing.T) {
	c := readerSimulator(t, nil)
	ctx := context.Background()
	for _, e := range []error{c.SetActiveSlot(ctx, 0), c.SetActiveSlot(ctx, 9), c.SetSlotNickname(ctx, 1, SenseHF, strings.Repeat("x", 33)), c.SetBLEKey(ctx, "abc123"), c.SetButton(ctx, 'C', false, 0), c.SetEmulatorCounter(ctx, 3, 0, false), c.SetEmulatorLFID(ctx, "hidprox", make([]byte, 12)), c.SetEmulatorLFID(ctx, "ioprox", make([]byte, 8)), c.WriteClassicBlock(ctx, 0, KeyA, make([]byte, 6), make([]byte, 15)), c.WriteType2Page(ctx, 256, make([]byte, 4))} {
		if e == nil {
			t.Fatal("accepted invalid input")
		}
	}
	if c.Stats().Requests != 0 {
		t.Fatal("invalid input caused IO")
	}
}
func TestClassicGeometry(t *testing.T) {
	for block, sector := range map[int]int{0: 0, 127: 31, 128: 32, 143: 32, 144: 33, 255: 39} {
		s, e := ClassicSector(block)
		if e != nil || s != sector {
			t.Fatal(block, s, e)
		}
	}
	for block, trailer := range map[int]int{0: 3, 127: 127, 128: 143, 144: 159, 255: 255} {
		v, e := ClassicTrailer(block)
		if e != nil || v != trailer {
			t.Fatal(block, v, e)
		}
	}
}
func TestRawEncodingAndTrace(t *testing.T) {
	p, e := (RawOptions{ActivateField: true, WaitResponse: true, AppendCRC: true, AutoSelect: true, CheckResponseCRC: true, TimeoutMS: 500}).payload([]byte{0x60})
	if e != nil || !bytes.Equal(p, []byte{0xf4, 1, 0xf4, 0, 8, 0x60}) {
		t.Fatalf("%x %v", p, e)
	}
	frames, e := ParseTrace([]byte{0x80, 4, 0xa, 0, 8, 0x60})
	if e != nil || len(frames) != 2 || !frames[0].FromCard || frames[0].Bits != 4 {
		t.Fatalf("%+v %v", frames, e)
	}
	for _, b := range [][]byte{{1}, {0, 16, 0}} {
		if _, e = ParseTrace(b); e == nil {
			t.Fatal("truncated trace accepted")
		}
	}
}
func TestSniffUsesEmulatorNotReader(t *testing.T) {
	c := readerSimulator(t, []readerStep{{command: 1002, status: 0x68, data: []byte{1}}, {command: 1001, request: []byte{0}, status: 0x68}, {command: 2020, request: []byte{0, 100}, status: 0x68, data: []byte{0, 8, 0x26}}, {command: 1001, request: []byte{1}, status: 0x68}})
	c.timeout = 2 * time.Second
	frames, e := c.SniffHF(context.Background(), 100)
	if e != nil || len(frames) != 1 {
		t.Fatal(e)
	}
}
func TestBootloaderDoesNotWaitForResponse(t *testing.T) {
	local, peer := net.Pipe()
	c := newClient(local, time.Second)
	defer c.Close()
	defer peer.Close()
	read := make(chan []byte, 1)
	go func() { b := make([]byte, 10); _, _ = io.ReadFull(peer, b); read <- b }()
	if e := c.EnterBootloader(context.Background()); e != nil {
		t.Fatal(e)
	}
	if b := <-read; !bytes.Equal(b, encode(1010)) {
		t.Fatalf("%x", b)
	}
	if c.Stats().Requests != 1 || c.Stats().Responses != 0 {
		t.Fatal(c.Stats())
	}
	if _, e := c.Mode(context.Background()); !errors.Is(e, ErrClosed) {
		t.Fatal(e)
	}
}
func TestRecoveryOfflineKnownVector(t *testing.T) {
	if _, e := os.Stat(filepath.Join("tools", "bin", "mfkey64")); e != nil {
		t.Skip("build optional workers first")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r, e := RunRecovery(ctx, "tools/bin", "mfkey64", "65535D33", "BE2B7B5D", "B1E1B891", "2CF7A248", "36081500")
	if e != nil || !strings.Contains(strings.ToLower(r.Stdout), "974c262b9278") {
		t.Fatalf("%+v %v", r, e)
	}
	if _, e = RunRecovery(ctx, "tools/bin", "../mfkey64"); e == nil {
		t.Fatal("path traversal accepted")
	}
}
func FuzzParseTrace(f *testing.F) {
	f.Add([]byte{0, 8, 0x26})
	f.Fuzz(func(t *testing.T, b []byte) {
		frames, e := ParseTrace(b)
		if e == nil {
			for _, r := range frames {
				if len(r.Data) != (int(r.Bits)+7)/8 {
					t.Fatal("trace length")
				}
			}
		}
	})
}

func TestLFScanTypedAndModeRestore(t *testing.T) {
	c := readerSimulator(t, []readerStep{{command: 1002, status: 0x68, data: []byte{0}}, {command: 1001, request: []byte{1}, status: 0x68}, {command: 3000, status: 0x40, data: []byte{0, 100, 1, 2, 3, 4, 5}}, {command: 1001, request: []byte{0}, status: 0x68}})
	card, e := c.ScanLF(context.Background(), "em410x", 1)
	if e != nil || card.TagType != 100 || card.ID.String() != "0102030405" {
		t.Fatalf("%+v %v", card, e)
	}
}
func TestT55ExplicitKeysPayload(t *testing.T) {
	payload := []byte{1, 2, 3, 4, 5, 0x20, 0x20, 0x66, 0x66, 0x51, 0x24, 0x36, 0x48}
	c := readerSimulator(t, []readerStep{{command: 1002, status: 0x68, data: []byte{0}}, {command: 1001, request: []byte{1}, status: 0x68}, {command: 3001, request: payload, status: 0x40}, {command: 1001, request: []byte{0}, status: 0x68}})
	if e := c.WriteT55(context.Background(), "em410x", []byte{1, 2, 3, 4, 5}, []byte{0x20, 0x20, 0x66, 0x66}, [][]byte{{0x51, 0x24, 0x36, 0x48}}); e != nil {
		t.Fatal(e)
	}
}
func TestAPDULimitsBeforeIO(t *testing.T) {
	c := readerSimulator(t, nil)
	if e := c.AddStaticAPDU(context.Background(), make([]byte, 17), []byte{1}); e == nil {
		t.Fatal("firmware silently truncates long command prefixes")
	}
	if e := c.AddStaticAPDU(context.Background(), []byte{1}, make([]byte, 254)); e == nil {
		t.Fatal("persistent response limit ignored")
	}
	if e := c.SendAPDUResponse(context.Background(), make([]byte, 261)); e == nil {
		t.Fatal("RAM APDU limit ignored")
	}
	if _, e := c.ReaderAPDU(context.Background(), make([]byte, 62)); e == nil {
		t.Fatal("FIFO payload limit ignored")
	}
}
func TestDescribeCommandSources(t *testing.T) {
	d, e := DescribeCommand(CmdMf1ReadOneBlock)
	if e != nil || len(d.Python) == 0 || d.FirmwareLine == nil || *d.FirmwareLine == 0 {
		t.Fatalf("%+v %v", d, e)
	}
	debug, e := DescribeCommand(CmdHf14a4DebugCounters)
	if e != nil || debug.Effect != "read" || !debug.Registered {
		t.Fatalf("%+v %v", debug, e)
	}
}

func TestRecoveryRejectsMalformedArgumentsBeforeLaunch(t *testing.T) {
	for _, tool := range RecoveryTools() {
		if _, e := RunRecovery(context.Background(), "/directory-that-does-not-exist", tool); e == nil || !strings.Contains(e.Error(), "usage:") {
			t.Fatalf("%s: %v", tool, e)
		}
	}
	for _, v := range []struct {
		tool string
		args []string
	}{{"nested", []string{"1", "0", "2", "3", "4"}}, {"nested", []string{"bad", "160", "2", "3", "4"}}, {"staticnested", []string{"1", "0", "2", "3"}}, {"mfkey64", []string{"nothex", "1", "2", "3", "4"}}} {
		if e := validateRecoveryArgs(v.tool, v.args); e == nil {
			t.Fatalf("accepted %+v", v)
		}
	}
}
func TestBoundedRecoveryOutput(t *testing.T) {
	b := &boundedBuffer{limit: 4}
	n, e := b.Write([]byte("abcdef"))
	if n != 6 || e != nil || b.String() != "abcd" || !b.truncated {
		t.Fatalf("%+v %d %v", b, n, e)
	}
}

func TestBundledRecoveryArgumentGuards(t *testing.T) {
	for _, tool := range []string{"nested", "staticnested", "darkside"} {
		tool := tool
		t.Run(tool, func(t *testing.T) {
			p := filepath.Join("tools", "bin", tool)
			if _, e := os.Stat(p); e != nil {
				t.Skip("build optional workers first")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			out, e := exec.CommandContext(ctx, p).CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(e, &exit) || exit.ExitCode() != 1 || len(out) == 0 {
				t.Fatalf("worker should report argument error, not crash: %s %v", out, e)
			}
		})
	}
}

type eofWithDataTransport struct {
	read   bool
	closed bool
}

func (p *eofWithDataTransport) Write(b []byte) (int, error) {
	if p.read {
		return 0, io.ErrClosedPipe
	}
	return len(b), nil
}
func (p *eofWithDataTransport) Read(b []byte) (int, error) {
	p.read = true
	return copy(b, response(1000, 0x68, []byte{2, 2})), io.EOF
}
func (p *eofWithDataTransport) Close() error { p.closed = true; return nil }
func TestResponseBeforeSimultaneousEOF(t *testing.T) {
	p := &eofWithDataTransport{}
	c := newClient(p, time.Second)
	v, e := c.Firmware(context.Background())
	if e != nil || v != (Version{2, 2}) {
		t.Fatalf("valid last response lost: %+v %v", v, e)
	}
	if _, e = c.Firmware(context.Background()); e == nil {
		t.Fatal("closed transport reused")
	}
	if !p.closed {
		t.Fatal("failed transport not closed")
	}
}
func TestSniffNoCaptureRestores(t *testing.T) {
	c := readerSimulator(t, []readerStep{{command: 1002, status: 0x68, data: []byte{0}}, {command: 2020, status: 1, request: []byte{0, 100}}})
	c.timeout = 2 * time.Second
	if _, e := c.SniffHF(context.Background(), 100); !errors.Is(e, ErrNoCapture) {
		t.Fatal(e)
	}
}
