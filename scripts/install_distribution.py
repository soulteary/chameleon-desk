#!/usr/bin/env python3
"""Verify a distribution and explicitly install selected offline bridge wheels."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import subprocess
import sys
import venv


def verify(root):
    names = set()
    for line in (root/"SHA256SUMS").read_text(encoding="utf-8").splitlines():
        expected, name = line.split("  ", 1)
        if name in names:
            raise ValueError("duplicate checksum entry")
        names.add(name)
        path = root / name
        if path.is_symlink() or not path.resolve().is_relative_to(root.resolve()) or hashlib.sha256(path.read_bytes()).hexdigest() != expected:
            raise ValueError("distribution checksum mismatch or unsafe path")
    actual = {p.relative_to(root).as_posix() for p in root.rglob("*") if p.is_file() and p.relative_to(root).parts[0] != ".venv" and p != root/"SHA256SUMS"}
    if names != actual or not {"install.py", "distribution.json", "bridge/version.json", "bridge/_build.json"}.issubset(names):
        raise ValueError("distribution checksum inventory is incomplete")
    info = json.loads((root/"distribution.json").read_text(encoding="utf-8"))
    system = {"Linux":"linux", "Darwin":"darwin", "Windows":"windows"}.get(platform.system())
    machine = {"x86_64":"amd64", "AMD64":"amd64", "arm64":"arm64", "aarch64":"arm64"}.get(platform.machine())
    if info["os"] != system or info["arch"] != machine or info["python"] != list(sys.version_info[:2]):
        raise ValueError("distribution platform/Python mismatch; use the matching OS, architecture and Python minor version")
    binary = root / ("chameleon.exe" if system == "windows" else "chameleon")
    if system != "windows":
        binary.chmod(0o755)  # Some ZIP extractors do not preserve executable bits.
    core = json.loads(subprocess.check_output([str(binary), "version", "--json"], text=True))
    bridge = json.loads((root/"bridge/version.json").read_text())
    revision = json.loads((root/"bridge/_build.json").read_text())["revision"]
    if core != dict(bridge, revision=revision) or revision != info["revision"] or bridge != info["version"]:
        raise ValueError("core/bridge/distribution version mismatch")
    if info.get("recovery_bundled"):
        workers = json.loads((root/"recovery/bin/workers.json").read_text(encoding="utf-8"))
        if workers["core_version"] != core or workers["os"] != system or workers["arch"] != machine:
            raise ValueError("recovery companion identity mismatch")
        expected_workers = {"nested", "staticnested", "darkside", "mfkey32", "mfkey32v2", "mfkey64", "staticnested_1nt", "staticnested_2x1nt_rf08s", "staticnested_2x1nt_rf08s_1key", "mfulc_des_brute", "hardnested"}
        if workers["schema_version"] != 1 or len(workers["files"]) != len(expected_workers) or {w["name"] for w in workers["files"]} != expected_workers:
            raise ValueError("recovery worker inventory mismatch")
        for worker in workers["files"]:
            name = worker["name"]
            if not name.replace("_", "").isalnum():
                raise ValueError("unsafe recovery worker name")
            path = root/"recovery/bin"/(name+(".exe" if system == "windows" else ""))
            if path.is_symlink() or hashlib.sha256(path.read_bytes()).hexdigest() != worker["sha256"]:
                raise ValueError("recovery worker checksum mismatch")
            if system != "windows": path.chmod(0o755)
    return info


def install(root, backend):
    verify(root)
    environment = root/".venv"
    if environment.exists():
        raise ValueError(".venv already exists; use a fresh extraction instead of replacing an environment")
    venv.EnvBuilder(with_pip=True).create(environment)
    python = environment/("Scripts/python.exe" if os.name == "nt" else "bin/python")
    command = [str(python), "-m", "pip", "--isolated", "install", "--no-index", "--no-deps", "--require-hashes"]
    for name in (["usb", "ble"] if backend == "both" else [backend]):
        command += ["-r", f"requirements-{name}.lock"]
    # No index, resolver download, source build, shell activation or device access.
    subprocess.run(command, cwd=root, check=True)
    subprocess.run([str(python), "-m", "pip", "--isolated", "check"], cwd=root, check=True)
    print("Installed selected bridge dependencies. Start the bridge explicitly; see INSTALL.md.")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--verify", action="store_true", help="verify files, platform and core/bridge identity without installing")
    parser.add_argument("--backend", choices=("usb", "ble", "both"))
    args = parser.parse_args()
    if args.verify == bool(args.backend):
        parser.error("choose --verify or --backend usb|ble|both")
    try:
        root = Path(__file__).resolve().parent
        verify(root) if args.verify else install(root, args.backend)
    except (ValueError, OSError, KeyError, subprocess.CalledProcessError) as error:
        print("distribution setup failed: " + str(error), file=sys.stderr)
        raise SystemExit(1)
