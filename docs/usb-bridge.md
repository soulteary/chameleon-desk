# USB serial TCP bridge

Matched core/bridge artifacts and non-invasive dependency, permission, listener
and version checks are documented in [distribution and diagnosis](distribution.md).

The Python bridge can expose one explicit Chameleon **application serial port**
through Desk's existing TCP transport. Routine USB application access uses this backend. Native Go
USB is reserved for explicit maintenance and DFU; see [USB migration](usb-migration.md). BLE setup and
its management API are unchanged; see the [bridge README](../tools/ble_bridge/README.md).

The bridge supports a fixed-port mode and an authenticated managed mode. Managed
mode enumerates ports on the bridge host and exposes selection, connection,
status and disconnect through the existing Web device panel. Neither mode adds
a hotplug reconnect or DFU backend. Port metadata is not proof of device model,
firmware compatibility or physical identity.

Fixed `--serial-port` mode cannot be combined with BLE `--device`, `--scan`,
`--pair`, `--control-port` or `--token-file`. Managed `--usb` mode requires
`--control-port` and `--token-file` and cannot be combined with `--device`,
`--scan`, `--serial-port` or `--pair`. Do not select a bootloader port or a pySerial URL
handler.

## Install and connect

Use Python 3.11 or later. USB requires only pySerial; Bleak is not imported by
this backend. Install both requirements files only if you also need BLE.

```sh
python3 -m venv tools/ble_bridge/.venv
tools/ble_bridge/.venv/bin/python -m pip install -r tools/ble_bridge/requirements-usb.txt
tools/ble_bridge/.venv/bin/python tools/ble_bridge/bridge.py \
  --serial-port /dev/ttyACM0 --port 12345
```

On macOS, choose the device's `/dev/cu.*` application port. On Windows, use
`tools\ble_bridge\.venv\Scripts\python.exe` and, for example,
`--serial-port COM7`. The account running the bridge needs access to the port.
Close other applications using it. Desk's `devices --native-usb` command can help
identify local ports; fixed-port mode does not enumerate or auto-select them.

In another terminal:

```sh
./chameleon info --port tcp:127.0.0.1:12345 --json
./chameleon serve --port tcp:127.0.0.1:12345 --timeout 10s
```

Alternatively start `serve` without `--port`, enter `tcp:127.0.0.1:12345` in
the Web device window and connect. Web disconnect releases the TCP session and
closes its serial port. The Web server retains its existing session/task and
write-permission rules. Use ordinary TCP controls, not `serve --ble-bridge`:
fixed-port mode has no management endpoint. CLI and Web cannot use the
same data session concurrently.

SDK callers can use `OpenContext(ctx, "tcp:127.0.0.1:12345", timeout)` and inspect
`Client.TransportInfo()`. Listener startup and the transport declaration do not
prove that the serial port opened or that firmware accepts a command.

## Managed USB and Desk Web UI

Use the same pySerial installation, then start a separate authenticated control
listener. Update Desk and the Python bridge together for managed USB support;
earlier fixed-port-only Desk builds reject managed USB declarations. Fixed-port
mode remains available.

```sh
tools/ble_bridge/.venv/bin/python tools/ble_bridge/bridge.py --usb \
  --port 12345 --control-port 12346 --token-file tools/ble_bridge/usb.key
./chameleon serve --usb-bridge http://127.0.0.1:12346 \
  --bridge-token-file tools/ble_bridge/usb.key --timeout 10s
```

The token file is created once with private permissions. Keep it on the backend
and outside version control; Windows users must restrict its ACL to their
account. Desk validates it as a private regular file and never returns its
contents to the browser. `--usb-bridge`, `--ble-bridge` and `--port` are mutually
exclusive server configurations. Native USB maintenance is explicit; existing BLE commands are unchanged. Add `--allow-writes` only when device/card writes are intended;
connection management itself is available in read-only mode and still requires
the Web session token.

In the Web device panel, scan ports, select the Chameleon **application** port,
then connect. The panel shows connection state and disconnect controls. These
are ports on the Python bridge host, not the browser host or the Desk server's
native enumeration. The shared Web operation gate, session identity, task
progress/cancellation and stale-response handling also apply to USB bridge
commands. Refreshing the page retains the server's existing device session and
allows querying retained background tasks.

The management protocol is the same authenticated loopback HTTP/1.1 protocol
used by BLE, with `Authorization: Bearer <token>` and JSON POST bodies:

- `POST /v1/scan` with `{}` returns `api_version` and `devices`. Each record has
  `id` (port path/name), `name`, `vid`, `pid`, `serial_number`, `location` and
  `hwid`; unavailable OS metadata can be null. Scanning opens no serial port
  and reports all enumerated ports, including devices that are not Chameleons.
- `POST /v1/select` with `{"device_id":"COM7"}` accepts only a last-scan ID.
- `POST /v1/connect` with `{}` re-enumerates and checks the selected metadata
  before opening the port. Changed/missing metadata clears selection and
  requires scanning and selection again. Equal metadata cannot distinguish
  every physical replacement, particularly ports without serial numbers.
- `GET /v1/status` reports `state`, `selected_device`, `session_active`,
  `transport`, `data_address` and a non-sensitive `error`.
- `POST /v1/disconnect` with `{}` cancels setup or closes the active link.

