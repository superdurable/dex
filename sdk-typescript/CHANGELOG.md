# Changelog

## Unreleased

### Added

- Register multiple instances of one Step class under distinct Step types.
  Route movements, execute-failure fallbacks, and cancellation selections by
  registered instance or StepRef. Existing unambiguous class targets keep working.
  Declared fallback targets are checked at registration; dynamic targets are
  checked when returned by a handler.

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
