package chameleon

import (
	"context"
	"errors"
	"github.com/soulteary/chameleon-desk/dfu"
	"io"
	"net"
	"testing"
	"time"
)

func maintenanceInfo() TransportInfo { v := serialTransport(); v.NativeUSBMaintenance = true; return v }

type nativePortFixture struct {
	net.Conn
	closed  bool
	dtr     bool
	timeout time.Duration
	reset   bool
	stage   string
	cancel  context.CancelFunc
}

func (p *nativePortFixture) SetDTR(v bool) error {
	p.dtr = v
	if p.cancel != nil {
		p.cancel()
	}
	if p.stage == "dtr" {
		return io.ErrClosedPipe
	}
	return nil
}
func (p *nativePortFixture) SetReadTimeout(v time.Duration) error {
	p.timeout = v
	if p.stage == "timeout" {
		return io.ErrClosedPipe
	}
	return nil
}
func (p *nativePortFixture) ResetInputBuffer() error {
	p.reset = true
	if p.stage == "reset" {
		return io.ErrClosedPipe
	}
	return nil
}
func (p *nativePortFixture) Close() error { p.closed = true; return p.Conn.Close() }

func TestApplicationOpenersNeverOpenNativeSerial(t *testing.T) {
	old := openNativeSerial
	defer func() { openNativeSerial = old }()
	calls := 0
	openNativeSerial = func(string) (nativeSerialPort, error) { calls++; return nil, errors.New("must not open") }
	for _, path := range []string{"", "COM7", "/dev/ttyACM0", "/dev/cu.usbmodem1", "serial:/dev/ttyACM0", "socket://127.0.0.1:12345"} {
		if _, err := OpenContext(context.Background(), path, time.Second); !errors.Is(err, ErrUSBBridgeRequired) {
			t.Fatal(path, err)
		}
		if _, err := Open(path, time.Second); !errors.Is(err, ErrUSBBridgeRequired) {
			t.Fatal(path, err)
		}
	}
	if calls != 0 {
		t.Fatal("application opener accessed native USB", calls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := OpenNativeUSBContext(ctx, "COM7", time.Second); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, path := range []string{"", "tcp:127.0.0.1:12345", "ble:device", "socket://host:123"} {
		if _, err := OpenNativeUSBContext(context.Background(), path, time.Second); err == nil {
			t.Fatal(path)
		}
	}
	if calls != 0 {
		t.Fatal(calls)
	}
}

func TestNativeMaintenanceSetupAndFailureCleanup(t *testing.T) {
	old := openNativeSerial
	defer func() { openNativeSerial = old }()
	for _, stage := range []string{"", "dtr", "timeout", "reset", "cancel"} {
		t.Run(stage, func(t *testing.T) {
			a, b := net.Pipe()
			defer b.Close()
			p := &nativePortFixture{Conn: a, stage: stage}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if stage == "cancel" {
				p.cancel = cancel
			}
			openNativeSerial = func(path string) (nativeSerialPort, error) {
				if path != "COM7" {
					t.Error(path)
				}
				return p, nil
			}
			c, err := OpenNativeUSBContext(ctx, "COM7", time.Second)
			if stage != "" {
				if err == nil || !p.closed {
					t.Fatal(stage, err, p.closed)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !p.dtr || p.timeout != 50*time.Millisecond || !p.reset || c.TransportInfo() != maintenanceInfo() {
				t.Fatal(p, c.TransportInfo())
			}
			c.Close()
			if !p.closed {
				t.Fatal("close lost ownership")
			}
		})
	}
}

func TestNativeMaintenanceRejectsRoutineCommandsBeforeTransmission(t *testing.T) {
	c := readerSimulator(t, nil)
	c.transportInfo = maintenanceInfo()
	ctx := context.Background()
	animation := byte(1)
	key := "123456"
	for _, run := range []func() error{
		func() error { _, e := c.Info(ctx); return e },
		func() error { _, e := c.ScanISO14443A(ctx); return e },
		func() error { return c.SetAnimation(ctx, 1) },
		func() error { return c.SetActiveSlot(ctx, 1) },
		func() error {
			_, e := c.ExecuteWithOptions(ctx, CmdMf1ReadOneBlock, nil, CommandOptions{AllowUnregistered: true})
			return e
		},
		func() error {
			_, e := c.ConfigureSettings(ctx, SettingsPatch{Animation: &animation, BLEKey: &key}, true)
			return e
		},
		func() error { _, e := c.BackupDevice(ctx); return e },
	} {
		if err := run(); !errors.Is(err, ErrUSBBridgeRequired) {
			t.Fatal(err)
		}
	}
	if c.Stats().Requests != 0 || c.IsClosed() {
		t.Fatal(c.Stats())
	}
}

func TestNativeMaintenancePairingReadbackAndSave(t *testing.T) {
	key := "123456"
	raw := append([]byte{6, 0, 0, 0, 0, 0, 1}, []byte(key)...)
	c := readerSimulator(t, []readerStep{{command: 1031, status: 0x68, data: []byte("654321")}, {command: 1030, request: []byte(key), status: 0x68}, {command: 1031, status: 0x68, data: []byte(key)}, {command: 1013, status: 0x68}, {command: 1034, status: 0x68, data: raw}, {command: 1039, status: 103}, {command: 1041, status: 103}})
	c.transportInfo = maintenanceInfo()
	value, err := c.ConfigureSettings(context.Background(), SettingsPatch{BLEKey: &key}, true)
	if err != nil || value.BLEKey != "" || value.Raw != nil {
		t.Fatal(value, err)
	}
}

func TestNativeRestoreScopeDoesNotEnableOtherApplicationCalls(t *testing.T) {
	c := backupSimulator(t, newBackupDevice())
	c.transportInfo = maintenanceInfo()
	ctx := context.Background()
	backup, err := c.BackupForRestore(ctx)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := c.RestoreDevice(ctx, backup, RestoreOptions{Persist: true})
	if err != nil || !restored.Verified || !restored.Persisted {
		t.Fatal(restored, err)
	}
	before := c.Stats().Requests
	if err := c.SetAnimation(ctx, 1); !errors.Is(err, ErrUSBBridgeRequired) {
		t.Fatal(err)
	}
	if _, err := c.BackupDevice(ctx); !errors.Is(err, ErrUSBBridgeRequired) {
		t.Fatal(err)
	}
	if c.Stats().Requests != before {
		t.Fatal("restore scope escaped")
	}
	bridge := readerSimulator(t, nil)
	if _, err := bridge.BackupForRestore(ctx); err == nil || bridge.Stats().Requests != 0 {
		t.Fatal(err)
	}
}

func TestNativeMaintenanceRetainsResetAndBootloader(t *testing.T) {
	c := readerSimulator(t, []readerStep{{command: 1032, status: 0x68}, {command: 1014, status: 0x68}, {command: 1020, status: 0x68}})
	c.transportInfo = maintenanceInfo()
	ctx := context.Background()
	for _, run := range []func(context.Context) error{c.ClearBLEBonds, c.ResetSettings, c.FactoryReset} {
		if err := run(ctx); err != nil {
			t.Fatal(err)
		}
	}
	a, b := net.Pipe()
	c = newClient(a, time.Second)
	c.transportInfo = maintenanceInfo()
	defer c.Close()
	defer b.Close()
	done := make(chan []byte, 1)
	go func() { frame := make([]byte, 10); _, _ = io.ReadFull(b, frame); done <- frame }()
	if err := c.EnterBootloader(ctx); err != nil {
		t.Fatal(err)
	}
	if data := <-done; len(data) != 10 || data[2] != 3 || data[3] != 0xf2 {
		t.Fatalf("%x", data)
	}
}

func TestNativeMaintenanceFeatureScopeAndPairingPatch(t *testing.T) {
	ctx := featureTestContext()
	ctx.Device.Transport = maintenanceInfo()
	for _, id := range []string{"reader.hf-scan", "device.info", "slot.configure", "backup.create", "device.mode"} {
		state, err := EvaluateFeature(id, "sdk", ctx, nil)
		if err != nil || state.State != "unavailable" || !hasFeatureReason(state, "usb_bridge_required") {
			t.Fatal(id, state, err)
		}
	}
	for _, id := range []string{"device.features", "device.settings", "device.pairing", "device.reset", "backup.restore", "firmware.enter"} {
		state, err := EvaluateFeature(id, "sdk", ctx, nil)
		if err != nil || state.State == "unavailable" {
			t.Fatal(id, state, err)
		}
	}
	key := "123456"
	animation := byte(1)
	for _, mixed := range []bool{false, true} {
		p := SettingsPatch{BLEKey: &key}
		if mixed {
			p.Animation = &animation
		}
		state, err := EvaluateFeature("device.configure", "web", ctx, &FeatureRequest{SettingsPatch: &p})
		if err != nil || (state.State == "available") == mixed {
			t.Fatal(state, err)
		}
	}
}

func TestNativeRestoreFailureRetainsCleanupScope(t *testing.T) {
	device := newBackupDevice()
	c := backupSimulator(t, device)
	c.transportInfo = maintenanceInfo()
	backup, err := c.BackupForRestore(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	device.badRead = true
	result, err := c.RestoreDevice(context.Background(), backup, RestoreOptions{Persist: true})
	if err == nil || errors.Is(err, ErrUSBBridgeRequired) || result.Verified || result.Persisted {
		t.Fatal(result, err)
	}
	if device.mode != backup.Mode || int(device.active)+1 != backup.ActiveSlot {
		t.Fatal("restore cleanup lost its maintenance scope")
	}
	if err := c.SetActiveSlot(context.Background(), 2); !errors.Is(err, ErrUSBBridgeRequired) {
		t.Fatal(err)
	}
}

type dfuPortFixture struct {
	*nativePortFixture
	rts    bool
	writes int
}

func (p *dfuPortFixture) SetRTS(v bool) error            { p.rts = v; return nil }
func (p *dfuPortFixture) Write(data []byte) (int, error) { p.writes++; return 0, io.ErrClosedPipe }
func TestDFURetainsIndependentNativeSerialPath(t *testing.T) {
	oldApp, oldDFU := openNativeSerial, openDFUSerial
	defer func() { openNativeSerial = oldApp; openDFUSerial = oldDFU }()
	openNativeSerial = func(string) (nativeSerialPort, error) {
		t.Error("DFU used application opener")
		return nil, io.ErrClosedPipe
	}
	a, b := net.Pipe()
	defer b.Close()
	port := &dfuPortFixture{nativePortFixture: &nativePortFixture{Conn: a}}
	calls := 0
	openDFUSerial = func(path string) (dfuSerialPort, error) {
		calls++
		if path != "DFU-port" {
			t.Error(path)
		}
		return port, nil
	}
	file := packageZIP(t, map[string][]byte{"manifest.json": []byte(`{"manifest":{"application":{"bin_file":"app.bin","dat_file":"app.dat"}}}`), "app.bin": {1, 2, 3}, "app.dat": {4}})
	for _, path := range []string{"tcp:127.0.0.1:12345", "ble:device", "socket://localhost:12345"} {
		if _, err := FlashFirmware(context.Background(), path, file, dfu.Options{}); err == nil {
			t.Fatal(path)
		}
	}
	if _, err := FlashFirmware(context.Background(), "DFU-port", "missing.zip", dfu.Options{}); err == nil || calls != 0 {
		t.Fatal(err, calls)
	}
	if _, err := FlashFirmware(context.Background(), "DFU-port", file, dfu.Options{Timeout: time.Second}); err == nil {
		t.Fatal("simulated DFU I/O failure lost")
	}
	if calls != 1 || !port.dtr || !port.rts || !port.reset || port.timeout != 100*time.Millisecond || !port.closed || port.writes != 1 {
		t.Fatal(calls, port)
	}
}

type cancelRestoreWrite struct {
	transport
	cancel context.CancelFunc
}

func (p *cancelRestoreWrite) Write(data []byte) (int, error) {
	if len(data) >= 4 && Command(uint16(data[2])<<8|uint16(data[3])) == CmdSetSlotTagType {
		p.cancel()
		return 0, context.Canceled
	}
	return p.transport.Write(data)
}
func TestNativeRestoreCancellationDoesNotLeaveApplicationPermission(t *testing.T) {
	c := backupSimulator(t, newBackupDevice())
	c.transportInfo = maintenanceInfo()
	backup, err := c.BackupForRestore(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.port = &cancelRestoreWrite{transport: c.port, cancel: cancel}
	result, err := c.RestoreDevice(ctx, backup, RestoreOptions{Persist: true})
	if !errors.Is(err, context.Canceled) || result.Verified || result.Persisted || !c.IsClosed() {
		t.Fatal(result, err, c.IsClosed())
	}
	if !c.TransportInfo().NativeUSBMaintenance {
		t.Fatal("maintenance boundary changed after cancellation")
	}
	if err := c.checkTransportCommand(context.Background(), 1000); !errors.Is(err, ErrUSBBridgeRequired) {
		t.Fatal(err)
	}
}
