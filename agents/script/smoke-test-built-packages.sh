#!/usr/bin/env bash

# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

# Builds every agents package, installs the built artifacts into fresh projects, and runs one session per language.
# Usage: smoke-test-built-packages.sh [all|python|typescript]. DEXCLI must name the dexcli binary.
# AGENTS_DIST_DIR, when set, keeps the built artifacts in that directory for publishing.

set -euo pipefail

languages="${1:-all}"
case "$languages" in
  all | python | typescript) ;;
  *)
    echo "usage: $0 [all|python|typescript]" >&2
    exit 2
    ;;
esac

if [[ -z "${DEXCLI:-}" || ! -x "${DEXCLI}" ]]; then
  echo "DEXCLI must name the dexcli binary" >&2
  exit 2
fi
DEXCLI="$(cd "$(dirname "$DEXCLI")" && pwd)/$(basename "$DEXCLI")"
export DEXCLI
export DEX_SERVER_ZD_BINARY="${DEX_SERVER_ZD_BINARY:-$(dirname "$DEXCLI")/dex-server-zd}"
if [[ ! -x "$DEX_SERVER_ZD_BINARY" ]]; then
  echo "dex-server-zd is not executable: $DEX_SERVER_ZD_BINARY" >&2
  exit 2
fi
export MASTRA_TELEMETRY_DISABLED=1

agents_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work_dir="$(mktemp -d "${TMPDIR:-/tmp}/dex-agents-smoke.XXXXXX")"
trap 'rm -rf "$work_dir"' EXIT
dist_dir="${AGENTS_DIST_DIR:-$work_dir/dist}"
mkdir -p "$dist_dir/python" "$dist_dir/typescript"
dist_dir="$(cd "$dist_dir" && pwd)"

python_packages=(core openai-agents pydantic-ai langgraph google-adk claude-agent-sdk)
typescript_packages=(core vercel-ai openai-agents mastra claude-agent-sdk)

run_python() {
  uv run --no-project --python 3.11 python "$@"
}

python_module_name() {
  if [[ "$1" == "core" ]]; then
    echo "dex_agents"
  else
    echo "dex_agents_${1//-/_}"
  fi
}

python_distribution_name() {
  if [[ "$1" == "core" ]]; then
    echo "dex-agents"
  else
    echo "dex-agents-$1"
  fi
}

smoke_test_python() {
  local release_version
  release_version="$(run_python -c 'import sys, tomllib; print(tomllib.load(open(sys.argv[1], "rb"))["project"]["version"])' "$agents_dir/python/core/pyproject.toml")"
  echo "== Python agents packages $release_version"

  local package
  for package in "${python_packages[@]}"; do
    uv build --project "$agents_dir/python/$package" --out-dir "$dist_dir/python" --no-sources
  done
  run_python "$agents_dir/script/verify_built_python_packages.py" "$dist_dir/python" "$release_version" "${python_packages[@]}"

  for package in "${python_packages[@]}"; do
    local project_dir="$work_dir/python-$package"
    local distribution
    distribution="$(python_distribution_name "$package")"
    mkdir -p "$project_dir"
    uv venv --quiet --python 3.11 "$project_dir/.venv"
    uv export --quiet --project "$agents_dir/python/$package" --frozen --no-dev --all-extras --no-emit-project \
      --no-emit-package dex-agents --no-hashes --output-file "$project_dir/constraints.txt"
    # The core installs first, then the adapter resolves dex-agents==<version> from the built wheels.
    uv pip install --quiet --python "$project_dir/.venv" --find-links "$dist_dir/python" \
      --constraint "$project_dir/constraints.txt" "dex-agents==$release_version"
    if [[ "$package" != "core" ]]; then
      uv pip install --quiet --python "$project_dir/.venv" --find-links "$dist_dir/python" \
        --constraint "$project_dir/constraints.txt" "$distribution==$release_version"
    fi
    (cd "$project_dir" && .venv/bin/python - "$distribution" "$(python_module_name "$package")" "$release_version" <<'PY'
import importlib
import importlib.metadata
import sys
from pathlib import Path

distribution, module_name, release_version = sys.argv[1:]
venv = Path(".venv").resolve()
for name, module in dict([("dex-agents", "dex_agents"), (distribution, module_name)]).items():
    imported = importlib.import_module(module)
    if venv not in Path(imported.__file__).resolve().parents:
        raise SystemExit(f"{module} imported from {imported.__file__}, not the installed wheel")
    installed = importlib.metadata.version(name)
    print(f"{name} {installed} imports from {Path(imported.__file__).parent}")
PY
    )
    if [[ "$package" == "core" ]]; then
      smoke_test_python_mcp_extra "$project_dir" "$release_version"
    fi
  done

  echo "== Python session: dex-agents-pydantic-ai against dexcli dev --engine zd"
  local session_dir="$work_dir/python-pydantic-ai"
  cp -R "$agents_dir/python/pydantic-ai/tests" "$session_dir/tests"
  uv export --quiet --project "$agents_dir/python/pydantic-ai" --frozen --only-group dev --no-hashes \
    --output-file "$session_dir/test-requirements.txt"
  uv pip install --quiet --python "$session_dir/.venv" --requirement "$session_dir/test-requirements.txt"
  cat >"$session_dir/pytest.ini" <<'INI'
[pytest]
asyncio_mode = auto
asyncio_default_fixture_loop_scope = session
asyncio_default_test_loop_scope = session
INI
  (cd "$session_dir" && .venv/bin/python -m pytest -p no:cacheprovider -v tests/test_completed_turns.py)
}

