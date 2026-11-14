#!/usr/bin/env bash

# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

# Usage: run.sh [start|stop]. Starts or stops the local Dex Web demo; see README.md.

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
STATE_DIR="${TMPDIR:-/tmp}/dex-web-local-demo-$(id -u)"
PID_FILE="$STATE_DIR/web-$WEB_PORT.pid"

RUN_DIR=""
BIN_DIR=""
RUN_DIR_MARKER=".dex-web-local-demo-run"
BIN_DIR_MARKER=".dex-web-local-demo-bin"
CHILD_PIDS=()
CHILD_NAMES=()

# log tolerates a closed terminal. The subshell drops output that a failed write leaves buffered.
log() {
  (printf '[local-demo] %s\n' "$*" 2>/dev/null) >&2 || true
}

fail() {
  log "ERROR: $*"
  exit 1
}

# --- PID file ---------------------------------------------------------------
# Tab-separated lines: supervisor_pid, supervisor_started, script, run_dir, and
# "child <name> <pgid> <started> <executable>".

prepare_state_dir() {
  if [[ -L "$STATE_DIR" ]]; then
    fail "$STATE_DIR is a symlink; remove it"
  fi
  mkdir -p "$STATE_DIR"
  if [[ ! -O "$STATE_DIR" ]]; then
    fail "$STATE_DIR is not owned by the current user"
  fi
  chmod 700 "$STATE_DIR"
}

# pid_file_value KEY prints the value of a one-value line.
pid_file_value() {
  local key value
  [[ -f "$PID_FILE" ]] || return 0
  while IFS=$'\t' read -r key value; do
    if [[ "$key" == "$1" ]]; then
      printf '%s\n' "$value"
      return 0
    fi
  done <"$PID_FILE"
}

is_process_alive() {
  [[ -n "$1" ]] && kill -0 "$1" 2>/dev/null
}

# process_started PID prints the start time that tells a live process from a reused PID.
process_started() {
  ps -p "$1" -o lstart= 2>/dev/null || true
}

# is_recorded_process_alive PID STARTED checks that PID still names the process started at STARTED.
is_recorded_process_alive() {
  [[ -n "$2" ]] || return 1
  is_process_alive "$1" || return 1
  [[ "$(process_started "$1")" == "$2" ]]
}

is_recorded_child_alive() {
  local pgid="$1" started="$2" executable="$3"
  [[ -n "$executable" ]] || return 1
  is_recorded_process_alive "$pgid" "$started" || return 1
  [[ "$(ps -p "$pgid" -o command= 2>/dev/null)" == "$executable"* ]]
}

# for_each_live_recorded_child COMMAND runs COMMAND NAME PGID for each live recorded child.
for_each_live_recorded_child() {
  local key name pgid started executable
  [[ -f "$PID_FILE" ]] || return 0
  while IFS=$'\t' read -r key name pgid started executable; do
    if [[ "$key" == child ]] && is_recorded_child_alive "$pgid" "$started" "$executable"; then
      "$1" "$name" "$pgid" || return 1
    fi
  done <"$PID_FILE"
}

report_live_child() {
  return 1
}

has_live_recorded_child() {
  ! for_each_live_recorded_child report_live_child
}

is_recorded_supervisor_alive() {
  local supervisor_pid
  supervisor_pid="$(pid_file_value supervisor_pid)"
  is_recorded_process_alive "$supervisor_pid" "$(pid_file_value supervisor_started)" || return 1
  [[ "$(ps -p "$supervisor_pid" -o command= 2>/dev/null)" == *run.sh* ]] || return 1
  [[ "$(pid_file_value script)" == "$SCRIPT_DIR" ]]
}

# acquire_pid_file creates the PID file atomically; noclobber refuses an existing file or symlink.
acquire_pid_file() {
  if [[ -e "$PID_FILE" || -L "$PID_FILE" ]]; then
    if is_recorded_supervisor_alive || has_live_recorded_child; then
      fail "a kit is already running on Web port $WEB_PORT; run '$0 stop' first"
    fi
    log "removing stale $PID_FILE"
    rm -f "$PID_FILE"
  fi
  local started
  started="$(process_started "$$")"
  if ! (set -o noclobber
    printf 'supervisor_pid\t%s\nsupervisor_started\t%s\nscript\t%s\nrun_dir\t%s\n' \
      "$$" "$started" "$SCRIPT_DIR" "$RUN_DIR" >"$PID_FILE") 2>/dev/null; then
    fail "another run.sh created $PID_FILE first"
  fi
}

# --- stop -------------------------------------------------------------------

stop_running_kit() {
  if [[ ! -e "$PID_FILE" ]]; then
    log "no kit is running on Web port $WEB_PORT ($PID_FILE is missing)"
    return 0
  fi
  if [[ "$(pid_file_value script)" != "$SCRIPT_DIR" ]]; then
    fail "$PID_FILE belongs to the kit in $(pid_file_value script); run that checkout's run.sh stop"
  fi
  local supervisor_pid
  supervisor_pid="$(pid_file_value supervisor_pid)"
  if is_recorded_supervisor_alive; then
    log "stopping kit supervisor $supervisor_pid"
    kill -TERM "$supervisor_pid"
    # bash runs the TERM trap only after a foreground build step finishes.
    if ! wait_for_exit "$supervisor_pid" 60; then
      fail "supervisor $supervisor_pid is still building or stopping; run '$0 stop' again later"
    fi
  fi
  # The supervisor may have died without cleanup, for example after SIGKILL.
  for_each_live_recorded_child stop_leftover_child
  rm -f "$PID_FILE"
  log "stopped"
}

