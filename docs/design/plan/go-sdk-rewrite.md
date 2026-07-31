# Go SDK rewrite plan

Status: Phases 1 through 4 are implemented. Phase 5 remains a
boundary only and requires its own design review before implementation.

## Current source of truth

This plan is based on the current:

- [`protos/dex.proto`](../../../protos/dex.proto)
- [`protos/README.md`](../../../protos/README.md)
- [`server/service/interpreter/activityImpl.go`](../../../server/service/interpreter/activityImpl.go)
- [`server/service/interpreter/workflowImpl.go`](../../../server/service/interpreter/workflowImpl.go)
- [`server/service/interpreter/channel/plan.go`](../../../server/service/interpreter/channel/plan.go)
- [`server/service/common/rpc/invoke.go`](../../../server/service/common/rpc/invoke.go)
- [`server/service/api/service.go`](../../../server/service/api/service.go)
- [`sdk-go/dex/registration.go`](../../../sdk-go/dex/registration.go)
- [`sdk-go/dex/rpc_registration.go`](../../../sdk-go/dex/rpc_registration.go)
- [`sdk-go/dex/proto_mapper.go`](../../../sdk-go/dex/proto_mapper.go)
- [`sdk-go/dex/hydration.go`](../../../sdk-go/dex/hydration.go)
- [`sdk-go/dex/blobcache/cache.go`](../../../sdk-go/dex/blobcache/cache.go)
- [`docs/design/transient-step-movement.md`](../transient-step-movement.md)

The old `sdk-go/dex` API is not a compatibility constraint. The product has not
launched, so the rewrite removes obsolete Workflow/State/WaitUntil,
DataAttribute/SearchAttribute, and Signal/InternalChannel concepts instead of
adding aliases.

### Server contracts the SDK must preserve

- `Flow`, `Step`, `Attribute`, `Channel`, `WaitFor`, and `Execute` are the
  canonical concepts.
- Persistence is the declared set of attributes plus channels.
- Attributes are unified. Indexing is an optional `IndexConfig` attached to an
  attribute write.
- Channels are unified and carry multiple FIFO values.
- Both channel bounds are optional:
  - neither bound: exactly one;
  - only `at_least`: at least N, with no upper bound;
  - only `at_most`: zero through N;
  - both: the inclusive range.
- WaitFor may carry one server-supported transient step movement. The Go SDK
  uses this internally and does not expose it to applications.
- `StepDecision` has normal next steps and a separate `CloseDecision`.
- A normal Execute must return a non-empty decision. Only conditional
  force-complete may combine a close decision with fallback next steps.
- `Context.from_step_execution_id` exposes step-execution lineage.
- WaitFor, Execute, and RPC may write attributes, record events, and publish
  channel messages. Step-execution locals pass from WaitFor to Execute.
- RPC may trigger next-step movements, but the current server rejects a close
  decision returned by RPC.
- Channel sizes are supplied only to Worker RPC invocations.
- Locking RPC, WaitForStepCompletion, and WaitForAttribute use Temporal
  synchronous updates and require an SDK-generated request ID.
- `WorkerTarget` belongs to `FlowConfig` and may describe a normal or headless
  plaintext gRPC target.
- Large string/object `Value` arms may contain blob IDs. Phase 4 wires the
  existing cache into hydration; cache storage policy remains independently
  designed.

## Phase boundaries

### Phase 1 — public API and type model

Design and add the public Go contracts used by application code:

- flow and step interfaces plus the RPC function signature;
- `PersistenceSchema`;
- typed attributes and channels;
- untyped step-execution locals and recorded events;
- invocation `Context`;
- waiting conditions and typed result accessors;
- step movements, close decisions, and step options;
- public client request/result structs and non-generic client methods;
- public SDK errors.

Phase 1 does not implement registration, WorkerService, gRPC calls, protobuf
mapping, value encoding, blob hydration, or blob caching.

### Phase 2 — value codec and protobuf mapping

Implement concrete-value encoding, pure proto conversion, error conversion,
and the internal hydration seam. The separately implemented blob-cache
component may later be injected behind that seam; this SDK plan does not define
its storage, eviction, size, recovery, or test strategy.

### Phase 3 — registration assembly

Build the private immutable registry used later by WorkerService. Validate flow,
step, persistence, and fallback definitions; erase generic step and RPC
handlers; discover Flow method RPCs; and assemble scoped lookups. Phase 3 adds
no WorkerService, invocation context, protobuf request handling, or transport.

### Phase 4 — WorkerService runtime

Implement the application-hosted plaintext gRPC worker around the Phase 3
registry. Add invocation contexts, request hydration and decoding, buffered
commit mapping, method errors, panic recovery, and worker lifecycle. Keep the
generated WorkerService and hydration machinery private.

### Phase 5 — FlowService client and integration migration

Provisional boundary only. Implement the Phase 1 client façade, migrate the Go
integration suite, and run it against the default Temporal-backed Dex server.

## Phase 2 detailed design

### Value encoding

Application code continues to use Go values and opaque `dex.Value`; `dexpb`
remains internal.

| Go value | Proto arm |
|---|---|
| string and named strings | `string_value` |
| signed integers | `int_value` |
| unsigned integers up to `math.MaxInt64` | `int_value` |
| float32 and float64 | `double_value` |
| bool and named bools | `bool_value` |
| all other JSON-compatible values | `obj_value`, encoding `"json"` |

Structs, maps, slices, arrays, `[]byte`, and non-indexed `time.Time` use JSON.
Ordinary nil and typed nil encode as a JSON null object. The proto null arm is
reserved for attribute deletion.

`Value.Decode` requires a non-nil pointer. It rejects overflow, incompatible
targets, malformed JSON, unknown object encodings, deletion markers, and blob
arms that have not passed through hydration. Dynamic failures return errors and
never panic.

### Indexed attributes

| Index type | Accepted Go values |
|---|---|
| keyword and text | string and named strings |
| keyword array | string slices and equivalent named slices |
| int | signed integers and unsigned integers up to `math.MaxInt64` |
| double | float32 and float64 |
| bool | bool and named bools |
| datetime | `time.Time` or an RFC3339Nano string |

An indexed delete carries both the null arm and the definition's index config.
An attribute without indexing omits `IndexConfig`.
Datetime strings accept UTC `Z` and numeric offsets, preserve fractional
seconds, and do not interpret numeric strings as Unix nanoseconds.

### Pure proto mapping

Phase 2 maps waits, conditions, decisions, movements, retries, step and flow
options, initial attributes, result values, statuses, search entries, and health
results without assembling a WorkerService or FlowService invocation.

Positive durations round up to whole seconds; negative durations fail.
Pointer durations retain absence. Unknown SDK or proto enum values fail.
Unnamed conditions receive deterministic reserved IDs in declaration order.
Explicit IDs must be non-empty, unique, and outside the reserved prefix.

### Errors and hydration

The internal error mapper preserves the gRPC code, Dex substatus and detail,
plus original worker code, type, and detail. A gRPC error without Dex details
uses `ErrorUncategorized`; local non-gRPC errors remain unchanged.

The private hydration seam accepts blob arms and returns concrete values in the
same order. It deduplicates repeated references and validates count and arm
kind. String cache payloads are raw UTF-8 bytes. Object cache payloads are
deterministic protobuf encodings of the complete `EncodedObject`.

Phase 2 does not call `LoadBlobs`, wire the disk cache, register flows or RPCs,
run WorkerService, or execute public client methods.

### Phase 2 exit gate

1. Concrete and indexed values round-trip without exposing `dexpb`.
2. All pure mappers and invariant failures have package-internal tests.
3. Error details and blob hydration contracts are independently testable.
4. Registration, WorkerService, and FlowService transport remain absent.

## Phase 3 detailed design

### Registration surface

Phase 3 adds no application-facing registry API. The SDK builds a private
registry from `[]Flow`:

```go
type registry struct {
	// unexported
}

func newRegistry(flows []Flow) (*registry, error)
```

Phase 4 calls this constructor while assembling WorkerService and keeps it
behind the public Worker lifecycle API defined below.
Application code continues to declare registration through `Flow.GetSteps`,
`Flow.GetPersistenceSchema`, and RPC methods on the Flow value.

There is no mutable `AddFlow`, `AddStep`, or `AddRPC` API. Construction is
atomic: validation and lookup assembly happen in temporary state, and any error
returns no registry. Registration errors are ordinary descriptive errors; no
new public error hierarchy is needed.

### Registry structure and lookup scope

The private registry owns:

- a flow lookup keyed by durable flow type;
- per-flow step lookups keyed by durable step type;
- per-flow RPC lookups keyed by exported Go method name;
- per-flow attribute and channel definition lookups.

Step and RPC names are scoped to one flow. The same durable step or RPC name may
appear in different flows. Flow types are registry-wide and must be unique.
Attribute names are unique within a flow's attribute namespace, and channel
names are unique within its channel namespace. An attribute and channel may
share a name because the server stores them separately.

All lookup APIs remain private. Phase 4 receives immutable descriptors rather
than raw maps and is responsible for converting a missing lookup into the
appropriate WorkerService error.

