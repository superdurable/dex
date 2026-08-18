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

postgres_port="${DATASET_DEAL_POSTGRES_PORT:-15432}"
dex_port="${DATASET_DEAL_DEX_PORT:-20801}"
dex_web_port="${DATASET_DEAL_DEX_WEB_PORT:-20802}"
worker_port="${DATASET_DEAL_WORKER_PORT:-20803}"
api_port="${DATASET_DEAL_API_PORT:-20804}"
dex_address="127.0.0.1:${dex_port}"
api_address="127.0.0.1:${api_port}"
postgres_url="postgres://dataset_deal:dataset_deal@127.0.0.1:${postgres_port}/dataset_deal?sslmode=disable"
compose_project="dataset-deal-demo-$$"
test_dir=$(mktemp -d)
dex_log="/tmp/dataset-deal-dex.log"
app_log="/tmp/dataset-deal-app.log"
dexcli_pid=""
app_pid=""

cleanup() {
  if [[ -n "$app_pid" ]] && kill -0 "$app_pid" 2>/dev/null; then
    kill -TERM "$app_pid"
    wait "$app_pid" || true
  fi
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

for command_name in docker temporal curl jq; do
  if ! command -v "$command_name" >/dev/null; then
    echo "$command_name is required" >&2
    exit 1
  fi
done

DATASET_DEAL_POSTGRES_PORT="$postgres_port" docker compose \
  -p "$compose_project" \
  -f dataset-deal/docker-compose.yml \
  up -d --wait

make -C ../../cli build
make bins
: >"$dex_log"
: >"$app_log"
../../cli/dexcli dev \
  -bind-address 127.0.0.1 \
  -dex-port "$dex_port" \
  -web-port "$dex_web_port" \
  -open=false \
  -sqlite-db-filename "$test_dir/temporal.db" \
  >>"$dex_log" 2>&1 &
dexcli_pid=$!

DATASET_DEAL_POSTGRES_URL="$postgres_url" \
DEX_FLOW_SERVICE_ADDRESS="$dex_address" \
DEX_WORKER_BIND_ADDRESS="127.0.0.1:${worker_port}" \
DEX_WORKER_TARGET="127.0.0.1:${worker_port}" \
DEX_EXAMPLES_HTTP_ADDRESS="$api_address" \
DEX_BLOB_CACHE_DIR="$test_dir/blob-cache" \
  ./dex-samples >>"$app_log" 2>&1 &
app_pid=$!

api_ready=false
for _ in {1..240}; do
  if curl --fail --silent "http://${api_address}/api/dataset-deal/actions" >/dev/null; then
    api_ready=true
    break
  fi
  if ! kill -0 "$app_pid" 2>/dev/null; then
    cat "$app_log" >&2
    echo "Go examples server exited before becoming ready" >&2
    exit 1
  fi
  sleep 0.25
done
if ! $api_ready; then
  cat "$app_log" >&2
  echo "Go examples API did not become ready" >&2
  exit 1
fi

tables=$(DATASET_DEAL_POSTGRES_PORT="$postgres_port" docker compose \
  -p "$compose_project" \
  -f dataset-deal/docker-compose.yml \
  exec -T postgres psql -U dataset_deal -d dataset_deal -Atc \
  "SELECT tablename FROM pg_tables WHERE schemaname = 'public' ORDER BY tablename")
if [[ "$tables" != "dataset_deal_processes" ]]; then
  echo "unexpected Dataset Deal tables: ${tables}" >&2
  exit 1
fi

DATASET_DEAL_API_URL="http://${api_address}" ./trigger-dataset-deal-demo.sh

echo "  Dex UI: http://127.0.0.1:${dex_web_port}"
echo "  logs:   ${dex_log}, ${app_log}"
if [[ "${KEEP_DATASET_DEAL_DEMO:-0}" == "1" ]]; then
  echo "Services remain running. Press Ctrl+C to stop and clean up."
  wait "$app_pid"
fi
