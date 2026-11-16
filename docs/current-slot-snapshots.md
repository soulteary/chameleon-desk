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

Even a complete slot snapshot remains a draft in this phase. Standard-card
conversion, slot loading, binary/NDEF export, physical writing and Classic repair
are not enabled for this source kind. This avoids dropping unknown/scope metadata
or treating emulator identity as physical-read authority. Exact memory and
anticollision identity are retained separately even when they differ.

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
These tests do not qualify physical adapters/cards or concurrent RF activity.
