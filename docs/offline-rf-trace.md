# Offline RF and trace workbench

The Reader window's **RF / trace offline file analysis** panel accepts files only.
It does not acquire samples, transmit, change device mode, authenticate a card,
run recovery workers or import observations into the card library. Existing
CLI/SDK acquisition APIs retain their separate behavior and restrictions. The
separate [Web RF capture panel](rf-capture.md) can explicitly transfer a returned
file here; selecting or analyzing a file never starts acquisition.

Choose a format, select a local file, set the range/options and run analysis.
Changing source or parameters invalidates export eligibility for the previous
analysis; late results cannot replace a newer source. The selected original file
can still be exported after a parsing failure. No file extension is used to guess
encoding or protocol.

## Accepted formats

All source files are limited to **1 MiB**. Text JSON must be UTF-8 with one value,
exact supported field names, no duplicate properties or trailing values. The
service accepts file bytes, never server filesystem paths or executable names.

### LF samples

- `lf-u8`: each byte is one unsigned 8-bit ADC sample. No header, signed/16-bit,
  packed-bit, IQ or WAV interpretation is performed.
- `lf-json`: the existing Desk `LFSamples` object, with hexadecimal `data` and
  optional `rate_hz` and `requested_ms`. For example:

```json
{"rate_hz":125000,"data":"00000000C8C8C8C8C8C8C8C800000000","requested_ms":1}
```

At most **262144 samples** are accepted. Missing/zero rate stays unknown. A rate
entered for a source without one is recorded as **operator assigned**; a declared
nonzero JSON rate cannot be silently overridden. A requested capture duration is
only file metadata, not proof of actual duration or continuous acquisition.

The panel displays min/max/mean, threshold, transitions, a min/max waveform
envelope of at most 1024 buckets, and a paginated sample table. The envelope
retains each bucket's extremes, not every sample. Values have no calibrated
voltage scale. Table time is calculated from absolute sample index and the
specified rate; it is not an independently observed timestamp.

Select a zero-based start and count (`0` count means the remaining samples).
Modulation/RF-divider heuristics and optional Manchester decoding require an
explicitly declared/assigned **125000 Hz** rate. Other rates still allow sample
statistics and the index-based envelope. Manchester uses the existing SDK helper:
an even clock/divider 2–1024, half-bit phase 0/1 and optional inversion. It restarts
at the selected range. Invalid pairs are omitted from the candidate bit string;
nonzero timing/pair errors mean it is not a lossless or position-preserving decode.
Neither its bits nor a modulation hint establish a card identity or protocol.

CSV export is limited to **65536 selected samples** and requires a rate. CSV
indices and calculated time restart at the selection's zero; the report retains
its absolute start. SVG likewise describes the selection. These exports reuse
`LFSamplesCSV` and `LFSamplesSVG`. A complete analysis report can be exported as
JSON; it contains selected data, source hash and parameters, not the original file.

### Desk HF frame files

- `hf-json`: the existing `TraceFrame` array, with explicit `from_card`, `bits`
  and hexadecimal `data` on every row. Missing direction is not defaulted.
- `hf-packed`: the payload format consumed by `ParseTrace`: a two-byte big-endian
  header, bit 15 for card-to-reader, bits 0–14 for bit count, followed by
  `ceil(bits / 8)` bytes. For example, `00 07 26 80 10 04 00` contains a reader
  short frame and a card frame. This is not a full Desk transport packet.

```json
[{"from_card":false,"bits":7,"data":"26"},
 {"from_card":true,"bits":16,"data":"0400"}]
```

Choose **data** or **parity9** layout explicitly. Data layout preserves the supplied
bit count/bytes. Parity9 interprets complete groups of eight least-significant-bit
first data bits plus one parity bit; short frames below eight bits are retained.
Longer nonmultiples of nine are refused rather than guessed. Raw packed bytes,
normalized data and extracted parity are retained separately in the report.
Unused padding bits are not silently used as another byte. Zero-bit frames and
payload-length disagreement are invalid.

### PM3 / upstream GUI trace records

`pm3-trace` supports the bounded record layout used by the pinned upstream GUI:
little-endian timestamp `uint32`, duration `uint16`, length/direction `uint16`,
then data and `ceil(data_length/8)` parity bytes (most-significant parity bit
first). Bit 15 of length/direction means card-to-reader. Supported records have
1–4095 payload bytes. PCAP/PCAPNG, textual traces, other PM3 variants and protocol
containers are not accepted by this selector.

