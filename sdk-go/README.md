# Dex Go SDK

The Go SDK is being rewritten around the current Dex `Flow`, `Step`,
`Attribute`, `Channel`, `Stream`, `WaitFor`, and `Execute` contracts.

The rewrite provides the public model, value/protobuf mapping, immutable
registration, application-hosted WorkerService, and typed FlowService Client.

## License

[Super Durable Source License 1.0](LICENSE), with legacy portions under their
original terms as described in [LEGACY_NOTICES.md](LEGACY_NOTICES.md).

## Authoring a flow

Application packages import `dex`, never `gen/dexpb`.

```go
var (
	OrderStatus = dex.DefineAttribute[string](
		"order-status",
		dex.Indexed(dex.AttributeIndex{Type: dex.IndexKeyword}),
	)
	Commands = dex.DefineChannel[Command]("commands")
	Progress = dex.DefineStream[string]("progress", 10<<20)
)

type WaitForCommandStep struct {
	dex.StepDefaults
}

func (WaitForCommandStep) WaitFor(
	ctx dex.Context,
	input OrderInput,
) (*dex.Wait, error) {
	var checkpoint string
	found, err := ctx.GetLastHeartbeatValue(&checkpoint)
	if err != nil {
		return nil, err
	}
	if !found {
		checkpoint = "starting"
	}
	if err := ctx.RecordHeartbeat(checkpoint); err != nil {
		return nil, err
	}
	if err := Progress.Write(ctx, "waiting for a command"); err != nil {
		return nil, err
	}
	if err := Progress.Write(ctx, "inventory check started"); err != nil {
		return nil, err
	}
	if err := OrderStatus.Set(ctx, "waiting"); err != nil {
		return nil, err
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
) (*dex.StepDecision, error) {
	if ctx.HasTimerFired() {
		return dex.ForceFail("command timed out"), nil
	}
	commands, err := Commands.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	return dex.GracefulComplete(commands), nil
}

var WaitForCommand = WaitForCommandStep{}

type OrderFlow struct {
	dex.FlowDefaults
}

func (OrderFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(WaitForCommand),
	}
}

func (OrderFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{OrderStatus},
		Channels:   []dex.ChannelDef{Commands},
		Streams:    []dex.StreamDef{Progress},
	}
}
```

Flows use `dex.DefineStartStep` for at most one starting step and
`dex.DefineStep` for every non-starting step.

Execute-only steps embed `dex.StepDefaultsNoWaitFor[IN]`. Step transitions use
`dex.GoTo`, or `dex.MovementOf` with `dex.GoToMulti`.

`StepOptions.WaitForMethodTimeout` and `ExecuteMethodTimeout` bound the two
handler calls. Timer and channel conditions determine how long a Step waits.
Use `dex.Until(condition)` for one condition, and `dex.AllOf` or `dex.AnyOf`
for multiple conditions.

`WaitForRetry` and `ExecuteRetry` limit one logical handler execution. With
`StepDurabilityAsync`, local and fallback regular activities share maximum
attempts, total duration, and 1-based attempt numbers. Fallback starts
immediately; later regular retries continue the backoff sequence at the
cumulative attempt.

Step durability resolves from the method override, then FlowConfig, then
`StepDurabilitySync`. Retry total duration defaults to four hours. Regular
attempts default to two hours with a one-minute heartbeat timeout. The server's
minimum explicit heartbeat timeout defaults to ten seconds and can be changed
by operators; the Go SDK validates only non-negative whole seconds in the
signed int32 range. Async execution first uses at most seven local-activity
seconds and three attempts. The local phase ignores method and heartbeat
timeouts before regular fallback.

### Heartbeats and Stream progress

`Context.RecordHeartbeat` emits a checkpoint from WaitFor or Execute. A retry
restores the most recent regular-activity checkpoint through
`Context.GetLastHeartbeatValue`. Passing nil, including a typed nil, explicitly
clears the checkpoint. Local-activity heartbeats are ignored. Flow timeout
handlers and RPCs cannot send heartbeat or Stream progress.

