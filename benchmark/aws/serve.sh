#!/usr/bin/env bash
# Runs dex-server-zd and collects server-side data on the benchmark server host.
# Usage: serve.sh start | stop | status | config | collect-start DIR | collect-stop DIR | host-info
#        serve.sh profile DIR PID (started by collect-start when DEXBENCH_PROFILE_DELAY is set)
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [[ -f "$script_dir/serve.env" ]]; then
  # shellcheck source=/dev/null
  source "$script_dir/serve.env"
fi

bench_home="${DEXBENCH_SERVER_HOME:-$script_dir}"
zd_binary="${DEXBENCH_SERVER_ZD_BINARY:-$bench_home/dex-server-zd}"
bench_binary="${DEXBENCH_SERVER_BENCH_BINARY:-$bench_home/dexbench}"
state_dir="${DEXBENCH_STATE_DIR:-$bench_home/state}"
storage_dir="${DEXBENCH_STORAGE_DIR:-/mnt/nvme/dexzd}"
listen_host="${DEXBENCH_LISTEN_HOST:-0.0.0.0}"
api_port="${DEXBENCH_API_PORT:-8801}"
metrics_port="${DEXBENCH_METRICS_PORT:-9901}"
config_path="$state_dir/zd-config.yaml"
pid_path="$state_dir/server.pid"
log_path="$state_dir/server.log"
collectors_path="$state_dir/collectors.pid"
core_dir="${DEXBENCH_CORE_DIR:-/mnt/nvme/cores}"
profile_delay="${DEXBENCH_PROFILE_DELAY:-}"
profile_seconds="${DEXBENCH_PROFILE_SECONDS:-10}"
profile_frequency="${DEXBENCH_PROFILE_FREQUENCY:-49}"

# Maps each tunable variable to its server-zd YAML key; edit here when a key is renamed.
tunable_keys="
DEXBENCH_ZD_PARTITIONS cluster.partitionCount
DEXBENCH_ZD_MAX_IN_FLIGHT commit.maxInFlight
DEXBENCH_ZD_BATCH_WINDOW commit.batchWindow
DEXBENCH_ZD_APPLIER_LANES commit.applierLanes
DEXBENCH_ZD_APPLIER_QUEUE_FRAMES commit.applierQueueFrames
DEXBENCH_ZD_MAX_BATCH_RECORDS commit.maxBatchRecords
DEXBENCH_ZD_MAX_UNAPPLIED_BYTES commit.maxUnappliedBytes
DEXBENCH_ZD_MAX_BACKGROUND_JOBS localStorage.maxBackgroundJobs
DEXBENCH_ZD_WRITE_BUFFER_BYTES localStorage.writeBufferBytes
DEXBENCH_ZD_BLOCK_CACHE_BYTES localStorage.blockCacheBytes
DEXBENCH_ZD_MAX_INFLIGHT_RPCS limits.maxInFlightRpcsPerNode
"

fail() { echo "serve.sh: $*" >&2; exit 1; }

# Values are written unquoted, so only plain scalars such as 8 or 20ms are accepted.
check_plain_value() {
  [[ "$2" =~ ^[A-Za-z0-9._:/-]+$ ]] || fail "$1 has unsupported value '$2'"
}

