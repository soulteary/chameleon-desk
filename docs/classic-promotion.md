# Converting a Classic read draft

The Web library can explicitly convert an eligible Classic read draft into a
standard saved card. Conversion is offline: it creates a new library record,
keeps the source draft and uses the existing browser persistence. It works with
the default read-only server and does not send device or physical-card writes.
Type2 and LF conversion use separate checks; see [Type2 workflows](type2-workflows.md)
and [LF conversion](lf-promotion.md).

## Read, inspect and convert

1. Read Classic with the Web per-sector key form, then inspect the reported
   UID, ATQA, SAK and ATS. Import the result into the card library.
2. Select the draft. Save or discard pending data/name edits before converting.
   The conversion button checks the saved draft through the shared feature
   evaluator and displays its rejection reason when unavailable.
3. Choose **转换为标准卡片（保留草稿）**. The library receives a new ID;
   the original draft remains unchanged. Wait for **已自动保存** before closing.
4. Export the selected standard card as JSON, or export the database to retain
   both records. Refresh/search/show restores the saved records at the same
   browser profile and origin. See [library persistence](library-persistence.md).

An exported standard card can use existing binary/image conversion and NDEF
entrypoints. NDEF still requires a valid NDEF mapping in the memory. Loading it
into an emulator slot remains a separate explicit operation with existing model,
command, session, transport, write-permission and readback checks. Conversion does
not start a load, connect a device, format NDEF or enable physical-card writing.

## Eligibility

The SDK and actual database operation use the same checks as the preview:

- A valid schema-1 Desk draft with a schema-2 Classic dump and a valid checksum.
- For an ordinary draft: `complete:true`, all requested units present, and no
  operation or cleanup error. A full byte count after failed mode restoration is
  ineligible. A merged draft requires complete effective coverage and at least
  one successful full-read source; original errors remain in the evidence tree.
  Selected or partial-only unions cannot establish that full-read authority.
- Captured identity with matching UID, two ATQA bytes, an explicit SAK and an
  explicit ATS value. An empty ATS means observed absence; missing ATS is not
  silently treated as empty. Identity UID must match both result and dump UID.
- No `unknown_keys`, unknown bytes or missing rows. Editing cannot resolve unknown
  bytes or turn a partial/error result into a successful observation.
- Exact memory size: Mini 20 blocks, 1K 64, 2K 128 or 4K 256, each 16 bytes.
- SAK compatible with the selected layout: 0x09 for Mini, 0x08 for 1K/2K and 0x18
  for 4K, ignoring bit 0x80 for this compatibility check only. The observed SAK
  itself is preserved. These checks do not authenticate a chip or infer its
  exact model from SAK/ATQA; geometry still comes from the requested read.
- Supported four- or seven-byte UID block-0 layout. Source and edited block 0
  must retain the observed UID prefix. Remaining manufacturer bytes are opaque:
  they are preserved rather than interpreted as SAK/ATQA or a universal BCC layout.
  Identity comes from the scan response. Ten-byte UIDs and conflicting UID layouts
  remain readable drafts, with a conversion rejection rather than guessed fields
  or a patched block 0.
- Valid Classic access-bit redundancy in every source and edited sector trailer.
  Conversion does not repair invalid ACL bytes.

