package chameleon

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"go.bug.st/serial"
)

// Ports lists local serial ports for explicit USB maintenance and DFU selection.
// Routine device discovery uses USBBridge.Scan. Enumeration does not open ports.
func Ports() ([]string, error) { return serial.GetPortsList() }

type nativeSerialPort interface {
	io.ReadWriteCloser
	SetDTR(bool) error
	SetReadTimeout(time.Duration) error
	ResetInputBuffer() error
}

// Test seam; no routine application opener calls this function.
var openNativeSerial = func(path string) (nativeSerialPort, error) {
	return serial.Open(path, &serial.Mode{BaudRate: 115200, DataBits: 8, Parity: serial.NoParity, StopBits: serial.OneStopBit})
}

func nativeUSBPath(path string) bool {
	return strings.TrimSpace(path) != "" && !strings.HasPrefix(path, "tcp:") && !strings.HasPrefix(path, "ble:") && !strings.Contains(path, "://")
}

// OpenNativeUSBContext opens an explicitly selected local application port for
// pairing, reset, pre-restore backup, full restore or bootloader entry. Ordinary
// application commands are rejected before transmission. Use FlashFirmware for
// an already enumerated DFU port. OS serial setup may outlast ctx cancellation;
// a canceled setup is closed before this function returns a client.
func OpenNativeUSBContext(ctx context.Context, path string, timeout time.Duration) (*Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !nativeUSBPath(path) {
		return nil, fmt.Errorf("native USB maintenance requires an explicit local application serial port")
	}
	p, err := openNativeSerial(path)
	if err != nil {
		return nil, fmt.Errorf("open native USB %s: %w", path, err)
	}
	fail := func(err error) (*Client, error) { _ = p.Close(); return nil, err }
	if err = ctx.Err(); err != nil {
		return fail(err)
	}
	if err = p.SetDTR(true); err != nil {
		return fail(fmt.Errorf("enable DTR: %w", err))
	}
	if err = p.SetReadTimeout(50 * time.Millisecond); err != nil {
		return fail(err)
	}
	if err = p.ResetInputBuffer(); err != nil {
		return fail(err)
	}
	if err = ctx.Err(); err != nil {
		return fail(err)
	}
	c := newClient(p, timeout)
	c.transportInfo = serialTransport()
	c.transportInfo.NativeUSBMaintenance = true
	return c, nil
}

func nativeMaintenanceCommand(cmd Command) bool {
	switch uint16(cmd) {
	case 1010, 1013, 1014, 1020, 1030, 1031, 1032, 1033, 1034, 1035, 1036, 1037, 1039, 1041:
		return true
	}
	return false
}
