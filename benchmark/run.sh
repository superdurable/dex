#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
mode="${1:-quick}"
if [[ "$mode" == prod ]]; then exec "$repo_root/benchmark/prod/run-prod.sh"; fi
if [[ "$mode" != quick ]]; then echo "usage: $0 quick|prod" >&2; exit 2; fi

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

# Quick defaults target about one hour; set DEXBENCH_DURATION=180 DEXBENCH_WARMUP=30 DEXBENCH_CONCURRENCY=1,8,32,128 for the full sweep.
duration="${DEXBENCH_DURATION:-60}"
warmup="${DEXBENCH_WARMUP:-15}"
concurrency="${DEXBENCH_CONCURRENCY:-1,32}"
workers="${DEXBENCH_WORKERS:-0}"
steps="${DEXBENCH_STEPS:-10}"
scenarios="${DEXBENCH_SCENARIOS:-start-complete,sequential-steps,async-steps,rpc,attribute-read,channel-wake,attribute-write,recovery}"
targets="${DEXBENCH_TARGETS:-temporal,zd}"
output_dir="${DEXBENCH_OUT:-$repo_root/benchmark/results/$(date -u +%Y-%m-%d)-$mode-${DEXBENCH_HOST_LABEL:-$(uname -s | tr '[:upper:]' '[:lower:]')-$(uname -m)}}"
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
(cd "$repo_root/benchmark" && nice -n 10 env GOWORK=off go build -o "$benchmark_binary" ./cmd/dexbench)
dexcli_binary="${DEXBENCH_DEXCLI_BINARY:-$(command -v dexcli || true)}"
zd_binary="${DEXBENCH_ZD_BINARY:-$(command -v dex-server-zd || true)}"
if [[ -z "$dexcli_binary" ]]; then echo "set DEXBENCH_DEXCLI_BINARY or install dexcli" >&2; exit 1; fi
if [[ "$targets" == *zd* && -z "$zd_binary" ]]; then echo "set DEXBENCH_ZD_BINARY or install dex-server-zd" >&2; exit 1; fi
dexcli_help="$("$dexcli_binary" dev --help 2>&1)"
if [[ "$dexcli_help" != *-engine* ]]; then echo "dexcli must support dev --engine; build this checkout's cli/cmd/dexcli" >&2; exit 1; fi

cpu_model="$(sysctl -n machdep.cpu.brand_string 2>/dev/null || awk -F: '/model name/{print $2; exit}' /proc/cpuinfo 2>/dev/null || true)"
cpu_count="$(getconf _NPROCESSORS_ONLN)"
ram_bytes="$(sysctl -n hw.memsize 2>/dev/null || awk '/MemTotal/{print $2 * 1024; exit}' /proc/meminfo 2>/dev/null || true)"
free_disk_kb="$(df -Pk "$output_dir" | awk 'NR==2 {print $4}')"
printf 'cpu_model=%s\ncpu_count=%s\nram_bytes=%s\nfree_disk_kb=%s\n' "$cpu_model" "$cpu_count" "$ram_bytes" "$free_disk_kb" > "$output_dir/host.txt"
DEXBENCH_GIT_COMMIT="$(git -C "$repo_root" rev-parse HEAD)"
export DEXBENCH_GIT_COMMIT
export DEXBENCH_DISK_PATH="$output_dir"

wait_for_server() {
  "$benchmark_binary" wait-ready --pid "$current_pid" --timeout 180s "$@"
}

stop_server() {
  kill "$current_pid" 2>/dev/null || true
  wait "$current_pid" 2>/dev/null || true
  current_pid=""
}

# Every scenario variant and concurrency step gets a newly started server with empty state.
start_quick_server() {
  local target="$1" state_dir="$2"
  mkdir -p "$state_dir"
  case "$target" in
    temporal)
      nice -n 10 "$dexcli_binary" dev --engine temporal --open=false --dex-port 8801 --web-port 8802 \
        --sqlite-db-filename "$state_dir/temporal.sqlite" --blob-store-dir "$state_dir/go-blobs" >"$state_dir/server.log" 2>&1 &
      ;;
    zd)
      cat > "$state_dir/zd-quick.yaml" <<'YAML'
metrics:
  listen: "127.0.0.1:9901"
YAML
      nice -n 10 "$dexcli_binary" dev --engine zd --open=false --dex-port 8801 --web-port 8802 \
        --zd-binary "$zd_binary" --zd-data-dir "$state_dir/zd-data" --zd-config "$state_dir/zd-quick.yaml" >"$state_dir/server.log" 2>&1 &
      ;;
    *) echo "unknown target: $target" >&2; exit 2 ;;
  esac
  current_pid=$!
  if ! wait_for_server; then tail -50 "$state_dir/server.log" >&2; return 1; fi
}

