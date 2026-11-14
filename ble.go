package chameleon

import (
	"context"
	"errors"
	"time"
)

// ErrBLEUnavailable reports use of a removed native BLE entry point.
// BLE application traffic is provided by tools/ble_bridge over TCP.
var ErrBLEUnavailable = errors.New("native BLE has been removed; run tools/ble_bridge/bridge.py and use tcp:HOST:PORT or NewBLEBridge for managed connections")

// BLEDevice is retained for source compatibility with ScanBLE.
//
// Deprecated: use BridgeDevice returned by BLEBridge.Scan.
type BLEDevice struct {
	DFU  bool   `json:"dfu"`
	ID   string `json:"id"`
	Name string `json:"name"`
	RSSI int    `json:"rssi"`
}

// ScanBLE no longer accesses the host Bluetooth adapter.
//
// Deprecated: use BLEBridge.Scan or tools/ble_bridge/bridge.py --scan.
func ScanBLE(ctx context.Context, duration time.Duration) ([]BLEDevice, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, ErrBLEUnavailable
}

// OpenBLE no longer opens a native Bluetooth connection.
//
// Deprecated: use OpenContext with tcp:HOST:PORT, or BLEBridge.OpenSession.
func OpenBLE(ctx context.Context, id string, timeout time.Duration) (*Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, ErrBLEUnavailable
}
