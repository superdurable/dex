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
nvme_source="$(findmnt -no SOURCE -T "$DEXBENCH_NVME_DIR")"
# EBS volumes also appear as nvme* on Nitro instances, so check the device model.
nvme_disk="$nvme_source"
if [[ -n "$(lsblk -no PKNAME "$nvme_source" 2>/dev/null | head -1)" ]]; then nvme_disk="/dev/$(lsblk -no PKNAME "$nvme_source" | head -1)"; fi
if [[ "$(lsblk -dno MODEL "$nvme_disk" 2>/dev/null)" != *"Instance Storage"* ]]; then
  echo "DEXBENCH_NVME_DIR ($nvme_source) must be on instance-store NVMe; run benchmark/prod/mount-nvme.sh" >&2
  exit 1
fi

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

duration="${DEXBENCH_DURATION:-180}"
warmup="${DEXBENCH_WARMUP:-30}"
concurrency="${DEXBENCH_CONCURRENCY:-1,8,32,128}"
workers="${DEXBENCH_WORKERS:-0}"
steps="${DEXBENCH_STEPS:-10}"
scenarios="${DEXBENCH_SCENARIOS:-start-complete,sequential-steps,async-steps,rpc,attribute-read,channel-wake,attribute-write,recovery}"
targets="${DEXBENCH_TARGETS:-temporal,zd}"
output_dir="${DEXBENCH_OUT:-}"
run_dir="$(mktemp -d "$DEXBENCH_NVME_DIR/dexbench.XXXXXX")"
current_pid=""
compose_started=0
docker_command=(docker)
if ! docker info >/dev/null 2>&1; then docker_command=(sudo docker); fi
docker_root="$("${docker_command[@]}" info -f '{{.DockerRootDir}}')"
# Temporal's PostgreSQL and Elasticsearch data live under the Docker root, so it must share the NVMe device.
if [[ "$(findmnt -no SOURCE -T "$docker_root")" != "$nvme_source" ]]; then
  echo "Docker root $docker_root is not on $nvme_source; run benchmark/prod/mount-nvme.sh before host-setup.sh" >&2
  exit 1
fi
compose=("${docker_command[@]}" compose --env-file "$repo_root/server/docker-compose/.env" -f "$repo_root/server/docker-compose/integ-dependencies.yml" -f "$repo_root/server/docker-compose/attribute-store-dependencies.yml")
# elasticsearch uses Temporal advanced visibility; sql keeps visibility in Temporal's PostgreSQL without Elasticsearch.
temporal_visibility="${DEXBENCH_TEMPORAL_VISIBILITY:-elasticsearch}"
temporal_services=(postgresql elasticsearch temporal)
case "$temporal_visibility" in
  elasticsearch) ;;
  sql)
    temporal_services=(postgresql temporal)
    printf 'services:\n  temporal:\n    depends_on: !override\n      - postgresql\n    environment:\n      - ENABLE_ES=false\n' > "$run_dir/temporal-sql-visibility.yml"
    compose+=(-f "$run_dir/temporal-sql-visibility.yml")
    ;;
  *) echo "DEXBENCH_TEMPORAL_VISIBILITY must be elasticsearch or sql" >&2; exit 2 ;;
esac

cleanup() {
  if [[ -n "$current_pid" ]]; then kill "$current_pid" 2>/dev/null || true; wait "$current_pid" 2>/dev/null || true; fi
  if [[ "$compose_started" == 1 ]]; then "${compose[@]}" down -v >/dev/null 2>&1 || true; fi
  rm -rf "$run_dir"
}
trap cleanup EXIT INT TERM

if [[ -z "${DEXBENCH_INSTANCE_TYPE:-}" ]]; then
  metadata_token="$(curl -fsS --max-time 2 -X PUT -H 'X-aws-ec2-metadata-token-ttl-seconds: 60' http://169.254.169.254/latest/api/token)"
  DEXBENCH_INSTANCE_TYPE="$(curl -fsS --max-time 2 -H "X-aws-ec2-metadata-token: $metadata_token" http://169.254.169.254/latest/meta-data/instance-type)"
