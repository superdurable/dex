# Dex IDL (`protos/`)

Protobuf + gRPC interface between Dex SDKs and the Dex server.

- Source: [`dex.proto`](dex.proto)
- Rename catalog: [`../docs/design/idl-renames.md`](../docs/design/idl-renames.md)
- License: [Sustainable Use License 1.0](LICENSE), with legacy portions
  under their original terms ([`LEGACY_NOTICES.md`](LEGACY_NOTICES.md))

## Inspect legacy Go Server history

The `protos/codec-server` tool decodes known Dex protobuf payloads when
inspecting legacy Go Server history in Temporal Cloud or a local Temporal
Web UI. It is a local inspection tool; current Dex execution and semantic
history do not require it. Start it with:

```bash
dexcli codec-server
```

The server listens on `http://127.0.0.1:8804` and accepts only loopback
addresses. Pass a different loopback address when needed:

```bash
dexcli codec-server --address 127.0.0.1:8805
```

When developing from this repository, use
`go run ./cli/cmd/dexcli codec-server` or `make -C protos codec-server`.

In the Temporal Cloud Workflows page, select **Configure Codec Server**. Choose
**Use my browser setting and ignore Namespace-level setting**, then enter
`http://127.0.0.1:8804`. Leave **Pass access token** and **Include cross-origin
credentials** disabled. If Chrome requests Local Network Access, allow it for
`https://cloud.temporal.io`.

The browser calls the local server directly. Each user must run their own copy.
For a local Temporal Web UI, configure the same Codec Server URL in its
settings. The server accepts the Temporal Cloud UI origin and HTTP/HTTPS
origins on `localhost` or loopback IP addresses, including `127.0.0.1` and
`[::1]`, on any port. No additional arguments are needed when the local UI
selects another port. Other browser origins are rejected. The server does not
authenticate requests and must not be exposed beyond the local machine. Stop
it with `Ctrl-C`.

The `/decode` endpoint converts known Dex `binary/protobuf` payloads to
`json/protobuf`. The `/encode` endpoint performs the reverse conversion when
the input includes a known Dex `messageType`. Other payloads pass through
unchanged. Known types are the messages still defined in `dex.proto`; payloads
of the removed Go Server internal messages pass through unchanged.

## Services

- **FlowService** — hosted by the server; SDKs call these RPCs
- **WorkerTaskService** — hosted by the server; pull Workers open task streams
- **WorkerService** — describes the `WaitFor`, `Execute`, and `WorkerRpc` calls a
  Worker serves; Dex delivers them over WorkerTaskService and never dials Workers

`InvokeWaitForMethod` and `InvokeExecuteMethod` use a unary request and a
server-streaming response. `InvokeWorkerRPC` remains unary. A Step response may
emit heartbeat and Stream frames before exactly one result and clean EOF.

Workers call `SyncAttributeIndexes` before opening their listener. The RPC is
an internal startup protocol: it adds missing backend indexes, validates
existing types, and returns only after all requested indexes are readable.

## Channel message queues

Dex assigns every Channel publication a UUIDv7 `message_id`, replacing any ID
in an incoming protocol message. `GetChannelMessages` returns all pending
messages for one Channel in FIFO order. `DeleteChannelMessage` deletes one
pending message by ID and returns `CHANNEL_MESSAGE_NOT_FOUND` when it is absent.

Direct deletion verifies the pending message ID before committing. Channel
conditions continue to expose only Values to Step handlers.

`InvokeWorkerRPCResponse.delete_from_channel` stages deletions with other RPC
side effects. Set `InvokeRPCRequest.is_transactional` for one atomic read and
write operation. Dex validates all deletions before
committing any side effect. Attribute locking enables transactional execution
automatically, but Channel deletion alone does not. Nontransactional RPCs treat
a missing deletion as a no-op.

## RPC state loading

Nontransactional RPC inputs and outputs travel directly between the API and
Worker and are not persisted. Dex commits only the RPC's state changes.
Transactional RPC inputs and outputs are persisted, and a retry with the same
request ID returns the recorded response. Persisted payloads and state changes
use the configured Blob Store threshold.

