# Dex IDL (`protos/`)

Protobuf + gRPC interface between Dex SDKs and the Dex server.

- Source: [`dex.proto`](dex.proto)
- Rename catalog: [`../docs/design/idl-renames.md`](../docs/design/idl-renames.md)
- License: [Super Durable Source License 1.0](LICENSE), with legacy portions
  under their original terms ([`LEGACY_NOTICES.md`](LEGACY_NOTICES.md))

## Services

- **FlowService** — hosted by the server; SDKs call these RPCs
- **WorkerService** — hosted by the worker; the server calls `WaitFor`, `Execute`, and `WorkerRpc`

`InvokeWaitForMethod` and `InvokeExecuteMethod` use a unary request and a
server-streaming response. `InvokeWorkerRPC` remains unary. A Step response may
emit heartbeat and Stream frames before exactly one result and clean EOF.

Workers call `SyncAttributeIndexes` before opening their listener. The RPC is
an internal startup protocol: it adds missing backend indexes, validates
existing types, and returns only after all requested indexes are readable.

## Worker targets

`WorkerTarget.address` is a plaintext gRPC target. Set `is_headless_address` for a
`host:port` whose DNS records represent individual WorkerService endpoints.

Set `FlowConfig.worker_target` when starting a flow. `UpdateFlowConfig` can
change the target for subsequent WorkerService calls while the flow is running.

## Resumable Streams

`WriteStream` appends one best-effort message to the Stream instance identified
by `flow_id` and `stream_name`. Every write carries `stream_capacity_bytes` and a
non-empty `source`. Sources may repeat and may contain `#`. Every write appends;
the Store does not deduplicate messages. Step output uses
`#<stepExecutionID>` as source metadata.

Capacity applies across every Flow instance of the same Stream name. Reaching
the trim trigger schedules background global FIFO trimming toward the target.
A write that would exceed the hard capacity is not appended and returns
`ResourceExhausted`; retry it after trimming creates space.

`ReadStream` long-polls one message after an opaque, scope-bound resume token.
An empty token starts at the earliest retained message. A token older than the
retained head also resumes at that head. Each response includes the message
value, the next resume token, its Redis-derived creation time, and its source.

Stream RPCs do not require a Flow to exist or remain active. Their availability
depends only on the optional Redis Stream Store.

## Step close decisions

`StepDecision.close_decision` explicitly ends a step thread or flow. Graceful
completion waits for other active steps; force completion and force failure end
the flow immediately; dead end ends only the current step thread.

`FORCE_COMPLETE_ON_CHANNELS_EMPTY` atomically completes when every named channel
has neither queued messages nor messages committed to a Step that has not
finished processing `Execute`. Otherwise, it schedules the `next_steps`
fallback. It requires unique, non-empty `conditional_channel_names` and at
least one `next_steps` fallback. Other close types cannot include channel names
or next steps. `FORCE_FAIL` accepts only a string `close_input`, and `DEAD_END`
accepts no input.

## Step execution cancellation

`StepDecision.cancel_step_types` selects all queued or active executions of the
exact Step types in the current Flow. `cancel_sibling_step_types` additionally
requires the target and producer Step execution to have the same
`from_step_execution_id`. Step handlers may return both selectors. RPC responses
may return `cancel_step_types`, but the server rejects
`cancel_sibling_step_types` because an RPC invocation has no Step execution
lineage. Selectors are resolved from one snapshot after the producer's
successful side effects commit and before its next movements are enqueued.
Missing, completed, and previously canceled executions are no-ops. Selection
does not recurse into descendants or match future executions.

A queued movement receives its normal Step execution ID before being marked
complete. Dex invokes each active target's workflow cancellation handler;
backend activity results that arrive after logical cancellation do not affect
Flow state. Cancellation does not add a Dex semantic history event.

`StepOptions.heartbeat_timeout_seconds` applies to regular wait-for and execute
activities. Zero selects the one-minute default. Explicit values must be at
least `interpreter.interpreterActivityConfig.minimumStepHeartbeatTimeout`,
which defaults to ten seconds. Local activities ignore it, while an ASYNC
fallback regular activity uses it. Dex does not generate automatic Step heartbeats. Worker
heartbeat frames call the backend heartbeat API, and Stream frames count as
implicit heartbeats. A heartbeat Value is restored on the next attempt through
`Context.last_heartbeat_value`. An explicit heartbeat without a Value clears
the details. An implicit Stream heartbeat reuses the latest explicit heartbeat
state, including cleared details. Effective regular and local method history
retains the configured value in `StepMethodOptions`.

Step durability resolves from the method override, then `FlowConfig`, then
SYNC. Regular attempts default to two hours. Retry total duration defaults to
four hours. ASYNC uses a local Schedule-to-Close window of at most seven seconds
and three attempts before regular fallback.

## Search flows

`SearchFlows` returns each execution's flow ID, run ID, flow type, status,
start/close times, and all Indexed Attributes supplied by Dex visibility as
`indexed_attributes`. Indexed Attribute values use the `Value` oneof; the response does not
expose backend index types.

Temporal type metadata preserves numeric types. Cadence visibility payloads do
not include index types, so Dex infers numbers from JSON: integral JSON numbers
become `int_value`, and other numbers become `double_value`.

## Web introspection

`GetFlowSummary` returns Dex execution metadata. `GetHistoryEvents` converts
Temporal/Cadence history into Dex semantic events, and `WaitForHistoryEvent`
supports incremental refresh. `GetFlowState` returns the interpreter
snapshot for a running flow.

External `SetAttributes` writes appear as `RpcExecutionCompleted` events with
`is_set_attribute_api` set and the writes in `upsert_attributes`.

