# Web reader workflows

The reader window adds three read-only workflows on the existing server-owned
session and background task layer:

- Classic reading with supplied per-sector keys and an expected UID;
- Type2 reading of an explicit page range with UID checks;
- LF reading with a selected protocol, optional HID format or EM4x05 password.

These workflows require Ultra reader commands. Lite remains unavailable even if
a synthetic command list advertises reader commands. Routine USB uses the Python
bridge; BLE and compatible TCP application connections retain their existing
behavior. Native USB maintenance mode rejects these application operations.
There is no native fallback, automatic reconnect or write replay.

## Web use

Connect an application session and refresh device features. HF discovery reads
identity only; use its UID for the Classic or Type2 target. Availability and
disable reasons reflect the model, actual command list, transport and selected
parameters. An offline parameter preview is not authorization: the operation
reads actual model/capabilities again before its first reader command.

The three reader actions work without `--allow-writes`. They can temporarily
change reader mode and RF/card selection. The SDK attempts to restore the original
device mode. Cancellation is not rollback and cleanup can fail after confirmed
reads. Check mode/session state after an interruption; nothing is resubmitted.

Read results show completed units and the requested count. Classic uses 16-byte
blocks, Type2 uses 4-byte pages. Failures can retain a confirmed prefix; no missing
units are zero-filled or presented as a complete card image. A cleanup failure
keeps `complete:false` even if all requested bytes were read. Unknown Classic keys
remain explicit. LF absence is a completed scan with `present:false`; other LF
errors do not assert that a card is absent or present.

Inputs or observed device/session changes invalidate previews and the current
result/export. Late responses do not update a new form/session. Retained task
results stay in task history after refresh and are not applied to the current
form. Keys/passwords are cleared after submission and when device facts change.
Submitted key maps and passwords are not saved to the card library, localStorage or task submission receipts. Explicitly imported read results can contain card keys.
This is DOM clearing, not secure memory erasure: the browser/request and server
worker necessarily hold parameters while executing.

Exports are explicit JSON workflow envelopes (`schema`, `read_result`, `error`),
with complete/partial filenames. They are not GUI card files. Complete Classic
results include the SDK `dump` representation; incomplete prefixes must not be
loaded as full card images. Type2 exports describe only the requested range,
not automatically detected capacity. Results can now be explicitly imported as
[Desk library drafts](web-read-library.md), edited and exported as JSON while
preserving their original observations and unknown bytes. Eligible Classic drafts can be explicitly [converted to standard cards](classic-promotion.md).
Automatic conversion, password-authenticated Type2 reading, key recovery and physical card writing
remain outside these workflows.

Classic results/exports can contain supplied or readable card keys. Task results
are available to authorized pages of this local Desk service and remain in server
memory under the existing limits. Export/back up explicitly when needed; task
history is not a durable card library or encrypted secret store.

## API and selected requirements

All three routes use POST, the existing `X-Chameleon-Token`, session header and
operation gate. Unknown JSON fields and invalid parameters are rejected before
device access. They are also supported `/api/tasks` paths with `write:false`.
Browser refresh queries retained tasks without re-submitting them. Existing
30-second device-operation and five-minute outer task budgets still apply;
progress reports phases and command counts, not percentages. Long reads over a
slow adapter can stop with a partial result. Process restart does not resume them.

### Classic

`POST /api/reader/classic`:

```json
{
  "uid": "01020304",
  "sectors": [
    {"sector": 0, "a": "010203040506", "b": ""},
    {"sector": 1, "a": "111213141516", "b": ""},
    {"sector": 2, "a": "212223242526", "b": ""},
    {"sector": 3, "a": "313233343536", "b": ""},
    {"sector": 4, "a": "414243444546", "b": ""}
  ]
}
```

The template UID/capacity controls only generate input; the JSON UID and sector
list are authoritative for a read. UID is 4, 7 or 10 bytes. The request lists
ordered contiguous sectors from zero:
5 (Mini), 16 (1K), 32 (2K) or 40 (4K). Each sector needs at least one supplied
six-byte key; there is no default-key guessing or recovery. `ClassicRead.KeyMap`
derives the existing key-map completeness flag and shares validation with
`DumpClassicWithKeys`. The Web SDK path captures and compares UID/ATQA/SAK/ATS before each sector and
after the final block. It tries only supplied keys,
verifies supplied hidden keys and retains unknown-key metadata. UID matching is
not physical-card authentication or proof of an atomic snapshot.

The selected requirements are mode commands 1001/1002, HF scan 2000 and Classic
read 2008. If any sector supplies both A and B, authentication 2007 can also be
needed for independent verification. Actual card ACLs may avoid that command;
without reading the card a preview cannot assume this. One-key requests do not
require 2007. Invalid key material is not echoed in capability responses.

### Type2

`POST /api/reader/type2`:

```json
{"uid":"01020304050607","start":3,"count":6}
```

The range must fit pages 0–255, count at least one. This is an input bound, not a
claim that the card has that capacity. `ReadType2Range` holds one SDK reader
operation and checks the expected UID before and after each READ. The Type2 READ
command reads four aligned pages; only requested bytes from a successfully
UID-checked chunk enter the result. A changed UID discards the uncertain chunk
and preserves earlier chunks. The checks cannot detect replacement by a card
with the same UID or establish a transactional snapshot.

Requirements: 1001, 1002, 2000 and raw HF command 2010, with a fixed READ opcode.
The API cannot supply arbitrary raw frames or physical writes. No password
authentication, automatic capacity detection or full-card dump is implied.
Existing `ReadType2Pages` retains its previous SDK/CLI behavior.

### LF

`POST /api/reader/lf`:

```json
{"protocol":"hidprox","format":0}
```

Supported protocols are `em410x`, `hidprox` (`hid` alias), `ioprox`, `viking`,
`pac`, `jablotron`, `pyramid` and `em4x05`. IDTECK encoding support is not a read
protocol. HID format 0 requests automatic detection; nonzero format IDs must be
known to the SDK. `format` is only meaningful for HIDProx. `password` is an
unsigned 32-bit integer used only for EM4x05 (default 0); Web input uses eight hex
digits. Passwords are not included in result metadata.

Requirements are 1001/1002 plus only the selected scan command:
EM410x 3000, HIDProx 3002, ioProx 3010, Viking 3004, PAC 3014, Jablotron 3019,
Pyramid 3021 or EM4x05 3030. This map is shared with `ScanLF`; no union of unrelated
LF commands is required. Reading delegates to `ReadLFRecord` and its typed decode.
The existing `GET /api/scan/lf` remains an EM410x identity-scan compatibility route.

For an offline preview, `POST /api/features/check` accepts the dedicated feature
ID and selected request, for example:

```json
{"id":"reader.type2-range","device":{},"type2_read":{"uid":"01020304","start":0,"count":4}}
```

Use `classic_read` with `reader.classic-keyed`, or `lf_read` with `reader.lf`.
Empty/missing facts do not enable the operation. `classic.read` and `type2.memory`
remain broader partial Web groups; they do not imply low-level auth, passwords
or write controls. The dedicated entries document this phase's exact scope.

## Validation

SDK/HTTP tests cover selected requirements, model/command/transport rejection,
invalid input before I/O, per-sector keys, partial reads, UID changes, cleanup
failure, read-only tasks, no-card semantics and token/session checks. Web fixtures
cover stale previews/results, input invalidation, cleared secrets, export state
and text-only rendering. Chromium exercises all three forms through a TCP
simulator alongside existing refresh/task/session coverage. These tests do not
establish adapter/card compatibility or successful reads from physical hardware.
