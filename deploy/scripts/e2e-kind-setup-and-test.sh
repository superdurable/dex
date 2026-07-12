#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "$0")" && pwd)/common.sh"

require_command kind
require_command kubectl
require_command helm
require_command docker
require_command python3

KIND_CLUSTER_NAME="${KIND_CLUSTER_NAME:-${KIND_CLUSTER_NAME_DEFAULT}}"
KIND_NAMESPACE="${KIND_NAMESPACE:-${KIND_NAMESPACE_DEFAULT}}"

SERVER_IMAGE="${SERVER_IMAGE:-dex-server:kind-validate}"
BENCHMARK_IMAGE="${BENCHMARK_IMAGE:-dex-benchmark-worker:kind-validate}"

SERVER_IMAGE_REPOSITORY="${SERVER_IMAGE_REPOSITORY:-dex-server}"
BENCHMARK_IMAGE_REPOSITORY="${BENCHMARK_IMAGE_REPOSITORY:-dex-benchmark-worker}"

SERVER_RELEASE="${SERVER_RELEASE:-${SERVER_RELEASE_DEFAULT}}"
BENCHMARK_RELEASE="${BENCHMARK_RELEASE:-${BENCHMARK_RELEASE_DEFAULT}}"

# Backend selects the persistence layer to deploy + validate against.
# postgres (default) or mongo — must match the server's DEX_DB_BACKEND.
DEX_DB_BACKEND="${DEX_DB_BACKEND:-postgres}"

KIND_CONFIG_FILE="${KIND_CONFIG_FILE:-deploy/kind/kind-cluster.yaml}"
KIND_MONGO_MANIFEST="${KIND_MONGO_MANIFEST:-deploy/kind/mongodb-replicaset.yaml}"
KIND_POSTGRES_MANIFEST="${KIND_POSTGRES_MANIFEST:-deploy/kind/postgres.yaml}"
KIND_POSTGRES_SCHEMA_DIR="${KIND_POSTGRES_SCHEMA_DIR:-server/internal/persistence/postgres/schema}"
KIND_SERVER_VALUES_FILE="${KIND_SERVER_VALUES_FILE:-deploy/kind/dex-values-kind.yaml}"
KIND_BENCHMARK_VALUES_FILE="${KIND_BENCHMARK_VALUES_FILE:-deploy/kind/benchmark-values-kind.yaml}"

if [[ "${DEX_DB_BACKEND}" != "postgres" && "${DEX_DB_BACKEND}" != "mongo" ]]; then
  echo "DEX_DB_BACKEND must be one of: postgres, mongo (got: ${DEX_DB_BACKEND})" >&2
  exit 1
fi

SEQUENTIAL_STEPS="${SEQUENTIAL_STEPS:-2}"
PARALLEL_STEPS="${PARALLEL_STEPS:-4}"
STATE_SIZE="${STATE_SIZE:-16}"
RUN_COMPLETION_TIMEOUT_SECONDS="${RUN_COMPLETION_TIMEOUT_SECONDS:-120}"
TRIGGER_RETRY_COUNT="${TRIGGER_RETRY_COUNT:-10}"
TRIGGER_RETRY_SLEEP_SECONDS="${TRIGGER_RETRY_SLEEP_SECONDS:-3}"

PORT_FORWARD_PORT="${PORT_FORWARD_PORT:-19129}"
PORT_FORWARD_PID=""
DATADOG_SECRET_NAME="${DATADOG_SECRET_NAME:-dex-datadog}"
DD_ENDPOINT="${DD_ENDPOINT:-api.datadoghq.com}"
TEMP_SERVER_VALUES_FILE=""

cleanup() {
  if [[ -n "${PORT_FORWARD_PID}" ]]; then
    kill "${PORT_FORWARD_PID}" >/dev/null 2>&1 || true
  fi
  if [[ -n "${TEMP_SERVER_VALUES_FILE}" && -f "${TEMP_SERVER_VALUES_FILE}" ]]; then
    rm -f "${TEMP_SERVER_VALUES_FILE}"
  fi
}
trap cleanup EXIT

