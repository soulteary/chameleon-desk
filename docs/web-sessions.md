# Web device sessions

The device window accepts an explicit `tcp:host:port` target for a fixed bridge.
Managed USB and BLE selection use the Python bridge's control panel. Ordinary
Web sessions no longer enumerate or open local USB application ports. Connect
and disconnect are available on a read-only server and require the existing Web
session token. Device/card writes still require `--allow-writes`. See
[USB migration](usb-migration.md) for the explicit native pairing maintenance mode.

Starting `serve` without `--port` leaves hardware disconnected; it no longer
chooses a USB port on the first device request. `serve --port TARGET` opens one
persistent connection before serving and fails startup if that connection cannot
be established. Browser reload does not close the server-owned connection.

## Ownership and ordering

There is one active device session per Desk server, shared by its browser pages.
Background device tasks now provide identifiers, polling, progress, cancellation
and retained results; see [background tasks](background-tasks.md). Multiple
simultaneous devices and a task event stream remain outside this phase. Fixed TCP, managed USB/BLE and native maintenance
are alternative server modes; `--ble-bridge` still cannot be combined with `--port`.
Managed BLE retains its scan/select/connect/disconnect API and cleanup ordering.
The generic direct-connect API cannot replace a configured BLE bridge.

Direct sessions stay open across hardware requests. All hardware operations and
connection transitions use the same gate, including firmware checks that need
device information and BLE management actions. Disconnect/replacement waits for
active work (up to its bounded request timeout); it does not interrupt a write.
There is no FIFO guarantee. A queued request captures the session identity before
waiting and is rejected if the identity changes before it acquires the gate.
The SDK also serializes wire commands on each client. This gate is local to one
Desk process, not an operating-system lock against other programs.

Each connection attempt or disconnect changes the opaque `session_id`, including
failed replacement. A valid replacement closes the old connection before opening
the new one. Invalid target input is rejected without closing the current one.
Open failure leaves the session disconnected. A closed SDK client stays unusable;
Desk does not automatically reconnect or replay commands. Refresh status and
explicitly reconnect. Session IDs are identity checks, not authentication tokens.

## API

- `GET /api/session/ports`: compatibility response `{ "ports": [], "usb_bridge_required": true }`; no native enumeration.
- `GET /api/session/status`: `session_id`, `connected`, `target`, `kind`, and
  transport properties when connected. `kind` is `direct`, `usb-bridge`, `ble-bridge` or `native-usb-maintenance`.
- `POST /api/session/connect`: `{ "target": "tcp:127.0.0.1:12345", "session_id": "..." }`.
- `POST /api/session/disconnect`: `{ "session_id": "..." }`.

POSTs require `X-Chameleon-Token` and the current `session_id` from status. A
stale identity returns HTTP 409. Routine serial targets are rejected with bridge migration instructions.
TCP targets must contain a nonempty host and numeric port in 1–65535. The caller
explicitly selects the target; TCP retains its existing transport restrictions.
No request upgrades TCP to direct-serial privileges.

Hardware requests may send `X-Chameleon-Session`. A mismatched value is rejected
before device work. For compatibility, callers omitting the header are bound to
the current session at receipt, rather than rejected. Responses carry the bound
identity in that header. Offline file APIs are not tied to device sessions.
A firmware update check with explicit model/version remains usable offline.

The Web client sends its observed identity, invalidates facts, previews, device
and slot results on transitions, and ignores old successes and failures after a
new identity is observed. Other pages discover changes when refreshing connection
status; there is no push synchronization. On a stale request, refresh connection
status before attempting new device work. This does not guarantee that all pages
always display the latest server connection without refreshing.

## Validation limits

Go tests cover persistent connection reuse, direct session lifecycle, token and
identity checks, target validation, common-gate serialization, failed replacement
and queued old-session rejection. Web fixtures cover TCP and managed USB selection, native-maintenance scope, identity
propagation and late success/failure isolation. Existing BLE, persistence and
Chromium capability tests remain in place. These tests use simulated transports;
they do not establish physical USB/BLE compatibility or write success rates.

Managed [USB serial bridges](usb-bridge.md#managed-usb-and-desk-web-ui) use
`serve --usb-bridge` and the shared `/api/bridge/*` controller, operation gate,
session ID and background tasks. `/api/session/status` reports `kind: "usb-bridge"`;
connect/disconnect rotate the same session ID used for fixed TCP, native maintenance and BLE.
Direct session transitions are rejected when either managed bridge is configured.
Bridge management and direct TCP are alternative server modes. Native maintenance
can reconnect only the server-configured physical port; ordinary device endpoints
remain unavailable in that mode.
