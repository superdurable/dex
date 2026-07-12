#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "$0")" && pwd)/common.sh"

require_command kind
require_command kubectl
require_command helm

KIND_CLUSTER_NAME="${KIND_CLUSTER_NAME:-${KIND_CLUSTER_NAME_DEFAULT}}"
KIND_NAMESPACE="${KIND_NAMESPACE:-${KIND_NAMESPACE_DEFAULT}}"

SERVER_RELEASE="${SERVER_RELEASE:-${SERVER_RELEASE_DEFAULT}}"
BENCHMARK_RELEASE="${BENCHMARK_RELEASE:-${BENCHMARK_RELEASE_DEFAULT}}"

DELETE_CLUSTER=1
DELETE_NAMESPACE=1
UNINSTALL_RELEASES=1

usage() {
  cat <<'EOF'
Usage: deploy/scripts/kind-cleanup.sh [options]

This script cleans up the local kind validation environment.

By default it:
1. uninstalls the Helm releases
2. deletes the validation namespace
3. deletes the kind cluster

Options:
  --keep-cluster       Keep the kind cluster after cleaning Kubernetes resources.
  --keep-namespace     Keep the namespace after uninstalling Helm releases.
  --skip-uninstall     Skip Helm uninstall and only delete namespace/cluster.
  --help               Show this help message.

Environment overrides:
  KIND_CLUSTER_NAME
  KIND_NAMESPACE
  SERVER_RELEASE
  BENCHMARK_RELEASE
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --keep-cluster)
      DELETE_CLUSTER=0
      shift
      ;;
    --keep-namespace)
      DELETE_NAMESPACE=0
      shift
      ;;
    --skip-uninstall)
      UNINSTALL_RELEASES=0
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

log_step() {
  echo
  echo "==> $1"
}

if [[ "${UNINSTALL_RELEASES}" -eq 1 ]]; then
  log_step "Uninstalling Helm releases from namespace ${KIND_NAMESPACE}"
  helm uninstall "${BENCHMARK_RELEASE}" -n "${KIND_NAMESPACE}" >/dev/null 2>&1 || true
  helm uninstall "${SERVER_RELEASE}" -n "${KIND_NAMESPACE}" >/dev/null 2>&1 || true
fi

if [[ "${DELETE_NAMESPACE}" -eq 1 ]]; then
  log_step "Deleting namespace ${KIND_NAMESPACE}"
  kubectl delete namespace "${KIND_NAMESPACE}" --ignore-not-found >/dev/null 2>&1 || true
fi

if [[ "${DELETE_CLUSTER}" -eq 1 ]]; then
  log_step "Deleting kind cluster ${KIND_CLUSTER_NAME}"
  kind delete cluster --name "${KIND_CLUSTER_NAME}" >/dev/null 2>&1 || true
  for _ in $(seq 1 20); do
    if ! kind get clusters | grep -x "${KIND_CLUSTER_NAME}" >/dev/null 2>&1; then
      break
    fi
    sleep 1
  done
fi

echo "kind cleanup complete"
