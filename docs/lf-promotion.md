# Converting LF read drafts

The SDK can convert a completed LF read draft into a standard saved card with a
fresh ID. The caller adds the copy to the existing library and retains the source.
Conversion is offline. It does not connect a device or write a physical tag.

Supported mappings are EM410x subtypes 100–104, HIDProx (200), ioProx (201), Viking
(170), PAC (150), and Jablotron (180). The original EM410x subtype is required and
preserved; Electra subtype 104 carries a 13-byte identity, other EM410x subtypes
carry five bytes. The other supported readers normally omit a tag type; conversion
uses the existing protocol mapping and rejects a conflicting supplied type.

Pyramid and EM4x05 results remain diagnostic drafts. An LF read response is not
necessarily a complete emulatable identity. IDTECK has an existing saved-card
mapping and offline codec, but no reader entry, so it is not a read-draft
conversion in this workflow.

`GUICard.LFPromotionError` and `GUICard.PromoteLFDraft` share validation:

- a completed schema-1 LF result, `present:true`, one returned/requested unit,
  and no operation or mode-cleanup error;
- a supported protocol and matching tag type;
- one complete hex row with the exact protocol width;
- valid source and edited bytes under the existing LF codec;
- no collision with the reserved `desk_read_origin` metadata field.

The converted card uses the current saved row as its UID/encoded LF ID. It keeps
the name, folder, color, extras and unknown metadata, and leaves HF identity and
memory fields empty. The new record contains `desk_read_origin` with the source
ID and a conversion snapshot. Original decoded fields remain observations of the
source, not claims about later edits. Standard slot loading remains subject to
its existing model, command, transport, permission and readback checks.

ioProx's host decoder preserves a 16-byte firmware response but does not validate
the relationship between its structured and raw fields. Consequently an unchanged
captured response can convert, while raw edits require explicit firmware
validation and are rejected by offline promotion. This does not infer a valid
encoding from byte count alone.

Ordinary standard LF cards can use `GUICard.EditLFData` to change a name and one
encoded UID row while preserving tag type and unrelated metadata. Host-supported
identities pass their existing offline encoder/decoder. ioProx name-only edits
are supported; changed ioProx bytes require firmware validation. These edits do
not issue RF writes. Unknown or diagnostic tag types have no ordinary LF editor.

The CLI workflows are described in [reader CLI workflows](reader-cli.md).
Tests cover all supported mappings, Electra width, edited PAC data, source and
metadata retention, absent/missing/diagnostic results and ioProx restrictions.
No physical-tag qualification is implied by these offline tests.

The Web library also exposes LF protocol-field JSON editing for standard cards.
`protocol` and `tag_type` must match the selected card. Host-supported fields
use the offline codec; ioProx requires explicit `ioprox` fields and a connected
firmware codec. The action replaces only the saved card's encoded identity/name,
preserving its ID/type and other metadata. Save or discard existing row edits
first. A changed library, form, selection or editor revision rejects an in-flight
result; device activity disables the ioProx action. No RF write or slot load is
issued by this editor.

Structured saved-card edits are also available through
`GUICard.EditLFIdentityOffline` for host-supported codecs and
`Client.EditLFIdentity` for firmware-backed ioProx. These preserve the saved
protocol and tag type, including EM410x reader subtypes 100–103. ioProx ID-only
edits decode the raw eight-byte portion through firmware and compare the full
encoded response; a sixteen-byte length alone is not validation.
`LFIdentityInput.EditRequirements` selects command 3012 for raw/ID validation
or 3013 for structured composition. The Web `/api/reader/lf-edit` endpoint uses
the session/task layer, supports read-only firmware models, and returns an edited
database without writing RF or emulator memory.
