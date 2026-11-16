# Current-slot snapshots

The slot window can read the current emulator slot, preview the selected HF/LF
channel, export the observation and import it into the existing local card
library. This is a read of emulator memory, not a physical-card read, a flash
backup or a claim that the original source card was complete.

## Web workflow

1. Connect through the existing Desk device session and choose **槽位管理**.
2. Choose **读取槽位** to observe the active slot and device mode.
3. In **读取当前槽位到卡库**, select HF memory/identity or LF protocol identity.
   Desk checks the selected type and advertised firmware commands. Missing
   capabilities, unsupported types, unknown slot information, reader mode and
   native USB maintenance keep reading disabled with a reason.
4. Choose **读取当前槽位**. The registered task `slot.read-current` retains its
   submitted session and uses the common device gate. Progress/cancellation and
   retained results use the existing task panel. This operation needs no
   `--allow-writes` permission.
5. Inspect the slot, type, enabled state, nickname, emulator identity, confirmed
   units, unknown rows and original result. An error can retain a confirmed
   prefix. A detected source change prevents import.
6. Set an import name and choose **导入快照草稿到卡库**. This adds a new record;
   it does not overwrite another record with the same UID or write to a slot.
   Wait for the existing **已自动保存** status. Saving failure and unsaved/conflict
   reminders use the existing library persistence controls.

After a page refresh, saved snapshots are available in the card library. An
unimported task result can be explicitly queried/imported through task history
while its body is retained. It is not automatically applied to the current slot
view. Device/feature changes clear the live preview, and old session responses
cannot replace it. A server restart retains task metadata/receipts, not result
bodies: import or export required observations before stopping Desk.

**导出读取快照** produces the existing `{schema:1, read_result, error}` envelope.
It can be recovered through the card library's existing read-result import.
Exporting the selected library record preserves the snapshot, editable rows and
unknown markers. Exports/library storage may include emulator keys or credentials;
they are not encrypted by this workflow.

## Supported scope

- Classic: Mini, 1K, 2K and 4K emulator memory, including manufacturer/trailer
  bytes as returned by the emulator getter. Captured emulator keys are not
  described as recovered keys from a physical card.
- Type2: the existing modeled slot types 1100–1108. The firmware page count must
  match the modeled layout before any memory is accepted. This does not infer a
  new layout from an RF response or a failed command.
- LF: EM410x variants, Electra, HIDProx, ioProx, Viking, PAC, Jablotron and IDTECK
  identities with their existing host decoders. Full encoded ID bytes and the
  selected slot type are preserved. Pyramid, EM4x05 and other unsupported slot
  types are rejected rather than guessed.

A disabled channel may still be read when the firmware exposes its stored data.
The result retains its enabled state; it does not enable the channel.

Version, signature, counters, advanced emulator configuration, flash persistence
and physical-source provenance are explicitly listed as uncaptured. These fields
must not be inferred from a complete memory transfer. A byte returned by the
emulator getter is known emulator RAM; this cannot reconstruct unknown-source
markers that an earlier upload did not store on the device.

## Read and consistency boundaries

The caller supplies the observed active slot (1–8), sense (HF 2 / LF 1) and tag
type. The SDK validates the request, transport and exact firmware requirements.
It refuses reader mode, a different active slot or a different type. It never
sends mode/slot selection, initialization, memory writes or save commands, and
never retries a failed read automatically.

HF chunks contain at most 32 blocks/pages. Source metadata is observed initially
and after each chunk: mode, active slot, selected type/enable flag, nickname and
anticollision identity. A chunk enters the confirmed prefix only after a complete
response of the expected size and matching metadata. Failed/short/unverified
chunks and the remaining suffix stay unknown. LF identities require matching
getter responses with a metadata check between them. A detected source change
is retained in `source_changed` and blocks library import.

The Desk/SDK gates prevent another operation on the same owned session from
interleaving. External RF activity can still mutate RAM without changing this
metadata, and values can change between observations. Completion is not an
atomic memory snapshot, exclusive RF access, stored-flash verification or chip
authentication. Reads are bounded by existing command/operation deadlines.
Canceling an in-flight command may invalidate the connection; no automatic
reconnection or task replay is added.

## Data format and library behavior

