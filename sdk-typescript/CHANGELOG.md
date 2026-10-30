# Changelog

## Unreleased

### Added and fixed

- Add `Context.deadline`, an absolute UTC Date for the current Step method attempt; RPC handlers return `undefined`.
- Make `AsyncContext.recordHeartbeat` reject its returned Promise for every failure, including inactive tasks and closed streams, instead of throwing synchronously. A caller that does not await or catch the Promise now sees an unhandled rejection, which terminates a Node.js process by default; await `recordHeartbeat` or attach a rejection handler.
- Register multiple instances of one Step class under distinct Step types.
  Route movements, execute-failure fallbacks, timeout-handler failure fallbacks,
  and cancellation selections by registered instance or StepRef. Existing
  unambiguous class targets keep working. Declared execute-failure targets are
  checked at registration; timeout-handler failure targets are checked when start
  options are mapped; targets returned from handlers, including StepRef targets,
  are checked when the Worker applies the returned result.

### Compatibility

- These public fields now have type `StepTarget` and can hold a Step class, a
  registered Step instance, or a StepRef: `StepMovement.step`,
  `ExecuteFailure.step`, `FlowTimeoutHandlerFailure.step`,
  `StepCancellationSelection.cancelingSteps`,
  `StepCancellationSelection.cancelingSiblingSteps` (and the same fields on
  `StepDecision`), and `RPCResult.cancelingSteps`. Writers are unchanged. Code
  that reads them as a class, for example `movement.step.name`, no longer
  compiles; narrow first with `typeof target === "function"` or
  `target instanceof StepRef`.
- `FlowTimeoutHandlerFailure.proceedTo` now accepts a registered instance or
  StepRef as well as a class. Existing class arguments keep working.
- `Client.startFlow` now throws `FlowDefinitionError` instead of `TypeError`
  when a start-option execute-failure or timeout-handler failure target does not
  belong to the Flow. `FlowDefinitionError` does not extend `TypeError`.
- Registry now rejects an execute-failure target whose built-in scalar input
  codec (`stringCodec`, `booleanCodec`, `int64Codec`, `doubleCodec`,
  `bytesCodec`, or `voidCodec`) differs from the failing Step's, matching the Go
  SDK. Such a route previously failed at runtime when the fallback decoded its
  input. Steps that omit `inputCodec` or use any other codec are not checked.

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
