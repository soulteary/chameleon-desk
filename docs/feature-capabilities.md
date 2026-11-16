# Feature inventory and capability evaluation

`internal/features/catalog.json` is the versioned workflow inventory. The SDK,
CLI and Web server read the same catalog. The [generated reference](features.md)
lists Web/CLI/SDK entries, support levels, model scope, transport policy,
unconditional commands, conditional command vocabulary and implementation
sources. It currently groups 99 workflows; this is not a coverage percentage.
The separate command catalog remains the source for wire IDs and upstream
registration. A generated Wire method is not a complete typed workflow.

## Support and availability are different

An entry is `supported`, `partial`, `raw`, `missing` or `removed` on each surface.
For example, Web database CRUD covers fewer operations than the CLI/SDK; T55xx
arbitrary block writes are raw-only; native BLE is removed. An implemented SDK
workflow does not imply that the Web UI has an entry for it.

Availability is evaluated separately for a surface and observed device context:

- `available`: the evaluated entry, observed model/command requirements and
  policy checks permit an attempt. File contents, card behavior, installed
  workers, network access and current device state still need their runtime
  checks. This is not hardware compatibility or a successful-operation claim.
- `unavailable`: an explicit requirement fails, such as no entry, unsupported
  model, missing command, disallowed transport, invalid parameters or disabled
  writes. Stable reason codes and missing command IDs accompany the result.
- `unknown`: device/transport/capability information or parameter/external
  context is missing. It must not be converted to `available` by assuming a
  recent firmware or a particular device model.

Firmware support uses the device's advertised command IDs, not a minimum
version guessed from a release string. An unknown command list differs from a
known empty list. Reader operations are scoped to Ultra in the pinned firmware;
Lite is not enabled for readers even if a synthetic command list claims those
commands. Unknown model IDs are not treated as Ultra. Device-independent file
tools remain available without either model or firmware information.

`commands` is an all-of list of unconditional requirements. `conditional` lists
parameter branches or a labeled command vocabulary; **do not take their union**.
Parameterized advanced workflows without a request resolver remain unknown.
Their SDKs still perform their existing runtime validation; this first catalog
does not implement a generic execution engine or preflight every CLI flag.
`write` denotes an unconditional device/card write permission requirement;
`effect: parameter-dependent` covers mixed read/write groups. It is not safe to
interpret `write: false` on a parameterized/raw group as proof of no side effects.

Exact saved-card requirements are expanded from `PlanGUICardLoad`, using the same
plan-specific helper as `LoadGUICard`. They include mode management, plan writes,
readback and optional save. One LF card does not require unrelated Classic or
Type2 commands. Raw command evaluation uses the selected command's catalog
metadata and the existing transport guard; unregistered commands require an
explicit compatibility override. Explicit firmware model/version parameters
allow an update check without querying a device.

## SDK and CLI

```go
features := chameleon.Features() // independent copies; no I/O
facts, err := client.FeatureDevice(ctx) // GET_DEVICE_MODEL + GET_DEVICE_CAPABILITIES
if err != nil {
    return err
}
availability, err := chameleon.EvaluateFeature("reader.hf-scan", "sdk",
    chameleon.FeatureContext{Device: &facts}, nil)
```

Use `EvaluateFeatures` for a complete surface view. `FeatureRequest` resolves a
selected saved card/slot/options, a raw command, or explicit firmware-check
parameters. `WritesAllowed` and `BridgeConfigured` are caller-supplied context,
not permissions granted by the SDK. `BridgeMedium` distinguishes `ble` from
`usb-serial`; an omitted value preserves existing BLE evaluator callers.
The configured bridge must match the workflow's `managed-ble` or `managed-usb`
policy. No evaluator call executes a device command.
`Client.FeatureDevice` queries only model and capabilities and reports the
client's existing immutable transport information.

```sh
./chameleon features
./chameleon features --surface web --id reader.hf-scan
./chameleon features --surface sdk --port tcp:127.0.0.1:12345
./chameleon features --surface sdk --native-usb --port /dev/ttyACM0
./chameleon features --surface cli --bridge-configured --bridge-medium usb-serial --id bridge.usb-scan
./chameleon features --surface cli --allow-writes
```

Listing without `--port` does not auto-select or open a device. With an explicit
port, only model and capabilities are queried. `--allow-writes` changes the
advisory report; it does not execute or authorize a mutation. Existing CLI
`--yes`, Web session tokens and `serve --allow-writes` remain in force.

## Web behavior and API

Startup reads `/api/capabilities`, which includes schema version, catalog and
availability without probing hardware. Offline library/NDEF/ACL tools remain
usable. Hardware actions stay disabled until device facts are known; bootstrap
information/feature reads remain available so users can establish those facts.

