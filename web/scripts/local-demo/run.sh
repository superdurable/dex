#!/usr/bin/env bash

# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

# Starts a local Dex Web demo: dexcli dev, the Go examples server, an agent
# session Worker, and a many-events Worker. See README.md in this directory.
#
#   run.sh [start]   start everything; Ctrl+C or `run.sh stop` stops it
#   run.sh stop      stop the kit that `run.sh start` started on the same ports

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"

DEX_PORT="${DEX_DEMO_DEX_PORT:-8811}"
WEB_PORT="${DEX_DEMO_WEB_PORT:-8812}"
EXAMPLES_PORT="${DEX_DEMO_EXAMPLES_PORT:-8095}"
READY_TIMEOUT_SECONDS="${DEX_DEMO_READY_TIMEOUT_SECONDS:-180}"
DEX_ADDRESS="127.0.0.1:$DEX_PORT"
WEB_URL="http://127.0.0.1:$WEB_PORT"
EXAMPLES_URL="http://127.0.0.1:$EXAMPLES_PORT"
PID_FILE="${TMPDIR:-/tmp}/dex-web-local-demo-$WEB_PORT.pid"

CHILD_PIDS=()
CHILD_NAMES=()

log() {
  printf '[local-demo] %s\n' "$*" >&2
}

fail() {
  log "ERROR: $*"
  exit 1
}

# --- stop -------------------------------------------------------------------

stop_running_kit() {
  if [[ ! -f "$PID_FILE" ]]; then
    log "no kit is running on Web port $WEB_PORT ($PID_FILE is missing)"
    return 0
  fi
  local supervisor_pid
  supervisor_pid="$(cat "$PID_FILE")"
  if ! kill -0 "$supervisor_pid" 2>/dev/null; then
    log "removing stale $PID_FILE (process $supervisor_pid is gone)"
    rm -f "$PID_FILE"
    return 0
  fi
  if [[ "$(ps -p "$supervisor_pid" -o command= 2>/dev/null)" != *local-demo/run.sh* ]]; then
    fail "process $supervisor_pid in $PID_FILE is not run.sh; remove the file by hand if it is stale"
  fi
  log "stopping kit supervisor $supervisor_pid"
  kill -TERM "$supervisor_pid"
  local waited=0
  while kill -0 "$supervisor_pid" 2>/dev/null; do
    if (( waited >= 60 )); then
      fail "supervisor $supervisor_pid did not stop within 30 seconds"
    fi
    sleep 0.5
    waited=$((waited + 1))
  done
  log "stopped"
}

# --- start helpers ----------------------------------------------------------

is_port_open() {
  (exec 3<>"/dev/tcp/127.0.0.1/$1") 2>/dev/null
}

require_free_port() {
  if is_port_open "$1"; then
    fail "port $1 ($2) is in use; stop that process or set $3"
  fi
}

# start_child NAME LOG_FILE COMMAND... runs COMMAND in its own process group.
start_child() {
  local name="$1" log_file="$2"
  shift 2
  "$@" >"$log_file" 2>&1 &
  CHILD_PIDS+=("$!")
  CHILD_NAMES+=("$name")
  log "started $name (pid $!, log $log_file)"
}

is_child_running() {
  kill -0 "$1" 2>/dev/null
}

