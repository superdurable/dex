#!/usr/bin/env bash

# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

# Verifies modules, checks formatting, vet, and public API docs, then runs the unit and integration tests.
# DEXCLI must name the dexcli binary; dex-server-zd must sit in the same directory.
# The cross-language tests run the Python core from its locked environment with uv, and the
# TypeScript core from its locked, built package with Node.

set -euo pipefail

if [[ -z "${DEXCLI:-}" ]]; then
  echo "DEXCLI must name the dexcli binary" >&2
  exit 2
fi
if [[ ! -x "$DEXCLI" ]]; then
  echo "DEXCLI is not executable: $DEXCLI" >&2
  exit 2
fi
DEXCLI="$(cd "$(dirname "$DEXCLI")" && pwd)/$(basename "$DEXCLI")"
export DEXCLI
if [[ -z "${DEX_SERVER_ZD_BINARY:-}" ]]; then
  export DEX_SERVER_ZD_BINARY="$(dirname "$DEXCLI")/dex-server-zd"
fi
if [[ ! -x "$DEX_SERVER_ZD_BINARY" ]]; then
  echo "dex-server-zd is not executable: $DEX_SERVER_ZD_BINARY" >&2
  exit 2
fi

cd "$(dirname "${BASH_SOURCE[0]}")"
# The module builds against its go.mod alone, never the repository's go.work.
export GOWORK=off

"$DEXCLI" --version
go version
go mod verify

unformatted="$(gofmt -l .)"
if [[ -n "$unformatted" ]]; then
  echo "gofmt needed for:" >&2
  echo "$unformatted" >&2
  exit 1
fi
go vet ./...
go run ../../../sdk-go/scripts/check-public-docs.go . ./stepwise ./openaimodel ./anthropicmodel ./agentstest

(cd ../../python/core && uv sync --locked --all-extras)
(cd ../../typescript/core && npm ci && npm run build)

go test -race -count=1 -v ./...