usage() {
  cat <<'EOF'
Usage: deploy/scripts/e2e-kind-setup-and-test.sh [options]

This script performs a clean-room validation on a local kind cluster:
1. recreates the kind cluster
2. deploys the selected database backend (Postgres by default, or Mongo)
3. builds and loads server + benchmark images
4. deploys Helm charts
5. triggers sequential + parallel benchmark runs
6. verifies benchmark logs and run terminal states in the database

Options:
  --keep-cluster      Reuse the existing kind cluster instead of recreating it.
  --help              Show this help message.

Environment overrides:
  DEX_DB_BACKEND      postgres (default) or mongo
  KIND_CLUSTER_NAME
  KIND_NAMESPACE
  SERVER_IMAGE
  BENCHMARK_IMAGE
  SERVER_IMAGE_REPOSITORY
  BENCHMARK_IMAGE_REPOSITORY
  DD_API_KEY
  DATADOG_SECRET_NAME
  DD_ENDPOINT
  SEQUENTIAL_STEPS
  PARALLEL_STEPS
  STATE_SIZE
  RUN_COMPLETION_TIMEOUT_SECONDS
  TRIGGER_RETRY_COUNT
  TRIGGER_RETRY_SLEEP_SECONDS
  PORT_FORWARD_PORT
EOF
}

KEEP_CLUSTER=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --keep-cluster)
      KEEP_CLUSTER=1
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

repo_abs() {
  abs_path_from_repo "$1"
}

log_step() {
  echo
  echo "==> $1"
}

if [[ "${KEEP_CLUSTER}" -eq 0 ]]; then
  log_step "Recreating kind cluster ${KIND_CLUSTER_NAME}"
  kind delete cluster --name "${KIND_CLUSTER_NAME}" >/dev/null 2>&1 || true
  kind create cluster --name "${KIND_CLUSTER_NAME}" --config "$(repo_abs "${KIND_CONFIG_FILE}")"
fi

log_step "Building validation images"
docker build -f "$(repo_abs "server/Dockerfile")" -t "${SERVER_IMAGE}" "$(repo_abs ".")"
docker build -f "$(repo_abs "benchmark/Dockerfile")" -t "${BENCHMARK_IMAGE}" "$(repo_abs ".")"

kubectl create namespace "${KIND_NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f -

if [[ "${DEX_DB_BACKEND}" == "postgres" ]]; then
  log_step "Deploying PostgreSQL"
  # Pre-load the postgres image so the kind node never has to pull it.
  docker pull postgres:16 >/dev/null
  kind load docker-image --name "${KIND_CLUSTER_NAME}" postgres:16
  # The schema-init Job mounts the DDL (init.sh + v0.sql) from this ConfigMap.
  kubectl create configmap dex-pg-schema \
    --from-file="$(repo_abs "${KIND_POSTGRES_SCHEMA_DIR}")" \
    -n "${KIND_NAMESPACE}" \
    --dry-run=client -o yaml | kubectl apply -f -
  kubectl apply -n "${KIND_NAMESPACE}" -f "$(repo_abs "${KIND_POSTGRES_MANIFEST}")"
  kubectl rollout status "statefulset/postgres" -n "${KIND_NAMESPACE}" --timeout=180s
  kubectl wait --for=condition=ready pod -l app=postgres -n "${KIND_NAMESPACE}" --timeout=180s
  kubectl wait --for=condition=complete job/postgres-schema-init -n "${KIND_NAMESPACE}" --timeout=180s
  kubectl create secret generic "dex-postgres" \
    --from-literal=uri="postgres://dex:dex@postgres.${KIND_NAMESPACE}.svc.cluster.local:5432/?sslmode=disable" \
    -n "${KIND_NAMESPACE}" \
    --dry-run=client -o yaml | kubectl apply -f -
