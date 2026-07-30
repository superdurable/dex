# Go SDK rewrite plan

Status: Phase 1 design. Later phases are boundaries only and require their own
design review before implementation.

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
- WaitFor may return a waiting condition and one transient step movement.
- A transient step skips WaitFor, runs after WaitFor writes are committed, and
  must return only `DeadEnd`.
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

- flow, step, and RPC definitions;
- `PersistenceSchema`;
- typed attributes, channels, step-execution locals, and events;
- invocation `Context`;
- waiting conditions and condition results;
- step movements, close decisions, and step options;
- public client request/result structs and typed client helper signatures;
- public SDK errors.

Phase 1 does not implement registration, WorkerService, gRPC calls, protobuf
mapping, value encoding, blob hydration, or blob caching.

### Phase 2 — value codec and protobuf mapping

Provisional boundary only. Design concrete-value encoding, proto conversion,
error conversion, and the hydration seam. The separately implemented blob-cache
component may be injected behind that seam; this SDK plan will not define its
storage, eviction, size, recovery, or test strategy.

### Phase 3 — definition assembly and registration

Provisional boundary only. Design schema validation, type erasure for generic
definitions, flow/step/RPC lookup, duplicate detection, and handler lifecycle.

### Phase 4 — WorkerService runtime

Provisional boundary only. Design the gRPC worker, invocation contexts,
buffering/commit mapping, method errors, transient steps, and shutdown.

### Phase 5 — FlowService client and integration migration

Provisional boundary only. Implement the Phase 1 client façade, migrate the Go
integration suite, and run it against the default Temporal-backed Dex server.

## Phase 1 detailed design

### Design rules

1. Application code imports `dex`, not `gen/dexpb`.
2. Persisted flow, step, RPC, attribute, and channel names are explicit strings.
   Do not derive durable identity from Go package or struct names.
3. Generic definitions are immutable and safe as package variables.
4. Invocation state lives only in a `Context`-bound handle. Definitions must
   never retain a current invocation or be mutated by the worker.
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
definition.go       FlowDefinition and erased definition interfaces
step.go             Step, StepWithWaitFor, StepDefinition
context.go          Context and invocation metadata
attribute.go        Attribute, AttributeMap, indexing, bound handles
channel.go          Channel, ChannelMap, publish, size, bounded conditions
local.go            StepExecutionLocal
event.go            Event and EventRecord
condition.go        Wait, timers, combinations, results
decision.go         StepMovement, StepDecision, CloseDecision helpers
options.go          retry, durability, step, start, and flow options
rpc.go              RPC, RPCDefinition, RPCResult
client.go           Client façade declarations and public result types
errors.go           public error model
```

This is a logical split, not permission to add runtime implementations during
Phase 1.

### Definitions and explicit durable names

Generic step/RPC definitions retain compile-time input/output types. Erased
interfaces allow one flow to contain heterogeneous definitions without exposing
reflection or protobuf types.

```go
type AnyStepDefinition interface {
	StepType() string
	stepDefinition()
}

type StepDefinition[IN any] struct {
	// unexported
}

func DefineStep[IN any](
	stepType string,
	handler Step[IN],
) StepDefinition[IN]

type AnyRPCDefinition interface {
	RPCName() string
	rpcDefinition()
}

type RPCDefinition[IN, OUT any] struct {
	// unexported
}

func DefineRPC[IN, OUT any](
	rpcName string,
	handler RPC[IN, OUT],
) RPCDefinition[IN, OUT]

type FlowDefinition struct {
	// unexported
}

func DefineFlow(
	flowType string,
	options ...FlowDefinitionOption,
) FlowDefinition

func WithSteps(steps ...AnyStepDefinition) FlowDefinitionOption
func WithRPCs(rpcs ...AnyRPCDefinition) FlowDefinitionOption
func WithPersistence(schema PersistenceSchema) FlowDefinitionOption
```

Names must be non-empty. Duplicate definitions are rejected later when a flow is
assembled, but the public definition shape is fixed in Phase 1.

There is no required default starting step. `StartFlowAt` chooses one, while
`StartFlow` starts a flow with no step, matching the current server contract.

### Step handlers

An Execute-only step implements `Step`. A step with WaitFor additionally
implements `StepWithWaitFor`.

```go
type Step[IN any] interface {
	Execute(ctx Context, input IN) (StepDecision, error)
}

