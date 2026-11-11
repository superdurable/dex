# Changelog

## Unreleased

### Deprecated

- Deprecated `FlowConfig.delete_expired_history_objects`. It has no effect, and the SDK no longer sends it.
  Runs that start with server-zd 0.1.23 or later ignore the field and, when
  they offload, always delete the objects of expired history segments. A run
  that started under server-zd 0.1.22 still follows the value it recorded; to
  change it, use an SDK older than 1.40.0 or a direct gRPC UpdateFlowConfig.
  Existing code that sets it still compiles.

### Added and fixed

- Add `Join` and `JoinRound` to synchronize parallel branches of a Flow.
  `Join.fork` starts a round from the split Step's `execute` and builds the
  branch and join movements, so the application keeps no branch count or round
  key. Branches report with `end_branch` or `report_branch_result`. The join
  Step waits with `for_all_branches`, `for_first_branch`, or `for_branches` and
  reads `branch_results`. The next round's split Step resets the previous round
  with `for_remaining_branches`. RPC handlers use `fork_with_round_key`. A Join
  is a ChannelMap with the same name, and `PersistenceSchema.of` accepts it.
- Add `FlowConfig.offload_history` to choose whether a run offloads its history
  to object storage. `None` means `True`. Set it to `False` for many short runs
  that close soon, to avoid object storage requests. Set it at start through
  `StartFlowOptions.config_override` or for a SubFlow through
  `SubFlowOptions.config_override`; a SubFlow otherwise inherits its parent's
  value. `update_flow_config` rejects it with `INVALID_ARGUMENT`. Requires
  server-zd 0.1.23 or later; older servers ignore the field.
- Add `FlowConfig.delete_expired_history_objects` to control expired history object
  deletion at start, on update, and for SubFlows. Omission uses the server default at
  start and inherits for children; explicit false is preserved. Requires history
  offload. See the API reference for deletion timing and update semantics.
  Requires server-zd 0.1.22 or later; older servers ignore the value.
- Add Actions. Pass `action=Action(label, required_permission=..., condition=...)` to
  `@rpc` to declare an RPC that a person can invoke from Dex Web.
  `when_attribute_matches(attribute, AttributeMatch.equal_to(value), ...)` makes it
  available while a scalar Attribute of the Flow holds one of the values; the condition
  is optional. Registry construction validates the Action and registers the
  `DexWorkQueuePermissions` index. Workers send the Flow's Action permission mappings with
  every Step, timeout handler, and RPC result that writes a condition Attribute, and
  `start_flow` and SubFlow starts record the permissions the initial Attributes make
  available, as Go Workers and Clients do.

- Limit LoadBlobs requests to 1000 entries, preserving input order when hydrating
  more values with history offload enabled.

- Add `FlowConfig.history_window_events`, which sets about how many of the newest
  history events a run keeps for history reads and Time Travel. Set it at start
  through `StartFlowOptions.config_override`, change it with `update_flow_config`,
  or override it for a SubFlow; a SubFlow otherwise inherits its parent's value.
  `None` uses the server's `history.windowEvents` setting (10000 by default). Dex
  rejects a value that is not positive or exceeds the server's
  `history.maxWindowEvents` setting (100000 by default) with `INVALID_ARGUMENT`.
  Requires server-zd 0.1.21 or later; older servers ignore the field.
- Raise the gRPC receive limit from 4 MiB to `DEFAULT_GRPC_MAX_MESSAGE_BYTES` (32 MiB)
  on every FlowService and pull stream channel, matching the Go SDK. Set
  `grpc_max_message_bytes` on `ClientOptions` or `WorkerOptions` to change it. A
  positive value limits both directions.
- Split a LoadBlobs request in half when its response exceeds a gRPC size limit, so a
  Worker with an empty blob cache no longer fails on a long Flow. Both the SDK's
  receive limit (RESOURCE_EXHAUSTED) and the Dex server's send limit (OUT_OF_RANGE)
  trigger the split. A single blob that does not fit raises the new
  `BlobMessageTooLargeError`.