### Flow and persistence validation

`newRegistry` evaluates each supplied Flow once. It rejects:

- nil and typed-nil Flow values;
- empty or duplicate durable flow types;
- nil persistence definitions;
- empty or duplicate attribute names;
- empty or duplicate channel names;
- invalid attribute index types;
- conflicting index types for one non-empty shared index key.

A definition records whether it is static or a map. Registration uses that
metadata later to validate locks, channel references, and worker writes. A
static attribute's empty `IndexKey` resolves to its attribute name. An
attribute map with an empty `IndexKey` continues to index each concrete physical
key independently.

Index keys are server visibility names, so each effective key must use one index
type throughout the registry. Definitions in different flows may share that key
only when their index types agree.

### Generic step erasure

`StepDef` keeps its public opaque shape, but `DefineStep` and
`DefineStepAsStart` create a private generic adapter instead of storing an
unclassified `any`:

```go
// typedStepDef erases Step[IN] so differently typed steps can share []StepDef.
type typedStepDef[IN any] struct {
	step     Step[IN]
	starting bool
}

type StepDef interface {
	stepType() string
	stepInputType() reflect.Type
	stepOptions() *StepOptions
	stepValue() any
	isStarting() bool
	skipWaitFor() bool
	waitFor(Context, any) (Wait, error)
	execute(Context, any) (StepDecision, error)
}
```

`StepDef` is the sealed, non-generic form of `Step` for heterogeneous lists.
`typedStepDef` is the only implementation: it captures `IN` at `DefineStep` time
so `GetSteps`, movements, and execute-failure targets can share one slice type.

The adapter validates the concrete input type before invoking the typed
handler. A mismatch returns an error and never reaches application code. It
does not encode or decode protobuf values; Phase 4 performs that work before
and after calling the adapter.

Registration rejects:

- an empty or duplicate durable step type within one flow;
- a zero `StepDef`, nil handler, or typed-nil handler;
- more than one `DefineStepAsStart` entry;
- invalid step defaults or attribute locks, reporting the first illegal step
  in `GetSteps` order;
- an execute-failure target outside the same flow;
- an execute-failure target with a different input Go type;
- recursive execute-failure fallback configuration.

Zero steps and zero starting steps remain valid. A starting step is only the
default selected by `StartFlow`; it is not a separate handler kind.

The private `NoWaitFor` marker is authoritative. Registration caches
`skipWaitFor` and Phase 4 never invokes the marker's panic implementation. A
step that embeds `NoWaitFor` and also shadows `WaitFor` is still Execute-only.

Step defaults are obtained once during registration. Registered definitions
and returned option values transfer ownership to the SDK and must not be
mutated afterward.

### Registry-aware step references

Movements and execute-failure fallbacks continue to preserve their generic input
type through private adapters. Registration and Phase 4 must resolve each
reference through the current flow's step lookup.

The durable name alone is not enough. Resolution verifies that:

- the target step is registered in the current flow;
- the supplied generic input type exactly matches the registered input type;
- the movement input value is assignable to that type;
- registered target defaults, not an unregistered lookalike value, are used.

This prevents an application from constructing another Step value with the same
durable name and changing the registered handler or defaults. Phase 3 provides
the private resolver; Phase 4 applies it to movements returned by Execute and
RPC.

Conditional close decisions likewise resolve every channel through the current
flow schema. Duplicate, empty, undeclared, or wrong static/map references fail
before a WorkerService response is committed.

### Flow method RPC discovery

RPCs are not listed in a separate communication schema. Registration enumerates
the exported method set of the exact Flow value with `reflect.Type.NumMethod`.
A method is an RPC only when its signature is exactly:

```go
func(
	ctx dex.Context,
	input IN,
) (dex.RPCResult[OUT], error)
```

The receiver is supplied by reflection and is not part of the application
signature. `Context` must be the SDK interface, the second result must be
`error`, and the first result must be a concrete `RPCResult[OUT]`. Pointer
results and defined lookalike result types are not accepted. Every exported
method on the registered Flow value other than the `Flow` interface methods
(`GetFlowType`, `GetSteps`, `GetPersistenceSchema`) must match this RPC
signature; otherwise registration fails. Unexported methods are ignored.

The exported Go method name is the durable RPC name. The registry retains:

- the method bound to the exact registered Flow receiver;
- its input and output Go types;
- its durable method name.

Value-receiver and pointer-receiver RPCs are supported when the supplied Flow
value exposes them. The registry retains that exact receiver so constructor
dependencies stored on the Flow remain available. If a value-typed Flow
implements the `Flow` interface but exported methods exist only on the pointer
type, registration fails and names those methods so the application registers a
pointer instead of discovering an incomplete method set.

`RPCResult[OUT]` implements a private erasure contract so the reflected result
can expose its output and next movements without exporting a non-generic
wrapper. RPC invocation still receives and returns concrete Go values in Phase
3 tests; protobuf conversion and invocation state belong to Phase 4.

Package-level functions, closures, method expressions, and anonymous wrappers
are not registrable RPCs. The later non-generic client accepts a direct bound
Flow method value, validates its canonical `-fm` method identity, and derives
the same durable method name. It does not need a public communication schema.

### Handler lifecycle and concurrency

Registration retains the exact Flow and Step values supplied by the
application. It does not construct a handler per request and never calls
`WaitFor`, `Execute`, or an RPC while building the registry.

Phase 4 may invoke registered handlers concurrently. Flow and Step values must
therefore be immutable or concurrency-safe and must keep invocation-specific
state in `Context`, attributes, channels, or step-execution locals.

### Phase exclusions

Phase 3 does not implement:

- a gRPC server or `WorkerServiceServer`;
- protobuf request decoding or response mapping;
- concrete invocation `Context` values;
- buffered attribute, event, local, or channel commits;
- method panic recovery or worker error conversion;
- internal transient-step execution;
- worker startup, readiness, draining, or shutdown;
- FlowService client calls or RPC request-ID retries.

Those runtime concerns remain in Phase 4 or Phase 5. Phase 3 only proves that a
valid application definition can become an immutable, type-safe private
registry.

### Phase 3 exit gate

1. Heterogeneous generic steps assemble behind private typed adapters.
2. Flow method RPCs are discovered without an explicit RPC schema.
3. Invalid names, duplicates, schema references, options, and fallbacks fail
   atomically during registration.
4. Runtime step references resolve only to registered definitions in their
   current flow.
5. No application package imports `dexpb`, and no WorkerService or client
   transport is added.

### Tests

Phase 3 uses package-internal unit tests because no WorkerService transport
exists yet. Integration coverage cannot reach the registry until Phase 4.

Add focused tests for:

1. heterogeneous steps, zero or one starting step, and flow-scoped lookups;
2. nil, empty, duplicate, and typed-nil flow and step definitions;
3. duplicate persistence names, static/map metadata, and index-key conflicts;
4. `NoWaitFor` detection without calling its panic implementation;
5. typed adapter input validation and successful WaitFor/Execute dispatch;
6. undeclared locks, invalid fallback targets, mismatched input types, and
   fallback cycles;
7. value- and pointer-receiver RPC discovery, input/output type retention, and
   durable method names; rejection when pointer-only methods are invisible on a
   value-typed Flow;
8. rejection of exported non-RPC Flow methods, plus rejection of package
   functions, method expressions, closures, and wrappers as RPC identities;
9. lookalike Step references using registered defaults, plus undeclared or
   wrong-kind channel references;
10. atomic failure without a partially usable registry;
11. stable first-error reporting when multiple steps have invalid options.

Run Phase 3 verification through the Makefile:

```bash
make -C sdk-go unitTests 2>&1 | tee /tmp/test-go-sdk-phase3.log
make copyright-check 2>&1 | tee /tmp/test-go-sdk-phase3-copyright.log
```

Phase 4 integration tests must invoke registered WaitFor, Execute, and RPC
handlers through an in-process WorkerService and verify protobuf boundaries,
buffer commit/rollback, errors, and concurrency.

### Documentation

- Keep this plan linked from [`docs/README.md`](../../README.md).
- Update [`sdk-go/README.md`](../../../sdk-go/README.md) when Phase 3 lands with
  Flow registration, starting-step, RPC reflection, and concurrency rules.
- Update [`sdk-go/CONTRIBUTION.md`](../../../sdk-go/CONTRIBUTION.md) with the
  Phase 3 verification commands.
- Add registry construction to SDK examples only when the Phase 4 worker
  constructor gives applications a public entry point.

### UI/UX

N/A: no in-repo web UI.

## Phase 4 detailed design

### Public worker surface

Phase 4 adds one application-hosted worker. It owns the private Phase 3
registry and the generated WorkerService gRPC server:

```go
type WorkerOptions struct {
	BindAddress        string
	WorkerTarget       WorkerTarget
	FlowServiceAddress string
	BlobCache          *blobcache.Cache
}

type Worker struct {
	// unexported
}

func NewWorker(
	flows []Flow,
	options WorkerOptions,
) (*Worker, error)

func (worker *Worker) WorkerTarget() *WorkerTarget
func (worker *Worker) Start() error
func (worker *Worker) Stop(ctx context.Context) error
```