Flow started/continued events expose the configured flow timeout as a protobuf
duration. An absent duration means the flow has no timeout.

Step method events expose the same `input`, `output`, and `context` structure
for sync, async, and async-fallback execution. Regular Activity inputs come from
scheduled history; successful local Activity inputs come from run-scoped
external storage. A local failure that exhausts its retry budget before regular
fallback has no snapshot and sets `input.unavailable=true`, as do missing or
cleaned-up snapshots.

Each event context includes the effective method timeout, heartbeat timeout,
and retry policy.
Regular Activity options come from scheduled metadata; local Activity options
are retained with the stored request. Regular Activity input messages remain
unchanged and use a null second Activity argument.

SYNC retries expose only the immediately preceding failure as
`context.last_failure_info`. `StepMethodFailure.attempt` identifies that attempt
or the terminal attempt in `output.failure`. While a regular Step activity is
retrying, `GetFlowState.active_step_executions.last_failure_info` exposes the
backend pending-activity failure only when its deterministic activity ID still
matches an active Step execution. ASYNC local-activity retries are not observable
through backend describe; the failure becomes visible after execution falls
back to a regular activity.

ASYNC local and regular activities share one logical retry sequence. Maximum
attempts and total duration apply across both phases, and attempts remain
1-based and cumulative in Worker `Context` and semantic history. Fallback is
immediate, without the local failure's transition delay.
The regular policy subtracts consumed attempts and elapsed local duration, then
sets its initial interval to the original backoff for the cumulative attempt,
capped by the configured maximum interval. If either budget is exhausted, the
local failure is terminal and produces the Step failed event without scheduling
a regular activity.

`WorkerErrorResponse.stack_trace` carries an optional Worker-language stack.
The server persists it in `InternalWorkerError` inside the activity failure and
exposes it as `ServiceErrorResponse.original_worker_error_stack_trace` at the
service and semantic-history boundaries. `StepMethodFailure` does not retain a
separate backend stack. The Java Worker caps its UTF-8 value at 16 KiB; other
Worker SDKs may omit the field.

`Context.recovery_error` carries the final failure that selected a recovery
path. A failed WaitFor method passes it directly to Execute in the same run.
A failed Execute method stores it in the server-owned
`StepMovement.recovery_error_internal_only`, so the configured recovery Step
retains the error while queued and across continue-as-new. Worker-provided
movements cannot set this internal field. `RecoveryErrorInfo` retains only the
failure detail and type; Worker stack traces and retry delays are not recovery
inputs. If no `WorkerErrorResponse` exists, the interpreter synthesizes the
recovery information from the backend application or timeout type.

`WorkerErrorResponse.retry_after_seconds` requests the next retry interval.
Temporal applies it directly to the Activity failure's next-retry delay without
persisting it in `InternalActivityError`. Cadence rejects a nonzero value with an
`INVALID_USER_FLOW_CODE` validation error from the Step method Activity.

`ServiceErrorResponse.detail` and `original_worker_error_detail` are mutually exclusive.
Worker responses use the original field; transport failures without a
`WorkerErrorResponse` use `detail`. Consumers should prefer the original Worker
detail and fall back to `detail`.

`StepMethodFailure.backend_error` uses Temporal's application failure type,
timeout type, or fallback failure message. Cadence uses the activity failure
reason or timeout type. `details` is present only when the failure carries a
decodable `InternalActivityError` or `InternalLocalStepActivityFailure`. The
history client converts its activity error to `ServiceErrorResponse`; backend
timeouts normally expose only `backend_error`.

`LocalActivityMetadata` stores marker lineage only.
`InternalLocalActivityInput` is the local-only runtime argument carrying the
run start time and method options.
`InternalLocalStepActivityFailure` carries the local attempt count, first
attempt time, original method options, and nested `InternalActivityError` as one
failure detail. A regular activity carries one `InternalActivityError` detail.
Final workflow failures carry one `InternalFlowError`. Interpreter-originated
failures set `server_detail`; activity-originated failures set `activity_error`.
The service client converts both sources to `ServiceErrorResponse` at the public
boundary.
A fallback regular activity carries only its prior attempt count and
first-attempt time in `Context`.
`InternalAsyncStepInputSnapshot` is the run-scoped request and method-options
record; none of these internal types is returned by `FlowService`.

`LoadBlobs` resolves batches of string/object blob arms. Callers should dedupe
by value kind and blob ID before loading. Missing objects and unconfigured store
IDs are omitted from the response map so callers can render them as unavailable.

History events describe flows, step methods, RPCs, and channel publications.
They do not expose workflow tasks, activities, markers, or raw backend events.

When a flow closes before a regular Step activity finishes, semantic history
emits `step_wait_for_pending` or `step_execute_pending` at its scheduled event
ID. Its time and phase report whether the backend activity started.

## Codegen

Regenerate checked-in stubs into server and SDK trees:

```bash
make -C protos proto
```

| Output | Replaces |
|--------|----------|
| `server/gen/dexpb/` | `server/gen/dexpb/` |
| `sdk-go/gen/dexpb/` | `sdk-go/gen/dexpb/` |
| `sdk-java/src/main/java/io/superdurable/gen/` | OpenAPI `build/generated` |
| `sdk-python/dex/dexpb/` | `sdk-python/dex/dex_api/` |
| `sdk-typescript/src/gen/` | `sdk-typescript/src/gen/` |

`make -C server idl-code-gen` and `make -C sdk-go idl-code-gen` delegate to `make -C protos proto`.
When `dex.proto` changes, commit every generated output from the full command.
Component-only targets are for troubleshooting without an IDL change.
