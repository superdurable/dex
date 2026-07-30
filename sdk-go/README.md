# Dex Go SDK

The Go SDK is being rewritten around the current Dex `Flow`, `Step`,
`Attribute`, `Channel`, `WaitFor`, and `Execute` contracts.

Phase 1 contains the application-facing interfaces and value types. Phase 2
adds value encoding and internal protobuf mapping. Registration, WorkerService,
and the FlowService transport are not implemented yet.

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

## Value encoding

Strings, booleans, signed integers, representable unsigned integers, and
floating-point values use native Dex value arms. Structs, maps, slices, arrays,
`[]byte`, and other JSON-compatible values use an object arm with encoding
`"json"`.

Returned dynamic values remain opaque until decoded:

```go
var result OrderResult
if err := value.Decode(&result); err != nil {
	return err
}
```

Decode requires a non-nil pointer. Integer overflow, incompatible targets,
unknown encodings, unhydrated blob references, and malformed JSON return
errors.

Ordinary nil encodes as JSON null. The Dex null arm is used only when deleting
an attribute.

### Indexed attributes

Keyword and text indexes accept strings. Keyword-array indexes accept string
slices. Int, double, and bool indexes accept their matching Go scalar families.
Datetime indexes accept `time.Time` or absolute server-compatible datetime
strings. Initial indexed values are validated by `dex.Initial` and
`dex.InitialMapValue`.

Large string and object values may be returned as blob references. Hydration is
internal and always occurs before a public `Value` is constructed; Decode never
performs network I/O.

Compilable examples:

- [Order flow](examples/order/main.go)
- [Every Client API](examples/order/client.go)
- [Step transitions](examples/transitions/main.go)
- [Flow method RPC](examples/rpc/main.go)

## Phase 2 verification

```text
make unitTests
make blobCacheTests
make copyright-check
```

The detailed design and later phase boundaries are in the
[Go SDK rewrite plan](../docs/design/plan/go-sdk-rewrite.md).