The device window exposes **Refresh feature status**, which uses
`GET /api/features/device` to read model and capabilities without requiring the
full `Info` workflow. This read-only bootstrap stays enabled even after an
unsupported model or missing commands were observed, so replacing/updating a
device does not require reloading the page. This also lets older firmware explain missing full-info
commands. `GET /api/info` includes evaluated feature states alongside its normal
fields. The feature panel lists all surfaces and constraints, including features
without Web controls. Disabled controls show text reasons and accessible
`aria-describedby` links rather than relying only on hover text.

For a saved card, `POST /api/features/check` accepts `id: "slot.load-card"`,
observed `device`, `card`, `slot`, `persist` and `activate`. It validates and expands
the plan offline. The server supplies its own write/bridge configuration flags.
It does not open hardware and cannot enable writes on a read-only server.
Malformed or stale preview responses leave the write disabled. New input,
disconnection or capability-read failures invalidate outstanding previews.

Capability responses are snapshots. Refresh after reconnecting or replacing a
device. Hardware failures clear the observed facts; scan retries first refresh
facts. A mismatched/malformed schema cannot retain previously enabled hardware
controls. This layer is advisory, not an authorization boundary: the actual
endpoints and SDK retain parameter checks, write permission/token checks,
transport restrictions and firmware responses. A caller can submit fictitious
facts to an offline preview, but that does not grant device permissions.

Pairing/reset and full-device restore policies require an explicit native USB
maintenance connection. `native_usb_maintenance` scopes the native opener to
maintenance workflows and blocks routine application commands before transmission.
Web disables those workflows with `usb_bridge_required`; pairing-only settings
patches and feature/settings inspection remain available in maintenance mode.
The [USB serial TCP bridge](usb-bridge.md) declares its
`usb-serial` medium without granting direct-USB permissions and also blocks
bootloader entry. TCP, including unknown TCP or a self-declared serial permission,
cannot grant these permissions. An opaque caller-managed stream is not certified
as wired by the evaluator. Existing custom-stream SDK compatibility is unchanged.
USB DFU needs a bootloader port and a validated application package; an app
command list cannot certify DFU readiness. There is no built-in BLE DFU backend.
Bridge connection state and browser storage have their own additional runtime
checks. Capability metadata does not replace those controllers.

## Updating the catalog

Update a workflow's stable ID, entries, models, effect, transport and command
requirements in `internal/features/catalog.json`, then regenerate the reference:

```sh
python3 scripts/generate_features.py
python3 scripts/generate_features.py --check
```

Tests verify catalog IDs, command references, SDK symbols, source paths, declared
Web routes/controls, CLI help dispatch and the generated-document fingerprint.
Policy tests cover Ultra/Lite/unknown models, unknown/empty/missing capabilities,
read-only policy, BLE/unknown TCP, raw transport restrictions and parameterized
card requirements. Web tests cover reason rendering, offline availability,
connection invalidation, schema mismatch and stale previews. The real Chromium
suite uses a loopback protocol simulator for model/command changes; it does not
access physical RFID/NFC or Bluetooth hardware.

TCP bridge and explicit native-maintenance connection management use its own lifecycle controller; see [Web device sessions](web-sessions.md). Device facts and card previews are cleared when the observed session changes.

The [background task layer](background-tasks.md) preserves operation checks and reports cancellation and unknown write outcomes separately from advisory availability.

[Web settings and slot editing](web-configuration.md) resolve selected patches/operations, including readback, save commands and pairing transport restrictions. Slot read-only discovery has its own fixed feature entry.

[USB migration](usb-migration.md) documents the removal of native application
openers and auto-selection, explicit native maintenance flags, and retained DFU.

## Reader and library request resolution

Request resolution covers Classic full/selected rereads, verification, explicit
dictionaries and bounded recovery; modeled Type2 reads and ordinary-page writes;
controlled LF writes; draft conversion; and Classic evidence merges. ioProx file
editing selects command 3012 for raw decoding or 3013 for structured composition.
Host LF codecs remain offline. Recovery checks trusted helper availability on
the Web server; browser input cannot select executable paths.

The advanced Web panel reports parameter-specific states. Actual handlers repeat
validation against the live serialized session. Physical write plans are private
HMAC bound to complete input and current session; public plan checksums exclude
trailer credentials. Saved results remain user-supplied evidence, not authenticated
hardware provenance.


`slot.read-current` resolves commands from `SlotReadRequest`: observed current
slot/type and selected HF/LF channel. It is a read-only operation; reader mode,
unsupported layouts, missing commands and native USB maintenance are disabled
with reasons. Previewed mode/slot facts do not replace the SDK's live checks.