fi
export DEXBENCH_INSTANCE_TYPE
output_dir="${output_dir:-$repo_root/benchmark/results/$(date -u +%Y-%m-%d)-prod-$DEXBENCH_INSTANCE_TYPE}"
mkdir -p "$repo_root/benchmark/bin" "$output_dir"
if compgen -G "$output_dir/*.json" >/dev/null; then
  echo "result directory already contains JSON; set DEXBENCH_OUT for a new run" >&2
  exit 1
fi
wait_for_load
(cd "$repo_root/benchmark" && env GOWORK=off go build -o "$benchmark_binary" ./cmd/dexbench)
DEXBENCH_GIT_COMMIT="$(git -C "$repo_root" rev-parse HEAD)"
export DEXBENCH_GIT_COMMIT
export DEXBENCH_DISK_PATH="$run_dir"
cpu_model="$(awk -F: '/model name/{print $2; exit}' /proc/cpuinfo)"
cpu_count="$(getconf _NPROCESSORS_ONLN)"
ram_bytes="$(awk '/MemTotal/{print $2 * 1024; exit}' /proc/meminfo)"
free_disk_kb="$(df -Pk "$run_dir" | awk 'NR==2 {print $4}')"
printf 'cpu_model=%s\ncpu_count=%s\nram_bytes=%s\nfree_disk_kb=%s\ninstance_type=%s\nregion=%s\n' \
  "$cpu_model" "$cpu_count" "$ram_bytes" "$free_disk_kb" "$DEXBENCH_INSTANCE_TYPE" "$DEXBENCH_S3_REGION" > "$output_dir/host.txt"

temporal_version="$(sed -n 's/^TEMPORAL_VERSION=//p' "$repo_root/server/docker-compose/.env")"
printf 'temporal_image=temporalio/auto-setup:%s\npostgresql=%s\nelasticsearch=%s\n' "$temporal_version" \
  "$(sed -n 's/^POSTGRESQL_VERSION=//p' "$repo_root/server/docker-compose/.env")" \
  "$(sed -n 's/^ELASTICSEARCH_VERSION=//p' "$repo_root/server/docker-compose/.env")" >> "$output_dir/host.txt"
printf 'temporal_visibility=%s\n' "$temporal_visibility" >> "$output_dir/host.txt"

wait_for_server() {
  "$benchmark_binary" wait-ready --pid "$current_pid" --timeout 300s "$@"
}

stop_server() {
  kill "$current_pid" 2>/dev/null || true
  wait "$current_pid" 2>/dev/null || true
  current_pid=""
}

# container_cgroup prints the cgroup v2 directory that holds every process of one container.
container_cgroup() {
  local container_pid relative
  container_pid="$("${docker_command[@]}" inspect -f '{{.State.Pid}}' "$1")"
  relative="$(awk -F: '$1 == "0" {print $3}' "/proc/$container_pid/cgroup")"
  if [[ -z "$relative" || ! -f "/sys/fs/cgroup$relative/cpu.stat" ]]; then
    echo "container $1 has no cgroup v2 cpu.stat" >&2
    return 1
  fi
  printf '/sys/fs/cgroup%s' "$relative"
}

attribute_dsn='postgres://dex:dex@127.0.0.1:55432/dex?sslmode=disable'
export DEXBENCH_ATTRIBUTE_DSN="$attribute_dsn"
export DEXBENCH_GO_CONFIG="$run_dir/go-server.yaml"
export DEXBENCH_GO_BLOBS_PLACEHOLDER="__GO_BLOBS__"
python3 - "$install_root/server-development.yaml" <<'PY'
import os
import sys
import yaml

with open(sys.argv[1], encoding="utf-8") as source:
    config = yaml.safe_load(source)
