# Migrate USB application access to the Python bridge

Routine USB application access now uses the Python/pySerial bridge on Linux,
macOS and Windows. The Go workbench keeps native serial code for explicit
maintenance and DFU. Offline tools, a Go build and native maintenance do not
require Python; routine USB requires Python 3.11 or later and the USB bridge
requirements. BLE continues to use its existing Python/Bleak backend.

Desk does not launch Python, install packages, guess a serial port or fall back
to native USB when a TCP connection fails. Start the bridge separately and
select its application port. See [USB bridge setup](usb-bridge.md).

## CLI and Web application connections

Replace a local application port with a running fixed-port bridge:

```sh
python tools/ble_bridge/bridge.py --serial-port /dev/ttyACM0 --port 12345
# In another terminal:
./chameleon info --port tcp:127.0.0.1:12345 --json
./chameleon scan --port tcp:127.0.0.1:12345 --wait 15s
./chameleon serve --port tcp:127.0.0.1:12345
```

The example path must be replaced with the actual application port; Windows
uses names such as `COM7`, and macOS generally uses `/dev/cu.*`. For managed USB
selection from Desk, start the authenticated management listener instead:

```sh
python tools/ble_bridge/bridge.py --usb --control-port 12346 \
  --token-file tools/ble_bridge/usb.key
./chameleon usb scan --usb-bridge http://127.0.0.1:12346 \
  --bridge-token-file tools/ble_bridge/usb.key
./chameleon serve --usb-bridge http://127.0.0.1:12346 \
  --bridge-token-file tools/ble_bridge/usb.key
```

Scan does not open a serial port. The Web panel scans, selects, connects and
disconnects through Python. Fixed and managed USB retain one-client ownership,
bounded queues/cleanup and explicit reconnect, without command replay.

These application entrypoints have changed:

- `info`, `slots`, `scan`, `stress` and other device commands require an explicit
  TCP target. An omitted target or a bare serial path now returns bridge
  migration instructions without selecting or opening a local port.
- `devices` directs application discovery to `usb scan`. Use
  `devices --native-usb` only to enumerate local maintenance/DFU candidates.
  SDK `Ports` retains that enumeration role; it does not open ports.
- Ordinary Web sessions accept TCP targets only. The former
  `GET /api/session/ports` endpoint returns an empty list with
  `usb_bridge_required: true` for compatibility and never enumerates local
  ports. The direct USB selection control has been removed. `serve` without a
  target remains disconnected and usable for offline tools.

Generic TCP peers and the existing BLE bridge remain supported. USB bridge
metadata still grants no pairing, reset, full-device restore or DFU permission.
Bootloader entry remains blocked on USB bridges. A management declaration does
not grant native permissions.

## Explicit native maintenance

Close the Python data session before opening a native maintenance or DFU port.
The Desk operation gate orders work within one process; it is not a lock across
Desk, Python and unrelated applications. Native and bridged sessions must not
be used concurrently for the same physical port. Do not select a device solely
by a descriptive name; enumerated metadata does not certify compatibility.

The existing typed CLI maintenance actions now require `--native-usb`, an
explicit local application port and the existing `--yes` confirmation for
mutations:

```sh
./chameleon devices --native-usb
./chameleon settings get --native-usb --port /dev/ttyACM0
./chameleon settings set --name ble-key --key 123456 \
  --native-usb --port /dev/ttyACM0 --yes
./chameleon settings save --native-usb --port /dev/ttyACM0 --yes
./chameleon settings clear-bonds --native-usb --port /dev/ttyACM0 --yes
```

`settings get` redacts the PIN by default. `settings reset`, `factory-reset`,
`bootloader` and `firmware enter` use the same explicit maintenance flag.
Ordinary settings, slot, card, reader and raw CLI commands do not accept that
flag as an alternative application transport. `--force` changes command
registration checks, not the USB transport policy. Native pairing changes do
not implicitly save settings; use the save action when persistence is intended.

Full restore retains its separate rollback snapshot and validation requirements:

