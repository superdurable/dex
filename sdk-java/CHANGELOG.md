# Changelog

## Unreleased

### Breaking

- Removed `Client.triggerContinueAsNew`, `FlowStatus.CONTINUED_AS_NEW`,
  `FlowConfig.Builder.continueAsNewThreshold`,
  `FlowConfig.Builder.continueAsNewPageSizeBytes`, `SearchFlowsOptions`, and
  the four-argument `Client.searchFlows` overload. Use the three-argument
  search method.
- Removed `WaitForStepCompletionOptions.Builder.internalHandlerTimeout` and
  `WaitForAttributeOptions.Builder.internalHandlerTimeout`. Request IDs remain
  accepted, but the server does not read them for these waits.
- Generated bindings rename `FlowExecutionID` to `FlowRunID` and
  `getFlowExecutionId` to `getFlowRunId`. ProtoJSON uses `flowRunId`.

### Deprecated

- Deprecated `FlowConfig.Builder.deleteExpiredHistoryObjects(boolean)`. It has no effect, and
  the SDK no longer sends it.
  Runs that start with server-zd 0.1.23 or later ignore the field and, when
  they offload, always delete the objects of expired history segments. A run
  that started under server-zd 0.1.22 still follows the value it recorded; to
  change it, use an SDK older than 1.40.0 or a direct gRPC UpdateFlowConfig.
  Existing code that sets it still compiles.

### Breaking change

This change ships in the next minor Java SDK release during Beta. Pull Workers
are the only Worker mode. Workers need only an outbound connection to the Dex
Server; they no longer open a gRPC Worker listener.

Removed `WorkerTarget` (including its address/headless constructor and `pull()`
factory), `Worker.getWorkerTarget()`, `WorkerOptions.Builder.bindAddress(...)`,
and the `workerTarget(...)` builders on `WorkerOptions`, `ClientOptions`, and
`FlowConfig`. Removed the `ClientOptions(String, WorkerTarget)` and
`ClientOptions(String, WorkerTarget, ObjectMapper)` constructors.

The generated `io.superdurable.gen` protocol classes no longer include the Go
Server internal services and messages, the `WorkerTarget` address and headless
fields, or the local activity metadata of Step method responses. Only code that
uses these generated classes directly is affected.

To migrate, delete Worker bind addresses, headless/advertised targets, and Worker
mode selection. Configure only the Dex Server address. Replace target-bearing
Client constructors with `ClientOptions(serverAddress)` or
`ClientOptions(serverAddress, objectMapper)`. Keep `WorkerOptions.Builder.pull(...)`
only when customizing capacity or lifecycle settings; omitting it now uses pull
defaults. Flow starts and SubFlows automatically send the existing pull wire value.

UpdateFlowConfig leaves the Worker target unset, so an unrelated configuration
update preserves the Flow's existing Worker routing on both Server implementations.
Starts and SubFlows always select pull Workers. A Java Client can no longer select
a push Worker for a new Flow or SubFlow; migrate those Workers to pull first.

Workers that previously omitted pull settings used push handler concurrency of
`max(2, min(32, availableProcessors))`. They now use PullWorkerOptions defaults:
`availableProcessors` concurrent tasks, with one slot reserved for RPCs when that
maximum is at least two. Set `maxConcurrentTasks` and `reservedRpcTasks` explicitly
when preserving application capacity during migration.

### Added

Added `FlowConfig.Builder.offloadHistory(boolean)` to choose whether a run
offloads its history to object storage. Omitting it means `true`. Pass `false`
for many short runs that close soon, to avoid object storage requests. Set it at
start through `StartFlowOptions.Builder.configOverride` or for a SubFlow through
`SubFlowOptions`; a SubFlow otherwise inherits its parent's value.
`Client.updateFlowConfig` rejects it with `INVALID_ARGUMENT`. Requires server-zd
0.1.23 or later; older servers ignore the field.

Added `FlowConfig.Builder.deleteExpiredHistoryObjects(boolean)` to control expired
history object deletion at start, on update, and for SubFlows. Omission uses the
server default at start and inherits for children; explicit false is preserved.
Requires history offload. See the API reference for deletion timing and update
semantics. Requires server-zd 0.1.22 or later; older servers ignore the value.

Added `FlowConfig.Builder.historyWindowEvents(int)`, which sets about how many of the
newest history events a run keeps for history reads and Time Travel. Set it at start
through `StartFlowOptions.Builder.configOverride`, change it with
`Client.updateFlowConfig`, or override it for a SubFlow; a SubFlow otherwise inherits
its parent's value. Omitting it uses the server's `history.windowEvents` setting
(10000 by default). Dex rejects a value that is not positive or exceeds the server's
`history.maxWindowEvents` setting (100000 by default) with `INVALID_ARGUMENT`.
Requires server-zd 0.1.21 or later; older servers ignore the value.

Added `FlowServiceTlsOptions` and the Client and Worker `flowServiceTls` builders.
They enable TLS 1.2 or newer, require HTTP/2 ALPN, and verify server certificates
with the JDK default trust roots plus optional PEM roots. An optional server name
override controls certificate verification, SNI, and HTTP/2 authority. Omitting the
option preserves plaintext gRPC. Bearer credentials compose with TLS unchanged.

Added `Client.getAttributes(flowId, reads...)` and a
`getAttributes(flowId, List<? extends AttributeRead<?>>)` overload. They read
committed Attribute and AttributeMap instance values from the current or latest
Flow run in one request, without an RPC or a Worker. A closed run stays readable
until Dex purges it. `AttributeMap.instance(key)` returns a new
`AttributeMapInstance<T>` selector, and `Attribute<T>` now implements the new
`AttributeRead<T>` interface. The call returns `AttributeValues`, whose
`get(read)` returns an `Optional<T>` that is empty for an unset value.
Hard-delete Attributes are readable, and large values are loaded through
LoadBlobs and the BlobCache. Unregistered or mismatched definitions raise
`FlowDefinitionException` before any request, and a missing Flow raises
`FlowNotFoundException`.

### Fixed

Limit LoadBlobs requests to 1000 entries, preserving input order when hydrating
more values with history offload enabled.

Reject heartbeat and Stream writes from a cancelled or completed Step invocation,
including when its pull stream has been reused for another task. A handler that
returns cooperatively after cancellation still sends one final result; only
progress is suppressed.
