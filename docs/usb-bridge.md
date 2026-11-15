# USB serial TCP bridge

The Python bridge can expose one explicit Chameleon **application serial port**
through Desk's existing TCP transport. This is an optional backend; native Go
USB serial support remains available and does not require Python. BLE setup and
its management API are unchanged; see the [bridge README](../tools/ble_bridge/README.md).

This first stage has no USB discovery, selection/status API, hotplug reconnect
or DFU backend. Select a port before starting the bridge. `--serial-port` cannot
be combined with BLE `--device`, `--scan`, `--pair`, `--control-port` or
`--token-file`. Do not use a bootloader port or a pySerial URL handler.

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
Close other applications using it. Native Desk's `devices` command can help
identify local ports; this bridge does not enumerate or auto-select them.

In another terminal:

```sh
./chameleon info --port tcp:127.0.0.1:12345 --json
./chameleon serve --port tcp:127.0.0.1:12345 --timeout 10s
```

Alternatively start `serve` without `--port`, enter `tcp:127.0.0.1:12345` in
the Web device window and connect. Web disconnect releases the TCP session and
closes its serial port. The Web server retains its existing session/task and
write-permission rules. Use ordinary TCP controls, not `serve --ble-bridge`:
there is no USB management endpoint in this stage. CLI and Web cannot use the
same bridge concurrently.

SDK callers can use `OpenContext(ctx, "tcp:127.0.0.1:12345", timeout)` and inspect
`Client.TransportInfo()`. Listener startup and the transport declaration do not
prove that the serial port opened or that firmware accepts a command.

## Transport and operation limits

The declaration is `connection: "tcp"`, `medium: "usb-serial"`, `managed: false`.
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
existing native USB workflow. No native USB permissions are changed by this
backend. BLE bootloader behavior is also unchanged.

The data listener remains loopback-only and has no authentication or TLS.
Other local processes can connect and issue allowed application commands.
`--token-file` protects the separate BLE management API, not this data socket.
Protocol payloads and backend exception text are not logged by the USB backend.

## Session lifetime and failures

Each accepted TCP connection opens a fresh serial session at 115200 baud,
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
metadata, restrictions and persistent TCP-session disconnect. The USB startup
test explicitly makes Bleak unavailable. CI runs bridge/CLI/Web integration on
Linux, macOS and Windows; the virtual-TTY case is skipped on Windows.

These tests do not validate physical USB adapters, OS permissions, driver
behavior or transfer throughput. Those need device testing on each target OS.
