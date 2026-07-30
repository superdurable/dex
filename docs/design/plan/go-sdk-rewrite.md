# Go SDK rewrite plan

Status: Phase 1 public contracts implemented. Later phases are boundaries only
and require their own design review before implementation.

## Current source of truth

This plan is based on the current:

- [`protos/dex.proto`](../../../protos/dex.proto)
- [`protos/README.md`](../../../protos/README.md)
- [`server/service/interpreter/activityImpl.go`](../../../server/service/interpreter/activityImpl.go)
- [`server/service/interpreter/workflowImpl.go`](../../../server/service/interpreter/workflowImpl.go)
- [`server/service/interpreter/channel/plan.go`](../../../server/service/interpreter/channel/plan.go)
- [`server/service/common/rpc/invoke.go`](../../../server/service/common/rpc/invoke.go)
- [`server/service/api/service.go`](../../../server/service/api/service.go)
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
- Large string/object `Value` arms may contain blob IDs. Hydration is required
  later, but blob-cache design and implementation are outside this plan.

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

Provisional boundary only. Design concrete-value encoding, proto conversion,
error conversion, and the hydration seam. The separately implemented blob-cache
component may be injected behind that seam; this SDK plan will not define its
storage, eviction, size, recovery, or test strategy.

### Phase 3 — registration assembly

Provisional boundary only. Design schema validation, private type erasure for
generic step/RPC handlers, flow/step/RPC lookup, duplicate detection, and
handler lifecycle.

### Phase 4 — WorkerService runtime

Provisional boundary only. Design the gRPC worker, invocation contexts,
buffering/commit mapping, method errors, transient steps, and shutdown.

### Phase 5 — FlowService client and integration migration

Provisional boundary only. Implement the Phase 1 client façade, migrate the Go
integration suite, and run it against the default Temporal-backed Dex server.

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

type StepDef struct {
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
- `Attempt` starts at one.
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
	AttributeName() string
	attributeDefinition()
}

type ChannelDef interface {
	ChannelName() string
	channelDefinition()
}

type PersistenceSchema struct {
	Attributes []AttributeDef
	Channels   []ChannelDef
}
```

`AttributeDef` and `ChannelDef` are sealed, erased interfaces
implemented by the generic definitions below. A flow declares both static and
map definitions in this schema.

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
	conditionOption()
}

func ExecuteImmediately() Wait
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

func Reply[OUT any](output OUT) RPCResult[OUT]
func ReplyAndMove[OUT any](
	output OUT,
	movements ...StepMovement,
) RPCResult[OUT]
```

Application code defines a Flow method with that signature:

```go
type BillingFlow struct{}

func (BillingFlow) Refund(
	ctx dex.Context,
	input RefundInput,
) (dex.RPCResult[RefundOutput], error) {
	return dex.Reply(RefundOutput{}), nil
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
	runID string,
	channel ChannelDef,
	values ...any,
) error

func (client *Client) PublishToChannelMap(
	ctx context.Context,
	flowID string,
	runID string,
	channel ChannelDef,
	instance string,
	values ...any,
) error

func (client *Client) InvokeRPC(
	ctx context.Context,
	flowID string,
	runID string,
	rpc any,
	input any,
	outputPtr any,
	options InvokeOptions,
) error

func (client *Client) GetAttribute(
	ctx context.Context,
	flowID string,
	runID string,
	attribute AttributeDef,
	valuePtr any,
) (found bool, err error)

func (client *Client) GetAttributeMap(
	ctx context.Context,
	flowID string,
	runID string,
	attribute AttributeDef,
	instance string,
	valuePtr any,
) (found bool, err error)

func (client *Client) SetAttribute(
	ctx context.Context,
	flowID string,
	runID string,
	attribute AttributeDef,
	value any,
) error

func (client *Client) SetAttributeMap(
	ctx context.Context,
	flowID string,
	runID string,
	attribute AttributeDef,
	instance string,
	value any,
) error

func (client *Client) DeleteAttribute(
	ctx context.Context,
	flowID string,
	runID string,
	attribute AttributeDef,
) error

func (client *Client) DeleteAttributeMap(
	ctx context.Context,
	flowID string,
	runID string,
	attribute AttributeDef,
	instance string,
) error

func (client *Client) WaitForAttributeEqual(
	ctx context.Context,
	flowID string,
	runID string,
	attribute AttributeDef,
	value any,
	options WaitOptions,
) error

func (client *Client) WaitForAttributeMapEqual(
	ctx context.Context,
	flowID string,
	runID string,
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
separately; physical key construction remains internal.

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
	runID string,
	attributes ...AttributeDef,
) (map[string]Value, error)

func (client *Client) SetAttributes(
	ctx context.Context,
	flowID string,
	runID string,
	writes ...AttributeWrite,
) error
```

