#!/usr/bin/env bash
# Copyright (c) 2025 superdurable
# SPDX-License-Identifier: MIT
#
# Creates the six per-store databases (idempotently) then applies v0.sql to
# create tables + indexes in each. Run by the postgres-init container against a
# freshly-started Postgres. Connection params come from the standard libpq env
# vars (PGHOST, PGPORT, PGUSER, PGPASSWORD).
set -euo pipefail

SCHEMA_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DATABASES=(dex_shards dex_runs dex_blobs dex_tasklists dex_visibility dex_history)

echo ">> Waiting for Postgres..."
for _ in $(seq 1 30); do
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

echo ">> Applying v0.sql"
psql -v ON_ERROR_STOP=1 -d postgres -f "${SCHEMA_DIR}/v0.sql"
echo ">> Postgres init complete."
