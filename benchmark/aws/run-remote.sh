#!/usr/bin/env bash
# Runs benchmark points from the load host against a remote server-zd.
# Usage: run-remote.sh; see "AWS with real S3" in benchmark/README.md.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/../.." && pwd)"

remote_mode="${DEXBENCH_REMOTE:-ssh}"
server_host="${DEXBENCH_SERVER_HOST:-}"
server_address="${DEXBENCH_SERVER_ADDRESS:-127.0.0.1}"
api_port="${DEXBENCH_API_PORT:-8801}"
metrics_port="${DEXBENCH_METRICS_PORT:-9901}"
ssh_options="${DEXBENCH_SSH_OPTIONS:--o BatchMode=yes -o StrictHostKeyChecking=accept-new}"
duration="${DEXBENCH_DURATION:-45}"
warmup="${DEXBENCH_WARMUP:-10}"
concurrency="${DEXBENCH_CONCURRENCY:-1,256}"
connections="${DEXBENCH_CONNECTIONS:-1}"
trials="${DEXBENCH_TRIALS:-1}"
workers="${DEXBENCH_WORKERS:-0}"
steps="${DEXBENCH_STEPS:-10}"
scenarios="${DEXBENCH_SCENARIOS:-sequential-steps}"
points_file="${DEXBENCH_POINTS:-}"
run_id="${DEXBENCH_RUN_ID:-$(date -u +%Y%m%dT%H%M%SZ)}"
# Each invocation writes under its own prefix, so rerunning a run ID never reuses objects.
invocation_id="$(date -u +%Y%m%dT%H%M%SZ)-$(od -An -N3 -tx1 /dev/urandom | tr -d ' \n')"
prefix_root="$run_id/$invocation_id"
output_dir="${DEXBENCH_OUT:-$(mktemp -d "${TMPDIR:-/tmp}/dexbench-$run_id-XXXXXX")}"
benchmark_binary="${DEXBENCH_BENCH_BINARY:-$repo_root/benchmark/bin/dexbench}"
if [[ "$remote_mode" == local ]]; then
  server_home="${DEXBENCH_SERVER_HOME:-$script_dir}"
  # Keeps server state and collected files out of the repository.
  export DEXBENCH_STATE_DIR="${DEXBENCH_STATE_DIR:-$(mktemp -d "${TMPDIR:-/tmp}/dexbench-state-XXXXXX")}"
else
  server_home="${DEXBENCH_SERVER_HOME:-dexbench}"
  [[ -n "$server_host" ]] || { echo "set DEXBENCH_SERVER_HOST to the SSH target of the server host" >&2; exit 2; }
fi
server_state_dir="${DEXBENCH_STATE_DIR:-$server_home/state}"
# Server-side settings forwarded to serve.sh on every call, in addition to every DEXBENCH_ZD_* variable.
forwarded_settings="DEXBENCH_SERVER_HOME DEXBENCH_SERVER_ZD_BINARY DEXBENCH_SERVER_BENCH_BINARY DEXBENCH_STATE_DIR
DEXBENCH_STORAGE_DIR DEXBENCH_LISTEN_HOST DEXBENCH_API_PORT DEXBENCH_METRICS_PORT DEXBENCH_NODE_ID
DEXBENCH_S3_BUCKET DEXBENCH_S3_REGION DEXBENCH_S3_ENDPOINT DEXBENCH_S3_CREDENTIALS
DEX_ZD_OBJECT_STORE_ACCESS_KEY_ID DEX_ZD_OBJECT_STORE_SECRET_ACCESS_KEY"

# tunable_names lists the exported server settings; DEXBENCH_ZD_BINARY names the local run.sh binary.
tunable_names() {
  compgen -e | grep '^DEXBENCH_ZD_' | grep -vx DEXBENCH_ZD_BINARY || true
}

