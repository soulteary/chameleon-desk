"""Build identity shared by the core and both bridge backends."""
import json
from pathlib import Path


def build_info():
    root = Path(__file__).resolve().parent
    value = json.loads((root / "version.json").read_text(encoding="utf-8"))
    build = root / "_build.json"
    value["revision"] = json.loads(build.read_text(encoding="utf-8"))["revision"] if build.exists() else ""
    return value
