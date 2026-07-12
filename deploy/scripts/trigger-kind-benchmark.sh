#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "$0")" && pwd)/common.sh"

require_command kubectl
require_command python3

KIND_NAMESPACE="${KIND_NAMESPACE:-${KIND_NAMESPACE_DEFAULT}}"
BENCHMARK_RELEASE="${BENCHMARK_RELEASE:-${BENCHMARK_RELEASE_DEFAULT}}"
DEX_DB_BACKEND="${DEX_DB_BACKEND:-postgres}"
START_CONCURRENCY="${START_CONCURRENCY:-200}"

MODE="${MODE:-parallel}"
RUNS="${RUNS:-1}"
NUM_STEPS="${NUM_STEPS:-4}"
STATE_SIZE="${STATE_SIZE:-16}"
PORT_FORWARD_PORT="${PORT_FORWARD_PORT:-19129}"
RUN_COMPLETION_TIMEOUT_SECONDS="${RUN_COMPLETION_TIMEOUT_SECONDS:-120}"

WAIT_FOR_COMPLETION=0
PORT_FORWARD_PID=""

cleanup() {
  if [[ -n "${PORT_FORWARD_PID}" ]]; then
    kill "${PORT_FORWARD_PID}" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

usage() {
  cat <<'EOF'
Usage: deploy/scripts/trigger-kind-benchmark.sh [options]

Trigger benchmark runs against an already deployed benchmark worker in the local
kind validation namespace.

Options:
  --mode MODE         sequential or parallel.
  --runs N            Number of runs to start.
  --num-steps N       Number of steps per run.
  --state-size N      State payload size per step.
  --wait              Wait for all triggered runs to reach terminal completion.
  --help              Show this help message.

Environment overrides:
  DEX_DB_BACKEND      postgres (default) or mongo — for --wait verification
  START_CONCURRENCY   parallel StartRun fan-out at the worker (default 200)
  KIND_NAMESPACE
  BENCHMARK_RELEASE
  MODE
  RUNS
  NUM_STEPS
  STATE_SIZE
  PORT_FORWARD_PORT
  RUN_COMPLETION_TIMEOUT_SECONDS
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --mode)
      MODE="$2"
      shift 2
      ;;
    --runs)
      RUNS="$2"
      shift 2
      ;;
    --num-steps)
      NUM_STEPS="$2"
      shift 2
      ;;
    --state-size)
      STATE_SIZE="$2"
      shift 2
      ;;
    --wait)
      WAIT_FOR_COMPLETION=1
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

if [[ "${MODE}" != "sequential" && "${MODE}" != "parallel" ]]; then
  echo "--mode must be one of: sequential, parallel" >&2
  exit 1
fi

echo
echo "==> Starting benchmark port-forward"
kubectl port-forward "deployment/${BENCHMARK_RELEASE}" "${PORT_FORWARD_PORT}:9123" -n "${KIND_NAMESPACE}" >/tmp/kind-benchmark-portforward.log 2>&1 &
PORT_FORWARD_PID=$!
sleep 3

echo
echo "==> Triggering benchmark"
response="$(curl --silent "http://127.0.0.1:${PORT_FORWARD_PORT}/trigger?mode=${MODE}&runs=${RUNS}&numSteps=${NUM_STEPS}&stateSize=${STATE_SIZE}&startConcurrency=${START_CONCURRENCY}")"
echo "${response}"

# All run IDs for one trigger share the prefix "<startedAt>-<mode>-" (the
# server appends "<i>" per run), so we verify completion with a single
# aggregate count query keyed on that prefix instead of N per-run lookups.
# Parse via stdin (not argv): a 100K-run response is multi-MB and would
# blow past ARG_MAX as a command-line argument.
RUN_ID_PREFIX="$(printf '%s' "${response}" | python3 -c 'import json,sys; p=json.load(sys.stdin); print(f"{p["started_at"]}-{p["mode"]}-")')"
EXPECTED="$(printf '%s' "${response}" | python3 -c 'import json,sys; print(json.load(sys.stdin)["runs"])')"

if [[ "${WAIT_FOR_COMPLETION}" -eq 0 ]]; then
  exit 0
fi

# completed_count echoes how many runs with RUN_ID_PREFIX have reached
# status=4 (completed). Backend-aware: Postgres counts in dex_runs, Mongo
# counts run rows (row_type:1) in the dex database.
completed_count() {
  if [[ "${DEX_DB_BACKEND}" == "postgres" ]]; then
    kubectl exec -n "${KIND_NAMESPACE}" postgres-0 -- \
      psql -U dex -d dex_runs -tAc \
      "SELECT count(*) FROM runs WHERE status=4 AND id LIKE '${RUN_ID_PREFIX}%'" 2>/dev/null | tr -d '[:space:]'
  else
    kubectl exec -n "${KIND_NAMESPACE}" mongodb-0 -- \
      mongosh "mongodb://localhost:27017/dex?replicaSet=rs0" --quiet --eval \
      "print(db.runs.countDocuments({row_type:1,status:4,id:{\$regex:'^${RUN_ID_PREFIX}'}}));" 2>/dev/null | tr -d '[:space:]'
  fi
}

echo
echo "==> Waiting for ${EXPECTED} triggered runs to complete (prefix ${RUN_ID_PREFIX})"
deadline=$(( $(date +%s) + RUN_COMPLETION_TIMEOUT_SECONDS ))
while true; do
  done_count="$(completed_count)"
  done_count="${done_count:-0}"
  echo "  completed ${done_count}/${EXPECTED}"
  if [[ "${done_count}" == "${EXPECTED}" ]]; then
    echo "All ${EXPECTED} runs completed"
    break
  fi
  if (( $(date +%s) > deadline )); then
    echo "Timed out: only ${done_count}/${EXPECTED} runs completed within ${RUN_COMPLETION_TIMEOUT_SECONDS}s" >&2
    exit 1
  fi
  sleep 5
done
