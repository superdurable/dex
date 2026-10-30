# Changelog

## Unreleased

### Added and fixed

- Add `Context.deadline`, an absolute UTC Date for the current Step method attempt; RPC handlers return `undefined`.
- Make `AsyncContext.recordHeartbeat` reject its returned Promise for every failure, including inactive tasks and closed streams, instead of throwing synchronously. A caller that does not await or catch the Promise now sees an unhandled rejection, which terminates a Node.js process by default; await `recordHeartbeat` or attach a rejection handler.

### Breaking change

- Remove push Workers. Workers now always pull tasks from the Dex Server
  and need only an outbound connection to it. Publish this change in the next
  minor SDK release.
- Remove `WorkerOptions.bindAddress`, `WorkerOptions.workerTarget`, and
  `Worker.workerTarget`. Remove `WorkerTarget` and `PullWorkerTarget`, including
  their address, headless, and delivery-mode fields.
- Remove `ClientOptions.workerTarget` and `FlowConfig.workerTarget`, including
  StartFlow and SubFlow configuration overrides. Delete these settings when
  migrating; Flow starts and SubFlows use pull routing automatically.
  Configuration updates preserve the Flow's existing Worker target.
- Remove the inbound WorkerService listener and its lifecycle. Use pull Workers
  and keep `WorkerOptions.pull` only to customize capacity and shutdown settings.
  For an active Flow created with a Worker address by an older SDK, keep the old
  Worker available until the Flow finishes or migrate its target separately
  before removing the listener.
