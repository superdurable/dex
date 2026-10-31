#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
mode="${1:-quick}"
if [[ "$mode" != quick ]]; then echo "usage: $0 [quick]" >&2; exit 2; fi

max_load="${DEXBENCH_MAX_LOAD:-$(getconf _NPROCESSORS_ONLN)}"
wait_for_load() {
  local load
  while :; do
    load="$(uptime | sed -E 's/.*load averages?: ([0-9.]+).*/\1/')"
    if awk -v current_load="$load" -v limit="$max_load" 'BEGIN { exit !(current_load < limit) }'; then return; fi
    echo "waiting for 1-minute load below $max_load (now $load)" >&2
    sleep 15
  done
}

# Quick defaults take about 19 minutes; set DEXBENCH_DURATION=180 DEXBENCH_WARMUP=30 DEXBENCH_CONCURRENCY=1,8,32,128 for the full sweep.
duration="${DEXBENCH_DURATION:-60}"
warmup="${DEXBENCH_WARMUP:-15}"
concurrency="${DEXBENCH_CONCURRENCY:-1,32}"
workers="${DEXBENCH_WORKERS:-0}"
connections="${DEXBENCH_CONNECTIONS:-1}"
steps="${DEXBENCH_STEPS:-10}"
scenarios="${DEXBENCH_SCENARIOS:-start-complete,sequential-steps,sequential-steps-async,rpc,attribute-read,channel-wake}"
# Output defaults to a new directory outside the repository; committed result sets set DEXBENCH_OUT.
output_dir="${DEXBENCH_OUT:-$(mktemp -d "${TMPDIR:-/tmp}/dexbench-XXXXXX")}"
run_dir="$(mktemp -d)"
benchmark_binary="$repo_root/benchmark/bin/dexbench"
current_pid=""

cleanup() {
  if [[ -n "$current_pid" ]]; then
    kill "$current_pid" 2>/dev/null || true
    wait "$current_pid" 2>/dev/null || true
  fi
  rm -rf "$run_dir"
}
trap cleanup EXIT INT TERM

mkdir -p "$repo_root/benchmark/bin" "$output_dir"
if compgen -G "$output_dir/*.json" >/dev/null; then
  echo "result directory already contains JSON; set DEXBENCH_OUT for a new run" >&2
  exit 1
fi
wait_for_load
(cd "$repo_root/benchmark/dexbench" && nice -n 10 env GOWORK=off go build -o "$benchmark_binary" ./cmd/dexbench)
dexcli_binary="${DEXBENCH_DEXCLI_BINARY:-$(command -v dexcli || true)}"
zd_binary="${DEXBENCH_ZD_BINARY:-$(command -v dex-server-zd || true)}"
if [[ -z "$dexcli_binary" ]]; then echo "set DEXBENCH_DEXCLI_BINARY or install dexcli" >&2; exit 1; fi
if [[ -z "$zd_binary" ]]; then echo "set DEXBENCH_ZD_BINARY or install dex-server-zd" >&2; exit 1; fi
dexcli_help="$("$dexcli_binary" dev --help 2>&1)"
if [[ "$dexcli_help" != *-engine* ]]; then echo "dexcli must support dev --engine; build this checkout's cli/cmd/dexcli" >&2; exit 1; fi
variant_list="$("$benchmark_binary" variants --scenarios "$scenarios")"

cpu_model="$(sysctl -n machdep.cpu.brand_string 2>/dev/null || awk -F: '/model name/{print $2; exit}' /proc/cpuinfo 2>/dev/null || true)"
cpu_count="$(getconf _NPROCESSORS_ONLN)"
ram_bytes="$(sysctl -n hw.memsize 2>/dev/null || awk '/MemTotal/{print $2 * 1024; exit}' /proc/meminfo 2>/dev/null || true)"
free_disk_kb="$(df -Pk "$output_dir" | awk 'NR==2 {print $4}')"
printf 'cpu_model=%s\ncpu_count=%s\nram_bytes=%s\nfree_disk_kb=%s\n' "$cpu_model" "$cpu_count" "$ram_bytes" "$free_disk_kb" > "$output_dir/host.txt"
DEXBENCH_GIT_COMMIT="$(git -C "$repo_root" rev-parse HEAD)"
export DEXBENCH_GIT_COMMIT
export DEXBENCH_DISK_PATH="$output_dir"
server_version="$("$zd_binary" --version 2>/dev/null || echo unknown)"

stop_server() {
  kill "$current_pid" 2>/dev/null || true
  wait "$current_pid" 2>/dev/null || true
  current_pid=""
}

# Every scenario variant and concurrency step gets a newly started server with empty state.
start_server() {
  local state_dir="$1"
  mkdir -p "$state_dir"
  cat > "$state_dir/zd-quick.yaml" <<'YAML'
metrics:
  listen: "127.0.0.1:9901"
YAML
  nice -n 10 "$dexcli_binary" dev --engine zd --open=false --dex-port 8801 --web-port 8802 \
    --zd-binary "$zd_binary" --zd-data-dir "$state_dir/zd-data" --zd-config "$state_dir/zd-quick.yaml" >"$state_dir/server.log" 2>&1 &
  current_pid=$!
  if ! "$benchmark_binary" wait-ready --pid "$current_pid" --timeout 180s; then
    tail -50 "$state_dir/server.log" >&2
    return 1
  fi
}

run_step() {
  local variant="$1" step_concurrency="$2"
  wait_for_load
  # The dexcli PID is the sampling root, so its children (server-zd, s3mock) are included.
  DEXBENCH_SERVER_VERSION="$server_version" nice -n 10 "$benchmark_binary" run \
    --address 127.0.0.1:8801 --target zd --engine zd \
    --duration "$duration" --warmup "$warmup" --concurrency "$step_concurrency" \
    --workers "$workers" --connections "$connections" --steps "$steps" --scenarios "$variant" --mode quick \
    --out "$output_dir" --server-pid "$current_pid" --metrics-url http://127.0.0.1:9901/metrics
}

IFS=, read -ra requested_concurrency <<< "$concurrency"
for variant in $variant_list; do
  for step_concurrency in "${requested_concurrency[@]}"; do
    step_dir="$run_dir/$variant-c$step_concurrency"
    wait_for_load
    start_server "$step_dir"
    run_step "$variant" "$step_concurrency"
    stop_server
    rm -rf "$step_dir"
  done
done

if compgen -G "$output_dir/*.json" >/dev/null; then
  "$benchmark_binary" report "$output_dir"/*.json > "$output_dir/report.md"
  "$benchmark_binary" chart "$output_dir"/*.json > "$output_dir/charts.html"
else
  printf 'No scenarios produced results.\n' > "$output_dir/report.md"
fi
echo "results: $output_dir"