`Stream.Write` emits a fire-and-forget frame on the same Worker response stream.
A handler may write any number of messages to the same or different Streams.
The call reports local validation, encoding, and gRPC send failures, but it does
not wait for a Stream Store acknowledgment. Server-side store rejection or
unavailability is visible through server logs and metrics rather than the
handler return value. Heartbeats and Stream writes may run concurrently; their
frames are serialized by the Worker.

Use `NewBufferedTextStream` for token-sized text deltas. It preserves text
exactly, flushes after one second or a soft 16 KiB UTF-8 threshold, and emits
the tail before the handler result or error:

```go
progress, err := dex.NewBufferedTextStream(ctx, Thinking)
if err != nil {
	return nil, err
}
if err := progress.Write(delta); err != nil {
	return nil, err
}
```

`BufferedTextStreamFlushInterval` and `BufferedTextStreamMaxBytes` override the
defaults. Empty buffers emit no message or heartbeat. Retry does not restore an
unsent buffer or deduplicate batches already sent.

Messages written by a Step have `#<stepExecutionID>` in
`StreamMessage.Source`. Client writes accept any non-empty source, including
values containing `#`. Reusing a source appends another message; source is
metadata, not an idempotency key.

### Canceling Step executions

A successful Step can cancel queued or active executions while continuing with
its normal decision:

```go
return dex.GoTo(RecordQuote, quote).
	CancelSiblingSteps(CarrierA, CarrierB).
	CancelSteps(GlobalQuoteTimeout), nil
```

`CancelSteps` selects every current execution of each registered Step type.
`CancelSiblingSteps` selects only executions whose
`Context.FromStepExecutionID()` matches the current execution. Repeated calls
form a union, and a Flow-wide selector wins for the same Step type. The Worker
rejects nil, mismatched, or unregistered selectors as invalid Step results.

Dex resolves one snapshot after the current execution succeeds. Completed,
already-canceled, and absent targets are no-ops. Next Steps created by the same
decision are not in that snapshot. Dex immediately applies the next or close
action without waiting for target handlers; late decisions, writes, retries,
and recovery Steps are discarded.

An RPC may call `RPCResult.CancelSteps` for Flow-wide selection while also
returning output and scheduling Next Steps. RPCs do not support sibling
selection because an RPC invocation has no Step execution lineage.

### Soft Flow timeout

`StartFlowOptions.Timeout` starts a durable Dex timer rather than a backend
execution timeout. A Flow implementing `FlowTimeoutHandler` defaults to
`TimeoutHandler`; other Flows default to `TimeoutFail`. Override either default
with `TimeoutFail` or `TimeoutCancel`:

```go
func (OrderFlow) HandleTimeout(ctx dex.Context) (*dex.StepDecision, error) {
	return dex.ForceComplete("expired"), nil
}

timeout := 30 * time.Minute
options := dex.StartFlowOptions{
	Timeout:       &timeout,
	TimeoutPolicy: dex.TimeoutHandler,
}
```

The hook is Execute-only, receives no input, and runs at most once after its
durable timer fires or is skipped. It may use `Context` normally and return any
`StepDecision`. Continue-as-new preserves its deadline; retry runs get
a fresh timeout budget. Zero or nil timeout disables the feature.

Use `dex.None` when a Step, RPC, or Channel has no application payload, and pass
`nil` at every call site. It rejects accidental values unlike `any` and makes
the absence of a payload explicit.

Flow RPCs are methods matching `dex.RPC[IN, OUT]`. Attributes and channels stay
strongly typed inside handlers. Step-execution locals and recorded events accept
arbitrary values through `dex.Context`.

Invocation attribute reads return `(value, error)`. A missing static or map
attribute returns `*dex.AttributeNotFoundError`, which callers can inspect with
`errors.As` when absence is expected.

## Waiting and map inspection

`AllOf` and `AnyOf` may contain unnamed Conditions; the Worker sends an empty
Condition ID and Dex evaluates them normally. Every Condition in `AnyComboOf`
must use `WithConditionID`. Reusing the same Condition value across combinations
is supported, while duplicate IDs on distinct Conditions are rejected.

Clients wait on singleton Attribute equality in the current run with
`WaitForAttributeEqual` or `WaitForAttributeMapInstanceEqual`. Client-side map
reads and writes use `GetAttributeMapInstance` and `SetAttributeMapInstance`.
Expected values must encode as string, bool, integer, or double; JSON, bytes,
and null fail before the RPC is sent.