else
  log_step "Deploying MongoDB replica set"
  kubectl apply -n "${KIND_NAMESPACE}" -f "$(repo_abs "${KIND_MONGO_MANIFEST}")"
  kubectl rollout status "statefulset/mongodb" -n "${KIND_NAMESPACE}" --timeout=180s
  kubectl wait --for=condition=ready pod -l app=mongodb -n "${KIND_NAMESPACE}" --timeout=180s
  kubectl wait --for=condition=complete job/mongodb-rs-init -n "${KIND_NAMESPACE}" --timeout=180s
  kubectl create secret generic "dex-mongo" \
    --from-literal=uri="mongodb://mongodb.${KIND_NAMESPACE}.svc.cluster.local:27017/?replicaSet=rs0" \
    -n "${KIND_NAMESPACE}" \
    --dry-run=client -o yaml | kubectl apply -f -
fi

if [[ -n "${DD_API_KEY:-}" ]]; then
  log_step "Creating Datadog API key secret"
  kubectl create secret generic "${DATADOG_SECRET_NAME}" \
    --from-literal=api-key="${DD_API_KEY}" \
    -n "${KIND_NAMESPACE}" \
    --dry-run=client -o yaml | kubectl apply -f -

  TEMP_SERVER_VALUES_FILE="$(mktemp)"
  cat > "${TEMP_SERVER_VALUES_FILE}" <<EOF
secrets:
  datadog:
    existingSecret: ${DATADOG_SECRET_NAME}
server:
  config:
    metrics:
      provider: datadog
      datadog:
        apiKey: "\${DD_API_KEY}"
        endpoint: "${DD_ENDPOINT}"
EOF
fi

log_step "Loading images into kind"
kind load docker-image --name "${KIND_CLUSTER_NAME}" "${SERVER_IMAGE}" "${BENCHMARK_IMAGE}"

log_step "Deploying dex server"
helm upgrade --install "${SERVER_RELEASE}" "$(repo_abs "deploy/helm/dex")" \
  -n "${KIND_NAMESPACE}" \
  -f "$(repo_abs "${KIND_SERVER_VALUES_FILE}")" \
  ${TEMP_SERVER_VALUES_FILE:+-f "${TEMP_SERVER_VALUES_FILE}"} \
  --set "image.repository=${SERVER_IMAGE_REPOSITORY}" \
  --set "image.tag=${SERVER_IMAGE##*:}"

log_step "Deploying benchmark worker"
helm upgrade --install "${BENCHMARK_RELEASE}" "$(repo_abs "benchmark/helm/dex-benchmark")" \
  -n "${KIND_NAMESPACE}" \
  -f "$(repo_abs "${KIND_BENCHMARK_VALUES_FILE}")" \
  --set "image.repository=${BENCHMARK_IMAGE_REPOSITORY}" \
  --set "image.tag=${BENCHMARK_IMAGE##*:}"

log_step "Waiting for workloads"
kubectl rollout status "statefulset/${SERVER_RELEASE}" -n "${KIND_NAMESPACE}" --timeout=180s
kubectl rollout status "deployment/${BENCHMARK_RELEASE}" -n "${KIND_NAMESPACE}" --timeout=180s

log_step "Starting benchmark port-forward"
kubectl port-forward "deployment/${BENCHMARK_RELEASE}" "${PORT_FORWARD_PORT}:9123" -n "${KIND_NAMESPACE}" >/tmp/kind-benchmark-portforward.log 2>&1 &
PORT_FORWARD_PID=$!
sleep 3

trigger_run_once() {
  local mode="$1"
  local steps="$2"
  curl --silent "http://127.0.0.1:${PORT_FORWARD_PORT}/trigger?mode=${mode}&runs=1&numSteps=${steps}&stateSize=${STATE_SIZE}"
}

