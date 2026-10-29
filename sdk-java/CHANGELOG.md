# Changelog

## Unreleased

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

### Fixed

Reject heartbeat and Stream writes from a cancelled or completed Step invocation,
including when its pull stream has been reused for another task. A handler that
returns cooperatively after cancellation still sends one final result; only
progress is suppressed.
