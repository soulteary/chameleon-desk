import argparse
import asyncio
import errno
import importlib.metadata
import json
from pathlib import Path
import socket
import tempfile
import unittest
from unittest.mock import patch

import bridge
import doctor
import management
import serial_backend
import serial_management


def args(**values):
    return argparse.Namespace(**dict(dict(backend="usb", listen="127.0.0.1", port=0, control_port=None, token_file=None, serial_port=None), **values))


class DoctorTests(unittest.TestCase):
    def test_missing_dependency_without_opening_hardware(self):
        with patch.object(doctor.importlib.metadata, "version", side_effect=importlib.metadata.PackageNotFoundError), patch.object(doctor.importlib, "import_module") as load:
            result = doctor.diagnose(args())
        load.assert_not_called()
        self.assertIn("dependency_missing", [c["code"] for c in result["checks"]])
        self.assertIn("hardware_unverified", [c["code"] for c in result["checks"]])

    def test_busy_listener_and_version_mismatch(self):
        with socket.socket() as owned:
            owned.bind(("127.0.0.1", 0))
            owned.listen()
            with patch.object(doctor.importlib.metadata, "version", return_value="wrong"), patch.object(doctor.importlib, "import_module"):
                result = doctor.diagnose(args(port=owned.getsockname()[1]))
        codes = {c["code"]: c["status"] for c in result["checks"]}
        self.assertEqual(codes["dependency_version"], "fail")
        self.assertEqual(codes["data_port_in_use"], "fail")

    def test_private_token_redaction_and_no_creation(self):
        with tempfile.TemporaryDirectory() as directory:
            token = Path(directory)/"token.key"
            missing = doctor.diagnose(args(token_file=str(token)))
            self.assertFalse(token.exists())
            self.assertIn("token_not_created", [c["code"] for c in missing["checks"]])
            token.write_text("a"*64)
            token.chmod(0o600)
            result = doctor.diagnose(args(token_file=str(token)))
            self.assertNotIn("a"*64, json.dumps(result))
            self.assertIn("token_file", [c["code"] for c in result["checks"]])

    def test_error_classification_does_not_reflect_secrets(self):
        self.assertEqual(doctor.failure_code(OSError(errno.EBUSY, "secret"), "usb"), "serial_port_busy")
        self.assertEqual(doctor.failure_code(OSError(errno.ENOENT, "secret"), "usb"), "serial_port_missing")
        with patch.object(doctor.os, "name", "nt"):
            self.assertEqual(doctor.failure_code(PermissionError("secret"), "usb"), "serial_access_denied_or_busy")
        self.assertNotIn("secret", doctor.failure_code(RuntimeError("secret"), "ble"))

    def test_build_metadata_on_both_backends(self):
        class Device:
            address = "test"
            name = "test"
        ble = management.Controller(None, None)
        ble.selected = Device()
        usb = serial_management.SerialController(None, None)
        usb.selected = {"id": "COM7"}
        for owner in (ble, usb):
            self.assertEqual(owner.status()["bridge_version"], bridge.build_info())
            self.assertEqual(owner.bridge.metadata()["bridge_version"], bridge.build_info())
        fixed = serial_backend.SerialBridge("COM7", None)
        self.assertEqual(fixed.metadata()["bridge_version"], bridge.build_info())

    def test_serial_setup_preserves_sanitized_failure_code(self):
        def factory(**options):
            raise OSError(errno.EBUSY, "secret path")
        async def run():
            worker = serial_backend.SerialWorker("port", factory, 1)
            with self.assertRaisesRegex(doctor.BackendFailure, "serial_port_busy"):
                await worker.connect()
            await worker.disconnect()
            self.assertNotIn("secret", worker.error)
        asyncio.run(run())


if __name__ == "__main__":
    unittest.main()
