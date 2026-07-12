#!/usr/bin/env bash
set -euo pipefail

# Escalating benchmark: trigger runs with wait=false, poll DB for completion.
# Usage: deploy/scripts/benchmark-escalation.sh [--namespace NS] [--context CTX]

KUBE_CONTEXT="${KUBE_CONTEXT:-kind-dex-e2e}"
KUBE_NS="${KUBE_NS:-dex-kind}"
BENCHMARK_PORT="${BENCHMARK_PORT:-19129}"
POLL_INTERVAL=5
MAX_WAIT=600  # 10 minutes per batch

RUN_COUNTS=(100 1000 2000 4000 10000)

mongo_query() {
  kubectl --context "${KUBE_CONTEXT}" exec -n "${KUBE_NS}" mongodb-0 -- \
    mongosh "mongodb://localhost:27017/dex?replicaSet=rs0" --quiet --eval "$1" 2>/dev/null
}

clean_db() {
  mongo_query 'db.runs.deleteMany({}); db.tasklist.deleteMany({}); print("DB cleaned")'
}

count_by_status() {
  mongo_query 'db.runs.aggregate([{$match:{row_type:1}},{$group:{_id:"$status",count:{$sum:1}}}]).forEach(d=>print(JSON.stringify(d)))'
}

trigger_runs() {
  local count=$1
  curl -s --max-time 60 "http://127.0.0.1:${BENCHMARK_PORT}/trigger?mode=parallel&runs=${count}&numSteps=4&stateSize=16"
}

wait_for_completion() {
  local expected=$1
  local started_at
  started_at=$(date +%s)

  while true; do
    local raw
    raw=$(count_by_status)
    local completed=0 running=0 waiting=0 pending=0 other=0
    while IFS= read -r line; do
      local status count
      status=$(echo "$line" | python3 -c "import json,sys; print(json.loads(sys.stdin.read())['_id'])")
      count=$(echo "$line" | python3 -c "import json,sys; print(json.loads(sys.stdin.read())['count'])")
      case "$status" in
        4) completed=$count ;;
        2) running=$count ;;
        1) waiting=$count ;;
        0) pending=$count ;;
        *) other=$((other + count)) ;;
      esac
    done <<< "$raw"

    local elapsed=$(( $(date +%s) - started_at ))
    echo "  [${elapsed}s] completed=${completed}/${expected} running=${running} waiting=${waiting} pending=${pending} other=${other}"

    if (( completed >= expected )); then
      echo "  All ${expected} runs completed in ${elapsed}s"
      return 0
    fi

    if (( elapsed > MAX_WAIT )); then
      echo "  TIMEOUT after ${MAX_WAIT}s — ${completed}/${expected} completed"
      return 1
    fi

    sleep "${POLL_INTERVAL}"
  done
}

echo "=== Benchmark Escalation ==="
echo "Context: ${KUBE_CONTEXT}, Namespace: ${KUBE_NS}"
echo ""

for count in "${RUN_COUNTS[@]}"; do
  echo "--- Cleaning DB ---"
  clean_db

  echo ""
  echo "=== Triggering ${count} runs ==="
  response=$(trigger_runs "$count" || echo '{"error":"trigger failed"}')
  triggered=$(echo "$response" | python3 -c "import json,sys; d=json.loads(sys.stdin.read()); print(len(d.get('run_ids',[])))" 2>/dev/null || echo "0")
  echo "  Trigger response: triggered ${triggered} runs"

  if (( triggered == 0 )); then
    echo "  WARNING: trigger returned 0 run_ids, StartRun may have failed. Skipping."
    continue
  fi

  wait_for_completion "$triggered"
  result=$?

  echo ""
  if (( result != 0 )); then
    echo "=== STOPPED at ${count} runs (${triggered} triggered, not all completed) ==="
    exit 1
  fi
done

echo "=== All benchmarks passed ==="