`Client.ReadCurrentSlot` returns `ReadResult` with `kind:"slot"` and
`slot_snapshot`. It uses a separate source kind so emulator observations cannot
enter the physical Classic/Type2/LF promotion and repair paths.

The schema-1 snapshot retains:

- `source:"emulator_slot"`, source slot/sense/type and `metadata_read`.
- Enabled state and nullable nickname. When metadata was not captured,
  `metadata_read:false` prevents treating default fields as observed values.
- HF UID, numeric-order ATQA, SAK and explicit ATS (including observed empty ATS),
  or the LF protocol/type/encoded ID and decoded fields.
- Modeled unit count/width, confirmed unit count and exact confirmed bytes.
- Explicit unknown suffix ranges, completion/source-change flags and uncaptured
  scope. No guessed zero bytes are appended to the data.
- `sha256` over the SDK's canonical typed snapshot serialization with its hash
  field cleared. `SlotSnapshot.Validate` checks it along with geometry, counts,
  identities, scope and unknown ranges. It detects accidental changes, not
  authenticity or tampering by a party able to recompute the hash.

`SlotSnapshot.DraftRows` produces addressed hex rows and `??` for unknown bytes.
`NewGUICardFromRead` imports the result as a distinct Desk read draft: tag 0,
empty ordinary card data and the original snapshot inside `desk_read`. Known
rows can be edited through the existing editor while the original observation
and checksum remain unchanged. Unknown bytes cannot be supplied by this editor.
Card JSON/database metadata continues to use the existing lossless container.

Imported snapshots remain drafts until an explicit conversion passes the checks
below. Partial snapshots remain JSON drafts. Physical-read promotion and Classic
repair are separate workflows; slot observations do not become physical-card
read evidence. Exact memory and anticollision identity remain separate even when
they differ; inconsistent identity prevents standard-card conversion.

## Explicit standard-card conversion

In the card library, select a saved slot draft. The existing **转换为标准卡片（保留草稿）**
button checks `library.promote-slot` and shows a reason when conversion is not
available. Save or discard pending edits before converting. Conversion is offline
and requires neither a device connection nor `--allow-writes`. It creates a new
standard saved-card record; it leaves the source draft unchanged and uses the
existing autosave, save-error, conflict and recovery controls. Wait for **已自动保存**
before closing the page. Refreshing does not repeat conversion.

All of these conditions must hold:

- A valid schema-1 slot snapshot and envelope, matching checksum, complete modeled
  geometry, confirmed metadata, no unknown ranges, no source-change flag and no
  read error. Replacing unknown bytes or changing completion flags does not repair
  an interrupted snapshot.
- Complete captured HF identity, or a matching LF protocol/type/encoded identity.
  Original memory and saved edited rows are checked independently. Editing a
  draft cannot make invalid original evidence eligible.
- Classic: Mini/1K/2K/4K with four- or seven-byte UID, matching geometry/SAK,
  block-0 UID and valid access-bit complements in every trailer. All memory and
  key bytes must be known emulator bytes. Manufacturer bytes remain opaque;
  conversion does not impose a universal block-0 BCC/SAK/ATQA layout.
- Type2: slot types 1100–1108 with the existing modeled page count, seven-byte UID,
  SAK 00, ATQA 0044, explicit empty ATS, and matching UID/BCC pages. Stored emulator
  password/PACK pages are copied exactly. The separate physical-Type2 conversion
  options for assigning unknown credentials do not apply and are rejected.
- LF: the snapshot protocols listed above, including IDTECK, with valid encoded
  identities according to the existing codecs. Edited ioProx bytes are rejected
  because the host codec does not establish their firmware encoding relationship.

`GUICard.SlotPromotionError` and `GUICard.PromoteSlotDraft` share these checks with
feature previews and the Web conversion handler. A preview does not replace
validation at conversion time. Missing identity, incomplete memory, invalid
Classic trailers, inconsistent Type2 UID/BCC, invalid LF encodings and reserved
provenance conflicts produce reasons without adding a standard card.

