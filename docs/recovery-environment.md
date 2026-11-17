# Recovery companions and environment diagnosis

Recovery helpers remain separate native processes. They are optional for normal
reading, library editing and bridge management. Adding a companion does not add
Go cgo dependencies or change native USB maintenance, DFU, pairing or write
permissions. A recovered candidate still needs the existing on-card verification
and identity checks. No diagnostic attempts recovery or retries a write.

## Matching native companions

The CI native distribution includes all 11 allowlisted workers: nested,
staticnested, darkside, mfkey32, mfkey32v2, mfkey64, staticnested_1nt,
staticnested_2x1nt_rf08s, staticnested_2x1nt_rf08s_1key, mfulc_des_brute and
hardnested. Linux x86-64, macOS arm64 and Windows x86-64 builds use their native
compiler. OpenSSL/liblzma archives are linked statically; the host OS C library
baseline remains required. Windows uses UCRT MinGW and static threading/runtime
libraries. This does not qualify every OS release or every recovery algorithm.

Extract and verify the complete archive as described in
[distribution](distribution.md). `install.py --verify` checks the companion
identity and per-worker hashes and restores executable bits after verification.
The package includes patched worker sources, build recipe, original notices,
static-library notices, compiler identification and library hashes. The recovery
manifest records the same core release, compatibility generation, source revision
and native OS/architecture. Hashes detect corruption, not replacement of the
entire archive and its checksum by an untrusted party.

The core defaults to `recovery/bin` beside its executable when that directory
exists, independently of the current working directory. A damaged companion is
not hidden by falling back to `tools/bin`. Source checkouts retain `tools/bin` as
the fallback. Explicit `--recovery-tools-dir` (Web/reader workflows) or
`--tools-dir` (existing offline recovery commands) overrides the default with a
trusted operator-supplied directory. Browsers cannot choose paths or executables.
Do not place credentials or unrelated executables in the companion directory.

Before executing a helper, the core checks the relevant file and, when present,
the companion manifest, native platform, version and hash. A mismatch is refused
before RF collection by the bound Classic recovery workflow. Existing unversioned
local builds remain usable, but diagnostics report their pairing as unverified.
This file check does not promise a successful process load, candidate or recovery.

From a verified extracted distribution, on Linux/macOS:

```sh
./chameleon environment doctor
./chameleon serve --usb-bridge http://127.0.0.1:12346 --bridge-token-file usb.key
```

On PowerShell use `.\chameleon.exe` with the same arguments. To inspect a source
build, specify `environment doctor --recovery-tools-dir tools/bin`. The command
prints JSON and exits 1 for an unavailable worker or failed requested bridge
check. Unknown observations alone are not failures. Missing workers do not block
unrelated device operations. Add `--usb-bridge URL` or `--ble-bridge URL` with
`--bridge-token-file FILE` to include authenticated management diagnosis; this
makes no bridge data connection.

Build a companion distribution with `scripts/build_distribution.py
--with-recovery` and the existing wheelhouse arguments. The native build requires
C/pthread headers plus static OpenSSL and liblzma archives and their notices.
Linux uses the distribution development packages, macOS Homebrew `openssl@3` and
`xz`, Windows MSYS2 UCRT64 `gcc`, `openssl` and `xz`. Set `--cc` explicitly when
several compilers exist. CI uses the matching native compiler, builds the entire
archive twice, compares bytes, verifies a fresh extraction and runs explicit
argument/loader probes plus an mfkey32 known vector. These probes are separate
from the non-executing doctor. Reproducibility requires the same toolchain,
static-library and wheel inputs; it is not guaranteed across later OS updates.

Corresponding sources are under `recovery/source` in repository layout. From that
directory, `python scripts/build_recovery.py --output NEW_DIRECTORY --revision
RECORDED_REVISION --cc COMPILER` rebuilds workers using installed native build
dependencies; it does not download them. Keep the original manifest/build metadata
for comparison. A source checkout can still use `scripts/build-recovery.sh` for
unversioned local workers.

## Bridge CLI through the existing Desk session

Start one Desk server with a managed USB or BLE bridge. Keep its private
`--task-token-file` separate from the Python bridge token. The bridge commands
below submit through that server's shared task/session layer. They never create
another bridge data client, auto-select a device, reconnect or fall back to USB.

```sh
chameleon tasks session --server http://127.0.0.1:8088 --token-file desk.task.token
chameleon bridge scan --token-file desk.task.token --session-id SESSION_ID --request-id bridge-scan-attempt-001
chameleon tasks wait --token-file desk.task.token --id TASK_ID
chameleon bridge select --token-file desk.task.token --session-id SESSION_ID --request-id bridge-select-attempt-001 --device-id DEVICE_ID
chameleon bridge connect --token-file desk.task.token --session-id SESSION_ID --request-id bridge-connect-attempt-001
```

Replace placeholders with observed IDs. Each command returns a task acceptance;
query/wait before taking the next action. `bridge status` and `bridge disconnect`
use the same flags without `--device-id`. Connect/disconnect rotate the session:
read `tasks session` again before submitting further work. Preserve a request ID
after an ambiguous response and query `tasks find --request-id ...`; do not create
a replacement ID to retry automatically. Existing receipt deduplication and
restart rules apply. Management actions require a valid Desk token and current
session, but do not require physical-card write permission. Direct
`bridge doctor` remains a separate read-only management check for a running bridge.

## Web environment panel

Device Management → Environment and recovery dependency diagnosis submits the
registered `environment.diagnostics` read task. It shares the operation gate,
checks session identity again after waiting and works without a device connection.
The panel shows core host OS/architecture, per-worker state, version/integrity
checks and optional running-bridge checks. Failed and unknown observations are
separate. Reports can be exported; same-process task results can be queried after
refresh without repeating the check. Restart retains task metadata, not bodies.

New bridges advertise a read-only authenticated `/v1/diagnostics` endpoint. Desk
uses it only when advertised. It checks the running bridge's Python/backend
version/import and available selected USB permission metadata. It does not bind
ports already owned by that bridge, enumerate devices, scan, connect or run RF.
Core diagnosis allowlists codes/messages instead of reflecting backend exception
text or credentials. Older bridges remain supported; their host checks are
explicitly unverified. Invalid advertised responses are reported as failures.

Before starting a bridge, its existing local `--doctor` still checks bind
availability, token-file metadata and dependencies. Bluetooth privacy permission,
Windows ACLs, serial occupancy and actual driver/hardware behavior can remain
unknown without an explicit authorized connection. The Web panel never grants
permissions, starts/kills processes, installs software, executes workers or changes
transport policy. File integrity success is not hardware readiness.
