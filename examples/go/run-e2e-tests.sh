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

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd "$script_dir/../.." && pwd)
dex_port="${DEX_EXAMPLES_DEX_PORT:-19801}"
web_port="${DEX_EXAMPLES_WEB_PORT:-19901}"
postgres_port="${DEX_EXAMPLES_POSTGRES_PORT:-19432}"
dex_address="127.0.0.1:${dex_port}"
postgres_url="postgres://dataset_deal:dataset_deal@127.0.0.1:${postgres_port}/dataset_deal?sslmode=disable"
compose_project="dataset-deal-e2e-$$"
entity_store_dir="$repo_root/examples/entity-store"
entity_store_project="entity-store-e2e-$$"
log_file="/tmp/test-go-examples-e2e-services.log"
test_dir=$(mktemp -d)
binary_dir=$(mktemp -d)
dexcli_pid=""
entity_store_started=false
dataset_deal_started=false
: >"$log_file"

cleanup() {
  status=$?
  if $keep_running; then
    if [[ "$status" -ne 0 ]]; then
      cat "$log_file" >&2
    fi
    return
  fi
  if [[ -n "$dexcli_pid" ]] && kill -0 "$dexcli_pid" 2>/dev/null; then
    kill -TERM "$dexcli_pid"
    wait "$dexcli_pid" || true
  fi
  if $dataset_deal_started; then
    if ! DATASET_DEAL_POSTGRES_PORT="$postgres_port" docker compose \
      -p "$compose_project" \
      -f "$script_dir/dataset-deal/docker-compose.yml" \
      down --volumes >>"$log_file" 2>&1; then
      echo "failed to stop the Dataset Deal database" >&2
    fi
  fi
  if $entity_store_started; then
    if ! docker compose -p "$entity_store_project" \
      -f "$entity_store_dir/docker-compose.yml" down --volumes >>"$log_file" 2>&1; then
      echo "failed to stop the Go examples entity store" >&2
    fi
  fi
  if [[ "$status" -ne 0 ]]; then
    cat "$log_file" >&2
  fi
  rm -r "$test_dir" "$binary_dir"
}
trap cleanup EXIT

docker compose -p "$entity_store_project" \
  -f "$entity_store_dir/docker-compose.yml" up --detach --wait
entity_store_started=true

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
  -attribute-store-config "$entity_store_dir/attribute-store.yaml" \
  -bind-address 127.0.0.1 \
  -dex-port "$dex_port" \
  -web-port "$web_port" \
  -open=false \
  -sqlite-db-filename "$test_dir/temporal.db" \
  >>"$log_file" 2>&1 &
dexcli_pid=$!

export DEXCLI_PATH="$binary_dir/dexcli"

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
common_test_env=(
  DEX_FLOW_SERVICE_ADDRESS="$dex_address"
  DEX_WORKER_HOST=127.0.0.1
  GOCACHE="${GOCACHE:-$test_dir/gocache}"
  GOMODCACHE="${GOMODCACHE:-/tmp/dex-examples-gomodcache}"
  GOWORK=off
)
integ_status=0
env "${common_test_env[@]}" \
  go test -count=1 -race -v ./integ ${test_args[@]+"${test_args[@]}"} || integ_status=$?

DATASET_DEAL_POSTGRES_PORT="$postgres_port" docker compose \
  -p "$compose_project" \
  -f "$script_dir/dataset-deal/docker-compose.yml" \
  up -d --wait
dataset_deal_started=true

dataset_deal_status=0
env "${common_test_env[@]}" \
DATASET_DEAL_POSTGRES_URL="$postgres_url" \
  go test -count=1 -race -v ./integ/datasetdeal ${test_args[@]+"${test_args[@]}"} || dataset_deal_status=$?

if [[ "$integ_status" -ne 0 || "$dataset_deal_status" -ne 0 ]]; then
  exit 1
fi

if $keep_running; then
  echo ""
  echo "Dex Web:  http://127.0.0.1:${web_port}"
  echo "dexcli:   --server ${dex_address}"
  echo "Press Ctrl+C to stop dexcli dev"
  wait "$dexcli_pid"
fi