`NewWorker` calls `newRegistry` and returns registration or option errors before
opening a listener. Construction remains atomic: an invalid flow set returns no
Worker. The Worker retains the supplied Flow and Step values exactly as Phase 3
specifies.

An empty `BindAddress` uses `:8803`. It controls only the local plaintext gRPC
listener. `WorkerTarget` is the server-reachable target supplied later through
`StartFlowOptions.ConfigOverride.WorkerTarget` or `UpdateFlowConfig`.

An empty `WorkerTarget.Address` derives from `BindAddress`. A concrete bind host
and port are copied. An unspecified bind host such as `:8803`, `0.0.0.0:8803`,
or `[::]:8803` becomes `localhost:8803`, because a wildcard is not a dialable
advertised host. `WorkerTarget.Headless` is preserved. This default is intended
for a Worker and Dex server with matching local network reachability;
deployments with containers, pods, load balancers, or DNS must set the
advertised address explicitly.

`Worker.WorkerTarget()` returns a fresh copy of the resolved target for use in
flow options. It does not mutate a caller-owned option value.

The bind address and WorkerTarget are not required to match. For example, a
Worker may bind every local interface while Dex dials a headless Kubernetes
service:

```go
worker, err := dex.NewWorker(
	[]dex.Flow{Orders},
	dex.WorkerOptions{
		BindAddress: "0.0.0.0:8803",
		WorkerTarget: dex.WorkerTarget{
			Address:  "orders-worker.default.svc.cluster.local:8803",
			Headless: true,
		},
	},
)

runID, err := client.StartFlow(
	ctx,
	Orders,
	"order-1",
	OrderInput{},
	dex.StartFlowOptions{
		ConfigOverride: &dex.FlowConfig{
			WorkerTarget: worker.WorkerTarget(),
		},
	},
)
```

`WorkerTarget.Headless` describes how the server resolves the advertised
target; it does not change the local listener. The Worker resolves the empty
address default but never automatically registers or updates the target for a
flow.

An empty `FlowServiceAddress` uses `localhost:8801`. The Worker uses this
plaintext target only for private `LoadBlobs` calls. It does not expose
`LoadBlobs`, construct the Phase 5 public Client, or make any FlowService call
when a request has no blob arms.

`BlobCache` is optional and constructor-injected. The caller owns it: stopping
the Worker does not purge or close a shared cache. The Worker owns and closes
its private FlowService gRPC connection.

The runtime uses `slog.Default` for lifecycle, cache, and recovered-panic logs.
Phase 4 does not add a custom logging interface.

`Start` binds the configured address, serves WorkerService, and blocks. It may
be called once. A normal `Stop` makes `Start` return nil; bind and serve failures
are returned. The Worker does not install signal handlers or start itself in a
goroutine.

`Stop` is idempotent. It stops accepting calls and waits for in-flight handlers.
If `ctx` expires first, it force-stops the gRPC server, cancels in-flight RPC
contexts, and returns `ctx.Err()`. Calling `Stop` before `Start` succeeds and
prevents a later start. A Worker is one-shot and cannot restart after stopping.

The public `Worker` does not implement generated request methods. An unexported
server value implements `dexpb.WorkerServiceServer`, so application code never
uses `dexpb` to host or invoke the worker.

### Runtime structure

The Worker owns these private components:

```text
Worker
  registry                 immutable Phase 3 descriptors
  workerService            generated gRPC adapter
  hydrationCoordinator     cache + private FlowService LoadBlobs client
  grpcServer / listener    plaintext WorkerService transport
  lifecycle state          start, drain, force-stop, terminal error
```

Each gRPC call creates one independent invocation object. No attribute values,
condition results, locals, events, or buffered writes are retained on the
registered Flow or Step values. Calls for different flows and runs may execute
concurrently.

The private WorkerService handlers use a common pipeline:

1. validate the request envelope and resolve its flow, step, or RPC;
2. collect and hydrate every input value required by that method;
3. decode the typed handler input;
4. build the method-specific invocation Context;
5. invoke the registered handler with panic recovery;
6. validate registry-aware Wait, decision, and movement references;
7. encode the handler result and buffered mutations; and
8. return one response only after every conversion succeeds.

Any failure before step 8 discards the complete invocation buffer. Worker calls
never partially commit attributes, locals, events, or channel messages.

### Request validation and lookup

All three handlers reject a nil request or nil proto Context. WaitFor and
Execute require non-empty flow type, step type, step input, flow ID, run ID,
step-execution ID, `Attempt >= 1`, and a non-zero first-attempt timestamp. RPC
requires non-empty flow type, RPC name, input, flow ID, and run ID; its proto
does not carry step-attempt metadata.

Lookup is always scoped through the Phase 3 registry:

- an unknown flow, step, or RPC is `NotFound`;
- a WaitFor call for a registered `NoWaitFor` step is `FailedPrecondition` and
  never calls the marker's panic method;
- a malformed request or duplicate input key is `InvalidArgument`;
- a registered handler is selected only from the current flow descriptor.

The runtime never uses an application-supplied durable name to bypass the
registered descriptor. Runtime movements and conditional channel references
are resolved again before response mapping.

### Hydration and typed input decoding

Before decoding, the Worker flattens request values in deterministic request
order:

- WaitFor: step input, then attributes;
- Execute: step input, attributes, step-execution locals, channel result values;
- RPC: input, then attributes.

It calls the Phase 2 hydration seam once for that ordered list. Repeated blob
references are deduplicated without changing reconstructed request order.

The hydration coordinator resolves a blob as follows:

1. try `BlobCache.Get` when a cache is configured;
2. decode and validate the cached payload against the blob arm;
3. delete a missing or corrupt cache entry and treat it as a miss;
4. batch all misses into one private `FlowService.LoadBlobs` request;
5. require one concrete response of the correct arm for every requested ID;
6. use the fresh response even when cache admission rejects it; and
7. log cache read, deletion, or write errors while continuing with a valid
   fresh response.

An unavailable FlowService, missing result, wrong result kind, or response that
still contains a blob arm fails the Worker call. Cache payloads retain the Phase
2 string/object format. The runtime does not change cache capacity, eviction,
recovery, or directory-ownership semantics.

Typed step and RPC inputs are allocated from the registered `reflect.Type`,
decoded through the Phase 2 codec, and passed through the Phase 3 erased
adapter. Decode failures return errors; reflection and codec failures never
panic across the gRPC boundary.

### Invocation Context

The private invocation type implements `Context`, `attributeInvocation`, and
`channelInvocation`. It embeds the incoming gRPC context, so deadlines,
cancellation, and forced Worker shutdown propagate to application handlers.

Metadata maps directly from proto Context. Unix-second timestamps use
`time.Unix(seconds, 0)`. Step-execution IDs, lineage, first-attempt time, and
attempt are populated for WaitFor and Execute. RPC returns empty
step-execution IDs, zero `FirstAttemptAt`, and zero `Attempt` because the server
supplies no step attempt metadata for RPC.

Method-specific operations are enforced at runtime:

| Operation | WaitFor | Execute | RPC |
|---|:---:|:---:|:---:|
| attribute Get/Set/Delete | yes | yes | yes |
| channel Publish | yes | yes | yes |
| RecordEvent | yes | yes | yes |
| SetStepExecutionLocal | yes | no | no |
| GetStepExecutionLocal | no | yes | no |
| channel condition results | no | yes | no |
| timer/wait-failure helpers | no | yes | no |
| channel Size | no | no | yes |

Operations returning an error use `errInvalidInvocationContext` outside their
allowed method. `Channel.Size` has no error result, so invalid use panics; the
Worker recovers it and returns a structured method failure. Invocation values
must not be retained or used after the handler returns.

`HasTimerFired` is true when any timer result is completed.
`HasTimerFiredByIndex` uses the timer-result order supplied by the server and
returns false outside Execute or for an invalid index. Natural completion and
`SkipTimer` are intentionally indistinguishable. `WaitForMethodFailed` reads
`ConditionResults.wait_for_failed`.

### Attributes and buffered reads

Incoming attributes are validated as unique, non-empty physical keys and stored
as hydrated concrete values. Attribute operations resolve the registered
definition by logical name and verify static/map kind. The runtime uses the
registered index configuration rather than trusting a same-name lookalike
handle.

Map operations derive the physical key with the Phase 2 escaping rule. Get
first observes the invocation's latest buffered write:

- a buffered Set decodes that value;
- a buffered Delete returns `found=false`;
- otherwise Get reads the hydrated request snapshot; and
- a missing key returns the typed zero value with `found=false`.

Set and Delete encode immediately. A failed encode leaves the previous buffer
unchanged. Multiple writes to one physical key use last-write-wins and emit one
`AttributeWrite`; distinct keys retain first-write order for deterministic
responses. Deletes keep the registered index config.

### Step-execution locals and events

Execute validates incoming locals as unique non-empty keys. Get requires a
non-nil pointer and decodes the hydrated value. Missing locals return
`found=false`.