type StepWithWaitFor[IN any] interface {
	Step[IN]
	WaitFor(ctx Context, input IN) (Wait, error)
}
```

There is no `NoWaitFor` marker and no public `SkipWaitFor` field. Registration
later infers the normal skip flag from whether the handler implements
`StepWithWaitFor`. The transient-step constructor always sets the internal skip
flag.

Example:

```go
type ApproveOrderStep struct{}

func (ApproveOrderStep) WaitFor(
	ctx dex.Context,
	input ApproveOrderInput,
) (dex.Wait, error) {
	approval := ApprovalChannel.In(ctx)
	return dex.AnyOf(
		approval.ForOne(),
		dex.Timer("approval-timeout", input.Timeout),
	), nil
}

func (ApproveOrderStep) Execute(
	ctx dex.Context,
	input ApproveOrderInput,
) (dex.StepDecision, error) {
	results := ctx.GetConditionResults()
	_ = results
	return dex.Next(dex.Move(ShipOrder, ShipOrderInput{
		OrderID: input.OrderID,
	})), nil
}

var ApproveOrder = dex.DefineStep[ApproveOrderInput](
	"approve-order",
	ApproveOrderStep{},
)
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

	GetConditionResults() ConditionResults
	WaitForMethodFailed() bool
	RecordEvent(record EventRecord) error
}
```

Semantics:

- `StepExecutionID` and `FromStepExecutionID` are empty for RPC invocations.
- `GetConditionResults` is empty in WaitFor, RPC, and skip-WaitFor Execute.
- `WaitForMethodFailed` is true only when Execute follows a failed WaitFor under
  `ProceedOnFailure`.
- `Attempt` starts at one.
- writes, events, and channel publishes are buffered until the method returns
  successfully;
- bound attribute reads observe earlier writes in the same invocation.

`Context.RecordEvent` takes an `EventRecord` because Go does not support generic
methods. The typed event definition builds that record:

```go
type Event[T any] struct {
	// unexported
}

type EventRecord struct {
	// unexported
}

func DefineEvent[T any](name string) Event[T]
func (event Event[T]) Record(value T) EventRecord

var PaymentAttempted = dex.DefineEvent[PaymentAttempt]("payment-attempted")

err := ctx.RecordEvent(PaymentAttempted.Record(attempt))
```

An event name may be recorded once per method invocation. Events do not belong
to `PersistenceSchema`.

### PersistenceSchema

Persistence remains the combination of attributes and channels:

```go
type AttributeDefinition interface {
	AttributeName() string
	attributeDefinition()
}

type ChannelDefinition interface {
	ChannelName() string
	channelDefinition()
}

type PersistenceSchema struct {
	Attributes []AttributeDefinition
	Channels   []ChannelDefinition
}
```

`AttributeDefinition` and `ChannelDefinition` are sealed, erased interfaces
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
key. The physical escaping/encoding is SDK-owned; callers use `Key(instance)`
instead of constructing it.

```go
type AttributeKey struct {
	// unexported
}

func (a Attribute[T]) Key() AttributeKey
func (a AttributeMap[T]) Key(instance string) AttributeKey
```

Invocation binding creates short-lived handles:

```go
type BoundAttribute[T any] struct {
	// unexported
}

type BoundAttributeMap[T any] struct {
	// unexported
}

func (a Attribute[T]) In(ctx Context) BoundAttribute[T]
func (a AttributeMap[T]) In(ctx Context) BoundAttributeMap[T]

func (a BoundAttribute[T]) Get() (value T, found bool, err error)
func (a BoundAttribute[T]) Set(value T) error
func (a BoundAttribute[T]) Delete() error

func (a BoundAttributeMap[T]) Get(instance string) (value T, found bool, err error)
func (a BoundAttributeMap[T]) Set(instance string, value T) error
func (a BoundAttributeMap[T]) Delete(instance string) error
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

