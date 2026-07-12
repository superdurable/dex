#!/usr/bin/env bash
# dev-stack.sh — bring up the dex stack, start the WebUI in the background,
# and seed runs for local WebUI dev.
#
# Run from the repo root:
#   ./dev-stack.sh
#
# The persistence backend defaults to Postgres; only that DB is started. To run
# against Mongo instead:
#   DEX_DB_BACKEND=mongo ./dev-stack.sh
#
# By default the script starts `npm run dev` in the BACKGROUND right after the
# docker compose stack is healthy, so the WebUI is rendering by the time you
# scroll down to see the agent-flow trigger URLs at the end. To skip the
# background WebUI (e.g. you already have it running in another terminal):
#   START_WEBUI=0 ./dev-stack.sh
#
# Tear down docker stack with the command printed at the end of this script.
# Stop background WebUI: kill the PID printed in the "WebUI started" line, or
#   pkill -f "next dev"
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT="${SCRIPT_DIR}"
WEB_DIR="${REPO_ROOT}/web"
PROJECT_NAME="${DEX_E2E_PROJECT:-dex-web-e2e}"

# Pick the persistence backend (postgres default, or mongo). Only the selected
# backend's database is started — the base compose has no DB, and we layer the
# matching override on top.
DB_BACKEND="${DEX_DB_BACKEND:-postgres}"
BACKEND_COMPOSE="${WEB_DIR}/docker-compose.${DB_BACKEND}.yaml"
if [ ! -f "${BACKEND_COMPOSE}" ]; then
  echo "ERROR: unknown DEX_DB_BACKEND=${DB_BACKEND} (expected 'postgres' or 'mongo')" >&2
  exit 1
fi
# COMPOSE is the docker-compose invocation prefix (base + backend override).
COMPOSE=(docker compose -f "${WEB_DIR}/docker-compose.e2e.yaml" -f "${BACKEND_COMPOSE}" -p "${PROJECT_NAME}")
echo ">> Persistence backend: ${DB_BACKEND}"

# Exported so docker compose substitutes them into the published host ports
# (see web/docker-compose.e2e.yaml). Override any of these to dodge a host
# port already taken by an unrelated local process, e.g. BENCH_PORT=9124.
export OPS_PORT="${OPS_PORT:-7235}"
export RUN_PORT="${RUN_PORT:-7233}"
export MATCHING_PORT="${MATCHING_PORT:-7234}"
export BENCH_PORT="${BENCH_PORT:-9123}"
RUNS="${RUNS:-5}"
NUM_STEPS="${NUM_STEPS:-3}"
STATE_SIZE="${STATE_SIZE:-64}"
START_WEBUI="${START_WEBUI:-1}"
WEBUI_PORT_PREFERRED="${WEBUI_PORT_PREFERRED:-3000}"
WEBUI_LOG="${WEBUI_LOG:-/tmp/dex-webui-dev.log}"

echo ">> Bringing up docker compose stack (project=${PROJECT_NAME})..."
"${COMPOSE[@]}" up -d --build

echo ">> Waiting for OpsService gRPC on 127.0.0.1:${OPS_PORT}..."
for i in $(seq 1 90); do
  if (echo > "/dev/tcp/127.0.0.1/${OPS_PORT}") 2>/dev/null; then
    echo "   OpsService is up."
    break
  fi
  if [ "$i" = "90" ]; then
    echo "   ERROR: OpsService did not come up in time." >&2
    "${COMPOSE[@]}" logs --tail=50 server >&2
    exit 1
  fi
  sleep 1
done

echo ">> Waiting for benchmark worker on 127.0.0.1:${BENCH_PORT}..."
for i in $(seq 1 60); do
  if curl -fs "http://127.0.0.1:${BENCH_PORT}/healthz" >/dev/null 2>&1; then
    echo "   Benchmark worker is up."
    break
  fi
  if [ "$i" = "60" ]; then
    echo "   ERROR: Benchmark worker did not come up in time." >&2
    "${COMPOSE[@]}" logs --tail=50 benchmark >&2
    exit 1
  fi
  sleep 1
