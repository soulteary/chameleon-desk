# Python device bridge

For matched native bundles, offline dependency installation, three-platform
startup and non-invasive diagnostics, see [distribution](../../docs/distribution.md).
`--version` prints build identity. `--doctor --backend usb|ble` checks local setup;
Desk's `bridge doctor` reads the running authenticated management service only.

The existing BLE backend and USB serial backend share the TCP entrypoint.
For fixed-port and managed USB setup, restrictions and tests, see [USB serial TCP bridge](../../docs/usb-bridge.md).
Routine USB requires this bridge; native maintenance/DFU remains separate.
See the [USB migration guide](../../docs/usb-migration.md).
The BLE instructions below continue to use the Bleak-only requirements file.

This optional Python process connects to one Chameleon Ultra or Lite over BLE
and exposes its application protocol on a loopback TCP port. Desk uses its
existing `tcp:HOST:PORT` transport. The Go workbench does not need Python, Bleak
or cgo to use that transport. All Desk BLE access uses this bridge; the native
macOS backend has been removed. See the [migration guide](../../docs/ble-migration.md)
for former native BLE commands and SDK calls.

Device commands, firmware capabilities and card operations remain in Desk.
The optional management API provides scanning, selection, connection state and
disconnect. It does not implement BLE DFU or share a BLE session between TCP
clients. Reset and pairing-change commands are blocked before GATT writes.

## Requirements

