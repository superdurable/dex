#!/usr/bin/env bash

set -euo pipefail

if [[ $# -ne 4 && $# -ne 5 ]]; then
  echo "usage: $0 <dex-server-zd-binary> <total-partitions> <partition-num> <json-output-path> [test-allowlist]" >&2
  exit 2
fi

dex_server_zd_binary=$1
total_partitions=$2
partition_num=$3
json_output_path=$4
test_allowlist_path=${5:-}

if [[ ! -x "$dex_server_zd_binary" ]]; then
  echo "dex-server-zd binary is not executable: $dex_server_zd_binary" >&2
  exit 2
fi

script_directory=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
cd "$script_directory/.."

export GOWORK=off
# Attribute Sync tests are behind this build tag; listing and running must agree.
export GOFLAGS="${GOFLAGS:+$GOFLAGS }-tags=attributestore_integration"

partition_arguments=("$total_partitions" "$partition_num")
if [[ -n "$test_allowlist_path" ]]; then
  partition_arguments+=("$test_allowlist_path")
fi
test_run_pattern=$(./script/select-integ-test-partition.sh "${partition_arguments[@]}")
# An empty partition would let go test pass without running anything.
if [[ "$test_run_pattern" == '^$' ]]; then
  echo "partition $partition_num of $total_partitions selects no tests" >&2
  exit 2
fi
mkdir -p "$(dirname "$json_output_path")"
# The summary reports selected tests without events, such as those after a panic, as not run.
expected_tests_path="${json_output_path%.json}.tests.txt"
test_names=${test_run_pattern#^(}
test_names=${test_names%)\$}
tr '|' '\n' <<<"$test_names" >"$expected_tests_path"

set +e
go test -json . -timeout 40m -run "$test_run_pattern" \
  -dex-server-binary="$dex_server_zd_binary" |
  tee "$json_output_path" |
  jq --unbuffered -rj 'select(.Action == "output") | .Output'
pipeline_statuses=("${PIPESTATUS[@]}")
set -e

set +e
python3 "$script_directory/summarize-integ-test-json.py" --expected-tests "$expected_tests_path" "$json_output_path"
summary_status=$?
set -e

for pipeline_status in "${pipeline_statuses[@]}"; do
  if ((pipeline_status != 0)); then
    exit "$pipeline_status"
  fi
done
exit "$summary_status"
