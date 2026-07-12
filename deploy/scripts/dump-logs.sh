#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "$0")" && pwd)/common.sh"

require_command kubectl

NAMESPACE="${NAMESPACE:-${KIND_NAMESPACE_DEFAULT}}"
SERVER_RELEASE="${SERVER_RELEASE:-${SERVER_RELEASE_DEFAULT}}"
BENCHMARK_RELEASE="${BENCHMARK_RELEASE:-${BENCHMARK_RELEASE_DEFAULT}}"
OUTPUT_DIR="${OUTPUT_DIR:-/tmp/dex-logs}"
TAIL_LINES="${TAIL_LINES:-10000}"

usage() {
  cat <<'EOF'
Usage: deploy/scripts/dump-logs.sh [options]

Dump logs from all server pods and benchmark worker pods into a local directory
for offline debugging.

Options:
  --namespace NS       Kubernetes namespace.
  --output-dir DIR     Directory to write log files to.
  --tail N             Number of lines per pod (default 10000).
  --help               Show this help message.

Environment overrides:
  NAMESPACE
  SERVER_RELEASE
  BENCHMARK_RELEASE
  OUTPUT_DIR
  TAIL_LINES
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --namespace)
      NAMESPACE="$2"
      shift 2
      ;;
    --output-dir)
      OUTPUT_DIR="$2"
      shift 2
      ;;
    --tail)
      TAIL_LINES="$2"
      shift 2
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

mkdir -p "${OUTPUT_DIR}"

echo "Dumping logs to ${OUTPUT_DIR} (namespace=${NAMESPACE}, tail=${TAIL_LINES})"

# Dump server pods
server_pods=$(kubectl get pods -n "${NAMESPACE}" -l "app.kubernetes.io/name=${SERVER_RELEASE}" -o jsonpath='{.items[*].metadata.name}' 2>/dev/null || true)
for pod in ${server_pods}; do
  echo "  ${pod} -> ${OUTPUT_DIR}/${pod}.log"
  kubectl logs "${pod}" -n "${NAMESPACE}" --tail="${TAIL_LINES}" > "${OUTPUT_DIR}/${pod}.log" 2>&1

  # Also grab previous container logs if the pod restarted
  kubectl logs "${pod}" -n "${NAMESPACE}" --previous --tail="${TAIL_LINES}" > "${OUTPUT_DIR}/${pod}.previous.log" 2>/dev/null || rm -f "${OUTPUT_DIR}/${pod}.previous.log"
done

# Dump benchmark worker pods
benchmark_pods=$(kubectl get pods -n "${NAMESPACE}" -l "app.kubernetes.io/name=${BENCHMARK_RELEASE}" -o jsonpath='{.items[*].metadata.name}' 2>/dev/null || true)
for pod in ${benchmark_pods}; do
  echo "  ${pod} -> ${OUTPUT_DIR}/${pod}.log"
  kubectl logs "${pod}" -n "${NAMESPACE}" --tail="${TAIL_LINES}" > "${OUTPUT_DIR}/${pod}.log" 2>&1
done

# Dump pod status
echo "  pod-status.txt"
kubectl get pods -n "${NAMESPACE}" -o wide > "${OUTPUT_DIR}/pod-status.txt" 2>&1

# Summary counts
echo
echo "=== Summary ==="
total_files=$(ls -1 "${OUTPUT_DIR}"/*.log 2>/dev/null | wc -l)
echo "Log files: ${total_files}"

for log_file in "${OUTPUT_DIR}"/*.log; do
  name=$(basename "${log_file}")
  lines=$(wc -l < "${log_file}")
  panics=$(grep -c "panic" "${log_file}" 2>/dev/null || true)
  errors=$(grep -c "level=ERROR" "${log_file}" 2>/dev/null || true)
  printf "  %-50s  lines=%-6s  errors=%-4s  panics=%s\n" "${name}" "${lines}" "${errors}" "${panics}"
done

echo
echo "Logs written to ${OUTPUT_DIR}"
