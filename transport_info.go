package chameleon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// TransportInfo describes the link, independently of the address syntax.
// TCP peers cannot grant permission to change pairing state or restore a device.
type TransportInfo struct {
	Connection           string `json:"connection"`
	Medium               string `json:"medium"`
	Managed              bool   `json:"managed"`
	AllowsPairingChanges bool   `json:"allows_pairing_changes"`
	AllowsDeviceRestore  bool   `json:"allows_device_restore"`
	SupportsDFU          bool   `json:"supports_dfu"`
	NativeUSBMaintenance bool   `json:"native_usb_maintenance,omitempty"`
}

func serialTransport() TransportInfo {
	return TransportInfo{Connection: "serial", Medium: "serial", AllowsPairingChanges: true, AllowsDeviceRestore: true, SupportsDFU: true}
}

func bleTransport(connection string, managed bool) TransportInfo {
	return TransportInfo{Connection: connection, Medium: "ble", Managed: managed}
}

// TransportInfo returns a snapshot of the connection's immutable properties.
func (c *Client) TransportInfo() TransportInfo { return c.transportInfo }

// IsClosed reports whether a timeout, error or explicit Close ended the client.
func (c *Client) IsClosed() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

type nativeRestoreContextKey struct{}

func (c *Client) checkTransportCommand(ctx context.Context, command uint16) error {
	info := c.transportInfo
	if ctx.Value(nativeRestoreContextKey{}) == c {
		info.NativeUSBMaintenance = false
	}
	return checkTransportCommand(info, command)
}

func checkTransportCommand(info TransportInfo, command uint16) error {
	if info.NativeUSBMaintenance && !nativeMaintenanceCommand(Command(command)) {
		return ErrUSBBridgeRequired
	}
	if info.Connection == "tcp" && info.Medium == "usb-serial" && Command(command) == CmdEnterBootloader {
		return fmt.Errorf("bootloader entry is unavailable over the USB serial TCP bridge")
	}
	if info.AllowsPairingChanges {
		return nil
	}
	switch Command(command) {
	case CmdSetBlePairingKey, CmdSetBlePairingEnable, CmdDeleteAllBleBonds, CmdResetSettings, CmdWipeFds:
		return fmt.Errorf("%s is unavailable over %s/%s: pairing state requires a direct serial connection", Command(command), info.Connection, info.Medium)
	}
	return nil
}

type bufferedConnection struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConnection) Read(p []byte) (int, error) { return c.reader.Read(p) }

// tcpClient recognizes the optional bridge declaration without consuming bytes
// from legacy serial TCP peers. Missing declarations retain restrictive defaults.
func tcpClient(ctx context.Context, conn net.Conn, timeout time.Duration) (*Client, error) {
	r := bufio.NewReaderSize(conn, 4096)
	info := TransportInfo{Connection: "tcp", Medium: "unknown"}
	deviceID := ""
	deadline := time.Now().Add(500 * time.Millisecond)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := conn.SetReadDeadline(deadline); err != nil {
		conn.Close()
		return nil, err
	}
	interrupted := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = conn.SetReadDeadline(time.Now()); close(interrupted) })
	defer func() {
		if !stop() {
			<-interrupted
		}
		_ = conn.SetReadDeadline(time.Time{})
	}()
	first, err := r.Peek(1)
	if err != nil {
		if ctx.Err() != nil {
			conn.Close()
			return nil, ctx.Err()
		}
		var network net.Error
		if !errors.As(err, &network) || !network.Timeout() || ctx.Err() != nil {
			conn.Close()
			return nil, fmt.Errorf("TCP declaration: %w", err)
		}
	} else if first[0] == 'C' {
		line, e := r.ReadSlice('\n')
		if e != nil || !strings.HasPrefix(string(line), "CHAMELEON_BRIDGE/1 ") {
			conn.Close()
			return nil, fmt.Errorf("invalid bridge declaration")
		}
		var declaration struct {
			Version   int           `json:"api_version"`
			Transport TransportInfo `json:"transport"`
			DeviceID  string        `json:"device_id"`
		}
		if e = json.Unmarshal(line[len("CHAMELEON_BRIDGE/1 "):], &declaration); e != nil ||
			declaration.Version != 1 || declaration.Transport.Connection != "tcp" ||
			(declaration.Transport.Medium != "ble" && declaration.Transport.Medium != "usb-serial") ||
			declaration.Transport.NativeUSBMaintenance || declaration.Transport.AllowsPairingChanges || declaration.Transport.AllowsDeviceRestore || declaration.Transport.SupportsDFU {
			conn.Close()
			return nil, fmt.Errorf("unsupported bridge declaration")
		}
		if declaration.Transport.Medium == "ble" {
			info = bleTransport("tcp", declaration.Transport.Managed)
		} else {
			info = TransportInfo{Connection: "tcp", Medium: "usb-serial", Managed: declaration.Transport.Managed}
		}
		deviceID = declaration.DeviceID
	}
	if err := ctx.Err(); err != nil {
		conn.Close()
		return nil, err
	}
	c := newClient(&bufferedConnection{Conn: conn, reader: r}, timeout)
	c.transportInfo = info
	c.bridgeDeviceID = deviceID
	return c, nil
}
