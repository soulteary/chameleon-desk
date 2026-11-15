# Chameleon Desk

A local RFID/NFC workbench for Chameleon Ultra and Lite, with a desktop Web UI,
command-line tools, and a Go SDK.

The workbench includes device and slot management, HF/LF scanning, a card
library, NDEF editing, MIFARE Classic access-bit tools, and key dictionaries.
The CLI and SDK also provide backup, recovery, firmware, and raw protocol tools.
Available operations depend on the device model and firmware capabilities.

## Build

Requires Go 1.22 or later; use Go 1.27 on macOS 26. Go dependency versions and
checksums are recorded in `go.mod` and `go.sum`.
The build script defaults to `CGO_ENABLED=0`. The Go workbench has no native
BLE backend or third-party cgo dependency. Node.js and Python are not needed
to build or run its USB serial, TCP or offline tools. BLE access requires the
separate Python bridge.

```sh
git clone https://github.com/soulteary/chameleon-desk.git
cd chameleon-desk
go mod download
./scripts/build.sh
./chameleon version
```

The [Python + Bleak bridge](tools/ble_bridge/README.md) provides BLE through
the existing TCP transport on Linux, macOS and Windows. It runs separately.
See its setup, connection limits and offline tests before use. Existing native
BLE users should follow the [migration guide](docs/ble-migration.md).
Its authenticated management API can be enabled with `--control-port` and
`--token-file`; `serve --ble-bridge` exposes scan, selection, connection and
disconnect controls in the Web UI. BLE/TCP transport properties are checked
before pairing changes or full device restore.

## Web UI

```sh
./chameleon serve
```

Open <http://127.0.0.1:8088>. Use the Dock or sidebar to open a tool.
The server listens on loopback addresses only. Device and card writes are
disabled by default. To enable them, restart with `--allow-writes`.

Use the device window to list/select a USB serial port or enter `tcp:host:port`, then connect or disconnect. The server owns one persistent session shared by all pages; no device is auto-selected when started without `--port`. See [Web device sessions](docs/web-sessions.md).

Use `--port` to connect a device when the server starts:

```sh
./chameleon serve --port /dev/ttyACM0
```

The [settings and slot editors](docs/web-configuration.md) provide settings readback, nickname/type/enable changes and explicitly confirmed single-channel initialization.

Device information, scans and slot writes use [background tasks](docs/background-tasks.md) with progress, cancellation and results that can be queried after a page refresh. Interrupted writes can have an unknown outcome and are not replayed automatically. Task records are held in server memory and do not survive a server restart.

Offline file tools work without a connected device. Valid card-library changes
are saved automatically in IndexedDB and restored after refresh or restart,
using the same browser profile and origin (scheme, host and port). Save failures
retain the current draft and the previous committed copy. Export files for
independent backups; browser storage is not a backup service. See
[card-library persistence](docs/library-persistence.md) for recovery, conflicts
and storage limits. Dictionary downloads and firmware checks require internet access.

## CLI and SDK

```sh
./chameleon --help
./chameleon devices
./chameleon info
./chameleon scan --wait 15s
./chameleon commands --supported
./chameleon features --surface cli
./chameleon acl --help
```

CLI mutation commands require `--yes`. Raw command interfaces expose firmware
operations directly; inspect their arguments before use.

The Go module is `github.com/soulteary/chameleon-desk`; its package name is
`chameleon`. Use `go doc github.com/soulteary/chameleon-desk` to inspect the SDK.

The [feature catalog](docs/features.md) records Web/CLI/SDK entrypoints and
requirements. [Capability evaluation](docs/feature-capabilities.md) explains
availability states, parameter-dependent commands and Web disable reasons.
Listing `features` without a port stays offline.

Optional recovery executables can be built with `./scripts/build-recovery.sh`.
They require a C compiler, OpenSSL, and liblzma development libraries. On macOS,
the build script also supports Homebrew's `openssl@3` and `xz` installations.

## Tests

Install Node.js 20 or later (required by the browser test driver) and download
the test and Go dependencies once:

```sh
npm ci --ignore-scripts
go mod download
./scripts/check.sh
```

The check script runs Go race tests, coverage, vet, Web UI regressions, and a
CLI build. It disables hardware-test opt-ins and Go dependency downloads, using
the verified local module cache. Builds and checks use `-mod=readonly`.
Results are written to the ignored `reports/` directory. Hardware tests remain
opt-in and are not part of CI.
The Web tests include IndexedDB transaction, recovery and conflict fixtures.
For the real Chromium reload/browser/server restart check, build the CLI first:

```sh
npx playwright install chromium
CHAMELEON_WEBUI_TEST_BINARY="$PWD/chameleon" npm run test:browser
```

The browser check uses a temporary profile and loopback server, with no device
access. CI runs it separately on Linux with Chromium. The browser driver and
IndexedDB emulator are test dependencies, not production runtime dependencies.

On Linux and macOS, the suite also runs C helper regressions with AddressSanitizer.
These checks need `cc`; complete worker tests also need liblzma and OpenSSL
development files. CI installs these dependencies and requires the C tests.

## License

Chameleon Desk is licensed under [AGPL-3.0](LICENSE). Third-party code retains
its original licenses. Upstream sources, design references, and license texts
are recorded in [licenses/NOTICE](licenses/NOTICE).
