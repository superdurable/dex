#!/usr/bin/env bash

# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

# Runs the Dex Web payload encryption Playwright test against dexcli dev -codec-key-file and a Go SDK
# Worker that encrypts every value. Needs DEX_SERVER_ZD_BINARY, built web/assets/dist, web/node_modules,
# and a Playwright Chromium (npx playwright install chromium). Set DEX_WEB_E2E_SCREENSHOT_DIR to keep
# cropped screenshots of the decrypted value and the placeholder. Playwright traces of failed tests stay in
# DEX_WEB_E2E_OUTPUT_DIR, or in a new directory under TMPDIR.

set -euo pipefail

if [[ -z "${DEX_SERVER_ZD_BINARY:-}" || ! -x "$DEX_SERVER_ZD_BINARY" ]]; then
  echo "DEX_SERVER_ZD_BINARY must name an executable dex-server-zd binary" >&2
  exit 2
fi

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd "$script_dir/../.." && pwd)
if [[ ! -f "$repo_root/web/assets/dist/index.html" ]]; then
  echo "web/assets/dist is missing; run: cd web && npm ci && npm run build" >&2
  exit 2
fi

available_port() {
  python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()'
}

work_dir=$(mktemp -d "${TMPDIR:-/tmp}/dex-web-payload-e2e.XXXXXX")
dex_port=$(available_port)
web_port=$(available_port)
dev_pid=""
worker_pid=""

cleanup() {
  status=$?
  for pid in "$worker_pid" "$dev_pid"; do
    if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
      kill -TERM "$pid"
      wait "$pid" || true
    fi
  done
  if [[ "$status" -ne 0 ]]; then
    for log_file in "$work_dir/dexcli-dev.log" "$work_dir/worker.log"; do
      [[ -f "$log_file" ]] && { echo "==> $log_file" >&2; tail -n 100 "$log_file" >&2; }
    done
  fi
  rm -rf "$work_dir"
}
trap cleanup EXIT

# Binaries stay outside the repository.
(cd "$repo_root/cli" && GOWORK=off go build -trimpath -o "$work_dir/dexcli" ./cmd/dexcli)
(cd "$repo_root/web/scripts/local-demo" && GOWORK=off go build -o "$work_dir/encrypted-payloads" ./encrypted-payloads)

"$work_dir/dexcli" codec-server keygen -o "$work_dir/payload-keys.json" >/dev/null
"$work_dir/dexcli" dev -open=false -connectors-mcp=false \
  -dex-port "$dex_port" -web-port "$web_port" \
  -codec-key-file "$work_dir/payload-keys.json" \
  -zd-binary "$DEX_SERVER_ZD_BINARY" -zd-data-dir "$work_dir/zd" -server-log-folder "$work_dir/logs" \
  >"$work_dir/dexcli-dev.log" 2>&1 &
dev_pid=$!

for _ in {1..240}; do
  curl --fail --silent --max-time 2 "http://127.0.0.1:$web_port/readyz" >/dev/null && break
  kill -0 "$dev_pid" 2>/dev/null || { echo "dexcli dev exited early" >&2; exit 1; }
  sleep 0.25
done
curl --fail --silent "http://127.0.0.1:$web_port/api/codec-server" | grep -q '"configured":true'

order_flow_id="encrypted-order-$RANDOM"
declined_flow_id="declined-payment-$RANDOM"
DEX_FLOW_SERVICE_ADDRESS="127.0.0.1:$dex_port" "$work_dir/encrypted-payloads" \
  -key-file "$work_dir/payload-keys.json" -order-flow-id "$order_flow_id" -declined-flow-id "$declined_flow_id" \
  >"$work_dir/worker.log" 2>&1 &
worker_pid=$!
for _ in {1..120}; do
  grep -q '^ORDER ' "$work_dir/worker.log" && break
  kill -0 "$worker_pid" 2>/dev/null || { echo "the encrypted-payloads Worker exited early" >&2; exit 1; }
  sleep 0.25
done

cd "$repo_root/web"
DEX_WEB_E2E_URL="http://127.0.0.1:$web_port" \
DEX_WEB_E2E_ORDER_FLOW_ID="$order_flow_id" \
DEX_WEB_E2E_DECLINED_FLOW_ID="$declined_flow_id" \
DEX_WEB_E2E_OUTPUT_DIR="${DEX_WEB_E2E_OUTPUT_DIR:-$(mktemp -d "${TMPDIR:-/tmp}/dex-web-payload-e2e-results.XXXXXX")}" \
  npx playwright test --config e2e/playwright.config.ts
