# Classic read completion, recovery and physical writes

Classic RF operations use the connected Desk session, background task receipts
and firmware capability checks. They require a reader-capable Chameleon Ultra
and explicit target credentials. Draft comparison, merge and conversion remain
offline. BLE and USB bridges retain the existing transport
restrictions. There is no native fallback or automatic write replay.

## Reread and compare

A repair request contains the source read-draft card and an explicit `read`
object: the expected UID and an ordered sector key map for the same geometry.
Omit `blocks` to run a new full read. Supply distinct block numbers in `blocks`
to read only those blocks. Full reads observe UID, ATQA, SAK and ATS before each
sector and after the final block; selected reads check the same identity before
each selected block and after the last block. An identity change stops the
operation. Matching these values does not prove that two observations came from
the same physical card, or that data did not change between reads.

A full reread returns a normal `ReadResult`. A selected reread returns
`ClassicSelectedRepairResult`, including requested block numbers, successful
block observations, unknown trailer-key masks, completion and any error.
Selected results remain sparse evidence. Unread blocks are not zero-filled or
presented as successful acquisitions. A failed operation retains only confirmed
block observations and its error, including a reader-mode cleanup failure.

The UI can preview and apply selected observations or merge two read drafts.
Applying either operation creates a new card. The input cards, original read
results, requested ranges and errors remain in the saved evidence tree.
Conflicting known bytes require individual `left` or `right` choices; there is
no default overwrite. Explicit same-card acknowledgement is required in addition
to matching full identity and geometry. Merges are limited to 16 observations
and eight levels of evidence to bound import and validation work.

Unknown bytes can be filled only by successful read/authentication evidence.
Editing a row cannot turn an unknown key into a verified key. Key bytes in merge
sources and overlays are immutable; ordinary known data bytes remain editable.
The evidence is an archive of supplied observations, not a cryptographically
attested hardware log.

Conversion to a standard Classic card still requires complete data, complete
identity, no unknown keys and a successful **full-read** source. Even complete
byte coverage assembled from several failed or selected reads is archival until
a successful full reread exists. The original failed operation is never relabeled
as successful. Existing Classic identity/manufacturer-block consistency checks
also apply during conversion.

## Verify candidates and search dictionaries

Candidate verification accepts explicit sector/key-type/six-byte-key tuples.
Authentication or an ACL-readable Key B proves a candidate. Results contain
coordinates and verification status, without echoing submitted keys. A failed
candidate is reported as unverified. Transport failures and identity changes stop
the operation and retain partial verification status.

The bounded dictionary workflow reuses `CheckClassicDictionary`. It accepts the
captured identity, a Classic geometry (5, 16, 32 or 40 sectors), 1–4096 distinct
six-byte keys and an explicit 1–30 second budget. It checks full identity before
and after the existing dictionary operation. Returned keys have passed the
existing authentication/readable-Key-B checks. A partial key map is not permission
to fill unknown draft bytes: explicitly reread the affected blocks or the card
with the resulting keys. No dictionary or recovery task modifies the card
library automatically.

The existing firmware dictionary command requires a per-command timeout of at
least `1s + (2 × sectors + 1) × 100ms`. Desk reports an insufficient timeout before
RF access. The overall Web task budget remains in effect; a large search may
finish with a partial result or timeout.

## Bounded key recovery

Start the server with `--recovery-tools-dir PATH` pointing to trusted compiled
recovery helpers. The browser cannot select executable paths. The selected
strategy requires these helper files:

- `nested`: `nested` and `staticnested`;
- `hardnested`: `hardnested`;
- `darkside`: `darkside`.

Windows uses the `.exe` suffix. Missing, non-regular or non-executable helpers are
reported before RF access. Desk uses the existing recovery workers and does not
implement another cipher or silently switch strategies.

Each request explicitly chooses a strategy, the target block/key type, captured
identity, a 1–30 second budget, 1–64 runs and 1–3 attempts. Nested and hardnested
also require a known block/key type/key. Cancellation interrupts device work and
helper computation through the task context. The final candidate is verified
against the full expected UID and checked identity. Failed or interrupted
verification does not produce a reusable recovered key. Inspect the task result
and start a new explicit request after interruption; no result or write command
is replayed automatically.

## Physical Classic write plan

Physical writes are separate from read-draft editing and conversion. They require
`--allow-writes`, an explicit source schema-2 dump, an explicit target UID/key map
and an exported complete target backup with captured identity. Export source and
backup files before execution; the server task record is not a durable backup.

Plan first, inspect the exact block list and retain the returned confirmation.
Block 0 is always excluded. Trailer writes require explicit `write_trailers`, all
source/backup keys known and valid access-bit redundancy. The default plan writes
only data blocks. The public source/backup checksums redact Key A and Key B in every trailer, including readable Key B. Full-dump checksums are not included in the public plan. The public plan digest covers only that redacted summary;
Web confirmation additionally uses a server-private HMAC over the full request,
including credentials. It expires after five minutes and is invalidated by a
changed session, request or server restart.

Execution requires `confirm: true`, the current plan digest and the Web
confirmation. Desk rereads the complete target backup and compares identity,
data checksum and unknown-key metadata before entering the write phase.
`CloneClassic` then checks full identity immediately before each write, preserves
access bytes, skips block 0 and reads back every written block. Trailer writes
also verify the resulting keys. Cancellation, an identity change or a failed
readback stops the operation.

A failure before the first write command reports `not_started`. Once a write
command may have reached the device, cancellation, timeout or a lost response
reports `write_outcome_unknown`. A successful result reports `verified` only
after readback and reader cleanup. Candidates in the explicit key map are authenticated before each write. Once a write command is sent, no write-status response triggers a second write with another key. Uncertain writes are not retried. There is no atomic multi-block write or rollback. Preserve the
backup, inspect the target and make a new explicit recovery decision.

## SDK and HTTP entry points

SDK entry points are `ReadClassicRepair`, `ReadClassicSelectedRepair`,
`ClassicMergePreview`, `ValidateClassicMerge`, `MergeClassicDraft`,
`ClassicSelectedRepairPreview`, `ApplyClassicSelectedRepair`,
`VerifyClassicKeys`, `CheckClassicDictionaryBound`, `RecoverClassicBound`,
`PlanClassicPhysicalWrite` and `WriteClassicPlanned`.

Web read/recovery tasks use `/api/reader/classic-repair`,
`/api/reader/classic-verify`, `/api/reader/classic-dictionary` and
`/api/reader/classic-recover`. Planning uses
`/api/reader/classic-write-plan`; execution uses
`/api/reader/classic-write`. The execution body is
`{"request": { ...reviewed request... }, "confirmation": "..."}`.
The normal CSRF token, session identity, capability preflight, cancellation and
receipt-query rules apply. Refreshing the UI queries an existing task; it does
not resume its RF operation. Server restarts do not resume in-flight tasks.

Tests exercise identity changes, partial and conflicting reads, unknown-key
masks, evidence persistence, bounded recovery inputs, unavailable helpers,
HMAC/session invalidation, backup changes, block protection, readback and lost
write responses with protocol simulators. These tests do not establish physical
Classic-card compatibility or qualify trailer writes on a particular card.