# dex_agents.mcp must name the extra without the MCP SDK, and import with dex-agents[mcp].
smoke_test_python_mcp_extra() {
  local project_dir="$1" release_version="$2"
  (cd "$project_dir" && .venv/bin/python - <<'PY'
try:
    import dex_agents.mcp
except ModuleNotFoundError as error:
    if "dex-agents[mcp]" not in str(error):
        raise
    print(f"dex_agents.mcp without the extra: {error}")
else:
    raise SystemExit("dex_agents.mcp imported without the mcp extra installed")
PY
  )
  uv pip install --quiet --python "$project_dir/.venv" --find-links "$dist_dir/python" \
    --constraint "$project_dir/constraints.txt" "dex-agents[mcp]==$release_version"
  (cd "$project_dir" && .venv/bin/python -c \
    'import dex_agents.mcp; print("dex_agents.mcp exports", ", ".join(dex_agents.mcp.__all__))')
}

typescript_package_name() {
  if [[ "$1" == "core" ]]; then
    echo "@superdurable/dex-agents"
  else
    echo "@superdurable/dex-agents-$1"
  fi
}

# Prints name@version for each peer of the adapter, at the version its package-lock.json tests.
locked_peer_specs() {
  node --input-type=module - "$1" <<'NODE'
import { readFileSync } from "node:fs";
const packageDir = process.argv[2];
const manifest = JSON.parse(readFileSync(`${packageDir}/package.json`, "utf8"));
const lock = JSON.parse(readFileSync(`${packageDir}/package-lock.json`, "utf8"));
const names = [...Object.keys(manifest.peerDependencies ?? {}), "zod", "typescript", "@types/node"];
for (const name of new Set(names)) {
  if (name === "@superdurable/dex-agents") continue;
  const locked = lock.packages[`node_modules/${name}`];
  if (locked !== undefined) console.log(`${name}@${locked.version}`);
}
NODE
}

