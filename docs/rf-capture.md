# Web RF capture

The Reader window's **RF hardware capture and offline analysis** panel connects
the existing SDK acquisition APIs to session-bound background tasks. Connect the
device through the configured BLE/USB Python bridge or ordinary TCP, refresh
firmware capabilities, choose an action and supply its parameters. Read the slot
impact notice, confirm it and check capabilities before each explicit capture.

## Actions and requirements

- **HF sniff** (`Client.SniffHF`, command 2020): 1–30000 ms in emulator mode.
  Uses the currently active HF slot; Desk does not select, initialize or save one.
  The firmware reloads its stored data before sniffing. The device responds as a
  card and an external reader can change emulator RAM through normal card
  commands. This is not a passive sniffer of an independent reader/card pair.
- **HF auth trace** (`Client.AuthTrace`, command 2017): 1–30000 ms tag wait, block
  0–255, Key A/B and an explicit six-byte known key. This is an active reader-side
  selection/authentication exchange. The pinned firmware synthesizes some
  anticollision frames; the returned trace is not entirely observed RF data.
  The UI does not guess keys or invoke recovery workers.
- **LF samples** (`Client.CaptureLF`, command 3031): 1–10000 ms timeout ceiling in
  reader mode. Enables the LF reader field. The returned `LFSamples` JSON records
  the SDK/firmware convention of 125000 Hz and requested duration. Neither is an
  independent measurement. The pinned buffer holds at most 4000 samples and can
  fill before the timeout; the request is not a continuous acquisition interval.
- **LF ADC** (`Client.ReadLFADC`, command 3009): existing fixed firmware capture,
  with no user duration. Enables the reader field and returns unsigned byte
  samples without rate or duration metadata. Both remain unknown in analysis.

All actions require Ultra, advertised mode commands 1001/1002 and the selected
acquisition command. Parameter checks use the shared feature catalog; the server
probes the actual device again. Lite, missing commands, unknown facts and native
USB maintenance connections remain unavailable, with reasons shown in the UI.

HF sniff additionally requires `serve --allow-writes`. Its registered task is
conservatively classified as a mutation because it reloads mutable emulator RAM;
this preserves the explicit-intent boundary of CLI `sniff --yes`. The other
actions retain their read classification and still require impact confirmation.
No action adds physical-card writes, slot persistence, pairing changes, device
restore or DFU through TCP. Direct SDK APIs and existing CLI flags are unchanged.

The command timeout must be at least duration +1s, or +2s for auth trace. The
default service timeout is 5s; longer capture requests require a larger
`serve --timeout` or a shorter duration. Invalid timeout combinations are rejected
before device I/O. Capture tasks reuse the common device gate and have a bounded
host budget of max(30s, duration +10s). Existing SDK cleanup uses its own bounded
command timeout after cancellation. Progress reports actual protocol requests
and responses; it is not a firmware sample counter or an elapsed-time estimate.

## Mode and slot effects

Save or export slot contents that must survive **before** starting capture. The
existing SDK temporarily enters emulator mode for HF sniff and reader mode for
auth trace / LF. It attempts to restore the original mode when it changed it.
In the pinned firmware, the mode transition itself stops/restarts sensing; it
does not call the slot-storage reload function. HF sniff explicitly reloads slot
data even when the original mode is already emulator.
Returning to the original mode does not recover unsaved RAM, prove unchanged
slot contents or undo commands received from an external reader.

The result records mode observations before/after the SDK workflow. A matching
readback sets `mode_restored`; a failed cleanup or unavailable readback does not.
The Web gate serializes these observations with other Desk device operations.
Direct SDK users of `CaptureRF` must serialize surrounding workflows themselves;
the individual mode-scoped acquisition APIs keep their existing atomic sections.
After an attempted Web capture, cached slot/configuration views are cleared;
read them again before editing. Desk does not automatically save RAM, restore a
backup, change the active slot or replay an uncertain operation.

## Results and offline handoff

An `rf_capture` result records action, original session, requested wait, mode
readback, notes and an optional file. `captured` means data was returned, not that
all RF frames or the whole requested interval were captured. Firmware buffers can
omit frames without an overflow flag. `no_capture` is an empty observation, not a
reason to retry. Other firmware/protocol errors remain failures. A malformed HF
payload rejected by the existing SDK is not converted into invented frames.

HF files contain the SDK's `TraceFrame` array (`hf-json`); LF samples contain
`LFSamples` (`lf-json`); ADC files are `lf-u8`. A capture record can be exported as
JSON with its mode/session metadata. **Send to offline analysis** copies its file
into the existing workbench without acquiring again or automatically analyzing.
There, export the exact transferred file or explicitly choose options and run
analysis. The transfer resets old range/rate/short-frame overrides.

HF transfer defaults to `data` layout to preserve supplied bits/bytes. This does
not assert that parity was removed. In the pinned sniff implementation the RX
hook runs before software parity removal; RX/TX interpretation can differ.
The offline workbench supports one explicitly selected layout per analysis and
cannot normalize an arbitrary mixed-layout capture. Inspect raw frames and keep
CRC/parity/authentication labels as candidates. No timing fields, UID, card
identity, complete exchange or recovery inputs are inferred by the handoff.
ADC rate stays unknown unless the operator independently assigns a rate.
See [offline RF/trace formats and limits](offline-rf-trace.md).

## Sessions, cancellation and refresh

Operations are `rf.hf-sniff` (`POST /api/rf/hf-sniff`), `rf.hf-auth`
(`POST /api/rf/hf-auth`) and `rf.lf-capture` (`POST /api/rf/lf-capture`). They use
the existing token, session IDs, receipts, task client and task journal. Queued
work from an old session is rejected. Late responses cannot replace the current
device view. There is no automatic acquisition retry or reconnect/replay.

Cancellation cannot immediately stop the firmware's blocking acquisition. A
timeout or cancellation after transmission closes the protocol connection so a
late reply cannot contaminate a new session. Mode cleanup may consequently fail;
reconnect and check mode/slot contents before proceeding. An interrupted sniff
task may have changed emulator RAM even if no result was returned. Preserve its
unknown mutation outcome rather than treating cancellation as rollback.

The task center can query/cancel work. After a page refresh, query the capture
task ID to retrieve retained data and explicitly hand it to analysis. Historical
results name their original session and do not establish current device state.
Bytes returned before a mode-cleanup failure remain exportable/analyzable, with
the failure retained. Lost/expired bodies cannot be reconstructed by a query.
The journal persists metadata and deduplication receipts, not keys, trace files
or result bodies; service restart never resumes capture. Keep exported records.

Firmware behavior is based on the pinned revision
[`6009a5f`](https://github.com/RfidResearchGroup/ChameleonUltra/blob/6009a5fb8adbb92d3efb548eaac9e501bd179801/firmware/application/src/app_cmd.c)
and its `rfid_main.c` / `tag_emulation.c` mode/sense paths. Other firmware builds
can differ even when they advertise the same command number.

Tests use scripted device transports and a TCP simulator in Chromium. They cover
mode restoration/failure, no observations, explicit handoff, capabilities, write
permission, stale sessions, cancellation and receipts. They do not qualify real
antenna behavior, capture completeness, timing accuracy or BLE radio performance.
