#!/usr/bin/env bash

# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

# Builds the core, the Pi package, and the CLI core, installs locked dependencies, typechecks, and runs the
# integration tests. DEXCLI must name the dexcli binary; dex-server-zd must sit in the same directory.
# DEX_AGENTS_PI_SEARCH_TOOLS_DIR may name a directory whose bin/ keeps Pi's rg and fd between runs.

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
"$DEXCLI" --version
node --version

# npm packs the file: dependencies for install-links=true, so their dist must exist first.
(cd ../../../typescript/core && npm ci && npm run build)
(cd ../../../typescript/pi && npm ci && npm run build)
(cd ../core && npm ci && npm run build)

# pi-ai calls a local scripted Messages API in every test.
npm ci
npm run typecheck
npm run build

# Pi's grep and find need rg and fd. Pi downloads the missing ones, as it does for its users; the tests copy them.
if [[ -z "${DEX_AGENTS_PI_SEARCH_TOOLS_DIR:-}" ]]; then
  DEX_AGENTS_PI_SEARCH_TOOLS_DIR="$(mktemp -d)"
  trap 'rm -rf "$DEX_AGENTS_PI_SEARCH_TOOLS_DIR"' EXIT
fi
export DEX_AGENTS_PI_SEARCH_TOOLS_DIR
PI_CODING_AGENT_DIR="$DEX_AGENTS_PI_SEARCH_TOOLS_DIR" node dist/test/fixtures/download-search-tools.js

# dexcli dev never starts a connectors MCP server, and nothing opens a browser.
export DEX_CONNECTORS_MCP_BINARY="$DEX_AGENTS_PI_SEARCH_TOOLS_DIR/missing-dex-connectors-mcp"
export DEX_AGENTS_OPEN_BROWSER=0
npm test