Static and map channels follow the same immutable-definition/bound-handle
model:

```go
type Channel[T any] struct {
	// unexported
}

type ChannelMap[T any] struct {
	// unexported
}

func DefineChannel[T any](name string) Channel[T]
func DefineChannelMap[T any](name string) ChannelMap[T]

type BoundChannel[T any] struct {
	// unexported
}

type BoundChannelMap[T any] struct {
	// unexported
}

func (c Channel[T]) In(ctx Context) BoundChannel[T]
func (c ChannelMap[T]) In(ctx Context) BoundChannelMap[T]
```

Bound handles expose publish and wait construction:

```go
func (c BoundChannel[T]) Publish(value T) error
func (c BoundChannelMap[T]) Publish(instance string, value T) error

func (c BoundChannel[T]) ForOne(options ...ConditionOption) ChannelCondition
func (c BoundChannel[T]) ForN(count int, options ...ConditionOption) ChannelCondition
func (c BoundChannel[T]) AtLeast(count int, options ...ConditionOption) ChannelCondition
func (c BoundChannel[T]) AtMost(count int, options ...ConditionOption) ChannelCondition
func (c BoundChannel[T]) AtLeastAtMost(
	atLeast int,
	atMost int,
	options ...ConditionOption,
) ChannelCondition

func (c BoundChannelMap[T]) ForOne(
	instance string,
	options ...ConditionOption,
) ChannelCondition
func (c BoundChannelMap[T]) ForN(
	instance string,
	count int,
	options ...ConditionOption,
) ChannelCondition
func (c BoundChannelMap[T]) AtLeast(
	instance string,
	count int,
	options ...ConditionOption,
) ChannelCondition
func (c BoundChannelMap[T]) AtMost(
	instance string,
	count int,
	options ...ConditionOption,
) ChannelCondition
func (c BoundChannelMap[T]) AtLeastAtMost(
	instance string,
	atLeast int,
	atMost int,
	options ...ConditionOption,
) ChannelCondition
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

Channel size is RPC-only:

```go
func (c BoundChannel[T]) Size() int
func (c BoundChannelMap[T]) Size(instance string) int
```

The intended usage is:

```go
myCh := OrderCommands.In(ctx)
size := myCh.Size()

myChMap := CommandsByOrder.In(ctx)
orderSize := myChMap.Size("order-1")
```

`Size` starts from `InvokeWorkerRPCRequest.channel_infos` and includes messages
published earlier in the current RPC invocation. Calling it from WaitFor or
Execute is a programming error detected by the bound handle. There is no
`Context.ChannelSize` or raw channel-name API.

Close decisions take concrete channel references:

```go
type ChannelReference interface {
	ChannelName() string
	channelReference()
}

func (c Channel[T]) Ref() ChannelReference
func (c ChannelMap[T]) Ref(instance string) ChannelReference
```

### StepExecutionLocal

```go
type StepExecutionLocal[T any] struct {
	// unexported
}

type BoundStepExecutionLocal[T any] struct {
	// unexported
}

func DefineStepExecutionLocal[T any](key string) StepExecutionLocal[T]

func (local StepExecutionLocal[T]) In(ctx Context) BoundStepExecutionLocal[T]
func (local BoundStepExecutionLocal[T]) Set(value T) error
func (local BoundStepExecutionLocal[T]) Get() (value T, found bool, err error)
```

`Set` is valid in WaitFor. `Get` is valid in Execute for the same step execution.
The SDK does not expose Execute writes because the current server does not carry
them to another method. Locals are unavailable to RPC and transient Execute.

### Waiting conditions and results

```go
type Wait struct {
	// unexported
}

type Condition interface {
	ConditionID() string
}