WaitFor may Set a local more than once; the last value wins and the response
uses first-write key order. Execute and RPC reject Set. Internal transient
Execute receives no locals and therefore cannot read source-step locals.

`RecordEvent` requires a non-empty name. One name may be recorded once per
method invocation; a duplicate returns an error without replacing the first
value. Events preserve call order. They remain outside `PersistenceSchema`.

### Channels and condition results

Publish resolves a registered static or map channel, encodes the value
immediately, and appends one `ChannelMessage`. Multiple publishes preserve call
order and are never coalesced.

Wait mapping validates every channel condition against the current flow schema.
A same-name static/map mismatch, empty map instance, invalid bounds, undeclared
channel, condition-ID error, or encoding error rejects the complete response.
`SkipWaitImmediately` maps to an omitted waiting condition.

Execute stores hydrated condition results in server order. A typed
`GetConditionResults` call:

- resolves the requested registered channel and physical map instance;
- selects completed channel results with that physical name;
- concatenates values in the request's channel-result order; and
- decodes each value into the channel's `T`.

Waiting results contribute no values. Duplicate results for the same concrete
channel are intentionally concatenated. A malformed status, empty channel
name, or undecodable value fails the invocation.

RPC channel sizes start from `InvokeWorkerRPCRequest.channel_infos`. Missing
entries read as zero. Each successful Publish earlier in the same RPC
increments the matching concrete size, so `Size` observes invocation-local
publishes. Empty names and negative incoming sizes are invalid. WaitFor and
Execute never receive or synthesize channel sizes.

### WaitFor response

WaitFor invokes the registered typed handler unless `skipWaitFor` is true. A
successful response contains:

- the registry-validated waiting condition;
- buffered attribute writes;
- buffered step-execution locals;
- recorded events; and
- published channel messages.

The Worker never sets `local_activity_input`; that field is server-owned.

Transient movement remains internal. `Wait` reserves an unexported optional
movement used only by SDK-owned machinery. If present, the mapper requires a
registered execute-only target, forces `skip_wait_for`, rejects failure-proceed
options, and leaves lineage empty. Public Wait constructors cannot set it.
Normal application WaitFor responses therefore omit
`transient_step_movement`. The server remains authoritative for requiring a
transient Execute to return only `DeadEnd()`.

### Execute response

Execute receives condition results and source-step locals, calls the registered
typed handler, and requires a non-empty `StepDecision`.

Every movement is resolved through `registeredFlow.resolveMovement`. Mapping
uses the registered target's immutable defaults plus the explicit movement
overrides. It rejects unregistered lookalikes, input-type mismatches, invalid
options, and worker-owned lineage fields.

Conditional close channels are resolved through the registered flow before
mapping. All other close decisions remain mutually exclusive with next steps.
The response contains the decision plus buffered attributes, events, and
channel messages. The Worker leaves `local_activity_input` empty and does not
emit Execute local writes.

### RPC response

RPC resolves the reflected method from the current flow, decodes its registered
input type, and invokes the exact bound receiver retained by Phase 3.

The erased `RPCResult` output is encoded directly. Optional movements use the
same registry-aware resolution as Execute. RPC cannot return a close decision
or step-execution locals. The response contains output, optional next steps,
and buffered attributes, events, and channel messages.

### Method failures and panic recovery

Every Worker failure crosses gRPC as a status with one `WorkerErrorResponse`.
The server can therefore preserve the original worker code, concrete error
type, and detail in its public `dex.Error` conversion.

| Failure | gRPC code |
|---|---|
| malformed request or handler result | `InvalidArgument` |
| unknown flow, step, or RPC | `NotFound` |
| WaitFor called for `NoWaitFor` | `FailedPrecondition` |
| handler-returned gRPC status | preserve its code |
| ordinary handler error | `Unknown` |
| request cancellation/deadline | `Canceled` / `DeadlineExceeded` |
| panic or SDK invariant failure | `Internal` |

For a returned error, `WorkerErrorResponse.error_type` is its concrete Go type.
For a panic it records the recovered value type. Panic stacks are logged by the
Worker but are not returned over gRPC. A panic in application code, reflection,
or an invocation helper is recovered at the outer handler boundary after all
buffers have become unreachable.

### Concurrency and ownership

The registry and registered definitions are immutable after `NewWorker`.
Separate invocation objects make concurrent calls independent, and the worker
implementation must pass the race detector.

Application Flow and Step values may be called concurrently and remain the
application's responsibility to make concurrency-safe. Invocation Context
mutation is synchronous handler state: applications must finish any goroutines
using it before returning.

Request protobuf values are freshly deserialized and transferred to the
invocation. The runtime does not defensively clone them. Encoded response
values and mutation buffers become owned by the response only after successful
mapping.

### Phase exclusions

Phase 4 does not implement:

- public FlowService Client methods or request-ID retries;
- automatic WorkerTarget registration or service discovery;
- TLS WorkerService or FlowService transport;
- public gRPC request/response types or a custom service registrar;
- a public transient-step constructor;
- custom value codecs or codec registries; or
- blob-cache eviction, recovery, or capacity changes.

### Phase 4 exit gate

1. Applications can construct and run a Worker from `[]Flow` without importing
   `dexpb`.
2. WaitFor, Execute, and RPC dispatch through the Phase 3 registry over gRPC.
3. Typed values, attributes, channels, locals, results, and decisions cross the
   proto boundary through Phase 2 codecs and registry-aware validation.
4. Successful handlers commit their complete buffer; errors and panics commit
   nothing.
5. Blob-backed request values hydrate through private LoadBlobs and optional
   cache wiring before application decode.
6. Graceful and forced shutdown have deterministic, race-free behavior.
7. The Worker exposes its resolved target for flow options without making
   Client calls or updating flows automatically.
8. No public Client transport or registration API is added.

### Tests

Phase 4 adds in-process WorkerService integration tests using a real gRPC
server and client. Package-internal tests may use generated stubs; application
examples and external contract tests still do not import `dexpb`.

Cover these scenarios:

1. Worker construction rejects invalid registration and target configuration;
   empty target addresses derive from concrete and wildcard bind addresses.
2. `Start`/`Stop` lifecycle is one-shot, idempotent, and deadline-bounded.
3. WaitFor dispatch decodes typed input and maps attributes, locals, events,
   channel publishes, timers, combinations, and immediate execution.
4. Execute dispatch decodes locals and channel results, exposes timer and
   WaitFor-failure helpers, and maps every next/close decision.
5. Multiple completed conditions on one channel concatenate values in server
   order, including static and map channels.
6. RPC reflection dispatch preserves typed input/output, movement validation,
   attributes, events, publishes, and channel sizes including local publishes.
7. Set-then-Get and Delete-then-Get observe buffered state; duplicate events
   and malformed incoming keys fail.
8. Returned errors, gRPC status errors, panics, cancellation, and mapping
   failures return WorkerError details and commit no buffered mutations.
9. Unknown flow/step/RPC, a WaitFor request for `NoWaitFor`, undeclared schema
   handles, and lookalike movement targets return the planned status codes.
10. A fake FlowService plus real disk cache covers cache hit, miss, corrupt
   payload reload, batching, deduplication, wrong kind, missing result, and
   cache failure while using a fresh result.
11. Concurrent WaitFor, Execute, and RPC calls isolate invocation state under
    the race detector; graceful stop drains an in-flight handler and deadline
    expiry cancels it.
12. The internal transient mapper emits valid skip options and rejects a
    waiting target or failure-proceed configuration without exposing an
    application constructor.

Run Phase 4 verification through the Makefile:

```bash
make -C sdk-go unitTests 2>&1 | tee /tmp/test-go-sdk-phase4.log
make -C sdk-go workerIntegTests 2>&1 | tee /tmp/test-go-sdk-phase4-worker.log
make -C sdk-go blobCacheTests 2>&1 | tee /tmp/test-go-sdk-phase4-blobcache.log
make copyright-check 2>&1 | tee /tmp/test-go-sdk-phase4-copyright.log
```

`workerIntegTests` runs the WorkerService integration package with `-race`.

The default Temporal-backed end-to-end suite remains Phase 5 because Phase 4
does not yet provide the public Client calls needed to start and drive runs.

### Documentation

- Keep this plan linked from [`docs/README.md`](../../README.md).
- When Phase 4 lands, update [`sdk-go/README.md`](../../../sdk-go/README.md)
  with Worker construction, bind-versus-advertise addresses, concurrency,
  hydration, error, and shutdown semantics.
- Update [`sdk-go/CONTRIBUTION.md`](../../../sdk-go/CONTRIBUTION.md) with the
  worker integration and race verification commands.
- Update the Go examples to construct a Worker from their Flow values and show
  signal-driven `Stop(ctx)` without importing generated protobufs.
- Update [`sdk-go/dex/blobcache/README.md`](../../../sdk-go/dex/blobcache/README.md)
  with cache ownership and Worker hydration wiring; do not change cache policy
  documentation.

### UI/UX

N/A: no in-repo web UI.

## Phase 1 detailed design

### Design rules