The GUI's single-byte parity-field values 1–7 can encode a short-frame extension.
This interpretation is **off by default** and requires explicit confirmation of
that source convention after selecting a file. It resets on source/format change.
Without it, the data-byte length is retained; exact RF bit length is unknown.

Timestamp/duration fields are preserved as **unverified file ticks**. They are
not converted to measured microseconds. The upstream GUI can synthesize these
fields when exporting captures; neither their presence nor monotonicity establishes
physical timing. Desk never generates replacement timestamps for frame files.
The layout reference is the pinned GUI's
[`hf_sniff.dart`](https://github.com/GameTec-live/ChameleonUltraGUI/blob/cc4d03c53ee74c387dbf2427d33b9101d346c2b0/chameleonultragui/lib/helpers/hf_sniff.dart).

## HF analysis and partial input

At most **4096 confirmed frames** are retained. A truncated packed header/payload,
invalid JSON frame or frame-limit boundary produces an explicitly incomplete
report with the confirmed prefix and a byte/frame failure position. No missing
payload, parity or record is padded. Invalid JSON syntax or ambiguous properties
are rejected as a whole. Empty inputs are refused. Range selection must stay
inside confirmed data. Parsing completeness describes the supplied file bytes,
not a complete RF exchange or uninterrupted hardware capture.

The frame table supports direction/payload filtering and 50-row pages. Original
frame indices, declared directions, bit interpretation, payload, available parity
and file ticks remain inspectable. Odd-parity comparison and CRC-A suffix matching
are separate candidate checks. A mismatch can mean omitted CRC, encryption,
another protocol or damaged data; it is not by itself a protocol-invalid verdict.

A few short-frame/authentication request shapes are annotated as candidates.
Adjacent direction/length-compatible authentication fragments are linked only
within the selected range. UID, key, same-card identity, plaintext/encrypted nonce
and nested-authentication state are not verified. The panel does not decrypt,
generate mfkey commands, construct recovery bundles or call helpers. Raw source
and an exported report should be retained before any later independent analysis.

## Tasks, refresh and retention

`files.rf-analyze` / `POST /api/files/rf/analyze` is a registered **offline** task.
It requires the Desk token and normal request receipts, but no device session,
connection, firmware capability or `--allow-writes`. Device operations keep their
existing session requirements and write boundaries. Scope comes from the server
registry, not request input. Device reconnects cannot replace or invalidate an
in-flight file result. Offline computation uses a separate two-job gate so it
does not claim the hardware operation gate. Each handler has a 30-second bound;
input parsing, retained prefixes and exports have the size limits above.

The task center provides progress/state, cancellation and result queries. The
workbench can query a completed task ID after refresh without resubmitting its
file. Original bytes remain only in the selecting page's memory unless exported.
There is no browser autosave of traces. The private task journal retains metadata
and deduplication receipts, not source files or result bodies. Restart marks
started offline phases interrupted and never replays them. Preserve the original
file and exported report; a lost/expired result does not trigger automatic reruns.

Offline result bodies share a **16 MiB in-memory retention budget**, independently
of hardware result bodies. Older terminal offline bodies may be evicted while
metadata/receipts remain queryable. Individual reports are limited to 4 MiB;
select a smaller range if a report would exceed it. There is no durable attachment
store or unbounded server-side file collection in this phase.

Programmatic entry points are `RFFileRequest.Validate` and `AnalyzeRFFile`, or the
existing Go `TaskClient` / `chameleon tasks`. A task body contains the original
file encoded in hexadecimal `data`, its explicit format/name and analysis options.
For example, save this as `request.json`:

```json
{"format":"lf-u8","name":"example.bin","data":"0000C8C80000C8C8",
 "rate_hz":125000,"start":0,"count":0,"decode":false,"clock":64,"phase":0}
```

```sh
chameleon tasks submit --token-file desk.task.token \
  --operation files.rf-analyze --request-id offline-example-attempt-001 \
  --body-file request.json
chameleon tasks wait --token-file desk.task.token --id TASK_ID
```

Omit the device session for offline operations. Existing `data csv|plot|modulation|
manrawdecode` CLI commands retain their own file formats and bounds. The new
analysis report is not a replacement for a raw `LFSamples`/`TraceFrame` file.

Tests use synthetic files and protocol fixtures. They do not qualify hardware
capture fidelity, RF timing precision, every PM3 variant or recovery success.
