#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "$0")" && pwd)/common.sh"

IMAGE_TAG="${IMAGE_TAG:-$(default_image_tag)}"

BUILD_ARGS=()
DEPLOY_ARGS=()

usage() {
  cat <<'EOF'
Usage: deploy/scripts/release.sh [options]

This script builds and pushes images, then redeploys the Helm releases using
the same image tag.

Options:
  --tag TAG            Image tag to use for both build and deploy.
  --namespace NS       Kubernetes namespace for Helm deploy.
  --server-only        Operate only on the server component.
  --benchmark-only     Operate only on the benchmark component.
  --no-push            Build images locally without pushing, then deploy.
  --help               Show this help message.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --tag)
      IMAGE_TAG="$2"
      BUILD_ARGS+=("$1" "$2")
      DEPLOY_ARGS+=("$1" "$2")
      shift 2
      ;;
    --namespace)
      DEPLOY_ARGS+=("$1" "$2")
      shift 2
      ;;
    --server-only|--benchmark-only|--no-push)
      BUILD_ARGS+=("$1")
      if [[ "$1" != "--no-push" ]]; then
        DEPLOY_ARGS+=("$1")
      fi
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

if [[ ! " ${BUILD_ARGS[*]} " =~ " --tag " ]]; then
  BUILD_ARGS=(--tag "${IMAGE_TAG}" "${BUILD_ARGS[@]}")
fi
if [[ ! " ${DEPLOY_ARGS[*]} " =~ " --tag " ]]; then
  DEPLOY_ARGS=(--tag "${IMAGE_TAG}" "${DEPLOY_ARGS[@]}")
fi

"${SCRIPT_DIR}/build-and-push-images.sh" "${BUILD_ARGS[@]}"
"${SCRIPT_DIR}/deploy-helm.sh" "${DEPLOY_ARGS[@]}"

echo "Release completed with tag: ${IMAGE_TAG}"
