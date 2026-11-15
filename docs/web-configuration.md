# Web settings and slot editing

The device window reads settings and edits animation, sleep timeout, long-press
threshold, A/B short/long button actions, pairing policy and PIN. The slot window
reads all slot types, enable flags and nicknames, and edits one slot/channel at a
time: nickname update/delete, type, enable state or initialization. Settings and
slot operations use the existing server-owned session and background task layer.
There is no automatic retry after a mutation failure.

Read device information or feature status first. Read-only servers can read the
new views; mutations require `serve --allow-writes`, the existing token and the
observed session. Buttons display capability/parameter/transport disable reasons.
Each edit computes only the selected field/operation requirements; it does not
require every command from the conditional vocabulary. Backend operations probe
actual model/capabilities and evaluate the same requirements before modifying
hardware. Submitted preview facts do not authorize real writes.

## Settings

- `GET /api/settings/read` returns `SettingsView(false)`. PIN and raw settings
  bytes are redacted. Unsupported optional sleep/threshold getters are reported;
  absent values are not filled with defaults or silently submitted.
- `POST /api/settings/configure` accepts `{"patch":{"animation":2},"persist":true}`.
  The typed patch supports `animation` (0–3), `sleep_seconds` (0–255),
  `long_press_ms` (0–65535), `short_a`, `short_b`, `long_a`, `long_b` (0–5),
  `ble_pairing` (boolean) and `ble_key` (six decimal digits). An empty patch is
  rejected. The form submits one selected field; the API can submit several.
- `ConfigureSettings` preflights selected getters, verifies each setter through
  its corresponding getter, optionally saves, then returns a redacted view.
  `SettingsRequirements` shares the SDK operation builder, including final
  aggregate read and optional save, without changing existing SDK wire order.
- `readback_verified` and `save_confirmed` are acknowledgement indicators.
  False on an error does not prove that nothing changed or was saved. Final
  view/readback errors can occur after earlier updates or a save acknowledgement.
- PIN/pairing changes require a declared direct serial connection, before any
  probe. TCP, BLE and opaque streams cannot elevate this permission. A mixed
  patch containing a restricted field is rejected as a whole. Bond clearing and
  factory reset are not exposed in this form.

PIN input uses a password field and is cleared after submission or selecting a
different setting. Preview and
execution use only the local server; values are not stored in browser receipts
or echoed into task results. The task worker necessarily holds the submitted
payload in memory until execution completes; this is not encrypted storage.
Numeric button/animation values follow existing SDK/firmware enums; this form
makes no claim that every firmware implements every advertised value/type.

## Slot editing

`GET /api/slot/details` uses `SlotDetails`. Read-only requirements are separate
from the grouped, parameter-dependent nickname workflow.

`POST /api/slot/edit` accepts a `SlotEdit`, for example:

```json
{"slot":1,"sense":2,"action":"enable","enabled":true,"persist":true}
```

Slots are 1–8. Sense is LF=1 or HF=2. Exactly one operation is accepted:

- `nickname`: `name` is 1–32 UTF-8 bytes. SDK update verifies the readback.
- `nickname-delete`: no `name`; missing nicknames are handled by the SDK.
- `type`: `tag` must be a known, nonzero type matching the HF/LF channel.
- `enable`: `enabled` is required, including an explicit false.
- `initialize`: `tag` must match the channel's current type and
  `confirm_initialize` must be true. The UI requires explicit confirmation.

Extra operation-specific fields are rejected, as are unknown JSON fields.
The server uses existing SDK mutation methods and readback, under the shared Web
operation gate. Type and enable edits read slot snapshots before and after the
mutation. A mismatch stops the optional save. The snapshots are not a firmware
transaction and physical buttons/external clients can change state concurrently.
The editor does not activate a different slot or switch mode to apply these
configuration operations. It does not provide whole-device initialization,
channel deletion or portable memory import/export in this phase.

Initialization can overwrite data and persist through firmware even when
`persist` is false. `initialization_acknowledged` reports its command response;
`configuration_verified` verifies the selected configuration readback.
`memory_verified` remains false: reading type/enable flags does not verify all
default card bytes. It is not an atomic transaction or reversible preview.

For `persist:true`, `SaveSlotData` runs only after successful configuration
verification. This firmware save may commit other pending slot data as well.
`save_confirmed` means that the save was acknowledged, not independent flash or
power-loss verification. A failed save is not retried. Results retain before/after
snapshots and completed operations where available; those counts are workflow
substeps, not a complete log of acknowledged protocol writes.

## Capability checks and tasks

`POST /api/features/check` additionally accepts:

```json
{"id":"device.configure","device":{},"settings_patch":{"animation":2},"persist":true}
```

```json
{"id":"slot.configure","device":{},"slot_edit":{"slot":1,"sense":2,"action":"enable","enabled":false}}
```

Replace `device` with observed facts; empty/unknown facts do not enable controls.
The server supplies its actual write policy. Invalid parameters, missing getters,
setters/save commands, unknown models and unsuitable pairing transports produce
reasons. Input/session changes invalidate previews and results. Recovered task
results remain in history rather than being applied to a new session's form.

All four routes are accepted by `/api/tasks`, making ten supported task routes.
Configuration tasks distinguish `write_not_started` (preflight stopped before a
mutating SDK call) from `write_outcome_unknown` (a mutating SDK call began but the
workflow did not succeed). `write_attempted` is a call boundary, not proof that a
complete packet reached hardware. Existing legacy task classifications remain
compatible. On an uncertain result, reconnect if needed and read actual state;
no command replay or rollback is attempted. Task lifetime and receipt limits in
[background tasks](background-tasks.md) still apply.

Tests cover selected-command resolution, read-only policy, pairing rejection
before I/O, redaction, readback/save ordering, nickname mismatch, Lite slot edits,
initialization confirmation, stale previews/results and actual Chromium editing
through a TCP simulator. These do not establish physical adapter/card behavior
or power-loss durability.

Routine USB settings and slot editing now use the Python bridge. Web pairing
maintenance requires `serve --native-usb-maintenance PATH --allow-writes`; only
pairing-only patches are accepted there. The native mode retains settings
inspection, feature bootstrap, readback and optional save, while ordinary slot
and application actions show `usb_bridge_required`. See [USB migration](usb-migration.md).
