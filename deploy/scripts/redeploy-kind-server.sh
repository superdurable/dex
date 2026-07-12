#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "$0")" && pwd)/common.sh"

require_command docker
require_command kind
require_command helm
require_command kubectl

KIND_CLUSTER_NAME="${KIND_CLUSTER_NAME:-${KIND_CLUSTER_NAME_DEFAULT}}"
KIND_NAMESPACE="${KIND_NAMESPACE:-${KIND_NAMESPACE_DEFAULT}}"

SERVER_RELEASE="${SERVER_RELEASE:-${SERVER_RELEASE_DEFAULT}}"
SERVER_IMAGE_REPOSITORY="${SERVER_IMAGE_REPOSITORY:-dex-server}"
SERVER_VALUES_FILE="${SERVER_VALUES_FILE:-deploy/kind/dex-values-kind.yaml}"
SERVER_EXTRA_VALUES_FILE="${SERVER_EXTRA_VALUES_FILE:-}"

IMAGE_TAG="${IMAGE_TAG:-$(default_image_tag)}"

WAIT_ROLLOUT=1

usage() {
  cat <<'EOF'
Usage: deploy/scripts/redeploy-kind-server.sh [options]

Build the local server image, load it into an existing kind cluster, and
redeploy only the server Helm release.

Options:
  --tag TAG            Image tag to use.
  --no-wait            Do not wait for the StatefulSet rollout to finish.
  --help               Show this help message.

Environment overrides:
  KIND_CLUSTER_NAME
  KIND_NAMESPACE
  SERVER_RELEASE
  SERVER_IMAGE_REPOSITORY
  SERVER_VALUES_FILE
  SERVER_EXTRA_VALUES_FILE
  IMAGE_TAG
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --tag)
      IMAGE_TAG="$2"
      shift 2
      ;;
    --no-wait)
      WAIT_ROLLOUT=0
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

echo
echo "==> Building server image ${SERVER_IMAGE_REPOSITORY}:${IMAGE_TAG}"
docker build -f "${REPO_ROOT}/server/Dockerfile" -t "${SERVER_IMAGE_REPOSITORY}:${IMAGE_TAG}" "${REPO_ROOT}"

echo
echo "==> Loading server image into kind cluster ${KIND_CLUSTER_NAME}"
kind load docker-image --name "${KIND_CLUSTER_NAME}" "${SERVER_IMAGE_REPOSITORY}:${IMAGE_TAG}"

echo
echo "==> Redeploying server release ${SERVER_RELEASE} in namespace ${KIND_NAMESPACE}"

helm_args=(-f "$(abs_path_from_repo "${SERVER_VALUES_FILE}")")
if [[ -n "${SERVER_EXTRA_VALUES_FILE}" ]]; then
  helm_args+=(-f "$(abs_path_from_repo "${SERVER_EXTRA_VALUES_FILE}")")
fi

helm upgrade --install "${SERVER_RELEASE}" "$(abs_path_from_repo "deploy/helm/dex")" \
  --namespace "${KIND_NAMESPACE}" \
  "${helm_args[@]}" \
  --set "image.repository=${SERVER_IMAGE_REPOSITORY}" \
  --set "image.tag=${IMAGE_TAG}"

if [[ "${WAIT_ROLLOUT}" -eq 1 ]]; then
  echo
  echo "==> Waiting for server rollout"
  kubectl rollout status "statefulset/${SERVER_RELEASE}" -n "${KIND_NAMESPACE}" --timeout=180s
fi

echo
echo "Server redeploy complete with tag ${IMAGE_TAG}"