The standard card retains unrelated card metadata and a `desk_read_origin` object
with `schema:1`, `source:"emulator_slot"`, `source_card_id`,
`physical_write_allowed:false` and the complete original `draft` extension. That
extension retains the observation, checksum, edits, unknown/scope markers and
future source fields. The Web editor displays the source slot and its scope on
the converted card, including after reload. Export card/database JSON to retain
this provenance; raw binary/emulator images do not carry it.

Version, signature, counters, advanced emulator configuration and flash state
remain uncaptured. Conversion neither assigns Type2 extras nor claims to restore
them. Separately attached `extra.ultralightVersion`, `ultralightSignature` or
`ultralightCounters` are rejected for Type2 slot conversion rather than described
as captured fields. Unrelated `extra` metadata is preserved. Complete stored
Type2 bytes are emulator RAM, so physical RF credential masks and mirror-byte
rules are not applied to this source kind.

Converted cards may use existing file export and slot-load planning. Loading is
an explicit separate device write, with its existing capability, session and
write-permission checks. Default initialization can change uncaptured settings;
it is not a full snapshot/configuration restore. No physical-card write handoff
is added. The existing LF saved-card write planner and capability check reject
slot-derived cards, including after editing and JSON reload. Classic/Type2
physical write APIs retain their separate request formats; conversion does not
produce those requests or confirmations. Source metadata is not authentication
or an access-control mechanism for manually reconstructed bytes.

Offline CLI conversion uses the same validation and new-ID behavior:

```sh
./chameleon read-draft promote --kind slot --input slot-draft.json --plan
./chameleon read-draft promote --kind slot --input slot-draft.json --output card.json
./chameleon database promote-read --kind slot --input database.json \
  --id SOURCE_DRAFT_ID --output converted-database.json
```

`--kind auto` also selects slot conversion for a slot draft. Keep output files
separate from their inputs. SDK callers use `source.PromoteSlotDraft()` followed
by `database.AddCard(converted)`, then persist through their existing store. The
Web API uses `POST /api/features/check` with `id:"library.promote-slot"` and `card`,
and `POST /api/database` with `op:"promote-slot"`, `database` and the source `id`.
Neither endpoint sends device commands.

## SDK and CLI task entry

```go
read, readErr := client.ReadCurrentSlot(ctx, chameleon.SlotReadRequest{
    Slot: 3, Sense: chameleon.SenseHF, TagType: 1001,
})
// Inspect readErr and retain partial observations. Only confirmed data without
// a detected source change can be imported. The original error is retained.
if read.Read > 0 && read.Slot != nil && !read.Slot.SourceChanged {
    message := ""
    if readErr != nil { message = readErr.Error() }
    card, err := chameleon.NewGUICardFromRead("Slot 3", read, message)
    if err != nil { return err }
    next, err := database.AddCard(card)
    if err != nil { return err }
    database = next // Persist/export through the caller's existing library store.
}
```

The existing task client/CLI can execute the same registered handler in Desk's
owned session, without opening a competing bridge connection:

```sh
./chameleon tasks submit --token-file ./private-state/tasks.jsonl.token \
  --request-id current-slot-read-0001 --session-id OBSERVED_SESSION_ID \
  --operation slot.read-current --body-file slot-read.json
```

`slot-read.json` contains `{"slot":3,"sense":2,"tag_type":1001}`. Use
`tasks get`, `find`, `wait` or `cancel` as documented in
[background tasks](background-tasks.md). The feature catalog exposes
`slot.read-current`; parameter previews use `slot_read` and optional observed
`device_mode`. They remain advisory; live SDK checks are authoritative.

## Validation

Offline tests cover all modeled HF geometries, selected LF identity encodings,
partial data/unknown masks, checksums, metadata changes, malformed lengths,
missing commands, reader mode and native-maintenance rejection. HTTP tests cover
read-only task registration, session/token/method enforcement, deduplication and
offline library import. Web fixtures cover stale responses/import guards and
unknown-data rendering. Chromium covers partial read, preview, autosave, reload
and lossless export while checking that no mutation commands were issued.
Conversion tests cover all modeled snapshot types, original/edited validation,
append-only library behavior, source preservation, CLI/HTTP previews and the LF
physical-write boundary. Chromium also covers explicit conversion, retained
source records, autosave, reload and provenance export.
These tests do not qualify physical adapters/cards or concurrent RF activity.
