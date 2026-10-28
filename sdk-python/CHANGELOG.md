# Changelog

## Unreleased

### Breaking changes

- Removed push Workers and their inbound gRPC WorkerService listeners and lifecycle.
  Both Worker and AsyncWorker now always use pull Workers.
- Removed WorkerTarget (address, headless, and pull), Worker.worker_target,
  AsyncWorker.worker_target, WorkerOptions.bind_address,
  WorkerOptions.worker_target, ClientOptions.worker_target, and
  FlowConfig.worker_target. Delete these settings from applications, including
  StartFlowOptions and SubFlowOptions config overrides. Pull routing is automatic.
- WorkerOptions.pull now defaults to PullWorkerOptions() and no longer accepts
  None to select push mode. Retain it only to configure polling, task capacity,
  identity, registration, or shutdown.

Migrate to pull Workers, which need only an outbound connection to the Dex Server.
Remove inbound callback ports and Worker address advertisements. Before upgrading,
use an SDK version that supports Worker targets to migrate active push Flows to
pull routing. Configuration updates preserve the existing Worker target, and an
empty FlowConfig() changes nothing. Start the Worker and wait for
wait_pull_registered before admitting work. This breaking change ships in the next
minor SDK release during Beta.
