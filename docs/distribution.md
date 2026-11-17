# Core and bridge distribution

The native distribution contains a core binary, USB/BLE bridge source, separate
offline wheel sets, hash-locked install inputs, licenses and build metadata.
CI companion builds also include matching native recovery workers, their sources
and build metadata. Python, drivers and Bluetooth services are not included.
No installer starts a bridge, opens hardware, enables writes or installs system
packages. Native USB maintenance and DFU remain separate explicit workflows.

## Choose and verify a build

Download the archive and its `.zip.sha256` from the same successful Checks run.
CI supplies Linux x86-64, Windows x86-64 and macOS arm64 artifacts. Other native
architectures require a separate build and validation; these are not universal
binaries. These are Actions artifacts, not a new signed release/update channel.

Compare the archive SHA-256 with the accompanying file before extracting:

- Linux: `sha256sum chameleon-desk-*.zip`
- macOS: `shasum -a 256 chameleon-desk-*.zip`
- PowerShell: `Get-FileHash .\chameleon-desk-*.zip -Algorithm SHA256`

Extract to a new directory. Keep its files together and use the Python minor
version recorded in `distribution.json` (CI uses Python 3.11). The installer
checks every packaged file, OS/architecture/Python, and core/bridge identities
before installing. A digest detects corruption; it does not authenticate an
archive if the archive and checksum were both replaced. Obtain both from the
intended repository/run. The archive is not code-signed or notarized.

Linux/macOS, from the extracted directory:

```sh
python3.11 install.py --verify
python3.11 install.py --backend usb
.venv/bin/python bridge/bridge.py --version
./chameleon version --json
```

Windows PowerShell, from the extracted directory:

```powershell
py -3.11 install.py --verify
py -3.11 install.py --backend usb
.\.venv\Scripts\python.exe bridge\bridge.py --version
.\chameleon.exe version --json
```

Use `--backend ble` for BLE only, or `--backend both` for both. USB installation
does not install/import Bleak. Installation uses a fresh `.venv`, local wheels,
`--no-index --no-deps --require-hashes`, then `pip check`. It does not contact a
package index or replace an existing environment. If installation fails, use a
fresh extraction after diagnosing the cause. Do not copy/move an installed venv.
Python with `venv`/`ensurepip` must already be installed; some Linux distributions
package these separately. Offline/file tools and native maintenance do not need
this Python environment.

## Diagnose before starting

Linux/macOS:

```sh
.venv/bin/python bridge/bridge.py --doctor --backend usb --serial-port /dev/ttyACM0 --control-port 12346 --token-file usb.key
```

Windows:

```powershell
.\.venv\Scripts\python.exe bridge\bridge.py --doctor --backend usb --serial-port COM7 --control-port 12346 --token-file usb.key
```

For BLE, use `--backend ble` and omit `--serial-port`. This JSON report checks
the selected dependency/version/import, data/control bind availability, existing
token file format/available permission metadata and POSIX serial access metadata.
It never opens a serial device, scans Bluetooth, changes pairing or sends firmware
commands. A bind result is a snapshot, not a port reservation. A missing new token
is `unknown`; management startup creates it. Windows ACLs require separate review.
Do not run this bind check against an already running bridge: its ports should
be occupied. Use the core management doctor below for a running bridge.

`pass` means that specific check passed; `unknown` is not hardware readiness.
Exit status is 1 for any failed check, 0 otherwise (including unknown checks).
Runtime USB setup failures preserve sanitized codes such as `serial_port_busy`,
`serial_port_missing` and `permission_denied`. On Windows, access denied can mean
permissions **or** another owner: `serial_access_denied_or_busy`. BLE connection
setup errors distinguish recognized permission, adapter-not-ready and service
failures in managed mode; unrecognized setup failures remain generic. The core
doctor reports allowlisted backend codes without echoing backend exception text.

- Linux: check the application port, device group/udev rules, other serial clients,
  and for BLE BlueZ/D-Bus, adapter power and desktop permission policy.
- macOS: choose `/dev/cu.*`; check Bluetooth privacy permission for the actual
  Python executable/terminal. Do not infer permission from an import succeeding.
- Windows: check Device Manager/COM selection, driver, other serial clients and
  Bluetooth adapter/privacy settings. A busy COM port can report access denied.

No diagnostic changes privileges, kills port owners or retries a device write.

## Start USB or BLE explicitly

Linux/macOS, USB:

```sh
.venv/bin/python bridge/bridge.py --usb --port 12345 --control-port 12346 --token-file usb.key
# Another terminal in the same directory:
./chameleon bridge doctor --usb-bridge http://127.0.0.1:12346 --bridge-token-file usb.key --json
./chameleon serve --usb-bridge http://127.0.0.1:12346 --bridge-token-file usb.key
```