The remaining FlowService operations use non-generic public types:

| Server RPC | Phase 1 façade |
|---|---|
| `StopFlow` | `Client.StopFlow(ctx, flowID, runID, StopOptions)` |
| `WaitForFlow` | `Client.WaitForFlow(ctx, flowID, runID, WaitForFlowOptions)` |
| `SearchFlows` | `Client.SearchFlows(ctx, SearchFlowsOptions)` |
| `ResetFlow` | `Client.ResetFlow(ctx, flowID, runID, ResetOptions)` |
| `SkipTimer` | `Client.SkipTimer(ctx, flowID, runID, StepExecutionRef, TimerRef)` |
| `UpdateFlowConfig` | `Client.UpdateFlowConfig(ctx, flowID, runID, FlowConfig)` |
| `WaitForStepCompletion` | `Client.WaitForStepCompletion(ctx, flowID, StepExecutionRef, WaitOptions)` |
| `TriggerContinueAsNew` | `Client.TriggerContinueAsNew(ctx, flowID, runID)` |
| `HealthCheck` | `Client.HealthCheck(ctx)` |

`runID` may be empty for operations where the server permits targeting the
current run. `StartFlow` returns only the created run ID because the caller
already supplied the flow ID.

`WaitForAttributeEqual` compares the encoded server value. Waiting on a
blob-backed stored value may return `FailedPrecondition`; SDK hydration does not
change server-side wait semantics.

Request IDs:

- the SDK generates one UUID per logical `InvokeRPC`, `WaitForStepCompletion`, or
  `WaitForAttributeEqual` call;
- transparent retries reuse it;
- option structs may accept an advanced override for deterministic tests or an
  application retry spanning client calls;
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

`LoadBlobs` is not a public client operation. It is an internal hydration call
implemented after the Phase 2 seam is designed.

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
	Timeout         *time.Duration
	IDReusePolicy   IDReusePolicy
	CronSchedule    string
	StartDelay      *time.Duration
	RetryPolicy     *FlowRetryPolicy
	Attributes      []InitialAttribute
	ConfigOverride *FlowConfig
	AlreadyStarted *AlreadyStartedOptions
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
	RequestID   string
}

type InvokeOptions struct {
	Timeout        time.Duration
	LockAttributes []AttributeLock
	RequestID      string
}

type WaitOptions struct {
	Timeout   time.Duration
	RequestID string
}

type WaitForFlowOptions struct {
	NeedsResults bool
	Timeout      time.Duration
}

type SearchFlowsOptions struct {
	Query         string
	PageSize      int32
	NextPageToken string
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

type StepExecutionRef struct {
	StepType       string
	ExecutionNumber int32
}

type TimerRef struct {
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

`InitialAttribute` is sealed and constructed with typed helpers so initial
values carry the definition's index configuration:

```go
type InitialAttribute interface {
	initialAttribute()
}

func Initial[T any](
	attribute Attribute[T],
	value T,
) (InitialAttribute, error)

func InitialMapValue[T any](
	attribute AttributeMap[T],
	instance string,
	value T,
) (InitialAttribute, error)
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

Phase 1 cannot use server integration tests because it intentionally has no
registration, worker, codec, or transport. Contract compile tests are the only
reachable test level in this phase.

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
13. Non-generic client methods use server identifiers directly, return only run
    ID from starts, and preserve request-ID overrides.

Run Phase 1 verification through the Makefile:

```bash
make -C sdk-go unitTests 2>&1 | tee /tmp/test-go-sdk-phase1.log
make copyright-check 2>&1 | tee /tmp/test-go-sdk-phase1-copyright.log
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

- Keep this plan linked from [`docs/README.md`](../../README.md) after Phase 1 is
  approved.
- Rewrite [`sdk-go/README.md`](../../../sdk-go/README.md) when Phase 1 types
  land. Use the authoring example above and cover the single `Step[IN]`
  interface, embedded `NoWaitFor[IN]`, Flow method RPCs, close decisions, typed
  attributes/channels, untyped step-execution locals and events, strongly typed
  channel results, timer-fired helpers, `WaitForMethodFailed`, and RPC-only
  channel size.
- Update [`sdk-go/CONTRIBUTION.md`](../../../sdk-go/CONTRIBUTION.md) with the
  Phase 1 verification commands and the rule that application packages do not
  import `dexpb`.
- Blob-cache documentation belongs with its independent component. The Go SDK
  documentation will later describe only how that component is configured or
  injected.

## UI/UX

N/A: no in-repo web UI.
