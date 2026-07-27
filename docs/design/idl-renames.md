# iWF IDL renames (OpenAPI → iwf.proto)

Canonical naming for the protobuf rewrite. Old OpenAPI names are not kept as aliases.

## Product concepts

| Old | New |
|-----|-----|
| Workflow | Flow |
| WorkflowState / State | Step |
| DataAttribute / DataObject | Attribute (unified with SearchAttribute) |
| SearchAttribute | Attribute (+ optional IndexConfig on write) |
| SignalChannel / InternalChannel / InterStateChannel | Channel |
| WaitUntil (worker start API) | **InvokeWaitForMethod** |
| Decide | **InvokeExecuteMethod** |
| Command (waiting unit) | Condition |

## Identity fields

| Old | New |
|-----|-----|
| workflow_id / workflowId | flow_id |
| workflow_type / workflowType | flow_type |
| workflow_run_id / workflowRunId | run_id |
| workflow_started_timestamp | flow_started_timestamp |
| workflow_state_id / state_id / **step_id** | **step_type** (aligns with `flow_type`) |
| workflow_state_execution_id / state_execution_id | **step_execution_id** (= `step_type` + number) |
| state_locals / upsert_state_locals | step_exe_locals / upsert_step_exe_locals |

`step_type` names the step definition (like `flow_type` names the flow). Each execution instance is `step_execution_id`, composed as `stepType` + a monotonic number.

## RPCs (FlowService — server)

| Old HTTP / concept | New RPC |
|--------------------|---------|
| /workflow/start | StartFlow |
| /workflow/signal + /publishToInternalChannel | PublishToChannel |
| /workflow/stop | StopFlow |
| /dataobjects/get\|set + /searchattributes/get\|set | GetAttributes / SetAttributes |
| /encodedobject/load | LoadBlobs |
| /workflow/get + /getWithWait | WaitForFlow |
| /workflow/search | SearchFlows |
| /workflow/reset | ResetFlow |
| /workflow/rpc | **InvokeRPC** |
| /timer/skip | SkipTimer |
| /config/update | UpdateFlowConfig |
| /waitForStateCompletion | WaitForStepCompletion (`oneof` step_execution_id \| step_type) |
| (new) | **WaitForAttribute** (exactly-equal condition for now) |
| /internal/dump (public) | **deleted** as FlowService RPC |
| (new, server-internal) | **InternalService.DumpFlowForContinueAsNew** + `ContinueAsNewDump` (proto pages; replaces JSON dump) |
| /triggerContinueAsNew | TriggerContinueAsNew |
| /info/healthcheck | HealthCheck |

## RPCs (WorkerService — worker)

| Old | New |
|-----|-----|
| workflowState/start (WaitUntil) | **InvokeWaitForMethod** |
| workflowState/decide (Execute) | **InvokeExecuteMethod** |
| workflowWorker/rpc | **InvokeWorkerRPC** |

## Types / messages

