set -euo pipefail

SCHEMA_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DATABASES=(dex_shards dex_runs dex_blobs dex_taskqueues)

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

for ((n = 1; n <= max_n; n++)); do
  file="v${n}.sql"
  path="${SCHEMA_DIR}/${file}"
  if [ ! -f "${path}" ]; then
    echo ">> Missing ${file} (expected contiguous v1..v${max_n})" >&2
    exit 1
  fi
  echo ">> Applying ${file}"
  psql -v ON_ERROR_STOP=1 -d postgres -f "${path}"
done
echo ">> Postgres init complete."
