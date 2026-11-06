#!/usr/bin/env bash
# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

# Writes one hashed lock per Python SDK version in api-reference-versions.json.
# Usage: lock-python-dependencies.sh <cutoff date, at least two weeks ago, such as 2026-10-20>

set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "Usage: $0 <cutoff date, at least two weeks ago>" >&2
  exit 2
fi
cutoff_date="$1"
tools_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
docs_directory="$(cd "${tools_directory}/../.." && pwd)"
lock_directory="${tools_directory}/python"
input_directory="$(mktemp -d)"
trap 'rm -rf "${input_directory}"' EXIT

sdk_versions="$(node -e 'console.log(require(process.argv[1]).python.join(" "))' "${docs_directory}/api-reference-versions.json")"
mkdir -p "${lock_directory}"
rm -f "${lock_directory}"/*.txt
for sdk_version in ${sdk_versions}; do
  input_path="${input_directory}/dex-python-sdk-${sdk_version}.in"
  cat "${tools_directory}/pdoc-requirements.in" > "${input_path}"
  echo "dex-python-sdk==${sdk_version}" >> "${input_path}"
  # The cutoff applies to tools and SDK dependencies; the SDK release itself is pinned exactly.
  uv pip compile "${input_path}" --quiet --generate-hashes --universal --python-version 3.12 \
    --no-annotate --exclude-newer "${cutoff_date}" \
    --exclude-newer-package "dex-python-sdk=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    --custom-compile-command "scripts/api-reference-tools/lock-python-dependencies.sh ${cutoff_date}" \
    --output-file "${lock_directory}/${sdk_version}.txt"
done
