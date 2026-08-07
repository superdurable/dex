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

dex_port="${DEX_EXAMPLES_DEX_PORT:-19811}"
web_port="${DEX_EXAMPLES_WEB_PORT:-19911}"
temporal_port="${DEX_EXAMPLES_TEMPORAL_PORT:-19243}"
temporal_ui_port="${DEX_EXAMPLES_TEMPORAL_UI_PORT:-19343}"
dex_address="127.0.0.1:${dex_port}"
temporal_address="127.0.0.1:${temporal_port}"
log_file="/tmp/test-python-examples-e2e-services.log"
test_log="/tmp/test-python-examples-e2e.log"
test_dir=$(mktemp -d)
dexcli_pid=""
script_dir=$(cd "$(dirname "$0")" && pwd)

cleanup() {
  status=$?
  if [[ -n "$dexcli_pid" ]] && kill -0 "$dexcli_pid" 2>/dev/null; then
    kill -TERM "$dexcli_pid"
    wait "$dexcli_pid" || true
  fi
  rm -r "$test_dir"
  exit "$status"
}
trap cleanup EXIT

make -C "$script_dir/../../cli" build
: >"$log_file"
"$script_dir/../../cli/dexcli" dev \
  -bind-address 127.0.0.1 \
  -dex-port "$dex_port" \
  -web-port "$web_port" \
  -temporal-port "$temporal_port" \
  -temporal-ui-port "$temporal_ui_port" \
  -temporal-db-filename "$test_dir/temporal.db" \
  >>"$log_file" 2>&1 &
dexcli_pid=$!

temporal_ready=false
for _ in {1..240}; do
  if temporal --address "$temporal_address" operator search-attribute list >/dev/null 2>&1; then
    temporal_ready=true
    break
  fi
  if ! kill -0 "$dexcli_pid" 2>/dev/null; then
    cat "$log_file" >&2
    echo "dexcli exited before Temporal became ready" >&2
    exit 1
  fi
  sleep 0.25
done
if ! $temporal_ready; then
  cat "$log_file" >&2
  echo "Temporal did not become ready" >&2
  exit 1
fi

temporal --address "$temporal_address" operator search-attribute create \
  --name ActiveStepTypes --type KeywordList || true
temporal --address "$temporal_address" operator search-attribute create \
  --name CustomKeywordField --type Keyword || true
temporal --address "$temporal_address" operator search-attribute create \
  --name CustomStringField --type Text || true

cd "$script_dir"
uv sync --locked
DEX_FLOW_SERVICE_ADDRESS="$dex_address" \
  uv run --frozen pytest tests/unit tests/integ -v 2>&1 | tee "$test_log"
