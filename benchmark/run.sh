#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
mode="${1:-quick}"
case "$mode" in quick|prod) ;; *) echo "usage: $0 quick|prod" >&2; exit 2 ;; esac

duration="${DEXBENCH_DURATION:-60}"
warmup="${DEXBENCH_WARMUP:-10}"
concurrency="${DEXBENCH_CONCURRENCY:-16}"
workers="${DEXBENCH_WORKERS:-16}"
steps="${DEXBENCH_STEPS:-10}"
scenarios="${DEXBENCH_SCENARIOS:-start-complete,sequential-steps,async-steps,rpc,channel-wake}"
targets="${DEXBENCH_TARGETS:-temporal,zd}"
output_dir="${DEXBENCH_OUT:-$repo_root/benchmark/results/$(date -u +%Y%m%dT%H%M%SZ)-$mode}"
run_dir="$(mktemp -d)"
benchmark_binary="$repo_root/benchmark/bin/dexbench"
current_pid=""
compose_started=0

cleanup() {
  if [[ -n "$current_pid" ]]; then
    kill "$current_pid" 2>/dev/null || true
    wait "$current_pid" 2>/dev/null || true
  fi
  if [[ "$compose_started" == 1 ]]; then
    docker compose --env-file "$repo_root/server/docker-compose/.env" -f "$repo_root/server/docker-compose/integ-dependencies.yml" down -v >/dev/null 2>&1 || true
  fi
  rm -rf "$run_dir"
}
trap cleanup EXIT INT TERM

mkdir -p "$repo_root/benchmark/bin" "$output_dir"
(cd "$repo_root/benchmark" && GOWORK=off go build -o "$benchmark_binary" ./cmd/dexbench)
dexcli_binary="${DEXBENCH_DEXCLI_BINARY:-$(command -v dexcli || true)}"
zd_binary="${DEXBENCH_ZD_BINARY:-$(command -v dex-server-zd || true)}"
if [[ -z "$dexcli_binary" ]]; then echo "set DEXBENCH_DEXCLI_BINARY or install dexcli" >&2; exit 1; fi
if [[ "$targets" == *zd* && -z "$zd_binary" ]]; then echo "set DEXBENCH_ZD_BINARY or install dex-server-zd" >&2; exit 1; fi

wait_for_server() {
  local attempts
  for ((attempts=0; attempts<90; attempts++)); do
    if (echo > /dev/tcp/127.0.0.1/8801) >/dev/null 2>&1; then return 0; fi
    if ! kill -0 "$current_pid" 2>/dev/null; then echo "server exited during startup" >&2; return 1; fi
    sleep 1
  done
  echo "server did not listen on port 8801" >&2
  return 1
}

stop_server() {
  kill "$current_pid" 2>/dev/null || true
  wait "$current_pid" 2>/dev/null || true
  current_pid=""
}

run_target() {
  local target="$1"
  local server_pid="$current_pid"
  local metrics_argument=()
  local version="unknown"
  if [[ "$target" == zd ]]; then
    if [[ "$mode" == prod ]]; then server_pid="$current_pid"; else server_pid="$(pgrep -n -x dex-server-zd || true)"; fi
    metrics_argument=(--metrics-url http://127.0.0.1:9901/metrics)
    version="$($zd_binary --version 2>/dev/null || echo unknown)"
  else
    version="$($dexcli_binary --version 2>/dev/null || echo unknown)"
  fi
  local pid_argument=()
  if [[ -n "$server_pid" ]]; then pid_argument=(--server-pid "$server_pid"); fi
  DEXBENCH_SERVER_VERSION="$version" "$benchmark_binary" run \
    --address 127.0.0.1:8801 --target "$target" --engine "$target" \
    --duration "$duration" --warmup "$warmup" --concurrency "$concurrency" \
    --workers "$workers" --steps "$steps" --scenarios "$scenarios" \
    --out "$output_dir" "${pid_argument[@]}" "${metrics_argument[@]}"
}

if [[ "$mode" == prod ]]; then
  : "${DEXBENCH_S3_BUCKET:?set DEXBENCH_S3_BUCKET}"
  : "${DEXBENCH_S3_PREFIX:?set DEXBENCH_S3_PREFIX}"
  : "${DEXBENCH_S3_REGION:?set DEXBENCH_S3_REGION}"
  docker compose --env-file "$repo_root/server/docker-compose/.env" -f "$repo_root/server/docker-compose/integ-dependencies.yml" up -d postgresql elasticsearch temporal
  compose_started=1
  for ((attempts=0; attempts<90; attempts++)); do
    if (echo > /dev/tcp/127.0.0.1/7233) >/dev/null 2>&1; then break; fi
    sleep 1
  done
fi

IFS=, read -ra requested_targets <<< "$targets"
for target in "${requested_targets[@]}"; do
  target="${target//[[:space:]]/}"
  case "$target" in
    temporal)
      if [[ "$mode" == quick ]]; then
        "$dexcli_binary" dev --engine temporal --open=false --dex-port 8801 --web-port 8802 \
          --sqlite-db-filename "$run_dir/temporal.sqlite" --blob-store-dir "$run_dir/go-blobs" >"$output_dir/temporal-server.log" 2>&1 &
      else
        "$dexcli_binary" dev --engine temporal --open=false --dex-port 8801 --web-port 8802 \
          --external-temporal-address 127.0.0.1:7233 --blob-store-dir "$run_dir/go-blobs" >"$output_dir/temporal-server.log" 2>&1 &
      fi
      current_pid=$!
      ;;
    zd)
      if [[ "$mode" == quick ]]; then
        cat > "$run_dir/zd-quick.yaml" <<'YAML'
metrics:
  listen: "127.0.0.1:9901"
YAML
        "$dexcli_binary" dev --engine zd --open=false --dex-port 8801 --web-port 8802 \
          --zd-binary "$zd_binary" --zd-data-dir "$run_dir/zd-data" --zd-config "$run_dir/zd-quick.yaml" >"$output_dir/zd-server.log" 2>&1 &
      else
        cat > "$run_dir/zd-prod.yaml" <<YAML
cluster:
  nodeId: "benchmark-$(date -u +%s)"
objectStore:
  region: "$DEXBENCH_S3_REGION"
  bucket: "$DEXBENCH_S3_BUCKET"
  prefix: "$DEXBENCH_S3_PREFIX/$(date -u +%Y%m%dT%H%M%SZ)"
  credentialsSource: defaultChain
localStorage:
  directory: "$run_dir/zd-data"
api:
  listen: "127.0.0.1:8801"
metrics:
  listen: "127.0.0.1:9901"
YAML
        "$zd_binary" serve --config "$run_dir/zd-prod.yaml" >"$output_dir/zd-server.log" 2>&1 &
      fi
      current_pid=$!
      ;;
    *) echo "unknown target: $target" >&2; exit 2 ;;
  esac
  wait_for_server
  run_target "$target"
  stop_server
done

echo "results: $output_dir"