stop_children() {
  local index pid
  for (( index=${#CHILD_PIDS[@]}-1; index>=0; index-- )); do
    pid="${CHILD_PIDS[index]}"
    if is_child_running "$pid"; then
      log "stopping ${CHILD_NAMES[index]} (pid $pid)"
      kill -TERM -- "-$pid" 2>/dev/null || kill -TERM "$pid" 2>/dev/null || true
    fi
  done
  local waited=0
  for pid in "${CHILD_PIDS[@]}"; do
    while is_child_running "$pid" && (( waited < 40 )); do
      sleep 0.5
      waited=$((waited + 1))
    done
    if is_child_running "$pid"; then
      kill -KILL -- "-$pid" 2>/dev/null || kill -KILL "$pid" 2>/dev/null || true
    fi
  done
}

cleanup() {
  trap - EXIT INT TERM
  stop_children
  if [[ -f "$PID_FILE" && "$(cat "$PID_FILE")" == "$$" ]]; then
    rm -f "$PID_FILE"
  fi
  log "logs kept in $RUN_DIR/logs"
}

# wait_until DESCRIPTION WATCHED_PID COMMAND... polls COMMAND until it succeeds.
wait_until() {
  local description="$1" watched_pid="$2"
  shift 2
  local deadline=$((SECONDS + READY_TIMEOUT_SECONDS))
  until "$@"; do
    if ! is_child_running "$watched_pid"; then
      fail "process $watched_pid exited while waiting for $description"
    fi
    if (( SECONDS >= deadline )); then
      fail "timed out after ${READY_TIMEOUT_SECONDS}s waiting for $description"
    fi
    sleep 1
  done
}

is_web_ready() {
  curl -fsS -o /dev/null "$WEB_URL/readyz" 2>/dev/null
}

is_log_line_present() {
  grep -q "$2" "$1" 2>/dev/null
}

resolve_dexcli() {
  if [[ -n "${DEXCLI:-}" ]]; then
    [[ -x "$DEXCLI" ]] || fail "DEXCLI is not executable: $DEXCLI"
    log "using DEXCLI=$DEXCLI"
    return 0
  fi
  if [[ "${DEX_DEMO_SKIP_WEB_BUILD:-}" == "1" && -f "$REPO_ROOT/web/assets/dist/index.html" ]]; then
    log "reusing web/assets/dist (DEX_DEMO_SKIP_WEB_BUILD=1)"
  else
    log "building Dex Web assets"
    if [[ ! -d "$REPO_ROOT/web/node_modules" ]]; then
      (cd "$REPO_ROOT/web" && npm ci) >"$RUN_DIR/logs/web-build.log" 2>&1 \
        || fail "npm ci failed; see $RUN_DIR/logs/web-build.log"
    fi
    (cd "$REPO_ROOT/web" && npm run build) >>"$RUN_DIR/logs/web-build.log" 2>&1 \
      || fail "Dex Web build failed; see $RUN_DIR/logs/web-build.log"
  fi
  log "building dexcli from cli/"
  (cd "$REPO_ROOT/cli" && GOWORK=off go build -trimpath -o "$RUN_DIR/bin/dexcli" ./cmd/dexcli) \
    || fail "dexcli build failed"
  DEXCLI="$RUN_DIR/bin/dexcli"
}

resolve_server_zd() {
  if [[ -n "${DEX_SERVER_ZD_BINARY:-}" ]]; then
    [[ -x "$DEX_SERVER_ZD_BINARY" ]] || fail "DEX_SERVER_ZD_BINARY is not executable: $DEX_SERVER_ZD_BINARY"
  elif [[ -x "$(dirname "$DEXCLI")/dex-server-zd" ]]; then
    DEX_SERVER_ZD_BINARY="$(dirname "$DEXCLI")/dex-server-zd"
  elif command -v dex-server-zd >/dev/null 2>&1; then
    DEX_SERVER_ZD_BINARY="$(command -v dex-server-zd)"
  elif [[ -x "$REPO_ROOT/server-zd/target/release/dex-server-zd" ]]; then
    DEX_SERVER_ZD_BINARY="$REPO_ROOT/server-zd/target/release/dex-server-zd"
  else
    fail "dex-server-zd not found; install dexcli, or set DEX_SERVER_ZD_BINARY"
  fi
  export DEX_SERVER_ZD_BINARY
  log "using DEX_SERVER_ZD_BINARY=$DEX_SERVER_ZD_BINARY"
}

build_go_programs() {
  log "building the demo Workers and the Go examples server"
  (cd "$SCRIPT_DIR" && GOWORK=off go build -o "$RUN_DIR/bin/" ./agent-session ./many-events) \
    || fail "demo Worker build failed"
  (cd "$REPO_ROOT/examples/go" && GOWORK=off go build -o "$RUN_DIR/bin/examples-server" ./cmd/server) \
    || fail "examples server build failed"
}

# write_flow_graphs fills the directory that Dex Web reads with --flow-rendering-dir.
write_flow_graphs() {
  local graph_dir="$RUN_DIR/flow-graphs"
  mkdir -p "$graph_dir"
  cp "$REPO_ROOT/docs/src/data/flow-definitions/customer-refund-agentic.json" "$graph_dir/"
  sed -e 's/"<flow-type>"/"DemoAgent"/' -e 's/"<action-permission>"/"dex.agents.action"/g' \
    "$REPO_ROOT/agents/python/core/src/dex_agents/session_flow_graph.json" >"$graph_dir/demo-agent.json"
}

start_agentic_refund() {
  local flow_id="$1"
  local body
  body="{\"flowId\":\"$flow_id\",\"case\":{\"caseId\":\"$flow_id\",\"customer\":\"customer-2\",\"customerEmail\":\"customer-2@example.com\",\"customerNote\":\"Please review this charge\",\"amountCents\":1400000,\"orderAgeDays\":8}}"
  curl -fsS -X POST "$EXAMPLES_URL/products/customer-refund/agentic/start" \
    -H 'content-type: application/json' -d "$body" >>"$RUN_DIR/logs/agentic-refund-start.log" 2>&1
}

# --- start ------------------------------------------------------------------

start_kit() {
  command -v go >/dev/null 2>&1 || fail "go is not on PATH"
  command -v curl >/dev/null 2>&1 || fail "curl is not on PATH"
  require_free_port "$DEX_PORT" "Dex" DEX_DEMO_DEX_PORT
  require_free_port "$WEB_PORT" "Dex Web" DEX_DEMO_WEB_PORT
  require_free_port "$EXAMPLES_PORT" "examples HTTP" DEX_DEMO_EXAMPLES_PORT
  if [[ -f "$PID_FILE" ]] && kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
    fail "a kit is already running (pid $(cat "$PID_FILE")); run '$0 stop' first"
  fi

  RUN_DIR="${DEX_DEMO_RUN_DIR:-$(mktemp -d "${TMPDIR:-/tmp}/dex-web-local-demo.XXXXXX")}"
  mkdir -p "$RUN_DIR/logs" "$RUN_DIR/bin"
  log "run directory: $RUN_DIR"

  resolve_dexcli
  resolve_server_zd
  build_go_programs
  write_flow_graphs

  echo "$$" >"$PID_FILE"
  trap cleanup EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  # Each background child leads its own process group, so stopping it also stops its children.
  set -m

  local suffix
  suffix="$(date +%Y%m%d-%H%M%S)"
  local refund_flow_id="customer-refund-agentic-$suffix"
  local session_flow_id="demo-agent-session-$suffix"
  local many_events_flow_id="many-events-$suffix"

  start_child dexcli "$RUN_DIR/logs/dexcli.log" \
    "$DEXCLI" dev -dex-port "$DEX_PORT" -web-port "$WEB_PORT" -open=false -connectors-mcp=false \
    -flow-rendering-dir "$RUN_DIR/flow-graphs" -zd-data-dir "$RUN_DIR/zd-state"
  local dexcli_pid="${CHILD_PIDS[0]}"
  wait_until "Dex Web readiness on $WEB_URL/readyz" "$dexcli_pid" is_web_ready
  is_port_open "$DEX_PORT" || fail "Dex is not listening on $DEX_ADDRESS; see $RUN_DIR/logs/dexcli.log"
  log "Dex on $DEX_ADDRESS and Dex Web on $WEB_URL are ready"

  export DEX_FLOW_SERVICE_ADDRESS="$DEX_ADDRESS"
  DEX_EXAMPLES_HTTP_ADDRESS="127.0.0.1:$EXAMPLES_PORT" DEX_BLOB_CACHE_DIR="$RUN_DIR/examples-blobs" \
    start_child examples-server "$RUN_DIR/logs/examples-server.log" "$RUN_DIR/bin/examples-server"
  local examples_pid="${CHILD_PIDS[1]}"
  start_child agent-session "$RUN_DIR/logs/agent-session.log" \
    "$RUN_DIR/bin/agent-session" -session-id "$session_flow_id"
  local agent_pid="${CHILD_PIDS[2]}"
  start_child many-events "$RUN_DIR/logs/many-events.log" \
    "$RUN_DIR/bin/many-events" -flow-id "$many_events_flow_id"
  local many_events_pid="${CHILD_PIDS[3]}"

  wait_until "the examples server on $EXAMPLES_URL" "$examples_pid" is_port_open "$EXAMPLES_PORT"
  wait_until "the agentic customer refund start" "$examples_pid" start_agentic_refund "$refund_flow_id"
  wait_until "the many-events run start" "$many_events_pid" \
    is_log_line_present "$RUN_DIR/logs/many-events.log" "^FLOW "
  wait_until "two DemoAgent session turns" "$agent_pid" \
    is_log_line_present "$RUN_DIR/logs/agent-session.log" "finished two turns"

  cat <<EOF

Dex Web local demo is running.

  Dex Web                    $WEB_URL/
  Flow graphs (Design)       $WEB_URL/v1/rendering
  Agentic customer refund    $WEB_URL/v1/flows/$refund_flow_id
  DemoAgent session          $WEB_URL/v1/flows/$session_flow_id
  Many-events Timeline       $WEB_URL/v1/flows/$many_events_flow_id

  Dex (FlowService)          $DEX_ADDRESS
  Examples HTTP              $EXAMPLES_URL
  Logs                       $RUN_DIR/logs

Press Ctrl+C, or run '$0 stop', to stop everything.
EOF

  while is_child_running "$dexcli_pid"; do
    local index
    for index in 1 2 3; do
      if [[ -n "${CHILD_PIDS[index]}" ]] && ! is_child_running "${CHILD_PIDS[index]}"; then
        log "WARNING: ${CHILD_NAMES[index]} exited; see $RUN_DIR/logs/${CHILD_NAMES[index]}.log"
        CHILD_PIDS[index]=""
      fi
    done
    sleep 1 &
    wait "$!" || true
  done
  fail "dexcli exited; see $RUN_DIR/logs/dexcli.log"
}

case "${1:-start}" in
  start) start_kit ;;
  stop) stop_running_kit ;;
  *) fail "usage: $0 [start|stop]" ;;
esac