done

# ---------------------------------------------------------------------------
# Start the WebUI in the background BEFORE seeding any runs, so it's already
# warm by the time you open it. Capture the port Next.js actually picked
# (it auto-bumps when WEBUI_PORT_PREFERRED is taken). The WEBUI_PORT we
# resolve here is also what the playground URLs at the end of the script
# reference.
# ---------------------------------------------------------------------------
WEBUI_PORT=""
WEBUI_PID=""
if [ "${START_WEBUI}" != "1" ]; then
  echo ">> Skipping background WebUI (START_WEBUI=0). Start manually with:"
  echo "      cd ${WEB_DIR} && OPS_SERVICE_ADDRESS=127.0.0.1:${OPS_PORT} RUN_SERVICE_ADDRESS=127.0.0.1:${RUN_PORT} npm run dev"
  WEBUI_PORT="${WEBUI_PORT_PREFERRED}"
elif curl -fs "http://127.0.0.1:${WEBUI_PORT_PREFERRED}/" -o /dev/null -m 2 2>/dev/null; then
  # If something is already serving the preferred port (e.g. you re-ran
  # the script and a previous background dev server is still up), don't
  # pile on another instance — just reuse it.
  echo ">> WebUI already serving on http://localhost:${WEBUI_PORT_PREFERRED} — reusing."
  WEBUI_PORT="${WEBUI_PORT_PREFERRED}"
else
  echo ">> Starting WebUI (npm run dev) in the background — log at ${WEBUI_LOG}..."
  : > "${WEBUI_LOG}"
  (
    cd "${WEB_DIR}"
    OPS_SERVICE_ADDRESS="127.0.0.1:${OPS_PORT}" \
      RUN_SERVICE_ADDRESS="127.0.0.1:${RUN_PORT}" \
      PORT="${WEBUI_PORT_PREFERRED}" \
      nohup npm run dev > "${WEBUI_LOG}" 2>&1 &
    echo $! > /tmp/dex-webui-dev.pid
  )
  WEBUI_PID=$(cat /tmp/dex-webui-dev.pid 2>/dev/null || echo "")
  # Wait up to 60s for next dev to print the "Local: http://localhost:XXXX"
  # line. The actual port may differ from WEBUI_PORT_PREFERRED if it was
  # already in use — Next.js auto-bumps to the next free port.
  for i in $(seq 1 60); do
    if [ -s "${WEBUI_LOG}" ] && grep -qE "Local:\s+http://localhost:[0-9]+" "${WEBUI_LOG}"; then
      WEBUI_PORT=$(grep -oE "Local:\s+http://localhost:[0-9]+" "${WEBUI_LOG}" | grep -oE "[0-9]+$" | tail -1)
      echo "   WebUI started on http://localhost:${WEBUI_PORT} (pid=${WEBUI_PID})."
      break
    fi
    sleep 1
  done
  if [ -z "${WEBUI_PORT}" ]; then
    echo "   WARN: WebUI didn't print Local URL in 60s; tail of log:"
    tail -20 "${WEBUI_LOG}" >&2 || true
    WEBUI_PORT="${WEBUI_PORT_PREFERRED}"
  fi
fi

if [ "${RUNS}" -gt 0 ]; then
echo ">> Triggering ${RUNS} sequential runs (numSteps=${NUM_STEPS}, stateSize=${STATE_SIZE})..."
curl -fs "http://127.0.0.1:${BENCH_PORT}/trigger?mode=sequential&runs=${RUNS}&numSteps=${NUM_STEPS}&stateSize=${STATE_SIZE}" \
  | tee /tmp/dex-trigger-sequential.json
echo

echo ">> Triggering ${RUNS} parallel runs (numSteps=${NUM_STEPS}, stateSize=${STATE_SIZE})..."
curl -fs "http://127.0.0.1:${BENCH_PORT}/trigger?mode=parallel&runs=${RUNS}&numSteps=${NUM_STEPS}&stateSize=${STATE_SIZE}" \
  | tee /tmp/dex-trigger-parallel.json
