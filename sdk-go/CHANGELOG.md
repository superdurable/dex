# Changelog

## Unreleased

### Added

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

### Fixed

- Reject late Step heartbeats, Stream writes, and results after task cancellation,
  completion, or pull-stream reuse. Preserve progress-before-result ordering.
