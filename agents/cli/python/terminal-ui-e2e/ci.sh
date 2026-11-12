#!/usr/bin/env bash
# Installs locked dependencies, checks lint and types, and tests the terminal emulator.
# The scenarios need a dexcli with agents-tui; run-scenarios.sh runs them.

set -euo pipefail

cd "$(dirname "$0")"

uv sync --locked
uv run --frozen ruff check
uv run --frozen ruff format --check
uv run --frozen mypy
uv run --frozen pytest -p no:cacheprovider -v
