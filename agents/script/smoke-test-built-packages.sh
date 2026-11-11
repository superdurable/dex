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

# A "cli/<name>" package lives in agents/cli/<language>/<name>. Dependencies come before dependents.
python_library_packages=(core openai-agents pydantic-ai langgraph google-adk claude-agent-sdk)
python_cli_packages=(cli/runtime cli/claude-sdk cli/openai-sdk cli/pydantic-ai cli/deepagents cli/google-adk cli/builtin)
python_packages=("${python_library_packages[@]}" "${python_cli_packages[@]}")
typescript_library_packages=(core vercel-ai openai-agents mastra claude-agent-sdk pi)
typescript_cli_packages=(cli/runtime cli/claude-sdk cli/openai-sdk cli/vercel-ai cli/mastra cli/builtin)
typescript_packages=("${typescript_library_packages[@]}" "${typescript_cli_packages[@]}")

run_python() {
  uv run --no-project --python 3.11 python "$@"
}

# Prints the source directory of a package in a language.
agents_package_dir() {
  local language="$1" package="$2"
  if [[ "$package" == cli/* ]]; then
    echo "$agents_dir/cli/$language/${package#cli/}"
  else
    echo "$agents_dir/$language/$package"
  fi
}

python_module_name() {
  local distribution
  distribution="$(python_distribution_name "$1")"
  echo "${distribution//-/_}"
}

python_distribution_name() {
  case "$1" in
    core) echo "dex-agents" ;;
    cli/runtime) echo "dex-agents-cli" ;;
    cli/*) echo "dex-agents-${1#cli/}-cli" ;;
    *) echo "dex-agents-$1" ;;
  esac
}

# Installs agents distributions, and the agents distributions they need, only from the built wheels.
install_built_python() {
  local venv="$1" constraints="$2"
  shift 2
  local requirements=()
  while IFS= read -r requirement; do
    requirements+=("$requirement")
  done < <(run_python "$agents_dir/script/built_python_wheels.py" requirements "$dist_dir/python" "$@")
  if [[ "${#requirements[@]}" -eq 0 ]]; then
    echo "found no built wheels for $*" >&2
    exit 1
  fi
  uv pip install --quiet --python "$venv" --constraint "$constraints" "${requirements[@]}"
  "$venv/bin/python" "$agents_dir/script/built_python_wheels.py" check "$dist_dir/python"
}

smoke_test_python() {
  local release_version
  release_version="$(run_python -c 'import sys, tomllib; print(tomllib.load(open(sys.argv[1], "rb"))["project"]["version"])' "$agents_dir/python/core/pyproject.toml")"
  echo "== Python agents packages $release_version"

  local package
  for package in "${python_packages[@]}"; do
    uv build --project "$(agents_package_dir python "$package")" --out-dir "$dist_dir/python" --no-sources
  done
  run_python "$agents_dir/script/verify_built_python_packages.py" "$dist_dir/python" "$release_version" "${python_packages[@]}"

  for package in "${python_library_packages[@]}"; do
    local project_dir="$work_dir/python-$package"
    local distribution
    distribution="$(python_distribution_name "$package")"
    mkdir -p "$project_dir"
    uv venv --quiet --python 3.11 "$project_dir/.venv"
    uv export --quiet --project "$agents_dir/python/$package" --frozen --no-dev --all-extras --no-emit-project \
      --no-emit-package dex-agents --no-hashes --output-file "$project_dir/constraints.txt"
    # The core installs first, then the adapter resolves dex-agents==<version> from the built wheels.
    install_built_python "$project_dir/.venv" "$project_dir/constraints.txt" dex-agents
    if [[ "$package" != "core" ]]; then
      install_built_python "$project_dir/.venv" "$project_dir/constraints.txt" "$distribution"
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
ui_stream = importlib.import_module("dex_agents.ui_stream")
if venv not in Path(ui_stream.__file__).resolve().parents:
    raise SystemExit(f"dex_agents.ui_stream imported from {ui_stream.__file__}, not the installed wheel")
PY
    )
    if [[ "$package" == "core" ]]; then
      smoke_test_python_core_modules "$project_dir" "$release_version"
      smoke_test_python_mcp_extra "$project_dir" "$release_version"
      smoke_test_python_connectors_extra "$project_dir" "$release_version"
    fi
  done

  echo "== Python session: dex-agents-pydantic-ai against dexcli dev --engine zd"
  local session_dir="$work_dir/python-pydantic-ai"
  cp -R "$agents_dir/python/pydantic-ai/tests" "$session_dir/tests"
  # The dev group names dex-agents[connectors] by its source path; install that extra from the built wheels instead.
  install_built_python "$session_dir/.venv" "$session_dir/constraints.txt" "dex-agents[connectors]"
  uv export --quiet --project "$agents_dir/python/pydantic-ai" --frozen --only-group dev --no-hashes \
    --no-emit-package dex-agents --output-file "$session_dir/test-requirements.txt"
  uv pip install --quiet --python "$session_dir/.venv" --requirement "$session_dir/test-requirements.txt"
  "$session_dir/.venv/bin/python" "$agents_dir/script/built_python_wheels.py" check "$dist_dir/python"
  (cd "$session_dir" && .venv/bin/python - <<'PY'
import importlib
from pathlib import Path

venv = Path(".venv").resolve()
for module_name in ("dex_agents", "dex_agents.connectors", "dex_agents_pydantic_ai"):
    module = importlib.import_module(module_name)
    if venv not in Path(module.__file__).resolve().parents:
        raise SystemExit(f"{module_name} imported from {module.__file__}, not the installed wheel")
print("the session tests import dex-agents[connectors] and the adapter from the built wheels")
PY
  )
  cat >"$session_dir/pytest.ini" <<'INI'
[pytest]
asyncio_mode = auto
asyncio_default_fixture_loop_scope = session
asyncio_default_test_loop_scope = session
INI
  (cd "$session_dir" && .venv/bin/python -m pytest -p no:cacheprovider -v tests/test_completed_turns.py)

  smoke_test_python_cli "$release_version"
}

# Installs each CLI package from the built wheels alone and runs its offline checks.
smoke_test_python_cli() {
  local release_version="$1"
  local package
  for package in "${python_cli_packages[@]}"; do
    echo "== Python CLI package $(python_distribution_name "$package")"
    local project_dir="$work_dir/python-${package//\//-}"
    local distribution
    distribution="$(python_distribution_name "$package")"
    mkdir -p "$project_dir/home"
    uv venv --quiet --python 3.11 "$project_dir/.venv"
    # Every agents package is a path dependency here, so the built wheels must provide them all.
    uv export --quiet --project "$(agents_package_dir python "$package")" --frozen --no-dev --all-extras \
      --no-emit-project --no-emit-local --no-hashes --output-file "$project_dir/constraints.txt"
    install_built_python "$project_dir/.venv" "$project_dir/constraints.txt" "$distribution"
    (cd "$project_dir" && .venv/bin/python - "$distribution" "$(python_module_name "$package")" <<'PY'
import importlib
import importlib.metadata
import sys
from pathlib import Path

distribution, module_name = sys.argv[1:]
venv = Path(".venv").resolve()
for name, module in dict([(distribution, module_name), ("dex-agents-cli", "dex_agents_cli"), ("dex-agents", "dex_agents")]).items():
    imported = importlib.import_module(module)
    if venv not in Path(imported.__file__).resolve().parents:
        raise SystemExit(f"{module} imported from {imported.__file__}, not the installed wheel")
    print(f"{name} {importlib.metadata.version(name)} imports from {Path(imported.__file__).parent}")
PY
    )
    [[ "$package" == "cli/runtime" ]] && continue
    smoke_test_cli_command "dex-agents-${package#cli/}" "$project_dir/.venv/bin" "$project_dir/home"
  done
}

# Runs --help and init --yes with a scratch HOME; neither reaches a model provider or server.
smoke_test_cli_command() {
  local command="$1" bin_dir="$2" home_dir="$3"
  local help_output
  help_output="$(HOME="$home_dir" "$bin_dir/$command" --help)"
  if [[ "$help_output" != *"$command"*init* ]]; then
    echo "$command --help printed no usage:" >&2
    echo "$help_output" >&2
    exit 1
  fi
  local config_path="$home_dir/$command.yaml"
  HOME="$home_dir" "$bin_dir/$command" init --yes --config "$config_path" </dev/null
  if [[ ! -s "$config_path" ]]; then
    echo "$command init --yes wrote no $config_path" >&2
    exit 1
  fi
  echo "$command --help and init --yes passed; init wrote $(wc -l <"$config_path" | tr -d ' ') lines"
}

# Imports every dex_agents module; dex_agents.litellm and dex_agents.s3 must name their extras until they are installed.
smoke_test_python_core_modules() {
  local project_dir="$1" release_version="$2"
  (cd "$project_dir" && .venv/bin/python - <<'PY'
import importlib

for module in ("dex_agents.builtin_loop", "dex_agents.stepwise", "dex_agents.testing"):
    importlib.import_module(module)
# Checked first: the litellm extra installs boto3 too.
try:
    importlib.import_module("dex_agents.s3")
except ImportError as error:
    if "dex-agents[s3]" not in str(error):
        raise SystemExit(f"dex_agents.s3 failed without naming its extra: {error}")
    print("dex_agents.s3 without the extra names dex-agents[s3]")
else:
    raise SystemExit("dex_agents.s3 imported without the s3 extra")
try:
    importlib.import_module("dex_agents.litellm")
except ImportError as error:
    if "dex-agents[litellm]" not in str(error):
        raise SystemExit(f"dex_agents.litellm failed without naming its extra: {error}")
    print("dex_agents.litellm without the extra names dex-agents[litellm]")
else:
    raise SystemExit("dex_agents.litellm imported without the litellm extra")
PY
  )
  install_built_python "$project_dir/.venv" "$project_dir/constraints.txt" "dex-agents[litellm]"
  (cd "$project_dir" && .venv/bin/python -c 'import dex_agents.litellm; print("dex_agents.litellm imports with the litellm extra")')
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
  install_built_python "$project_dir/.venv" "$project_dir/constraints.txt" "dex-agents[mcp]"
  (cd "$project_dir" && .venv/bin/python -c \
    'import dex_agents.mcp; print("dex_agents.mcp exports", ", ".join(dex_agents.mcp.__all__))')
}

# dex_agents.connectors imports with dex-agents[connectors], which installs the Connector SDK.
smoke_test_python_connectors_extra() {
  local project_dir="$1" release_version="$2"
  install_built_python "$project_dir/.venv" "$project_dir/constraints.txt" "dex-agents[connectors]"
  (cd "$project_dir" && .venv/bin/python -c \
    'import dex_agents.connectors; print("dex_agents.connectors exports", ", ".join(dex_agents.connectors.__all__))')
}

typescript_package_name() {
  case "$1" in
    core) echo "@superdurable/dex-agents" ;;
    cli/runtime) echo "@superdurable/dex-agents-cli" ;;
    cli/*) echo "@superdurable/dex-agents-${1#cli/}-cli" ;;
    *) echo "@superdurable/dex-agents-$1" ;;
  esac
}

# Prints the built tarball of a package.
typescript_tarball() {
  local package_name
  package_name="$(typescript_package_name "$1")"
  echo "$dist_dir/typescript/superdurable-${package_name#@superdurable/}-$2.tgz"
}

# Prints the built tarball of each agents package the package peer-depends on.
agents_peer_tarballs() {
  node --input-type=module - "$1" "$dist_dir/typescript" "$2" <<'NODE'
import { readFileSync } from "node:fs";
const [packageDir, tarballDir, releaseVersion] = process.argv.slice(2);
const manifest = JSON.parse(readFileSync(`${packageDir}/package.json`, "utf8"));
for (const name of Object.keys(manifest.peerDependencies ?? {})) {
  if (name === "@superdurable/dex-agents" || name.startsWith("@superdurable/dex-agents-")) {
    console.log(`${tarballDir}/superdurable-${name.slice("@superdurable/".length)}-${releaseVersion}.tgz`);
  }
}
NODE
}

# Prints name@version for each peer of the package, except the agents packages, at the version its
# package-lock.json tests.
locked_peer_specs() {
  node --input-type=module - "$1" <<'NODE'
import { readFileSync } from "node:fs";
const packageDir = process.argv[2];
const manifest = JSON.parse(readFileSync(`${packageDir}/package.json`, "utf8"));
const lock = JSON.parse(readFileSync(`${packageDir}/package-lock.json`, "utf8"));
const names = [...Object.keys(manifest.peerDependencies ?? {}), "zod", "typescript", "@types/node"];
for (const name of new Set(names)) {
  if (name === "@superdurable/dex-agents" || name.startsWith("@superdurable/dex-agents-")) continue;
  const locked = lock.packages[`node_modules/${name}`];
  if (locked !== undefined) console.log(`${name}@${locked.version}`);
}
NODE
}

# Prints name@version from a package's lockfile for each named package.
locked_package_specs() {
  node --input-type=module - "$@" <<'NODE'
import { readFileSync } from "node:fs";
const [packageDir, ...names] = process.argv.slice(2);
const lock = JSON.parse(readFileSync(`${packageDir}/package-lock.json`, "utf8"));
for (const name of names) {
  const locked = lock.packages[`node_modules/${name}`];
  if (locked === undefined) throw new Error(`${packageDir}/package-lock.json does not lock ${name}`);
  console.log(`${name}@${locked.version}`);
}
NODE
}

# Imports every core subpath without the optional peer ai, where ./ai-sdk must fail naming it, then with it.
smoke_test_typescript_core_subpaths() {
  local core_tarball="$1"
  local project_dir="$work_dir/typescript-core-subpaths"
  mkdir -p "$project_dir"
  printf '{"name":"agents-smoke-core-subpaths","private":true,"type":"module"}\n' >"$project_dir/package.json"
  local locked_specs=()
  while IFS= read -r peer_spec; do
    locked_specs+=("$peer_spec")
  done < <(locked_peer_specs "$agents_dir/typescript/core")
  local without_ai_specs=() ai_specs=()
  local peer_spec
  for peer_spec in "${locked_specs[@]}"; do
    case "$peer_spec" in
      ai@* | zod@*) ai_specs+=("$peer_spec") ;;
      *) without_ai_specs+=("$peer_spec") ;;
    esac
  done
  (
    cd "$project_dir"
    npm install --no-audit --no-fund "$core_tarball" "${without_ai_specs[@]}"
    if [[ -d node_modules/ai ]]; then
      echo "the optional peer ai was installed without being requested" >&2
      exit 1
    fi
    printf '%s\n' \
      'import * as core from "@superdurable/dex-agents";' \
      'import * as builtinLoop from "@superdurable/dex-agents/builtin-loop";' \
      'import * as stepwise from "@superdurable/dex-agents/stepwise";' \
      'import * as testing from "@superdurable/dex-agents/testing";' \
      'import * as connectors from "@superdurable/dex-agents/connectors";' \
      'export const exportedNames: string[] = [...Object.keys(core), ...Object.keys(builtinLoop), ...Object.keys(stepwise), ...Object.keys(testing), ...Object.keys(connectors)];' \
      >consumer.ts
    npx tsc --strict --noEmit --module NodeNext --moduleResolution NodeNext --target ES2022 --skipLibCheck \
      --types node consumer.ts
    node --input-type=module -e "
      for (const subpath of ['', '/builtin-loop', '/stepwise', '/testing', '/connectors']) {
        const module = await import('@superdurable/dex-agents' + subpath);
        if (Object.keys(module).length === 0) throw new Error('@superdurable/dex-agents' + subpath + ' exports nothing');
        console.log('@superdurable/dex-agents' + subpath, 'exports', Object.keys(module).join(', '));
      }
      try {
        await import('@superdurable/dex-agents/ai-sdk');
        throw new Error('@superdurable/dex-agents/ai-sdk imported without the optional peer ai');
      } catch (error) {
        if (!String(error).includes(\"'ai'\")) throw error;
        console.log('@superdurable/dex-agents/ai-sdk without ai fails with:', String(error).split('\\n')[0]);
      }
    "
    npm install --no-audit --no-fund "${ai_specs[@]}"
    printf '%s\n' \
      'import { createAiSdkModelClient } from "@superdurable/dex-agents/ai-sdk";' \
      'export const modelClientFactory: typeof createAiSdkModelClient = createAiSdkModelClient;' \
      >consumer-ai-sdk.ts
    npx tsc --strict --noEmit --module NodeNext --moduleResolution NodeNext --target ES2022 --skipLibCheck \
      --types node consumer-ai-sdk.ts
    node --input-type=module -e "
      const aiSdk = await import('@superdurable/dex-agents/ai-sdk');
      if (typeof aiSdk.createAiSdkModelClient !== 'function') throw new Error('ai-sdk lacks createAiSdkModelClient');
      console.log('@superdurable/dex-agents/ai-sdk exports', Object.keys(aiSdk).join(', '));
    "
  )
}

smoke_test_typescript() {
  local release_version
  release_version="$(node -p "require('$agents_dir/typescript/core/package.json').version")"
  echo "== TypeScript agents packages $release_version"

  local package
  # npm packs the file: dependencies for install-links=true, so each package builds after its dependencies.
  for package in "${typescript_packages[@]}"; do
    (
      cd "$(agents_package_dir typescript "$package")"
      npm ci --no-audit --no-fund
      npm run build
      npm pack --pack-destination "$dist_dir/typescript"
    )
  done
  node "$agents_dir/script/verify-built-npm-packages.mjs" "$dist_dir/typescript" "$release_version" "${typescript_packages[@]}"

  local core_tarball="$dist_dir/typescript/superdurable-dex-agents-$release_version.tgz"
  smoke_test_typescript_core_subpaths "$core_tarball"
  for package in "${typescript_library_packages[@]}"; do
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
      printf 'import * as adapter from "%s";\nimport * as uiStream from "@superdurable/dex-agents/ui-stream";\nexport const adapterExports: string[] = [...Object.keys(adapter), ...Object.keys(uiStream)];\n' "$package_name" >consumer.ts
      npx tsc --strict --noEmit --module NodeNext --moduleResolution NodeNext --target ES2022 --skipLibCheck \
        --types node consumer.ts
      node --input-type=module -e "
        const adapter = await import('$package_name');
        if (Object.keys(adapter).length === 0) throw new Error('$package_name exports nothing');
        console.log('$package_name exports', Object.keys(adapter).join(', '));
        const uiStream = await import('@superdurable/dex-agents/ui-stream');
        if (typeof uiStream.createAgentUIMessageStreamResponse !== 'function') {
          throw new Error('@superdurable/dex-agents/ui-stream lacks createAgentUIMessageStreamResponse');
        }
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
  # It tests a module the package does not export, so it cannot import it from the packed package.
  rm "$session_dir/test/integ/gen-ai-provider-names.integration.test.ts"
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
    "skipLibCheck": true,
    "resolveJsonModule": true
  },
  "include": ["test/**/*.ts"]
}
JSON
  # The copied tests read spans from an in-memory exporter, so they need the adapter's OpenTelemetry devDependencies.
  # Its connector tool tests build a fake Connector package, so they need the Connector SDK devDependency too.
  local test_dependency_specs=()
  while IFS= read -r test_dependency_spec; do
    test_dependency_specs+=("$test_dependency_spec")
  done < <(locked_package_specs "$agents_dir/typescript/vercel-ai" @opentelemetry/api @opentelemetry/sdk-trace-base \
    @superdurable/connectors-sdk)
  (
    cd "$session_dir"
    npm install --no-audit --no-fund "${test_dependency_specs[@]}"
    npx tsc -p tsconfig.json
    node --test --test-concurrency=1 --test-timeout=240000 \
      --test-name-pattern "a read tool runs in CallTool between two RunModel Steps" \
      dist/test/integ/session-turns.integration.test.js
  )

  smoke_test_typescript_cli "$release_version"
}

# Installs each CLI package from the built tarballs and runs its offline checks.
smoke_test_typescript_cli() {
  local release_version="$1"
  local package
  for package in "${typescript_cli_packages[@]}"; do
    local package_dir project_dir package_name
    package_dir="$(agents_package_dir typescript "$package")"
    project_dir="$work_dir/typescript-${package//\//-}"
    package_name="$(typescript_package_name "$package")"
    echo "== TypeScript CLI package $package_name"
    mkdir -p "$project_dir/home"
    printf '{"name":"agents-smoke-%s","private":true,"type":"module"}\n' "${package//\//-}" >"$project_dir/package.json"
    # One install, so npm takes every agents peer from the built tarballs instead of the registry.
    local install_specs=()
    while IFS= read -r install_spec; do
      install_specs+=("$install_spec")
    done < <(agents_peer_tarballs "$package_dir" "$release_version")
    install_specs+=("$(typescript_tarball "$package" "$release_version")")
    while IFS= read -r install_spec; do
      install_specs+=("$install_spec")
    done < <(locked_peer_specs "$package_dir")
    (
      cd "$project_dir"
      npm install --no-audit --no-fund "${install_specs[@]}"
      duplicates="$(find node_modules -path '*/@superdurable/*' -name package.json |
        { grep -E '(^|/)node_modules/@superdurable/dex-agents(-[a-z0-9-]+)?/package\.json$' || true; } |
        sed -E 's#.*/(@superdurable/[^/]+)/package\.json$#\1#' | sort | uniq -d)"
      if [[ -n "$duplicates" ]]; then
        echo "expected one installed copy of each agents package, found more of: $duplicates" >&2
        exit 1
      fi
      if [[ "$package" == "cli/runtime" ]]; then
        printf 'import { runAgentCli } from "%s";\nexport const run: typeof runAgentCli = runAgentCli;\n' \
          "$package_name" >consumer.ts
        npx tsc --strict --noEmit --module NodeNext --moduleResolution NodeNext --target ES2022 --skipLibCheck \
          --types node consumer.ts
        node --input-type=module -e "
          const runtime = await import('$package_name');
          if (typeof runtime.runAgentCli !== 'function') throw new Error('$package_name lacks runAgentCli');
          console.log('$package_name exports', Object.keys(runtime).join(', '));
        "
      else
        smoke_test_cli_command "dex-agents-${package#cli/}" "$project_dir/node_modules/.bin" "$project_dir/home"
      fi
    )
  done
}

"$DEXCLI" --version
if [[ "$languages" == "all" || "$languages" == "python" ]]; then
  smoke_test_python
fi
if [[ "$languages" == "all" || "$languages" == "typescript" ]]; then
  smoke_test_typescript
fi
echo "Built agents packages passed the smoke test."
