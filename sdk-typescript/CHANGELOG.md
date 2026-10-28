# Changelog

## Unreleased

### Breaking change

- Remove push Workers. Workers now pull tasks from the Dex Server by default
  and need only an outbound connection to it. Publish this change in the next
  minor SDK release.
- Remove `WorkerOptions.bindAddress`, `WorkerOptions.workerTarget`, and
  `Worker.workerTarget`. Remove `WorkerTarget` and `PullWorkerTarget`, including
  their address, headless, and delivery-mode fields.
- Remove `ClientOptions.workerTarget` and `FlowConfig.workerTarget`, including
  StartFlow and SubFlow configuration overrides. Delete these settings when
  migrating; Flow starts, SubFlows, and configuration updates use pull routing
  automatically.
- Remove the inbound WorkerService listener and its lifecycle. Use pull Workers
  and keep `WorkerOptions.pull` only to customize capacity and shutdown settings.
  For an active Flow created with a Worker address by an older SDK, start a pull
  Worker with the same Flow and Step types and call
  `await client.updateFlowConfig(flowId, {})` to route later tasks through pull.
  Already dispatched work is not recalled; drain the old Worker before removing
  its listener.
