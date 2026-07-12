#!/usr/bin/env bash

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

SERVER_IMAGE_REPOSITORY_DEFAULT="us-central1-docker.pkg.dev/your-project/your-repo/dex-server"
BENCHMARK_IMAGE_REPOSITORY_DEFAULT="us-central1-docker.pkg.dev/your-project/your-repo/dex-benchmark-worker"

SERVER_RELEASE_DEFAULT="dex"
BENCHMARK_RELEASE_DEFAULT="dex-benchmark"
NAMESPACE_DEFAULT="default"

SERVER_CHART_DIR_DEFAULT="deploy/helm/dex"
BENCHMARK_CHART_DIR_DEFAULT="benchmark/helm/dex-benchmark"

SERVER_VALUES_FILE_DEFAULT="deploy/helm/dex/values-gke-atlas.yaml"
BENCHMARK_VALUES_FILE_DEFAULT="benchmark/helm/dex-benchmark/values.yaml"
KIND_CLUSTER_NAME_DEFAULT="dex-e2e"
KIND_NAMESPACE_DEFAULT="dex-kind"

default_image_tag() {
  date -u +"%Y%m%d-%H%M%S"
}

require_command() {
  local command_name="$1"
  if ! command -v "${command_name}" >/dev/null 2>&1; then
    echo "Missing required command: ${command_name}" >&2
    exit 1
  fi
}

abs_path_from_repo() {
  local relative_path="$1"
  echo "${REPO_ROOT}/${relative_path}"
}
