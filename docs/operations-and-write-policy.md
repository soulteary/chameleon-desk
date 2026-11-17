# Operations and write retry policy

Desk's task operations are registered in
[`internal/operations/catalog.json`](../internal/operations/catalog.json).
`Operations()` attaches the existing feature definition: firmware requirements,
parameter-dependent alternatives, model and transport policy. Stable operation
IDs, HTTP methods, write gates, session requirements and `retry_policy: "never"`
are shared by Web, `chameleon tasks` and the Go `TaskClient`.

The server publishes this registry in `/api/capabilities` and
`GET /api/operations` with `operation_schema_version` / `schema_version` 1.
`chameleon tasks operations` prints the local build's registry without device
I/O. Clients should use a matching Desk build. Web task dispatch and write
receipts use the server registry rather than separate path/write lists.

## Execution boundary

Registration is an allowlist for the existing bounded task handlers, not a
replacement for typed validation. Each handler retains its parameter checks,
firmware/model preflight where applicable, write permission, physical write-plan
confirmation and transport guards. Feature previews are advisory. A read or plan
can refer to a write feature without authorizing execution of that feature.
Firmware check/download, connection management, arbitrary raw commands, native
maintenance and DFU are not added to the task allowlist.

Every task retains the submitted device session ID and uses the server's common
device gate. A session change rejects queued work. CLI/SDK task clients use that
same server session; they do not open another connection to the bridge. Legacy
CLI commands and direct `Client` calls remain independent callers: they are not
silently converted to HTTP tasks or deduplicated by the journal.

## Write boundary

The host does not automatically resend a failed WRITE, reconnect and replay an
operation, or resume an interrupted write after restart. Distinct, planned writes
to different blocks/pages are still workflow steps. Readback and read-only key
candidate authentication are permitted; a failed write/readback stops further
card-data writes in the workflow. Existing bounded device-mode cleanup may
still run; it does not repeat the failed card write. Firmware-internal behavior is outside this host policy.
Caller-supplied raw/APDU payloads still require their own effect assessment.

`CloneClassic` now authenticates candidate keys before sending at most one WRITE
per block, including when `ClassicCloneOptions{}` is used. After WRITE, status 2
or 6 no longer selects another key and resends the block. Successful authentication
does not prove write access: a write denial still stops the clone. Existing
`NoWriteRetry` fields compile but are deprecated; false does not restore the old
behavior. Manufacturer-block and trailer restrictions remain unchanged.

`CleanT55Password` now accepts exactly one candidate per explicit invocation.
This operation changes the probe ID/password; checking another candidate by
writing it is not a read-only probe. Firmware errors, absent readback and a
nonmatching identity stop the invocation. CLI users with a dictionary must select
one candidate (`--max-attempts 1`) and inspect the target before a separate attempt.
The CLI retains the partial structured result on errors. This intentionally
removes automatic multi-candidate password cleaning.

Classic clone and T55 checking report `write_attempted` and `outcome`:
`write_not_started`, `completed`, or `write_outcome_unknown`. The first two APIs
also wrap failed attempted writes with `ErrWriteOutcomeUnknown`, preserving the
underlying error for `errors.Is` / `errors.As`. These are conservative workflow
outcomes: entering a mutating method is not proof of RF transmission, and
`completed` only establishes that method's acknowledgement/readback contract.
Other typed workflows retain their existing partial-result contracts. The task
layer supplies the common interruption/outcome envelope for registered operations.

Canceling a client request, polling, or requesting server cancellation does not
undo an acknowledged command. Before another explicit write, retain any partial
result, reconnect if required, read the target and compare against the intended
plan/backup. A new request ID is a new operation, not a safe retry mechanism.

See [task durability and clients](background-tasks.md) for restart handling,
record limits and examples. Tests use simulated transports; they do not establish
physical RF success, storage-device power-loss guarantees or adapter behavior.

Offline file analysis is registered separately with `scope: offline` and no
required device session. This is a server-owned exception for non-device,
non-write features; it does not relax any device operation. See
[offline RF/trace analysis](offline-rf-trace.md) for bounds, retention and the
explicit distinction between file/candidate observations and live RF evidence.

`rf.hf-sniff` retains an explicit mutation gate for emulator RAM reloads; this
does not grant physical-card write permission. HF auth trace and LF acquisition
are separately registered reads with mode/slot impact acknowledgment. Capture
results never establish that unsaved slot RAM survived mode restoration.
