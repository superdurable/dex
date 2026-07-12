# Worker-Server Protocol

This document describes the protocol between dex workers and the
dex server. The protocol was redesigned around tasklists and
unary long-polls; for the full redesign rationale, internal data
structures, and migration notes see
[`matching-tasklist-redesign.md`](matching-tasklist-redesign.md). This
file is the public/contractual surface that workers (any language) need
to implement.

## Architecture Overview

Workers interact with two gRPC services:

- **MatchingService.PollForRun** (server-streaming long-poll) — worker
  long-polls for the next run dispatch on a `(namespace, task_list_name)`.
  The stream carries a 3-message handshake: `Matched` → server sends
  `PollForRunResponse` (run_id, state, active step executions, …) → worker
  acks. After the ack the run is marked `Running` and the worker owns
  the run's lifecycle until release/completion.
- **MatchingService.PollForExternalEvents** (server-streaming long-poll)
  — worker subscribes to a *sticky* tasklist keyed by its `worker_id`.
  The server pushes `ExternalEvent` messages (channel deliveries, stop
  requests) for any of the worker's currently-running runs. Best-effort:
  delivery is not authoritative; missed events are reconciled via
  `WorkerCallResponse` catch-up on every heartbeat / step completion.
- **RunsService unary RPCs** — worker calls these directly:
  - `ProcessStepExecuteCompleted` — after Execute returns
  - `ProcessStepWaitForCompleted` — after WaitFor returns
  - `ProcessStepsUnblocked` — durable checkpoint for out-of-band sibling
    promotions (see
    [`wait-for-conditions-design.md`](wait-for-conditions-design.md))
  - `ProcessRecordHeartbeat` — periodic, every `HeartbeatInterval`
  - `ProcessReleaseRun` — graceful shutdown (`yield_to_another_worker`) or
    park all-steps-waiting (`all_steps_waiting`)

Every worker→server unary call carries a `WorkerCallContext`:

```protobuf
message WorkerCallContext {
  string worker_id = 1;
  int64  worker_request_counter = 2;
  int64  last_received_external_channel_message_id = 3;
  map<string, StepRetryStateUpdate> active_step_retry_updates = 4;
}
```

`active_step_retry_updates` carries the worker's live retry snapshot for
every in-flight step (during backoff or between attempts). The server
merges into `RunRow.ActiveStepExecutions` on heartbeat and step
completion RPCs. `StepRetryStateUpdate.clear_wait_for_retry_state` clears
WaitFor retry state after a successful WaitFor completion.

`StepRetryState.last_error` and `last_error_stack_trace` are inline
strings on `RunRow.ActiveStepExecutions` (not blob refs). The server
rejects strings over `StepRetryLastErrorMaxBytes` (default 2048).

Step completion RPCs carry an optional `StepMethodReport`
(`execute_method` / `wait_for_method`):

- **Succeeded, first try**: omitted / default.
- **Succeeded after retry**: `outcome=SUCCEEDED`, `attempt_count>1`, plus
  the best diagnostic error/stack from prior failures (skips a trailing
  timeout-only error when a prior attempt had a useful message).
- **Failed after retry**: `outcome=FAILED`, run transitions to `Failed`
  with `RunStop` history. WaitFor failures use `StepWaitForCompletedRequest`;
  Execute failures require `stop_decision=FAIL`.
- **Failed after retry, proceeded**: `outcome=FAILED` with `next_steps` —
  run stays `Running` and spawns the error-handler step (SAGA compensation).
  Execute uses `stop_decision=NONE`; WaitFor sets `next_steps` on
  `StepWaitForCompletedRequest`.

The worker sends `active_step_retry_updates` via an outbound channel
drained into each RPC's `WorkerCallContext` (last-win merge per step).

Step methods enforce timeout via `context.WithTimeout` in the worker
(`StepOptions.{WaitFor,Execute}MethodTimeout`, default 10m). Any
returned `error` is retryable per `RetryPolicy`; terminal `Fail()` without
an error is not retried.

The server validates `worker_id` against `RunRow.WorkerID` (CAS-protected),
treats `worker_request_counter == run.Counter` as an idempotent no-op,
`== run.Counter+1` as the next request to process, and anything else as
a protocol violation.

Every worker←server response carries a `WorkerCallResponse`:

```protobuf
message WorkerCallResponse {
  repeated UnreceivedChannelMessage unreceived_external_channel_messages = 1;
  bool stop_requested = 2;
}
```

`PollForRunResponse` carries `step_method_exe_counter` so the worker seeds its
mirror on dispatch (including after `serverWakePromoteIfAny`). The worker
pre-allocates `wait_for_method_exe_id` and `execute_method_exe_id` on
outgoing `NextStep` / `StepUnblocked` messages; the server persists them
leniently.

