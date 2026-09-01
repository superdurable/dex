# Step Worker server-streaming protocol

This plan defines the server contract for Step Worker progress, heartbeats, and
Stream output. The protocol is a breaking change. Dex does not provide a
compatibility layer or migrate Stream Store data.

## Worker RPC shape

**InvokeWaitForMethod** and **InvokeExecuteMethod** use one unary request and a
server-streaming response. **InvokeWorkerRPC** remains unary.

Each Step response stream may contain any number of heartbeat and Stream frames.
It must then contain exactly one result frame followed by a clean EOF. A missing
result, a second result, an empty frame, or any frame after the result is a Worker
protocol error. A headless Worker call may fail over before its first response
frame. Once any frame arrives, a broken stream fails the activity attempt.

Stream frames are fire-and-forget. The Worker receives no write acknowledgement.
Dex consumes frames in order and attempts each Stream Store write in that order.
A write failure produces a warning and increments
`dex_step_stream_write_failure`. It does not fail the Step method or stop later
frames. The metric has `flow_type`, `step_type`, `step_method`, and a bounded
gRPC `reason`; it does not use Stream names or execution IDs as labels.

## Heartbeats

Dex does not generate automatic Step activity heartbeats. A regular activity
heartbeats only when the Worker emits a heartbeat or Stream frame. A Stream frame
is an implicit heartbeat because it proves the handler is making progress.

A heartbeat may carry a Value. Dex persists that Value as backend heartbeat
details. A heartbeat without a Value clears the previous details. An implicit
Stream heartbeat reuses the latest explicit heartbeat state, including cleared
details. On a regular activity retry, Dex decodes the latest details into
`Context.last_heartbeat_value` before calling the Worker.

Local activities ignore heartbeat frames and values because the backends do not
support local activity heartbeats. They still write Stream frames. Local
heartbeat values are not passed to a fallback regular activity.

Cancellation reaches a running regular handler through its gRPC context when
the next Worker output lets the server observe cancellation. A handler that
emits no heartbeat or Stream frame is terminated by the backend heartbeat
timeout.

## Stream Store

Every Stream write appends a message. The Store does not perform an idempotency
lookup and does not create an `:idem` Redis hash. Repeated and concurrent writes
with the same `source` are retained independently.

`WriteStreamRequest.source` is required, may contain `#`, and is returned as
`StreamMessage.source`. Step output uses `#<stepExecutionID>`. WaitFor, Execute,
retries, and multiple messages from
one Step execution therefore share a source. Readers treat it only as metadata.

Capacity charging includes the serialized Value, Flow ID, source, and configured
fixed overhead. Global FIFO trimming, hard-capacity rejection, long polling, and
resume tokens are unchanged. The Redis prefix remains `dex:stream:v1`, and the
resume-token version remains 1.

The Flow API and Interpreter share one Stream Store instance. A disabled or
unavailable Store returns its normal error. External **WriteStream** exposes that
error; Step output drops it after logging and recording the metric.

## Step defaults

Durability precedence is the Step method override, then **FlowConfig**, then
**SYNC**. Failure policy does not change the selected durability.

An omitted Step retry total duration defaults to four hours. Explicit shorter or
longer durations remain unchanged.

SYNC uses only a regular activity. Its default attempt timeout is two hours and
its default heartbeat timeout is one minute. A zero heartbeat timeout selects
the one-minute default. Negative values are invalid. The server setting
**interpreter.interpreterActivityConfig.minimumStepHeartbeatTimeout** controls
the minimum explicit value and defaults to ten seconds.

ASYNC first uses a local activity with a total Schedule-to-Close window of at
most seven seconds and at most three attempts. Smaller user duration or attempt
limits win. The local phase ignores method and heartbeat timeouts. A fallback
regular activity uses the same two-hour attempt and one-minute heartbeat
defaults as SYNC. Its retry policy subtracts the attempts and elapsed duration
consumed by the local phase.
