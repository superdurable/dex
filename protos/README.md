# Dex IDL (`protos/`)

Protobuf + gRPC interface between Dex SDKs and the Dex server.

- Source: [`dex.proto`](dex.proto)
- Rename catalog: [`../docs/design/idl-renames.md`](../docs/design/idl-renames.md)
- License: [Super Durable Source License 1.0](LICENSE), with legacy portions
  under their original terms ([`LEGACY_NOTICES.md`](LEGACY_NOTICES.md))

## Services

- **FlowService** — hosted by the server; SDKs call these RPCs
- **WorkerService** — hosted by the worker; the server calls `WaitFor`, `Execute`, and `WorkerRpc`

Workers call `SyncAttributeIndexes` before opening their listener. The RPC is
an internal startup protocol: it adds missing backend indexes, validates
existing types, and returns only after all requested indexes are readable.

## Worker targets

`WorkerTarget.address` is a plaintext gRPC target. Set `is_headless_address` for a
`host:port` whose DNS records represent individual WorkerService endpoints.

Set `FlowConfig.worker_target` when starting a flow. `UpdateFlowConfig` can
change the target for subsequent WorkerService calls while the flow is running.

## Step close decisions

`StepDecision.close_decision` explicitly ends a step thread or flow. Graceful
completion waits for other active steps; force completion and force failure end
the flow immediately; dead end ends only the current step thread.

`FORCE_COMPLETE_ON_CHANNELS_EMPTY` atomically completes when every named channel
is empty. It requires unique, non-empty `conditional_channel_names` and at least
one `next_steps` fallback. Other close types cannot include channel names or
next steps. `FORCE_FAIL` accepts only a string `close_input`, and `DEAD_END`
accepts no input.

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

Flow started/continued events expose the configured flow timeout as a protobuf
duration. An absent duration means the flow has no timeout.

Step method events expose the same `input`, `output`, and `context` structure
for sync, async, and async-fallback execution. Regular Activity inputs come from
scheduled history; successful local Activity inputs come from run-scoped
external storage. Missing or cleaned-up snapshots set `input.unavailable=true`.

Each event context includes the effective method timeout and retry policy.
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

`WorkerErrorResponse.stack_trace` carries an optional Worker-language stack.
The server persists it as `ErrorResponse.original_worker_error_stack_trace`
inside structured failure details. `StepMethodFailure` does not retain a
separate backend stack. The Java Worker caps its UTF-8 value at 16 KiB; other
Worker SDKs may omit the field.

`ErrorResponse.detail` and `original_worker_error_detail` are mutually exclusive.
Worker responses use the original field; transport failures without a
`WorkerErrorResponse` use `detail`. Consumers should prefer the original Worker
detail and fall back to `detail`.

`StepMethodFailure.backend_error` uses Temporal's application failure type,
timeout type, or fallback failure message. Cadence uses the activity failure
reason or timeout type. `details` is present only when the failure carries a
decodable `ErrorResponse`, so backend timeouts normally expose only
`backend_error`.

`LocalActivityInput` stores marker lineage only. `InternalLocalActivityInput`
is the local-only runtime argument. `InternalAsyncStepInputSnapshot` is the
run-scoped request and method-options record; neither internal type is returned
by `FlowService`.

`LoadBlobs` resolves batches of string/object blob arms. Callers should dedupe
by value kind and blob ID before loading. Missing objects and unconfigured store
IDs are omitted from the response map so callers can render them as unavailable.

History events describe flows, step methods, RPCs, and channel publications.
They do not expose workflow tasks, activities, markers, or raw backend events.

When a flow closes before a regular Step activity finishes, semantic history
emits `step_wait_for_pending` or `step_execute_pending` at its scheduled event
ID. Its time and phase report whether the backend activity started.

### Transient step movement

`InvokeWaitForMethodResponse.transient_step_movement` optionally runs one
skip-WaitFor step before the returned waiting condition starts. The movement
cannot configure failure-proceed behavior, and its Execute method must return a
DeadEnd close decision without next steps.

The server applies WaitFor writes before the transient Execute. It normalizes
timer deadlines and makes the source step resumable only after the transient
step succeeds. Continue-as-new may be requested during the transient Execute,
but the run transition waits for that Execute to finish.

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

`make -C server idl-code-gen` and `make -C sdk-go idl-code-gen` delegate to `make -C protos proto`.
Use `make -C server idl-code-gen-server` to regenerate only server Go stubs.