Windows, USB:

```powershell
.\.venv\Scripts\python.exe bridge\bridge.py --usb --port 12345 --control-port 12346 --token-file usb.key
# Another PowerShell in the same directory:
.\chameleon.exe bridge doctor --usb-bridge http://127.0.0.1:12346 --bridge-token-file usb.key --json
.\chameleon.exe serve --usb-bridge http://127.0.0.1:12346 --bridge-token-file usb.key
```

Open `http://127.0.0.1:8088`, scan, select the **application** port, then connect.
For BLE on any platform, omit `--usb` on Python and replace the Desk flag with
`--ble-bridge`; use a separate token and stop the USB bridge first or choose
different listener ports. Scan/select/connect in Web. No bridge is auto-launched.

For fixed USB, replace the Python management arguments with
`--serial-port /dev/ttyACM0 --port 12345` (or `COM7`). Desk uses
`--port tcp:127.0.0.1:12345`. Fixed mode has no management doctor endpoint;
local doctor still applies. Fixed BLE uses `--device` instead. Keep the existing
USB/BLE setup documentation for adapter-specific requirements.

The core doctor makes one authenticated `GET /v1/status`: no scan, selection,
connection changes, data socket claim or firmware commands. It reports missing
listener, authentication, timeout, invalid protocol/medium, cleanup blocking,
active data ownership and version mismatch. It redacts tokens and backend error
text. Backend occupancy/permissions cannot always be distinguished without an
explicit connection attempt. The existing device `doctor --port ...` is a
different command that does query firmware.

Matching distributions advertise release, compatibility generation and source
revision on status/scan and the TCP declaration. A declared release/compatibility
mismatch, or differing known revisions, is rejected; no native fallback occurs.
Legacy peers without metadata remain supported and are reported as unverified.
Source builds without a stamped revision verify release/compatibility only; they
must not be described as exact revision matches. Update the shared
`tools/ble_bridge/version.json` and SDK version together for a release; bump its
compatibility generation when the bridge contract becomes incompatible.

## Build a native distribution

On each target OS/architecture, install Go (1.22+, 1.27 on current macOS), Python
3.11+ and cache Go modules. Prepare wheel sets using the same Python that will
build and install the bundle. This network step is explicit:

```sh
python -m pip download --only-binary=:all: -r tools/ble_bridge/requirements-usb.txt --dest reports/wheels-usb
python -m pip download --only-binary=:all: -r tools/ble_bridge/requirements.txt --dest reports/wheels-ble
python scripts/build_distribution.py --with-recovery --ref HEAD --usb-wheelhouse reports/wheels-usb --ble-wheelhouse reports/wheels-ble
python scripts/test_distribution_smoke.py reports/distribution
```

Use `py -3.11` instead of `python` on Windows if needed. The builder exports only
the selected committed tree, ignores dirty/untracked files, sets `CGO_ENABLED=0`,
uses `-mod=readonly -trimpath -buildvcs=false` and stamps the same revision into
core and bridge. It does not cross-build wheels. Core serial dependencies remain
for maintenance/DFU. Use `--with-recovery` to include the matching native C worker companion (enabled
in CI artifacts); building it requires native C dependencies. See
[recovery companions and diagnostics](recovery-environment.md).

The resulting archive records Go, Python and zlib versions, target and exact wheel files/hashes;
ZIP entry order, permissions and timestamps are normalized. CI builds twice
with the same commit/toolchain/wheel inputs and compares archive bytes, then
extracts, verifies, installs offline and checks USB/BLE imports and management
diagnosis. Re-resolving transitive wheels from an index later can produce a
different input set; archive reproducibility requires retaining the recorded
wheels and toolchain, not just the top-level requirements. The smoke test uses
no physical adapter, device or card and does not establish driver compatibility.

## Web and worker diagnostics

The Web environment panel and `environment doctor` inspect the configured worker
companion and running-bridge host checks without opening hardware or executing
workers. The bridge CLI controls the existing Desk session through tasks. See
[recovery and bridge environment workflows](recovery-environment.md) for commands,
unknown states, compatibility checks and acceptance boundaries.

## Transport and maintenance boundaries

Keep the existing USB/BLE restrictions. Management authentication does not cover
the unauthenticated loopback data socket. Pairing/reset/full restore are not
enabled by build metadata; USB bridges still block bootloader/DFU. Stop/release
the bridge before explicit native maintenance or DFU. A process-local operation
gate is not a system-wide serial lock. No installer/doctor reconnects, switches
to native USB, rolls back hardware writes or replays a write command.

References: [pip hash checking](https://pip.pypa.io/en/stable/topics/secure-installs/)
and [Python venv](https://docs.python.org/3/library/venv.html).
