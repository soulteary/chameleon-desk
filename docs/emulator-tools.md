# Current emulator controls

The Slots window includes configuration and detection-log controls for the
current HF slot. Refresh device facts first. The selected slot/type must match
the device's current active slot, and the device must already be in emulator
mode. Reading these tools never selects a slot or changes mode. Unsupported
firmware commands, types and unavailable transport/session states produce a
disabled reason through the shared feature catalog.

Read configuration before editing. The UI exposes family-specific flags, PRNG
or write mode, supported counters/tearing state, authentication attempts, version
and signature bytes. Only changed properties are submitted. Review the preview,
confirm, then apply. The server compares the captured configuration under the
same device gate, writes the patch, and reads it back. Optional flash persistence
has a separate acknowledgement; a verified RAM change does not imply a completed
flash save. A changed current slot/configuration rejects a stale request.

Configuration writes require `serve --allow-writes`. Failure can leave a partial
configuration. Inspect the returned before/after observations and read again;
there is no automatic rollback, replay, slot switch or native transport fallback.

Detection logs are read in explicit bounded requests, with an offset and limit
(1–1024). Classic nonce records and Type2 password observations have different
shapes. Request completion means that requested batch completed, not that the
entire device log was captured. Reading does not clear logs or toggle detection.
Results can contain credentials; export and store them accordingly. Task result
bodies remain in server memory and are not written to the task journal.

The Reader window also exposes RF configuration read/compare/update and an
explicit device-mode change. Readback verifies successful changes. These actions
use the existing serialized session and task layer. Mode changes invalidate
previous facts; refresh before another operation. Emulator reads do not invoke
this mode-change action implicitly.

SDK entry points: `ReadCurrentEmulator`, `ConfigureCurrentEmulator`,
`ReadCurrentDetectionLogs`, `ConfigureReaderObserved` and
`ChangeDeviceModeObserved`. Parameter-specific `Requirements` methods drive the
Web availability preview. HTTP routes are listed in the generated
[feature catalog](features.md). Existing low-level CLI commands remain available;
these Web forms do not create a second command retry policy.