```sh
./chameleon backup restore --native-usb --port /dev/ttyACM0 \
  --input backup.json --rollback-output before-restore.json \
  --output restore-result.json --yes
```

`--commit` remains an explicit final persistence choice. Slot switching and
initialization can already persist data in firmware. A disconnect can leave
partial changes; no automatic rollback or write replay is performed. Regular
`backup create` uses the bridge. The native restore path captures and saves its
required pre-restore snapshot before attempting restore. Backup files can
contain card keys and the BLE PIN.

For Web pairing maintenance, start a separate server configuration:

```sh
./chameleon serve --native-usb-maintenance /dev/ttyACM0 --allow-writes
```

This cannot be combined with `--port`, `--usb-bridge` or `--ble-bridge`. The port
is fixed by server configuration; browser reconnect can only reopen that port.
Refresh feature status, read settings and select the BLE pairing/PIN fields.
Pairing-only edits retain preflight, readback, optional save, session identity,
background tasks and cancellation semantics. Ordinary device endpoints are
blocked, and the UI shows a bridge migration reason. Native Web mode does not
add full-device restore or DFU controls. Without `--allow-writes`, maintenance
writes remain disabled.

## Go SDK

`Open` and `OpenContext` now accept TCP application addresses. A non-TCP path
returns `ErrUSBBridgeRequired` (usable with `errors.Is`) without native I/O:

```go
client, err := chameleon.OpenContext(ctx, "tcp:127.0.0.1:12345", 10*time.Second)
```

Managed USB uses `NewUSBBridge`, `Scan`, `Select`, `Connect` and `OpenSession`.
For maintenance, explicitly use:

```go
client, err := chameleon.OpenNativeUSBContext(ctx, "/dev/ttyACM0", 10*time.Second)
if err != nil {
    return err
}
defer client.Close()
key := "123456"
_, err = client.ConfigureSettings(ctx, chameleon.SettingsPatch{BLEKey: &key}, true)
```

The native client reports `connection: "serial"`, `medium: "serial"` and
`native_usb_maintenance: true`. It allows model/capability and settings
inspection for maintenance, pairing changes, resets and bootloader entry.
Ordinary application commands are rejected before transmission; mixed pairing
and ordinary settings patches are rejected before any setting is changed.

Use `BackupForRestore` to capture a native pre-restore snapshot and save it before
`RestoreDevice`. `BackupDevice` remains a general application workflow and is
rejected on the native maintenance client. Pre-restore backup and restore have a
private serialized scope, including bounded cleanup after cancellation. That
scope does not enable later ordinary commands on the client or grant access to
other clients. Caller-provided `NewClient` streams retain their existing policy;
Desk does not configure or certify their underlying hardware transport.

Native OS setup may outlast context cancellation. A canceled setup is closed
before returning a client. This path does not claim the Python bridge's setup
thread or queue guarantees. OS/driver behavior still needs hardware validation.

## DFU

DFU remains a separate native USB workflow. Stop the bridge, explicitly enter
bootloader through maintenance if needed, then enumerate the new DFU port:

```sh
./chameleon firmware enter --native-usb --port /dev/ttyACM0 --yes
./chameleon devices --native-usb
./chameleon firmware flash --port /dev/ttyACM1 --input application.zip --yes
```

The DFU path may differ from the application path. `firmware flash` already
requires an explicit physical DFU port and does not take `--native-usb`; it does
not enter bootloader, guess a port or upload through TCP/BLE. Archive validation
still precedes serial open. Bridge declarations cannot enable DFU, pairing or
full restore.

## Verification limits

Offline tests verify that ordinary openers cannot call the native serial
factory; maintenance and DFU use their separate native paths; routine commands
and mixed patches are rejected; restore scope does not escape after success or
failure; and CLI/Web migration errors do not trigger native fallback. Bridge
integration and Chromium tests retain USB/BLE session and task coverage. Hardware
test opt-ins now need a TCP bridge address in `CHAMELEON_TEST_PORT` for routine
operations. No automated test establishes physical USB driver compatibility,
firmware update success or transport throughput.
