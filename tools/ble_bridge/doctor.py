"""Non-invasive local checks. Unknown is not a successful hardware probe."""
import errno
import importlib
import importlib.metadata
import os
from pathlib import Path
import platform
import socket
import stat
import sys

from version import build_info

FAILURE_CODES = frozenset(("serial_port_busy", "serial_port_missing", "permission_denied",
                          "serial_access_denied_or_busy", "serial_io_error", "serial_setup_failed",
                          "adapter_busy", "adapter_service_unavailable", "adapter_not_ready", "ble_backend_error"))

class BackendFailure(RuntimeError):
    def __init__(self, code):
        self.code = code
        super().__init__(code)


def failure_code(error, backend):
    if isinstance(error, BackendFailure):
        return error.code if error.code in FAILURE_CODES else "backend_error"
    number = getattr(error, "errno", None)
    dbus = getattr(error, "dbus_error", "")
    if number in (errno.EBUSY, errno.EAGAIN):
        return "serial_port_busy" if backend == "usb" else "adapter_busy"
    if number == errno.ENOENT:
        return "serial_port_missing" if backend == "usb" else "adapter_service_unavailable"
    if isinstance(error, PermissionError) or number in (errno.EACCES, errno.EPERM) or dbus.endswith(("AccessDenied", "NotAuthorized", "NotPermitted")):
        return "serial_access_denied_or_busy" if backend == "usb" and os.name == "nt" else "permission_denied"
    # Windows serial open errors often wrap WinError 5 without an errno.
    if backend == "usb" and os.name == "nt" and ("PermissionError" in str(error) or "Access is denied" in str(error)):
        return "serial_access_denied_or_busy"
    if dbus.endswith("NotReady"):
        return "adapter_not_ready"
    if dbus.endswith(("ServiceUnknown", "NameHasNoOwner")):
        return "adapter_service_unavailable"
    return "serial_io_error" if backend == "usb" else "ble_backend_error"


def dependency(backend):
    return ("pyserial", "serial", "3.5") if backend == "usb" else ("bleak", "bleak", "3.1.0")


def diagnose(args):
    checks = []
    def add(code, status, message):
        checks.append(dict(code=code, status=status, message=message))
    add("python_version", "pass" if sys.version_info >= (3, 11) else "fail", "Python 3.11 or later is required")
    distribution, module, expected = dependency(args.backend)
    try:
        actual = importlib.metadata.version(distribution)
        importlib.import_module(module)
        add("dependency_version", "pass" if actual == expected else "fail", f"{distribution}: installed {actual}, required {expected}; use the matching environment/lock file")
    except importlib.metadata.PackageNotFoundError:
        add("dependency_missing", "fail", f"Install the {args.backend} requirements into the Python environment running this command")
    except Exception:
        add("dependency_load_failed", "fail", "Installed backend cannot load; check OS libraries, architecture and the matching environment")
    if args.control_port == args.port:
        add("listener_configuration", "fail", "Management and data ports must differ")
    for name, number in (("data", args.port), ("management", args.control_port)):
        if number is None:
            continue
        family = socket.AF_INET6 if ":" in args.listen else socket.AF_INET
        try:
            with socket.socket(family, socket.SOCK_STREAM) as listener:
                if os.name == "nt" and hasattr(socket, "SO_EXCLUSIVEADDRUSE"):
                    listener.setsockopt(socket.SOL_SOCKET, socket.SO_EXCLUSIVEADDRUSE, 1)
                listener.bind((args.listen, number))
            add(name + "_port_free", "pass", "Bind check passed; this is a snapshot, not a reservation")
        except OSError as error:
            code = "port_in_use" if error.errno in (errno.EADDRINUSE, 10048) else "listener_permission_or_address"
            add(name + "_" + code, "fail", "Cannot bind listener; stop the owning process or choose another loopback port and update Desk")
    if args.token_file:
        try:
            p = Path(args.token_file)
            mode = p.lstat().st_mode
            if not stat.S_ISREG(mode) or (os.name != "nt" and mode & 0o077):
                raise ValueError()
            token = p.read_bytes()
            if len(token) > 256 or len(token.strip()) != 64 or any(c not in b"0123456789abcdef" for c in token.strip()):
                raise ValueError()
            add("token_file", "pass", "Token format and available permission metadata checked; Windows ACLs require separate inspection")
        except FileNotFoundError:
            add("token_not_created", "unknown", "Management startup creates a new private token; this check does not create or modify files")
        except (OSError, ValueError):
            add("token_file_invalid", "fail", "Use an accessible private regular token file; do not use a symlink or a public file")
    if args.backend == "usb" and args.serial_port and os.name == "posix":
        path = Path(args.serial_port)
        if not path.exists():
            add("serial_port_missing", "fail", "Selected serial path is absent; enumerate and select the application port again")
        elif not os.access(path, os.R_OK | os.W_OK):
            add("serial_permission_denied", "fail", "Check device ownership/group and OS serial permissions; do not run the workbench as root")
        else:
            add("serial_access_metadata", "unknown", "Access metadata permits an attempt; device occupancy and driver setup were not tested")
    hints = {"Linux": "Check serial group/udev permissions or BlueZ/D-Bus and adapter power", "Darwin": "Check the application serial port or Bluetooth privacy permission for this Python executable", "Windows": "Check COM ownership/driver or Bluetooth adapter/privacy settings; access denied may also mean another process owns the port"}
    add("hardware_unverified", "unknown", hints.get(platform.system(), "Check platform backend support") + "; no serial port, BLE scan or firmware command was attempted")
    return dict(schema_version=1, bridge_version=build_info(), backend=args.backend, platform=platform.system(), checks=checks)