Inside a handler, `AttributeMap.MapSize` and `AllInstanceKeys` include buffered
sets and deletes. `ChannelMap.MapSize` and `AllInstanceKeys` are RPC-only and
include buffered publishes, but omit empty instances. Keys are decoded and
sorted. Use `ForceCompleteIfChannelsEmpty` for atomic conditional completion.

SubFlows are normal, independently addressable Flows used as durable Conditions:

```go
func (ParentStep) WaitFor(_ dex.Context, input ChargeInput) (*dex.Wait, error) {
	return dex.Until(dex.SubFlow(ChargeFlow{}, input)), nil
}

func (ParentStep) Execute(ctx dex.Context, _ ChargeInput) (*dex.StepDecision, error) {
	result, err := dex.SubFlowResult(ctx)
	if err != nil {
		return nil, err
	}
	var receipt Receipt
	if err := result.DecodeSingleOutput(&receipt); err != nil {
		return nil, err
	}
	return dex.GracefulComplete(receipt), nil
}
```

`SubFlowID(ctx, index...)` returns the generated identity, including for a running
`AnyOf` loser. `SubFlowOptions` configures timing, timeout policy, retry, initial
target Attributes, Flow config, Condition ID, and reuse. Parent completion does not cancel an
unfinished SubFlow.

## Registration

Registration uses pointer-stripped package-qualified Go types by default:
`*orders.OrderFlow` becomes `orders.OrderFlow`. Embed `FlowDefaults` in a
Flow. Waiting steps embed `StepDefaults`; execute-only steps embed
`StepDefaultsNoWaitFor[IN]`. Both include the default step type and options.
Override `GetFlowType` or `GetStepType` only when an explicit durable identity
is required.

Registration is assembled once from each Flow's final durable type, steps,
persistence schema, and exported RPC methods. It rejects empty or duplicate
names, multiple starting steps, invalid indexes, undeclared locks, and
incompatible execute-failure targets before WorkerService starts.

`Worker.Start` aggregates every registered Indexed Attribute and synchronizes
the physical index names with Dex Server before opening its listener. Existing
indexes return immediately; a synchronization failure or the default two-minute
deadline fails startup. Indexed AttributeMaps must declare a fixed index key.

## Attribute Store synchronization

Opt an Attribute or AttributeMap into the Flow's external latest-state
projection, then select Server-configured Attribute Stores on the Flow:

```go
var CustomerEmail = dex.DefineAttribute[string](
	"customer-email",
	dex.SyncToAttributeStore(),
)

config := &dex.FlowConfig{
	AttributeStoreNames: []string{"profiles", "audit"},
}
```

Store updates are asynchronous. Every enabled Attribute write is sent to every
selected Store. Deleting an opted-in Attribute writes SQL `NULL`, and a Store
failure does not roll back the Flow Attribute. A `nil` `AttributeStoreNames`
preserves current targets; an empty non-nil slice disables future synchronization
while preserving protocol presence.

`DefineStep` and `DefineStartStep` retain the step input type behind a private
`typedStepDef` that implements the sealed `StepDef` interface. Runtime
movements resolve through the current Flow's registered step definitions, so a
same-name Step value cannot replace the registered handler or defaults.

RPCs require no communication schema. Every exported Flow method other than the
`Flow` interface methods must use this exact shape and is registered under its
Go method name:

```go
func (
	ctx dex.Context,
	input IN,
) (*dex.RPCResult[OUT], error)
```

Exported methods with any other signature fail registration. Unexported methods
are ignored. Register a pointer Flow value when methods use pointer receivers; a
value-typed Flow that only exposes those methods on `*T` fails registration
instead of silently omitting them. Client calls must pass the direct bound
method value, such as `Orders.Update`; package functions, method expressions,
closures, and wrappers are rejected. Return `nil, err` on failure; returning
`nil, nil` is an invalid Worker result.

Registered Flow and Step values are retained and may be invoked concurrently.
They must be immutable or concurrency-safe and must keep
invocation state in `dex.Context`.

## Running a Worker

Applications share one Registry and BlobCache between Worker and Client:

The cache comes from
[`github.com/superdurable/dex/blob-cache-go/blobcache`](https://pkg.go.dev/github.com/superdurable/dex/blob-cache-go/blobcache).

```go
logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
registry, err := dex.NewRegistry([]dex.Flow{Orders})
if err != nil {
	return err
}
cache, err := blobcache.New(&blobcache.Config{
	Dir:      "/var/tmp/order-dex-blobs",
	MaxBytes: 1 << 30,
	Logger:   logger,
})
if err != nil {
	return err
}

worker, err := dex.NewWorker(registry, cache, dex.WorkerOptions{
	BindAddress: ":8803",
	Logger:      logger,
})
if err != nil {
	return err
}

client, err := dex.NewClient(registry, cache, dex.ClientOptions{
	WorkerTarget: worker.WorkerTarget(),
	Logger:       logger,
})
if err != nil {
	return err
}
defer client.Close()

if err := worker.Start(); err != nil {
	return err
}
```

`BindAddress` is the local plaintext listener. When
`WorkerOptions.WorkerTarget.Address` is empty, the advertised target derives
from it; wildcard hosts become `localhost`. Set an explicit target when Dex
reaches the Worker through different DNS, a load balancer, or a headless
address. `WorkerTarget()` returns the resolved value for
`FlowConfig.WorkerTarget`.

`Start` blocks and is one-shot. `Stop(ctx)` is idempotent, drains in-flight
calls, and force-stops them if the context expires. The SDK does not install
signal handlers; the [Order example](examples/order/main.go) shows signal-driven
shutdown.

Each invocation receives independent buffered attributes, channel messages,
locals, and events. Successful handlers commit the whole response. Returned
errors, mapping failures, and recovered panics commit nothing. Flow and Step
values may be called concurrently and must be concurrency-safe.

Blob-backed values hydrate through private FlowService calls.
`FlowServiceAddress` defaults to `localhost:8801`. Registry and BlobCache are
required; the caller closes the cache after Client and Worker stop using it.

`dex.Logger` supports structured debug, info, warning, and error messages.
`blobcache.Config.Logger` defaults to `slog.Default` and is inherited by Client
and Worker. Their option-level loggers override it for that component.

## Calling FlowService

Client methods target the current run by sending an empty run ID. This follows
Continue-as-New automatically. `StartFlow` and `TimeTravel` return run IDs for
diagnostics, while normal calls need only the flow ID.

`ClientOptions.WorkerTarget` is the default advertised target for StartFlow.
A per-call `ConfigOverride.WorkerTarget` takes precedence. UpdateFlowConfig does
not inherit the Client default.

StartFlow uses the starting step retained by Registry. Its input must match
that step's input type, and its step options come from the registered Step.
Flows without a starting step require nil input.

The SDK generates request IDs for StartFlow, SetAttributes, InvokeRPC, and both
wait-update APIs. Only `StartFlowOptions.RequestID` is public because it may be
a stable business identifier spanning separate calls.

Client is safe for concurrent calls. `Close` is idempotent and closes only its
owned gRPC connection. Calls after Close return a local error.

`WaitForFlow` hydrates every requested completion before returning. Decode one
known output with `result.DecodeSingleOutput(&output)`; it returns a local error
unless exactly one completion exists. For multiple outputs, match
`result.Completions` by `StepType` or `StepExecutionID`, then call
`completion.Output.Decode(&target)`. The slice preserves server collection
order, but parallel branch order is not deterministic. No-output Flows return
an empty slice.

Remote FlowService failures use concrete Go error types. Match expected
conditions with `errors.As` instead of comparing gRPC codes or sub-statuses:

```go
var inactive *dex.FlowNotActiveError
if errors.As(err, &inactive) {
	// The Flow exists historically but cannot accept this mutation or RPC.
}
```

`FlowNotFoundError` is returned by reads requiring an existing Flow, including
GetAttribute, GetAttributes, WaitForFlow, and TimeTravel. Mutations, RPCs,
publishes, timer operations, and step or attribute waits return
`FlowNotActiveError` when no active Flow is available.

Duplicate starts return `FlowAlreadyStartedError`. Server long polls return
`LongPollTimeoutError`, including the operation and Flow ID. Worker handler
failures return `WorkerInvocationError`; its `Worker` field preserves the
original Worker code, type, and detail. Concurrent locking RPCs return
`RPCLockConflictError` separately.

`WaitForFlow` returns `FlowResult` for every terminal status. Inspect `Status`,
`ErrorType`, `ErrorMessage`, and any requested `Completions`; transport and
long-poll failures remain errors.

Every specific service error unwraps to `*dex.ServiceError` and then to the
original gRPC error. `ServiceError` exposes `Op`, `FlowID`, `Code`, `SubStatus`,
and `Detail`; sub-status is diagnostic metadata, not application control flow.
Unknown or malformed server details fall back to `ServiceError`.

Registry failures use `FlowDefinitionError`, invalid Worker handler results use
`InvalidStepResultError`, and codec failures use `ValueMappingError`. Ordinary
argument and context errors remain ordinary Go errors.

## Value encoding

Strings, booleans, signed integers, representable unsigned integers, and
floating-point values use native Dex value arms. Strings must contain valid
UTF-8; use `[]byte` for arbitrary binary data. Byte slices use an object arm
with encoding `"rawbytes"` and store the bytes directly without base64.
Structs, maps, other slices, arrays, and JSON-compatible values use an object
arm with encoding `"json"`.

Returned dynamic values remain opaque until decoded:

```go
var result OrderResult
if err := value.Decode(&result); err != nil {
	return err
}
```

Decode requires a non-nil pointer. Invalid UTF-8 strings, integer overflow,
incompatible targets, unknown encodings, unhydrated blob references, and
malformed JSON return errors.

Ordinary nil encodes as JSON null. The Dex null arm is used only when deleting
an attribute.

### Indexed attributes

Keyword and text indexes accept valid UTF-8 strings. Keyword-array indexes
accept slices containing valid UTF-8 strings. Int, double, and bool indexes
accept their matching Go primitive types. Datetime indexes accept `time.Time`
or RFC3339Nano strings, including UTC `Z` and numeric offsets. Fractional
seconds are preserved. Numeric strings are not treated as Unix nanoseconds.
Initial indexed values are validated by `dex.InitialAttribute` and
`dex.InitialAttributeMapValue`.

The SDK generates a UUID for every request-ID-bearing call.
`StartFlowOptions.RequestID` may override the generated start ID. Retries reuse
the selected UUID.

Large string and object values may be returned as blob references. Worker
inputs and Client results hydrate before handler or application decode. Decode
never performs network I/O.

Compilable examples:

- [Order flow](examples/order/main.go)
- [Every Client API](examples/order/client.go)
- [Step transitions](examples/transitions/main.go)
- [Flow method RPC](examples/rpc/main.go)

## Verification

```text
make unitTests
make clientIntegTests
make workerIntegTests
make e2eTests
make docsCheck
make copyright-check
```

`docsCheck` parses the hand-written `dex`, `logging`, and shared `blobcache`
packages and requires a Go doc comment on every exported type, function,
method, field, interface method, constant, and variable. Comments must begin
with the declared name. Generated protobufs and tests are excluded. Use
`go doc dex.<Name>` (or an IDE hover) to inspect the same application-facing
API documentation locally.

`e2eTests` uses the current checkout's `dexcli dev` environment. It
runs the migrated iWF Go SDK scenarios through the public Dex SDK.

### Measure integration coverage

Run the local Client and Worker integration suites plus all `dexcli dev`
E2E scenarios with Go coverage:

```shell
make integrationCoverage
```

The report measures only production packages under `./dex/...`. Unit
tests, standalone BlobCache module tests, examples, generated protobuf stubs under
`gen/`, and `*_test.go` files are excluded. Open `coverage/index.html` for
annotated source, or inspect `coverage/coverage.txt` for per-function
totals. `coverage/coverage.out` is the profile uploaded by CI.

CI uploads the profile to Codecov with GitHub OIDC, so no upload secret is
stored in this repository. The report uses the `sdk-go-integration` flag
and contributes to the Go SDK component defined in the root `codecov.yml`.
The Actions run also publishes the report directory as
`sdk-go-integration-coverage`.

The detailed design and later phase boundaries are in the
[Go SDK rewrite plan](../docs/design/plan/go-sdk-rewrite.md).
