#!/usr/bin/env bash

# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

# Builds the core, the Vercel AI SDK adapter, and the CLI runtime, installs locked dependencies, typechecks, and runs
# the integration tests. DEXCLI must name the dexcli binary; dex-server-zd must sit in the same directory.

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
(cd ../../../typescript/vercel-ai && npm ci && npm run build)
(cd ../runtime && npm ci && npm run build)

# Every test runs the AI SDK OpenAI provider against a local scripted Responses API.
npm ci
# The command must run the Vercel AI SDK version that the adapter's own lockfile tests.
node -e '
const { readFileSync } = require("node:fs");
const lockedVersion = (path) => JSON.parse(readFileSync(path, "utf8")).packages["node_modules/ai"].version;
const commandVersion = lockedVersion("package-lock.json");
const adapterVersion = lockedVersion("../../../typescript/vercel-ai/package-lock.json");
if (commandVersion !== adapterVersion) {
  console.error(`package-lock.json has ai ${commandVersion}, the adapter lockfile ${adapterVersion}`);
  process.exit(1);
}
console.log(`ai ${commandVersion}, as in the adapter lockfile`);
'
npm run typecheck
npm test
