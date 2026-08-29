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

keep_running=false
test_args=()
for arg in "$@"; do
  case "$arg" in
    --keep-running)
      keep_running=true
      ;;
    *)
      test_args+=("$arg")
      ;;
  esac
done

dex_port="${DEX_EXAMPLES_DEX_PORT:-19811}"
web_port="${DEX_EXAMPLES_WEB_PORT:-19911}"
dex_address="127.0.0.1:${dex_port}"
log_file="/tmp/test-python-examples-e2e-services.log"
test_log="/tmp/test-python-examples-e2e.log"
test_dir=$(mktemp -d)
dexcli_pid=""
script_dir=$(cd "$(dirname "$0")" && pwd)
repo_root=$(cd "$script_dir/../.." && pwd)
entity_store_dir="$repo_root/examples/entity-store"
compose_project="dex-python-examples-e2e-$$"
entity_store_started=false

cleanup() {
  status=$?
  if $keep_running; then
    if [[ "$status" -ne 0 ]]; then
      cat "$log_file" >&2
    fi
    exit "$status"
  fi
  if [[ -n "$dexcli_pid" ]] && kill -0 "$dexcli_pid" 2>/dev/null; then
    kill -TERM "$dexcli_pid"
    wait "$dexcli_pid" || true
  fi
  if $entity_store_started; then
    if ! docker compose -p "$compose_project" \
      -f "$entity_store_dir/docker-compose.yml" down --volumes >>"$log_file" 2>&1; then
      echo "failed to stop the Python examples entity store" >&2
    fi
  fi
  rm -r "$test_dir"
  exit "$status"
}
trap cleanup EXIT

make -C "$script_dir/../../cli" build
: >"$log_file"
docker compose -p "$compose_project" \
  -f "$entity_store_dir/docker-compose.yml" up --detach --wait
entity_store_started=true
"$script_dir/../../cli/dexcli" dev \
  -attribute-store-config "$entity_store_dir/attribute-store.yaml" \
  -bind-address 127.0.0.1 \
  -dex-port "$dex_port" \
  -web-port "$web_port" \
  -open=false \
  -sqlite-db-filename "$test_dir/temporal.db" \
  >>"$log_file" 2>&1 &
dexcli_pid=$!

cd "$script_dir"
uv sync --locked
export DEXCLI_PATH="$script_dir/../../cli/dexcli"
if ((${#test_args[@]})); then
  DEX_FLOW_SERVICE_ADDRESS="$dex_address" \
    uv run --frozen pytest tests/unit tests/integ sync-python/sync_tests/integ -v "${test_args[@]}" 2>&1 | tee "$test_log"
else
  DEX_FLOW_SERVICE_ADDRESS="$dex_address" \
    uv run --frozen pytest tests/unit tests/integ sync-python/sync_tests/integ -v 2>&1 | tee "$test_log"
fi

if $keep_running; then
  echo ""
  echo "Dex Web:  http://127.0.0.1:${web_port}"
  echo "dexcli:   --server ${dex_address}"
  echo "Press Ctrl+C to stop dexcli dev"
  wait "$dexcli_pid"
fi