The worker merges any `unreceived_external_channel_messages` into its
in-memory unconsumed buffer (deduped on ID) and treats `stop_requested
= true` as a graceful stop signal — drain in-flight steps, call
`ProcessReleaseRun(yield)`, and exit the run. The server sets
`stop_requested` when the run's persisted status is terminal
(Completed or Failed), including after a client `StopRun` CAS.

## Worker Lifecycle

A worker has a stable `worker_id` for its lifetime
(`<host_id>-<start_time_iso>-<rand6>`) and runs three classes of
concurrent goroutines, sized by three independent options:

1. **PollForRun pool** (`ConcurrentRunPollers` goroutines, default 10):
   each long-polls `MatchingService.PollForRun(ns, task_list_name,
   worker_id)`. Every poller acquires a slot from a counting semaphore
   sized at `RunConcurrency` (default 100) BEFORE issuing its
   long-poll, hands the slot to the spawned `runMain(run)` on receipt,
   and loops back. When all `RunConcurrency` slots are held by active
   runs, every poller blocks on the semaphore — the server sees no
   available worker for this tasklist and queues the dispatch. This is
   the upstream back-pressure knob: `RunConcurrency` caps real
   in-flight runs, `ConcurrentRunPollers` caps how many fresh
   dispatches the worker can pick up in parallel after a burst of
   completions.
2. **PollForExternalEvents pool** (`ConcurrentExternalEventPollers`
   goroutines, default 2): each long-polls
   `MatchingService.PollForExternalEvents(ns, worker_id)`. On each
   pushed `ExternalEvent`, routes to the corresponding `runMain`'s
   per-run inbox (channel). Running >1 poller smooths the reconnect
   gap so events land within tens of ms even mid-rotation; missed
   events still self-heal via `WorkerCallResponse` catch-up on the
   next worker→server unary.
3. **runMain(run)** (1 goroutine per active run): owns the run's local
   state (in-memory unconsumed channel buffer, `worker_request_counter`,
   waiting siblings, armed local timers) and runs the step
   execute/waitFor invocation loop. Heartbeats every
   `HeartbeatInterval` via `ProcessRecordHeartbeat`. Exits when the run
   completes, `stop_requested = true` is observed (drains then calls
   `ProcessReleaseRun(all_steps_waiting)`), or the worker is shutting down (best-effort
   `ProcessReleaseRun(yield)`). Releases its `RunConcurrency` slot on exit.

## StopRun (client API)

Clients call `RunsService.StopRun` to durably stop a run before natural
completion. The request requires a terminal outcome and may carry an
optional user-visible reason:

```protobuf
message StopRunRequest {
  string namespace = 1;
  string run_id = 2;
  StopDecision stop_decision = 3;  // COMPLETE or FAIL (required)
  string reason = 4;               // optional; stored on RunStop history
}
```

Semantics:

- CAS-transitions the run from any non-terminal status to
  `RunStatusCompleted` or `RunStatusFailed` per `stop_decision`.
  There is no separate `Stopped` status.
- Appends a `RunStop` history event with `run_status` and `reason` (empty
  when omitted). Worker-driven completions also emit `RunStop` but leave
  `reason` blank.
- Best-effort push of `ExternalEvent.StopRequested` to the active worker
  (same low-latency path as before). Missed pushes self-heal via
  `WorkerCallResponse.stop_requested` once the run row is terminal.
- Idempotent on already-terminal runs (no status change; second call is
  a no-op success).
- `reason` is capped at `StepRetryLastErrorMaxBytes` (default 2048 UTF-8
  bytes).

## RunsService — Shard Owner Forwarding

`StartRun`, `StopRun`, `PublishToChannel`, `ProcessAsyncMatch`,
`ProcessRecordHeartbeat`, and `ProcessReleaseRun` are forwarded by
`RunsServiceHandler.tryForward` to the shard owner instance —
immediate-task SortKey allocation requires the per-shard sequence lock
that only the owner holds.

Worker-driven completion RPCs (`ProcessStep*Completed`,
`ProcessStepsUnblocked`) and timer-fired internal handlers can also
write tasks; the engine dispatches process-local wake-up hints to the
per-shard batch readers via `engine.TaskNotifier` (implemented by
`taskprocessor.LocalTaskNotifier`).

## See Also

- [`matching-tasklist-redesign.md`](matching-tasklist-redesign.md) —
  full design (server side)
- [`wait-for-conditions-design.md`](wait-for-conditions-design.md) —
  WaitFor + sibling unblock semantics
- [`run-engine-design.md`](run-engine-design.md) — engine state
  machine
- `protocol-grpc/protos/dex.proto` — wire format (source of truth)