1. Application code imports `dex`, not `gen/dexpb`.
2. Persisted flow, step, attribute, and channel names are explicit strings.
   RPC names come from registered Flow method names.
3. Typed attributes and channels are immutable and safe as package variables.
4. Methods that use invocation state take `Context` explicitly. Flow, step, and
   RPC implementations must not retain a current invocation.
5. Dynamic encoding failures return errors; they do not panic.
6. Constructors and unexported fields protect server invariants. Do not expose
   structs that permit impossible proto combinations.
7. Durations use `time.Duration`. A later mapper converts them to whole seconds,
   rounding positive sub-second remainders up so the SDK never shortens a
   requested timeout.
8. No public compatibility types retain the old SDK vocabulary.

### Package surface

Phase 1 owns these conceptual files under `sdk-go/dex/`:

```text
flow.go             Flow and persistence declaration
step.go             Step, StepDef, NoWaitFor, StepDefaults
context.go          Context, invocation metadata, locals, and events
attribute.go        Attribute, AttributeMap, indexing, invocation operations
channel.go          Channel, ChannelMap, publish, size, bounded conditions
condition.go        Wait, timers, and combinations
decision.go         StepMovement, StepDecision, CloseDecision helpers
options.go          retry, durability, step, start, and flow options
rpc.go              RPC and RPCResult
client.go           Client façade declarations and public result types
errors.go           public error model
```

This is a logical split, not permission to add runtime implementations during
Phase 1.

### Flow and explicit durable names

Application code implements a minimal flow interface:

```go
type Flow interface {
	GetFlowType() string
	GetSteps() []StepDef
	GetPersistenceSchema() PersistenceSchema
}
```

`GetFlowType` must return a non-empty, explicit durable name. Registration of a
flow together with its heterogeneous steps and RPCs belongs to Phase 3.
`GetSteps` supplies every step through an opaque `StepDef`. Generic handler
adapters remain internal implementation details, not public Phase 1 API.

A flow declares at most one starting step with `DefineStepAsStart`. Other
steps use `DefineStep`. A flow without a starting step starts with no step,
matching dex-base and the current server contract.

### Step handlers

Every application step implements the same `Step[IN]` interface:

```go
type Step[IN any] interface {
	GetStepType() string
	GetStepOptions() *StepOptions
	WaitFor(ctx Context, input IN) (Wait, error)
	Execute(ctx Context, input IN) (StepDecision, error)
}

type StepDef interface {
	// unexported
}

func DefineStep[IN any](step Step[IN]) StepDef
func DefineStepAsStart[IN any](step Step[IN]) StepDef

type NoWaitFor[IN any] struct{}

type DefaultStepOptions struct{}

type StepDefaults[IN any] struct {
	DefaultStepOptions
	NoWaitFor[IN]
}

func (NoWaitFor[IN]) WaitFor(Context, IN) (Wait, error) {
	panic("NoWaitFor: framework must skip WaitFor")
}

func (NoWaitFor[IN]) noWaitFor() {}

func (DefaultStepOptions) GetStepOptions() *StepOptions {
	return nil
}
```

An Execute-only step embeds `StepDefaults[IN]`, or embeds `NoWaitFor[IN]` and
implements `GetStepOptions` itself. `NoWaitFor` supplies the interface method
and carries an unexported marker. Phase 3 registration detects that marker and
sets `skip_wait_for`; it never calls the supplied `WaitFor` method. There is no
public `SkipWaitFor` field.

A step that waits implements `WaitFor` and may implement `GetStepOptions`
directly. It must not embed `NoWaitFor` or `StepDefaults`. `GetStepType` must
return a non-empty, explicit durable name.

`DefineStep` and `DefineStepAsStart` retain the handler's input type while
building the heterogeneous `GetSteps` result. Phase 3 validates duplicate step
types and rejects flows with multiple starting steps.

`GetStepOptions() == nil` uses server defaults. A non-nil value supplies
immutable defaults whenever the step is scheduled. The starting step uses those
options directly; movement options may override them field by field.

Example:

```go
type ApproveOrderStep struct {
	dex.DefaultStepOptions
}

func (ApproveOrderStep) GetStepType() string {
	return "approve-order"
}

func (ApproveOrderStep) WaitFor(
	ctx dex.Context,
	input ApproveOrderInput,
) (dex.Wait, error) {
	return dex.AnyOf(
		ApprovalChannel.ForOne(),
		dex.Timer(
			input.Timeout,
			dex.WithConditionID("approval-timeout"),
		),
	), nil
}

func (ApproveOrderStep) Execute(
	ctx dex.Context,
	input ApproveOrderInput,
) (dex.StepDecision, error) {
	if ctx.HasTimerFired() {
		return dex.ForceFail("approval timed out"), nil
	}
	approvals, err := ApprovalChannel.GetConditionResults(ctx)
	if err != nil {
		return dex.StepDecision{}, err
	}
	_ = approvals
	return dex.GoTo(ShipOrder, ShipOrderInput{
		OrderID: input.OrderID,
	}), nil
}

var ApproveOrder = ApproveOrderStep{}
var _ dex.Step[ApproveOrderInput] = ApproveOrder

type ShipOrderStep struct {
	dex.StepDefaults[ShipOrderInput]
}

func (ShipOrderStep) GetStepType() string {
	return "ship-order"
}

func (ShipOrderStep) Execute(
	ctx dex.Context,
	input ShipOrderInput,
) (dex.StepDecision, error) {
	return dex.DeadEnd(), nil
}

var ShipOrder = ShipOrderStep{}
var _ dex.Step[ShipOrderInput] = ShipOrder
```

### Invocation Context

`Context` embeds the request context and exposes current proto context using
Go-idiomatic names and time values:

```go
type Context interface {
	context.Context

	FlowID() string
	RunID() string
	FlowStartedAt() time.Time
	StepExecutionID() string
	FromStepExecutionID() string
	FirstAttemptAt() time.Time
	Attempt() int32

	HasTimerFired() bool
	HasTimerFiredByIndex(index int) bool
	WaitForMethodFailed() bool

	SetStepExecutionLocal(key string, value any) error
	GetStepExecutionLocal(key string, valuePtr any) (found bool, err error)
	RecordEvent(name string, value any) error
}
```

Semantics:

- `StepExecutionID` and `FromStepExecutionID` are empty for RPC invocations.
- `HasTimerFired` is true when any timer completed. A timer completed through
  `SkipTimer` counts as fired.
- `HasTimerFiredByIndex` uses the timer declaration order in WaitFor. It returns
  false when the index is out of range.
- Both timer helpers return false outside Execute or when Execute skipped
  WaitFor.
- `WaitForMethodFailed` is true only when Execute follows a failed WaitFor under
  `ProceedOnFailure`.
- Step-method `Attempt` starts at one. RPC returns zero `Attempt` and a zero
  `FirstAttemptAt` because its request carries no attempt metadata.
- writes, events, and channel publishes are buffered until the method returns
  successfully;
- attribute reads observe earlier writes in the same invocation.

Step-execution locals deliberately do not carry a Go type:

```go
if err := ctx.SetStepExecutionLocal("snapshot", snapshot); err != nil {
	return dex.Wait{}, err
}

var snapshot OrderSnapshot
found, err := ctx.GetStepExecutionLocal("snapshot", &snapshot)
if err != nil {
	return dex.StepDecision{}, err
}
```

`valuePtr` must be a non-nil pointer. `SetStepExecutionLocal` is valid in
WaitFor; `GetStepExecutionLocal` is valid in Execute for the same step
execution. Locals are unavailable to RPC and internal transient Execute.

`RecordEvent` accepts arbitrary data because events are not read through the
SDK. An event name may be recorded once per method invocation. Events do not
belong to `PersistenceSchema`.

### PersistenceSchema

Persistence remains the combination of attributes and channels:

```go
type AttributeDef interface {
	attributeName() string
	attributeIndex() *AttributeIndex
	attributeIsMap() bool
}

type ChannelDef interface {
	channelName() string
	channelIsMap() bool
}

type PersistenceSchema struct {
	Attributes []AttributeDef
	Channels   []ChannelDef
}
```

`AttributeDef` and `ChannelDef` are sealed, erased interfaces
implemented by the generic definitions below. Unexported methods keep
third-party types from satisfying them. A flow declares both static and
map definitions in this schema. Concrete `Attribute` / `Channel` values still
expose public `AttributeName` / `ChannelName` for application use.

Step-execution locals and record events are not flow persistence definitions:
locals have one step-execution scope, while events are history annotations.

### Typed attributes

Definitions are immutable:

```go
type Attribute[T any] struct {
	// unexported
}

type AttributeMap[T any] struct {
	// unexported
}

func DefineAttribute[T any](
	key string,
	options ...AttributeOption,
) Attribute[T]

func DefineAttributeMap[T any](
	name string,
	options ...AttributeOption,
) AttributeMap[T]
```

`AttributeMap` maps an application instance key to one flat server attribute
key. Physical keys and their escaping are internal SDK details.

Invocation operations take `Context` explicitly:

```go
func (a Attribute[T]) Get(ctx Context) (value T, found bool, err error)
func (a Attribute[T]) Set(ctx Context, value T) error
func (a Attribute[T]) Delete(ctx Context) error

func (a AttributeMap[T]) Get(
	ctx Context,
	instance string,
) (value T, found bool, err error)
func (a AttributeMap[T]) Set(
	ctx Context,
	instance string,
	value T,
) error
func (a AttributeMap[T]) Delete(ctx Context, instance string) error
```

`found` distinguishes a missing attribute from the zero value. `Delete` maps to
the proto null arm. A delete retains the definition's index configuration so an
indexed value is also removed from visibility.

Indexing is opt-in:

```go
type IndexType uint8

const (
	IndexKeyword IndexType = iota + 1
	IndexText
	IndexKeywordArray
	IndexInt
	IndexDouble
	IndexBool
	IndexDatetime
)

type AttributeIndex struct {
	Type     IndexType
	IndexKey string
}

func Indexed(index AttributeIndex) AttributeOption
```

An empty `IndexKey` uses the concrete attribute key. A non-empty key supports
dynamic attributes that write to a shared visibility key. Phase 2 validates
that encoded values are compatible with the selected index type.

### Typed channels

Static and map channel definitions are immutable:

```go
type Channel[T any] struct {
	// unexported
}

type ChannelMap[T any] struct {
	// unexported
}

func DefineChannel[T any](name string) Channel[T]
func DefineChannelMap[T any](name string) ChannelMap[T]
```

Publishing uses invocation state, while wait construction does not:

```go
func (c Channel[T]) Publish(ctx Context, value T) error
func (c ChannelMap[T]) Publish(ctx Context, instance string, value T) error

func (c Channel[T]) ForOne(options ...ConditionOption) Condition
func (c Channel[T]) ForN(count int, options ...ConditionOption) Condition
func (c Channel[T]) AtLeast(count int, options ...ConditionOption) Condition
func (c Channel[T]) AtMost(count int, options ...ConditionOption) Condition
func (c Channel[T]) AtLeastAtMost(
	atLeast int,
	atMost int,
	options ...ConditionOption,
) Condition

func (c ChannelMap[T]) ForOne(
	instance string,
	options ...ConditionOption,
) Condition
func (c ChannelMap[T]) ForN(
	instance string,
	count int,
	options ...ConditionOption,
) Condition
func (c ChannelMap[T]) AtLeast(
	instance string,
	count int,
	options ...ConditionOption,
) Condition
func (c ChannelMap[T]) AtMost(
	instance string,
	count int,
	options ...ConditionOption,
) Condition
func (c ChannelMap[T]) AtLeastAtMost(
	instance string,
	atLeast int,
	atMost int,
	options ...ConditionOption,
) Condition
```

Mapping:

| SDK condition | `at_least` | `at_most` |
|---|---:|---:|
| `ForOne()` | omit | omit |
| `ForN(n)` | n | n |
| `AtLeast(n)` | n | omit |
| `AtMost(n)` | omit | n |
| `AtLeastAtMost(lo, hi)` | lo | hi |

Counts must be non-negative and an upper bound cannot be below the lower bound.
`AtMost(0)` is valid and completes without consuming a message.

Channel size and condition results:

```go
func (c Channel[T]) Size(ctx Context) int
func (c ChannelMap[T]) Size(ctx Context, instance string) int

func (c Channel[T]) GetConditionResults(ctx Context) ([]T, error)
func (c ChannelMap[T]) GetConditionResults(
	ctx Context,
	instance string,
) ([]T, error)
```

The intended usage is invocation-specific:

```go
size := OrderCommands.Size(ctx)
orderSize := CommandsByOrder.Size(ctx, "order-1")

commands, err := OrderCommands.GetConditionResults(ctx)
orderCommands, err := CommandsByOrder.GetConditionResults(ctx, "order-1")
```

`Size` starts from `InvokeWorkerRPCRequest.channel_infos` and includes messages
published earlier in the current RPC invocation. Calling it from WaitFor or
Execute is a programming error detected from `ctx`. There is no
`Context.ChannelSize` or raw channel-name API.

`GetConditionResults` is Execute-only and decodes consumed values directly to
`T`. When multiple completed conditions reference the same concrete channel,
their values are concatenated in channel-condition declaration order. The raw
proto-shaped condition result types remain internal.

Conditional close decisions take `Channel[T]` values directly through the
erased `[]ChannelDef` slice. Callers do not construct physical channel
references.

### Waiting conditions

```go
type Wait struct {
	// unexported
}

type Condition interface {
	condition()
}

type ConditionOption interface {
	applyCondition(*conditionValue)
}

func SkipWaitImmediately() Wait
func AllOf(conditions ...Condition) Wait
func AnyOf(conditions ...Condition) Wait
func Combo(conditions ...Condition) ConditionCombination
func AnyComboOf(combinations ...ConditionCombination) Wait
func Timer(
	duration time.Duration,
	options ...ConditionOption,
) Condition
```

Channel condition IDs are optional for `AllOf` and `AnyOf`:

```go
func WithConditionID(conditionID string) ConditionOption
```

`Condition` is an actual wait condition. `ConditionOption` only configures a
timer or channel condition, so an option cannot be passed directly to `AllOf`,
`AnyOf`, or `Combo`.

The SDK assigns internal IDs to unnamed conditions when serializing one `Wait`.
`AnyComboOf` refers to the actual condition values supplied to `Combo`, so the
application does not manually duplicate IDs. Explicit IDs remain useful for
timer skipping.

Channel values are read through the strongly typed channel handles.
`Context.HasTimerFired` and `HasTimerFiredByIndex` expose timer completion
without exporting proto-shaped condition result types.

### Internal transient steps

Transient steps are not part of the application-facing API. Phase 1 exports no
transient movement type, constructor, Wait modifier, or option.

Phase 4 must map the server feature internally. The internal target skips
WaitFor, runs after source WaitFor writes commit, and must return only
`DeadEnd()`.

### Step movements and close decisions

`StepMovement`, `StepDecision`, and `CloseDecision` have unexported fields.
Helpers construct only combinations accepted by the current server:

```go
type StepMovement struct {
	// unexported
}

func GoTo[IN any](
	step Step[IN],
	input IN,
	options ...StepMoveOption,
) StepDecision

func MovementOf[IN any](
	step Step[IN],
	input IN,
	options ...StepMoveOption,
) StepMovement

func GoToMulti(movements ...StepMovement) StepDecision
func GracefulComplete(output any) StepDecision
func ForceComplete(output any) StepDecision
func ForceFail(reason string) StepDecision
func DeadEnd() StepDecision

func ForceCompleteOnChannelsEmpty(
	output any,
	channels []ChannelDef,
	otherwise ...StepMovement,
) StepDecision
```

Rules enforced by construction or Phase 3 validation:

- `GoTo` constructs one movement and `GoToMulti` requires at least one valid
  movement.
- graceful complete, force complete, force fail, and dead end cannot have next
  steps;
- force fail accepts only a string reason;
- dead end has no output;
- conditional force-complete requires unique non-empty channels and at least one
  fallback movement;
- normal movements never expose the server-owned lineage field.

An RPC may return optional next-step movements but cannot close the flow.
Execute must return a valid, non-empty `StepDecision`.

### Step options

Public option types mirror server semantics without exposing proto enum names:

```go
type RetryPolicy struct {
	InitialInterval    time.Duration
	BackoffCoefficient float64
	MaximumInterval    time.Duration
	MaximumAttempts    int32
	TotalDuration      time.Duration
}

type StepDurability uint8

const (
	StepDurabilityDefault StepDurability = iota
	StepDurabilitySync
	StepDurabilityAsync
)

type WaitForFailurePolicy uint8

const (
	FailFlowOnWaitForFailure WaitForFailurePolicy = iota
	ProceedOnWaitForFailure
)

type ExecuteFailure struct {
	// unexported
}

type AttributeLock interface {
	attributeLock()
}

func LockAttribute[T any](attribute Attribute[T]) AttributeLock
func LockAttributeMap[T any](
	attribute AttributeMap[T],
	instance string,
) AttributeLock

func ProceedToOnExecuteFailure[IN any](
	step Step[IN],
	options *StepOptions,
) *ExecuteFailure

type StepOptions struct {
	WaitForTimeout  time.Duration
	ExecuteTimeout  time.Duration
	WaitForRetry    *RetryPolicy
	ExecuteRetry    *RetryPolicy
	WaitForFailure  WaitForFailurePolicy
	ExecuteFailure  *ExecuteFailure
	WaitForDurability  StepDurability
	ExecuteDurability  StepDurability
	WaitForLockAttributes []AttributeLock
	ExecuteLockAttributes []AttributeLock
}
```

The typed constructor prevents callers from placing an erased step inside
`ExecuteFailure`. Phase 3 validates that its target consumes the failed step's
unchanged input, because the server reuses that input. `StepOptions` does not
expose physical attribute keys, server-owned fields, or a generic skip flag.

### RPC

RPC output and its optional next-step movements are explicit:

