#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
install_root="${DEXBENCH_INSTALL_ROOT:-$HOME/.local/dex-benchmark}"
server_binary="${DEXBENCH_SERVER_BINARY:-$install_root/bin/dex-server}"
zd_binary="${DEXBENCH_ZD_BINARY:-$install_root/bin/dex-server-zd}"
benchmark_binary="$repo_root/benchmark/bin/dexbench"
: "${DEXBENCH_S3_BUCKET:?set DEXBENCH_S3_BUCKET}"
: "${DEXBENCH_S3_PREFIX:?set DEXBENCH_S3_PREFIX}"
: "${DEXBENCH_S3_REGION:?set DEXBENCH_S3_REGION}"
: "${DEXBENCH_NVME_DIR:?set DEXBENCH_NVME_DIR to the mounted local NVMe path}"
mkdir -p "$DEXBENCH_NVME_DIR"
if [[ "$(findmnt -no SOURCE -T "$DEXBENCH_NVME_DIR")" != *nvme* ]]; then
  echo "DEXBENCH_NVME_DIR must be backed by local NVMe" >&2
  exit 1
fi

wait_for_load() {
  local load
  while :; do
    load="$(uptime | sed -E 's/.*load averages?: ([0-9.]+).*/\1/')"
    if awk -v load="$load" 'BEGIN { exit !(load < 24) }'; then return; fi
    echo "waiting for 1-minute load below 24 (now $load)" >&2
    sleep 15
  done
}

duration="${DEXBENCH_DURATION:-180}"
warmup="${DEXBENCH_WARMUP:-30}"
concurrency="${DEXBENCH_CONCURRENCY:-1,8,32,128}"
workers="${DEXBENCH_WORKERS:-0}"
steps="${DEXBENCH_STEPS:-10}"
scenarios="${DEXBENCH_SCENARIOS:-start-complete,sequential-steps,async-steps,rpc,attribute-read,channel-wake,attribute-write,recovery}"
targets="${DEXBENCH_TARGETS:-temporal,zd}"
output_dir="${DEXBENCH_OUT:-$repo_root/benchmark/results/$(date -u +%Y-%m-%d)-prod-$(hostname -s)}"
run_dir="$(mktemp -d "$DEXBENCH_NVME_DIR/dexbench.XXXXXX")"
current_pid=""
compose_started=0
docker_command=(docker)
if ! docker info >/dev/null 2>&1; then docker_command=(sudo docker); fi
compose=("${docker_command[@]}" compose --env-file "$repo_root/server/docker-compose/.env" -f "$repo_root/server/docker-compose/integ-dependencies.yml" -f "$repo_root/server/docker-compose/attribute-store-dependencies.yml")

