# BLE to TCP bridge

This optional Python process connects to one Chameleon Ultra or Lite over BLE
and exposes its application protocol on a loopback TCP port. Desk uses its
existing `tcp:HOST:PORT` transport. The Go workbench does not need Python, Bleak
or cgo to use that transport. Native macOS BLE remains available separately.

The bridge handles transport only. Device commands, firmware capabilities and
card operations remain in Desk. It does not implement BLE DFU, a device
management API, or a persistent BLE session shared by multiple TCP clients.

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

## Use Desk without cgo

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

The Desk command timeout must allow for BLE setup on the first command. The
Web server also has a 30-second hardware-request budget. Its current per-request
TCP connection opens a new BLE session for each request, so initial pairing or
slow discovery can exceed that budget. Pair first, and close other clients
before opening the Web UI. After a failed request, wait for bridge cleanup
before explicitly retrying a read. No automatic reconnect or replay is done.

The bridge forwards raw application bytes without inspecting commands. Existing
Desk write controls (`--yes` and `serve --allow-writes`) still apply, but they
are not a bridge access-control mechanism. Any local process that can connect
to the TCP port can send device commands. The listener accepts loopback IP
addresses only; it has no authentication or encryption.

## Boundaries

- Normal application commands can use the bridge, subject to the device model,
  firmware capabilities and BLE throughput. No new card capability is added.
- Do not use it for `backup restore`, changing BLE PIN/pairing settings,
  clearing bonds, or factory reset. These operations can invalidate the link.
  Desk currently rejects `ble:` for full restore but cannot identify BLE behind
  a `tcp:` address. The bridge does not parse or block those commands.
- BLE DFU uses separate control and packet characteristics. The application
  UART stream cannot upload firmware, and `FlashFirmware` rejects `tcp:`.
  Entering bootloader ends the application connection; use the existing USB
  or native BLE DFU path for upload.
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

Moving BLE into this optional process removes the need to link native BLE into
a cgo-disabled Desk binary. It does not remove `cbgo` from the Go module or
remove the separate C recovery workers' build dependencies.

## Offline tests

The tests use the Python standard library, a simulated Bleak client and real
loopback sockets. They do not import Bleak, access an adapter, pair a device or
open a serial port.

```sh
python3 -m unittest discover -s tools/ble_bridge -v
CGO_ENABLED=0 go build -mod=readonly -o reports/chameleon-nocgo ./cmd/chameleon
CHAMELEON_BRIDGE_TEST_BINARY="$PWD/reports/chameleon-nocgo" \
  python3 -m unittest discover -s tools/ble_bridge -v
```

The optional binary test runs `info` through Desk's TCP transport, this bridge,
and a simulated BLE device that returns fragmented protocol replies. It checks
the CLI output; it does not establish hardware compatibility. CI runs this
test on Linux, macOS and Windows with a cgo-disabled binary.
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
