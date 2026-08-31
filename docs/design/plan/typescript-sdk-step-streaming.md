# TypeScript SDK Step streaming

Status: implemented

This design records the TypeScript SDK contract for the server-streaming Step
Worker protocol. WaitFor and Execute use one unary request and a streamed
response. WorkerRpc remains unary.

## Handler types

`Context` exposes execution metadata, restored heartbeat details, persistence
operations, and Step Stream writes. It does not expose heartbeat recording.
Existing handlers may accept `Context` and return either a result or a Promise.

`AsyncContext extends Context` adds:

```typescript
recordHeartbeat<T>(value: T | undefined, codec?: Codec<T>): Promise<void>
```

An `AsyncContext` Step handler must return a Promise. The value argument is
required, so clearing details is visible at the call site:

```typescript
await context.recordHeartbeat(undefined);
```

Undefined emits a heartbeat frame without a Value. Null and every other value
emit a present Value. JSON is the default codec. Scalar or custom values must be
read with the same codec on a later attempt.

Both Context types expose `hasLastHeartbeatValue()` and
`getLastHeartbeatValue(codec?)`. The has method preserves proto presence. This
distinguishes no Value from a present JSON-null Value, even though both decode
to undefined with the current JSON value mapping.

RPC and Flow timeout handlers never receive the Step output emitter. Attempts
to write a Step Stream from those Contexts fail locally.

## Output frames

Each invocation owns one emitter around the grpc-js server Writable. Heartbeat
and Stream calls synchronously enqueue frames on the Node event loop, preserving
application call order without creating another thread.

`recordHeartbeat` resolves after grpc-js accepts the frame locally. It does not
wait for Temporal persistence. `Stream.write` returns void after local Stream
registration checks and encoding. Dex sends no Stream Store acknowledgement.

A successful handler deactivates its Context, writes exactly one result frame,
and closes the stream. A failed handler deactivates the Context and terminates
the stream with a Worker status. Result frames never follow failure. Retained
Contexts cannot emit after success, failure, or cancellation.

The grpc-js cancelled event aborts `Context.cancellationSignal` and immediately
deactivates the emitter. A late handler result is discarded. Cancellation is
cooperative: JavaScript code must observe the signal or reach another output
operation.

## Heartbeat recovery

The dispatcher hydrates `Context.lastHeartbeatValue` before invoking WaitFor or
Execute. Regular activity retries can inspect it before recording new progress.
An explicit undefined heartbeat clears existing details. A Stream frame is an
implicit server heartbeat and reuses the latest explicit state, including an
empty state; it does not manufacture a new Value.

Local activities ignore heartbeat frames because their backend has no activity
heartbeat API. They still process Stream frames. A heartbeat emitted during the
local phase is not available after fallback to a regular activity.

## Stream semantics

A Step method may call `Stream.write` repeatedly for one or several registered
Streams. Every call emits another frame. Dex supplies `#<stepExecutionID>` as the
stored source, so WaitFor, Execute, retries, and multiple messages may share it.
Source is metadata, not an idempotency key.

The external Client API is:

```typescript
await client.writeStream(flowId, stream, source, value);
```

Source must be non-empty. Repeated source values and `#` are valid and every
write appends. Reads expose `StreamMessage.source` unchanged.

## Options and defaults

Durability selection is Step override, then Flow configuration, then sync.
Failure policy does not affect the default. Retry total duration defaults to
four hours. A regular attempt defaults to two hours with a one-minute heartbeat
timeout.

The SDK validates heartbeat timeouts as non-negative whole seconds within int32.
It does not encode the server minimum. Dex deployments default that minimum to
ten seconds, while integration deployments may configure two seconds.

Async durability first uses a local-activity schedule-to-close window of at
most seven seconds and at most three attempts. User-supplied smaller retry
limits win. Method timeout and heartbeat settings do not apply during this
phase. Fallback regular activity consumes the remaining retry budget and uses
the regular timeout defaults.

## Tests

Contract tests cover AsyncContext signatures, required heartbeat arguments,
Value presence and codecs, restored detail hydration, repeated Stream frames,
invalid Contexts, failure, cancellation, and two-second option encoding.

SDK integration tests cover WaitFor and Execute progress ordering, multiple
Streams, duplicate source append behavior, regular retry recovery and clearing,
Stream implicit heartbeat preservation, heartbeat-driven cancellation, no-output
timeout behavior, and local fallback without heartbeat recovery.

## Documentation

The SDK README documents application-facing calls and defaults. The integration
README records runtime coverage. Product documentation and runnable examples
remain deferred until all language SDK examples use the new protocol.

## UI/UX

N/A: no in-repo web UI changes.
