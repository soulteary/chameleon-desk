package chameleon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// BridgeDevice describes a device returned by the bridge's last scan.
type BridgeDevice struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	RSSI         int    `json:"rssi,omitempty"`
	VID          *int   `json:"vid,omitempty"`
	PID          *int   `json:"pid,omitempty"`
	SerialNumber string `json:"serial_number,omitempty"`
	Location     string `json:"location,omitempty"`
	HWID         string `json:"hwid,omitempty"`
}

type BridgeStatus struct {
	APIVersion     int           `json:"api_version"`
	State          string        `json:"state"`
	SelectedDevice *BridgeDevice `json:"selected_device"`
	SessionActive  bool          `json:"session_active"`
	Transport      TransportInfo `json:"transport"`
	DataAddress    string        `json:"data_address"`
	Error          string        `json:"error"`
}

type BridgeScan struct {
	APIVersion int            `json:"api_version"`
	Devices    []BridgeDevice `json:"devices"`
}

// BLEBridge controls one configured local bridge. Its credential is never
// returned by these methods. Device protocol traffic uses a separate TCP port.
type BLEBridge struct {
	base, token string
	medium      string
	http        *http.Client
}

func loopbackAuthority(authority string) error {
	host, port, err := net.SplitHostPort(authority)
	if err != nil || port == "" {
		return fmt.Errorf("bridge address must be a loopback IP and port")
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return fmt.Errorf("invalid bridge port")
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("bridge address must use a loopback IP")
	}
	return nil
}

func NewBLEBridge(address, token string) (*BLEBridge, error) {
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || (u.Path != "" && u.Path != "/") || u.ForceQuery {
		return nil, fmt.Errorf("bridge URL must be an HTTP loopback origin")
	}
	if err := loopbackAuthority(u.Host); err != nil {
		return nil, err
	}
	if len(token) != 64 || strings.Trim(token, "0123456789abcdef") != "" {
		return nil, fmt.Errorf("bridge token must be 64 lowercase hexadecimal characters")
	}
	return &BLEBridge{base: "http://" + u.Host, token: token, http: &http.Client{
		Timeout:       30 * time.Second,
		Transport:     &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

// Medium returns the underlying medium expected from this configured bridge.
func (b *BLEBridge) Medium() string {
	if b.medium == "usb-serial" {
		return b.medium
	}
	return "ble"
}

func (b *BLEBridge) request(ctx context.Context, method, path string, input, output any) error {
	var payload io.Reader
	if method == http.MethodPost {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		payload = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, b.base+"/v1/"+path, payload)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+b.token)
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := b.http.Do(req)
	if err != nil {
		return fmt.Errorf("device bridge is unavailable: %w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil || len(data) > 64<<10 {
		return fmt.Errorf("invalid device bridge response size")
	}
	if response.StatusCode != http.StatusOK {
		// Do not reflect a configured credential or untrusted error body.
		return fmt.Errorf("device bridge %s returned HTTP %d", path, response.StatusCode)
	}
	if err := json.Unmarshal(data, output); err != nil {
		return fmt.Errorf("invalid device bridge JSON response")
	}
	return nil
}

func (b *BLEBridge) status(ctx context.Context, method, path string, input any) (BridgeStatus, error) {
	var result BridgeStatus
	if err := b.request(ctx, method, path, input, &result); err != nil {
		return result, err
	}
	if result.APIVersion != 1 || result.Transport.Connection != "tcp" || result.Transport.Medium != b.Medium() || !result.Transport.Managed || result.Transport.AllowsPairingChanges || result.Transport.AllowsDeviceRestore || result.Transport.SupportsDFU {
		return BridgeStatus{}, fmt.Errorf("unsupported device bridge transport declaration")
	}
	if err := loopbackAuthority(result.DataAddress); err != nil {
		return BridgeStatus{}, err
	}
	switch result.State {
	case "disconnected", "scanning", "connecting", "connected", "disconnecting", "blocked":
	default:
		return BridgeStatus{}, fmt.Errorf("unknown device bridge connection state")
	}
	return result, nil
}

func (b *BLEBridge) Status(ctx context.Context) (BridgeStatus, error) {
	return b.status(ctx, http.MethodGet, "status", nil)
}

func (b *BLEBridge) Scan(ctx context.Context) (BridgeScan, error) {
	var result BridgeScan
	if err := b.request(ctx, http.MethodPost, "scan", struct{}{}, &result); err != nil {
		return result, err
	}
	if result.APIVersion != 1 {
		return BridgeScan{}, fmt.Errorf("unsupported device bridge scan version")
	}
	return result, nil
}

func (b *BLEBridge) Select(ctx context.Context, deviceID string) (BridgeStatus, error) {
	return b.status(ctx, http.MethodPost, "select", map[string]string{"device_id": deviceID})
}

func (b *BLEBridge) Connect(ctx context.Context) (BridgeStatus, error) {
	return b.status(ctx, http.MethodPost, "connect", struct{}{})
}

func (b *BLEBridge) Disconnect(ctx context.Context) (BridgeStatus, error) {
	return b.status(ctx, http.MethodPost, "disconnect", struct{}{})
}

// OpenSession claims the selected BLE link. Closing the returned client ends
// that BLE session; callers must explicitly connect again after a failure.
func (b *BLEBridge) OpenSession(ctx context.Context, status BridgeStatus, timeout time.Duration) (*Client, error) {
	if status.State != "connected" || status.SelectedDevice == nil || status.SelectedDevice.ID == "" {
		return nil, fmt.Errorf("connect a selected bridge device first")
	}
	if err := loopbackAuthority(status.DataAddress); err != nil {
		return nil, err
	}
	c, err := OpenContext(ctx, "tcp:"+status.DataAddress, timeout)
	if err != nil {
		return nil, err
	}
	if c.transportInfo.Medium != b.Medium() || !c.transportInfo.Managed || c.bridgeDeviceID != status.SelectedDevice.ID {
		c.Close()
		return nil, fmt.Errorf("bridge session does not match the selected device")
	}
	return c, nil
}

// USBBridge controls an authenticated local USB serial bridge. Device traffic
// uses the same restricted TCP transport as the fixed-port USB bridge.
type USBBridge struct{ control *BLEBridge }

func NewUSBBridge(address, token string) (*USBBridge, error) {
	control, err := NewBLEBridge(address, token)
	if err != nil {
		return nil, err
	}
	control.medium = "usb-serial"
	return &USBBridge{control: control}, nil
}
func (b *USBBridge) Medium() string                                   { return b.control.Medium() }
func (b *USBBridge) Status(ctx context.Context) (BridgeStatus, error) { return b.control.Status(ctx) }
func (b *USBBridge) Scan(ctx context.Context) (BridgeScan, error)     { return b.control.Scan(ctx) }
func (b *USBBridge) Select(ctx context.Context, id string) (BridgeStatus, error) {
	return b.control.Select(ctx, id)
}
func (b *USBBridge) Connect(ctx context.Context) (BridgeStatus, error) { return b.control.Connect(ctx) }
func (b *USBBridge) Disconnect(ctx context.Context) (BridgeStatus, error) {
	return b.control.Disconnect(ctx)
}
func (b *USBBridge) OpenSession(ctx context.Context, status BridgeStatus, timeout time.Duration) (*Client, error) {
	return b.control.OpenSession(ctx, status, timeout)
}
