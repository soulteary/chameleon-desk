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
to build Go, run TCP/offline tools or use explicit native USB maintenance/DFU.
Routine USB and BLE access require the separate Python bridge.

For matched core/bridge artifacts, offline dependency installation and platform
startup/diagnostic steps, see [distribution and bridge diagnosis](docs/distribution.md).
`chameleon bridge doctor` reads management status only; the Python bridge's
`--doctor` checks local dependencies/listeners without opening hardware.

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

The same Python entrypoint has a [USB serial TCP backend](docs/usb-bridge.md)
using pySerial. Select a fixed application port with `--serial-port`, then use
Desk's ordinary TCP connection. Native USB is reserved for explicit maintenance
and DFU. The USB bridge does not enable pairing changes, full-device restore or DFU. For scanning and
selection from Web UI, start the bridge with `--usb --control-port ...
--token-file ...`, then configure Desk with `serve --usb-bridge` and
`--bridge-token-file`. BLE management remains available. Existing native USB
users should follow the
[USB migration guide](docs/usb-migration.md).

## Web UI

```sh
./chameleon serve
```

Open <http://127.0.0.1:8088>. Use the Dock or sidebar to open a tool.
The server listens on loopback addresses only. Device and card writes are
disabled by default. To enable them, restart with `--allow-writes`.

Use managed USB bridge controls to scan/select a port on the Python host, or enter an explicit `tcp:host:port` for a fixed bridge, then connect or disconnect. The server owns one persistent session shared by all pages; no device is auto-selected when started without `--port`. See [Web device sessions](docs/web-sessions.md).

Use `--port` to connect a device when the server starts:

```sh
./chameleon serve --port tcp:127.0.0.1:12345
```

The [settings and slot editors](docs/web-configuration.md) provide settings readback, nickname/type/enable changes and explicitly confirmed single-channel initialization.

The [reader workflows and saved-card editor](docs/reader-workflows.md) support
Classic repair and conflict-aware merge, modeled Type2 full/authenticated reads,
and mapped LF conversion. Complete eligible reads can become standard saved
cards while preserving their sources. Memory address/diff and access-condition
editing, structured LF fields, explicit editor-draft recovery, task-history
import and a slot-loader shortcut reuse the card-library persistence layer.

Controlled Classic, Type2 user-page and LF T55xx physical writes require a
reviewed current-session plan and explicit execution with `--allow-writes`.
Target checks, protected ranges and verification limits differ by protocol;
see the workflow documentation before writing. Interrupted writes are not
replayed automatically. See the [hardware acceptance record](docs/hardware-acceptance.md)
for tested devices and remaining gaps.

Device information, scans and slot writes use [background tasks](docs/background-tasks.md) with progress, cancellation and results that can be queried after a page refresh. Interrupted writes can have an unknown outcome and are not replayed automatically. Task metadata and deduplication receipts survive server restart in a private journal; result bodies remain in memory. Running writes recover with an unknown outcome and are never resumed. Web, `chameleon tasks` and the Go `TaskClient` share the [operation registry and write policy](docs/operations-and-write-policy.md).

The slot window can [read the current emulator slot](docs/current-slot-snapshots.md), preview confirmed and unknown data, and import a distinct snapshot draft into the local card library. Complete snapshots that pass type-specific checks can be explicitly converted to standard saved cards while retaining their source draft and provenance. This does not switch slots/modes or enable physical-card writing.

The [recovery companion and environment tools](docs/recovery-environment.md) provide
matching native workers, bridge management through Desk tasks, and a Web diagnosis
panel. They preserve the existing transport and write boundaries.

The [R1/R2 delivery notes](docs/r1-r2-delivery.md) describe the typed reader and
controlled-write forms, [library organization and external-file drafts](docs/library-organization.md),
and [current-emulator controls](docs/emulator-tools.md), including their validation limits.

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
./chameleon usb scan --usb-bridge http://127.0.0.1:12346 --bridge-token-file tools/ble_bridge/usb.key
./chameleon info --port tcp:127.0.0.1:12345
./chameleon scan --port tcp:127.0.0.1:12345 --wait 15s
./chameleon commands --supported --port tcp:127.0.0.1:12345
./chameleon features --surface cli
./chameleon acl --help
```

Pairing, reset and full restore use explicit native maintenance (`--native-usb`
for CLI, `OpenNativeUSBContext` for SDK, or `serve --native-usb-maintenance PATH`
for Web pairing). DFU retains its separate physical port. Routine application
commands never auto-select or fall back to native USB.

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
See [CI stages, caching and timing](docs/ci.md) for the job layout, standalone
check stages, report artifacts and pull-request cancellation policy.
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
