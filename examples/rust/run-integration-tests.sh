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

set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd "$script_dir/../.." && pwd)
dex_port="${DEX_RUST_EXAMPLES_DEX_PORT:-19804}"
web_port="${DEX_RUST_EXAMPLES_WEB_PORT:-19904}"
temporal_port="${DEX_RUST_EXAMPLES_TEMPORAL_PORT:-19236}"
temporal_ui_port="${DEX_RUST_EXAMPLES_TEMPORAL_UI_PORT:-19336}"
dex_address="127.0.0.1:${dex_port}"
log_file="/tmp/test-rust-examples-integration-services.log"
test_dir=$(mktemp -d)
binary_dir=$(mktemp -d)
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
  rm -r "$test_dir" "$binary_dir"
}
trap cleanup EXIT

if [[ ! -f "$repo_root/web/assets/dist/index.html" ]]; then
  (
    cd "$repo_root/web"
    npm ci
    npm run build
  )
fi

(
  cd "$repo_root/cli"
  GOWORK=off go build -trimpath -o "$binary_dir/dexcli" ./cmd/dexcli
)

"$binary_dir/dexcli" dev \
  -bind-address 127.0.0.1 \
  -dex-port "$dex_port" \
  -web-port "$web_port" \
  -temporal-port "$temporal_port" \
  -temporal-ui-port "$temporal_ui_port" \
  -temporal-db-filename "$test_dir/temporal.db" \
  >>"$log_file" 2>&1 &
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
DEX_SERVER_ADDRESS="$dex_address" make integTests