run_step() {
  local target="$1" variant="$2" step_concurrency="$3"
  local metrics_argument=() version
  if [[ "$target" == zd ]]; then
    metrics_argument=(--metrics-url http://127.0.0.1:9901/metrics)
    version="$("$zd_binary" --version 2>/dev/null || echo unknown)"
  else
    version="$("$dexcli_binary" --version 2>/dev/null || echo unknown)"
  fi
  wait_for_load
  # The dexcli PID is the sampling root, so its children (Temporal, server-zd, s3mock) are included.
  DEXBENCH_SERVER_VERSION="$version" nice -n 10 "$benchmark_binary" run \
    --address 127.0.0.1:8801 --target "$target" --engine "$target" \
    --duration "$duration" --warmup "$warmup" --concurrency "$step_concurrency" \
    --workers "$workers" --steps "$steps" --scenarios "$variant" --mode quick \
    --out "$output_dir" --server-pid "$current_pid" ${metrics_argument[@]+"${metrics_argument[@]}"}
}

normal_scenarios=""
has_recovery=0
IFS=, read -ra requested_scenarios <<< "$scenarios"
for scenario in "${requested_scenarios[@]}"; do
  scenario="${scenario//[[:space:]]/}"
  if [[ "$scenario" == recovery ]]; then has_recovery=1; continue; fi
  if [[ -n "$normal_scenarios" ]]; then normal_scenarios+=","; fi
  normal_scenarios+="$scenario"
done
variants=()
if [[ -n "$normal_scenarios" ]]; then
  while IFS= read -r variant; do variants+=("$variant"); done < <("$benchmark_binary" variants --scenarios "$normal_scenarios")
fi

IFS=, read -ra requested_targets <<< "$targets"
IFS=, read -ra requested_concurrency <<< "$concurrency"
for target in "${requested_targets[@]}"; do
  target="${target//[[:space:]]/}"
  for variant in ${variants[@]+"${variants[@]}"}; do
    for step_concurrency in "${requested_concurrency[@]}"; do
      step_dir="$run_dir/$target-$variant-c$step_concurrency"
      wait_for_load
      start_quick_server "$target" "$step_dir"
      run_step "$target" "$variant" "$step_concurrency"
      stop_server
      rm -rf "$step_dir"
    done
  done
done

if [[ "$has_recovery" == 1 && "$targets" == *zd* ]]; then
  if [[ -n "${DEXBENCH_RECOVERY_MB:-}" ]]; then
    recovery_sizes="$DEXBENCH_RECOVERY_MB"
  elif (( free_disk_kb < 2097152 )); then
    recovery_sizes=""
  elif (( free_disk_kb < 10485760 )); then
    recovery_sizes="10,50,100"
  else
    recovery_sizes="100,500,1000"
  fi
  if [[ -z "$recovery_sizes" ]]; then
    echo "recovery skipped: less than 2 GiB free" | tee "$output_dir/recovery-skipped.txt"
  else
    IFS=, read -ra requested_sizes <<< "$recovery_sizes"
    for size in "${requested_sizes[@]}"; do
    data_dir="$run_dir/recovery-$size"
    mkdir -p "$data_dir"
    cat > "$run_dir/zd-recovery.yaml" <<'YAML'
metrics:
  listen: "127.0.0.1:9901"
snapshot:
  logThresholdBytes: 2147483647
  interval: 24h
YAML
    wait_for_load
    nice -n 10 "$dexcli_binary" dev --engine zd --open=false --dex-port 8801 --web-port 8802 \
      --zd-binary "$zd_binary" --zd-data-dir "$data_dir" --zd-config "$run_dir/zd-recovery.yaml" \
      >"$run_dir/recovery-$size-server.log" 2>&1 &
    current_pid=$!
    wait_for_server
    wait_for_load
    nice -n 10 "$benchmark_binary" seed-recovery --size-mb "$size" > "$run_dir/recovery-$size-seed.txt"
    stop_server
    # Removing local state makes this a cold-node restart that replays from object storage.
    rm -rf "$data_dir/store"
    wait_for_load
    start_ns="$(python3 -c 'import time; print(time.time_ns())')"
    nice -n 10 "$dexcli_binary" dev --engine zd --open=false --dex-port 8801 --web-port 8802 \
      --zd-binary "$zd_binary" --zd-data-dir "$data_dir" --zd-config "$run_dir/zd-recovery.yaml" \
      >>"$run_dir/recovery-$size-server.log" 2>&1 &
    current_pid=$!
    elapsed="$(wait_for_server --started-unix-nano "$start_ns")"
    version="$($zd_binary --version 2>/dev/null || echo unknown)"
    DEXBENCH_SERVER_VERSION="$version" "$benchmark_binary" record-recovery --size-mb "$size" \
      --elapsed-seconds "$elapsed" --mode quick --out "$output_dir/zd-recovery-${size}mb.json"
    stop_server
    done
  fi
fi

if compgen -G "$output_dir/*.json" >/dev/null; then
  "$benchmark_binary" report "$output_dir"/*.json > "$output_dir/report.md"
  "$benchmark_binary" chart "$output_dir"/*.json > "$output_dir/charts.html"
else
  printf 'No scenarios produced results.\n' > "$output_dir/report.md"
fi
echo "results: $output_dir"