Worker RPC requests always contain ordinary Attribute values and size metadata
for every known Channel and ChannelMap instance. AttributeMap entries and
pending Channel message envelopes are loaded only when the caller requests them
with `load_attribute_map_instances`, `load_channel_names`, or
`load_channel_map_instances`. A map instance name ending in `/` loads every
instance. The suffix names one instance.

The worker request echoes validated, sorted names so an empty loaded collection
is distinct from one that was not loaded. Pending messages retain FIFO order, server-generated
message IDs, and Values. Loading never consumes a message and does not add a Dex
semantic history event.

Names must be unique and non-empty. Loading,
transactional execution, and Attribute locking are independent. Transactional
execution makes staged effects atomic and validates Channel deletions. It does
not isolate handler reads from concurrent writers. When that isolation matters,
all cooperating Steps and RPCs must use the same Attribute lock.

`StepOptions` stores separate selection lists for WaitFor and Execute.
`FlowTimeoutHandlerOptions` stores the same lists for the synthetic timeout
Step. Each method still receives ordinary Attributes and every Channel's size
metadata. WaitFor and Execute use independent snapshots; Execute loads after the
winning Wait consumes messages. Retries reuse the first request snapshot for
that logical method execution.

WaitFor, Execute, timeout-handler, and RPC responses carry
`ChannelMessageDeletion` side effects outside `StepDecision`. Dex
commits successful responses in this order: Attribute writes, best-effort
Channel deletions, Channel publications, then the Step decision. A missing
message is a no-op for these handler responses.

Timeout-handler options are accepted only with a positive Flow timeout and a
resolved `HANDLER` policy. The options become Execute settings for the synthetic
timeout Step and survive SubFlow start and Flow retry. A no-input recovery
movement carries `Context.recovery_error` after the handler exhausts its retry
policy.

## Worker targets

Dex routes all Flow work to connected pull Workers. `FlowConfig.worker_target`
is optional. When it is set, `is_pull_mode` must be true. The removed
`address` and `is_headless_address` fields are reserved.

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
value, the next resume token, its server-assigned creation time, and its source.

`ListStreamMessages` returns one non-blocking, newest-first page of retained
messages. An empty `before_page_token` starts at the retained tail. A non-empty
token is an exclusive, scope-bound anchor for older messages. The response's
`next_page_token` is empty on the final page. If trimming removes the anchor and
places it before the retained head, the RPC returns an empty page instead of
restarting from the tail. Writes appended after the first page do not enter the
older-page chain. The required `page_size` must be positive and cannot exceed
the server's `streamStore.maxReadMessages` limit, which defaults to 1000.

Stream RPCs do not require a Flow to exist or remain active. Their availability
depends only on the configured Stream Store.

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
complete. Worker results that arrive after logical cancellation do not affect
Flow state. Cancellation does not add a Dex semantic history event.

`StepOptions.heartbeat_timeout_seconds` applies to every WaitFor and Execute
attempt. Zero selects the one-minute default. Explicit values must be at least
ten seconds. Dex does not generate automatic Step heartbeats. Worker heartbeat
frames extend the attempt's heartbeat deadline, and Stream frames count as
implicit heartbeats. A heartbeat Value is restored on the next attempt through
`Context.last_heartbeat_value`. An explicit heartbeat without a Value clears
the details. An implicit Stream heartbeat reuses the latest explicit heartbeat
state, including cleared details. Method history retains the configured value
in `StepMethodOptions`.

Step durability resolves from the method override, then `FlowConfig`, then
SYNC. Both durabilities record the same events. ASYNC advances without waiting
for the commit. Attempts default to two hours. Retry total duration defaults to
four hours.

## Search flows

`SearchFlowsRequest.include_continued_as_new` has no effect because the
Server does not create Continue-as-New runs. Keep the query unchanged across
pages.

`SearchFlows` returns each execution's flow ID, run ID, flow type, status,
start/close times, and all Indexed Attributes supplied by Dex visibility as
`indexed_attributes`. Indexed Attribute values use the `Value` oneof; the response does not
expose backend index types.

## Web introspection