Mutations return the same status shape. States are `disconnected`, `scanning`,
`connecting`, `connected`, `disconnecting` and `blocked`. Scan/select/connect
require a disconnected link; none silently replaces an active device. Connect
opens the port once, then one TCP client can claim it. `connected` describes
the open serial transport; it is not a firmware/protocol readiness check. `session_active` denotes
that data claim, separately from an open serial link. Idle links also detect
I/O failure or output-queue overflow and close. Closing the TCP client ends the
serial session; reconnect is explicit. An abandoned HTTP connection does not
interrupt cleanup. A lost connect response can leave an open idle link; query
status before deciding whether to disconnect or proceed.

Enumeration runs off the event loop with a timeout. A late result from an
abandoned/timed-out scan cannot change selection, and a second enumeration is
rejected while the first OS call is still running. Cleanup failures/timeouts
block further use until restart, including late serial-open completion.

SDK callers use `NewUSBBridge(address, token)` followed by `Scan`, `Select`,
`Connect`, `Status`, `OpenSession` and `Disconnect`. The SDK checks the configured
medium, selected device ID, restricted flags and loopback data destination.
CLI `usb scan --usb-bridge URL --bridge-token-file FILE` uses this management
endpoint for discovery. Ordinary CLI reads
can claim a connected bridge through `--port tcp:...` while Desk Web is not
using that session. The data socket itself remains unauthenticated.

## Transport and operation limits

The declaration is `connection: "tcp"`, `medium: "usb-serial"`. `managed` is false
for fixed-port mode and true for managed mode; it describes lifecycle control,
not direct-USB permission.
`allows_pairing_changes`, `allows_device_restore` and `supports_dfu` are all
false. Desk accepts this descriptive medium without granting direct-USB
permissions; a declaration attempting to enable those flags is rejected.

- Pairing changes, settings reset and FDS wipe remain blocked. The bridge
  rejects application command IDs 1014, 1020, 1030, 1032 and 1037, as does the
  existing transport guard.
- Full-device restore remains unavailable through Desk TCP entrypoints. A
  bridge cannot infer a restore workflow assembled from arbitrary otherwise
  allowed commands; this is not a generic write access-control service.
- USB bridge bootloader entry (1010) is rejected both by Desk and by the bridge.
  DFU upload requires the existing physical USB DFU path. The bridge accepts
  only complete, checksum-validated Chameleon application frames, with payloads
  up to 4096 bytes; it does not forward DFU/SLIP traffic.
- Other application commands retain firmware, model and write-policy checks.
  CLI `--yes` and Web `--allow-writes` do not override these transport limits.

For pairing changes, full-device restore or DFU, stop the bridge and use the
explicit native USB maintenance workflow (`--native-usb` or
`OpenNativeUSBContext`), or the separate physical DFU path. Routine native USB
application openers have been removed. BLE bootloader behavior is unchanged.

The data listener remains loopback-only and has no authentication or TLS.
Other local processes can connect and issue allowed application commands.
`--token-file` protects the BLE or USB management API, not this data socket.
Protocol payloads and backend exception text are not logged by the USB backend.

## Session lifetime and failures

Each fixed-port TCP connection or managed connect opens a fresh serial session
at 115200 baud,
8 data bits, no parity and one stop bit. DTR and RTS are asserted; software and
hardware flow control are disabled. One owner thread performs all serial
setup, reads, writes and close. POSIX exclusive mode is requested where
supported; this does not guarantee that every operating system or driver can
exclude unrelated applications.

Only one client is admitted while opening, forwarding or closing. TCP input
and serial output queues are byte-bounded at 64 KiB; serial reads poll at
20 ms. `--timeout` bounds setup waits, frame writes, TCP drains and cleanup.
A setup error, unplug, I/O error or queue overflow closes the TCP connection.
The bridge does not reconnect or replay a frame. A short write continues only
with its unaccepted suffix; an error ends the session without a retry.

Canceling an asynchronous wait cannot cancel an OS serial call. A write already
in progress may complete after TCP disconnect, and the device may still be
executing it. A timeout or disconnect therefore gives no rollback or exactly-once
guarantee. Check device state before deciding whether to submit a write again.

Replacement clients are rejected until the previous owner closes the port and
exits. If close fails or cleanup times out, restart the bridge before connecting
again. Late setup completion checks for shutdown and closes without forwarding.
Application queues belong to one TCP session and are discarded on close; input
buffer reset before forwarding cannot prove that the device has stopped an old
operation or prevent every delayed physical-device response.

## Offline verification

Install both bridge requirements files for the combined regression suite:

```sh
python -m pip install -r tools/ble_bridge/requirements.txt -r tools/ble_bridge/requirements-usb.txt
CGO_ENABLED=0 go build -mod=readonly -o chameleon ./cmd/chameleon
CHAMELEON_BRIDGE_TEST_BINARY="$PWD/chameleon" \
  python -m unittest discover -s tools/ble_bridge -v
```

Tests use fake serial ports for configuration, fragmentation, short writes,
command filtering, queue bounds, errors, unplug, cancellation and cleanup.
Linux/macOS also exercise real pySerial over a virtual TTY. CLI and Web tests
use the cgo-disabled Go binary with a simulated device to verify transport
metadata, restrictions and persistent TCP-session disconnect. USB startup tests explicitly make Bleak unavailable. Management tests cover
scan/selection, port replacement, idle unplug, HTTP cancellation, authentication
and the Web session/task flow. A Chromium regression checks the shared Web
panel against a simulated USB management/data endpoint, including page reload
without reopening the device session. CI runs bridge/CLI/Web integration on
Linux, macOS and Windows; the virtual-TTY case is skipped on Windows.

These tests do not validate physical USB adapters, OS permissions, driver
behavior or transfer throughput. Those need device testing on each target OS.