This separation follows the manufacturer-block / RF-response distinction in the
[NXP Classic EV1 data sheet, sections 8.6.1 and 9.4](https://www.nxp.com/docs/en/data-sheet/MF1S50YYX_V1.pdf).
It does not infer response metadata from manufacturer bytes.

Known memory bytes, including known trailer fields on ordinary drafts, can be edited before
conversion. Key bytes in merged evidence remain immutable; explicitly reread them
to supply new verified evidence. The converted card uses the current saved rows and name. These edits
are not reauthenticated against the source card; the original read and checksum
remain separate evidence of what was observed. Block-0 identity conflicts must
be corrected in the editor or resolved by a new read, not by silent rewriting.

## Identity capture

The Web Classic route now uses `Client.ReadClassicWithIdentity`. It shares the
existing keyed-read implementation and holds one SDK `Reader` operation:

- scan exactly one expected UID before each sector;
- capture/compare UID, ATQA, SAK and ATS across those responses;
- scan/compare again after the final block;
- attempt the existing mode cleanup before reporting completion.

A changed/ambiguous identity or final-scan failure returns an incomplete result.
Confirmed blocks and the first captured identity remain available for inspection
and draft import; conversion remains disabled. Partial prefixes have a checksum
of those prefix bytes, not a complete-image checksum. Cancellation does not
replay the operation. Matching UID/identity cannot detect a substitute card with
the same responses and does not establish an atomic snapshot or authenticity.

The selected firmware commands are unchanged: 1001/1002, 2000 and 2008, plus 2007
when independently verifying supplied A/B keys may require it. The extra final
scan uses 2000. Reader mode changes remain transient state changes rather than
physical memory writes. Ultra, bridge and native-maintenance restrictions remain
in force, as do the existing task budgets and refresh/session guards.

`ReadResult.identity` is additive to schema 1. Its ATQA is a two-byte hex string
in numeric/GUI order: a wire response `04 00` becomes `"0004"`, then standard
saved-card `atqa:[0,4]`. SAK is a JSON byte number and ATS a hex string, including
`""` for observed absence. Both are pointers in `ClassicIdentity` so missing
fields fail validation. No identity defaults are inferred from memory/tag type.

`DumpClassicWithKeys` retains its previous public format and command sequence.
Existing CLI dumps and older Web drafts without captured identity remain usable
as their original files/drafts; they cannot use this conversion. Read again with
the new Web flow or SDK method. The UI has no manual identity-fill control that
could attach a later, unrelated scan to an earlier read.

## Records, provenance and failure behavior

Conversion copies the draft's name, folder, color, extras and unknown metadata,
sets standard UID/SAK/ATQA/ATS/type/memory fields, and allocates a new ID. The active
`desk_read` extension is removed only from that new record. A `desk_read_origin`
audit snapshot contains schema 1, `source_card_id` and the original extension,
including source result, saved edited rows and additional extension fields.
This is a conversion snapshot, not an authenticity claim or a live invariant
for later edits to the standard card. Its source checksum is not an edited-image
checksum. Existing binary/image helpers compute/validate the current image.

`desk_read_origin` is reserved; conversion refuses to overwrite a preexisting
field with that name. The original draft and its unknown metadata are not
modified. Standard JSON follows the existing GUI card representation; upstream
programs may ignore the extra audit metadata. Unconverted drafts and
legacy identity-less drafts keep the existing image/load/NDEF restrictions.

The browser applies a conversion response only if the selected draft, editor
revision and database still match. Invalid previews fail closed. Session changes
do not invalidate this offline operation, while device operations retain their
session checks. HTTP/validation failures retain the draft and page database;
there is no automatic retry. Explicit retry does not replay a device command.

Adding the converted record schedules the existing atomic IndexedDB save. Quota,
validation and multi-tab failures retain the new page database and last committed
snapshots; use the existing retry/export/reload controls. A conversion response
is not a durable-save acknowledgement. No server library file is written.

## API and SDK

`POST /api/features/check` accepts
`{id:"library.promote-classic", card:<saved draft>}` without device facts or a
write flag. The catalog marks conversion as offline and parameterized; no card
means unknown, an ineligible card means unavailable with a reason.

`POST /api/database` with
`{op:"promote-classic", database:<database>, id:<source ID>}` returns
`{database, card}` after `GUIDatabase.AddCard`. Missing source returns 404;
ineligible conversion returns 422. This is a stateless transformation of the
submitted database, not a background hardware task or persisted server change.

The SDK exposes `ClassicIdentity`, `ReadClassicWithIdentity`,
`GUICard.ClassicPromotionError` and `GUICard.PromoteClassicDraft`. Preview does not
allocate an ID. The conversion method repeats all validation and allocates a new
ID only on success. The CLI exposes `read-draft promote --kind classic`; see
[reader CLI](reader-cli.md) for input envelopes and separate output requirements.

## Validation

SDK tests cover both UID layouts, all four memory sizes, byte order, identity
changes, final-scan/cleanup failures, partial prefixes, missing identity, unknown
keys, conflicting edits, ACL checks and metadata/source preservation. HTTP tests
verify offline append behavior, preview parity and read-only-server operation.
Web fixtures cover disabled reasons, stale previews/results, dirty editors,
conversion failure and device-session independence. Chromium reads a simulated
Classic, imports/converts it, exports the standard file and verifies persistence
with the source retained. These tests do not qualify physical cards or adapters.
