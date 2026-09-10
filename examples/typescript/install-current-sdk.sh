#!/bin/bash

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

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd "$script_dir/../.." && pwd)
package_dir=$(mktemp -d)

cleanup() {
  rm -r "$package_dir"
}
trap cleanup EXIT

(
  cd "$repo_root/sdk-typescript"
  npm ci
  npm pack --pack-destination "$package_dir" >/dev/null
)

sdk_version=$(node -p "require('$repo_root/sdk-typescript/package.json').version")
sdk_package="$package_dir/superdurable-dex-${sdk_version}.tgz"
if [[ ! -f "$sdk_package" ]]; then
  echo "current TypeScript SDK package was not created" >&2
  exit 1
fi

cd "$script_dir"
npm install --no-save --package-lock=false "$sdk_package"