smoke_test_typescript() {
  local release_version
  release_version="$(node -p "require('$agents_dir/typescript/core/package.json').version")"
  echo "== TypeScript agents packages $release_version"

  local package
  for package in "${typescript_packages[@]}"; do
    (
      cd "$agents_dir/typescript/$package"
      npm ci --no-audit --no-fund
      npm run build
      npm pack --pack-destination "$dist_dir/typescript"
    )
  done
  node "$agents_dir/script/verify-built-npm-packages.mjs" "$dist_dir/typescript" "$release_version" "${typescript_packages[@]}"

  local core_tarball="$dist_dir/typescript/superdurable-dex-agents-$release_version.tgz"
  for package in "${typescript_packages[@]}"; do
    [[ "$package" == "core" ]] && continue
    local project_dir="$work_dir/typescript-$package"
    local package_name
    package_name="$(typescript_package_name "$package")"
    mkdir -p "$project_dir"
    printf '{"name":"agents-smoke-%s","private":true,"type":"module"}\n' "$package" >"$project_dir/package.json"
    local peer_specs=()
    while IFS= read -r peer_spec; do
      peer_specs+=("$peer_spec")
    done < <(locked_peer_specs "$agents_dir/typescript/$package")
    (
      cd "$project_dir"
      npm install --no-audit --no-fund "$core_tarball"
      npm install --no-audit --no-fund \
        "$dist_dir/typescript/superdurable-dex-agents-$package-$release_version.tgz" "${peer_specs[@]}"
      copies="$(find node_modules -path '*/@superdurable/dex-agents/package.json' | wc -l | tr -d ' ')"
      if [[ "$copies" != "1" ]]; then
        echo "expected one installed @superdurable/dex-agents, found $copies" >&2
        exit 1
      fi
      printf 'import * as adapter from "%s";\nexport const adapterExports: string[] = Object.keys(adapter);\n' "$package_name" >consumer.ts
      npx tsc --strict --noEmit --module NodeNext --moduleResolution NodeNext --target ES2022 --skipLibCheck \
        --types node consumer.ts
      node --input-type=module -e "
        const adapter = await import('$package_name');
        if (Object.keys(adapter).length === 0) throw new Error('$package_name exports nothing');
        console.log('$package_name exports', Object.keys(adapter).join(', '));
      "
    )
  done

  echo "== TypeScript subpath @superdurable/dex-agents/mcp with its optional peer"
  local mcp_project_dir="$work_dir/typescript-core-mcp"
  mkdir -p "$mcp_project_dir"
  printf '{"name":"agents-smoke-core-mcp","private":true,"type":"module"}\n' >"$mcp_project_dir/package.json"
  local core_peer_specs=()
  while IFS= read -r peer_spec; do
    core_peer_specs+=("$peer_spec")
  done < <(locked_peer_specs "$agents_dir/typescript/core")
  (
    cd "$mcp_project_dir"
    npm install --no-audit --no-fund "$core_tarball" "${core_peer_specs[@]}"
    printf 'import { McpToolSource } from "@superdurable/dex-agents/mcp";\nexport const source = new McpToolSource([]);\n' >consumer.ts
    npx tsc --strict --noEmit --module NodeNext --moduleResolution NodeNext --target ES2022 --skipLibCheck \
      --types node consumer.ts
    node --input-type=module -e "
      const mcp = await import('@superdurable/dex-agents/mcp');
      if (typeof mcp.McpToolSource !== 'function') throw new Error('@superdurable/dex-agents/mcp lacks McpToolSource');
      console.log('@superdurable/dex-agents/mcp exports', Object.keys(mcp).join(', '));
    "
  )

  echo "== TypeScript session: @superdurable/dex-agents-vercel-ai against dexcli dev --engine zd"
  local session_dir="$work_dir/typescript-vercel-ai"
  cp -R "$agents_dir/typescript/vercel-ai/test" "$session_dir/test"
  find "$session_dir/test" -name '*.ts' -exec \
    perl -pi -e 's#"\.\./\.\./src/index\.js"#"\@superdurable/dex-agents-vercel-ai"#g' {} +
  if grep -rq '\.\./\.\./src/' "$session_dir/test"; then
    echo "the copied vercel-ai tests still import package sources" >&2
    exit 1
  fi
  cat >"$session_dir/tsconfig.json" <<'JSON'
{
  "compilerOptions": {
    "target": "ES2022",
    "module": "NodeNext",
    "moduleResolution": "NodeNext",
    "lib": ["ES2022"],
    "types": ["node"],
    "outDir": "dist",
    "rootDir": ".",
    "strict": true,
    "skipLibCheck": true
  },
  "include": ["test/**/*.ts"]
}
JSON
  (
    cd "$session_dir"
    npx tsc -p tsconfig.json
    node --test --test-concurrency=1 --test-timeout=240000 \
      --test-name-pattern "a read tool runs in CallTool between two RunModel Steps" \
      dist/test/integ/session-turns.integration.test.js
  )
}

"$DEXCLI" --version
if [[ "$languages" == "all" || "$languages" == "python" ]]; then
  smoke_test_python
fi
if [[ "$languages" == "all" || "$languages" == "typescript" ]]; then
  smoke_test_typescript
fi
echo "Built agents packages passed the smoke test."
