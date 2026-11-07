# Changelog

## Unreleased

### Added and fixed

- Add `FlowConfig.historyWindowEvents`, which sets about how many of the newest
  history events a run keeps for history reads and Time Travel. Set it at start
  through `StartFlowOptions.configOverride`, change it with `updateFlowConfig`, or
  override it for a SubFlow; a SubFlow otherwise inherits its parent's value.
  Omitting it uses the server's `history.windowEvents` setting (10000 by default).
  Dex rejects a value that is not positive or exceeds the server's
  `history.maxWindowEvents` setting (100000 by default) with `INVALID_ARGUMENT`.
  Requires server-zd 0.1.21 or later; older servers ignore the value.
- Raise the gRPC receive limit from 4 MiB to `DEFAULT_GRPC_MAX_MESSAGE_BYTES` (32 MiB)
  on every FlowService channel and Worker task stream, matching the Go and Python SDKs.
  A Worker task frame may now be up to that limit too. Set `grpcMaxMessageBytes` on
  `ClientOptions` or `WorkerOptions` to change it. A positive value limits both
  directions; a negative or non-integer value throws `RangeError`.
- Split a LoadBlobs request in half when its response exceeds a gRPC message size
  limit: RESOURCE_EXHAUSTED at the SDK's own receive limit, or OUT_OF_RANGE at the
  Dex server's send limit. Either code counts only without Dex error details and
  with a message that names a size limit. A single blob that does not fit rejects
  with the new `BlobMessageTooLargeError`.
- Add `ClientOptions.flowServiceTls` and `WorkerOptions.flowServiceTls` with the
  `FlowServiceTlsOptions` type. Setting it connects to Dex over TLS with the
  Node.js default trust store, optional `additionalRootCAsPem`, an optional
  `serverName` override, and ALPN h2. Bearer credentials are sent on the TLS
  connection. Omitting it keeps plaintext gRPC unchanged.
- Add `declareRPCMethod(FlowClass, "methodName", options)`, a decorator-free way
  to declare a Flow method as an RPC handler, and the `RPCMethodName` type for
  its method name argument. Use it when the runtime cannot transform
  decorators, such as Node.js type stripping with
  `node --experimental-transform-types`. It stores the same options as `@rpc`,
  and Registry construction reports the same errors for both forms. Declare a
  method through the class that defines it; a method declared on a base class
  is an RPC of every subclass that inherits it. It throws `TypeError` when the
  member is not a method that class defines, when it is `handleTimeout`, or
  when the method is already declared by `@rpc` or an earlier call.
  `@rpc` is unchanged.
- Add `Client.getAttributes(flowId, ...reads)`, which reads committed values of
  registered Attributes and AttributeMap instances in one request, without an
  RPC or Worker. It returns a typed tuple in read order whose elements are
  `T | undefined`. Add `AttributeMap.instance(key)`, the `AttributeMapInstance`
  class, and the `AttributeRead` and `AttributeReadValues` types for it.
- Add `DexServiceErrorOptions`, which lets a `DexServiceError` carry a message
  with call context while `detail` keeps the unchanged service detail. The
  `DexServiceError` and `WorkerInvocationError` constructors accept it in place
  of `ErrorOptions`; existing calls are unchanged.
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
