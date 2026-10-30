# Changelog

## Unreleased

### Added

- Automatic JSON codecs now resolve parameterized generic dataclasses,
  `Mapping` and `Sequence` types, fixed-length tuples with different item types,
  `list[Any]`, and unions declared directly, such as a Step input of
  `Receipt | None`. A directly declared union needs alternatives with distinct
  JSON kinds. An ambiguous one fails during codec resolution instead of
  choosing by order.
- Dataclass JSON decoding uses defaults and default factories for absent keys and
  reports missing required wire keys with `ValueError`.
- Dataclass fields support `dex_json_name` for wire names,
  `dex_json_omit_default` for opt-in default omission, and
  `dex_json_format="rfc3339"` for Go-compatible datetime formatting.
  The metadata also applies to lists, tuples, and dicts of dataclasses passed
  as Flow outputs, heartbeats, events, and Step locals.
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
