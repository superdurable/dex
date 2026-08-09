# Dex Go SDK

The Go SDK is being rewritten around the current Dex `Flow`, `Step`,
`Attribute`, `Channel`, `WaitFor`, and `Execute` contracts.

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
)

type WaitForCommandStep struct {
	dex.StepDefaults
}

func (WaitForCommandStep) WaitFor(
	ctx dex.Context,
	input OrderInput,
) (*dex.Wait, error) {
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

Use `dex.None` when a Step, RPC, or Channel has no application payload, and pass
`nil` at every call site. It rejects accidental values unlike `any` and makes
the absence of a payload explicit.

Flow RPCs are methods matching `dex.RPC[IN, OUT]`. Attributes and channels stay
strongly typed inside handlers. Step-execution locals and recorded events accept
arbitrary values through `dex.Context`.

Invocation attribute reads return `(value, error)`. A missing static or map
attribute returns `*dex.AttributeNotFoundError`, which callers can inspect with
`errors.As` when absence is expected.

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
) (dex.RPCResult[OUT], error)
```

Exported methods with any other signature fail registration. Unexported methods
are ignored. Register a pointer Flow value when methods use pointer receivers; a
value-typed Flow that only exposes those methods on `*T` fails registration
instead of silently omitting them. Client calls must pass the direct bound
method value, such as `Orders.Update`; package functions, method expressions,
closures, and wrappers are rejected.

Registered Flow and Step values are retained and may be invoked concurrently.
They must be immutable or concurrency-safe and must keep
invocation state in `dex.Context`.

## Running a Worker

Applications share one Registry and BlobCache between Worker and Client:

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
Continue-as-New automatically. `StartFlow` and `ResetFlow` return run IDs for
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
owned gRPC connection. Calls after Close return a local error. Remote
FlowService failures become `*dex.Error`; local validation and codec failures
remain ordinary Go errors.

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
accept their matching Go scalar families. Datetime indexes accept `time.Time`
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
make blobCacheTests
make e2eTests
make copyright-check
```

`e2eTests` uses the current checkout's `dexcli dev` environment. It
runs the migrated iWF Go SDK scenarios through the public Dex SDK.

### Measure integration coverage

Run the local Client and Worker integration suites plus all `dexcli dev`
E2E scenarios with Go coverage:

```shell
make integrationCoverage
```

The report measures only production packages under `./dex/...`. Unit
tests, BlobCache package tests, examples, generated protobuf stubs under
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