- Add `FlowServiceTlsOptions` to Client and Worker options for verified TLS 1.2+
  connections, including async APIs and pull streams. Trust is the gRPC default
  roots (grpcio's bundled roots, or `GRPC_DEFAULT_SSL_ROOTS_FILE_PATH` when set)
  plus the added roots. Adding roots fails when
  `GRPC_USE_SYSTEM_ROOTS_OVER_LANGUAGE_CALLBACK` is true, regardless of
  `GRPC_NOT_USE_SYSTEM_SSL_ROOTS`, including system roots selected
  with `GRPC_SYSTEM_SSL_ROOTS_DIR`. The preference flag accepts `true`, `t`, `yes`, `y`,
  and `1` as true, ignoring case. A configured `GRPC_DEFAULT_SSL_ROOTS_FILE_PATH`
  takes precedence and still supports added roots. Otherwise, disable the
  system-roots preference and pass the full OS or corporate CA bundle as
  `additional_root_cas_pem`, or configure the full bundle through
  `GRPC_DEFAULT_SSL_ROOTS_FILE_PATH`. Root-file errors include the configured
  path. An optional server name controls verification and authority.

- Add `Client.get_attributes` and `AsyncClient.get_attributes`, which read
  committed Attribute and AttributeMap instance values of the current or latest
  Flow run in one request, without an RPC or a Worker. Select a map instance
  with the new `AttributeMap.instance(key)`, which returns an
  `AttributeMapInstance`. The call returns `AttributeValues`, a read-only
  result keyed by the reads, whose lookups are typed by each read's value type.
  Unset reads are absent. Hard-delete Attributes are readable, and large values
  are resolved through LoadBlobs. A missing Flow raises `FlowNotFoundError`, and
  a definition that no registered Flow declares raises `FlowDefinitionError`.
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
- Automatic JSON codecs now resolve parameterized generic dataclasses,
  `Mapping` and `Sequence` types, fixed-length tuples with different item types,
  `list[Any]`, and unions declared directly, such as a Step input of
  `Receipt | None`. A directly declared union needs alternatives with distinct
  JSON kinds. An ambiguous one fails during codec resolution instead of
  choosing by order.
- A directly declared union accepts `Literal` alternatives, such as
  `Literal["draft", "sent"] | None`. Encoding and decoding both enforce
  `Literal` membership by exact type, so `true` never matches `Literal[1]`.
  Decoding a value outside the `Literal` raises `ValueError`. `Literal` and
  `Literal` unions inside dataclass fields and containers still return the JSON
  value unchanged.
- Dataclass JSON decoding uses defaults and default factories for absent keys and
  reports missing required wire keys with `ValueError`.
- Dataclass fields support `dex_json_name` for wire names,
  `dex_json_omit_default` for opt-in default omission, and
  `dex_json_format="rfc3339"` for Go-compatible datetime formatting.
  The metadata also applies to lists, tuples, and dicts of dataclasses passed
  as Flow outputs, heartbeats, events, and Step locals. Invalid metadata in
  such a collection, such as two fields with the same wire name, fails encoding
  like a directly passed dataclass instead of dropping a field.
  Existing field names and encoded defaults remain unchanged without metadata.
- `float` fields and items accept JSON integers, such as Go's encoding of a
  whole `float64`, and keep them as `int`.

### Behavior changes

- Optional and union fields, and unions inside containers, now decode a value
  into a dataclass, generic dataclass, Enum, or datetime alternative, or a
  container of those, when the value's JSON kind matches only that alternative.
  Earlier releases returned the raw JSON value. A value that does not fit that
  alternative, such as an object without a required key or an unknown Enum
  value, now raises. Other union values are still returned unchanged without a
  type check, and nested unions never fail during codec resolution.
- Fields typed by a parameterized generic dataclass, `Mapping` or `Sequence`
  fields of dataclasses, Enums, or datetimes, and type-variable fields that a
  base class binds to those types now decode typed values instead of raw JSON.
  Type-variable fields bound to other types keep their JSON values.
- An absent key no longer raises `KeyError`. It uses the field default, or
  raises `ValueError` for a required field. Decoding skips `init=False` fields
  instead of failing.
- A fixed-length tuple with different item types, such as `tuple[int, str]`,
  decodes and checks each item by position and requires the declared length.
  Earlier releases treated every item as the first item type and accepted any
  length. Tuples with one repeated item type are unchanged.

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

- Removed `Client.trigger_continue_as_new`, `AsyncClient.trigger_continue_as_new`,
  `FlowStatus.CONTINUED_AS_NEW`, `FlowConfig.continue_as_new_threshold`,
  `FlowConfig.continue_as_new_page_size_bytes`, and `include_continued_as_new`
  from both clients' `search_flows` methods.
- `FlowConfig` is a dataclass, and the two removed fields were its second and
  third fields. Every later field moves two positions forward, so a positional
  `FlowConfig(...)` call can set the wrong field. Construct `FlowConfig` with
  keyword arguments.
- Removed `WaitForStepCompletionOptions.internal_handler_timeout` and
  `WaitForAttributeOptions.internal_handler_timeout`. `request_id` remains
  accepted, but the server does not read it for these waits.
- Generated bindings rename `FlowExecutionID` to `FlowRunID` and
  `flow_execution_id` to `flow_run_id`. ProtoJSON uses `flowRunId`.
