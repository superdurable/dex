#!/usr/bin/env bash
# Installs locked dependencies and checks lint and types; the harness command packages run the code.

set -euo pipefail

cd "$(dirname "$0")"

uv sync --locked
uv run --frozen ruff check
uv run --frozen ruff format --check
uv run --frozen mypy