# config_entries prints one "section<TAB>key<TAB>value" line per setting; later lines win.
config_entries() {
  local bucket="${DEXBENCH_S3_BUCKET:?set DEXBENCH_S3_BUCKET}"
  local prefix="${DEXBENCH_S3_PREFIX:?set DEXBENCH_S3_PREFIX}"
  printf 'cluster\tnodeId\t"%s"\n' "${DEXBENCH_NODE_ID:-dexbench-1}"
  printf 'objectStore\tbucket\t"%s"\n' "$bucket"
  printf 'objectStore\tprefix\t"%s"\n' "$prefix"
  printf 'objectStore\tregion\t"%s"\n' "${DEXBENCH_S3_REGION:-us-west-2}"
  printf 'objectStore\tcredentialsSource\t%s\n' "${DEXBENCH_S3_CREDENTIALS:-defaultChain}"
  if [[ -n "${DEXBENCH_S3_ENDPOINT:-}" ]]; then
    printf 'objectStore\tendpoint\t"%s"\n' "$DEXBENCH_S3_ENDPOINT"
    printf 'objectStore\tforcePathStyle\ttrue\n'
  fi
  printf 'localStorage\tdirectory\t"%s"\n' "$storage_dir"
  printf 'api\tlisten\t"%s:%s"\n' "$listen_host" "$api_port"
  printf 'metrics\tlisten\t"%s:%s"\n' "$listen_host" "$metrics_port"
  local name key value
  while read -r name key; do
    [[ -z "$name" ]] && continue
    value="${!name:-}"
    [[ -z "$value" ]] && continue
    check_plain_value "$name" "$value"
    printf '%s\t%s\t%s\n' "${key%%.*}" "${key#*.}" "$value"
  done <<< "$tunable_keys"
  # DEXBENCH_ZD_EXTRA holds comma-separated section.key=value pairs.
  local pair
  IFS=, read -ra extra_pairs <<< "${DEXBENCH_ZD_EXTRA:-}"
  for pair in ${extra_pairs[@]+"${extra_pairs[@]}"}; do
    key="${pair%%=*}"
    value="${pair#*=}"
    [[ "$pair" == *=* && "$key" == *.* ]] || fail "DEXBENCH_ZD_EXTRA entry '$pair' is not section.key=value"
    check_plain_value "$key" "$value"
    printf '%s\t%s\t%s\n' "${key%%.*}" "${key#*.}" "$value"
  done
}

# render_config groups entries into one YAML block per section, keeping the last value of each key.
render_config() {
  config_entries | awk -F '\t' '
    !($1 in seen_section) { seen_section[$1] = 1; sections[++section_count] = $1 }
    !(($1, $2) in value) { keys[$1, ++key_count[$1]] = $2 }
    { value[$1, $2] = $3 }
    END {
      for (s = 1; s <= section_count; s++) {
        section = sections[s]
        print section ":"
        for (k = 1; k <= key_count[section]; k++) print "  " keys[section, k] ": " value[section, keys[section, k]]
      }
    }'
}

is_running() {
  [[ -f "$pid_path" ]] && kill -0 "$(cat "$pid_path")" 2>/dev/null
}

# stop_collectors ends samplers and iostat left by a collect-start without a collect-stop.
stop_collectors() {
  if [[ -f "$collectors_path" ]]; then
    local collector_pid
    while read -r collector_pid; do
      stop_profile "$collector_pid"
    done < "$collectors_path"
    rm -f "$collectors_path"
  fi
}

stop_server() {
  stop_collectors
  if is_running; then
    local server_pid
    server_pid="$(cat "$pid_path")"
    kill -TERM "$server_pid"
    for _ in $(seq 1 600); do
      kill -0 "$server_pid" 2>/dev/null || break
      sleep 0.1
    done
    if kill -0 "$server_pid" 2>/dev/null; then
      echo "server did not stop in 60 seconds; killing it" >&2
      kill -KILL "$server_pid"
    fi
  fi
  rm -f "$pid_path"
}

start_server() {
  stop_server
  [[ -x "$zd_binary" ]] || fail "dex-server-zd not found at $zd_binary; set DEXBENCH_SERVER_ZD_BINARY"
  mkdir -p "$state_dir"
  render_config > "$config_path"
  # The server must be stopped first: RocksDB holds a lock on its directory.
  rm -rf "$storage_dir"
  mkdir -p "$storage_dir"
  # A crash leaves a core in core_dir when launch.sh set kernel.core_pattern.
  ulimit -c unlimited || echo "cannot raise the core size limit" >&2
  nohup "$zd_binary" serve --config "$config_path" </dev/null >"$log_path" 2>&1 &
  echo $! > "$pid_path"
}