cleanup() {
  if [[ -n "$current_pid" ]]; then kill "$current_pid" 2>/dev/null || true; wait "$current_pid" 2>/dev/null || true; fi
  if [[ "$compose_started" == 1 ]]; then "${compose[@]}" down -v >/dev/null 2>&1 || true; fi
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
DEXBENCH_GIT_COMMIT="$(git -C "$repo_root" rev-parse HEAD)"
export DEXBENCH_GIT_COMMIT
export DEXBENCH_DISK_PATH="$run_dir"
if [[ -z "${DEXBENCH_INSTANCE_TYPE:-}" ]]; then
  metadata_token="$(curl -fsS --max-time 2 -X PUT -H 'X-aws-ec2-metadata-token-ttl-seconds: 60' http://169.254.169.254/latest/api/token)"
  DEXBENCH_INSTANCE_TYPE="$(curl -fsS --max-time 2 -H "X-aws-ec2-metadata-token: $metadata_token" http://169.254.169.254/latest/meta-data/instance-type)"
fi
export DEXBENCH_INSTANCE_TYPE
cpu_model="$(awk -F: '/model name/{print $2; exit}' /proc/cpuinfo)"
cpu_count="$(getconf _NPROCESSORS_ONLN)"
ram_bytes="$(awk '/MemTotal/{print $2 * 1024; exit}' /proc/meminfo)"
free_disk_kb="$(df -Pk "$run_dir" | awk 'NR==2 {print $4}')"
printf 'cpu_model=%s\ncpu_count=%s\nram_bytes=%s\nfree_disk_kb=%s\ninstance_type=%s\nregion=%s\n' \
  "$cpu_model" "$cpu_count" "$ram_bytes" "$free_disk_kb" "$DEXBENCH_INSTANCE_TYPE" "$DEXBENCH_S3_REGION" > "$output_dir/host.txt"

wait_for_server() {
  local attempts
  for ((attempts=0; attempts<300; attempts++)); do
    if "$benchmark_binary" probe >/dev/null 2>&1; then return; fi
    if ! kill -0 "$current_pid" 2>/dev/null; then echo "server exited during startup" >&2; return 1; fi
    sleep 1
  done
  echo "FlowService did not become ready" >&2
  return 1
}

stop_server() {
  kill "$current_pid" 2>/dev/null || true
  wait "$current_pid" 2>/dev/null || true
  current_pid=""
}

attribute_dsn='postgres://dex:dex@127.0.0.1:55432/dex?sslmode=disable'
export DEXBENCH_ATTRIBUTE_DSN="$attribute_dsn"
export DEXBENCH_GO_BLOBS="$run_dir/go-blobs"
export DEXBENCH_GO_CONFIG="$run_dir/go-server.yaml"
python3 - "$install_root/server-development.yaml" <<'PY'
import os
import sys
import yaml

with open(sys.argv[1], encoding="utf-8") as source:
    config = yaml.safe_load(source)
config["blobStore"]["supportedStorages"] = [{"status": "active", "storageId": "benchmark-local", "storageType": "local", "localDirectory": os.environ["DEXBENCH_GO_BLOBS"]}]
config["interpreter"]["temporal"]["hostPort"] = "127.0.0.1:7233"
config["attributeStore"] = {"stores": {"reporting": {"type": "postgres", "dsn": os.environ["DEXBENCH_ATTRIBUTE_DSN"], "tableName": "public.benchmark_flow_attributes"}}}
with open(os.environ["DEXBENCH_GO_CONFIG"], "w", encoding="utf-8") as destination:
    yaml.safe_dump(config, destination)
PY

"${compose[@]}" up -d attribute-postgres
compose_started=1
for ((attempts=0; attempts<180; attempts++)); do
  if (echo > /dev/tcp/127.0.0.1/55432) >/dev/null 2>&1; then break; fi
  sleep 1
done
"${compose[@]}" exec -T attribute-postgres psql -U dex -d dex -v ON_ERROR_STOP=1 \
  -c 'CREATE TABLE IF NOT EXISTS public.benchmark_flow_attributes (flow_id TEXT PRIMARY KEY, benchmark_write BIGINT)' \
  -c 'CREATE TABLE IF NOT EXISTS dexzd_attribute_sync_fencing (store_name text NOT NULL, cluster_id text NOT NULL, scope text NOT NULL, fencing_token bigint NOT NULL, updated_at timestamptz NOT NULL, PRIMARY KEY (store_name, cluster_id, scope))'

normal_scenarios=""
has_recovery=0
IFS=, read -ra requested_scenarios <<< "$scenarios"
for scenario in "${requested_scenarios[@]}"; do
  scenario="${scenario//[[:space:]]/}"
  if [[ "$scenario" == recovery ]]; then has_recovery=1; continue; fi
  if [[ -n "$normal_scenarios" ]]; then normal_scenarios+=","; fi
  normal_scenarios+="$scenario"
done

start_zd() {
  local data_dir="$1" prefix="$2" recovery="${3:-0}"
  local config_path="$run_dir/zd-config.yaml"
  cat > "$config_path" <<YAML
cluster:
  nodeId: "benchmark-$(basename "$data_dir")"
objectStore:
  region: "$DEXBENCH_S3_REGION"
  bucket: "$DEXBENCH_S3_BUCKET"
  prefix: "$prefix"
  credentialsSource: defaultChain
localStorage:
  directory: "$data_dir"
api:
  listen: "127.0.0.1:8801"
metrics:
  listen: "127.0.0.1:9901"
attributeStore:
  stores:
    reporting:
      type: postgres
      dsn: "$attribute_dsn"
      tableName: public.benchmark_flow_attributes
YAML
  if [[ "$recovery" == 1 ]]; then
    cat >> "$config_path" <<'YAML'
snapshot:
  logThresholdBytes: 2147483647
  interval: 24h
YAML
  fi
  nice -n 10 "$zd_binary" serve --config "$config_path" > "$run_dir/zd-server.log" 2>&1 &
  current_pid=$!
  wait_for_server
}

run_target() {
  local target="$1" version="$2" resources=""
  local extra_args=()
  attribute_container="$("${compose[@]}" ps -q attribute-postgres)"
  resources="$("${docker_command[@]}" top "$attribute_container" -eo pid | awk 'NR>1 {printf "%s%s", separator, $1; separator=","}')"
  if [[ "$target" == zd ]]; then extra_args=(--metrics-url http://127.0.0.1:9901/metrics); fi
  if [[ "$target" == temporal ]]; then
    resources+=",$("${docker_command[@]}" top temporal -eo pid | awk 'NR>1 {printf "%s%s", separator, $1; separator=","}')"
    resources+="$("${docker_command[@]}" top temporal-postgresql -eo pid | awk 'NR>1 {printf ",%s", $1}')"
  fi
  extra_args+=(--resource-pids "$resources")
  wait_for_load
  DEXBENCH_SERVER_VERSION="$version" nice -n 10 "$benchmark_binary" run \
    --target "$target" --engine "$target" --mode prod --region "$DEXBENCH_S3_REGION" \
    --instance-type "$DEXBENCH_INSTANCE_TYPE" --server-pid "$current_pid" \
    --duration "$duration" --warmup "$warmup" --concurrency "$concurrency" \
    --workers "$workers" --steps "$steps" --scenarios "$normal_scenarios" \
    --attribute-store-name reporting --out "$output_dir" "${extra_args[@]}"
}

IFS=, read -ra requested_targets <<< "$targets"
for target in "${requested_targets[@]}"; do
  target="${target//[[:space:]]/}"
  if [[ -z "$normal_scenarios" ]]; then continue; fi
  wait_for_load
  if [[ "$target" == temporal ]]; then
    "${compose[@]}" up -d postgresql elasticsearch temporal
    for ((attempts=0; attempts<180; attempts++)); do
      if (echo > /dev/tcp/127.0.0.1/7233) >/dev/null 2>&1; then break; fi
      sleep 1
    done
    nice -n 10 "$server_binary" --config "$DEXBENCH_GO_CONFIG" start > "$run_dir/go-server.log" 2>&1 &
    current_pid=$!
    wait_for_server
    version="$(sed -n 's/^server=//p' "$install_root/releases.txt")"
  elif [[ "$target" == zd ]]; then
    wait_for_load
    start_zd "$run_dir/zd-normal" "$DEXBENCH_S3_PREFIX/normal-$(date -u +%s)"
    version="$(sed -n 's/^server-zd=//p' "$install_root/releases.txt")"
  else
    echo "unknown target $target" >&2
    exit 2
  fi
  run_target "$target" "$version"
  stop_server
  if [[ "$target" == temporal ]]; then
    "${compose[@]}" stop temporal elasticsearch postgresql
    "${compose[@]}" exec -T attribute-postgres psql -U dex -d dex -v ON_ERROR_STOP=1 \
      -c 'TRUNCATE public.benchmark_flow_attributes, dexzd_attribute_sync_fencing'
  fi
done

if [[ "$has_recovery" == 1 && "$targets" == *zd* ]]; then
  recovery_sizes="${DEXBENCH_RECOVERY_MB:-100,500,1000}"
  IFS=, read -ra requested_sizes <<< "$recovery_sizes"
  for size in "${requested_sizes[@]}"; do
    data_dir="$run_dir/zd-recovery-$size"
    prefix="$DEXBENCH_S3_PREFIX/recovery-${size}mb-$(date -u +%s)"
    wait_for_load
    start_zd "$data_dir" "$prefix" 1
    wait_for_load
    nice -n 10 "$benchmark_binary" seed-recovery --size-mb "$size" > "$run_dir/recovery-$size-seed.txt"
    stop_server
    rm -rf "$data_dir"
    wait_for_load
    start_ns="$(python3 -c 'import time; print(time.time_ns())')"
    start_zd "$data_dir" "$prefix" 1
    end_ns="$(python3 -c 'import time; print(time.time_ns())')"
    elapsed="$(python3 -c 'import sys; print((int(sys.argv[2])-int(sys.argv[1]))/1e9)' "$start_ns" "$end_ns")"
    version="$(sed -n 's/^server-zd=//p' "$install_root/releases.txt")"
    DEXBENCH_SERVER_VERSION="$version" "$benchmark_binary" record-recovery --size-mb "$size" \
      --elapsed-seconds "$elapsed" --mode prod --region "$DEXBENCH_S3_REGION" \
      --instance-type "$DEXBENCH_INSTANCE_TYPE" --out "$output_dir/zd-recovery-${size}mb.json"
    stop_server
  done
fi

"$benchmark_binary" report "$output_dir"/*.json > "$output_dir/report.md"
echo "results: $output_dir"