trigger_run() {
  local mode="$1"
  local steps="$2"

  local attempt=1
  while (( attempt <= TRIGGER_RETRY_COUNT )); do
    local response
    response="$(trigger_run_once "${mode}" "${steps}")"

    if python3 - "${response}" <<'PY'
import json, sys
payload = json.loads(sys.argv[1])
sys.exit(0 if "run_ids" in payload and payload["run_ids"] else 1)
PY
    then
      echo "${response}"
      return 0
    fi

    echo "Trigger attempt ${attempt}/${TRIGGER_RETRY_COUNT} for mode=${mode} did not return run_ids: ${response}" >&2
    if (( attempt == TRIGGER_RETRY_COUNT )); then
      return 1
    fi
    attempt=$((attempt + 1))
    sleep "${TRIGGER_RETRY_SLEEP_SECONDS}"
  done
}

log_step "Triggering sequential benchmark"
sequential_json="$(trigger_run sequential "${SEQUENTIAL_STEPS}")"
echo "${sequential_json}"

log_step "Triggering parallel benchmark"
parallel_json="$(trigger_run parallel "${PARALLEL_STEPS}")"
echo "${parallel_json}"

SEQUENTIAL_RUN_ID="$(python3 -c 'import json,sys; print(json.loads(sys.argv[1])["run_ids"][0])' "${sequential_json}")"
PARALLEL_RUN_ID="$(python3 -c 'import json,sys; print(json.loads(sys.argv[1])["run_ids"][0])' "${parallel_json}")"

# run_status echoes the integer status of a run (4 == completed) or
# "missing". Reads from whichever backend is deployed.
run_status() {
  local run_id="$1"
  if [[ "${DEX_DB_BACKEND}" == "postgres" ]]; then
    # The runs table lives in the dex_runs database; one row per run.
    kubectl exec -n "${KIND_NAMESPACE}" postgres-0 -- \
      psql -U dex -d dex_runs -tAc \
      "SELECT status FROM runs WHERE id='${run_id}'" 2>/dev/null | tr -d '[:space:]' | grep -E '^[0-9]+$' || echo "missing"
  else
    local result
    result="$(kubectl exec -n "${KIND_NAMESPACE}" mongodb-0 -- mongosh "mongodb://localhost:27017/dex?replicaSet=rs0" --quiet --eval \
      "const docs = db.runs.find({row_type:1,id:\"${run_id}\"},{_id:0,status:1}).toArray(); print(docs.length ? docs[0].status : 'missing');")"
    echo "${result}" | tr -d '[:space:]'
  fi
}

wait_for_run_completion() {
  local run_id="$1"
  local description="$2"
  local started_at
  started_at="$(date +%s)"

  while true; do
    local status
    status="$(run_status "${run_id}")"
    if [[ "${status}" == "4" ]]; then
      echo "${description} run ${run_id} completed"
      return 0
    fi

    now="$(date +%s)"
    if (( now - started_at > RUN_COMPLETION_TIMEOUT_SECONDS )); then
      echo "${description} run ${run_id} did not complete within ${RUN_COMPLETION_TIMEOUT_SECONDS}s (status=${status})" >&2
      return 1
    fi
    sleep 2
  done
}

log_step "Waiting for sequential run completion"
wait_for_run_completion "${SEQUENTIAL_RUN_ID}" "Sequential"

log_step "Waiting for parallel run completion"
wait_for_run_completion "${PARALLEL_RUN_ID}" "Parallel"

log_step "Validating benchmark logs"
benchmark_logs="$(kubectl logs "deployment/${BENCHMARK_RELEASE}" -n "${KIND_NAMESPACE}" --tail=1200)"
echo "${benchmark_logs}" | grep "run processing completed" | grep "${SEQUENTIAL_RUN_ID}" >/dev/null
echo "${benchmark_logs}" | grep "run processing completed" | grep "${PARALLEL_RUN_ID}" >/dev/null

log_step "Validation succeeded"
echo "Sequential run: ${SEQUENTIAL_RUN_ID}"
echo "Parallel run:   ${PARALLEL_RUN_ID}"
