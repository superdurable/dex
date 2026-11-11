# Changelog

## Unreleased

### Deprecated

- Deprecated `FlowConfig.DeleteExpiredHistoryObjects`. It has no effect, and the SDK no longer sends it.
  Runs that start with server-zd 0.1.23 or later ignore the field and, when
  they offload, always delete the objects of expired history segments. A run
  that started under server-zd 0.1.22 still follows the value it recorded; to
  change it, use an SDK older than 1.40.0 or a direct gRPC UpdateFlowConfig.
  Existing code that sets it still compiles.

### Added

- Add `Join` and `JoinRound` to synchronize parallel branches of one Flow.
  `DefineJoin` defines a Join over a ChannelMap of branch results with the
  same name. `Join.Fork` uses the split Step's execution ID as the round key
  and the number of branch builders as the branch count, and builds every
  branch movement and the join movement with one `JoinRound`;
  `Join.ForkWithRoundKey` takes an explicit key for RPCs. Branches report with
  `Join.EndBranch` or `Join.ReportBranchResult`. The join Step waits with
  `Join.ForAllBranches`, `Join.ForFirstBranch`, or `Join.ForBranches` and reads
  `Join.BranchResults`. The next round's split Step resets the previous round
  with `Join.ForRemainingBranches` and reads `Join.RemainingBranchResults`.
  `Join.ChannelMap` returns the underlying ChannelMap for reading or loading a
  round's pending results; applications never publish to it.
  `JoinRound` is stored in Step inputs as
  `{"roundKey": string, "branchCount": integer}`.
  [API reference](https://docs.superdurable.io/api/go/latest/dex/#type-join)

- Add `NewStandaloneContext` and `StandaloneContextOptions` to create a
  `Context` for running Step code outside a Worker, for example in a tool
  server or a test. The caller supplies the metadata, including the required
  `Attempt`, and seeds the last heartbeat value and step-execution locals.
  `OnRecordHeartbeat`, `OnSetStepExecutionLocal`, and `OnRecordEvent` hooks
  receive what the handler records, so the caller can persist it.

- Add `FlowConfig.OffloadHistory` to choose whether a run offloads its history
  to object storage. Nil means true. Set it to false for many short runs that
  close soon, to avoid object storage requests. Set it at start through
  `StartFlowOptions.ConfigOverride` or for a SubFlow through
  `SubFlowOptions.ConfigOverride`; a SubFlow otherwise inherits its parent's
  value. `Client.UpdateFlowConfig` rejects it with `INVALID_ARGUMENT`.
  Requires server-zd 0.1.23 or later; older servers ignore the field.

- Add `FlowConfig.DeleteExpiredHistoryObjects` to control expired history object
  deletion at start, on update, and for SubFlows. Omission uses the server default at
  start and inherits for children; explicit false is preserved. Requires history
  offload. See the API reference for deletion timing and update semantics.
  Requires server-zd 0.1.22 or later; older servers ignore the value.

- `FlowConfig.HistoryWindowEvents` sets about how many of the newest history
  events a run keeps for history reads and Time Travel. Set it at start through
  `StartFlowOptions.ConfigOverride`, change it with `Client.UpdateFlowConfig`,
  or override it for a SubFlow; a SubFlow otherwise inherits its parent's
  value. Nil uses the server's `history.windowEvents` setting (10000 by
  default). Dex rejects a value that is not positive or exceeds the server's
  `history.maxWindowEvents` setting (100000 by default) with
  `INVALID_ARGUMENT`. Requires server-zd 0.1.21 or later; older servers ignore
  the field.
- `RPCOptions.Name` sets an RPC's protocol name. Empty keeps the Go method
  name. A Go Worker can now serve, and a Go Client invoke, an RPC that a Flow
  in another language names, such as `send_user_message`. A name may contain
  only ASCII letters, digits, `_`, and `-`, and may not be another RPC's Go
  method name.
- A Worker accepts a request without input for an RPC whose input is
  `dex.None`, as the TypeScript SDK sends one, and decodes it as JSON null.
  Before, it failed the request with `INVALID_ARGUMENT`; it still does for an
  RPC that takes an input.
- `dexcli visualize` names an Action and the Dex Web view RPCs by
  `RPCOptions.Name` when it is set.

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
- Regenerate `gen/dexpb` from the cleaned Dex protocol. It drops the Go Server
  internal services and messages, `WorkerTarget.Address`,
  `WorkerTarget.IsHeadlessAddress`, and `LocalActivityMetadata` on Step method
  responses. Only code that imports `dexpb` directly must remove these symbols.

- Removed `Client.TriggerContinueAsNew`, `FlowContinuedAsNew`,
  `FlowConfig.ContinueAsNewThreshold`, `FlowConfig.ContinueAsNewPageSizeBytes`,
  `SearchFlowsOptions`, `Client.SearchFlowsWithOptions`, and
  `IncludeContinuedAsNew`. Use `Client.SearchFlows` for run searches.
- Removed `WaitForStepCompletionOptions.InternalHandlerTimeout` and
  `WaitForAttributeOptions.InternalHandlerTimeout`. `RequestID` remains
  accepted, but the server does not read it for these waits.
- Generated bindings rename `FlowExecutionID` to `FlowRunID` and
  `FlowExecutionId` to `FlowRunId` on response and history messages. ProtoJSON
  uses `flowRunId`.

### Fixed

- Limit LoadBlobs requests to 1000 entries, preserving input order when hydrating
  more values with history offload enabled.

- Split a LoadBlobs batch when the Dex server rejects its response with
  OUT_OF_RANGE for exceeding the server's send limit, as server-zd does with
  default limits. Before, hydrating more than 32 MiB of uncached blobs failed.
  A RESOURCE_EXHAUSTED or OUT_OF_RANGE status now splits the batch only when it
  has no Dex error details and its message names a gRPC message size limit. A
  single blob that does not fit still returns `BlobMessageTooLargeError`, whose
  `Err` may now carry OUT_OF_RANGE.
- Reject late Step heartbeats, Stream writes, and results after task cancellation,
  completion, or pull-stream reuse. Preserve progress-before-result ordering.
