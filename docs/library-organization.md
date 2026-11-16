# Library organization and external files

The card-library window's organizer operates on the same database as the card
editor. Changes use the existing IndexedDB autosave, revision checks and
save-failure recovery. Export a database backup before replacing it. Unsaved
card or dictionary edits block replacement; a failed operation retains the
current library and pending edits.

Select Cards or Dictionaries, then a folder. Create or rename folders, change a
parent, or select records and move them together. Moves are validated before any
record changes. Folder cycles, missing destinations and deletion of nonempty
folders are rejected. Card IDs, snapshot provenance, dictionary metadata and
unrecognized database fields are retained. Card and dictionary folder trees are
separate. Folder bundles use the existing import/export validation.

Dictionary editing accepts hexadecimal keys with the declared key length.
Duplicates are removed. Changing an existing dictionary's key length is refused;
create a separate dictionary instead. Save or explicitly discard pending edits
before selecting another dictionary. Export key text to preserve an unfinished
edit if saving fails. These controls do not test keys against a device.

## External-file observations

Choose a file, inspect its parsed preview, then import it as a new draft. Import
checks the preview's SHA-256 and the current library revision. Supported subsets:

- Proxmark3 Classic JSON: addressed 16-byte `blocks`, with optional declared
  Card identity. ATQA is interpreted in the format's wire byte order.
- MIFARE Classic Tool sector text: addressed sectors and 16-byte rows; `--`
  bytes remain unknown.
- Flipper NFC version 4: Classic blocks or NTAG/Ultralight pages.
- Flipper RFID version 1: EM4100 identity only.

This is not a general importer for every revision of these formats. Files must
be valid UTF-8, at most 2 MiB, with at most 256 addressed rows. Duplicate JSON
properties/addresses, ambiguous fields, malformed hex and unsupported versions
are rejected. Binary dumps still use the existing explicitly typed import path.

The draft retains original text, format, file hash, declared identity and unknown
byte masks. Missing addresses are unknown, not zero-filled. A file's declared UID
or type is not a live card observation. File drafts cannot become standard cards,
load emulator memory or enter physical-write workflows. Editing known bytes does
not grant missing evidence. Export the draft to retain its source and masks.

Cross-file comparison accepts two Desk card JSON files, including saved file
drafts. It reports differing known bytes and unknown comparisons separately.
Geometry compatibility is not a same-card assertion. Comparison neither merges
files nor changes their authority or enables writing.

## CLI and SDK

All commands below are offline. Output must be a new path; source files and
existing outputs are never overwritten.

```sh
chameleon library view --input database.json --output view.json
chameleon library change --input database.json --action folder-create \
  --name Lab --output next.json
chameleon library change --input next.json --dictionaries \
  --action dictionary-create --name LabKeys --source keys.txt \
  --key-length 12 --output with-keys.json
chameleon library import-file --source dump.mct --preview --output preview.json
chameleon library import-file --input with-keys.json --source dump.mct \
  --output imported.json
chameleon library diff --input left-card.json --source right-card.json \
  --output diff.json
```

`library change` also supports `folder-update`, `folder-delete`, `move`,
`dictionary-update` and `dictionary-delete`. Supply full IDs from `library view`;
`--ids` is a comma-separated list for atomic moves, and `--folder` selects the
destination (empty means root). `--parent` applies to folder creation/update.

The Go entry points are `GUIDatabase.Folders`, `ApplyLibraryChange`,
`ParseCardFile`, `CardFileObservation.ReadResult` and `CompareCards`.
`POST /api/library/manage` exposes the same local validation to Web UI; it does
not open a device connection.
