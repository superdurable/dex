# Java SDK Step Worker streaming

This design applies the Step Worker server-streaming protocol to the synchronous
Java SDK. **InvokeWaitForMethod** and **InvokeExecuteMethod** stream progress and
one final result. **InvokeWorkerRPC** remains unary.

## Handler and transport model

Java Step signatures remain ordinary synchronous methods. The handler runs on
the existing bounded Worker executor. Calls to **Context.recordHeartbeat** and
**Stream.write** synchronously encode and send one response frame from that same
thread. The SDK adds no generator, queue, or streaming thread.

The Worker sends frames in application call order. After the handler returns,
it sends exactly one result and completes the response stream. A handler,
encoding, or transport failure ends the RPC with an error and no result; progress
already delivered to Dex is not retracted. gRPC cancellation is checked before
every progress or result frame and continues to interrupt the handler task.

Flow timeout handlers use the Execute stream only for their final result. Flow
timeout and RPC Contexts reject heartbeat and Stream operations.

## Heartbeat context

**recordHeartbeat(value)** emits a heartbeat with a Dex Value when value is
nonnull. **recordHeartbeat(null)** emits a heartbeat without a Value. The server
therefore clears the current heartbeat details, and a later Stream implicit
heartbeat also carries no details.

**hasLastHeartbeatValue** reports whether a regular activity retry supplied
details. **getLastHeartbeatValue(Class)** decodes those details or returns null
when they are absent. The SDK hydrates blob-backed heartbeat Values before the
handler runs. Local activity heartbeat frames are emitted by the Worker but are
ignored by the server and never reach fallback context.

## Stream behavior

**Stream.write** emits a fire-and-forget **StepStreamWrite** frame. It does not
call FlowService.WriteStream, wait for an acknowledgement, or restrict repeated
writes. The server assigns `#<stepExecutionID>` as source metadata and treats
Store failures as observable best-effort loss.

External **Client.writeStream** remains unary. Its third argument is **source**,
which must be nonblank, may contain `#`, and may repeat. Every call appends.
**StreamMessage.getSource** returns the metadata without interpreting it as an
identity.

## Defaults

Durability precedence is Step method, Flow configuration, then SYNC; failure
policy does not change it. Step retry duration defaults to four hours. Regular
attempt and heartbeat timeouts default to two hours and one minute. Zero
heartbeat timeout selects the default; explicit values must satisfy the
server-configured minimum, normally ten seconds.

ASYNC uses at most seven seconds and three attempts in the local phase, subject
to smaller user retry limits. Local execution ignores method and heartbeat
timeouts. Fallback applies the regular defaults and the remaining retry budget.
