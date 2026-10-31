#!/usr/bin/env bash
# Installs locked dependencies, checks lint and types, and runs integration tests with $DEXCLI.

set -euo pipefail

: "${DEXCLI:?set DEXCLI to the dexcli executable}"
DEXCLI="$(cd "$(dirname "$DEXCLI")" && pwd)/$(basename "$DEXCLI")"
export DEXCLI

cd "$(dirname "$0")"

uv sync --locked

uv run --frozen python - <<'PY'
import importlib.metadata
import tomllib
from pathlib import Path

lock = tomllib.loads(Path("uv.lock").read_text())
for name in ("dex-python-sdk", "openai-agents"):
    locked = next(package for package in lock["package"] if package["name"] == name)
    if "registry" not in locked.get("source", {}):
        raise SystemExit(f"{name} must come from the package registry, not a path")
    installed = importlib.metadata.version(name)
    if installed != locked["version"]:
        raise SystemExit(f"installed {name} {installed}, uv.lock {locked['version']}")
    print(f"{name} {installed} from {locked['source']['registry']}")
PY

uv run --frozen ruff check
uv run --frozen ruff format --check
uv run --frozen mypy
uv run --frozen pytest -p no:cacheprovider -v
