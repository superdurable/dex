#!/usr/bin/env bash
# Installs locked dependencies, checks lint and types, and runs integration tests with $DEXCLI.

set -euo pipefail

: "${DEXCLI:?set DEXCLI to the dexcli executable}"
DEXCLI="$(cd "$(dirname "$DEXCLI")" && pwd)/$(basename "$DEXCLI")"
export DEXCLI

cd "$(dirname "$0")"

# The mcp extra brings in the MCP SDK for dex_agents.mcp and its tests.
uv sync --locked --all-extras

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
PY

# DEX_AGENTS_CI_SHARD=<index>/<count> runs every count-th test file; shard 1 also checks lint and types.
shard="${DEX_AGENTS_CI_SHARD:-1/1}"
if [[ ! "$shard" =~ ^([1-9][0-9]*)/([1-9][0-9]*)$ ]] || (( BASH_REMATCH[1] > BASH_REMATCH[2] )); then
  echo "DEX_AGENTS_CI_SHARD must be <index>/<count> with 1 <= index <= count, not $shard" >&2
  exit 1
fi
shard_index="${BASH_REMATCH[1]}"
shard_count="${BASH_REMATCH[2]}"

shard_test_files="$(find tests -name 'test_*.py' | LC_ALL=C sort)"

if (( shard_index == 1 )); then
  uv run --frozen ruff check . ../../conformance
  uv run --frozen ruff format --check . ../../conformance
  uv run --frozen mypy
  # Every file pytest collects must be in the list the shards split.
  collected_test_files="$(uv run --frozen pytest -p no:cacheprovider --collect-only -q | sed -n 's/::.*//p' | LC_ALL=C sort -u)"
  unsharded_test_files="$(comm -13 <(printf '%s\n' "$shard_test_files") <(printf '%s\n' "$collected_test_files"))"
  if [[ -n "$unsharded_test_files" ]]; then
    echo "pytest collects test files that no shard runs:" >&2
    echo "$unsharded_test_files" >&2
    exit 1
  fi
fi

test_files=()
position=0
while IFS= read -r test_file; do
  if (( position % shard_count == shard_index - 1 )); then
    test_files+=("$test_file")
  fi
  position=$((position + 1))
done <<<"$shard_test_files"
if (( ${#test_files[@]} == 0 )); then
  echo "shard $shard has no test files" >&2
  exit 1
fi
uv run --frozen pytest -p no:cacheprovider -v "${test_files[@]}"
