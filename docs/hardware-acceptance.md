# Reader workflow hardware acceptance

Hardware qualification is specific to a model, firmware, transport and test tag.
Simulator and browser tests cover control flow and failure handling, but do not
qualify every physical card family or all three host platforms.

## Recorded environment

The current acceptance run uses macOS, a Chameleon Ultra hw_v1, firmware
`v2.2.0-64-g5c99d4a` (reported application version 2.2), and the Python pySerial
USB-to-TCP bridge. The Go binary was built with `CGO_ENABLED=0`. Real card dumps,
UIDs, credentials and detailed logs remain in ignored local `reports/` files;
they are not repository fixtures.

T5577 with an EM410x identity passed the following sequence: explicit isolated
target acknowledgement; signed plan; one host write command; selected-protocol
ID readback; independent read; library import; and standard-card conversion.
The observed reader subtype 103 differs from the generic source tag 100; ordinary
EM410x 100–103 are equivalent for this identity-only verification. Electra remains
a distinct 13-byte identity. This run does not verify a T5577 password, arbitrary
memory/configuration, reader interoperability or durability.

An earlier readback check incorrectly required the exact ordinary EM410x subtype.
The failed task retained `write_outcome_unknown`; the write was not replayed.
Independent reading confirmed the target before a separately planned test. The
regression test now accepts ordinary subtype differences for the same protocol
and ID.

NTAG213 passed modeled full reads, one explicit single-password authentication,
masked library import, explicit emulator-only credential conversion, and page 6
write/readback followed by a separately planned restoration. All 180 readable
bytes matched the pre-write backup afterward. Original PWD/PACK remained hidden
and were not changed. Initial diagnostics found 16-page FAST_READ responses fail
on this firmware while 1,4 and 15-page responses succeed. The workflow now uses
at most 15 pages per command; there is no failed-read fallback or write retry.

Managed BLE scan, selection, connection and device information passed using the
separate Python/Bleak bridge and the cgo-disabled core. Its transport declaration
retained pairing/reset/DFU restrictions. A user-supplied IC F08, observed as
Classic 1K geometry, passed full 64-block reading with one explicitly supplied
key, library import/conversion, candidate verification, selected-block reread
and evidence import, and a bounded single-key dictionary check. A reviewed
data-only write verified 47 blocks, skipping block 0 and every trailer; a separate
restoration verified 47 blocks again. Final full readable memory and captured
identity matched the backup. The original emulator mode was restored. BLE
disconnect rotated the session, and an old write confirmation was rejected
before device access. An inconsistent BLE cleanup status was detected during
this run. Shielded session teardown now preserves one completed disconnect;
regression tests retain blocked state for genuine failure. Real control
disconnect and overlapping TCP EOF/control disconnect both passed after the fix
with an empty error and inactive session. An initial RF collision was resolved by isolating one tag;
no write was attempted before a complete target backup.

These runs cover NTAG213 and EM410x-on-T5577 through USB, and F08 through BLE on
one macOS host and Ultra firmware build. They do not qualify Classic 4K/Mini,
other Type2 models, other LF protocols, Classic trailer writes, RF recovery
attacks, physical emulator-slot loading, Linux/Windows hardware, or Lite. Those
paths retain simulator/build coverage only. TK4100 was not qualified: the card
was replaced before a successful capture. No real tag dumps or credentials are
committed.

## Repeatable procedure

1. Build the core with `CGO_ENABLED=0`; install the matched bridge dependencies.
   Start the USB bridge on an explicit application serial port, then start
   `serve --port tcp:127.0.0.1:PORT --allow-writes`. Use a dedicated test device
   and cards whose permitted write range has been established.
2. Record device model, firmware, advertised commands, actual transport and
   original mode. Scan one target at a time. Export every readable source and
   target before writes. Type2 PWD/PACK readback zeroes are hidden values, not a
   credential backup. LF identity is not a full T5577 backup.
3. For Type2, select the modeled full-read operation with the observed UID.
   Verify model, requested/read counts and hidden-page masks. If an explicitly
   known password is available, test a separate authenticated read once; do not
   try default lists or brute force. Import the result, confirm that incomplete
   data cannot convert, then assign new emulator-only PWD/PACK explicitly for an
   otherwise complete modeled conversion. Do not write these values to the card.
4. Choose an unlocked ordinary Type2 user page, away from manufacturer, lock,
   configuration and credential pages. Record its original bytes. Generate a
   one-page plan with UID, page, expected original bytes and a distinct test
   marker. Inspect and execute once. Verify the result and independently read.
   To restore, generate a separate explicit plan expecting the marker and
   writing the saved original bytes; verify again. After an uncertain result,
   inspect first and stop if identity or state cannot be established.
5. For Classic, capture full identity and a readable target backup with explicit
   per-sector keys. Verify supplied candidates, run a bounded dictionary or
   configured recovery helper, and explicitly reread recovered sectors. Test
   selected-block import and conflict resolutions. Physical writes must skip
   block 0, keep trailers disabled unless separately qualified, use a reviewed
   plan and independently read back. Do not treat an all-zero placeholder key as
   known evidence or retry an ambiguous WRITE with another key.
6. For LF, place only the dedicated T55xx target in the field. Choose one
   supported identity, supply explicit old/new passwords and acknowledge the
   isolation limitation. Plan, execute once and verify the selected protocol ID.
   Import and convert the independently observed result. Record unverified
   password/full-memory scope explicitly.
7. Refresh the page while a task runs and query its existing ID. Confirm that
   task-result import does not rerun the command. Disconnect/reconnect and verify
   that old responses cannot replace current data or authorize a previous plan.
   A server restart retains task metadata/receipts, marks running writes unknown,
   and must not replay a write. Result bodies are not retained.

## Remaining matrix

Further physical qualification remains for other transport/card combinations,
Linux/Windows hosts, other firmware versions, Lite, other modeled Type2 chips,
Classic geometries and LF protocols.
Originality signatures are not captured by this Type2 workflow. Recovery-helper
availability and actual cryptanalytic success depend on the installed helper
bundle and card behavior. A simulator pass is not recorded as hardware success.
