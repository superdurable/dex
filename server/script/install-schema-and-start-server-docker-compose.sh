#!/bin/bash

# the script is used to start dex-server in docker-compose

CONFIG_PATH="${CONFIG_PATH:-/dex/config/development-postgres.yaml}"
SRC_ROOT="${SRC_ROOT:-/dex}"

"${SRC_ROOT}/dex-tools-postgres" --endpoint "postgres" install-schema

"${SRC_ROOT}/dex-tools-postgres" --endpoint "postgres" install-schema -f "${SRC_ROOT}/extensions/postgres/schema/sample_tables.sql"

"${SRC_ROOT}/dex-server" --config "${CONFIG_PATH}" "$@"
