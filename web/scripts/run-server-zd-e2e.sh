#!/usr/bin/env bash

# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

# Runs Dex Web end to end against `dex-server-zd dev`. Needs DEX_SERVER_ZD_BINARY and built web/assets/dist.

set -euo pipefail

if [[ "$#" -ne 0 ]]; then
  echo "usage: DEX_SERVER_ZD_BINARY=/path/to/dex-server-zd $0" >&2
  exit 2
fi
if [[ -z "${DEX_SERVER_ZD_BINARY:-}" || ! -x "$DEX_SERVER_ZD_BINARY" ]]; then
  echo "DEX_SERVER_ZD_BINARY must name an executable dex-server-zd binary" >&2
  exit 2
fi
DEX_SERVER_ZD_BINARY="$(cd "$(dirname "$DEX_SERVER_ZD_BINARY")" && pwd)/$(basename "$DEX_SERVER_ZD_BINARY")"

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd "$script_dir/../.." && pwd)
if [[ ! -f "$repo_root/web/assets/dist/index.html" ]]; then
  echo "web/assets/dist is missing; run: cd web && npm ci && npm run build" >&2
  exit 2
fi

available_port() {
  python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()'
}

work_dir=$(mktemp -d)
log_dir="${DEX_WEB_E2E_LOG_DIR:-$work_dir/logs}"
mkdir -p "$log_dir"
zd_port=$(available_port)
web_port=$(available_port)
zd_pid=""
web_pid=""

cleanup() {
  status=$?
  for pid in "$web_pid" "$zd_pid"; do
    if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
      kill -TERM "$pid"
      wait "$pid" || true
    fi
  done
  if [[ "$status" -ne 0 ]]; then
    for log_file in "$log_dir"/dex-server-zd.log "$log_dir"/dex-web.log; do
      if [[ -f "$log_file" ]]; then
        echo "==> $log_file" >&2
        tail -n 200 "$log_file" >&2
      fi
    done
  fi
  rm -rf "$work_dir"
}
trap cleanup EXIT

wait_for_http() {
  local url=$1
  local pid=$2
  local name=$3
  for _ in {1..240}; do
    if curl --fail --silent --max-time 2 "$url" >/dev/null; then
      return 0
    fi
    if ! kill -0 "$pid" 2>/dev/null; then
      echo "$name exited before $url became ready" >&2
      return 1
    fi
    sleep 0.25
  done
  echo "$url did not become ready" >&2
  return 1
}

# Binaries stay outside the repository.
(cd "$repo_root/web" && GOWORK=off go build -o "$work_dir/dex-web" ./cmd/dex-web)
(cd "$repo_root/cli" && GOWORK=off go build -trimpath -o "$work_dir/dexcli" ./cmd/dexcli)

mkdir -p "$work_dir/definitions"
for flow_package in webv2approval webv2reply; do
  "$work_dir/dexcli" visualize "$repo_root/sdk-go/integ/$flow_package/workflow.go" \
    --schema-version 2.0 \
    --json \
    --out "$work_dir/definitions/$flow_package"
done

"$DEX_SERVER_ZD_BINARY" dev \
  --data-dir "$work_dir/zd" \
  --listen "127.0.0.1:$zd_port" \
  >"$log_dir/dex-server-zd.log" 2>&1 &
zd_pid=$!

"$work_dir/dex-web" \
  --bind-address 127.0.0.1 \
  --port "$web_port" \
  --flow-service-address "127.0.0.1:$zd_port" \
  --flow-rendering-dir "$work_dir/definitions" \
  >"$log_dir/dex-web.log" 2>&1 &
web_pid=$!

# Readiness calls FlowService HealthCheck through standalone Dex Web, so both processes are up.
wait_for_http "http://127.0.0.1:$web_port/readyz" "$web_pid" "Dex Web"
kill -0 "$zd_pid"

cd "$repo_root/sdk-go"
DEX_FLOW_SERVICE_ADDRESS="127.0.0.1:$zd_port" \
DEX_WEB_ADDRESS="127.0.0.1:$web_port" \
DEX_INTEG_WORKER_MODE=pull \
  GOWORK=off go test -count=1 -race -tags webserverzd -v ./integ \
    -run "${DEX_WEB_E2E_TEST_PATTERN:-^(TestDexWebOnServerZD.*|TestDexWebV2ServesGoFlowsWithoutTypeOverrides)$}"
