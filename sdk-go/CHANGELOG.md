# Changelog

## Unreleased

### Breaking change

- Remove push Workers and their gRPC listener, handlers, and listener lifecycle.
  Pull Workers are the only mode. Release this change in the next SDK minor version.
- Remove `WorkerTarget` (including `Address`, `Headless`, and `Pull`),
  `Worker.WorkerTarget()`, `WorkerOptions.BindAddress`, `WorkerOptions.WorkerTarget`,
  `ClientOptions.WorkerTarget`, and `FlowConfig.WorkerTarget` (including overrides
  used by StartFlow and SubFlows).
- Migration: delete listener, advertised address, headless, and Worker target
  settings. Construct Workers with `WorkerOptions{}` or retain
  `WorkerOptions.Pull` to customize pull capacity, registration, and shutdown.
  StartFlow and SubFlows always send the pull target. UpdateFlowConfig leaves
  the Worker target unset, preserving the existing routing. Pull Workers need only
  an outbound connection to the Dex Server configured by `FlowServiceAddress`;
  remove inbound Worker ports and callback routing from deployments.

### Fixed

- Reject late Step heartbeats, Stream writes, and results after task cancellation,
  completion, or pull-stream reuse. Preserve progress-before-result ordering.
