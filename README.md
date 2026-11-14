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

Use `--port` to select a device explicitly:

```sh
./chameleon serve --port /dev/ttyACM0
```

Offline file tools work without a connected device. The card library is edited
in the browser; use its export action to save changes. Dictionary downloads
and firmware checks require internet access.

## CLI and SDK

```sh
./chameleon --help
./chameleon devices
./chameleon info
./chameleon scan --wait 15s
./chameleon commands --supported
./chameleon acl --help
```

CLI mutation commands require `--yes`. Raw command interfaces expose firmware
operations directly; inspect their arguments before use.

The Go module is `github.com/soulteary/chameleon-desk`; its package name is
`chameleon`. Use `go doc github.com/soulteary/chameleon-desk` to inspect the SDK.

Optional recovery executables can be built with `./scripts/build-recovery.sh`.
They require a C compiler, OpenSSL, and liblzma development libraries. On macOS,
the build script also supports Homebrew's `openssl@3` and `xz` installations.

## Tests

Install Node.js 18 or later and download the test and Go dependencies once:

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
On Linux and macOS, the suite also runs C helper regressions with AddressSanitizer.
These checks need `cc`; complete worker tests also need liblzma and OpenSSL
development files. CI installs these dependencies and requires the C tests.

## License

Chameleon Desk is licensed under [AGPL-3.0](LICENSE). Third-party code retains
its original licenses. Upstream sources, design references, and license texts
are recorded in [licenses/NOTICE](licenses/NOTICE).
