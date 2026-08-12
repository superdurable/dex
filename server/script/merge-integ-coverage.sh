#!/usr/bin/env bash

# Copyright (c) 2022-2026 Super Durable, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -euo pipefail

if [[ $# -lt 2 ]]; then
  echo "usage: $0 <output-dir> <coverage-data-dir> [<coverage-data-dir> ...]" >&2
  exit 2
fi

output_dir=$1
shift
coverage_dirs=("$@")
shopt -s nullglob
for coverage_dir in "${coverage_dirs[@]}"; do
  if [[ ! -d "$coverage_dir" ]]; then
    echo "coverage data directory does not exist: $coverage_dir" >&2
    exit 1
  fi
  coverage_metadata=("$coverage_dir"/covmeta.*)
  coverage_counters=("$coverage_dir"/covcounters.*)
  if [[ ${#coverage_metadata[@]} -eq 0 || ${#coverage_counters[@]} -eq 0 ]]; then
    echo "coverage data directory is incomplete: $coverage_dir" >&2
    exit 1
  fi
done

coverage_inputs=$(IFS=,; echo "${coverage_dirs[*]}")
mkdir -p "$output_dir"
rm -f "$output_dir/coverage.out" "$output_dir/coverage.txt" "$output_dir/index.html"
go tool covdata textfmt -i="$coverage_inputs" -o="$output_dir/coverage.out"
go tool cover -func="$output_dir/coverage.out" | tee "$output_dir/coverage.txt"
go tool cover -html="$output_dir/coverage.out" -o="$output_dir/index.html"
