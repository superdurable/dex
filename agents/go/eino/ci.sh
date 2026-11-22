#!/usr/bin/env bash

# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

# Verifies modules, checks formatting, vet, and public API docs, then runs the unit and integration
# tests against the pinned Eino release and again against the newest Eino v0.10.0 alpha.
# DEXCLI must name the dexcli binary; dex-server-zd must sit in the same directory.

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
go run ../../../sdk-go/scripts/check-public-docs.go .

go test -race -count=1 -v ./...

# The second pass runs the same suite against the newest Eino v0.10.0 alpha in a temporary copy.
alpha_version="$(go list -m -versions github.com/cloudwego/eino 2>/dev/null | tr ' ' '\n' | grep '^v0\.10\.0-alpha\.' | sort -t. -k4 -n | tail -1 || true)"
if [[ -z "$alpha_version" ]]; then
  echo "Skipping the Eino v0.10.0 alpha pass: no github.com/cloudwego/eino v0.10.0-alpha.* version resolves." >&2
  exit 0
fi
core_dir="$(cd ../core && pwd)"
alpha_dir="$(mktemp -d)"
trap 'rm -rf "$alpha_dir"' EXIT
cp -R . "$alpha_dir/eino"
cd "$alpha_dir/eino"
go mod edit -replace "github.com/superdurable/dex/agents/go/core=$core_dir"
if ! go get "github.com/cloudwego/eino@$alpha_version"; then
  echo "Skipping the Eino v0.10.0 alpha pass: github.com/cloudwego/eino@$alpha_version does not resolve." >&2
  exit 0
fi
go mod tidy
echo "Eino alpha pass: github.com/cloudwego/eino $alpha_version"
go vet -tags eino_v010 ./...
go test -race -count=1 -v -tags eino_v010 ./...