func ExecuteImmediately() Wait
func AllOf(conditions ...Condition) Wait
func AnyOf(conditions ...Condition) Wait
func Combo(conditions ...Condition) ConditionCombination
func AnyComboOf(combinations ...ConditionCombination) Wait
func Timer(
	conditionID string,
	duration time.Duration,
) TimerCondition
```

Channel condition IDs are optional for `AllOf` and `AnyOf`:

```go
func WithConditionID(conditionID string) ConditionOption
```

The SDK assigns internal IDs to unnamed conditions when serializing one `Wait`.
`AnyComboOf` refers to the actual condition values supplied to `Combo`, so the
application does not manually duplicate IDs. Explicit IDs remain useful for
timer skipping and result lookup.

`Context.GetConditionResults()` returns SDK-owned immutable values:

```go
type ConditionStatus uint8

const (
	ConditionWaiting ConditionStatus = iota
	ConditionCompleted
)

type TimerResult struct {
	ConditionID string
	Status      ConditionStatus
}

type ChannelResult struct {
	ConditionID string
	ChannelName string
	Status      ConditionStatus
	// encoded values are unexported
}

type ConditionResults struct {
	TimerResults   []TimerResult
	ChannelResults []ChannelResult
}

func DecodeChannelValues[T any](
	result ChannelResult,
) ([]T, error)
```

Keeping results as explicit slices preserves declaration order and supports
multiple conditions on the same channel.

### Transient step

The transient API makes the stricter server contract visible:

```go
type TransientStepMovement struct {
	// unexported
}

func TransientStep[IN any](
	step StepDefinition[IN],
	input IN,
	options ...TransientStepOption,
) TransientStepMovement

func (wait Wait) WithTransientStep(
	step TransientStepMovement,
) Wait
```

`TransientStepOption` intentionally cannot express WaitFor-failure or
Execute-failure proceed behavior. It may express only supported timeout,
retry, durability, and Execute lock-key overrides. The SDK always sets
`skip_wait_for`.

The target Execute must return `DeadEnd()` and no next steps. Runtime validation
remains in Phase 4 because the returned decision is dynamic.

Ordering exposed to users:

1. source WaitFor buffers attributes, locals, events, and channel publishes;
2. the server commits WaitFor writes;
3. transient Execute runs with its own execution ID and source lineage;
4. the source waiting condition starts after transient success;
5. source Execute runs when the condition completes.

### Step movements and close decisions

`StepMovement`, `StepDecision`, and `CloseDecision` have unexported fields.
Helpers construct only combinations accepted by the current server:

```go
type StepMovement struct {
	// unexported
}

func Move[IN any](
	step StepDefinition[IN],
	input IN,
	options ...StepMoveOption,
) StepMovement

func Next(movements ...StepMovement) StepDecision
func GracefulComplete(output any) StepDecision
func ForceComplete(output any) StepDecision
func ForceFail(reason string) StepDecision
func DeadEnd() StepDecision

func ForceCompleteOnChannelsEmpty(
	output any,
	channels []ChannelReference,
	otherwise ...StepMovement,
) StepDecision
```

Rules enforced by construction or Phase 3 validation:

- `Next` requires at least one valid movement.
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
	ProceedTo AnyStepDefinition
	Options   *StepOptions
}

type StepOptions struct {
	WaitForTimeout  time.Duration
	ExecuteTimeout  time.Duration
	WaitForRetry    *RetryPolicy
	ExecuteRetry    *RetryPolicy
	WaitForFailure  WaitForFailurePolicy
	ExecuteFailure  *ExecuteFailure
	WaitForDurability  StepDurability
	ExecuteDurability  StepDurability
	WaitForLockAttributes []AttributeKey
	ExecuteLockAttributes []AttributeKey
}
```

Phase 3 validates that an Execute failure target can consume the failed step's
unchanged input, because the server reuses that input. `StepOptions` does not
expose server-owned fields or a generic skip flag.

### RPC

RPC output and its optional next-step movements are explicit:

```go
type RPC[IN, OUT any] interface {
	Handle(ctx Context, input IN) (RPCResult[OUT], error)
}

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

RPC handlers use bound attributes/channels and `Context.RecordEvent`. They do
not receive a legacy `Persistence` or `Communication` argument. RPC cannot use
step-execution locals or return a close decision.

### Public client types and façade

Phase 1 fixes signatures but does not implement gRPC.

Go does not support generic methods, so type-safe calls are package functions
over a non-generic `Client`:

```go
type Client struct {
	// unexported
}

