# Reader results in the Web card library

The Web UI can add Classic, Type2 and LF reader results to its existing local
card library, edit stored data, and export JSON. These are offline library
operations. They do not issue device commands, require `--allow-writes`, resume
reader tasks, or write physical cards. The existing reader session, task and
model/transport checks still govern the preceding read.

## Import, edit and save

1. Perform a reader operation and inspect its result. Choose **导入卡库** to add
   a new Desk read draft. Classic/Type2 need at least one confirmed block/page;
   LF requires a successful result with `present:true`. A no-card result or a
   failure with no confirmed data cannot be imported.
2. Open **卡片库**. The imported draft is selected in **卡片数据编辑**. Each row
   represents a block/page, starting at index zero. LF drafts have one row of
   protocol response/ID bytes. Edit the name or known hex bytes, keeping row
   count, width and unknown markers unchanged.
3. Choose **保存编辑到卡库**. Server validation returns a replacement database;
   only then does the page replace its database and schedule the existing
   IndexedDB save. Wait for **已自动保存** before closing the page. Saving an
   editor draft into the library and committing it to browser storage are
   separate steps, with separate status messages.
4. Export the current card or the database for an independent backup. A card
   exported after an import/save includes both its original read and its edits.
   Search/show an existing card to select it again after refresh.

The card library also accepts the reader's previously exported
`{schema:1, read_result, error}` envelopes through **从导出的读取结果恢复到卡库**.
An envelope exported from the reader before refresh can be reopened here.
Retained task history can explicitly import a completed or partial confirmed
read without repeating the device operation. A server restart retains task metadata and receipts, but not read-result bodies;
the task journal is not card-library storage.
Importing the same result again creates another ID; it does not overwrite an
existing card by UID or merge partial reads.

Ordinary saved Classic and Type2 cards can be selected and edited in the same
editor. Name and memory rows can change; identity, type, folder, color, extras
and unknown metadata are retained. Existing empty rows remain empty. The editor
cannot add/remove rows or resolve missing data. Supported ordinary LF cards also have raw identity and structured protocol
editors; ioProx changes require explicit firmware validation. LF read drafts
retain their restrictions: their edited
raw bytes must still pass the selected protocol's existing decoder. Derived LF
fields in the original observation are not recomputed or presented as an edited
identity; the draft's `rows` are the edited bytes.

## Partial reads and unknown keys

A completed request means the requested operation and cleanup succeeded. It
neither proves full-card coverage nor confirms a physical card's identity or
capacity. This distinction remains after import and editing:

- Type2 rows before the requested start and unreturned requested pages are `??`.
  For example, a request for pages 5–8 returning pages 5–6 has nine draft rows:
  rows 0–4 and 7–8 remain unknown. Pages after the requested end are not inferred.
- Classic uses the requested geometry and confirmed prefix. Unreturned blocks
  remain unknown. Schema-2 `unknown_keys` mask the six bytes of Key A or B in
  the corresponding trailer, even if the wire dump contains zero placeholders.
  Known access bytes and other confirmed trailer bytes remain editable.
- A mode-cleanup failure keeps the original `complete:false`, including when all
  requested bytes were received. Changing bytes does not change that status.
- The editor cannot replace `??` with guesses, default keys or manually supplied
  values. Recovering keys, merging reads and explicitly resolving unknown data
  require a separate workflow. It cannot convert an unknown byte into a known
  zero, or remove a missing page by shortening the data.

The immutable original observation and its failure message remain alongside the
editable rows. Original checksums describe original bytes; they are not checksums
of the edited draft. The parser validates counters, lengths, ranges, Classic
checksum/unknown-key metadata, LF decoding and the draft mask before accepting a
library record. Imported files are user-supplied data, not authenticated evidence
of a hardware read.

## File representation and conversion limits

Read drafts use the existing saved-card/database container with a versioned
`desk_read` extension:

```json
{
  "id": "generated-uuid",
  "name": "Type2 example",
  "uid": "01020304",
  "tag": 0,
  "sak": 0,
  "atqa": [],
  "ats": [],
  "data": [],
  "extra": {},
  "desk_read": {
    "schema": 1,
    "read_result": {
      "schema": 1,
      "kind": "type2",
      "complete": false,
      "start": 1,
      "requested_units": 2,
      "units_read": 1,
      "uid": "01020304",
      "data": "01020304"
    },
    "error": "read interrupted",
    "rows": ["????????", "AA020304", "????????"]
  }
}
```

`desk_read` is a reserved extension; unsupported schemas or malformed values
are rejected rather than interpreted as ordinary cards.

`tag:0`, empty standard memory and empty HF identity fields are deliberate
sentinels, not inferred card values. Classic's requested type and LF's protocol
remain in the original result. Library summaries use that result to show the
correct HF/LF group. Legacy results do not provide the complete identity metadata
for standard HF conversion. New Web Classic reads capture identity;
Legacy Type2 range results still have no confirmed model. Modeled Type2 reads
carry explicit geometry, identity, counters and unknown-page masks.