| Old | New |
|-----|-----|
| WorkflowStateOptions / StateOptions | StepOptions |
| StateDecision | StepDecision |
| StateMovement | StepMovement |
| StateCompletionOutput | StepCompletionOutput |
| WorkflowConditionalClose | FlowConditionalClose |
| WorkflowConfig | FlowConfig |
| optimize_activity (bool) | **step_durability** (`SYNC` / `ASYNC`; was false/true) |
| optimize_timer | **deleted** (server always optimizes timers) |
| WorkflowStatus | FlowStatus |
| WorkflowRetryPolicy | FlowRetryPolicy |
| EncodedObject (extStoreId/extPath/string data) | Value (+ EncodedObject = encoding + bytes payload only) |
| KeyValue (for attributes) | AttributeWrite on write (key + Value + optional IndexConfig); KV for key+Value pairs (attrs, step locals, events) |
| SignalCommand + InterStateChannelCommand | ChannelCondition |
| TimerCommand | TimerCondition |
| CommandRequest | WaitingCondition |
| CommandWaitingType | WaitingConditionType |
| CommandCombination | ConditionCombination |
| CommandResults | ConditionResults |
| command_id / command_ids | condition_id / condition_ids |
| timer_command_id / timer_command_index | timer_condition_id / timer_condition_index |
| TimerStatus (SCHEDULED/FIRED) + ChannelRequestStatus (WAITING/RECEIVED) | **ConditionStatus** (`WAITING` / `COMPLETED`) |
| SignalResult + InterStateChannelResult | ChannelResult (`repeated Value values`; singular `value` deleted) |
| ChannelCondition.at_least / at_most | `optional int32` (omit vs 0 distinguishable) |
| worker_url (StartFlow) | **worker_target** (plaintext gRPC `host:port`) |
| PersistenceLoadingPolicy.LockingKeys (RPC) | `InvokeRPCRequest.lock_attribute_keys` |
| WaitUntilApi*LoadingPolicy.LockingKeys | `StepOptions.wait_for_lock_attribute_keys` |
| ExecuteApi*LoadingPolicy.LockingKeys | `StepOptions.execute_lock_attribute_keys` |
| WorkflowDumpRequest/Response (JSON pages) | ContinueAsNewDumpRequest/Response + ContinueAsNewDump (proto) |
| InterStateChannelPublishing | ChannelMessage |
| SearchAttribute* types | deleted (IndexConfig / inferred from Value) |
| PersistenceLoadingPolicy / PersistenceLoadingType | deleted |
| DeciderTriggerType | deleted (WaitingConditionType only) |
| ExecutingStateIdMode / ExecutingStepTypeMode / ActiveStepTypeSearchingMode | **ActiveStepSearchMode** |
| ExecutingStep | **ActiveStep** (concept rename) |
| ENABLED_FOR_STATES_WITH_WAIT_UNTIL | ENABLED_FOR_STEPS_WITH_WAIT_FOR |
| start_step_id / completed_step_id / … | start_step_type / completed_step_type / … |
| FLOW_RESET_TYPE_STEP_ID | FLOW_RESET_TYPE_STEP_TYPE |
| FORCE_COMPLETE_ON_INTERNAL_CHANNEL_EMPTY / …_SIGNAL_… | FORCE_COMPLETE_ON_CHANNELS_EMPTY |
| GRACEFUL_COMPLETE_ON_ALL_CHANNELS_EMPTY | GRACEFUL_COMPLETE_ON_CHANNELS_EMPTY |
| channel_name (FlowConditionalClose) | channel_names (repeated) |
| ALLOW_IF_PREVIOUS_EXITS_ABNORMALLY | ALLOW_IF_PREVIOUS_EXISTS_ABNORMALLY |
| waitUntilApi* / startApi* / decideApi* dual fields | wait_for_* / execute_* only |
| skipWaitUntil | skip_wait_for |
| skipSignalReapply / skip_signal_reapply | skip_channel_messages_reapply |
| skipUpdateReapply / skip_update_reapply | skip_locking_rpc_reapply |
| waitForKey / wait_for_key | **deleted** |
| upsert_step_exe_locals on InvokeWorkerRPCResponse | **deleted** (RPC is not a step execution) |

## Internal serializable types

History / query / activity payloads (not FlowService RPCs). Encoded as binary
protobuf via Temporal/Cadence DataConverters (`service/common/converter`).

| Message / enum | Role |
|----------------|------|
| `InterpreterWorkflowInput` / `Output`, `ContinueAsNewInput` | Interpreter workflow I/O + CAN resume |
| `BlobStoreCleanupWorkflowInput` / `Output` | Maintenance workflow I/O |
| `InvokeWaitForMethodActivityInput` / `Output` | Wait-for activity |
| `InvokeExecuteMethodActivityInput` / `Output` | Execute activity |
| `DumpFlowForContinueAsNewActivityInput` / `Output` | CAN dump activity |
| `InvokeWorkerRPCActivityInput` / `Output` | Worker RPC activity |
| `CleanupBlobStoreActivityInput` / `Output` | Blob cleanup activity |
| `ExecuteRpcSignalRequest`, `SkipTimerSignalRequest`, `FailFlowSignalRequest` | System signals (`FlowConfig` reused for config update) |
| `GetAttributesQueryRequest` / `Response`, `PrepareRpcQueryRequest` / `Response` | Attribute / RPC prep queries |
| `GetCurrentTimerInfosQueryResponse`, `GetScheduledGreedyTimerTimesQueryResponse`, `DebugDumpResponse` | Timer / debug queries |
| `TimerInfo`, `TimerInfoList`, `InternalTimerStatus` | Nested timer snapshot data |
| `InterpreterError`, `InvokeRpcUpdateResult` | Serialized failure + sync-update RPC envelope |
| `BackendType` | Temporal vs Cadence on activity inputs |

In-process-only helpers (e.g. `BasicInfo`) stay as Go structs and are not listed here.

## Attribute indexing (new)

On SDK→server attribute writes, optional IndexConfig when indexing cannot be inferred:

- enable (bool)
- type: KEYWORD | TEXT | KEYWORD_ARRAY | INT | DOUBLE | BOOL | DATETIME
  - omit → KEYWORD for string/object; INT/DOUBLE/BOOL/DATETIME usually inferred from `Value`
- index_key (omit → attribute key; set for dynamic attrs merging into KEYWORD_ARRAY or TEXT)

## Lazy loading

- Always load all attributes (no LoadingPolicy).
- Large string/object may be blob-id arms on Value; hydrate via LoadBlobs.
