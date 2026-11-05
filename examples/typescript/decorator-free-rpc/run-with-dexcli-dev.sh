#!/bin/bash

# Copyright (c) 2022-2026 Super Durable, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Starts dexcli dev and runs main.ts with Node.js type stripping, without a build step.
set -euo pipefail

available_port() {
  node -e 'const net=require("node:net");const server=net.createServer();server.listen(0,"127.0.0.1",()=>{console.log(server.address().port);server.close();});'
}

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
dexcli="${DEXCLI_PATH:-dexcli}"
dex_port="${DEX_DECORATOR_FREE_RPC_DEX_PORT:-$(available_port)}"
web_port="${DEX_DECORATOR_FREE_RPC_WEB_PORT:-$(available_port)}"
log_file="/tmp/test-typescript-decorator-free-rpc-services.log"
data_dir=$(mktemp -d)
dexcli_pid=""
: >"$log_file"

cleanup() {
  status=$?
  if [[ -n "$dexcli_pid" ]] && kill -0 "$dexcli_pid" 2>/dev/null; then
    kill -TERM "$dexcli_pid"
    wait "$dexcli_pid" || true
  fi
  if [[ "$status" -ne 0 ]]; then
    cat "$log_file" >&2
  fi
  rm -r "$data_dir"
}
trap cleanup EXIT

dev_args=(-bind-address 127.0.0.1 -dex-port "$dex_port" -web-port "$web_port" -open=false -zd-data-dir "$data_dir/zd")
if [[ -n "${DEX_SERVER_ZD_BINARY:-}" ]]; then
  dev_args+=(-zd-binary "$DEX_SERVER_ZD_BINARY")
fi
"$dexcli" dev "${dev_args[@]}" >>"$log_file" 2>&1 &
dexcli_pid=$!

dex_ready=false
for _ in {1..240}; do
  if grep -q "Dex development environment is ready" "$log_file"; then
    dex_ready=true
    break
  fi
  if ! kill -0 "$dexcli_pid" 2>/dev/null; then
    echo "dexcli exited before Dex became ready" >&2
    exit 1
  fi
  sleep 0.25
done
if ! $dex_ready; then
  echo "Dex did not become ready" >&2
  exit 1
fi

cd "$script_dir"
echo "running main.ts on Node.js $(node --version)"
DEX_FLOW_SERVICE_ADDRESS="127.0.0.1:${dex_port}" node --experimental-transform-types main.ts
