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
# Extra dexbench run flags for the slot stream scenarios, such as "-flow-types 4 -probe-rate 20".
read -ra slot_args <<< "${DEXBENCH_SLOT_ARGS:-}"
# idle-slots restarts the server after its probe unless this is 0.
slot_restart="${DEXBENCH_SLOT_RESTART:-1}"
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

is_slot_variant() {
  case "$1" in idle-slots-*|burst-*|fairness|periodic|backlog-one-partition-*) return 0 ;; esac
  return 1
}

# supervise_server runs dexcli dev and restarts it on SIGHUP with the same data directory.
# Its PID is the sampling root, so CPU and RSS sampling follows the server across a restart.
supervise_server() {
  local state_dir="$1" child="" restart_requested=0 status=0
  trap 'restart_requested=1; [[ -n "$child" ]] && kill -TERM "$child" 2>/dev/null' HUP
  trap '[[ -n "$child" ]] && kill -TERM "$child" 2>/dev/null; wait "$child" 2>/dev/null; exit 0' TERM INT
  while :; do
    nice -n 10 "$dexcli_binary" dev --engine zd --open=false --dex-port 8801 --web-port 8802 \
      --zd-binary "$zd_binary" --zd-data-dir "$state_dir/zd-data" --zd-config "$state_dir/zd-quick.yaml" >>"$state_dir/server.log" 2>&1 &
    child=$!
    # A trapped signal ends wait early, so keep waiting until the child is gone.
    while :; do
      wait "$child" && status=0 || status=$?
      kill -0 "$child" 2>/dev/null || break
    done
    if (( restart_requested )); then
      restart_requested=0
      echo "== restart requested" >>"$state_dir/server.log"
      continue
    fi
    exit "$status"
  done
}

# Every scenario variant and concurrency step gets a newly started server with empty state.
start_server() {
  local state_dir="$1" variant="$2"
  mkdir -p "$state_dir"
  {
    printf 'metrics:\n  listen: "127.0.0.1:9901"\n'
    # dexzd_worker_stream_events is a verbose family.
    if is_slot_variant "$variant"; then printf '  level: verbose\n'; fi
    printf 'dev:\n  durabilityMode: processCrashSafe\n'
    # Flow IDs cannot be pinned without the cluster's secret hash key, so the backlog runs on one partition.
    if [[ "$variant" == backlog-one-partition-* ]]; then printf 'cluster:\n  partitionCount: 1\n'; fi
  } > "$state_dir/zd-quick.yaml"
  supervise_server "$state_dir" &
  current_pid=$!
  if ! "$benchmark_binary" wait-ready --pid "$current_pid" --timeout 180s; then
    tail -50 "$state_dir/server.log" >&2
    return 1
  fi
}

run_step() {
  local variant="$1" step_concurrency="$2" variant_args=()
  wait_for_load
  if is_slot_variant "$variant"; then
    variant_args=(${slot_args[@]+"${slot_args[@]}"})
    if [[ "$variant" == idle-slots-* && "$slot_restart" != 0 ]]; then
      variant_args+=(--restart-command "kill -HUP $current_pid")
    fi
  fi
  # The supervisor PID is the sampling root, so its children (dexcli, server-zd, s3local) are included.
  DEXBENCH_SERVER_VERSION="$server_version" nice -n 10 "$benchmark_binary" run \
    --address 127.0.0.1:8801 --target zd --engine zd \
    --duration "$duration" --warmup "$warmup" --concurrency "$step_concurrency" \
    --workers "$workers" --connections "$connections" --steps "$steps" --scenarios "$variant" --mode quick \
    --out "$output_dir" --server-pid "$current_pid" --metrics-url http://127.0.0.1:9901/metrics \
    ${variant_args[@]+"${variant_args[@]}"}
}

IFS=, read -ra requested_concurrency <<< "$concurrency"
for variant in $variant_list; do
  for step_concurrency in "${requested_concurrency[@]}"; do
    step_dir="$run_dir/$variant-c$step_concurrency"
    wait_for_load
    start_server "$step_dir" "$variant"
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
