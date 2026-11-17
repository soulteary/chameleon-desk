# Server-owned device tasks

Web, CLI and Go task clients submit the same registered device operations to an
existing Desk server. Bridge lifecycle commands and environment diagnostics also
use this registry, session identity and serialized operation gate. Their durable
`bridge-management` and `environment` phases do not invent firmware-command
progress. Restart marks started host tasks as interrupted reads and never
replays them. The server owns execution and the serialized device session.
Refreshing/closing a page or stopping a polling client does not cancel a task.
Existing synchronous endpoints remain compatible. See
[operation registration and write policy](operations-and-write-policy.md) for
scope, model/transport checks and changes to legacy write behavior.

## States and outcomes

States are `queued`, `running`, `succeeded`, `failed`, `canceled` and `interrupted`.
`cancel_requested` is independent: cancellation is not rollback. A successful
result can win a late cancellation race. Queued cancellation sends no device
commands. An interrupted command can invalidate its connection; reconnect
explicitly when required. No reconnect, command replay or resumed write happens
automatically.

Outcomes are:

- `not_started`: the device callback was not entered. Validation or a changed
  session can fail here; cancellation while queued issues no command.
- `write_not_started`: a handler with an explicit write boundary stopped during
  preflight, before entering its mutating method. Read-only probes may have run.
- `in_progress`: the callback is active; the outcome is pending.
- `completed`: the handler returned success under its own verification contract.
- `read_interrupted`: a started read failed or its server process ended. RF reads
  may temporarily change mode; inspect device state after interruption.
- `write_outcome_unknown`: a started write failed without a definite pre-write
  rejection, or a running write was recovered after process termination. Some
  commands may already have taken effect. Read back before another explicit write.

Phase and `commands_attempted` / `responses_received` are observations, not a
percentage or proof of flash persistence. Partial workflow results retain their
existing verified/saved/completed-step fields. Counts are polled in memory and
saved at checkpoints; a crash can leave earlier counts, including zero for a
write that was actually transmitted.

## Durable history

`serve` opens a private append-only journal before accepting tasks. The default
is `chameleon-desk/tasks.jsonl` beneath Go's `os.UserConfigDir()`:

- Linux: `$XDG_CONFIG_HOME`, otherwise `~/.config`.
- macOS: `~/Library/Application Support`.
- Windows: `%AppData%`.

Use `--task-journal PATH` to choose explicit storage. The parent is created with
mode 0700, and the journal with 0600 on Unix; existing journals must be private
regular files. Windows uses inherited filesystem ACLs; use a directory private
to the Desk user. An OS file lock permits one server per journal and releases on
process death. Separate servers need separate journals and token files.

Accepted submissions are synced before HTTP 202. The running boundary is synced
before the device callback is entered. Terminal state and cancellation are
checkpointed as well. The journal stores metadata and keyed HMAC receipt digests;
it excludes request bodies, keys, card contents, handler errors and result bodies.
The private receipt key stays in the journal and is not returned by the API.
Request/session IDs are caller-visible metadata; do not put secrets in them.
Do not publish the raw journal as a diagnostic report.

After restart with the same journal:

- Queued records become `interrupted` / `not_started`.
- Running reads become `interrupted` / `read_interrupted`.
- Running writes become `interrupted` / `write_outcome_unknown`, even if the last
  stored command count was zero. An in-flight write is never treated as resumable.
- Terminal outcomes remain queryable; `recovered:true` identifies loaded records.
- Result bodies are unavailable (`result_retained:false`). Export/import read
  results into the existing card library before stopping the server if needed.
- Receipt keys still identify the original task or return 410 after eviction;
  they never execute it again. A new `server_id` identifies each process, while
  `store_id` identifies the retained history. Physical confirmation tokens and
  live device session IDs still expire with the process.

A write can succeed while its terminal checkpoint fails. Current-process results
may show that success; restart conservatively recovers the last durable running
record as unknown. New submissions and queued callbacks stop after a journal
failure. Existing in-flight operations may already have changed the device.
The task panel/list reports `journal_error`; retain available results and inspect
the target. There is no fallback to memory-only execution.

Corrupt/truncated, replaced, non-private or locked journals prevent startup.
Do not delete a journal to retry a write. Preserve it, inspect device state and
restore a verified backup or inspect/repair the damaged record offline. A partial
final append is deliberately not discarded automatically: losing a receipt could
permit replay. File sync is not a guarantee against filesystem/hardware failure.

Up to 16 nonterminal tasks, 128 task snapshots/results and 4096 receipt keys are
retained. The append-only journal is limited to 64 MiB. Metadata eviction keeps
receipts; there is no automatic receipt expiry or journal reset. At capacity,
retain/export required history and resolve pending/unknown writes before an
explicit archive and new journal. A new journal loses deduplication across that
boundary. Never automate archival as a reason to resubmit old writes.

## HTTP API

Task reads and writes require the current `X-Chameleon-Token`. Host/Origin checks
and `--allow-writes` still apply. Registered paths, methods and linked features
come from `GET /api/operations`; there is no arbitrary handler or raw execution.

```http
POST /api/tasks
Content-Type: application/json
X-Chameleon-Token: ...

{"request_id":"caller-owned-request-0001","session_id":"observed-session-id","operation_id":"slot.activate","body":{"slot":1}}
```

