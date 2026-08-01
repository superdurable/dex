# Dex Go SDK

The Go SDK is being rewritten around the current Dex `Flow`, `Step`,
`Attribute`, `Channel`, `WaitFor`, and `Execute` contracts.

Phases 1 through 4 provide the public model, value/protobuf mapping, private
registration, and the application-hosted WorkerService. Public FlowService
client transport remains Phase 5.

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
	return dex.GracefulComplete(commands), nil
}

var WaitForCommand = WaitForCommandStep{}

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
```

Flows use `dex.DefineStepAsStart` for at most one starting step and
`dex.DefineStep` for every non-starting step.

Execute-only steps embed `dex.StepDefaults[IN]`. Step transitions use
`dex.GoTo`, or `dex.MovementOf` with `dex.GoToMulti`.

Flow RPCs are methods matching `dex.RPC[IN, OUT]`. Attributes and channels stay
strongly typed inside handlers. Step-execution locals and recorded events accept
arbitrary values through `dex.Context`.

## Registration

Registration is assembled internally from each Flow's durable type, steps,
persistence schema, and exported RPC methods. It rejects empty or duplicate
names, multiple starting steps, invalid indexes, undeclared locks, and
incompatible execute-failure targets before WorkerService starts.

`DefineStep` and `DefineStepAsStart` retain the step input type behind a private
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

Applications host registered flows without importing generated protobufs:

```go
worker, err := dex.NewWorker([]dex.Flow{Orders}, dex.WorkerOptions{
	BindAddress: ":8803",
})
if err != nil {
	return err
}

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

Blob-backed request values hydrate through the private FlowService connection.
`FlowServiceAddress` defaults to `localhost:8801`. An optional `BlobCache`
avoids repeat loads; the caller still owns and closes the cache.

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

The SDK generates a UUID for every start and synchronous-update call when the
caller does not supply one. `StartFlowOptions.RequestID` may override the
generated start ID. Retries reuse that UUID.

Large string and object values may be returned as blob references. Worker
inputs hydrate before handler decode. Phase 5 public client results will hydrate
before a `Value` is constructed. Decode never performs network I/O.

Compilable examples:

- [Order flow](examples/order/main.go)
- [Every Client API](examples/order/client.go)
- [Step transitions](examples/transitions/main.go)
- [Flow method RPC](examples/rpc/main.go)

## Phase 4 verification

```text
make unitTests
make workerIntegTests
make blobCacheTests
make copyright-check
```

The detailed design and later phase boundaries are in the
[Go SDK rewrite plan](../docs/design/plan/go-sdk-rewrite.md).
