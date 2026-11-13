#!/usr/bin/env bash

# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

# Installs locked dependencies, runs the strict typecheck (the lint gate), then the integration tests.
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

# DEX_AGENTS_CI_SHARD=<index>/<count> runs one Node test shard; shard 1 also runs the typecheck.
shard="${DEX_AGENTS_CI_SHARD:-1/1}"
if [[ ! "$shard" =~ ^([1-9][0-9]*)/([1-9][0-9]*)$ ]] || (( BASH_REMATCH[1] > BASH_REMATCH[2] )); then
  echo "DEX_AGENTS_CI_SHARD must be <index>/<count> with 1 <= index <= count, not $shard" >&2
  exit 1
fi
shard_index="${BASH_REMATCH[1]}"

cd "$(dirname "${BASH_SOURCE[0]}")"
"$DEXCLI" --version
node --version
npm ci
if (( shard_index == 1 )); then
  npm run typecheck
fi
if [[ "$shard" == "1/1" ]]; then
  npm run test:integration
else
  # Same command as test:integration; --test-shard must precede the file pattern.
  npm run build
  node --test --test-concurrency=1 --test-timeout=240000 --test-shard="$shard" \
    'dist/test/integ/*.integration.test.js'
fi