config["blobStore"]["supportedStorages"] = [{"status": "active", "storageId": "benchmark-local", "storageType": "local", "localDirectory": os.environ["DEXBENCH_GO_BLOBS_PLACEHOLDER"]}]
config["interpreter"]["temporal"]["hostPort"] = "127.0.0.1:7233"
config["attributeStore"] = {"stores": {"reporting": {"type": "postgres", "dsn": os.environ["DEXBENCH_ATTRIBUTE_DSN"], "tableName": "public.benchmark_flow_attributes"}}}
with open(os.environ["DEXBENCH_GO_CONFIG"], "w", encoding="utf-8") as destination:
    yaml.safe_dump(config, destination)
PY

"${compose[@]}" up -d attribute-postgres
compose_started=1
for ((attempts=0; attempts<180; attempts++)); do
  if "${compose[@]}" exec -T attribute-postgres pg_isready -U dex -d dex >/dev/null 2>&1; then break; fi
  sleep 1
done
"${compose[@]}" exec -T attribute-postgres psql -U dex -d dex -v ON_ERROR_STOP=1 \
  -c 'CREATE TABLE IF NOT EXISTS public.benchmark_flow_attributes (flow_id TEXT PRIMARY KEY, benchmark_write BIGINT)' \
  -c 'CREATE TABLE IF NOT EXISTS dexzd_attribute_sync_fencing (store_name text NOT NULL, cluster_id text NOT NULL, scope text NOT NULL, fencing_token bigint NOT NULL, updated_at timestamptz NOT NULL, PRIMARY KEY (store_name, cluster_id, scope))'
attribute_cgroup="$(container_cgroup "$("${compose[@]}" ps -q attribute-postgres)")"

truncate_attribute_store() {
  "${compose[@]}" exec -T attribute-postgres psql -U dex -d dex -v ON_ERROR_STOP=1 \
    -c 'TRUNCATE public.benchmark_flow_attributes, dexzd_attribute_sync_fencing' >/dev/null
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
  while IFS= read -r variant; do variants+=("$variant"); done < <("$benchmark_binary" variants --scenarios "$normal_scenarios" --attribute-store-name reporting)
fi

write_zd_config() {
  local config_path="$1" data_dir="$2" prefix="$3" recovery="$4"
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
}

# Both prod targets run at the default scheduling priority, including Docker containers.
start_zd() {
  local data_dir="$1" prefix="$2" recovery="${3:-0}"
  local config_path="$run_dir/zd-config.yaml"
  write_zd_config "$config_path" "$data_dir" "$prefix" "$recovery"
  "$zd_binary" serve --config "$config_path" >> "$run_dir/zd-server.log" 2>&1 &
  current_pid=$!
}

# start_temporal recreates PostgreSQL, Elasticsearch, and Temporal with empty volumes.
start_temporal() {
  local step_dir="$1"
  "${compose[@]}" rm -s -f -v temporal elasticsearch postgresql >/dev/null 2>&1 || true
  "${compose[@]}" up -d "${temporal_services[@]}"
  for ((attempts=0; attempts<300; attempts++)); do
    if temporal operator namespace describe --namespace default --address 127.0.0.1:7233 >/dev/null 2>&1; then break; fi
    sleep 1
  done
  if ! temporal operator namespace describe --namespace default --address 127.0.0.1:7233 >/dev/null 2>&1; then
    echo "Temporal default namespace did not become available" >&2
    return 1
  fi
  sed "s#__GO_BLOBS__#$step_dir/go-blobs#" "$DEXBENCH_GO_CONFIG" > "$step_dir/go-server.yaml"
  "$server_binary" --config "$step_dir/go-server.yaml" start >> "$run_dir/go-server.log" 2>&1 &
  current_pid=$!
  wait_for_server
}

stop_temporal() {
  stop_server
  "${compose[@]}" rm -s -f -v temporal elasticsearch postgresql >/dev/null 2>&1 || true
}

run_step() {
  local target="$1" variant="$2" step_concurrency="$3" version="$4" cgroups="$5"
  local extra_args=()
  if [[ "$target" == zd ]]; then extra_args=(--metrics-url http://127.0.0.1:9901/metrics); fi
  wait_for_load
  DEXBENCH_SERVER_VERSION="$version" "$benchmark_binary" run \
    --target "$target" --engine "$target" --mode prod --region "$DEXBENCH_S3_REGION" \
    --instance-type "$DEXBENCH_INSTANCE_TYPE" --server-pid "$current_pid" --resource-cgroups "$cgroups" \
    --duration "$duration" --warmup "$warmup" --concurrency "$step_concurrency" \
    --workers "$workers" --steps "$steps" --scenarios "$variant" \
    --attribute-store-name reporting --out "$output_dir" "${extra_args[@]}"
}

# Every scenario variant and concurrency step starts its target with empty state.
IFS=, read -ra requested_targets <<< "$targets"
IFS=, read -ra requested_concurrency <<< "$concurrency"
for target in "${requested_targets[@]}"; do
  target="${target//[[:space:]]/}"
  for variant in ${variants[@]+"${variants[@]}"}; do
    for step_concurrency in "${requested_concurrency[@]}"; do
      step_dir="$run_dir/$target-$variant-c$step_concurrency"
      mkdir -p "$step_dir"
      truncate_attribute_store
      wait_for_load
      case "$target" in
        temporal)
          start_temporal "$step_dir"
          cgroups="$attribute_cgroup"
          temporal_containers=(temporal temporal-postgresql)
          if [[ "$temporal_visibility" == elasticsearch ]]; then temporal_containers+=(elasticsearch); fi
          for container in "${temporal_containers[@]}"; do
            cgroups+=",$(container_cgroup "$container")"
          done
          version="$(sed -n 's/^server=//p' "$install_root/releases.txt") with temporalio/auto-setup:$temporal_version, $temporal_visibility visibility"
          run_step "$target" "$variant" "$step_concurrency" "$version" "$cgroups"
          stop_temporal
          ;;
        zd)
          start_zd "$step_dir/zd-data" "$DEXBENCH_S3_PREFIX/$variant-c$step_concurrency-$(date -u +%s)"
          wait_for_server
          version="$(sed -n 's/^server-zd=//p' "$install_root/releases.txt")"
          run_step "$target" "$variant" "$step_concurrency" "$version" "$attribute_cgroup"
          stop_server
          ;;
        *) echo "unknown target $target" >&2; exit 2 ;;
      esac
      rm -rf "$step_dir"
    done
  done
