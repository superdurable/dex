#!/usr/bin/env bash

set -euo pipefail

if [[ $# -ne 4 ]]; then
  echo "usage: $0 <dex-server-zd-binary> <total-partitions> <partition-num> <json-output-path>" >&2
  exit 2
fi

dex_server_zd_binary=$1
total_partitions=$2
partition_num=$3
json_output_path=$4

if [[ ! -x "$dex_server_zd_binary" ]]; then
  echo "dex-server-zd binary is not executable: $dex_server_zd_binary" >&2
  exit 2
fi

script_directory=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
cd "$script_directory/.."

export GOWORK=off
# Attribute Sync tests are behind this build tag; listing and running must agree.
export GOFLAGS="${GOFLAGS:+$GOFLAGS }-tags=attributestore_integration"

test_run_pattern=$(./script/select-integ-test-partition.sh "$total_partitions" "$partition_num")
mkdir -p "$(dirname "$json_output_path")"

set +e
go test -json ./integ -timeout 40m -run "$test_run_pattern" \
  -dex-server-binary="$dex_server_zd_binary" -dex-server-engine=zd |
  tee "$json_output_path" |
  jq --unbuffered -rj 'select(.Action == "output") | .Output'
pipeline_statuses=("${PIPESTATUS[@]}")
set -e

python3 "$script_directory/summarize-integ-test-json.py" "$json_output_path"

for pipeline_status in "${pipeline_statuses[@]}"; do
  if ((pipeline_status != 0)); then
    exit "$pipeline_status"
  fi
done