type FlowRun struct {
	FlowID string
	RunID  string
}

func StartFlow(
	ctx context.Context,
	client *Client,
	flow FlowDefinition,
	flowID string,
	options StartFlowOptions,
) (FlowRun, error)

func StartFlowAt[IN any](
	ctx context.Context,
	client *Client,
	flow FlowDefinition,
	flowID string,
	step StepDefinition[IN],
	input IN,
	options StartFlowOptions,
) (FlowRun, error)

func PublishToChannel[T any](
	ctx context.Context,
	client *Client,
	run FlowRun,
	channel Channel[T],
	value T,
) error

func InvokeRPC[IN, OUT any](
	ctx context.Context,
	client *Client,
	run FlowRun,
	rpc RPCDefinition[IN, OUT],
	input IN,
	options InvokeOptions,
) (OUT, error)

func GetAttribute[T any](
	ctx context.Context,
	client *Client,
	run FlowRun,
	attribute Attribute[T],
) (value T, found bool, err error)

func SetAttribute[T any](
	ctx context.Context,
	client *Client,
	run FlowRun,
	attribute Attribute[T],
	value T,
) error

func DeleteAttribute[T any](
	ctx context.Context,
	client *Client,
	run FlowRun,
	attribute Attribute[T],
) error

func WaitForAttributeEqual[T any](
	ctx context.Context,
	client *Client,
	run FlowRun,
	attribute Attribute[T],
	value T,
	options WaitOptions,
) error
```

Equivalent map helpers take the instance key immediately after the map
definition. Heterogeneous batch operations use sealed `AttributeRead` and
`AttributeWrite` values built by typed helpers:

```go
func ReadAttribute[T any](attribute Attribute[T]) AttributeRead
func ReadAttributeMap[T any](
	attribute AttributeMap[T],
	instance string,
) AttributeRead

func WriteAttribute[T any](
	attribute Attribute[T],
	value T,
) (AttributeWrite, error)
func WriteAttributeMap[T any](
	attribute AttributeMap[T],
	instance string,
	value T,
) (AttributeWrite, error)

func (client *Client) GetAttributes(
	ctx context.Context,
	run FlowRun,
	attributes ...AttributeRead,
) (AttributeValues, error)
func (client *Client) SetAttributes(
	ctx context.Context,
	run FlowRun,
	writes ...AttributeWrite,
) error
```

The remaining FlowService operations use non-generic public types:

| Server RPC | Phase 1 façade |
|---|---|
| `StopFlow` | `Client.StopFlow(ctx, run, StopOptions)` |
| `WaitForFlow` | `Client.WaitForFlow(ctx, run, WaitForFlowOptions)` |
| `SearchFlows` | `Client.SearchFlows(ctx, SearchFlowsOptions)` |
| `ResetFlow` | `Client.ResetFlow(ctx, run, ResetOptions)` |
| `SkipTimer` | `Client.SkipTimer(ctx, run, StepExecutionRef, TimerRef)` |
| `UpdateFlowConfig` | `Client.UpdateFlowConfig(ctx, run, FlowConfig)` |
| `WaitForStepCompletion` | `Client.WaitForStepCompletion(ctx, flowID, StepExecutionRef, WaitOptions)` |
| `TriggerContinueAsNew` | `Client.TriggerContinueAsNew(ctx, run)` |
| `HealthCheck` | `Client.HealthCheck(ctx)` |

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

func DecodeValue[T any](value Value) (T, error)

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
	FlowRun
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

`StepCompletion.Output` and search attributes can be decoded with
`DecodeValue[T]`. The SDK does not guess an application type when the response
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
	Timeout         time.Duration
	StepOptions     *StepOptions
	IDReusePolicy   IDReusePolicy
	CronSchedule    string
	StartDelay      time.Duration
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
	LockAttributes []AttributeKey
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
	Snapshot = dex.DefineStepExecutionLocal[OrderSnapshot]("snapshot")
)

type WaitForCommandStep struct{}

func (WaitForCommandStep) WaitFor(
	ctx dex.Context,
	input OrderInput,
) (dex.Wait, error) {
	status := OrderStatus.In(ctx)
	if err := status.Set("waiting"); err != nil {
		return dex.Wait{}, err
	}

	snapshot := Snapshot.In(ctx)
	if err := snapshot.Set(OrderSnapshot{OrderID: input.OrderID}); err != nil {
		return dex.Wait{}, err
	}

	commands := Commands.In(ctx)
	return dex.AnyOf(
		commands.ForOne(dex.WithConditionID("command")),
		dex.Timer("timeout", 30*time.Minute),
	), nil
}

func (WaitForCommandStep) Execute(
	ctx dex.Context,
	input OrderInput,
) (dex.StepDecision, error) {
	snapshot, found, err := Snapshot.In(ctx).Get()
	if err != nil {
		return dex.StepDecision{}, err
	}
	if !found {
		return dex.StepDecision{}, fmt.Errorf("snapshot is missing")
	}
	return dex.GracefulComplete(snapshot), nil
}

var WaitForCommand = dex.DefineStep[OrderInput](
	"wait-for-command",
	WaitForCommandStep{},
)

var OrderFlow = dex.DefineFlow(
	"order",
	dex.WithSteps(WaitForCommand),
	dex.WithPersistence(dex.PersistenceSchema{
		Attributes: []dex.AttributeDefinition{OrderStatus},
		Channels:   []dex.ChannelDefinition{Commands},
	}),
)
```

