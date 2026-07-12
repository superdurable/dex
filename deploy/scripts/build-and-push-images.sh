#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "$0")" && pwd)/common.sh"

require_command docker

IMAGE_TAG="${IMAGE_TAG:-$(default_image_tag)}"
SERVER_IMAGE_REPOSITORY="${SERVER_IMAGE_REPOSITORY:-${SERVER_IMAGE_REPOSITORY_DEFAULT}}"
BENCHMARK_IMAGE_REPOSITORY="${BENCHMARK_IMAGE_REPOSITORY:-${BENCHMARK_IMAGE_REPOSITORY_DEFAULT}}"
DOCKER_PLATFORM="${DOCKER_PLATFORM:-}"

BUILD_SERVER=1
BUILD_BENCHMARK=1
PUSH_IMAGES=1

usage() {
  cat <<'EOF'
Usage: deploy/scripts/build-and-push-images.sh [options]

Options:
  --tag TAG            Image tag to build and push.
  --platform PLATFORM  Docker platform (e.g. linux/amd64). Required for GKE
                       when building on Apple Silicon.
  --server-only        Build/push only the server image.
  --benchmark-only     Build/push only the benchmark worker image.
  --no-push            Build images locally without pushing.
  --help               Show this help message.

Environment overrides:
  SERVER_IMAGE_REPOSITORY
  BENCHMARK_IMAGE_REPOSITORY
  IMAGE_TAG
  DOCKER_PLATFORM
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --tag)
      IMAGE_TAG="$2"
      shift 2
      ;;
    --platform)
      DOCKER_PLATFORM="$2"
      shift 2
      ;;
    --server-only)
      BUILD_SERVER=1
      BUILD_BENCHMARK=0
      shift
      ;;
    --benchmark-only)
      BUILD_SERVER=0
      BUILD_BENCHMARK=1
      shift
      ;;
    --no-push)
      PUSH_IMAGES=0
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

PLATFORM_ARGS=()
if [[ -n "${DOCKER_PLATFORM}" ]]; then
  PLATFORM_ARGS=(--platform "${DOCKER_PLATFORM}")
fi

if [[ "${BUILD_SERVER}" -eq 1 ]]; then
  SERVER_IMAGE="${SERVER_IMAGE_REPOSITORY}:${IMAGE_TAG}"
  echo "Building server image: ${SERVER_IMAGE} (platform: ${DOCKER_PLATFORM:-native})"
  docker build "${PLATFORM_ARGS[@]}" -f "${REPO_ROOT}/server/Dockerfile" -t "${SERVER_IMAGE}" "${REPO_ROOT}"
  if [[ "${PUSH_IMAGES}" -eq 1 ]]; then
    echo "Pushing server image: ${SERVER_IMAGE}"
    docker push "${SERVER_IMAGE}"
  fi
fi

if [[ "${BUILD_BENCHMARK}" -eq 1 ]]; then
  BENCHMARK_IMAGE="${BENCHMARK_IMAGE_REPOSITORY}:${IMAGE_TAG}"
  echo "Building benchmark image: ${BENCHMARK_IMAGE} (platform: ${DOCKER_PLATFORM:-native})"
  docker build "${PLATFORM_ARGS[@]}" -f "${REPO_ROOT}/benchmark/Dockerfile" -t "${BENCHMARK_IMAGE}" "${REPO_ROOT}"
  if [[ "${PUSH_IMAGES}" -eq 1 ]]; then
    echo "Pushing benchmark image: ${BENCHMARK_IMAGE}"
    docker push "${BENCHMARK_IMAGE}"
  fi
fi

echo "Completed image build flow with tag: ${IMAGE_TAG}"
