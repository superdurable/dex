#!/usr/bin/env bash
set -euo pipefail
script_directory=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
exec "$script_directory/../../server-zd/integ/script/run-zd-integ-tests.sh" "$@"
