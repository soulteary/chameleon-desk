# Controlled LF identity cloning

The Web API can clone a supported standard LF identity to one isolated dedicated
T55xx test target. This uses `Client.CloneLFObserved` with the existing `Client.CloneLF` behavior; it does not implement
a full T55xx memory backup, configuration editor, or password recovery.

The source must be a standard saved card. Active reader drafts, diagnostic
EM4x05/Pyramid records, unsupported tags, incomplete IDs, and a mismatched explicit
protocol are rejected. EM410x (including Electra), HIDProx, ioProx, Viking, PAC and
Jablotron use their existing codecs. IDTECK has no selected-protocol readback in
this workflow and is rejected. Ordinary EM410x clock/subtype metadata is not
reproduced by the T55xx writer; the plan states the effective format.

Both endpoints accept a wrapper with `request`:

```json
{
  "request": {
    "schema": 1,
    "source": {"id":"source","name":"PAC","uid":"3132333435363738","tag":150,"sak":0,"atqa":[],"ats":[],"data":[]},
    "protocol": "pac",
    "new_password": "01020304",
    "old_passwords": ["05060708"],
    "target_isolated": true
  }
}
```

`POST /api/physical/lf/plan` validates the source, explicit four-byte passwords,
and isolation acknowledgement without RF/device commands. It returns a plan
with required commands and a short-lived session-bound `confirmation`. The plan
contains no passwords or public password hashes. Planning does not require
`--allow-writes`.

After reviewing the plan, `POST /api/physical/lf/write` accepts the same request
plus its top-level `confirmation`. Execution requires `--allow-writes`, the Web
request token, a current session, and model/advertised-command checks. Both
endpoints are available through the existing background task interface. Tasks
retain their identifier and result for refresh/reconnect tracking; duplicate
submission of the same task request does not replay its operation.

The confirmation binds the session and complete input, including credentials;
changed input or a replaced session requires a new plan. ioProx additionally
uses the firmware decoder before the mutating boundary and requires its complete
encoded result to match the source. No default password list, native transport
fallback, automatic reconnect, write retry, or rollback is added.

T55xx targets do not offer a source-UID binding through this API. The isolation
acknowledgement is an operator statement: place only one dedicated writable test
tag in the RF field. The host cannot prove that the same physical target remained
there. A firmware write command can itself perform several RF operations.

Execution issues one host clone command, then reads the selected protocol and
compares the encoded identity. Success is `identity_verified`, with
`verification_scope:"selected_protocol_identity_only"`, `password_verified:false`
and `target_uid_bound:false`. This does not verify target password, full memory,
configuration, durability, or interchangeability with the source card. Ordinary
EM410x subtypes 100–103 are compared by their five-byte identity; differing
reader-reported clock subtypes do not invalidate an otherwise matching identity.
Electra subtype 104 remains distinct and requires its full thirteen-byte identity.

The response distinguishes `write_not_started` from `write_outcome_unknown`.
The task mutation marker is set immediately before the T55xx write command,
after codec, model and reader-mode checks. After that boundary, an absent
acknowledgement, failed cleanup, missing
readback, different identity, or cancellation leaves the outcome unknown. Inspect
the target before making a new decision; a failed request is not permission to
repeat the write. Tests use local protocol simulators and do not qualify physical
tag families or firmware versions.