The exact zero values returned on an error are not part of normal application
style; they only satisfy Go's return rules.

## Phase 1 deliverables

1. Approve this public API shape.
2. Add the public declarations with unexported runtime fields.
3. Add compile-time example coverage for generic definitions and signatures.
4. Delete old public API files only when their replacements compile in the same
   change; do not add compatibility aliases.
5. Stop at the Phase 1 exit gate. Do not add registry or WorkerService code.

## Tests

Phase 1 cannot use server integration tests because it intentionally has no
registration, worker, codec, or transport. Contract compile tests are the only
reachable test level in this phase.

Add SDK external-package tests (`package dex_test`) for these scenarios:

1. A flow with heterogeneous typed steps, attributes, channels, and RPCs
   compiles without importing `dexpb`.
2. `Move` and `StartFlowAt` preserve the target step input type.
3. `InvokeRPC` preserves RPC input/output types.
4. Static and map attributes expose typed Get/Set/Delete and typed lock keys.
5. Static and map channels expose Publish, all five count forms, and the
   `myCh.Size()` / `myChMap.Size(key)` RPC shape.
6. A Wait combines timers and channel conditions through All, Any, and
   AnyCombo.
7. A Wait carries a typed transient movement.
8. Execute can return next, all close decisions, and conditional
   force-complete fallback.
9. `StepExecutionLocal`, `Context.RecordEvent`,
   `Context.WaitForMethodFailed`, and condition-result declarations compile.
10. Client option structs preserve optional `FlowConfig` fields and request-ID
    overrides.

Run Phase 1 verification through the Makefile:

```bash
make -C sdk-go unitTests 2>&1 | tee /tmp/test-go-sdk-phase1.log
make copyright-check 2>&1 | tee /tmp/test-go-sdk-phase1-copyright.log
```

Later phases must add Temporal integration coverage for every actual proto
mapping and runtime behavior. Cadence execution is not required because the
default Dex server image is Temporal-backed.

## Documentation

- Keep this plan linked from [`docs/README.md`](../../README.md) after Phase 1 is
  approved.
- Rewrite [`sdk-go/README.md`](../../../sdk-go/README.md) when Phase 1 types
  land. Use the authoring example above and cover transient steps, close
  decisions, typed persistence, `StepExecutionLocal`,
  `WaitForMethodFailed`, and RPC-only channel size.
- Update [`sdk-go/CONTRIBUTION.md`](../../../sdk-go/CONTRIBUTION.md) with the
  Phase 1 verification commands and the rule that application packages do not
  import `dexpb`.
- Blob-cache documentation belongs with its independent component. The Go SDK
  documentation will later describe only how that component is configured or
  injected.

## UI/UX

N/A: no in-repo web UI.
