set -euo pipefail

SCHEMA_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DATABASES=(dex_shards dex_runs)

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

echo ">> Applying v1.sql"
psql -v ON_ERROR_STOP=1 -d postgres -f "${SCHEMA_DIR}/v1.sql"
echo ">> Postgres init complete."