echo
fi

# Step retry benchmark flow. finalOutcome=succeed (default): fail 4× then Complete.
# finalOutcome=fail: fail 4× then permanent errors until retry exhausted → Failed.
# See benchmark/cmd/benchmarkworker/retry_flows.go.
RETRY_RUNS="${RETRY_RUNS:-1}"
RETRY_FAIL_RUNS="${RETRY_FAIL_RUNS:-1}"
retry_run_id=""
retry_fail_run_id=""
if [ "${RETRY_RUNS}" -gt 0 ]; then
  echo ">> Triggering ${RETRY_RUNS} retry runs (finalOutcome=succeed)..."
  curl -fs "http://127.0.0.1:${BENCH_PORT}/trigger?mode=retry&runs=${RETRY_RUNS}&finalOutcome=succeed" \
    | tee /tmp/dex-trigger-retry-succeed.json
  echo
  retry_run_ids=$(python3 -c "import json; print('\n'.join(json.load(open('/tmp/dex-trigger-retry-succeed.json'))['run_ids']))" 2>/dev/null || true)
  retry_run_id=$(echo "${retry_run_ids}" | head -1)
fi
if [ "${RETRY_FAIL_RUNS}" -gt 0 ]; then
  echo ">> Triggering ${RETRY_FAIL_RUNS} retry runs (finalOutcome=fail)..."
  curl -fs "http://127.0.0.1:${BENCH_PORT}/trigger?mode=retry&runs=${RETRY_FAIL_RUNS}&finalOutcome=fail" \
    | tee /tmp/dex-trigger-retry-fail.json
  echo
  retry_fail_run_ids=$(python3 -c "import json; print('\n'.join(json.load(open('/tmp/dex-trigger-retry-fail.json'))['run_ids']))" 2>/dev/null || true)
  retry_fail_run_id=$(echo "${retry_fail_run_ids}" | head -1)
fi

# Wait/channel/timer benchmark flows. Each uses a small fixed runs count so
# the WebUI timeline stays readable; modes correspond 1:1 to docs/wait-for-conditions-design.md
# scenarios 1.1-1.8.
WAIT_RUNS="${WAIT_RUNS:-2}"
for mode in channelMinMax allOfTimerChannel anyOfTimerOnly anyOfTimerVsChannel; do
  if [ "${WAIT_RUNS}" -le 0 ]; then
    break
  fi
  echo ">> Triggering ${WAIT_RUNS} ${mode} runs..."
  curl -fs "http://127.0.0.1:${BENCH_PORT}/trigger?mode=${mode}&runs=${WAIT_RUNS}" \
    | tee "/tmp/dex-trigger-${mode}.json"
  echo
done

# Stagger publishes so the WebUI shows the WAITING -> INVOKING_EXECUTE
# transition live for the channel-driven flows. We capture the first run id
# from each trigger response. anyOfTimerOnly is excluded — its sole satisfier
# is the timer.
if [ "${WAIT_RUNS}" -gt 0 ]; then
  echo ">> Sleeping briefly so workers have time to enter WAITING_FOR_CONDITION..."
  sleep 5
  for mode in channelMinMax allOfTimerChannel anyOfTimerVsChannel; do
    resp_file="/tmp/dex-trigger-${mode}.json"
    if [ ! -f "$resp_file" ]; then
      continue
    fi
    # Extract run_ids from the JSON response without requiring jq (best-effort grep).
    run_ids=$(python3 -c "import json,sys; print('\n'.join(json.load(open('$resp_file'))['run_ids']))" 2>/dev/null || true)
    if [ -z "$run_ids" ]; then
      continue
    fi
    for run_id in $run_ids; do
      for i in 1 2; do
        curl -fs "http://127.0.0.1:${BENCH_PORT}/publish?runId=${run_id}&channel=notify&value=hello-${i}" >/dev/null || true
      done
      echo "   published 2 messages to ${mode} run ${run_id} on channel 'notify'"
    done
  done
