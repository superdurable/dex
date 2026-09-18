# Compact workflow-history wire names

Status: accepted.

## Decision

Dex uses compact names for Server-internal values persisted in Temporal and
Cadence workflow history. The Go identifiers remain descriptive where practical,
but their history-facing wire values are deliberately short.

| History record | Previous persisted name | Current persisted name |
| --- | --- | --- |
| Request ID memo key | `__DexSystem_WorkflowRequestId` | `ReqId` |
| InvokeRPC Update | `InvokeRpc` | `IRPC` |
| InvokeRPC fallback Signal | `__DexSystem_ExecuteRpc` | `ERPC` |
| WaitFor Activity Type | SDK-derived name ending in `InvokeWaitForMethod` | `IWaitForM` |
| Execute Activity Type | SDK-derived name ending in `InvokeExecuteMethod` | `IExecuteM` |
| Worker RPC Activity Type | SDK-derived name ending in `InvokeWorkerRPC` | `IWRPC` |
| WaitFor Activity ID prefix | `__DexSystem_StepWaitFor_` | `StepWaitFor_` |
| Execute Activity ID prefix | `__DexSystem_StepExecute_` | `StepExecute_` |
| Skip-timer Signal | `__DexSystem_SkipTimerChannel` | `SkipTimer` |
| Stop-workflow Signal | `__DexSystem_StopWorkflowChannel` | `StopWorkflow` |
| Update-config Signal | `__DexSystem_UpdateWorkflowConfig` | `UpdateConfig` |
| Continue-as-new Signal | `__DexSystem_TriggerContinueAsNew` | `TriggerContinueAsNew` |
| SubFlow-completion Signal | `__DexSystem_SubFlowCompletion` | `SubFlowCompletion` |

`DexSystemConstPrefix` is removed. The Go memo constant changes from
`WorkflowRequestId` to `ReqId`. The Activity implementation methods change from
`InvokeWaitForMethod`, `InvokeExecuteMethod`, and `InvokeWorkerRPC` to
`IWaitForM`, `IExecuteM`, and `IWRPC`. WorkerService retains its original gRPC
method names.

Temporal and Cadence workers explicitly register the three compact Activity
Types. Scheduling, history decoding, reset lookup, and replay therefore use the
same exact values on both backends.

## Context

Temporal and Cadence persist Activity Types, Activity IDs, Update names, Signal
names, and memo keys in durable history. Step Activities occur frequently, so
their Type and ID strings are repeated throughout a Flow's lifetime. The request
memo is also copied to child Flows, while RPC and internal control operations add
more named records.

The former `__DexSystem_` prefix and complete Go method names added bytes without
adding useful information. History event kinds already distinguish Activities,
Updates, Signals, and memos. These values are private to the interpreter, so Dex
also controls their namespace and decoding.

Compact persisted values reduce workflow-history storage, transfer, and replay
input size. This optimization is intentionally limited to history-facing Server
internals. Public SDK names, FlowService and WorkerService gRPC methods, protobuf
messages, and Web API results keep their descriptive names.

## Compatibility

This is an intentional Server-side breaking change. Workflows and histories
written with the former names are not supported by the new Server. The change
does not add Temporal or Cadence version gates, register old Activity aliases,
accept old Signal or Update names, or increment `GlobalVersioner`.

Replay baselines are histories captured from the new implementation. A rollout
must not expect existing workflows recorded with the former names to continue.

## Server protocol version

The Server protocol version is not incremented. That version negotiates
compatibility between Server, Workers, SDKs, and dexcli. This decision does not
change either gRPC service, any protobuf payload, or application-visible
behavior, so existing clients and Workers remain compatible.

Increasing the minimum protocol version would reject compatible SDK releases.
Increasing only the current version would add no negotiated behavior and would
not detect or repair an old workflow history. The breaking rollout requirement
belongs in Server release notes instead.

## Consequences

- Workflow histories become smaller, especially for Flows with many Steps.
- Temporal and Cadence persist the same exact Activity Type names.
- Server history conversion, reset, and replay recognize only the compact names.
- Web clients continue to receive the same `FlowHistoryEvent` representation.
- Future persisted internal names should remain compact; public APIs and ordinary
  code identifiers should remain descriptive.

## Verification

Integration tests inspect real Temporal and Cadence histories for the compact
memo key, Activity Types, Activity ID prefixes, Update name, and Signal names.
Replay tests use newly captured Temporal histories, and Web API integration tests
verify that the decoded history result is unchanged on both backends.
