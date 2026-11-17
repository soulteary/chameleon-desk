# R1 / R2 delivery

This change extends main after PR28. Existing current-slot snapshots and explicit
conversion remain intact. The additional scope is grouped as follows:

- R1: typed reader/key/repair/recovery forms, saved-source selection and explicit
  byte-conflict resolution; offline CLI merge and selected-repair application.
- R1: folder and dictionary management, atomic record moves, folder bundles,
  read-only external-file draft import and cross-file comparison.
- R2: exact NTAG210/212 layouts and optional Type2 signature observation.
- R2: current-emulator configuration, compare-before-change/readback, bounded
  detection logs, counters, RF configuration and explicit mode controls.
- R2: controlled physical-write forms, guarded existing Classic value blocks,
  read-only Magic inspection and bounded ordinary Type2 multi-page writes.

The Web forms use the shared feature/operation registry, serialized sessions,
background tasks and existing library persistence. Advanced JSON remains
available for inspection. Changing input or session invalidates reviewed plans.
No write command is automatically replayed after failure or interruption.

Usage and limits are documented in [reader workflows](reader-workflows.md),
[Type2 workflows](type2-workflows.md), [library organization](library-organization.md)
and [emulator controls](emulator-tools.md). The generated [feature catalog](features.md)
records Web, CLI and SDK entry points, firmware requirements and transport limits.

## Deliberate boundaries

NTAG210/212 are the only new physical Type2 models. Multi-page writes accept
1–32 sorted ordinary user-page entries, validate every original page before the
first write, and stop at the first failure. Manufacturer/block 0, OTP, lock,
configuration and credential writes are excluded from these new workflows.
Existing explicitly confirmed Classic trailer operations retain their previous
separate scope; this change does not broaden them.

Classic value operations set an already valid ordinary value block after checking
its original bytes. They do not implement an atomic card-side debit/credit
transaction or initialize arbitrary blocks. Magic inspection performs reads;
Gen3 normal-block readability does not positively identify a Magic chip.
Signatures are captured bytes, not a cryptographic authenticity verdict.

External-file drafts remain declarations with masks and source text. They are
not physical-read evidence and cannot be promoted or written. Slot-derived saved
cards remain ineligible for physical-card writing. Pairing, whole-device restore
and DFU keep their native USB maintenance requirements.

## Validation boundary

Automated protocol tests cover model/layout checks, source preservation,
capability routes, configuration compare/readback, mutation boundaries and
partial/unknown write outcomes. Web tests cover forms, stale input/session
responses, persistence and disabled states. Browser tests use a local protocol
simulator for import/reload and interrupted multi-page writes.

The newly added NTAG210/212, value-block, Magic and emulator-control paths have
not been physically accepted in this change. Prior hardware results in
[hardware acceptance](hardware-acceptance.md) apply only to their recorded scope.
Real-card checks must record firmware, card model, known keys, initial backup,
planned ranges, readback and any restoration result separately.

## Manual acceptance procedure

Use isolated dedicated test cards and export a backup before writing. Record the
core/bridge/firmware versions and transport for each run.

1. For each NTAG210/212, read the full layout with signature capture. Confirm the
   exact version, page count, hidden password/PACK masks and absence of RF counter
   claims. Import, explicitly assign emulator credentials, convert, export and
   reload. Inspect retained signature and source metadata.
2. For a Type2 test card, choose two or three unlocked ordinary pages and record
   their original bytes. Review a batch plan, execute once and independently read
   all selected pages. Restore through a separate reviewed plan using the newly
   observed bytes as `expected`. Do not include lock, OTP, configuration or secret
   pages. Check a mirror-enabled tag only with read/plan operations; planning must
   refuse virtual memory.
3. For Classic, select an existing valid ordinary value block with known keys.
   Back it up, review a small SET change, read back, then separately plan and
   verify restoration. Do not use block 0, trailers or a card used for access or
   payment. This checks byte encoding, not transaction atomicity.
4. For each available Magic family, run inspection against an explicitly captured
   identity. Check that no memory/configuration/lock write is issued. A negative
   or ambiguous response is not positive chip identification.
5. In emulator mode, record the active HF slot and configuration. Read logs in
   bounded batches, change one reversible supported setting, verify readback,
   restore it, and separately test requested flash persistence. Confirm an active
   slot/configuration change rejects an old preview.

Interrupted physical writes require independent reads before further action.
Simulator tests exercise uncertain outcomes; disconnecting a real card/device is
not required to validate every failure path.
