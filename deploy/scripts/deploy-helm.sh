#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "$0")" && pwd)/common.sh"

require_command helm
require_command kubectl

IMAGE_TAG="${IMAGE_TAG:-$(default_image_tag)}"
NAMESPACE="${NAMESPACE:-${NAMESPACE_DEFAULT}}"

SERVER_RELEASE="${SERVER_RELEASE:-${SERVER_RELEASE_DEFAULT}}"
BENCHMARK_RELEASE="${BENCHMARK_RELEASE:-${BENCHMARK_RELEASE_DEFAULT}}"

SERVER_IMAGE_REPOSITORY="${SERVER_IMAGE_REPOSITORY:-${SERVER_IMAGE_REPOSITORY_DEFAULT}}"
BENCHMARK_IMAGE_REPOSITORY="${BENCHMARK_IMAGE_REPOSITORY:-${BENCHMARK_IMAGE_REPOSITORY_DEFAULT}}"

SERVER_CHART_DIR="${SERVER_CHART_DIR:-${SERVER_CHART_DIR_DEFAULT}}"
BENCHMARK_CHART_DIR="${BENCHMARK_CHART_DIR:-${BENCHMARK_CHART_DIR_DEFAULT}}"

SERVER_VALUES_FILE="${SERVER_VALUES_FILE:-${SERVER_VALUES_FILE_DEFAULT}}"
BENCHMARK_VALUES_FILE="${BENCHMARK_VALUES_FILE:-${BENCHMARK_VALUES_FILE_DEFAULT}}"

SERVER_EXTRA_VALUES_FILE="${SERVER_EXTRA_VALUES_FILE:-}"
BENCHMARK_EXTRA_VALUES_FILE="${BENCHMARK_EXTRA_VALUES_FILE:-}"

DEPLOY_SERVER=1
DEPLOY_BENCHMARK=1

usage() {
  cat <<'EOF'
Usage: deploy/scripts/deploy-helm.sh [options]

Options:
  --tag TAG            Image tag to deploy.
  --namespace NS       Kubernetes namespace.
  --server-only        Deploy only the server chart.
  --benchmark-only     Deploy only the benchmark chart.
  --help               Show this help message.

Environment overrides:
  SERVER_RELEASE
  BENCHMARK_RELEASE
  SERVER_IMAGE_REPOSITORY
  BENCHMARK_IMAGE_REPOSITORY
  SERVER_VALUES_FILE
  BENCHMARK_VALUES_FILE
  SERVER_EXTRA_VALUES_FILE
  BENCHMARK_EXTRA_VALUES_FILE
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --tag)
      IMAGE_TAG="$2"
      shift 2
      ;;
    --namespace)
      NAMESPACE="$2"
      shift 2
      ;;
    --server-only)
      DEPLOY_SERVER=1
      DEPLOY_BENCHMARK=0
      shift
      ;;
    --benchmark-only)
      DEPLOY_SERVER=0
      DEPLOY_BENCHMARK=1
      shift
      ;;
    --help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      usage
      exit 1
      ;;
  esac
done

build_helm_args() {
  local base_values_file="$1"
  local extra_values_file="$2"

  local -a args
  args=(-f "$(abs_path_from_repo "${base_values_file}")")
  if [[ -n "${extra_values_file}" ]]; then
    args+=(-f "$(abs_path_from_repo "${extra_values_file}")")
  fi
  printf '%s\n' "${args[@]}"
}

if [[ "${DEPLOY_SERVER}" -eq 1 ]]; then
  echo "Deploying server chart ${SERVER_RELEASE} with tag ${IMAGE_TAG} to namespace ${NAMESPACE}"
  mapfile -t server_values_args < <(build_helm_args "${SERVER_VALUES_FILE}" "${SERVER_EXTRA_VALUES_FILE}")
  helm upgrade --install "${SERVER_RELEASE}" "$(abs_path_from_repo "${SERVER_CHART_DIR}")" \
    --namespace "${NAMESPACE}" \
    --create-namespace \
    "${server_values_args[@]}" \
    --set "image.repository=${SERVER_IMAGE_REPOSITORY}" \
    --set "image.tag=${IMAGE_TAG}"
fi

if [[ "${DEPLOY_BENCHMARK}" -eq 1 ]]; then
  local_run_service="${SERVER_RELEASE}:7233"
  local_matching_service="${SERVER_RELEASE}:7234"
  echo "Deploying benchmark chart ${BENCHMARK_RELEASE} with tag ${IMAGE_TAG} to namespace ${NAMESPACE}"
  mapfile -t benchmark_values_args < <(build_helm_args "${BENCHMARK_VALUES_FILE}" "${BENCHMARK_EXTRA_VALUES_FILE}")
  helm upgrade --install "${BENCHMARK_RELEASE}" "$(abs_path_from_repo "${BENCHMARK_CHART_DIR}")" \
    --namespace "${NAMESPACE}" \
    --create-namespace \
    "${benchmark_values_args[@]}" \
    --set "image.repository=${BENCHMARK_IMAGE_REPOSITORY}" \
    --set "image.tag=${IMAGE_TAG}" \
    --set "server.runServiceAddress=${local_run_service}" \
    --set "server.matchingServiceAddress=${local_matching_service}"
fi

echo "Completed Helm deploy flow with tag: ${IMAGE_TAG}"
