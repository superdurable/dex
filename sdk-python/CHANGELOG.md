# Changelog

## Unreleased

### Added and fixed

- Add `Context.flow_started_at`, the timezone-aware UTC server start time shared by Steps, retries, Flow timeout handlers, and RPC handlers in one run.

- Add `Context.deadline`, a timezone-aware UTC datetime for the current Step method attempt; RPC handlers return `None`.
- Register multiple instances of one Step class under distinct Step types.
  Route movements, execute-failure fallbacks, and cancellation selections by
  registered instance or StepRef. Existing unambiguous class targets keep working.
  Declared fallback targets are checked at registration; dynamic targets are
  checked when returned by a handler.
- Override a generic Step instance input annotation with `Step.input_type`,
  resolved through the Registry codec registrations for both handlers.

### Breaking changes

- Removed push Workers and their inbound gRPC WorkerService listeners and lifecycle.
  Both Worker and AsyncWorker now always use pull Workers.
- Removed WorkerTarget (address, headless, and pull), Worker.worker_target,
  AsyncWorker.worker_target, WorkerOptions.bind_address,
  WorkerOptions.worker_target, ClientOptions.worker_target, and
  FlowConfig.worker_target. Delete these settings from applications, including
  StartFlowOptions and SubFlowOptions config overrides. Pull routing is automatic.
- The generated dex.dexpb protocol module no longer includes the Go Server
  internal services and messages, WorkerTarget.address,
  WorkerTarget.is_headless_address, or local_activity_metadata on Step method
  responses. Only code that imports dex.dexpb directly is affected.
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
