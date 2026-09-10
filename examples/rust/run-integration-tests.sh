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

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd "$script_dir/../.." && pwd)
entity_store_dir="$repo_root/examples/entity-store"
compose_project="dex-rust-examples-$$"
dex_port="${DEX_RUST_EXAMPLES_DEX_PORT:-19804}"
web_port="${DEX_RUST_EXAMPLES_WEB_PORT:-19904}"
dex_address="127.0.0.1:${dex_port}"
log_file="/tmp/test-rust-examples-integration-services.log"
test_dir=$(mktemp -d)
binary_dir=$(mktemp -d)
dexcli_pid=""
entity_store_started=false
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
  if $entity_store_started; then
    if ! docker compose -p "$compose_project" \
      -f "$entity_store_dir/docker-compose.yml" down --volumes >>"$log_file" 2>&1; then
      echo "failed to stop the Rust examples entity store" >&2
    fi
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

docker compose -p "$compose_project" \
  -f "$entity_store_dir/docker-compose.yml" up --detach --wait
entity_store_started=true

"$binary_dir/dexcli" dev \
  -attribute-store-config "$entity_store_dir/attribute-store.yaml" \
  -bind-address 127.0.0.1 \
  -dex-port "$dex_port" \
  -web-port "$web_port" \
  -open=false \
  -sqlite-db-filename "$test_dir/temporal.db" \
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
export DEXCLI_PATH="$binary_dir/dexcli"
DEX_SERVER_ADDRESS="$dex_address" \
  cargo test --manifest-path current-sdk/Cargo.toml --locked --test dex_integration -- --ignored --test-threads=1
DEX_SERVER_ADDRESS="$dex_address" \
  cargo test --manifest-path current-sdk/Cargo.toml --locked --test flow_smoke -- --ignored --test-threads=1

if $keep_running; then
  echo ""
  echo "Dex Web:  http://127.0.0.1:${web_port}"
  echo "dexcli:   --server ${dex_address}"
  echo "Press Ctrl+C to stop dexcli dev"
  wait "$dexcli_pid"
fi