remote() {
  if [[ "$remote_mode" == local ]]; then
    bash -c "$1" < /dev/null
  else
    # The command is built and quoted locally on purpose.
    # shellcheck disable=SC2086,SC2029
    # -n keeps ssh from reading the points list on stdin.
    ssh -n $ssh_options "$server_host" "$1"
  fi
}

fetch() {
  if [[ "$remote_mode" == local ]]; then
    cp -R "$1" "$2"
  else
    # shellcheck disable=SC2086
    scp $ssh_options -q -r "$server_host:$1" "$2"
  fi
}

# serve_command builds a serve.sh call carrying the forwarded settings and the given overrides.
serve_command() {
  local overrides="$1"; shift
  local command="env" name
  for name in $forwarded_settings $(tunable_names); do
    if [[ -n "${!name:-}" ]]; then command+=" $(printf '%q' "$name=${!name}")"; fi
  done
  for name in $overrides; do command+=" $(printf '%q' "$name")"; done
  command+=" bash $(printf '%q' "$server_home/serve.sh")"
  for name in "$@"; do command+=" $(printf '%q' "$name")"; done
  printf '%s' "$command"
}

# effective_tunables prints the DEXBENCH_ZD_* settings of a point as semicolon-separated KEY=VALUE.
effective_tunables() {
  local overrides="$1" name pair result=""
  for name in $(tunable_names); do
    result+="${result:+;}$name=${!name}"
  done
  for pair in $overrides; do
    result="$(printf '%s' "$result" | tr ';' '\n' | grep -v "^${pair%%=*}=" | paste -sd ';' - || true)"
    result+="${result:+;}$pair"
  done
  printf '%s' "$result"
}

validate_override() {
  [[ "$1" =~ ^DEXBENCH_ZD_[A-Z_]+=[A-Za-z0-9._:/,=-]+$ ]] || { echo "invalid point override '$1'; use DEXBENCH_ZD_NAME=value" >&2; exit 2; }
}

mkdir -p "$output_dir"
if compgen -G "$output_dir/*.json" >/dev/null; then
  echo "result directory already contains JSON; set DEXBENCH_OUT for a new run" >&2
  exit 1
fi
if [[ -z "${DEXBENCH_BENCH_BINARY:-}" ]]; then
  mkdir -p "$repo_root/benchmark/bin"
  (cd "$repo_root/benchmark" && env GOWORK=off go build -buildvcs=true -o "$benchmark_binary" ./cmd/dexbench)
fi

