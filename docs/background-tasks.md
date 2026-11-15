# Background device tasks

The Web UI submits device information, feature probes, HF/LF scans, slot
activation, saved-card loads, settings reads/configuration and slot details/edits
as server-owned tasks. Refreshing or closing the
page does not cancel them. The device window lists retained tasks, their session,
progress, cancellation status and results. Results recovered after refresh stay
in task history; they are not applied to the current device or slot view.
Existing synchronous endpoints remain compatible. Offline file tools, firmware
update checks and connection management are not background tasks in this phase.

## States, progress and outcomes

States are `queued`, `running`, `succeeded`, `failed`, `canceled` and `interrupted`.
`cancel_requested` is a separate flag: a cancellation request is not proof that
work has stopped or that an acknowledged command was undone. A successful result
can win a race with a late cancellation request.

Progress reports `queued`, `device` or `finished`, with command attempts and
received responses while the device operation holds its gate. `commands_attempted`
counts SDK request attempts, including a write that might fail partway through
transmission. `responses_received` counts protocol responses, not successful
workflow steps. Neither is a percentage or proof of persistent storage. The final
saved-card result retains its existing `completed_steps`, `verified`, `persisted`
and `activated` fields, including partial results on errors.

- `not_started`: the task has not entered its device callback. Cancellation while
  queued does not issue commands. Validation or session rejection can fail here.
- `write_not_started`: configuration preflight stopped before calling a mutating
  SDK method; read-only probes may already have run.
- `in_progress`: the device callback has started; its result is pending.
- `completed`: the existing operation returned success. This means its own
  acknowledgement/verification contract, not extra guarantees from the task layer.
- `read_interrupted`: a read workflow entered its callback but failed. Scans can
  temporarily change mode; examine errors and device state after interruption.
- `write_outcome_unknown`: a slot mutation entered its callback but did not
  complete successfully, including cancellation, timeout or connection loss.
  Some commands may have taken effect. Retain partial results and read device
  state before deciding whether another explicit write is appropriate.

Cancellation signals the task context. The SDK may invalidate a connection when
an in-flight command cannot be disambiguated; reconnect explicitly if necessary.
There is no rollback, automatic reconnect or automatic replay. A session transition
still waits on the common gate. Queued tasks retain the submitted identity and
cannot execute against a replacement device session. Task progress does not hold
the registry lock while writing an HTTP response, so queries do not block cancel
or device completion on a slow HTTP reader.

## API

All task endpoints require the existing `X-Chameleon-Token`, including reads.
Host/Origin checks and default write protection remain in force.

```http
POST /api/tasks
Content-Type: application/json
X-Chameleon-Token: ...

{"request_id":"unique-client-request-id","session_id":"observed-session-id","path":"/api/slot/activate","body":{"slot":1}}
```

An accepted submission returns HTTP 202 and a task snapshot with opaque `id`.
The allowed paths are `/api/info`, `/api/features/device`, `/api/scan/hf`,
`/api/scan/lf`, `/api/slot/activate`, `/api/slot/load-card`, `/api/settings/read`,
`/api/settings/configure`, `/api/slot/details` and `/api/slot/edit`. Read operations
accept no body; write bodies use their existing schemas and checks. A malformed
workflow body can produce an accepted task that subsequently fails validation
without device access. There is no arbitrary route or raw-command execution.

- `GET /api/tasks`: server instance ID and retained task metadata, newest first.
  Full results are fetched individually to avoid repeatedly polling card payloads.
- `GET /api/tasks/{id}`: state, progress, outcome, original HTTP status and result.
- `POST /api/tasks/{id}/cancel` with `{}`: request cancellation; query until terminal.

The client supplies a 16–128-byte `request_id`. Within one server lifetime,
repeating exactly the same input/key returns the existing task, never executes
it again; different input with that key returns 409. This lookup precedes session
replacement checks so an old submission can still be found. After result eviction,
a retained receipt returns 410 rather than executing again. These receipts do
not deduplicate legacy synchronous calls or independently generated request IDs.

The Web client sends each submission only once. If the response is lost, it queries
history instead of resending a mutation. Minimal receipts (server ID, request ID,
write flag) use same-tab `sessionStorage`; card payloads and keys are not stored
there. Unknown-write receipts remain until explicitly dismissed after history is
lost; dismissing a reminder does not resolve device state. Definitively rejected
submissions (for example, validation, permission or capacity errors) are not kept
as ambiguous writes. If storage is unavailable, the server list remains queryable after refresh.
There is no automatic resubmission following a browser timeout or reload.

## Lifetime and limits

Tasks and deduplication receipts live in server memory, not a durable execution
journal. Up to 16 nonterminal tasks and 128 results are retained; the oldest terminal
result is evicted when needed. Up to 4096 receipt keys are accepted in one server
lifetime, after which new submissions return 429. Running tasks are never evicted.
Submission bodies are bounded by the card-body limit plus 64 KiB. Each task has a
five-minute outer deadline; existing device operations still have their 30-second
queue/operation bound and per-command timeout. This is not a long-running recovery
worker service. All pages share the same server registry and device operation gate.

Task queries/cancel do not take the device gate. No FIFO ordering is promised.
If the server serving loop returns, teardown signals cancellation and waits for
registered tasks. Process termination or a crash can lose records and interrupt
hardware work; this phase does not add a durable shutdown journal.
After a server restart, old IDs/receipts cannot be recovered or deduplicated by
the new process. The Web UI shows an unresolved receipt warning when it observes
a different server ID or missing history. It does not claim that a lost write
failed or restart it. Completed tasks are not resumable write checkpoints.

Polling is used rather than an event stream. The task panel can be queried after
refresh; other device views still follow the explicit session-isolation rules.
Results may contain imported identifiers or metadata; they are not encrypted and
are accessible to pages authorized for this local Desk server.

## Validation

Offline Go and race tests cover task ownership, deduplication, queued cancellation,
in-flight write cancellation, partial/unknown outcome handling, session changes,
query authorization and shutdown. Web fixtures cover one-shot submission, query
recovery, cancellation, storage failure, restart ambiguity and text-only rendering.
The Chromium simulator verifies that a task survives page reload without issuing
its firmware commands twice. Physical adapters/cards and power-loss recovery are
not established by these tests.

Reader tasks also accept POST bodies at `/api/reader/classic`, `/api/reader/type2` and `/api/reader/lf` with `write:false`. They share session identity, authorization, limits, cancellation and refresh-query behavior. See [Web reader workflows](web-reader.md) for partial data and RF/mode cleanup semantics.