```go
type RPC[IN, OUT any] func(
	ctx Context,
	input IN,
) (RPCResult[OUT], error)

type RPCResult[OUT any] struct {
	Output    OUT
	NextSteps []StepMovement
}
```

Application code defines a Flow method with that signature:

```go
type BillingFlow struct{}

func (BillingFlow) Refund(
	ctx dex.Context,
	input RefundInput,
) (dex.RPCResult[RefundOutput], error) {
	return dex.RPCResult[RefundOutput]{Output: RefundOutput{}}, nil
}

var Billing = BillingFlow{}
var _ dex.RPC[RefundInput, RefundOutput] = Billing.Refund
```

A Flow exposes RPCs as methods matching this function signature. Phase 3
registration associates the method value with its Flow and uses the Go method
name as the durable RPC name. Package-level functions are not registrable RPCs.

RPC methods use typed attributes/channels and `Context.RecordEvent`. They do not
receive a legacy `Persistence` or `Communication` argument. RPC cannot use
step-execution locals or return a close decision.

### Public client types and façade

Phase 1 fixes signatures but does not implement gRPC.

As in dex-base, every FlowService operation is a method on `Client`. Client
methods are intentionally non-generic; application handler APIs retain their
strong typing, while transport calls use `any` and caller-provided output
pointers.

```go
type Client struct {
	// unexported
}

func (client *Client) StartFlow(
	ctx context.Context,
	flow Flow,
	flowID string,
	input any,
	options StartFlowOptions,
) (runID string, err error)

func (client *Client) PublishToChannel(
	ctx context.Context,
	flowID string,
	channel ChannelDef,
	values ...any,
) error

func (client *Client) PublishToChannelMap(
	ctx context.Context,
	flowID string,
	channel ChannelDef,
	instance string,
	values ...any,
) error

func (client *Client) InvokeRPC(
	ctx context.Context,
	flowID string,
	rpc any,
	input any,
	outputPtr any,
	options InvokeOptions,
) error

func (client *Client) GetAttribute(
	ctx context.Context,
	flowID string,
	attribute AttributeDef,
	valuePtr any,
) (found bool, err error)

func (client *Client) GetAttributeMap(
	ctx context.Context,
	flowID string,
	attribute AttributeDef,
	instance string,
	valuePtr any,
) (found bool, err error)

func (client *Client) SetAttribute(
	ctx context.Context,
	flowID string,
	attribute AttributeDef,
	value any,
) error

func (client *Client) SetAttributeMap(
	ctx context.Context,
	flowID string,
	attribute AttributeDef,
	instance string,
	value any,
) error

func (client *Client) WaitForAttributeEqual(
	ctx context.Context,
	flowID string,
	attribute AttributeDef,
	value any,
	options WaitOptions,
) error

func (client *Client) WaitForAttributeMapEqual(
	ctx context.Context,
	flowID string,
	attribute AttributeDef,
	instance string,
	value any,
	options WaitOptions,
) error
```

`StartFlow` sends `input` to the Flow's starting step. Phase 3 registration
resolves that step and validates its handler signature. `InvokeRPC` accepts an
application RPC value as `any`. `valuePtr` and `outputPtr` must be non-nil
pointers. Attribute and channel methods accept generic definitions through
`AttributeDef` and `ChannelDef`. Map methods take their definition and instance
separately; physical key construction remains internal. Client methods target
the current run for a flow ID; they do not take a `runID` argument.
`StartFlow` still returns the created run ID.

Batch attribute methods are also non-generic:

```go
type AttributeWrite struct {
	Name  string
	Value any
	Index *AttributeIndex
}

func (client *Client) GetAttributes(
	ctx context.Context,
	flowID string,
	attributes ...AttributeDef,
) (map[string]Value, error)

func (client *Client) SetAttributes(
	ctx context.Context,
	flowID string,
	writes ...AttributeWrite,
) error
```

The remaining FlowService operations use non-generic public types:

| Server RPC | Phase 1 façade |
|---|---|
| `StopFlow` | `Client.StopFlow(ctx, flowID, StopOptions)` |
| `WaitForFlow` | `Client.WaitForFlow(ctx, flowID, WaitForFlowOptions)` |
| `SearchFlows` | `Client.SearchFlows(ctx, query, pageSize, nextPageToken)` |
| `ResetFlow` | `Client.ResetFlow(ctx, flowID, ResetOptions)` |
| `SkipTimer` | `Client.SkipTimer(ctx, flowID, StepExecutionID, TimerID)` |
| `UpdateFlowConfig` | `Client.UpdateFlowConfig(ctx, flowID, FlowConfig)` |
| `WaitForStepCompletion` | `Client.WaitForStepCompletion(ctx, flowID, StepExecutionID, WaitOptions)` |
| `TriggerContinueAsNew` | `Client.TriggerContinueAsNew(ctx, flowID)` |
| `HealthCheck` | `Client.HealthCheck(ctx)` |

`WaitForAttributeEqual` compares the encoded server value. Waiting on a
blob-backed stored value may return `FailedPrecondition`; SDK hydration does not
change server-side wait semantics.

Request IDs:

- the SDK generates one UUID per logical `StartFlow`, locking `InvokeRPC`,
  `WaitForStepCompletion`, or `WaitForAttributeEqual` call when the caller
  does not supply one;
- `StartFlowOptions.RequestID` lets applications override the generated ID;
- transparent retries reuse it;
- a non-locking RPC may omit the wire request ID, while locking RPC always sends
  it.

### Client result structs

Dynamic values returned by WaitForFlow and SearchFlows use an opaque SDK value,
never `dexpb.Value`:

```go
type Value struct {
	// unexported
}

func (value Value) Decode(valuePtr any) error

type FlowStatus uint8

const (
	FlowRunning FlowStatus = iota + 1
	FlowCompleted
	FlowFailed
	FlowTimedOut
	FlowTerminated
	FlowCanceled
	FlowContinuedAsNew
)

type FlowErrorType uint8

const (
	FlowErrorStepDecision FlowErrorType = iota + 1
	FlowErrorClientAPI
	FlowErrorWorkerMethod
	FlowErrorInvalidUserCode
	FlowErrorInternal
)

type StepCompletion struct {
	StepType        string
	StepExecutionID string
	Output          Value
}

type WaitForFlowResult struct {
	Status       FlowStatus
	Completions  []StepCompletion
	ErrorType    FlowErrorType
	ErrorMessage string
}

type SearchFlowEntry struct {
	FlowID           string
	RunID            string
	SearchAttributes map[string]Value
}

type SearchFlowsPage struct {
	Flows         []SearchFlowEntry
	NextPageToken string
}

type HealthInfo struct {
	Condition string
	Hostname  string
	Duration  int32
}
```

`StepCompletion.Output` and search attributes decode into a caller-provided
non-nil pointer. The SDK does not guess an application type when the response
does not carry a typed definition.

`LoadBlobs` is not a public client operation. Phase 5 connects it to the
internal Phase 2 hydration seam.

### Client option structs

```go
type WorkerTarget struct {
	Address  string
	Headless bool
}

type ActiveStepSearchMode uint8

const (
	SearchActiveStepsDefault ActiveStepSearchMode = iota
	SearchAllActiveSteps
	SearchActiveStepsWithWaitFor
	DisableActiveStepSearch
)

type FlowConfig struct {
	ActiveStepSearchMode       *ActiveStepSearchMode
	ContinueAsNewThreshold     *int32
	ContinueAsNewPageSizeBytes *int32
	StepDurability             *StepDurability
	WorkerTarget               *WorkerTarget
}

type StartFlowOptions struct {
	Timeout        *time.Duration
	IDReusePolicy  IDReusePolicy
	CronSchedule   string
	StartDelay     *time.Duration
	RetryPolicy    *FlowRetryPolicy
	Attributes     []InitialAttributeDef
	ConfigOverride *FlowConfig
	AlreadyStarted *AlreadyStartedOptions
	RequestID      *string
}

type IDReusePolicy uint8

const (
	IDReuseDefault IDReusePolicy = iota
	IDReuseAllowIfPreviousFailed
	IDReuseAllowIfNotRunning
	IDReuseDisallow
	IDReuseTerminateIfRunning
)

type FlowRetryPolicy struct {
	InitialInterval    time.Duration
	BackoffCoefficient float64
	MaximumInterval    time.Duration
	MaximumAttempts    int32
}

type AlreadyStartedOptions struct {
	IgnoreError bool
}

type InvokeOptions struct {
	Timeout        time.Duration
	LockAttributes []AttributeLock
}

type WaitOptions struct {
	Timeout time.Duration
}

type WaitForFlowOptions struct {
	NeedsResults bool
	Timeout      time.Duration
}

type StopType uint8

const (
	CancelFlow StopType = iota + 1
	TerminateFlow
	FailFlow
)

type StopOptions struct {
	Type   StopType
	Reason string
}

type StepExecutionID struct {
	StepType        string
	ExecutionNumber *int32
}

type TimerID struct {
	ConditionID string
	Index       *int32
}

type ResetType uint8

const (
	ResetByHistoryEventID ResetType = iota + 1
	ResetToBeginning
	ResetByHistoryEventTime
	ResetByStepType
	ResetByStepExecutionID
)

type ResetOptions struct {
	Type                       ResetType
	HistoryEventID             int32
	Reason                     string
	HistoryEventTime           time.Time
	StepType                   string
	StepExecutionID            string
	SkipChannelMessagesReapply bool
	SkipLockingRPCReapply      bool
}
```

