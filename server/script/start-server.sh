#!/bin/bash

CONFIG_PATH="${CONFIG_PATH:-/dex/config/development-postgres.yaml}"
SRC_ROOT="${SRC_ROOT:-/dex}"

"${SRC_ROOT}/dex-server" --config "${CONFIG_PATH}" "$@"