Read results are initially imported as Desk JSON drafts. An eligible complete
Classic draft with captured identity and no unknown keys can now be explicitly
[converted into a new standard card](classic-promotion.md). `PlanGUICardLoad`,
binary/image export or replacement and Web NDEF still reject unconverted drafts. Slot availability uses
the existing card-plan evaluator and reports the rejection before device access.
The Web inspector disables NDEF handoff for unconverted drafts and explains why.
Modeled Type2 and mapped LF drafts have separate conversion checks; see
[Type2](type2-workflows.md) and [LF](lf-promotion.md). Unsupported models,
unresolved masks, partial/error and identity-less results remain restricted. There is no
automatic fallback that drops the extension.

Exporting a draft as JSON preserves its source, edits and masks; it does not make
it a portable emulator card. Upstream GUI versions and older Desk versions do
not understand the extension. They may retain it as metadata but cannot edit or
load its memory. Use a Desk version supporting schema 1 to reopen these exports.
Existing ordinary card formats and their conversion behavior remain unchanged.

**导出编辑草稿** is different: it exports
`{schema:1, format:"chameleon-desk-editor-draft", card, name, rows_text}` from the
current editor, including invalid or unsaved text. It is a recovery aid, not a
saved-card file or a persistence acknowledgement. The recovery importer restores pending text only when the current saved source
exactly matches the exported source. Changed saved cards are refused. Recovered
text still requires explicit validation and save. Reader-result and saved-card imports are separate.

## Persistence, conflicts and failures

Successful imports, data edits, evidence merges and supported conversions use the same browser-local persistence as
add/delete operations: validation, a 600 ms debounce, atomic current/previous
snapshots, and conflict detection. Refresh, browser restart and Desk restart
restore the committed database at the same browser profile and origin. The
server stores no library file. See [library persistence](library-persistence.md).

Invalid edits or HTTP failures leave the current database and editor text intact.
The editor reports an unsaved error and does not automatically retry. Invalid
JSON, quota failures, unavailable storage and multi-tab conflicts retain the
page database and its last committed snapshots under the existing persistence
rules. Export the database/page draft and use the existing save/recovery controls.

An editor with unsaved changes prompts on page exit and before replacing its
selection. A database change while an edit/import is pending invalidates that
response instead of replacing newer content. The editor retains conflicting text
and permits export, but disables save until the card is selected against the
current database. Reloading/importing a database or restoring a previous snapshot
requires the existing replacement confirmation. Reload/restore clears the editor
selection; file import preserves a conflicting editor until it is discarded or
selected again against the new database. These guards do not make browser shutdown or a crash recover unsaved
editor text. Save edits into the library and wait for the storage acknowledgement.

Local library and exported JSON can contain card data and verified/readable
Classic keys. They are not encrypted secret storage. Submitted key maps and LF
passwords are not copied into library drafts; read results can themselves contain
card keys. Import/export is explicit and does not transmit data to an external
service.

## API and SDK

`POST /api/database` adds two offline operations:

- `import-read`: `{op, database, name, read_result, read_error}` returns
  `{database, card}`. The ID is newly generated. Unknown request fields are
  rejected. Missing results return 400; invalid/empty observations return 422.
- `edit-data`: `{op, database, id, name, rows}` returns `{database, card}`.
  It resolves the existing card and replaces it by the same ID; missing cards
  return 404 and invalid row edits return 422. Row strings accept hex byte pairs
  with optional whitespace; read-draft unknown bytes must remain `??`.

The handlers transform only the supplied database. They do not persist a server
copy, run background hardware tasks or automatically replay after failure.
The browser's library capability state gates import/save controls independently
of device connection. Device disconnects do not invalidate a saved library draft.

The SDK exposes `ReadResult`, `NewGUICardFromRead`, `GUICard.ReadDraft` and
`GUICard.EditData`. Existing `GUIDatabase.AddCard`/`ReplaceCard` and JSON parsers
provide the container and metadata preservation. There is no new CLI reader-to-
library command; existing database/cardfile commands can preserve draft JSON,
while binary/image/load conversion fails with the same explicit restriction.

## Validation

SDK and HTTP tests cover source preservation, unknown-key masks, sparse page
ranges, invalid edits, ordinary metadata retention, offline execution and image/
slot/NDEF restrictions. Web fixtures cover capability/busy state, stale results,
failed saves, unsaved exports, selection replacement and result import. Chromium
exercises reader-to-library handoff and a partial Type2 import/edit/save/export/
refresh workflow against the actual service; persistence tests also cover browser
and server restart. These tests do not qualify physical cards or adapters.
