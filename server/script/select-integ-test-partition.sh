#!/usr/bin/env bash

set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "usage: $0 <total-partitions> <partition-num>" >&2
  exit 2
fi

total_partitions=$1
partition_num=$2

if [[ ! "$total_partitions" =~ ^[1-9][0-9]*$ ]]; then
  echo "total-partitions must be a positive integer: $total_partitions" >&2
  exit 2
fi
if [[ ! "$partition_num" =~ ^(0|[1-9][0-9]*)$ ]]; then
  echo "partition-num must be a non-negative integer: $partition_num" >&2
  exit 2
fi
if ((partition_num >= total_partitions)); then
  echo "partition-num must be less than total-partitions: $partition_num >= $total_partitions" >&2
  exit 2
fi

test_list=$(
  go test ./integ \
    -list '^Test' \
    -temporal=false \
    -cadence=false \
    -dependencyWaitSeconds=0
)

selected_pattern=
selected_count=0
while IFS= read -r test_name; do
  if [[ ! "$test_name" =~ ^Test[[:alnum:]_]+$ ]]; then
    continue
  fi

  checksum_and_size=$(printf '%s' "$test_name" | cksum)
  checksum=${checksum_and_size%% *}
  if ((checksum % total_partitions != partition_num)); then
    continue
  fi

  if [[ -n "$selected_pattern" ]]; then
    selected_pattern="${selected_pattern}|"
  fi
  selected_pattern="${selected_pattern}${test_name}"
  selected_count=$((selected_count + 1))
  printf '  %s\n' "$test_name" >&2
done <<<"$test_list"

printf 'Selected %d tests for partition %d of %d.\n' \
  "$selected_count" "$partition_num" "$total_partitions" >&2

if [[ -z "$selected_pattern" ]]; then
  printf '^$'
else
  printf '^(%s)$' "$selected_pattern"
fi
