# Python SDK Step streaming

## Handler contracts

WorkerService WaitFor and Execute are unary-request/server-streaming RPCs. Python
keeps different application shapes for its synchronous and asynchronous runtimes.

A synchronous Step may return its final Wait or StepDecision directly. A handler
that emits progress instead declares `Generator[StepOutput, None, Wait]` or
`Generator[StepOutput, None, StepDecision]`. It yields only values created by
`heartbeat(...)` or `Stream.write(...)`, then returns the final result through
`StopIteration.value`. Yielding a final result, yielding another object, or ending
without a correctly typed return is an invalid Step result.

The sync Worker iterates the application generator on its existing gRPC worker
thread. A yielded frame is sent before the generator resumes. A bare Stream.write
call does not emit anything because sync gRPC has no public write method; application
code must yield the returned StepOutput. This avoids a second per-invocation thread.

An asynchronous Step remains a normal coroutine returning Wait or StepDecision. It
annotates AsyncContext, calls Stream.write synchronously to enqueue a frame, and
awaits `context.heartbeat(...)`. Async generators are rejected. Async RPC and Flow
timeout handlers continue to use Context because neither API emits Step progress.

## Output ordering and cancellation

Each async invocation owns one unbounded asyncio queue. Stream.write uses
`put_nowait`; heartbeat encodes its value, enqueues it, and yields once to the event
loop. The service drains frames in call order. After the handler completes, it drains
already-enqueued progress, writes exactly one result frame, and closes the response.
No SDK output waits for a Dex Stream Store acknowledgement.

Canceling an async Worker stream closes its emitter and cancels the handler task.
Later Stream.write calls are discarded; heartbeat observes task cancellation. Sync
handler cancellation remains cooperative. Closing the gRPC iterator closes the
application generator, and long-running sync code checks Context cancellation at
natural boundaries.

## Heartbeat values

`heartbeat()` and `await context.heartbeat()` emit a heartbeat with no Value. This
explicitly clears previously persisted heartbeat details. Passing any argument,
including Python `None`, emits a present Value encoded through the Registry codec
configuration.

Context exposes `has_last_heartbeat_value()` and
`get_last_heartbeat_value(ExpectedType)`. The presence method distinguishes no
details from a persisted Python `None`. The getter returns `None` when details are
absent and otherwise decodes with the requested Flow codec. A Stream frame remains
an implicit server heartbeat and preserves the latest explicit heartbeat state.
Local activities ignore heartbeat frames and do not pass their values to fallback.

## Stream writes and source

Step Stream writes contain only Stream name, capacity, and encoded Value. The SDK no
longer calls FlowService WriteStream from a Step and no longer limits one write per
Stream. Dex assigns `#<stepExecutionID>` as source metadata. Repeated source values
append independently.

Client and AsyncClient WriteStream remain acknowledged unary calls. Their public
parameter and StreamMessage field are named `source`. Source is required, may contain
`#`, and has no idempotency semantics.

## Validation and defaults

Registry requires exact generator annotations for synchronous progress handlers and
AsyncContext for coroutine Step handlers. Ordinary sync handlers remain valid in an
AsyncWorker, but sync generators require Worker. RPC and Flow timeout Contexts reject
heartbeat and Stream output.

The SDK sends unset options to the server. Server defaults are synchronous Step
durability, four hours of total retry duration, two-hour regular attempts, and a
one-minute regular heartbeat timeout. Explicit heartbeat timeout values use the
server-configured minimum, which defaults to ten seconds. Async durability uses at
most three local attempts in seven seconds before regular fallback; local execution
ignores method and heartbeat timeouts.
