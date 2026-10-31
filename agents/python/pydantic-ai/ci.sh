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
packages = {package["name"]: package for package in lock["package"]}
for name in ("dex-python-sdk", "pydantic-ai-slim"):
    package = packages[name]
    if "registry" not in package.get("source", {}):
        raise SystemExit(f"{name} must come from the package registry, not a path")
    installed = importlib.metadata.version(name)
    if installed != package["version"]:
        raise SystemExit(f"installed {name} {installed}, uv.lock {package['version']}")
    print(f"{name} {installed} from {package['source']['registry']}")
PY

uv run --frozen ruff check
uv run --frozen ruff format --check
uv run --frozen mypy
uv run --frozen pytest -p no:cacheprovider -v