# Expand the points: one "variant concurrency [overrides]" line each.
points_list="$output_dir/points.txt"
: > "$points_list"
add_points() {
  local scenario="$1" point_concurrency="$2" overrides="$3" variant
  for variant in $("$benchmark_binary" variants --scenarios "$scenario"); do
    echo "$variant $point_concurrency${overrides:+ $overrides}" >> "$points_list"
  done
}
if [[ -n "$points_file" ]]; then
  while read -r scenario point_concurrency overrides; do
    [[ -z "$scenario" || "$scenario" == \#* ]] && continue
    for pair in $overrides; do validate_override "$pair"; done
    add_points "$scenario" "$point_concurrency" "$overrides"
  done < "$points_file"
else
  IFS=, read -ra requested_scenarios <<< "$scenarios"
  IFS=, read -ra requested_concurrency <<< "$concurrency"
  for scenario in "${requested_scenarios[@]}"; do
    for point_concurrency in "${requested_concurrency[@]}"; do add_points "$scenario" "$point_concurrency" ""; done
  done
fi

# Record both hosts and the configuration needed to rerun these points.
{
  echo "# Load host"
  echo "load_cpu_count=$(getconf _NPROCESSORS_ONLN)"
  echo "load_kernel=$(uname -sr)"
  token="$(curl -fsS --connect-timeout 1 --max-time 2 -X PUT http://169.254.169.254/latest/api/token \
    -H 'X-aws-ec2-metadata-token-ttl-seconds: 60' 2>/dev/null || true)"
  if [[ -n "$token" ]]; then
    for field in instance-type placement/availability-zone placement/group-name; do
      echo "load_${field//[\/-]/_}=$(curl -fsS --connect-timeout 1 --max-time 2 -H "X-aws-ec2-metadata-token: $token" \
        "http://169.254.169.254/latest/meta-data/$field" 2>/dev/null || true)"
    done
  fi
  echo "# Server host"
  remote "$(serve_command "" host-info)" | sed 's/^/server_/'
} > "$output_dir/host.txt"
remote "$(serve_command "DEXBENCH_S3_PREFIX=$prefix_root/base" config)" > "$output_dir/zd-config.yaml"
{ echo "# server-zd config before per-point overrides"; cat "$output_dir/zd-config.yaml"; } >> "$output_dir/host.txt"
git_commit="${DEXBENCH_GIT_COMMIT:-$(git -C "$repo_root" rev-parse HEAD 2>/dev/null || echo unknown)}"
# The revision embedded in the dexbench binary identifies the load generator even off a clone.
dexbench_revision="$(go version -m "$benchmark_binary" 2>/dev/null | awk '$2 == "vcs.revision" {r = $3} $2 == "vcs.modified" {m = $3} END {print (r == "" ? "unknown" : r) (m == "true" ? "-modified" : "")}' || echo unknown)"
zd_version="$(sed -n 's/^server_zd_version=//p' "$output_dir/host.txt")"
{
  echo "# Settings to rerun these points with benchmark/aws/reproduce.sh. Recorded values start with RECORDED_."
  for name in DEXBENCH_DURATION DEXBENCH_WARMUP DEXBENCH_CONNECTIONS DEXBENCH_TRIALS DEXBENCH_WORKERS DEXBENCH_STEPS \
    DEXBENCH_S3_REGION DEXBENCH_STORAGE_DIR $(tunable_names); do
    case "$name" in
      DEXBENCH_DURATION) value="$duration" ;; DEXBENCH_WARMUP) value="$warmup" ;;
      DEXBENCH_CONNECTIONS) value="$connections" ;; DEXBENCH_TRIALS) value="$trials" ;;
      DEXBENCH_WORKERS) value="$workers" ;; DEXBENCH_STEPS) value="$steps" ;;
      *) value="${!name:-}" ;;
    esac
    [[ -n "$value" ]] && printf '%s=%q\n' "$name" "$value"
  done
  printf 'RECORDED_RUN_ID=%q\n' "$run_id"
  printf 'RECORDED_GIT_COMMIT=%q\n' "$git_commit"
  printf 'RECORDED_DEXBENCH_REVISION=%q\n' "$dexbench_revision"
  printf 'RECORDED_S3_PREFIX_ROOT=%q\n' "$prefix_root"
  printf 'RECORDED_ZD_VERSION=%q\n' "$zd_version"
  printf 'RECORDED_ZD_COMMIT=%q\n' "${DEXBENCH_SERVER_ZD_COMMIT:-unknown}"
  while IFS='=' read -r key value; do
    [[ "$key" =~ ^[a-z_]+$ ]] || continue
    printf 'RECORDED_%s=%q\n' "$(printf '%s' "$key" | tr '[:lower:]' '[:upper:]')" "$value"
  done < "$output_dir/host.txt"
  printf 'RECORDED_COMMAND=%q\n' "DEXBENCH_POINTS=points.txt benchmark/aws/run-remote.sh"
  printf 'RECORDED_REPRODUCE=%q\n' "benchmark/aws/reproduce.sh <this directory> [variant [concurrency]]"
} > "$output_dir/config.env"

stop_remote_server() { remote "$(serve_command "" stop)" || true; }

