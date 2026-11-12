#!/usr/bin/env bash
# Runs the terminal UI scenarios with $DEXCLI, or with a dexcli built from this checkout.
# Arguments go to pytest, such as --command py-builtin (repeatable) or -k steer.

set -euo pipefail

cd "$(dirname "$0")"

if [[ -z "${DEXCLI:-}" ]]; then
  checkout="$(cd ../../../.. && pwd)"
  if [[ ! -f "$checkout/web/assets/dist/index.html" ]]; then
    echo "set DEXCLI to a dexcli with agents-tui, or build Dex Web first (cd web && npm ci && npm run build)" >&2
    exit 2
  fi
  build_dir="$(mktemp -d)"
  trap 'rm -rf "$build_dir"' EXIT
  (cd "$checkout/cli" && GOWORK=off go build -trimpath -o "$build_dir/dexcli" ./cmd/dexcli)
  DEXCLI="$build_dir/dexcli"
fi
DEXCLI="$(cd "$(dirname "$DEXCLI")" && pwd)/$(basename "$DEXCLI")"
export DEXCLI

uv sync --locked
uv run --frozen pytest -p no:cacheprovider scenarios "$@"
