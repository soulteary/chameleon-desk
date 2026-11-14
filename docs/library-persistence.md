# Card-library persistence

Desk Web UI saves the card database in the browser's IndexedDB. It does not
write a server-side library file, synchronize browsers, or alter cards on a
connected device. Library saving works with the default read-only server.

## Automatic save and recovery

Changes to the database JSON, imported database files, and successful add/delete
results schedule a save after 600 ms without another edit. Desk validates the
complete database through its existing Go parser before storing it. Validation
includes cards, dictionaries, folder references and the existing 32 MiB limit.
Unknown metadata and Flutter-prefixed collections are preserved. Partial JSON
and invalid databases remain editable drafts and do not replace saved data.

The library status distinguishes pending edits, validation/saving, a completed
save and a failure. A save is acknowledged only when the IndexedDB transaction
completes. The transaction requests strict durability and updates the current
and previous snapshots atomically. Only one previous successful snapshot is
retained; this is not a full revision history.

Opening Desk, refreshing the page, restarting the browser, or restarting the
Desk server restores the last committed snapshot. Recovery validates the stored
version and database before replacing the editor. Use the same browser profile
and origin: for example, `http://127.0.0.1:8088` differs from
`http://localhost:8088` and from another port. A different origin starts with its
own library. The default server address is stable across normal restarts.

Automatic saving covers the database, not a card pasted into the add-card input
before pressing Add, search text, NDEF drafts, or other tools. Restoring the
library does not run a search, connect hardware, or replay a card operation.

## Failures, retries and recovery controls

- **Save now / retry** validates and saves the current draft. Quota, transaction,
  validation and network failures retain the draft and leave the last committed
  snapshots unchanged. There is no automatic retry loop after a failure; a new
  edit schedules another attempt, or use the retry button.
- **Reload saved version** reopens storage and reads the current snapshot.
  Unsaved changes require confirmation before replacement. Use this after an
  unavailable/blocked storage read, a service restart, or a multi-tab conflict.
- **Restore previous version** validates the preceding snapshot and replaces
  the current saved library in one transaction. It requires confirmation and
  keeps that preceding snapshot available. It is not a redo history. If the
  previous snapshot is invalid, nothing is replaced.
- **Export page draft** downloads the database editor as text, including partial
  or invalid JSON. It does not claim that a browser save succeeded. For a valid
  database, use the existing database export to make a portable JSON backup.

Unsupported snapshot versions or corrupted records are not deleted or silently
overwritten. Recovery stays blocked until a saved version is read successfully
or a valid preceding version is explicitly restored. If neither can be restored,
retain the browser profile and inspect/export the IndexedDB records using browser
developer tools before clearing storage. Desk does not automatically reset the
object store. A failed read must not be treated as an empty database.

Two pages may show different revisions. Each write compares the exact snapshot
read by that page with the current record inside the same write transaction.
If another page committed first, Desk refuses the stale write, retains the local
draft and asks the user to export it before reloading the saved version. It does
not merge or force-overwrite. Use Reload to see another page's changes; there is
no live cross-tab synchronization.

Imports and reloads ask for confirmation before discarding unsaved database
edits. Pending, failed and in-flight saves keep the page marked as unsaved. A pending
add/delete response also enables the unload warning until its outcome is known.
Desk registers a `beforeunload` warning while unsaved changes exist and attempts
an early save when the page becomes hidden. Browsers control whether they show
that warning, usually require prior user interaction, and can terminate a page
without one. An unload event cannot guarantee completion of asynchronous work.
Exporting a draft does not clear the unsaved status: a download can be cancelled.

## Storage scope and limits

Browser data can be removed by clearing site data, private-browsing cleanup,
profile removal, quota management or browser policy. Desk cannot guarantee
recovery after those events, an OS/storage failure, or a lost profile. Keep
independent exported backups. Changing the server host/port does not migrate
an existing library automatically.

Library records can contain card keys and other imported metadata. Desk does
not encrypt IndexedDB or exported files; protect the browser profile and backup
files accordingly. The database is sent only to the configured local Desk HTTP
server for validation and existing library operations, not an external service.

## Validation

`npm run test:webui` includes real IndexedDB API transaction semantics through
the test-only `fake-indexeddb` package: commit abort, quota failure, invalid
input, version/corruption handling, previous-version recovery, stale async saves,
and concurrent writers. UI tests cover debounce, add/delete persistence, new-page
recovery, draft export, retry controls and unsaved warnings.

`npm run test:browser` uses Playwright and an actual Chromium profile against the
built Go server. Set `CHAMELEON_WEBUI_TEST_BINARY` to the CLI path. It verifies
reload, browser process restart, server restart at the same address, Go schema
rejection, native before-unload prompting and preservation of the committed
library. CI runs this on Linux. It does not establish equivalent behavior for
all browser versions, private modes, profile corruption or power loss.
