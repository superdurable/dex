# Changelog

## Unreleased

### Added and fixed

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