fi

# Dynamic channel benchmark flow. Each run fans out per orderID into TWO
# sibling waiters (orderWaitStep + orderAckStep) over three dynamic channel
# families ("order-update-{id}", "order-ack-{id}", "order-cancel-{id}").
# Demo path (all 4 orders end up retired so the run reaches Completed):
#
#   1. Cancel publish for ord-4 lands FIRST while all 8 children are
#      WAITING. orderWaitStep-4 runs and issues
#      WithCancelingSiblingStepExecution(CancelOf(&orderAckStep{})).
#      Per the same-parent rule, this matches EVERY orderAckStep
#      currently active (ord-1/2/3/4 — they all share
#      dispatchOrdersStep-1 as parent), so all four orderAckStep
#      executions are deleted from ActiveStepExecutions in the same
#      commit. WebUI graph paints them rose ("Cancelled by
#      main.orderWaitStep-4").
#   2. Updates for ord-1/2/3 then drain the remaining
#      orderWaitStep-1/2/3 waiters. Their Execute publishes to
#      OrderAcks.Of(...) but no one's listening anymore (acks were
#      cancelled in step 1) — the publishes become orphan messages in
#      UnconsumedChannelMessages, which is harmless. The orderWaitSteps
#      themselves DEAD_END.
#   3. With no remaining active steps the run transitions to Completed.
#
# This pins the headline cancellation guarantees: (a) one cancel can
# match multiple siblings under the same parent, (b) cancelled steps
# are deleted atomically with the canceller's commit (no extra Execute
# event), (c) the run's terminal status is reached even when the
# cancellation drained an entire fan-out arm.
#
# See docs/wait-for-conditions-design.md +
# docs/cancel-sibling-step-execution-design.md.
DYNAMIC_RUNS="${DYNAMIC_RUNS:-2}"
if [ "${DYNAMIC_RUNS}" -gt 0 ]; then
  echo ">> Triggering ${DYNAMIC_RUNS} dynamicChannel runs (orderIds=ord-1,ord-2,ord-3,ord-4)..."
  curl -fs "http://127.0.0.1:${BENCH_PORT}/trigger?mode=dynamicChannel&runs=${DYNAMIC_RUNS}&orderIds=ord-1,ord-2,ord-3,ord-4" \
    | tee /tmp/dex-trigger-dynamicChannel.json
  echo

  echo ">> Sleeping briefly so dynamicChannel runs settle into AllStepsWaiting..."
  sleep 2
  dyn_resp="/tmp/dex-trigger-dynamicChannel.json"
  if [ -f "$dyn_resp" ]; then
    dyn_run_ids=$(python3 -c "import json,sys; print('\n'.join(json.load(open('$dyn_resp'))['run_ids']))" 2>/dev/null || true)
    for run_id in $dyn_run_ids; do
      # Step 1: ord-4 cancel publish FIRST. With all 8 children still
      # WAITING_FOR_CONDITION, this triggers the multi-sibling cancel
      # path (cancels all 4 orderAckSteps in one commit).
      curl -fs "http://127.0.0.1:${BENCH_PORT}/publish?runId=${run_id}&channel=order-cancel-ord-4&value=cancelled-by-user" >/dev/null || true
      # Tiny pause so the cancel commit lands before the update publishes
      # arrive — keeps the demo's timing predictable. The race recap in
      # docs/wait-for-conditions-design.md ("external publish during
      # worker exit") guarantees correctness either way; the sleep is
      # just for demo legibility (otherwise the four updates can race
      # the cancel on a fast machine and the WebUI may briefly show an
      # ack-step Completed before the cancel deletes it).
      sleep 1
      # Step 2: drain the remaining orderWaitStep-1/2/3 with their
      # update publishes. These complete normally (publish-to-cancelled
      # OrderAcks is a no-op orphan). orderWaitStep-4 already DEAD_ENDed
      # in step 1.
      for ord in ord-1 ord-2 ord-3; do
        curl -fs "http://127.0.0.1:${BENCH_PORT}/publish?runId=${run_id}&channel=order-update-${ord}&value=delivered-${ord}" >/dev/null || true
      done
      echo "   published cancel(ord-4) + updates(ord-1/2/3) to dynamicChannel run ${run_id} → all 4 orderAckSteps cancelled, run reaches Completed"
    done
  fi
