# Web device sessions

The device window lists USB serial ports (including Windows COM ports) and accepts
an explicit `tcp:host:port` target. TCP input takes precedence over the USB
selection. Refresh enumerates ports without opening them. Connect and disconnect
are available on a read-only server and require the existing Web session token.
Device/card writes still require `--allow-writes` and their existing checks.

Starting `serve` without `--port` leaves hardware disconnected; it no longer
chooses a USB port on the first device request. `serve --port TARGET` opens one
persistent connection before serving and fails startup if that connection cannot
be established. Browser reload does not close the server-owned connection.

## Ownership and ordering

There is one active device session per Desk server, shared by its browser pages.
This phase does not implement multiple simultaneous devices, background task IDs,
a task event stream or immediate cancellation. USB/TCP and configured managed BLE
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

- `GET /api/session/ports`: `{ "ports": ["COM7"] }`.
- `GET /api/session/status`: `session_id`, `connected`, `target`, `kind`, and
  transport properties when connected. `kind` is `direct` or `ble-bridge`.
- `POST /api/session/connect`: `{ "target": "tcp:127.0.0.1:12345", "session_id": "..." }`.
- `POST /api/session/disconnect`: `{ "session_id": "..." }`.

POSTs require `X-Chameleon-Token` and the current `session_id` from status. A
stale identity returns HTTP 409. Serial targets must match current enumeration;
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
and queued old-session rejection. Web fixtures cover USB selection, identity
propagation and late success/failure isolation. Existing BLE, persistence and
Chromium capability tests remain in place. These tests use simulated transports;
they do not establish physical USB/BLE compatibility or write success rates.
