"""Exercise a native artifact, including offline dependency installation."""
import argparse
import hashlib
import json
from pathlib import Path
import socket
import subprocess
import sys
import tempfile
import zipfile


def smoke(directory):
    archives = list(Path(directory).glob("*.zip"))
    if len(archives) != 1:
        raise ValueError("expected one native distribution archive")
    archive = archives[0]
    expected = archive.with_suffix(".zip.sha256").read_text().split()[0]
    if hashlib.sha256(archive.read_bytes()).hexdigest() != expected:
        raise ValueError("archive digest mismatch")
    with tempfile.TemporaryDirectory(prefix="desk-install-") as temp:
        root = Path(temp)
        with zipfile.ZipFile(archive) as file:
            file.extractall(root)
        subprocess.run([sys.executable, str(root/"install.py"), "--verify"], check=True)
        subprocess.run([sys.executable, str(root/"install.py"), "--backend", "both"], check=True)
        python = root/".venv"/("Scripts/python.exe" if sys.platform == "win32" else "bin/python")
        binary = root/("chameleon.exe" if sys.platform == "win32" else "chameleon")
        core = json.loads(subprocess.check_output([str(binary), "version", "--json"]))
        bridge = json.loads(subprocess.check_output([str(python), str(root/"bridge/bridge.py"), "--version"]))
        if core != bridge or not core["revision"]:
            raise ValueError("packaged core/bridge identity mismatch")
        for backend in ("usb", "ble"):
            with socket.socket() as listener:
                listener.bind(("127.0.0.1", 0)); port = listener.getsockname()[1]
            report = json.loads(subprocess.check_output([str(python), str(root/"bridge/bridge.py"), "--doctor", "--backend", backend, "--port", str(port)]))
            if any(c["status"] == "fail" for c in report["checks"]):
                raise ValueError("local packaged dependency check failed")
        # No device needs to be connected for authenticated management diagnosis.
        with socket.socket() as listener:
            listener.bind(("127.0.0.1",0)); control = listener.getsockname()[1]
        process = subprocess.Popen([str(python), str(root/"bridge/bridge.py"), "--usb", "--port", str(port), "--control-port", str(control), "--token-file", str(root/"usb.key")], stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        try:
            import time
            deadline=time.monotonic()+10
            while time.monotonic()<deadline:
                if process.poll() is not None: raise ValueError("packaged bridge exited before readiness")
                try:
                    with socket.create_connection(("127.0.0.1",control),timeout=.1): break
                except OSError: time.sleep(.05)
            else: raise TimeoutError("packaged management listener did not start")
            report=json.loads(subprocess.check_output([str(binary),"bridge","doctor","--usb-bridge",f"http://127.0.0.1:{control}","--bridge-token-file",str(root/"usb.key"),"--json"]))
            if not any(c["code"]=="version_match" for c in report["checks"]):raise ValueError("packaged management pairing not verified")
        finally:
            process.terminate()
            try: process.communicate(timeout=10)
            except subprocess.TimeoutExpired: process.kill();process.communicate()
    print("Native artifact: integrity, offline installation, USB/BLE dependencies and read-only management diagnosis passed; no hardware used.")


if __name__ == "__main__":
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument("directory")
    smoke(parser.parse_args().directory)