stop_leftover_child() {
  log "stopping leftover $1 (process group $2)"
  stop_process_group "$2"
}

# wait_for_exit PID SECONDS returns 1 when PID is still alive after SECONDS.
wait_for_exit() {
  local deadline=$((SECONDS + $2))
  while is_process_alive "$1"; do
    (( SECONDS < deadline )) || return 1
    sleep 0.5
  done
}

stop_process_group() {
  kill -TERM -- "-$1" 2>/dev/null || true
  if ! wait_for_exit "$1" 20; then
    kill -KILL -- "-$1" 2>/dev/null || true
  fi
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

# start_child NAME LOG_FILE EXECUTABLE ARGS... starts a process group led by EXECUTABLE and records it.
start_child() {
  local name="$1" log_file="$2"
  shift 2
  set -m
  "$@" >"$log_file" 2>&1 &
  local pid="$!"
  set +m
  CHILD_PIDS+=("$pid")
  CHILD_NAMES+=("$name")
  printf 'child\t%s\t%s\t%s\t%s\n' "$name" "$pid" "$(process_started "$pid")" "$1" >>"$PID_FILE"
  log "started $name (pid $pid, log $log_file)"
}

stop_children() {
  local index pid
  for (( index=${#CHILD_PIDS[@]}-1; index>=0; index-- )); do
    pid="${CHILD_PIDS[index]}"
    if is_process_alive "$pid"; then
      log "stopping ${CHILD_NAMES[index]} (pid $pid)"
      kill -TERM -- "-$pid" 2>/dev/null || kill -TERM "$pid" 2>/dev/null
    fi
  done
  local deadline=$((SECONDS + 20))
  for pid in "${CHILD_PIDS[@]}"; do
    while is_process_alive "$pid" && (( SECONDS < deadline )); do
      sleep 0.5
    done
    if is_process_alive "$pid"; then
      kill -KILL -- "-$pid" 2>/dev/null || kill -KILL "$pid" 2>/dev/null
    fi
  done
}

# cleanup ignores further signals so a second Ctrl+C or SIGHUP cannot skip the SIGKILL fallback.
cleanup() {
  set +eu
  trap '' INT TERM HUP
  stop_children
  if [[ "$(pid_file_value supervisor_pid)" == "$$" ]]; then
    rm -f "$PID_FILE"
  fi
  if [[ -n "$BIN_DIR" && ! -L "$BIN_DIR" && -f "$BIN_DIR/$BIN_DIR_MARKER" ]]; then
    rm -rf "$BIN_DIR"
  fi
  if [[ -n "$RUN_DIR" && -f "$RUN_DIR/$RUN_DIR_MARKER" ]]; then
    log "logs kept in $RUN_DIR/logs"
  fi
}

# wait_until DESCRIPTION WATCHED_PID COMMAND... polls COMMAND until it succeeds.
wait_until() {
  local description="$1" watched_pid="$2"
  shift 2
  local deadline=$((SECONDS + READY_TIMEOUT_SECONDS))
  until "$@"; do
    if ! is_process_alive "$watched_pid"; then
      fail "process $watched_pid exited while waiting for $description"
    fi
    if (( SECONDS >= deadline )); then
      fail "timed out after ${READY_TIMEOUT_SECONDS}s waiting for $description"
    fi
    sleep 1
  done
}

is_web_ready() {
  curl -fsS --max-time 10 -o /dev/null "$WEB_URL/readyz" 2>/dev/null
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
  local web_dir="$REPO_ROOT/web"
  if [[ "${DEX_DEMO_SKIP_WEB_BUILD:-}" == "1" && -f "$web_dir/assets/dist/index.html" ]]; then
    log "reusing web/assets/dist (DEX_DEMO_SKIP_WEB_BUILD=1)"
  else
    log "building Dex Web assets"
    if [[ ! -f "$web_dir/node_modules/.package-lock.json" \
      || "$web_dir/package-lock.json" -nt "$web_dir/node_modules/.package-lock.json" ]]; then
      (cd "$web_dir" && npm ci) >"$RUN_DIR/logs/web-build.log" 2>&1 \
        || fail "npm ci failed; see $RUN_DIR/logs/web-build.log"
    fi
    (cd "$web_dir" && npm run build) >>"$RUN_DIR/logs/web-build.log" 2>&1 \
      || fail "Dex Web build failed; see $RUN_DIR/logs/web-build.log"
  fi
  log "building dexcli from cli/"
  (cd "$REPO_ROOT/cli" && GOWORK=off go build -trimpath -o "$BIN_DIR/dexcli" ./cmd/dexcli) \
    || fail "dexcli build failed"
  DEXCLI="$BIN_DIR/dexcli"
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
  (cd "$SCRIPT_DIR" && GOWORK=off go build -o "$BIN_DIR/" ./agent-session ./many-events) \
    || fail "demo Worker build failed"
  (cd "$REPO_ROOT/examples/go" && GOWORK=off go build -o "$BIN_DIR/examples-server" ./cmd/server) \
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

is_flow_present() {
  curl -fsS --max-time 10 -o /dev/null "$WEB_URL/api/flows/summary?flowId=$1" 2>/dev/null
}

# start_agentic_refund is safe to retry: a run that an earlier attempt started counts as success.
start_agentic_refund() {
  local flow_id="$1"
  if is_flow_present "$flow_id"; then
    return 0
  fi
  local body
  body="{\"flowId\":\"$flow_id\",\"case\":{\"caseId\":\"$flow_id\",\"customer\":\"customer-2\",\"customerEmail\":\"customer-2@example.com\",\"customerNote\":\"Please review this charge\",\"amountCents\":1400000,\"orderAgeDays\":8}}"
  curl -fsS --max-time 10 -X POST "$EXAMPLES_URL/products/customer-refund/agentic/start" \
    -H 'content-type: application/json' -d "$body" >>"$RUN_DIR/logs/agentic-refund-start.log" 2>&1
}

# prepare_run_dir writes only into an empty directory or one an earlier run marked.
prepare_run_dir() {
  mkdir -p "$RUN_DIR"
  if [[ ! -f "$RUN_DIR/$RUN_DIR_MARKER" && -n "$(ls -A "$RUN_DIR")" ]]; then
    fail "DEX_DEMO_RUN_DIR $RUN_DIR is not empty and was not made by this kit"
  fi
  touch "$RUN_DIR/$RUN_DIR_MARKER"
  mkdir -p "$RUN_DIR/logs"
  BIN_DIR="$RUN_DIR/local-demo-bin"
  if [[ -L "$BIN_DIR" ]] || { [[ -e "$BIN_DIR" ]] && [[ ! -f "$BIN_DIR/$BIN_DIR_MARKER" ]]; }; then
    fail "$BIN_DIR exists and was not made by this kit"
  fi
  rm -rf "$BIN_DIR"
  mkdir "$BIN_DIR"
  touch "$BIN_DIR/$BIN_DIR_MARKER"
}

# --- start ------------------------------------------------------------------

start_kit() {
  command -v go >/dev/null 2>&1 || fail "go is not on PATH"
  command -v curl >/dev/null 2>&1 || fail "curl is not on PATH"
  # Fail before the long build; dexcli dev also refuses busy explicit ports, but the examples server binds later.
  require_free_port "$DEX_PORT" "Dex" DEX_DEMO_DEX_PORT
  require_free_port "$WEB_PORT" "Dex Web" DEX_DEMO_WEB_PORT
  require_free_port "$EXAMPLES_PORT" "examples HTTP" DEX_DEMO_EXAMPLES_PORT

  prepare_state_dir
  RUN_DIR="${DEX_DEMO_RUN_DIR:-$(mktemp -d "${TMPDIR:-/tmp}/dex-web-local-demo.XXXXXX")}"
  acquire_pid_file
  trap cleanup EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  trap 'exit 129' HUP
  prepare_run_dir
  log "run directory: $RUN_DIR"

  resolve_dexcli
  resolve_server_zd
  build_go_programs
  write_flow_graphs

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
  DEX_EXAMPLES_HTTP_ADDRESS="127.0.0.1:$EXAMPLES_PORT" DEX_BLOB_CACHE_DIR="$RUN_DIR/blobs/examples-server" \
    start_child examples-server "$RUN_DIR/logs/examples-server.log" "$BIN_DIR/examples-server"
  local examples_pid="${CHILD_PIDS[1]}"
  start_child agent-session "$RUN_DIR/logs/agent-session.log" \
    "$BIN_DIR/agent-session" -session-id "$session_flow_id" -blob-cache-dir "$RUN_DIR/blobs/agent-session"
  local agent_pid="${CHILD_PIDS[2]}"
  start_child many-events "$RUN_DIR/logs/many-events.log" \
    "$BIN_DIR/many-events" -flow-id "$many_events_flow_id" -blob-cache-dir "$RUN_DIR/blobs/many-events"
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

  while is_process_alive "$dexcli_pid"; do
    local index
    for index in 1 2 3; do
      if [[ -n "${CHILD_PIDS[index]}" ]] && ! is_process_alive "${CHILD_PIDS[index]}"; then
        log "WARNING: ${CHILD_NAMES[index]} exited; see $RUN_DIR/logs/${CHILD_NAMES[index]}.log"
        CHILD_PIDS[index]=""
      fi
    done
    sleep 1
  done
  fail "dexcli exited; see $RUN_DIR/logs/dexcli.log"
}

case "${1:-start}" in
  start) start_kit ;;
  stop) stop_running_kit ;;
  *) fail "usage: $0 [start|stop]" ;;
esac
