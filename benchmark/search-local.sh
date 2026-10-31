#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
backend="${1:?usage: search-local.sh builtin|postgres|paradedb RUNS ATTRIBUTES OUTPUT_DIRECTORY [--backfill]}"
runs="${2:?runs required}"
attributes="${3:?Attributes required}"
output_directory="${4:?output directory required}"
shift 4
case "$backend" in builtin|postgres|paradedb) ;; *) echo "unknown backend: $backend" >&2; exit 2;; esac
mkdir -p "$output_directory"
output_directory="$(cd "$output_directory" && pwd)"
run_directory="$(mktemp -d /tmp/dex-pgvis-bench-XXXXXX)"
container="dex-pgvis-bench-$backend-$$"
server_pid=""
has_container=false
cleanup() {
  if [[ -n "$server_pid" ]]; then
    kill "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
  if [[ "$has_container" == true ]]; then docker rm -fv "$container" > "$output_directory/container-removal.txt"; fi
  docker ps -a --filter "name=^${container}$" --format '{{.Names}}' >> "$output_directory/container-removal.txt"
  if [[ -f "$output_directory/server.log" ]]; then gzip "$output_directory/server.log"; fi
  rm -rf "$run_directory"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

binary="$repo_root/benchmark/bin/dexbench"
server_binary="$repo_root/server-zd/target/release/dex-server-zd"
address="127.0.0.1:${DEXBENCH_SEARCH_PORT:-18815}"
cat > "$output_directory/zd-config.yaml" <<YAML
cluster:
  partitionCount: ${DEXBENCH_PARTITIONS:-1024}
metrics:
  listen: 127.0.0.1:${DEXBENCH_SEARCH_METRICS_PORT:-19195}
search:
  backend: $backend
YAML
arguments=()
if [[ "$backend" != builtin ]]; then
  image=postgres:17
  connection_key=postgresConnectionString
  if [[ "$backend" == paradedb ]]; then image=paradedb/paradedb:0.26.1; connection_key=paradeDbConnectionString; fi
  docker run -d --name "$container" -e POSTGRES_PASSWORD=bench -e POSTGRES_DB=dexzd -p 127.0.0.1::5432 "$image" -c shared_buffers=256MB -c max_wal_size=4GB > "$output_directory/container-id.txt"
  has_container=true
  database_port="$(docker port "$container" 5432/tcp | awk -F: '{print $NF}')"
  for attempt in {1..60}; do
    if docker exec "$container" pg_isready -U postgres -d dexzd > /dev/null 2>&1; then break; fi
    sleep 1
  done
  printf '  %s: postgres://postgres:bench@127.0.0.1:%s/dexzd\n' "$connection_key" "$database_port" >> "$output_directory/zd-config.yaml"
  printf 'requested_image=%s\n' "$image" > "$output_directory/image.txt"
  docker image inspect "$image" --format 'architecture={{.Architecture}} id={{.Id}}' >> "$output_directory/image.txt"
  docker exec "$container" psql -U postgres -d dexzd -XAtc 'SELECT version()' > "$output_directory/database-version.txt"
  arguments+=(--database-container "$container")
fi
if [[ "$attributes" != 10 ]]; then arguments+=(--projection-only); fi
"$server_binary" --version > "$output_directory/server-version.txt"
"$server_binary" dev --data-dir "$run_directory/data" --listen "$address" --config "$output_directory/zd-config.yaml" > "$output_directory/server.log" 2>&1 &
server_pid=$!
"$binary" wait-ready --address "$address" --pid "$server_pid" --timeout 5m
"$binary" search --address "$address" --backend "$backend" --runs "$runs" --attributes "$attributes" --samples "${DEXBENCH_SEARCH_SAMPLES:-100}" --warmup 10 --concurrency "${DEXBENCH_SEARCH_CONCURRENCY:-128}" --git-commit "$(git -C "$repo_root" rev-parse HEAD)" --out "$output_directory/search.json" "${arguments[@]}" "$@" 2>&1 | tee "$output_directory/client.txt"
if [[ "$backend" == postgres && "$attributes" == 10 ]]; then
  cp "$repo_root/benchmark/search-explain.sql" "$output_directory/explain.sql"
  docker exec -i "$container" psql -U postgres -d dexzd -X < "$output_directory/explain.sql" > "$output_directory/explain.txt"
fi
if [[ "$has_container" == true ]]; then
  docker exec "$container" psql -U postgres -d dexzd -XAtc "SELECT count(*), count(*) FILTER (WHERE execution_status = 'Running') FROM dexzd_visibility_runs" > "$output_directory/projected-counts.txt"
  docker exec "$container" psql -U postgres -d dexzd -XAtc 'SELECT pg_size_pretty(pg_total_relation_size('\''dexzd_visibility_runs'\''))' > "$output_directory/table-size.txt"
fi
