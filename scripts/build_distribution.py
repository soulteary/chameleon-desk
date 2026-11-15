#!/usr/bin/env python3
"""Build a native distribution from one committed tree and supplied wheels."""
import argparse
import hashlib
import io
import json
import os
from pathlib import Path
import platform
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
import zipfile


def target():
    system = {"Linux": "linux", "Darwin": "darwin", "Windows": "windows"}.get(platform.system())
    machine = {"x86_64": "amd64", "AMD64": "amd64", "arm64": "arm64", "aarch64": "arm64"}.get(platform.machine())
    if not system or not machine:
        raise ValueError("unsupported distribution platform")
    return system, machine


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def export_tree(repo, revision, dest):
    data = subprocess.check_output(["git", "archive", "--format=tar", revision], cwd=repo)
    with tarfile.open(fileobj=io.BytesIO(data)) as archive:
        for member in archive:
            path = dest / member.name
            if not path.resolve().is_relative_to(dest.resolve()) or not (member.isfile() or member.isdir()):
                raise ValueError("source archive contains an unsafe entry")
            if member.isdir():
                path.mkdir(parents=True, exist_ok=True)
            else:
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(archive.extractfile(member).read())


def copy_wheels(source, dest, backend):
    wheels = sorted(source.iterdir())
    if not wheels or any(p.is_symlink() or not p.is_file() or not re.fullmatch(r"[A-Za-z0-9_.+\-]+\.whl", p.name) for p in wheels):
        raise ValueError(f"{backend} wheelhouse must contain only wheel files")
    folder = dest / "wheels" / backend
    folder.mkdir(parents=True)
    requirements = []
    for p in wheels:
        out = folder / p.name
        shutil.copyfile(p, out)
        requirements.append(f"./wheels/{backend}/{p.name} --hash=sha256:{digest(out)}")
    (dest / f"requirements-{backend}.lock").write_text("\n".join(requirements)+"\n", encoding="utf-8", newline="\n")


def write_zip(folder, output):
    with zipfile.ZipFile(output, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=9) as archive:
        for path in sorted(folder.rglob("*")):
            if not path.is_file():
                continue
            name = path.relative_to(folder).as_posix()
            info = zipfile.ZipInfo(name, (1980, 1, 1, 0, 0, 0))
            info.create_system = 3
            mode = 0o755 if name == "chameleon" else 0o644
            info.external_attr = (0o100000 | mode) << 16
            info.compress_type = zipfile.ZIP_DEFLATED
            archive.writestr(info, path.read_bytes(), compresslevel=9)


def build(args):
    repo = Path(__file__).resolve().parents[1]
    revision = subprocess.check_output(["git", "rev-parse", "--verify", args.ref+"^{commit}"], cwd=repo, text=True).strip()
    system, machine = target()
    env = dict(os.environ, CGO_ENABLED="0", GOOS=system, GOARCH=machine, GOTOOLCHAIN="local")
    # Build only the exported commit; dirty files and local credentials cannot enter it.
    with tempfile.TemporaryDirectory(prefix="desk-distribution-") as temporary:
        root = Path(temporary)
        source, bundle = root / "source", root / "bundle"
        source.mkdir(); bundle.mkdir()
        export_tree(repo, revision, source)
        version = json.loads((source / "tools/ble_bridge/version.json").read_text())
        binary = "chameleon.exe" if system == "windows" else "chameleon"
        subprocess.run([args.go, "build", "-mod=readonly", "-trimpath", "-buildvcs=false", "-ldflags", "-s -w -X github.com/soulteary/chameleon-desk.DistributionRevision="+revision, "-o", str(bundle/binary), "./cmd/chameleon"], cwd=source, env=env, check=True)
        bridge = bundle / "bridge"
        bridge.mkdir()
        for path in sorted((source / "tools/ble_bridge").iterdir()):
            if (path.suffix == ".py" and not path.name.startswith("test_")) or path.name in ("version.json", "requirements.txt", "requirements-usb.txt"):
                shutil.copyfile(path, bridge / path.name)
        (bridge/"_build.json").write_text(json.dumps({"revision": revision}, sort_keys=True)+"\n", encoding="utf-8")
        shutil.copyfile(source/"scripts/install_distribution.py", bundle/"install.py")
        shutil.copyfile(source/"docs/distribution.md", bundle/"INSTALL.md")
        shutil.copyfile(source/"LICENSE", bundle/"LICENSE")
        shutil.copytree(source/"licenses", bundle/"licenses")
        for backend, wheelhouse in (("usb", args.usb_wheelhouse), ("ble", args.ble_wheelhouse)):
            copy_wheels(Path(wheelhouse).resolve(), bundle, backend)
        manifest = dict(schema_version=1, revision=revision, version=version, os=system, arch=machine,
                        python=list(sys.version_info[:2]),
                        go=subprocess.check_output([args.go, "version"], text=True).strip())
        (bundle/"distribution.json").write_text(json.dumps(manifest, sort_keys=True, indent=2)+"\n", encoding="utf-8")
        sums = [f"{digest(p)}  {p.relative_to(bundle).as_posix()}" for p in sorted(bundle.rglob("*")) if p.is_file()]
        (bundle/"SHA256SUMS").write_text("\n".join(sums)+"\n", encoding="utf-8", newline="\n")
        output = Path(args.output).resolve()
        output.mkdir(parents=True, exist_ok=True)
        name = f"chameleon-desk-{version['release']}-{revision[:12]}-{system}-{machine}"
        archive = output / (name+".zip")
        write_zip(bundle, archive)
        (output/(name+".zip.sha256")).write_text(digest(archive)+"  "+archive.name+"\n", encoding="utf-8")
        print(archive)


if __name__ == "__main__":
    if sys.version_info < (3, 11):
        raise SystemExit("distribution builder requires Python 3.11 or later")
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--ref", default="HEAD", help="committed revision; working tree changes are excluded")
    p.add_argument("--go", default="go")
    p.add_argument("--usb-wheelhouse", required=True)
    p.add_argument("--ble-wheelhouse", required=True)
    p.add_argument("--output", default="reports/distribution")
    build(p.parse_args())
