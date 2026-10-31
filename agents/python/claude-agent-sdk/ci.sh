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
sdk = next(package for package in lock["package"] if package["name"] == "dex-python-sdk")
if "registry" not in sdk.get("source", {}):
    raise SystemExit("dex-python-sdk must come from the package registry, not a path")
installed = importlib.metadata.version("dex-python-sdk")
if installed != sdk["version"]:
    raise SystemExit(f"installed dex-python-sdk {installed}, uv.lock {sdk['version']}")
print(f"dex-python-sdk {installed} from {sdk['source']['registry']}")
claude = next(package for package in lock["package"] if package["name"] == "claude-agent-sdk")
installed_claude = importlib.metadata.version("claude-agent-sdk")
if installed_claude != claude["version"]:
    raise SystemExit(f"installed claude-agent-sdk {installed_claude}, uv.lock {claude['version']}")
print(f"claude-agent-sdk {installed_claude}")
PY

uv run --frozen ruff check
uv run --frozen ruff format --check
uv run --frozen mypy
uv run --frozen pytest -p no:cacheprovider -v