Legacy `path` submissions remain supported. If both path and operation ID are
provided they must match. GET operations accept no body. POST bodies use the
handler's existing schema; validation can fail in an accepted task before I/O.
Each task has a five-minute outer bound and the existing device operation bound.
This does not add an unbounded recovery worker service.

- `GET /api/tasks`: process/history IDs, durability/error status and retained
  metadata, newest first. Result bodies require individual queries.
- `GET /api/tasks/{id}`: metadata, outcome and result if still in memory.
- `GET /api/tasks?request_id=...`: find an accepted request without resubmitting;
  404 means no retained receipt, 410 means its snapshot was evicted.
- `POST /api/tasks/{id}/cancel` with `{}`: request cancellation; query until terminal.

A request ID is 16–128 bytes. Within a journal's retained history, an identical
submission returns the same task; different input returns 409; an evicted task
returns 410. This lookup precedes session replacement checks. These receipts do
not deduplicate independent IDs, legacy synchronous endpoints or direct SDK calls.
The Web client submits once and queries history after ambiguous acceptance.
Minimal browser receipts contain IDs/write flags, not card payloads or keys.
A recovered history does not restore old results into current device views.

## CLI

The server writes a private current-process token to `JOURNAL.token`; override
with `--task-token-file`. It rotates on startup. The path is printed, not its
contents. Do not share it or reuse a token path across servers.

```sh
./chameleon serve --task-journal ./private-state/tasks.jsonl --allow-writes
./chameleon tasks operations
./chameleon tasks session --token-file ./private-state/tasks.jsonl.token
./chameleon tasks list --token-file ./private-state/tasks.jsonl.token
```

Connect the device through Desk's existing session controls first. For an explicit
submission, use the observed session ID and retain your request ID before sending:

```sh
./chameleon tasks submit --token-file ./private-state/tasks.jsonl.token \
  --request-id slot-activation-review-0001 --session-id OBSERVED_SESSION_ID \
  --operation slot.activate --body-file activation.json
./chameleon tasks find --token-file ./private-state/tasks.jsonl.token \
  --request-id slot-activation-review-0001
./chameleon tasks get --token-file ./private-state/tasks.jsonl.token --id TASK_ID
./chameleon tasks wait --token-file ./private-state/tasks.jsonl.token --id TASK_ID
./chameleon tasks cancel --token-file ./private-state/tasks.jsonl.token --id TASK_ID
```

`activation.json` contains `{"slot":1}`. Commands use `--server` for another
loopback Desk origin. `submit` sends once and returns acceptance, not workflow
success. `get`/`wait` print the outcome; inspect it rather than interpreting a
successful HTTP query as a successful device write. Interrupting `wait` or its
`--timeout` stops polling only. Cancellation is a separate explicit action.

## Go SDK

```go
client, err := chameleon.NewTaskClient("http://127.0.0.1:8088", token)
if err != nil { return err }
input := chameleon.TaskSubmission{
    RequestID: "slot-activation-review-0001", // retain before calling Submit
    SessionID: observedSessionID,
    OperationID: "slot.activate",
    Body: json.RawMessage(`{"slot":1}`),
}
task, err := client.Submit(ctx, input)
if err != nil {
    // If TaskRequestError.Ambiguous, use Find with the retained RequestID.
    // Do not call Submit again to recover a missing acceptance response.
    return err
}
final, err := client.Wait(ctx, task.ID, time.Second)
```

`TaskClient` permits loopback HTTP origins, does not use environment HTTP proxies,
follows no redirects and does not retry POST. `TaskHTTPError` exposes query/rejection HTTP status. `TaskRequestError` carries request
ID, HTTP status and acceptance ambiguity. Invalid/malformed success responses are
also ambiguous. `Find`, `Get`, `List`, `Cancel` and `Session` are explicit APIs;
`Wait` polls GET only. A client context cancellation does not cancel server work.

## Validation

Offline tests cover write failures after authentication, per-block no-replay,
T55 candidate bounds, journal ownership/corruption/replacement, process-death
recovery, failure before admission/callback and lost terminal checkpoints. HTTP
SDK/CLI tests cover shared session guards, request lookup, deduplication after
restart, lost acceptance and redirect/rejection handling. Web fixtures cover
receipt handling, registry dispatch and result availability. Chromium verifies
page reload and a killed/restarted Desk process without a second write command.
These checks do not qualify physical cards, adapters or actual power-loss behavior.

## Current-slot read tasks

`slot.read-current` (`POST /api/slot/read-current`) reads the observed active
slot/channel in emulator mode without switching, saving or writing. Partial
results retain confirmed units; a changed source prevents library import. The
Web task panel can explicitly import retained slot snapshots as distinct drafts.
See [current-slot snapshots](current-slot-snapshots.md) for layouts and limits.

## Offline file operations

Registered `scope: offline` operations have `session_required: false`; all existing
device/bridge operations retain their session scope. The SDK and server normalize
an omitted offline session to the fixed `offline` audit marker and refuse a device
session on that scope. Operation scope cannot be selected in a request. Offline
scope is restricted to non-device, non-write features in the registry.

The [RF/trace file workbench](offline-rf-trace.md) uses this scope. Its tasks do not
open or serialize on the hardware gate and remain valid across device reconnects.
Its durable `offline-analysis` phase recovers as an interrupted read, with no
replay. Offline result bodies use a shared 16 MiB memory budget: old terminal
bodies can expire while receipts and metadata remain. Device result retention and
write retry rules are unchanged. Journals never retain imported files or bodies.
