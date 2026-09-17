#!/usr/bin/env bash

set -euo pipefail

if [[ $# -ne 2 && $# -ne 3 ]]; then
  echo "usage: $0 <total-partitions> <partition-num> [test-allowlist]" >&2
  exit 2
fi

total_partitions=$1
partition_num=$2
test_allowlist_path=${3:-}

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
if [[ -n "$test_allowlist_path" && ! -f "$test_allowlist_path" ]]; then
  echo "test allowlist does not exist: $test_allowlist_path" >&2
  exit 2
fi

test_list=$(
  go test ./integ \
    -list '^Test' \
    -temporal=false \
    -cadence=false \
    -dependencyWaitSeconds=0
)

allowed_test_names=
if [[ -n "$test_allowlist_path" ]]; then
  while IFS= read -r test_name || [[ -n "$test_name" ]]; do
    if [[ -z "$test_name" || "$test_name" == \#* ]]; then
      continue
    fi
    if [[ ! "$test_name" =~ ^Test[[:alnum:]_]+$ ]]; then
      echo "invalid test name in allowlist: $test_name" >&2
      exit 2
    fi
    if grep -Fxq "$test_name" <<<"$allowed_test_names"; then
      echo "duplicate test name in allowlist: $test_name" >&2
      exit 2
    fi
    allowed_test_names+="$test_name"$'\n'
  done <"$test_allowlist_path"

  if [[ -z "$allowed_test_names" ]]; then
    echo "test allowlist is empty: $test_allowlist_path" >&2
    exit 2
  fi
  while IFS= read -r allowed_test_name; do
    if [[ -z "$allowed_test_name" ]]; then
      continue
    fi
    if ! grep -Fxq "$allowed_test_name" <<<"$test_list"; then
      echo "allowlisted test does not exist: $allowed_test_name" >&2
      exit 2
    fi
  done <<<"$allowed_test_names"
fi

selected_pattern=
selected_count=0
while IFS= read -r test_name; do
  if [[ ! "$test_name" =~ ^Test[[:alnum:]_]+$ ]]; then
    continue
  fi

  if [[ -n "$allowed_test_names" ]] && ! grep -Fxq "$test_name" <<<"$allowed_test_names"; then
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
