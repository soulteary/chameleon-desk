# Reader and draft CLI workflows

These commands add a reproducible route from reader operation to saved-card
files and databases. Existing `mfc dump-keys` output/command sequencing remains
unchanged; use the new reader entry to capture Classic RF identity.

## Reader envelopes

```sh
chameleon reader classic --port tcp:127.0.0.1:18888 \
  --input sector-keys.json --output classic-read.json
chameleon reader type2 --port tcp:127.0.0.1:18888 \
  --input type2-request.json --output type2-read.json
chameleon reader lf --port tcp:127.0.0.1:18888 \
  --protocol pac --output pac-read.json
```

Classic input uses `{uid,sectors}` from the Web per-sector reader. The new entry
calls `ReadClassicWithIdentity`, retaining UID/ATQA/SAK/ATS from the same Reader
operation and including cleanup in completion. LF selects the existing protocol;
HID can add `--format ID`, and EM4x05 can add an explicit `--password HEX8`.
Type2 input is `{uid,full:true}` or `{uid,start,count}`, with an explicit expected
seven-byte UID and optional four-byte hex `password`. It uses exact modeled
GET_VERSION layouts and does not retry authentication. Unknown credential pages
remain masked; passwords/PACK are not included in result metadata. These commands read actual model
and advertised capabilities before the reader operation.

A bridge/compatible TCP `--port`, positive per-command `--timeout` (default 5s),
positive `--operation-timeout` (default 30s), and a new `--output` are required.
The command never enumerates or falls back to native USB, reconnects automatically,
or repeats a reader/write operation after failure. Classic keys and LF passwords
are validated before transport access, and predictable output failures are checked
before device access.

Output is `{schema:1,read_result,error}`. Once the reader operation ran, an
incomplete result is saved when possible and the command exits with an error.
The result distinguishes incomplete reads and cleanup failures. LF absence is a
completed scan with `present:false`, not a card draft. A late save failure reports
that the device operation already ran; it is not a reason to replay that operation.

## Offline import and conversion

```sh
chameleon read-draft import --input classic-read.json \
  --name 'Classic read' --output classic-draft.json
chameleon read-draft promote --input classic-draft.json \
  --plan --output conversion-plan.json
chameleon read-draft promote --input classic-draft.json \
  --output classic-card.json
```

Import requires an actual schema-1 reader envelope. It preserves operation errors,
partial-data markers, identity and unknown keys in a Desk draft. A result without
confirmed data cannot be imported. Conversion uses `--kind auto` by default, or
an explicit `classic`/`lf`/`type2` that must match the draft. `--plan` runs the same
eligibility checks without allocating a card ID; rejection exits nonzero and
publishes no successful plan/card. Converting creates a new ID and preserves
provenance; it does not rewrite its input or fill unknown bytes.

Type2 conversion also requires explicit emulator-only credentials:

```sh
chameleon read-draft promote --input type2-draft.json --kind type2 \
  --emulator-password 01020304 --emulator-pack ABCD \
  --confirm-emulator-credentials --output type2-card.json
```

These options assign credentials to the resulting emulator image. They do not
claim to recover source credentials and cannot be supplied to import or other
conversion kinds. Both preview and conversion require them. A complete supported
layout, identity consistency, unknown-page masks and counter/mirror restrictions
are checked by the same SDK conversion as the Web UI. The source draft keeps its
unknown credential rows and is retained in provenance.

Classic restrictions remain documented in [Classic conversion](classic-promotion.md).
LF supported mappings and diagnostic/edited-ioProx limits are documented in
[LF conversion](lf-promotion.md). No device access or write permission is required
for import/preview/conversion. The resulting standard card can use existing
file/slot workflows, each with its own checks.

## Append to a database

```sh
chameleon database import-read --input library.json \
  --source pac-read.json --name 'PAC read' --output library-with-draft.json
chameleon database promote-read --input library-with-draft.json \
  --id SOURCE_ID --output library-with-card.json
```

The commands use `GUIDatabase.AddCard`. Promotion retains the source draft and
appends the standard card under its fresh ID; it does not upsert by UID. Existing
Flutter prefixes, folders/preferences and unknown metadata are preserved. A
missing/ambiguous source reference is rejected. `database promote-read --plan`
outputs an eligibility plan instead of a modified database.

Offline commands omit `--output` to print JSON to stdout. Every file output is a
new, atomically published file, separate from all source files; existing files,
symlinks and aliases are not overwritten. Exported files/databases can contain
card keys and IDs and are not encrypted secret storage.

## Bounded Classic repair and key operations

```sh
chameleon reader classic-repair --port tcp:127.0.0.1:18888 \
  --input repair-request.json --output repair-result.json
chameleon reader classic-verify --port tcp:127.0.0.1:18888 \
  --input verification-request.json --output verification-result.json
chameleon reader classic-dictionary --port tcp:127.0.0.1:18888 \
  --input dictionary-request.json --output dictionary-result.json
chameleon reader classic-recover --port tcp:127.0.0.1:18888 \
  --input recovery-request.json --recovery-tools-dir ./tools/bin \
  --output recovery-result.json
```

Inputs are the SDK's `ClassicRepairRequest`, `ClassicKeyVerificationRequest`,
`ClassicDictionaryRequest` and `ClassicRecoveryRequest` JSON. Unknown fields,
invalid bounds/identity/keys, insufficient dictionary command timeout and missing
recovery helpers are rejected before opening the transport. Every operation
checks the selected model and advertised commands before execution. The helper
directory is an explicit CLI option and cannot come from the request JSON.

Repair without `blocks` returns the ordinary reader envelope, suitable for
`read-draft import`. Explicit selected blocks return sparse evidence. Sparse
repair, verification, dictionary and recovery use `{schema:1,result,error}`;
these are not complete card images. Selected repair can be previewed/imported
in the Web library's Classic repair panel. Key search/recovery results need
explicit verification and a subsequent read before becoming card data.

Verification runs each supplied candidate once under the existing SDK rules;
a rejected candidate can produce a completed result with `all_verified:false`.
An interrupted operation saves its returned partial result and exits nonzero.
The command never resubmits that operation. Existing output files are rejected
before opening a transport. Dictionary search requires 1–4096 explicit distinct
keys, a 1–30 second budget and enough per-command timeout for its selected
geometry; no built-in password/key list is substituted. Recovery uses explicit
strategy/run/attempt/time bounds. These actions do not write card memory.
