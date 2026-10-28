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

To migrate, delete Worker bind addresses, headless/advertised targets, and Worker
mode selection. Configure only the Dex Server address. Replace target-bearing
Client constructors with `ClientOptions(serverAddress)` or
`ClientOptions(serverAddress, objectMapper)`. Keep `WorkerOptions.Builder.pull(...)`
only when customizing capacity or lifecycle settings; omitting it now uses pull
defaults. Flow starts and SubFlows automatically send the existing pull wire value.
