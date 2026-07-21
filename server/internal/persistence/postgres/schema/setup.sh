set -euo pipefail

SCHEMA_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Optional suffix (arg or DB_SUFFIX) isolates DBs for parallel integ packages:
#   dex_shards_<suffix>, dex_runs_<suffix>, ...
SUFFIX="${1:-${DB_SUFFIX:-}}"
if [ -n "${SUFFIX}" ]; then
  if [[ ! "${SUFFIX}" =~ ^[A-Za-z0-9_]+$ ]]; then
    echo ">> Invalid DB suffix '${SUFFIX}' (use [A-Za-z0-9_]+ only)" >&2
    exit 1
  fi
  db_shards="dex_shards_${SUFFIX}"
  db_runs="dex_runs_${SUFFIX}"
  db_blobs="dex_blobs_${SUFFIX}"
  db_taskqueues="dex_taskqueues_${SUFFIX}"
else
  db_shards="dex_shards"
  db_runs="dex_runs"
  db_blobs="dex_blobs"
  db_taskqueues="dex_taskqueues"
fi
DATABASES=("${db_shards}" "${db_runs}" "${db_blobs}" "${db_taskqueues}")

echo ">> Waiting for Postgres..."
for _ in $(seq 1 60); do
  if psql -d postgres -c 'SELECT 1' >/dev/null 2>&1; then break; fi
  sleep 1
done

for db in "${DATABASES[@]}"; do
  exists=$(psql -d postgres -tAc "SELECT 1 FROM pg_database WHERE datname='${db}'")
  if [ "${exists}" != "1" ]; then
    echo ">> Creating database ${db}"
    psql -d postgres -c "CREATE DATABASE ${db}"
  fi
done

# Apply v1.sql, v2.sql, ... vN.sql in numeric order.
max_n=0
for path in "${SCHEMA_DIR}"/v*.sql; do
  [ -f "${path}" ] || continue
  base="$(basename "${path}")"
  if [[ "${base}" =~ ^v([0-9]+)\.sql$ ]]; then
    n="${BASH_REMATCH[1]}"
    if (( n > max_n )); then max_n=$n; fi
  fi
done
if (( max_n == 0 )); then
  echo ">> No vN.sql migrations found in ${SCHEMA_DIR}" >&2
  exit 1
fi

render_sql() {
  # Rewrite \connect targets so the same vN.sql works for suffixed DBs.
  sed \
    -e "s/^\\\\connect dex_shards$/\\\\connect ${db_shards}/" \
    -e "s/^\\\\connect dex_runs$/\\\\connect ${db_runs}/" \
    -e "s/^\\\\connect dex_blobs$/\\\\connect ${db_blobs}/" \
    -e "s/^\\\\connect dex_taskqueues$/\\\\connect ${db_taskqueues}/" \
    "$1"
}

for ((n = 1; n <= max_n; n++)); do
  file="v${n}.sql"
  path="${SCHEMA_DIR}/${file}"
  if [ ! -f "${path}" ]; then
    echo ">> Missing ${file} (expected contiguous v1..v${max_n})" >&2
    exit 1
  fi
  echo ">> Applying ${file} (shards=${db_shards})"
  render_sql "${path}" | psql -v ON_ERROR_STOP=1 -d postgres
done
echo ">> Postgres init complete."