- Python 3.11 or later.
- Bleak 3.1.0 and a working OS Bluetooth backend. See the
  [Bleak platform requirements](https://bleak.readthedocs.io/en/latest/).
- A Chameleon advertising the Nordic UART Service in application mode.

Linux requires BlueZ and access to the system D-Bus Bluetooth service. macOS
requires Bluetooth permission for the process running Python. Windows requires
a Bluetooth adapter and a supported Windows version. Python dependency
installation does not grant OS permissions or install an adapter driver.

From the repository root, create a separate environment:

```sh
python3 -m venv tools/ble_bridge/.venv
tools/ble_bridge/.venv/bin/python -m pip install -r tools/ble_bridge/requirements.txt
```

On Windows, use `python` in place of `python3` and
`tools\ble_bridge\.venv\Scripts\python.exe` in place of the environment's
`bin/python`. Use that environment's interpreter for the commands below.

## Start the bridge

List devices advertising the application service:

```sh
tools/ble_bridge/.venv/bin/python tools/ble_bridge/bridge.py --scan
```

The first column is the device identifier. It is normally a Bluetooth address
on Windows/Linux and an OS-assigned UUID on macOS. Select the intended Chameleon
explicitly; another device can advertise the same UART service.

```sh
tools/ble_bridge/.venv/bin/python tools/ble_bridge/bridge.py \
  --device DEVICE_ID --listen 127.0.0.1 --port 12345
```

Discovery happens at startup. Each accepted TCP client then gets a new BLE
connection. Notifications are enabled before application bytes are forwarded.
Other clients are closed while that session, including cleanup, is active.
Pairing, if required, is handled by the OS. `--pair` requests pairing on
supported backends; macOS prompts when an authenticated characteristic is
accessed. Allow additional connection time when pairing for the first time.

The default `--timeout 20` bounds discovery, connection setup, each GATT write,
TCP output drain and BLE cleanup. It is not a total session or idle timeout.
Use Ctrl-C to stop the listener and close an active connection.

## Manage the bridge from Desk Web UI

Start the bridge with a separate management port and private token file:

```sh
tools/ble_bridge/.venv/bin/python tools/ble_bridge/bridge.py \
  --control-port 12346 --token-file tools/ble_bridge/bridge.key
```

`--device` is optional in this mode. The management listener uses the same
loopback IP as the data listener (`127.0.0.1` by default). The token file is
created with a random 256-bit token and mode `0600` on POSIX systems. An
existing valid token is reused. On Windows, restrict the file's ACL to the
account running the bridge and Desk. Do not put the token in a URL or a browser
input. The repository ignores `*.key` files.

In another terminal, start Desk:

```sh
CGO_ENABLED=0 ./scripts/build.sh
./chameleon serve --ble-bridge http://127.0.0.1:12346 \
  --bridge-token-file tools/ble_bridge/bridge.key --timeout 10s
```

Open the device-management tool. Scan for devices, select an advertisement,
then connect. Desk retains the resulting data connection for subsequent
device requests. Use the disconnect button before selecting a different
device or scanning again. Refresh connection state after a physical disconnect
or a failed operation. Reconnection is explicit; requests are not replayed.
The UI clears stale device and slot results when a disconnect is observed.

The Web server keeps the bridge credential on the backend. Browser actions
use Desk's session token and existing Host/Origin checks. Connection management
works in read-only mode; writing cards or device data still requires
`serve --allow-writes`. `--ble-bridge` cannot be combined with `--port`.

The management API accepts `Authorization: Bearer TOKEN` on every request and
does not accept browser `Origin` headers or nonmatching Host headers. It has
no CORS interface. Requests use HTTP/1.1, JSON bodies, a 4 KiB body limit and
an 8 KiB header limit. Redirects are not followed by the Go management client.

- `GET /v1/status`: selected device, connection state, session ownership,
  data address, transport properties and last error.
- `POST /v1/scan` with `{}`: NUS advertisements from a bounded scan.
- `POST /v1/select` with `{"device_id":"ID"}`: select from the latest scan.
- `POST /v1/connect` with `{}`: connect and subscribe to NUS notifications.
- `POST /v1/disconnect` with `{}`: end the active data connection and BLE link,
  or cancel a pending connection.

Responses declare `api_version: 1`. States are `disconnected`, `scanning`,
`connecting`, `connected`, `disconnecting` and `blocked`. A failed cleanup
enters `blocked` and requires a bridge restart. If a TCP EOF overlaps a control
disconnect, the data session finishes its existing BLE cleanup before the
controller publishes the final state. Successful cleanup clears its error;
a cleanup failure or timeout remains blocked. An accepted TCP connection whose
session has not started still closes its prepared BLE link. Scan and selection do not
replace an active connection. A data connection is admitted only after an
explicit management connect; closing it also ends the BLE link. The API
token controls management operations, not access to the separate data port.

For command-line scanning against the managed bridge, use:

```sh
./chameleon ble scan --ble-bridge http://127.0.0.1:12346 \
  --bridge-token-file tools/ble_bridge/bridge.key
```

The scan duration is configured by the bridge's `--timeout`; Desk's former
native `ble scan --duration` option has been removed. Scanning does not select
or connect a device and requires the bridge to be disconnected.

The SDK exposes the same API through `NewBLEBridge`, `Scan`, `Select`,
`Connect`, `Status`, `OpenSession` and `Disconnect`. `OpenSession` verifies
that the data connection declares the selected device and managed BLE
transport before returning a client.

## Use Desk over TCP

```sh
CGO_ENABLED=0 ./scripts/build.sh
./chameleon version
./chameleon info --port tcp:127.0.0.1:12345 --timeout 30s --json
./chameleon serve --port tcp:127.0.0.1:12345 --timeout 30s
```

On PowerShell, set `$env:CGO_ENABLED = '0'` and build with:

```powershell
go build -mod=readonly -trimpath -o chameleon.exe ./cmd/chameleon
.\chameleon.exe info --port tcp:127.0.0.1:12345 --timeout 30s --json
```

In fixed-device mode, the Desk command timeout must allow for BLE setup on the
first command. With `serve --port tcp:...`, each Web request opens a new BLE
session and has a 30-second request budget. Initial pairing or slow discovery
can exceed that budget. Use managed mode for a persistent Web device session.
After a failure, wait for cleanup before explicitly reconnecting or retrying a
read. No automatic reconnect or replay is done.

The bridge validates application frames and blocks reset and pairing-change
commands. Existing Desk write controls (`--yes` and `serve --allow-writes`) still apply, but they
are not a bridge access-control mechanism. Any local process that can connect
to the data TCP port can send device commands. The data listener accepts
loopback IP addresses only; it has no authentication or encryption. The separate
management listener requires its bearer token for every request.

### Transport declaration and restrictions

Every accepted data connection starts with one UTF-8 line:

```text
CHAMELEON_BRIDGE/1 {"api_version":1,"transport":{"connection":"tcp","medium":"ble","managed":true,"allows_pairing_changes":false,"allows_device_restore":false,"supports_dfu":false},"device_id":"ID"}
```

`managed` is false in fixed-device mode. Desk reads this declaration before
using application frames; it is exposed through `Client.TransportInfo()` and
the `transport` field in `info` results. Invalid or unsupported declarations
are rejected. Legacy serial TCP peers without a declaration remain usable for
ordinary commands and are marked `tcp/unknown` after a bounded probe.

TCP declarations cannot grant serial-link permissions. Both declared BLE and
unknown TCP connections reject pairing changes, resets and full device restore.
Use a direct serial connection for these operations, including when a TCP
bridge happens to lead to a wired device. This changes the earlier allowance
for restoring over an assumed stable TCP bridge.

The restrictions apply in the SDK, including raw command execution. Full
restore and mixed settings patches are checked before sending any request.
`NewClient` also identifies a supplied TCP `net.Conn` as unknown TCP. Other
custom streams remain caller-managed for compatibility; use `OpenContext`
for built-in TCP declaration handling rather than hiding a connection in an
unidentified wrapper.

## Boundaries

- Normal application commands can use the bridge, subject to the device model,
  firmware capabilities and BLE throughput. No new card capability is added.
- The updated CLI and SDK reject full device restore before device access on
  BLE/TCP. The bridge also blocks application command IDs 1014, 1020, 1030,
  1032 and 1037 (settings reset, factory reset, BLE PIN, bond deletion and pairing
  enable). Fragmenting a frame or using a raw TCP client does not bypass this
  check. The bridge cannot infer an entire restore workflow from commands sent
  by an older or custom client; such a client could write some slot data before
  a restricted command is rejected. Do not use it for restore.
- BLE DFU uses separate control and packet characteristics. The application
  UART stream cannot upload firmware, and `FlashFirmware` rejects `ble:` and `tcp:`.
  Entering bootloader ends the application connection; use the existing USB
  DFU path for upload. Native BLE DFU has been removed; the Python bridge does
  not replace it.
- GATT writes are serialized in chunks of at most 20 bytes. Writes with a GATT
  response are preferred; otherwise the characteristic's advertised write
  limit is respected. This is a conservative transport, not a throughput
  optimization.
- Notifications and pending TCP input each have a 64 KiB byte budget. Overflow,
  disconnect or an I/O timeout closes the TCP session rather than discarding
  bytes or replaying a request. This matters because the application protocol
  has no request identifier for matching late replies after a timeout.
- A TCP EOF closes its BLE connection before another client is admitted. Old
  callbacks cannot send into a later session. Input queued during BLE setup is
  discarded if the TCP client closes before setup completes. If cleanup fails or
  times out, restart the bridge; subsequent clients are rejected.

The Go module no longer depends on `cbgo` or its transitive logging packages.
The build script defaults to cgo disabled and no longer embeds macOS Bluetooth
permission metadata. Bluetooth permission belongs to the Python process. The
separate C recovery workers retain their compiler and library requirements.

## Offline tests

The tests use a simulated Bleak client, fake serial ports and real loopback
sockets. The POSIX virtual-TTY case imports pySerial; install the USB test
dependency before running the combined suite. No physical USB/BLE device or
pairing is used. See the [USB test coverage](../../docs/usb-bridge.md#offline-verification).

```sh
python3 -m pip install -r tools/ble_bridge/requirements-usb.txt
python3 -m unittest discover -s tools/ble_bridge -v
CGO_ENABLED=0 go build -mod=readonly -o reports/chameleon-nocgo ./cmd/chameleon
CHAMELEON_BRIDGE_TEST_BINARY="$PWD/reports/chameleon-nocgo" \
  python3 -m unittest discover -s tools/ble_bridge -v
```

The optional binary tests run CLI `info` and the Web scan/select/connect/read/
disconnect workflow through the bridge and a simulated BLE device. Two Web
device reads must reuse one BLE connection. Tests also cover authorization,
connection cancellation, stale callbacks, transport declarations and blocked
raw pairing commands. They do not establish hardware compatibility. CI runs
these tests on Linux, macOS and Windows with a cgo-disabled binary.
The existing Go test suite also runs on Linux and macOS. It is not run by the
Windows bridge job because recovery fixtures use POSIX shell helpers and file
semantics; that job covers the native CLI build and the Python bridge tests.

## Hardware checks

Hardware checks are manual and are not run by the offline suite. Record the
OS, Python/Bleak versions, adapter, device model and firmware version. Start
with `info`, then a read-only card scan, and compare the results with USB.
Check a subsequent session after normal close, a physical BLE disconnect, and
a second client's rejection. Verify any intended write on a disposable test
slot and read it back. An interrupted write can have completed on the device;
do not retry until its state has been read. BLE performance and Windows/Linux
hardware compatibility require measurements on the target systems.