`GetFlowSummary` returns Dex execution metadata. `GetHistoryEvents` returns
the Dex semantic events the Server records, and `WaitForHistoryEvent`
supports incremental refresh. `GetFlowState` returns the current state
snapshot for a running flow.

External `SetAttributes` writes appear as `RpcExecutionCompleted` events with
`is_set_attribute_api` set and the writes in `upsert_attributes`.

Flow started/continued events expose the configured flow timeout as a protobuf
duration. An absent duration means the flow has no timeout.

Step method events expose the same `input`, `output`, and `context` structure
for SYNC and ASYNC execution. A method whose Step input is not stored sets
`input.unavailable=true`.

Each event context includes the effective method timeout, heartbeat timeout,
and retry policy.

Retries expose only the immediately preceding failure as
`context.last_failure_info`. `StepMethodFailure.attempt` identifies that attempt
or the terminal attempt in `output.failure`. While a Step method is retrying,
`GetFlowState.active_step_executions.last_failure_info` exposes its latest
failure. Attempts are 1-based and cumulative in Worker `Context` and semantic
history.

`WorkerErrorResponse.stack_trace` carries an optional Worker-language stack.
The server persists it with the Step failure and exposes it as
`ServiceErrorResponse.original_worker_error_stack_trace` at the service and
semantic-history boundaries. `StepMethodFailure` does not retain a separate
stack. The Java Worker caps its UTF-8 value at 16 KiB; other
Worker SDKs may omit the field.

`Context.recovery_error` carries the final failure that selected a recovery
path. A failed WaitFor method passes it directly to Execute in the same run.
A failed Execute method stores it in the server-owned
`StepMovement.recovery_error_internal_only`, so the configured recovery Step
retains the error while queued. Worker-provided
movements cannot set this internal field. `RecoveryErrorInfo` retains only the
failure detail and type; Worker stack traces and retry delays are not recovery
inputs. If no `WorkerErrorResponse` exists, Dex synthesizes the
recovery information from the failure kind, such as a method or heartbeat
timeout.

`WorkerErrorResponse.retry_after_seconds` requests the next retry interval. A
positive value replaces the retry policy's computed backoff for that retry.

`ServiceErrorResponse.detail` and `original_worker_error_detail` are mutually exclusive.
Worker responses use the original field; transport failures without a
`WorkerErrorResponse` use `detail`. Consumers should prefer the original Worker
detail and fall back to `detail`.

`StepMethodFailure.backend_error` carries the Server's failure detail, or the
Worker error detail when the Server adds none. `details` carries the failure as
a `ServiceErrorResponse`, including the original Worker error fields.

`LoadBlobs` resolves batches of string/object blob arms. Callers should dedupe
by value kind and blob ID before loading. Missing objects and unconfigured store
IDs are omitted from the response map so callers can render them as unavailable.

History events describe flows, step methods, RPCs, and Channel publications or deletions.
They do not expose internal commands or raw storage records.

When a flow closes before a Step method finishes, semantic history emits
`step_wait_for_pending` or `step_execute_pending`. Its phase reports whether the
method attempt was scheduled or marked Running by the server before dispatch.
`Started` does not confirm that a Worker received or executed the method.

## Codegen

Regenerate checked-in stubs into SDK trees:

```bash
make -C protos proto
```

| Output | Replaces |
|--------|----------|
| `sdk-go/gen/dexpb/` | `sdk-go/gen/dexpb/` |
| `sdk-java/src/main/java/io/superdurable/gen/` | OpenAPI `build/generated` |
| `sdk-python/dex/dexpb/` | `sdk-python/dex/dex_api/` |
| `sdk-typescript/src/gen/` | `sdk-typescript/src/gen/` |

`make -C sdk-go idl-code-gen` delegates to `make -C protos proto`.
When `dex.proto` changes, commit every generated output from the full command.
Component-only targets are for troubleshooting without an IDL change.

## Protocol compatibility

FlowService exposes `GetServerInfo`. Its response contains the diagnostic Server
artifact version and the inclusive protocol interval supported by that Server.
Workers and dexcli embed their own inclusive Server protocol interval. They are
compatible when the intervals overlap and negotiate the highest common version.
Protocol numbers increase monotonically and are never reused.