fi

# Multi-agent benchmark flow. End-to-end demo of cross-run StartRun,
# cross-run dynamic-channel publish, channel min/max draining, AnyOf
# timer re-arm, and sibling cancellation — all in one run.
# See benchmark/AGENT_WORKFLOW.md for the design + Mermaid diagrams.
AGENT_RUN="${AGENT_RUN:-1}"
AGENT_MAX_CONCURRENT="${AGENT_MAX_CONCURRENT:-2}"
AGENT_REQUEST_NUM="${AGENT_REQUEST_NUM:-3}"
agent_run_id=""
if [ "${AGENT_RUN}" = "1" ]; then
  echo ">> Triggering 1 mainAgent run (maxConcurrentSubAgents=${AGENT_MAX_CONCURRENT})..."
  curl -fs "http://127.0.0.1:${BENCH_PORT}/agentTrigger?maxConcurrentSubAgents=${AGENT_MAX_CONCURRENT}" \
    | tee /tmp/dex-trigger-mainAgent.json
  echo
  agent_run_id=$(python3 -c "import json; print(json.load(open('/tmp/dex-trigger-mainAgent.json'))['run_id'])" 2>/dev/null || true)
  if [ -n "$agent_run_id" ]; then
    echo ">> mainAgent run_id=${agent_run_id}; sleeping 3s for AllStepsWaiting..."
    sleep 3
    # Stage 1: human asks for N subagents. mainLoopStep wakes, publishes N
    # to SubAgentRequestCh, fan-out startSubAgentSteps each StartRun a
    # subAgent run; subAgents loop random 1-40s × 3 cycles publishing
    # SubAgentResponseCh-<rid> back to the parent.
    echo ">> Sending start_subagents num=${AGENT_REQUEST_NUM}..."
    curl -fs "http://127.0.0.1:${BENCH_PORT}/agentHumanMessage?runId=${agent_run_id}&kind=start_subagents&num=${AGENT_REQUEST_NUM}" >/dev/null || true
    echo ">> Sleeping 30s for subagent loops to flush..."
    sleep 30
    # Stage 2: enter the LLM loop (fastLLM=5s naturally completes,
    # slowLLM=60s would naturally complete much later). Then interrupt
    # before slowLLM finishes to demo sibling cancellation.
    echo ">> Sending start_llm_loop..."
    curl -fs "http://127.0.0.1:${BENCH_PORT}/agentHumanMessage?runId=${agent_run_id}&kind=start_llm_loop" >/dev/null || true
    echo ">> Sleeping 8s (fastLLM finishes naturally at 5s; slowLLM still running)..."
    sleep 8
    echo ">> Sending agentInterruptLLM (cancels slowLLM via WithCancelingSiblingStepExecution)..."
    curl -fs "http://127.0.0.1:${BENCH_PORT}/agentInterruptLLM?runId=${agent_run_id}&reason=dev-stack-demo" >/dev/null || true
    sleep 2
    # Stage 3: tell the agent to stop.
    echo ">> Sending complete..."
    curl -fs "http://127.0.0.1:${BENCH_PORT}/agentHumanMessage?runId=${agent_run_id}&kind=complete" >/dev/null || true
  else
    echo "   (skipping mainAgent driver — could not parse run_id)"
  fi
fi

echo
echo "================================================================================"
echo " Stack is up and seeded. WebUI:  http://localhost:${WEBUI_PORT}/?namespace=default"
echo "================================================================================"
if [ -n "${WEBUI_PID}" ]; then
  echo " WebUI background pid: ${WEBUI_PID}   (log: ${WEBUI_LOG})"