# write_report summarizes every successful point; failed points keep their .failed.json files.
write_report() {
  local results=() path
  for path in "$output_dir"/*.json; do
    [[ -e "$path" && "$path" != *.failed.json ]] && results+=("$path")
  done
  if (( ${#results[@]} > 0 )); then
    "$benchmark_binary" report "${results[@]}" > "$output_dir/report.md"
    "$benchmark_binary" chart "${results[@]}" > "$output_dir/charts.html"
  else
    echo "No point produced a result." > "$output_dir/report.md"
  fi
}

finish() {
  stop_remote_server
  write_report
  echo "results: $output_dir ($failures failed points)"
}
failures=0
trap finish EXIT
trap 'exit 130' INT TERM

# wait_for_server polls readiness in short rounds and fails fast when the server process exits.
wait_for_server() {
  local deadline=$((SECONDS + 300))
  while (( SECONDS < deadline )); do
    if "$benchmark_binary" wait-ready --address "$server_address:$api_port" --timeout 10s 2>/dev/null; then
      return 0
    fi
    if ! remote "$(serve_command "" status)"; then
      echo "server exited during startup" >&2
      return 1
    fi
  done
  echo "server was not ready within 300 seconds" >&2
  return 1
}

# run_point runs one point; it returns non-zero when any step fails, leaving the run to continue.
run_point() {
  local variant="$1" point_concurrency="$2" overrides="$3" trial_flag="$4" point_id="$5"
  local collect_dir="$server_state_dir/collect/$run_id/$point_id" point_dir="$output_dir/.work-$point_id"
  local run_status=0 collect_status=0 result_path
  mkdir -p "$point_dir"
  if ! remote "$(serve_command "$overrides DEXBENCH_S3_PREFIX=$prefix_root/$point_id" start)" || ! wait_for_server; then
    echo "server for $point_id did not start" >&2
    remote "tail -50 $(printf '%q' "$server_state_dir/server.log")" >&2 || true
    return 1
  fi
  remote "$(serve_command "$overrides" collect-start "$collect_dir")" || return 1
  DEXBENCH_GIT_COMMIT="$git_commit" DEXBENCH_SERVER_VERSION="$zd_version" "$benchmark_binary" run \
    --address "$server_address:$api_port" --metrics-url "http://$server_address:$metrics_port/metrics" \
    --duration "$duration" --warmup "$warmup" --concurrency "$point_concurrency" --connections "$connections" \
    --workers "$workers" --steps "$steps" --scenarios "$variant" --mode aws --trial "$trial_flag" \
    --server-tunables "$(effective_tunables "$overrides")" --out "$point_dir" </dev/null || run_status=$?
  remote "$(serve_command "$overrides" collect-stop "$collect_dir")" || collect_status=$?
  stop_remote_server
  fetch "$collect_dir" "$output_dir/$point_id.server" || collect_status=1
  result_path="$(find "$point_dir" -name '*.json' | head -1)"
  if [[ -z "$result_path" ]]; then
    echo "$point_id wrote no result" >&2
    return 1
  fi
  if (( run_status != 0 )); then
    mv "$result_path" "$output_dir/$point_id.failed.json"
    echo "$point_id failed; its result is $point_id.failed.json" >&2
    return 1
  fi
  mv "$result_path" "$output_dir/$point_id.json"
  rm -rf "$point_dir"
  if (( collect_status != 0 )); then
    echo "server-side collection for $point_id failed" >&2
    return 1
  fi
  "$benchmark_binary" merge-server --result "$output_dir/$point_id.json" --samples "$output_dir/$point_id.server/server-samples.json"
}

point_index=0
while read -r variant point_concurrency overrides; do
  for trial in $(seq 1 "$trials"); do
    point_index=$((point_index + 1))
    point_id="$(printf 'p%03d-%s-c%s' "$point_index" "$variant" "$point_concurrency")"
    trial_flag=0
    if (( trials > 1 )); then point_id+="-t$trial"; trial_flag="$trial"; fi
    echo "== $point_id ${overrides:-default settings}" >&2
    if ! run_point "$variant" "$point_concurrency" "$overrides" "$trial_flag" "$point_id"; then
      failures=$((failures + 1))
      stop_remote_server
    fi
  done
done < "$points_list"
