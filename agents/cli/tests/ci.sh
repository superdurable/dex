#!/usr/bin/env bash
# Checks the CLI configuration schemas, example, and conformance cases; needs no Dex server.

set -euo pipefail

cd "$(dirname "$0")"

uv sync --locked
uv run --frozen ruff check .
uv run --frozen ruff format --check .
uv run --frozen mypy
uv run --frozen pytest -p no:cacheprovider -v