collect_start() {
  local directory="$1"
  is_running || fail "server is not running"
  stop_collectors
  mkdir -p "$directory"
  cp "$config_path" "$directory/zd-config.yaml"
  # record_crash only reports cores written after this marker.
  touch "$directory/.collect-start"
  scrape_metrics "$directory/metrics-before.txt"
  nohup "$bench_binary" sample --pid "$(cat "$pid_path")" --out "$directory/server-samples.json" \
    </dev/null >"$directory/sampler.log" 2>&1 &
  echo $! > "$directory/.sampler.pid"
  echo $! >> "$collectors_path"
  if [[ "$(uname -s)" == Linux ]] && command -v iostat >/dev/null; then
    # Line buffering keeps the report intact when iostat is terminated.
    nohup stdbuf -oL iostat -dxmy 5 </dev/null >"$directory/iostat.txt" 2>&1 &
    echo $! > "$directory/.iostat.pid"
    echo $! >> "$collectors_path"
  else
    echo "iostat unavailable on this host" > "$directory/iostat.txt"
  fi
  if [[ -n "$profile_delay" ]]; then
    nohup bash "${BASH_SOURCE[0]}" profile "$directory" "$(cat "$pid_path")" \
      </dev/null >"$directory/profile.log" 2>&1 &
    echo $! > "$directory/.profile.pid"
    echo $! >> "$collectors_path"
  fi
}

# capture_profile records thread stacks and a perf profile in the middle of the measurement.
# collect-stop symbolizes the profile after the measurement, so that work does not compete with the server.
capture_profile() {
  local directory="$1" server_pid="$2"
  sleep "$profile_delay"
  kill -0 "$server_pid" 2>/dev/null || { echo "server exited before profiling"; return 0; }
  capture_thread_stacks "$server_pid" > "$directory/thread-stacks-1.txt"
  if command -v perf >/dev/null; then
    sudo -n perf record -F "$profile_frequency" --call-graph dwarf,8192 -p "$server_pid" \
      -o "$directory/perf.data" -- sleep "$profile_seconds" || echo "perf record failed"
  else
    echo "perf unavailable on this host"
    sleep "$profile_seconds"
  fi
  capture_thread_stacks "$server_pid" > "$directory/thread-stacks-2.txt"
}

symbolize_profile() {
  local directory="$1"
  [[ -s "$directory/perf.data" ]] || return 0
  # shellcheck disable=SC2024 # The outputs stay owned by the benchmark user.
  sudo -n perf report -i "$directory/perf.data" --stdio --no-children --sort comm,symbol --percent-limit 0.3 \
    > "$directory/perf-report.txt" 2>/dev/null || echo "perf report failed" >> "$directory/profile.log"
  sudo -n perf script -i "$directory/perf.data" 2>/dev/null | gzip > "$directory/perf-script.txt.gz" \
    || echo "perf script failed" >> "$directory/profile.log"
  # perf.data needs this exact binary to symbolize; the text outputs do not.
  sudo -n rm -f "$directory/perf.data" || true
}

# stop_profile ends a profile capture and its root-owned perf child.
stop_profile() {
  local profile_pid="$1"
  kill -0 "$profile_pid" 2>/dev/null || return 0
  sudo -n pkill -INT -f "perf record .* -p $(cat "$pid_path" 2>/dev/null || echo none) " 2>/dev/null || true
  pkill -TERM -P "$profile_pid" 2>/dev/null || true
  kill -TERM "$profile_pid" 2>/dev/null || true
}

capture_thread_stacks() {
  if command -v eu-stack >/dev/null; then
    sudo -n eu-stack -p "$1" 2>&1 || true
  else
    echo "eu-stack unavailable on this host"
  fi
}

