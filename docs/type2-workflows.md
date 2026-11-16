# Modeled Type2 reader workflows

The modeled reader path uses GET_VERSION to identify NTAG213, NTAG215,
NTAG216 and MIFARE Ultralight EV1 640/1312 layouts. It checks the expected
seven-byte UID, SAK, ATQA and ATS before and after each confirmed read chunk,
and checks GET_VERSION again before completing the operation. Unknown version
responses are rejected. SAK 00 alone does not establish a model or capacity.
The older `ReadType2Pages`, `ReadType2PagesAuthenticated` and range-read APIs
remain available with their existing behavior; their legacy results cannot be
promoted through this modeled path.

## Read and import

`POST /api/reader/type2-workflow` accepts either:

```json
{"uid":"045D4EA2A42091","full":true}
```

or an explicit range:

```json
{"uid":"045D4EA2A42091","start":4,"count":8,"password":"01020304"}
```

The optional password must contain exactly four bytes. The server retains no
password or returned PACK in the result. FAST_READ requests are bounded to the
identified memory and use at most 15 pages per chunk (60 data bytes plus RF CRC fit the 64-byte reader
receive FIFO). Authentication is repeated
when a fresh selection is needed; a failed authentication stops the operation
immediately. There is no automatic retry with another password or replay of a
failed command. A failed password can consume the tag's configured AUTHLIM
budget; use only a password known to belong to the selected card.

A result distinguishes confirmed prefix bytes from pages not requested or not
confirmed (`not_requested`, `read_failed_or_protected`). The latter reason does
not claim that protection caused the failure. Password and PACK pages remain
`hidden_credentials` even if a tag returns zeroes. Their raw bytes are redacted,
and imported draft rows show `????????`. Counters required by the observed
configuration are read separately. Interruption, identity change, counter
failure and RF-field/mode cleanup failure leave the operation incomplete.
Confirmed earlier chunks remain available for draft import.

## Conversion to a standard emulator card

Conversion requires a complete full-layout result, captured identity, valid UID
and BCC memory, all non-secret bytes known, required counter observations, and
no operation/cleanup error. Active NTAG ASCII mirrors are rejected because the
returned virtual bytes do not establish the underlying physical memory.

The original password and PACK cannot be recovered by reading their pages.
Conversion therefore requires explicit assignment of **new emulator-only**
credentials:

```json
{
  "emulator_password":"01020304",
  "emulator_pack":"0506",
  "confirm_emulator_credentials":true
}
```

These are chosen emulator values, not observations of source secrets. The two
RFUI bytes of the emulator PACK page are assigned zero. For an NTAG configuration
with its counter disabled, inactive emulator counters are also assigned zero.
Those assignments are recorded in `desk_read_origin`, together with the original
result and unknown-page masks. The draft is preserved; conversion creates a new
card ID. Other known page edits and unrelated metadata are retained. A standard
card can use existing binary export and slot-loading paths, but this conversion
does not claim to reproduce unknown source credentials or qualify firmware
emulation behavior on every reader. This workflow does not collect the READ_SIG
originality signature and does not establish source authenticity or restore that
signature. Existing emulator defaults apply when no signature extra is supplied.

## Ordinary user-page writes

The SDK provides `PlanType2UserPage`, `WriteType2UserPage` and
`WriteType2UserPageObserved`. A request specifies the expected UID, one page,
its four current `expected` bytes and four replacement `data` bytes. Optional
authentication follows the same no-retry rule as reads.

Only ordinary pages are eligible: 4–39 for NTAG213, 4–129 for NTAG215, 4–225
for NTAG216, 4–15 for EV1 640 and 4–35 for EV1 1312. Manufacturer, static and
dynamic lock, OTP, capability-container, configuration and credential pages are
excluded. The helper also rejects active NTAG ASCII mirrors. Locked or protected
user pages may still reject a write; the helper does not change lock or
protection settings.

Web execution requires reviewing `POST /api/reader/type2-write-plan` with
`{"request":{...}}`, then submitting the identical request to
`POST /api/reader/type2-write` with its returned `confirmation` and `confirm:true`.
The confirmation is bound to the current Desk session and exact input, and
expires after five minutes. Planning requires no write permission. Execution
requires server writes to be enabled and repeats live model, identity, mirror
and expected-byte checks. The password is included only in the signed input
digest, not the readable confirmation payload.

At most one physical WRITE is issued. Equal before/after bytes produce a verified
no-op. Otherwise, a successful result requires acknowledgement, matching
readback, unchanged identity/model and successful cleanup. `write_attempted`,
`acknowledged` and `readback_matches` retain separate observations on failure.
After interruption or a lost response, inspect the page before deciding on a
new write. Desk does not retry or roll back automatically.

## Scope and validation

Automated transport fixtures cover exact model recognition, authentication
selection state, secret redaction, masks, counter requirements, identity changes,
partial reads, conversion provenance, page exclusions, compare-before-write,
acknowledgement/readback failures, cancellation and cleanup failures. These tests
are not hardware qualification of every listed card or firmware version.

Layout, command and hidden-credential behavior are based on the NXP
[NTAG213/215/216 data sheet](https://www.nxp.com/docs/en/data-sheet/NTAG213_215_216.pdf)
and [MIFARE Ultralight EV1 data sheet](https://www.nxp.com/docs/en/data-sheet/MF0ULX1.pdf).
Legacy Ultralight, Ultralight C, NTAG I2C and unrecognized revisions require
separate model support; failed reads are not used to guess their capacity.