done

if [[ "$has_recovery" == 1 && "$targets" == *zd* ]]; then
  recovery_sizes="${DEXBENCH_RECOVERY_MB:-100,500,1000}"
  IFS=, read -ra requested_sizes <<< "$recovery_sizes"
  for size in "${requested_sizes[@]}"; do
    data_dir="$run_dir/zd-recovery-$size"
    prefix="$DEXBENCH_S3_PREFIX/recovery-${size}mb-$(date -u +%s)"
    wait_for_load
    start_zd "$data_dir" "$prefix" 1
    wait_for_server
    wait_for_load
    "$benchmark_binary" seed-recovery --size-mb "$size" > "$run_dir/recovery-$size-seed.txt"
    stop_server
    # Removing local state makes this a cold-node restart that replays from S3.
    rm -rf "$data_dir"
    wait_for_load
    start_ns="$(python3 -c 'import time; print(time.time_ns())')"
    start_zd "$data_dir" "$prefix" 1
    elapsed="$(wait_for_server --started-unix-nano "$start_ns")"
    version="$(sed -n 's/^server-zd=//p' "$install_root/releases.txt")"
    DEXBENCH_SERVER_VERSION="$version" "$benchmark_binary" record-recovery --size-mb "$size" \
      --elapsed-seconds "$elapsed" --mode prod --region "$DEXBENCH_S3_REGION" \
      --instance-type "$DEXBENCH_INSTANCE_TYPE" --out "$output_dir/zd-recovery-${size}mb.json"
    stop_server
  done
fi

"$benchmark_binary" report "$output_dir"/*.json > "$output_dir/report.md"
"$benchmark_binary" chart "$output_dir"/*.json > "$output_dir/charts.html"
echo "results: $output_dir"