collect_stop() {
  local directory="$1" sampler_pid
  [[ -f "$directory/.sampler.pid" ]] || fail "no sampler was started in $directory"
  sampler_pid="$(cat "$directory/.sampler.pid")"
  kill -TERM "$sampler_pid" 2>/dev/null || true
  for _ in $(seq 1 100); do
    kill -0 "$sampler_pid" 2>/dev/null || break
    sleep 0.1
  done
  if [[ -f "$directory/.iostat.pid" ]]; then
    kill -TERM "$(cat "$directory/.iostat.pid")" 2>/dev/null || true
  fi
  if [[ -f "$directory/.profile.pid" ]]; then
    local profile_pid
    profile_pid="$(cat "$directory/.profile.pid")"
    for _ in $(seq 1 300); do
      kill -0 "$profile_pid" 2>/dev/null || break
      sleep 0.1
    done
    stop_profile "$profile_pid"
    symbolize_profile "$directory"
  fi
  rm -f "$directory/.sampler.pid" "$directory/.iostat.pid" "$directory/.profile.pid" "$collectors_path"
  [[ -s "$directory/server-samples.json" ]] || fail "the sampler wrote no samples; see $directory/sampler.log"
  scrape_metrics "$directory/metrics-after.txt"
  {
    local log_file
    find "$storage_dir" -type f \( -name LOG -o -name 'LOG.old.*' \) 2>/dev/null | while read -r log_file; do
      printf '%s stalling=%s stopping=%s\n' "$log_file" \
        "$(grep -c 'Stalling writes' "$log_file" || true)" "$(grep -c 'Stopping writes' "$log_file" || true)"
    done
  } > "$directory/rocksdb-stalls.txt"
  cp "$log_path" "$directory/server.log"
  if ! is_running; then
    record_crash "$directory"
  fi
  rm -f "$directory/.collect-start"
}

# record_crash keeps the backtrace of every core; the cores stay on the server host.
record_crash() {
  local directory="$1" core
  echo "server was not running at collect-stop" > "$directory/crash.txt"
  find "$core_dir" -maxdepth 1 -name 'core.*' -newer "$directory/.collect-start" 2>/dev/null | while read -r core; do
    {
      echo "== $core"
      if command -v eu-stack >/dev/null; then
        timeout 300 sudo -n eu-stack --core="$core" --executable="$zd_binary" 2>&1 || true
      fi
    } >> "$directory/crash.txt"
  done
}

scrape_metrics() {
  if ! curl -fsS --max-time 5 "http://127.0.0.1:$metrics_port/metrics" > "$1" 2>/dev/null; then
    echo "metrics unavailable" > "$1"
  fi
}

# host_info prints key=value lines; EC2 fields are empty off EC2.
host_info() {
  local token instance_type="" zone="" placement_group=""
  token="$(curl -fsS --connect-timeout 1 --max-time 2 -X PUT http://169.254.169.254/latest/api/token \
    -H 'X-aws-ec2-metadata-token-ttl-seconds: 60' 2>/dev/null || true)"
  if [[ -n "$token" ]]; then
    instance_type="$(imds "$token" instance-type)"
    zone="$(imds "$token" placement/availability-zone)"
    placement_group="$(imds "$token" placement/group-name)"
  fi
  echo "instance_type=$instance_type"
  echo "availability_zone=$zone"
  echo "placement_group=$placement_group"
  echo "cpu_count=$(getconf _NPROCESSORS_ONLN)"
  echo "kernel=$(uname -sr)"
  echo "zd_version=$("$zd_binary" --version 2>/dev/null || echo unknown)"
  echo "storage_directory=$storage_dir"
  local existing="$storage_dir"
  while [[ ! -d "$existing" ]]; do existing="$(dirname "$existing")"; done
  echo "storage_filesystem=$(df -P "$existing" | awk 'NR==2 {print $1 " " $2 "KiB"}')"
  if command -v lsblk >/dev/null; then
    echo "disks=$(lsblk -dno NAME,SIZE,MODEL | tr -s ' ' | paste -sd ';' -)"
  fi
}

imds() {
  curl -fsS --connect-timeout 1 --max-time 2 -H "X-aws-ec2-metadata-token: $1" "http://169.254.169.254/latest/meta-data/$2" 2>/dev/null || true
}

case "${1:-}" in
  start) start_server ;;
  stop) stop_server ;;
  status) is_running ;;
  config) render_config ;;
  collect-start) collect_start "${2:?collect-start needs a directory}" ;;
  collect-stop) collect_stop "${2:?collect-stop needs a directory}" ;;
  host-info) host_info ;;
  profile) capture_profile "${2:?profile needs a directory}" "${3:?profile needs a server PID}" ;;
  *) fail "usage: serve.sh start|stop|status|config|collect-start DIR|collect-stop DIR|host-info|profile DIR PID" ;;
esac
