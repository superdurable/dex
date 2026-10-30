# Changelog

## Unreleased

### Added and fixed

- Add `Context.flow_started_at`, the timezone-aware UTC server start time shared by Steps, retries, Flow timeout handlers, and RPC handlers in one run.

- Add `Context.deadline`, a timezone-aware UTC datetime for the current Step method attempt; RPC handlers return `None`.
- Register multiple instances of one Step class under distinct Step types.
  Route movements, execute-failure fallbacks, timeout-handler failure fallbacks,
  and cancellation selections by registered instance or StepRef. Existing
  unambiguous class targets keep working. Declared execute-failure targets are
  checked at registration; timeout-handler failure targets are checked when start
  options are mapped; targets returned from handlers, including StepRef targets,
  are checked when the Worker applies the returned result.
- Override `Step.get_input_type()` to give a generic Step instance a concrete
  input type. The Registry resolves it through its codec registrations for both
  handlers. The default returns `None` and keeps annotation inference.

### Compatibility

- These public fields can now hold a Step class, a registered Step instance, or a
  StepRef: `StepMovement.step`, `StepDecision.canceling_steps`,
  `StepDecision.canceling_sibling_steps`, and `RPCResult.canceling_steps`.
  Writers are unchanged. Code that reads them as `type[Step]` must narrow first,
  for example with `isinstance(target, type)` or `isinstance(target, StepRef)`.
- `FlowTimeoutHandlerOptions.on_failure_proceed_to` now accepts a registered
  instance or StepRef as well as a class. Existing class arguments keep working.
- Registry now rejects an execute-failure target whose input type differs from
  the failing Step's input type, matching the Go SDK. Such a route previously
  failed at runtime when the fallback decoded its input. The check skips `Any`,
  `object`, and unresolved TypeVar input types.
- Registry now calls each Step's `get_step_options()` during construction to
  validate declared execute-failure targets. A `get_step_options()` that raises
  or depends on state prepared after Registry construction now fails registration.

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