Pointer fields in `FlowConfig` preserve proto presence for partial overrides.
`WorkerTarget` is configured through `FlowConfig`, not as a separate StartFlow
argument.

`StartFlowOptions.Timeout == nil` omits the Flow timeout.
`StartFlowOptions.StartDelay == nil` omits the start delay. Starting-step
options come from the step wrapped by `DefineStepAsStart`; StartFlow has no
separate step-options override.

`WaitOptions.Timeout == 0` retains the server's immediate-check semantics for
WaitForAttribute and WaitForStepCompletion. `WaitForFlowOptions` is separate
because its zero duration means the server-configured maximum long poll.

`InitialAttributeDef` is sealed and constructed with typed helpers so initial
values carry the definition's index configuration:

```go
type InitialAttributeDef interface {
	initialAttribute()
}

func InitialAttribute[T any](
	attribute Attribute[T],
	value T,
) (InitialAttributeDef, error)

func InitialAttributeMapValue[T any](
	attribute AttributeMap[T],
	instance string,
	value T,
) (InitialAttributeDef, error)
```

No old reset, memo, loading-policy, or worker-URL fields are retained.

### Public errors

All FlowService failures are returned as an SDK error that preserves both gRPC
status and Dex details:

```go
type Error struct {
	Code                codes.Code
	SubStatus           ErrorSubStatus
	Detail              string
	OriginalWorkerError *WorkerError
}

type WorkerError struct {
	Code   codes.Code
	Type   string
	Detail string
}

type ErrorSubStatus uint8

const (
	ErrorUncategorized ErrorSubStatus = iota + 1
	ErrorFlowAlreadyStarted
	ErrorFlowNotFound
	ErrorWorkerAPI
	ErrorLongPollTimeout
)
```

`Error` implements `error` and supports `errors.As`. Application-worker
failures remain distinguishable from backend `Unavailable`; long-poll timeout
retains `DeadlineExceeded` plus `LongPollTimeout`.

### End-to-end authoring example

```go
var (
	OrderStatus = dex.DefineAttribute[string](
		"order-status",
		dex.Indexed(dex.AttributeIndex{Type: dex.IndexKeyword}),
	)
	Commands = dex.DefineChannel[Command]("commands")
)

type WaitForCommandStep struct {
	dex.DefaultStepOptions
}

func (WaitForCommandStep) GetStepType() string {
	return "wait-for-command"
}

func (WaitForCommandStep) WaitFor(
	ctx dex.Context,
	input OrderInput,
) (dex.Wait, error) {
	if err := OrderStatus.Set(ctx, "waiting"); err != nil {
		return dex.Wait{}, err
	}

	if err := ctx.SetStepExecutionLocal(
		"snapshot",
		OrderSnapshot{OrderID: input.OrderID},
	); err != nil {
		return dex.Wait{}, err
	}
	if err := ctx.RecordEvent("waiting-for-command", input); err != nil {
		return dex.Wait{}, err
	}

	return dex.AnyOf(
		Commands.ForOne(dex.WithConditionID("command")),
		dex.Timer(
			30*time.Minute,
			dex.WithConditionID("timeout"),
		),
	), nil
}

func (WaitForCommandStep) Execute(
	ctx dex.Context,
	input OrderInput,
) (dex.StepDecision, error) {
	if ctx.HasTimerFired() {
		return dex.ForceFail("command timed out"), nil
	}

	commands, err := Commands.GetConditionResults(ctx)
	if err != nil {
		return dex.StepDecision{}, err
	}
	_ = commands

	var snapshot OrderSnapshot
	found, err := ctx.GetStepExecutionLocal("snapshot", &snapshot)
	if err != nil {
		return dex.StepDecision{}, err
	}
	if !found {
		return dex.StepDecision{}, fmt.Errorf("snapshot is missing")
	}
	return dex.GracefulComplete(snapshot), nil
}

var WaitForCommand = WaitForCommandStep{}
var _ dex.Step[OrderInput] = WaitForCommand

type OrderFlow struct{}

func (OrderFlow) GetFlowType() string {
	return "order"
}

func (OrderFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStepAsStart(WaitForCommand),
	}
}

func (OrderFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{OrderStatus},
		Channels:   []dex.ChannelDef{Commands},
	}
}

var Orders = OrderFlow{}
var _ dex.Flow = Orders
```

The exact zero values returned on an error are not part of normal application
style; they only satisfy Go's return rules.

## Phase 1 deliverables

1. Approve this public API shape.
2. Add the public declarations with unexported runtime fields.
3. Add compile-time example coverage for generic interfaces and signatures.
4. Keep registration-only generic handler adapters unexported.
5. Keep raw condition results and transient-step machinery unexported.
6. Delete old public API files only when their replacements compile in the same
   change; do not add compatibility aliases.
7. Stop at the Phase 1 exit gate. Do not add registry or WorkerService code.

## Tests

Phase 2 cannot use server integration tests because it intentionally has no
registration, worker, or transport. External contract tests cover the public
surface; package-internal tests cover codec and mapper branches.

Add SDK external-package tests (`package dex_test`) for these scenarios:

1. A package can declare a flow, heterogeneous typed steps, attributes,
   channels, and RPCs without importing `dexpb`.
2. Waiting and Execute-only handlers satisfy the same `Step[IN]`; the latter
   embeds `NoWaitFor[IN]`.
3. `DefineStep`, `DefineStepAsStart`, `GoTo`, `MovementOf`, and `GoToMulti`
   preserve target step input types.
4. A Flow method matching `RPC[IN, OUT]` preserves typed worker handlers, while
   `Client.InvokeRPC` accepts `any` and a caller-provided output pointer.
5. Static and map attributes expose typed Get/Set/Delete methods that take
   `Context`, while physical keys remain internal behind sealed lock values.
6. Static and map channels expose Publish, all five count forms, and the
   `myCh.Size(ctx)` / `myChMap.Size(ctx, key)` RPC shape.
7. A Wait combines timers and channel conditions through All, Any, and
   AnyCombo.
8. Static and map channel result methods return `[]T` without exposing raw
   condition-result types.
9. The `Context.HasTimerFired` and `HasTimerFiredByIndex` signatures compile.
10. Execute can return `GoTo`, `GoToMulti`, all close decisions, and conditional
    force-complete fallback using channel definitions directly.
11. Step-execution local Set accepts `any`, Get accepts a caller-provided
    pointer, and `RecordEvent(name, any)` compiles.
12. `Context.WaitForMethodFailed` compiles for Execute implementations.
13. Non-generic client methods use server identifiers directly and return only
    run ID from starts.

Package-internal tests cover native and JSON values, indexed attributes,
duration rounding, waits, decisions, errors, hydration validation, and cache
payload round trips.

Run Phase 2 verification through the Makefile:

```bash
make -C sdk-go unitTests 2>&1 | tee /tmp/test-go-sdk-phase2.log
make -C sdk-go blobCacheTests 2>&1 | tee /tmp/test-go-sdk-phase2-blobcache.log
make copyright-check 2>&1 | tee /tmp/test-go-sdk-phase2-copyright.log
```

Later phases must add Temporal integration coverage for:

1. static and map channel results decode to their declared Go types;
2. multiple completed conditions on one concrete channel concatenate values in
   declaration order;
3. natural timer completion and `SkipTimer` both make `HasTimerFired` true;
4. `HasTimerFiredByIndex` identifies the completed timer in `AnyComboOf`;
5. untyped step-execution locals round-trip from WaitFor to Execute through a
   caller-provided pointer;
6. arbitrary event values are encoded and recorded once per invocation name;
7. Flow method RPC registration, handler invocation, and optional next steps
   map to the current server contract;
8. internal transient movement mapping remains unavailable to application code.

Cadence execution is not required because the default Dex server image is
Temporal-backed.

## Documentation

- Keep this plan linked from [`docs/README.md`](../../README.md).
- Keep [`sdk-go/README.md`](../../../sdk-go/README.md) aligned with the
  authoring and value-codec contracts. Cover the single `Step[IN]`
  interface, embedded `NoWaitFor[IN]`, Flow method RPCs, close decisions, typed
  attributes/channels, untyped step-execution locals and events, strongly typed
  channel results, timer-fired helpers, `WaitForMethodFailed`, and RPC-only
  channel size.
- Update [`sdk-go/CONTRIBUTION.md`](../../../sdk-go/CONTRIBUTION.md) with the
  Phase 2 verification commands and the rule that application packages do not
  import `dexpb`.
- Blob-cache documentation belongs with its independent component. The Go SDK
  documentation will later describe only how that component is configured or
  injected.

## UI/UX

N/A: no in-repo web UI.