fi
if [ -n "${retry_run_id}" ]; then
  echo
  echo " Step retry benchmark (recovered → Completed):"
  echo "   http://localhost:${WEBUI_PORT}/flow/show?namespace=default&runId=${retry_run_id}"
fi
if [ -n "${retry_fail_run_id}" ]; then
  echo
  echo " Step retry benchmark (exhausted → Failed):"
  echo "   http://localhost:${WEBUI_PORT}/flow/show?namespace=default&runId=${retry_fail_run_id}"
fi
echo
echo " Clean up & shut down EVERYTHING (containers + volumes + WebUI):"
echo "   pkill -f 'next dev' || true   # stop the background WebUI"
echo "   docker compose -f ${WEB_DIR}/docker-compose.e2e.yaml -f ${BACKEND_COMPOSE} -p ${PROJECT_NAME} down -v"
echo "   # -v also removes the ${DB_BACKEND} data volume — a fully clean slate."
echo
echo "================================================================================"
echo " Multi-Agent Playground — paste any of these URLs into your browser address bar"
echo "================================================================================"
echo
echo " 1) Trigger a NEW mainAgent run (returns a fresh run_id JSON):"
echo "    http://127.0.0.1:${BENCH_PORT}/agentTrigger?maxConcurrentSubAgents=2"
echo "    http://127.0.0.1:${BENCH_PORT}/agentTrigger?maxConcurrentSubAgents=5    # bigger fan-out"
echo
if [ -n "${agent_run_id}" ]; then
  echo " 2) Drive the EXISTING demo run that dev-stack just created:"
  echo "    run_id = ${agent_run_id}"
  echo "    Open run in WebUI:"
  echo "      http://localhost:${WEBUI_PORT}/flow/show?namespace=default&runId=${agent_run_id}"
  echo
  echo "    Send 'start_subagents' (spawn N more child SubAgent runs):"
  echo "      http://127.0.0.1:${BENCH_PORT}/agentHumanMessage?runId=${agent_run_id}&kind=start_subagents&num=1"
  echo "      http://127.0.0.1:${BENCH_PORT}/agentHumanMessage?runId=${agent_run_id}&kind=start_subagents&num=3"
  echo
  echo "    Send 'start_llm_loop' (fan out fastLLMStep + slowLLMStep + llmLoopStep):"
  echo "      http://127.0.0.1:${BENCH_PORT}/agentHumanMessage?runId=${agent_run_id}&kind=start_llm_loop"
  echo
  echo "    Interrupt the LLM loop (cancels in-flight slowLLMStep via WithCancelingSiblingStepExecution):"
  echo "      http://127.0.0.1:${BENCH_PORT}/agentInterruptLLM?runId=${agent_run_id}"
  echo "      http://127.0.0.1:${BENCH_PORT}/agentInterruptLLM?runId=${agent_run_id}&reason=user-bored"
  echo
  echo "    Send 'complete' (terminates the run):"
  echo "      http://127.0.0.1:${BENCH_PORT}/agentHumanMessage?runId=${agent_run_id}&kind=complete"
  echo
  echo " 3) For a brand-new run, replace ${agent_run_id} above with the run_id"
  echo "    returned by /agentTrigger in step (1)."
else
  echo " 2) Once you have a run_id from /agentTrigger, the message URLs are:"
  echo "      http://127.0.0.1:${BENCH_PORT}/agentHumanMessage?runId=<RUN_ID>&kind=start_subagents&num=N"
  echo "      http://127.0.0.1:${BENCH_PORT}/agentHumanMessage?runId=<RUN_ID>&kind=start_llm_loop"
  echo "      http://127.0.0.1:${BENCH_PORT}/agentHumanMessage?runId=<RUN_ID>&kind=complete"
  echo "      http://127.0.0.1:${BENCH_PORT}/agentInterruptLLM?runId=<RUN_ID>&reason=...optional..."
fi
echo
echo " See benchmark/AGENT_WORKFLOW.md for the full design + Mermaid diagrams."
echo "================================================================================"
