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

# Cross-SDK payload encryption interoperability test.
#
# It starts one Dex server with dexcli dev, writes one key file with
# dexcli codec-server keygen, and starts a payload interop Worker for each of
# Go, Python, and TypeScript. Each SDK's Client test then starts and reads every
# Worker's Flow. It also starts the dexcli codec server and the three example
# codec servers; the Go test reads and writes through each of them.
#
# Install the example dependencies first:
#   (cd examples/python && uv sync --locked)
#   (cd examples/python/codec-server && uv sync --locked)
#   (cd examples/typescript && npm ci)
#   (cd examples/typescript/codec-server && npm ci)
#
# Set DEX_SERVER_ZD_BINARY to reuse a built dex-server-zd.

set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd "$script_dir/.." && pwd)
if [[ -n "${DEX_SERVER_ZD_BINARY:-}" && "$DEX_SERVER_ZD_BINARY" == */* ]]; then
  DEX_SERVER_ZD_BINARY="$(cd "$(dirname "$DEX_SERVER_ZD_BINARY")" && pwd)/$(basename "$DEX_SERVER_ZD_BINARY")"
fi
dex_port="${DEX_PAYLOAD_INTEROP_DEX_PORT:-19807}"
web_port="${DEX_PAYLOAD_INTEROP_WEB_PORT:-19907}"
codec_server_base_port="${DEX_PAYLOAD_INTEROP_CODEC_SERVER_BASE_PORT:-19820}"
dex_address="127.0.0.1:${dex_port}"
allowed_origin="http://127.0.0.1:${web_port}"
log_file="/tmp/test-payload-encryption-interop-services.log"
# The work directory holds the key file and bearer tokens; cleanup removes it.
work_dir=$(mktemp -d /tmp/dex-payload-interop.XXXXXX)
log_dir="$work_dir/logs"
mkdir -p "$log_dir" "$work_dir/bin" "$work_dir/tmp"
export TMPDIR="$work_dir/tmp"
background_pids=()
: >"$log_file"

cleanup() {
  status=$?
  for pid in "${background_pids[@]+"${background_pids[@]}"}"; do
    if kill -0 "$pid" 2>/dev/null; then
      kill -TERM "$pid" 2>/dev/null || true
    fi
  done
  for pid in "${background_pids[@]+"${background_pids[@]}"}"; do
    wait "$pid" 2>/dev/null || true
  done
  # The dexcli codec server prints its token; the token dies with the process, but keep it out of logs.
  for service_log in "$log_dir"/*.log; do
    [[ -f "$service_log" ]] || continue
    echo "===== $(basename "$service_log")" >>"$log_file"
    grep -v '^Token:' "$service_log" >>"$log_file" || true
  done
  if [[ "$status" -ne 0 ]]; then
    tail -n 200 "$log_file" >&2
  fi
  rm -rf "$work_dir"
}
trap cleanup EXIT

require_installed() {
  local path="$1" install_hint="$2"
  if [[ ! -e "$path" ]]; then
    echo "missing $path; run: $install_hint" >&2
    exit 2
  fi
}

require_installed "$script_dir/python/.venv" "(cd examples/python && uv sync --locked)"
require_installed "$script_dir/python/codec-server/.venv" "(cd examples/python/codec-server && uv sync --locked)"
require_installed "$script_dir/typescript/node_modules" "(cd examples/typescript && npm ci)"
require_installed "$script_dir/typescript/codec-server/node_modules" "(cd examples/typescript/codec-server && npm ci)"

# start_background NAME COMMAND... runs COMMAND with its output in $log_dir/NAME.log.
start_background() {
  local name="$1"
  shift
  "$@" >"$log_dir/$name.log" 2>&1 &
  background_pids+=("$!")
  last_background_pid=$!
}

# wait_for_log_line PID NAME TEXT waits up to 120 seconds for TEXT in NAME's log.
wait_for_log_line() {
  local pid="$1" name="$2" text="$3"
  for _ in {1..480}; do
    if grep -qF "$text" "$log_dir/$name.log"; then
      return 0
    fi
    if ! kill -0 "$pid" 2>/dev/null; then
      echo "$name exited before it logged: $text" >&2
      return 1
    fi
    sleep 0.25
  done
  echo "$name did not log within 120 seconds: $text" >&2
  return 1
}

# wait_for_codec_server PID NAME URL waits until an unauthenticated /decode returns 401.
wait_for_codec_server() {
  local pid="$1" name="$2" url="$3" status_code
  for _ in {1..480}; do
    status_code=$(curl -s -o /dev/null -w '%{http_code}' -X POST \
      -H 'Content-Type: application/json' -d '{}' "$url/decode" || true)
    if [[ "$status_code" == 401 ]]; then
      return 0
    fi
    if ! kill -0 "$pid" 2>/dev/null; then
      echo "$name exited before it served requests" >&2
      return 1
    fi
    sleep 0.25
  done
  echo "$name did not serve requests within 120 seconds" >&2
  return 1
}

echo "Building dexcli, the Go interop Worker, the Go codec server, and the TypeScript examples"
if [[ ! -f "$repo_root/web/assets/dist/index.html" ]]; then
  (
    cd "$repo_root/web"
    npm ci
    npm run build
  )
fi
(cd "$repo_root/cli" && GOWORK=off go build -trimpath -o "$work_dir/bin/dexcli" ./cmd/dexcli)
(cd "$script_dir/go" && GOWORK=off go build -o "$work_dir/bin/go-interop-worker" ./interop/payloadencryption/worker)
(cd "$script_dir/go/codec-server" && GOWORK=off go build -o "$work_dir/bin/go-codec-server" .)
(cd "$script_dir/typescript" && npx tsc -p tsconfig.json)
if [[ -z "${DEX_SERVER_ZD_BINARY:-}" ]]; then
  (cd "$repo_root" && cargo build --release --locked --manifest-path server-zd/Cargo.toml -p dexzd-server --bin dex-server-zd)
  DEX_SERVER_ZD_BINARY="$repo_root/server-zd/target/release/dex-server-zd"
fi

echo "Starting Dex on $dex_address"
printf 'dev:\n  durabilityMode: processCrashSafe\n' >"$work_dir/zd-overrides.yaml"
start_background dexcli-dev "$work_dir/bin/dexcli" dev \
  -engine zd \
  -zd-binary "$DEX_SERVER_ZD_BINARY" \
  -zd-data-dir "$work_dir/zd" \
  -zd-config "$work_dir/zd-overrides.yaml" \
  -bind-address 127.0.0.1 \
  -dex-port "$dex_port" \
  -web-port "$web_port" \
  -open=false
wait_for_log_line "$last_background_pid" dexcli-dev "Dex development environment is ready"

key_file="$work_dir/payload-keys.json"
"$work_dir/bin/dexcli" codec-server keygen -o "$key_file" >/dev/null
bearer_token_file="$work_dir/codec-server-bearer-token"
(umask 077 && openssl rand -hex 32 >"$bearer_token_file")

worker_env=(
  DEX_FLOW_SERVICE_ADDRESS="$dex_address"
  DEX_PAYLOAD_INTEROP_KEY_FILE="$key_file"
)
echo "Starting the Go, Python, and TypeScript interop Workers"
mkdir -p "$work_dir/go-worker-cache" "$work_dir/python-worker-cache" "$work_dir/typescript-worker-cache"
start_background go-worker env "${worker_env[@]}" DEX_PAYLOAD_INTEROP_CACHE_DIR="$work_dir/go-worker-cache" \
  "$work_dir/bin/go-interop-worker"
go_worker_pid=$last_background_pid
start_background python-worker env "${worker_env[@]}" DEX_PAYLOAD_INTEROP_CACHE_DIR="$work_dir/python-worker-cache" \
  bash -c 'cd "$1" && exec .venv/bin/python -m tests.interop.payload_encryption_interop_worker' _ "$script_dir/python"
python_worker_pid=$last_background_pid
start_background typescript-worker env "${worker_env[@]}" DEX_PAYLOAD_INTEROP_CACHE_DIR="$work_dir/typescript-worker-cache" \
  node "$script_dir/typescript/dist/test/interop/payload-encryption-interop-worker.js"
typescript_worker_pid=$last_background_pid

echo "Starting the dexcli codec server and the Go, Python, and TypeScript example codec servers"
dexcli_codec_port=$((codec_server_base_port + 1))
go_codec_port=$((codec_server_base_port + 2))
python_codec_port=$((codec_server_base_port + 3))
typescript_codec_port=$((codec_server_base_port + 4))
dexcli_token_file="$work_dir/dexcli-codec-server-token"
start_background dexcli-codec-server "$work_dir/bin/dexcli" codec-server \
  -key-file "$key_file" \
  -port "$dexcli_codec_port" \
  -token-file "$dexcli_token_file" \
  -web-origin "$allowed_origin"
dexcli_codec_pid=$last_background_pid
example_codec_env=(
  DEX_CODEC_SERVER_KEY_FILE="$key_file"
  DEX_CODEC_SERVER_ALLOWED_ORIGINS="$allowed_origin"
  DEX_CODEC_SERVER_AUTH_MODE=bearer-token
  DEX_CODEC_SERVER_BEARER_TOKEN_FILE="$bearer_token_file"
)
start_background go-codec-server env "${example_codec_env[@]}" \
  DEX_CODEC_SERVER_LISTEN_ADDRESS="127.0.0.1:$go_codec_port" "$work_dir/bin/go-codec-server"
go_codec_pid=$last_background_pid
start_background python-codec-server env "${example_codec_env[@]}" \
  DEX_CODEC_SERVER_LISTEN_ADDRESS="127.0.0.1:$python_codec_port" \
  bash -c 'cd "$1" && exec .venv/bin/python -m dex_codec_server' _ "$script_dir/python/codec-server"
python_codec_pid=$last_background_pid
start_background typescript-codec-server env "${example_codec_env[@]}" \
  DEX_CODEC_SERVER_LISTEN_ADDRESS="127.0.0.1:$typescript_codec_port" \
  bash -c 'cd "$1" && exec node src/main.ts' _ "$script_dir/typescript/codec-server"
typescript_codec_pid=$last_background_pid

wait_for_log_line "$go_worker_pid" go-worker "payload interop worker ready: go"
wait_for_log_line "$python_worker_pid" python-worker "payload interop worker ready: python"
wait_for_log_line "$typescript_worker_pid" typescript-worker "payload interop worker ready: typescript"
wait_for_codec_server "$dexcli_codec_pid" dexcli-codec-server "http://127.0.0.1:$dexcli_codec_port"
wait_for_codec_server "$go_codec_pid" go-codec-server "http://127.0.0.1:$go_codec_port"
wait_for_codec_server "$python_codec_pid" python-codec-server "http://127.0.0.1:$python_codec_port"
wait_for_codec_server "$typescript_codec_pid" typescript-codec-server "http://127.0.0.1:$typescript_codec_port"

dexcli_token=$(tr -d '\n' <"$dexcli_token_file")
bearer_token=$(tr -d '\n' <"$bearer_token_file")
codec_server_entry() {
  printf '{"name":"%s","url":"http://127.0.0.1:%s","token":"%s","allowed_origin":"%s"}' "$1" "$2" "$3" "$allowed_origin"
}
codec_servers="[$(codec_server_entry dexcli "$dexcli_codec_port" "$dexcli_token"),$(
  codec_server_entry go-example "$go_codec_port" "$bearer_token"),$(
  codec_server_entry python-example "$python_codec_port" "$bearer_token"),$(
  codec_server_entry typescript-example "$typescript_codec_port" "$bearer_token")]"

test_env=(
  DEX_FLOW_SERVICE_ADDRESS="$dex_address"
  DEX_PAYLOAD_INTEROP_KEY_FILE="$key_file"
  DEX_PAYLOAD_INTEROP_CODEC_SERVERS="$codec_servers"
)
failed_suites=()
echo "Running the Go Client and codec server tests"
(cd "$script_dir/go" && env "${test_env[@]}" GOWORK=off go test -count=1 -race -v ./interop/payloadencryption/) ||
  failed_suites+=("Go")
echo "Running the Python Client tests"
(cd "$script_dir/python" && env "${test_env[@]}" .venv/bin/python -m pytest -vv -p no:cacheprovider tests/interop) ||
  failed_suites+=("Python")
echo "Running the TypeScript Client tests"
(cd "$script_dir/typescript" && env "${test_env[@]}" \
  node --test --test-force-exit dist/test/interop/payload-encryption-interop.test.js) ||
  failed_suites+=("TypeScript")

if ((${#failed_suites[@]} > 0)); then
  echo "Payload encryption interop tests failed: ${failed_suites[*]}" >&2
  exit 1
fi
echo "Payload encryption interop tests passed"
