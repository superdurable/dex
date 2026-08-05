#!/bin/bash

# Copyright (c) 2022-2026 Super Durable, Inc.
#
# Permission is hereby granted, free of charge, to any person obtaining a copy
# of this software and associated documentation files (the "Software"), to deal
# in the Software without restriction, including without limitation the rights
# to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
# copies of the Software, and to permit persons to whom the Software is
# furnished to do so, subject to the following conditions:
#
# The above copyright notice and this permission notice shall be included in
# all copies or substantial portions of the Software.
#
# THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
# IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
# FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
# AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
# LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
# OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
# THE SOFTWARE.

set -euo pipefail

dex_port="${DEX_EXAMPLES_DEX_PORT:-19801}"
web_port="${DEX_EXAMPLES_WEB_PORT:-19901}"
temporal_port="${DEX_EXAMPLES_TEMPORAL_PORT:-19233}"
temporal_ui_port="${DEX_EXAMPLES_TEMPORAL_UI_PORT:-19333}"
postgres_port="${DEX_EXAMPLES_POSTGRES_PORT:-19432}"
dex_address="127.0.0.1:${dex_port}"
temporal_address="127.0.0.1:${temporal_port}"
postgres_url="postgres://dataset_deal:dataset_deal@127.0.0.1:${postgres_port}/dataset_deal?sslmode=disable"
compose_project="dataset-deal-e2e-$$"
log_file="/tmp/test-go-examples-e2e-services.log"
test_dir=$(mktemp -d)
dexcli_pid=""

cleanup() {
  if [[ -n "$dexcli_pid" ]] && kill -0 "$dexcli_pid" 2>/dev/null; then
    kill -TERM "$dexcli_pid"
    wait "$dexcli_pid" || true
  fi
  DATASET_DEAL_POSTGRES_PORT="$postgres_port" docker compose \
    -p "$compose_project" \
    -f dataset-deal/docker-compose.yml \
    down --volumes >/dev/null 2>&1 || true
  rm -r "$test_dir"
}
trap cleanup EXIT

DATASET_DEAL_POSTGRES_PORT="$postgres_port" docker compose \
  -p "$compose_project" \
  -f dataset-deal/docker-compose.yml \
  up -d --wait

make -C ../../cli build
: >"$log_file"
../../cli/dexcli dev \
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

temporal --address "$temporal_address" operator search-attribute create --name CustomKeywordField --type Keyword

DEX_FLOW_SERVICE_ADDRESS="$dex_address" \
DEX_WORKER_HOST=127.0.0.1 \
DATASET_DEAL_POSTGRES_URL="$postgres_url" \
GOWORK=off \
GOCACHE="${GOCACHE:-/tmp/dex-examples-gocache}" \
GOMODCACHE="${GOMODCACHE:-/tmp/dex-examples-gomodcache}" \
  go test -count=1 -race -v ./integ
