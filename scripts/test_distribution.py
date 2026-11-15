import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
import zipfile


def module(name, file):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).with_name(file))
    result = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(result)
    return result


builder = module("builder", "build_distribution.py")
installer = module("installer", "install_distribution.py")


class DistributionTests(unittest.TestCase):
    def fixture(self, root):
        (root/"bridge").mkdir()
        version = {"release": "0.8.0", "compatibility": 1}
        (root/"bridge/version.json").write_text(json.dumps(version))
        (root/"bridge/_build.json").write_text(json.dumps({"revision":"test"}))
        system, machine = builder.target()
        (root/("chameleon.exe" if system == "windows" else "chameleon")).write_bytes(b"binary")
        (root/"install.py").write_bytes(b"installer")
        (root/"distribution.json").write_text(json.dumps(dict(os=system, arch=machine, python=list(installer.sys.version_info[:2]), revision="test", version=version)))
        self.sums(root)
        return json.dumps(dict(version, revision="test"))

    def sums(self, root):
        (root/"SHA256SUMS").write_text("".join(f"{builder.digest(p)}  {p.relative_to(root).as_posix()}\n" for p in sorted(root.rglob("*")) if p.is_file() and p.name != "SHA256SUMS"))

    def test_tamper_and_missing_inventory_stop_before_execution(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp); core = self.fixture(root)
            with patch.object(installer.subprocess, "check_output", return_value=core) as run:
                installer.verify(root)
                self.assertEqual(run.call_count, 1)
                (root/"bridge/version.json").write_text("tampered")
                with self.assertRaisesRegex(ValueError, "checksum"):
                    installer.verify(root)
                self.assertEqual(run.call_count, 1)
                (root/"SHA256SUMS").write_text("")
                with self.assertRaisesRegex(ValueError, "inventory"):
                    installer.verify(root)
                self.assertEqual(run.call_count, 1)

    def test_version_and_platform_mismatch(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp); self.fixture(root)
            with patch.object(installer.subprocess, "check_output", return_value='{"release":"wrong"}'):
                with self.assertRaisesRegex(ValueError, "version mismatch"):
                    installer.verify(root)
            with patch.object(installer.platform, "machine", return_value="unavailable"), patch.object(installer.subprocess, "check_output") as run:
                with self.assertRaisesRegex(ValueError, "platform/Python mismatch"):
                    installer.verify(root)
                run.assert_not_called()

    def test_zip_bytes_and_wheel_lock_are_deterministic(self):
        with tempfile.TemporaryDirectory() as temp:
            root=Path(temp); source=root/"files"; source.mkdir(); wheelhouse=root/"wheels";wheelhouse.mkdir()
            (wheelhouse/"example.whl").write_bytes(b"wheel")
            builder.copy_wheels(wheelhouse,source,"usb")
            lock=(source/"requirements-usb.lock").read_text()
            self.assertIn(hashlib.sha256(b"wheel").hexdigest(),lock)
            builder.write_zip(source,root/"first.zip");builder.write_zip(source,root/"second.zip")
            self.assertEqual((root/"first.zip").read_bytes(),(root/"second.zip").read_bytes())
            with zipfile.ZipFile(root/"first.zip") as archive:
                self.assertTrue(all(i.date_time==(1980,1,1,0,0,0) for i in archive.infolist()))
            (wheelhouse/"source.tar.gz").write_bytes(b"source")
            with self.assertRaises(ValueError):builder.copy_wheels(wheelhouse,source,"ble")

    def test_windows_epoch_wheel_filename(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            source = root/"wheels"; source.mkdir()
            destination = root/"bundle"; destination.mkdir()
            name = "winrt_windows_devices_bluetooth-4!10.0.28000.2957-py3-none-any.whl"
            (source/name).write_bytes(b"epoch wheel")
            builder.copy_wheels(source, destination, "ble")
            self.assertIn(name, (destination/"requirements-ble.lock").read_text())
            self.assertEqual((destination/"wheels/ble"/name).read_bytes(), b"epoch wheel")


if __name__ == "__main__":
    unittest.main()
