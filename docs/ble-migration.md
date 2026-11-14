# Migrate native BLE connections to the Python bridge

Desk no longer opens Bluetooth adapters from Go. The macOS CoreBluetooth
backend, native BLE DFU opener, `cbgo` dependency and Bluetooth permission plist
have been removed. Linux, macOS and Windows use the same Python/Bleak bridge.
The bridge is a separate process; Desk does not start Python or install its
packages automatically.

USB serial connections and offline tools do not require Python. The Go build
script now defaults to `CGO_ENABLED=0`. An explicit `CGO_ENABLED=1` still uses
the same serial/TCP implementation, without a native Bluetooth backend. The
optional C recovery workers and C regression tests still need their existing
compiler and library dependencies.

## CLI and Web UI

Install the bridge dependencies in a separate virtual environment as described
in the [bridge guide](../tools/ble_bridge/README.md#requirements). Bluetooth
permissions now apply to the process running Python. An existing permission
granted to the old Desk executable does not authorize Python. `--pair` requests
OS pairing on supported backends; the bridge does not change the device PIN or
pairing policy.

For a fixed device, replace `ble:UUID` addresses with a TCP address:

```sh
# Discover application-mode devices; this does not connect or pair.
python tools/ble_bridge/bridge.py --scan

# In a separate terminal, using the bridge virtual environment:
python tools/ble_bridge/bridge.py --device DEVICE_ID --port 12345

# Use Desk while the bridge is running:
./chameleon info --port tcp:127.0.0.1:12345 --timeout 30s --json
```

`DEVICE_ID` is the identifier printed by the bridge. macOS uses an OS-assigned
UUID; Linux and Windows normally use a Bluetooth address. Do not select a
peer solely by its advertised name. Each fixed-mode TCP connection owns one
BLE session, which ends when TCP closes.

For scanning and connection management from the Web UI:

```sh
python tools/ble_bridge/bridge.py --control-port 12346 \
  --token-file tools/ble_bridge/bridge.key
./chameleon serve --ble-bridge http://127.0.0.1:12346 \
  --bridge-token-file tools/ble_bridge/bridge.key --timeout 10s
```

Use the device panel to scan, select, connect and disconnect. This mode retains
one data session across Web requests. Reconnection is explicit after a failure;
requests are not replayed. The token stays on the Desk backend. Keep the token
file private, and restrict its Windows ACL to the account running both processes.

The CLI scan command now requires the managed bridge:

```sh
./chameleon ble scan --ble-bridge http://127.0.0.1:12346 \
  --bridge-token-file tools/ble_bridge/bridge.key
```

It returns the versioned bridge scan response (`api_version` and `devices`),
rather than the former native device array. It lists application-mode NUS
advertisements, not DFU bootloaders. The former `--duration` option is removed;
configure scan duration with the Python bridge's `--timeout`. Desk's `--timeout`
bounds the management request (30 seconds by default). The bridge must
be disconnected before scanning. For a standalone scan, use Python's `--scan`.

## Go SDK

Replace `OpenBLE(ctx, deviceID, timeout)` or `OpenContext(ctx, "ble:"+deviceID,
timeout)` with `OpenContext` against a running fixed-device bridge:

```go
client, err := chameleon.OpenContext(ctx, "tcp:127.0.0.1:12345", 30*time.Second)
if err != nil {
    return err
}
defer client.Close()
```

For managed sessions, create `NewBLEBridge(url, token)`, call `Scan`, `Select`
with an ID from that scan, then `Connect` and `OpenSession`. Use `Disconnect`
to end the link, including if opening the data session fails after connecting.
Closing the data client also ends its BLE session. `OpenSession` verifies that
the TCP declaration matches the selected device. These APIs do not access a
host Bluetooth adapter from Go.

`ScanBLE` and `OpenBLE` remain as deprecated compatibility entry points. They
return `ErrBLEUnavailable` with bridge migration instructions on every platform;
no build flag re-enables them. `BLEDevice` remains for source compatibility.
The native implementation's NUS/DFU UUID constants have been removed. Use the
bridge's advertised device IDs and management API rather than Go GATT helpers.

## Restrictions and firmware upload

The previous bridge transport checks remain in force. BLE and unknown TCP
connections reject pairing changes, settings/factory reset and full device
restore. Use direct serial for these operations. Declaring a TCP peer cannot
grant serial permissions. See the [bridge boundaries](../tools/ble_bridge/README.md#boundaries)
for raw-client and restore limitations.

Native `firmware flash --port ble:UUID` has been removed. The Python bridge
carries application UART frames and does not implement Nordic BLE DFU. After
entering bootloader, use an explicitly selected USB DFU serial port:

```sh
./chameleon firmware flash --port /dev/ttyACM0 --input application.zip --yes
```

The port shown is an example; enumerate and select the actual bootloader port.
`FlashFirmware` rejects BLE and TCP addresses before reading an archive or
opening a device. The transport-independent `dfu.UploadBLE` protocol helper
remains for callers supplying their own transport; Desk ships no BLE DFU
backend. This migration does not add firmware upload through the Python bridge.
