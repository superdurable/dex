# Go primitives

This page shows each Primitive in Go. Read the matching core file for what it
guarantees. Every skeleton here compiles and runs against the current SDK. The
running example is an order that waits for approval, then ships.

## Flow, Step, and decisions

```go
var (
	OrderStatus = dex.DefineAttribute[string]("order-status",
		dex.Indexed(dex.AttributeIndex{Type: dex.IndexKeyword, IndexKey: "keyword1"}))
	ApprovalDecisions = dex.DefineChannel[ApprovalDecision]("approval-decisions")
	OrderProgress     = dex.DefineStream[string]("order-progress", 1<<20)
)

type OrderApprovalFlow struct {
	dex.FlowDefaults
	shipping ShippingService
}

func NewOrderApprovalFlow(shipping ShippingService) *OrderApprovalFlow {
	return &OrderApprovalFlow{shipping: shipping}
}

func (flow *OrderApprovalFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(recordOrder{}),
		dex.DefineStep(awaitApproval{}),
		dex.DefineStep(shipOrder{shipping: flow.shipping}),
	}
}

func (*OrderApprovalFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{OrderStatus},
		Channels:   []dex.ChannelDef{ApprovalDecisions},
		Streams:    []dex.StreamDef{OrderProgress},
	}
}

func (flow *OrderApprovalFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.SubmitApproval, nil),
		dex.DefineRPC(flow.GetOrderStatus, nil),
	}
}

type recordOrder struct {
	dex.StepDefaultsNoWaitFor[Order]
}

func (recordOrder) Execute(ctx dex.Context, order Order) (*dex.StepDecision, error) {
	if err := OrderStatus.Set(ctx, "awaiting-approval"); err != nil {
		return nil, err
	}
	return dex.GoTo(awaitApproval{}, order), nil
}

type awaitApproval struct {
	dex.StepDefaults
}

func (awaitApproval) WaitFor(_ dex.Context, _ Order) (*dex.Wait, error) {
	return dex.AnyOf(
		ApprovalDecisions.ForOne(),
		dex.Timer(72*time.Hour),
	), nil
}

func (awaitApproval) Execute(ctx dex.Context, order Order) (*dex.StepDecision, error) {
	if ctx.HasTimerFired() {
		if err := OrderStatus.Set(ctx, "expired"); err != nil {
			return nil, err
		}
		return dex.GracefulComplete("expired"), nil
	}
	decisions, err := ApprovalDecisions.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	if !decisions[0].IsApproved {
		if err := OrderStatus.Set(ctx, "rejected"); err != nil {
			return nil, err
		}
		return dex.GracefulComplete("rejected"), nil
	}
	return dex.GoTo(shipOrder{}, order), nil
}

type shipOrder struct {
	dex.StepDefaultsNoWaitFor[Order]
	shipping ShippingService
}

func (shipOrder) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteMethodTimeout: 30 * time.Second,
		ExecuteRetry: &dex.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2,
			MaximumAttempts:    5,
		},
	}
}

func (step shipOrder) Execute(ctx dex.Context, order Order) (*dex.StepDecision, error) {
	idempotencyKey := ctx.FlowID() + "/" + ctx.StepExecutionID()
	if err := step.shipping.Ship(ctx, order.OrderID, idempotencyKey); err != nil {
		return nil, err
	}
	if err := OrderProgress.Write(ctx, "shipped "+order.OrderID); err != nil {
		return nil, err
	}
	if err := OrderStatus.Set(ctx, "shipped"); err != nil {
		return nil, err
	}
	return dex.GracefulComplete(order.OrderID), nil
}
```

- A Flow embeds `dex.FlowDefaults` and implements `GetSteps` and
  `GetPersistenceSchema`. It overrides `GetRPCs` when it has RPCs.
- `DefineStartStep` marks at most one start Step. Its input type is the Flow's
  start input. A Flow with no start Step runs no Step until an RPC moves one.
- Register every Step a decision, failure route, or RPC can reach with
  `DefineStep`.

| Step shape | Embed | Implement |
| --- | --- | --- |
| No wait | `dex.StepDefaultsNoWaitFor[IN]` | `Execute` |
| Wait, then execute | `dex.StepDefaults` | `WaitFor` and `Execute` |
| Custom options | either | also `GetStepOptions() *dex.StepOptions` |

| Decision | Go |
| --- | --- |
| Move to one Step | `dex.GoTo(step, input)` |
| Move to several Steps | `dex.GoToMany(dex.MovementOf(a, x), dex.MovementOf(b, y))` |
| End this branch only | `dex.DeadEnd()` |
| Complete after open branches finish | `dex.GracefulComplete(output)` |
| Complete now, canceling other branches | `dex.ForceComplete(output)` |
| Fail the Flow | `dex.ForceFail(reason)` |
| Complete only if Channels are empty, else move | `dex.ForceCompleteIfChannelsEmpty(output, channels, otherwise...)` |
| Cancel Steps after this decision | `.CancelSiblingSteps(steps...)` or `.CancelSteps(steps...)` |
| Override target options for one move | `dex.MovementOf(step, input, dex.WithStepOptions(options))` |

## Wait and Timer

- `WaitFor` returns `dex.Until(condition)`, `dex.AnyOf(...)`, `dex.AllOf(...)`,
  `dex.AnyComboOf(dex.Combo(...), ...)`, or `dex.SkipWaitImmediately()`.
- Conditions: `channel.ForOne()`, `channel.ForN(n)`, `AtLeast`, `AtMost`,
  `AtLeastAtMost`, `dex.Timer(duration)`, and `dex.SubFlow(flow, input)`.
  `dex.WithConditionID(id)` names a condition so `Execute` can tell winners
  apart.
- In `Execute`, read the outcome with `ctx.HasTimerFired()`,
  `ctx.HasTimerFiredByIndex(i)`, `channel.GetConditionResults(ctx)`, and
  `dex.SubFlowResult(ctx)`.
- `WaitFor` decides what to wait for. Keep it deterministic and free of
  external effects. It may retry.
- A Timer is a durable condition. It survives Worker loss. Never use
  `time.Sleep` for business time.

## Attribute and AttributeMap

- `dex.DefineAttribute[T](name, options...)` and
  `dex.DefineAttributeMap[T](name, options...)` define typed state at package
  scope. List them in `PersistenceSchema.Attributes`.
- Options: `dex.Indexed(dex.AttributeIndex{Type, IndexKey})` for search,
  `dex.SyncToAttributeStore()` for the external store, and
  `dex.HardDeleteOnNull()` for secrets.
- Index keys are typed generic slots such as `keyword1`, `int1`, or `text1`.
  Reuse a slot across Flow types only with the same index type. Scope searches
  by Flow type. An AttributeMap index needs an explicit `IndexKey`.
- `Get` returns `*dex.AttributeNotFoundError` for an unset value. `Set` and
  `Delete` stage a write. Writes commit only when the handler returns
  successfully.
- A map handler reads only instances it loaded. A missing load returns
  `*dex.AttributeMapNotLoadedError`. That is a modeling bug, not an absent key.
- Load on registration with `StepOptions.ExecuteLoadAttributeMapInstances`,
  `WaitForLoadAttributeMapInstances`, or `RPCOptions.LoadAttributeMapInstances`.
  Load a request-chosen instance per call with `RPCInvokeOptions`.
- Lock with `dex.LockAttribute(attribute)` or
  `dex.LockAttributeMap(attributeMap, instance)` in
  `StepOptions.{WaitFor,Execute}LockAttributes`, `RPCOptions.LockAttributes`,
  or `RPCInvokeOptions.LockAttributeMapInstances`. A lock does not load the
  value. A read-modify-write needs both the lock and the load.

```go
var (
	CaseNotes    = dex.DefineAttributeMap[Note]("case-notes")
	CaseRevision = dex.DefineAttribute[int64]("case-revision")
)

func (flow *CaseFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.UpdateNote, &dex.RPCOptions{
			LockAttributes: []dex.AttributeLock{dex.LockAttribute(CaseRevision)},
		}),
		dex.DefineRPC(flow.GetNote, nil),
	}
}

func (*CaseFlow) UpdateNote(ctx dex.Context, update NoteUpdate) (*dex.RPCResult[dex.None], error) {
	revision, err := CaseRevision.Get(ctx)
	var notFound *dex.AttributeNotFoundError
	if err != nil && !errors.As(err, &notFound) {
		return nil, err
	}
	if err := CaseNotes.Set(ctx, update.NoteID, Note{Text: update.Text}); err != nil {
		return nil, err
	}
	if err := CaseRevision.Set(ctx, revision+1); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

func updateNote(ctx context.Context, client *dex.Client, flow *CaseFlow, flowID string, update NoteUpdate) error {
	return client.InvokeRPCWithOptions(ctx, flowID, flow.UpdateNote, update, nil, dex.RPCInvokeOptions{
		LockAttributeMapInstances: []dex.AttributeLock{dex.LockAttributeMap(CaseNotes, update.NoteID)},
		LoadAttributeMapInstances: []dex.AttributeMapLoad{CaseNotes.Load(update.NoteID)},
	})
}
```

The singleton `CaseRevision` lock serializes every writer that requests it.
Every cooperating Step and RPC must request the same lock. The lock is held by
the request, not by the value: never toggle an Attribute to "acquire" it.

### Hard-delete Attributes

```go
var ModelAPIKey = dex.DefineAttribute[string]("model-api-key", dex.HardDeleteOnNull())
```

- Use it for a short-lived secret, such as an API key passed at start with
  `dex.InitialAttribute(ModelAPIKey, apiKey)` in `StartFlowOptions.Attributes`.
  `InitialAttribute` returns an error; check it.
- `Delete`, or `Set` with nil, removes the value permanently. Delete it as soon
  as the Flow no longer needs it. A deleted value reads as
  `*dex.AttributeNotFoundError`.
- Only string and object value types are allowed. It cannot be indexed or
  synced, and Attribute-match waits reject it.
- A write or delete can become visible before its attempt commits. Keep the
  handler safe to repeat, and never read-modify-write the value.

## Channel and ChannelMap

- `dex.DefineChannel[T](name)` is a durable FIFO queue. `dex.DefineChannelMap[T]`
  gives one queue per instance name. List them in `PersistenceSchema.Channels`.
- Publish from an RPC or a Step with `channel.Publish(ctx, value)`. External
  callers publish through a typed RPC; the Client has no direct publish.
- A condition consumes messages only when it wins. `GetConditionResults(ctx)`
  returns the consumed values.
- `PendingMessages`, `FindPendingMessage`, and `Delete` need the Channel loaded
  through `ExecuteLoadChannels`, `RPCOptions.LoadChannels`, or the map
  variants. A missing load returns `*dex.ChannelMessagesNotLoadedError`.
- Pending reads are a snapshot of the invocation. Other handlers may publish
  or consume concurrently. Set `RPCOptions.IsTransactional` when an RPC deletes
  and publishes as one unit. When a decision needs the queue unchanged, every
  cooperating writer requests the same Attribute lock.
- Runnable: [Channel example](https://github.com/superdurable/dex/blob/main/examples/go/primitives/channel/workflow.go).

## RPC

```go
func (*OrderApprovalFlow) SubmitApproval(
	ctx dex.Context,
	decision ApprovalDecision,
) (*dex.RPCResult[dex.None], error) {
	if err := ApprovalDecisions.Publish(ctx, decision); err != nil {
		return nil, err
	}
	return &dex.RPCResult[dex.None]{}, nil
}

func (*ReportFlow) RestartReport(_ dex.Context, query string) (*dex.RPCResult[bool], error) {
	result := &dex.RPCResult[bool]{
		Output:    true,
		NextSteps: []dex.StepMovement{dex.MovementOf(generateReport{}, query)},
	}
	return result.CancelSteps(generateReport{}), nil
}
```

- An RPC is a bound Flow method with the shape
  `func (flow *F) VerbNoun(ctx dex.Context, input IN) (*dex.RPCResult[OUT], error)`.
  Only methods listed in `GetRPCs` through `dex.DefineRPC` are RPCs.
- Start every RPC name with a verb, such as `GetOrderStatus` or
  `SubmitApproval`.
- `dex.RPCOptions` fixes the timeout, locks, `IsTransactional`, and loads at
  registration. `RPCOptions.Name` sets the protocol name that every language's
  Client calls. Set it when another language's code names the RPC. Keep it
  stable.
- Callers invoke `client.InvokeRPC(ctx, flowID, flow.Method, input, &output)`.
  Use `InvokeRPCWithOptions` only to add request-chosen map locks and loads.
- `RPCResult.NextSteps` moves Steps. `RPCResult.CancelSteps(...)` cancels
  every current execution of a Step type, Flow-wide. An RPC has no Step
  lineage, so it has no sibling cancellation.
- An RPC without locks, transaction, or returned effects can read a retained
  closed run. Locks, `IsTransactional`, `NextSteps`, or cancellation require an
  open run.
- Keep RPC handlers short. Do no remote I/O while holding locks.

## Stream

- `dex.DefineStream[T](name, capacityBytes)` defines an ordered progress feed.
  List it in `PersistenceSchema.Streams`.
- A Step writes with `stream.Write(ctx, value)`. For text output, wrap the
  Stream with `dex.NewBufferedTextStream(ctx, stream, options...)`; it flushes
  when the invocation ends.
- A reader calls `client.ReadStream(ctx, flowID, stream, resumeToken, &value)`
  and keeps `StreamMessage.ResumeToken` for the next call. An empty token
  starts at the beginning.
- `client.ListStreamMessages(ctx, flowID, stream, pageSize, beforePageToken, &page)`
  returns newest-first pages. Pass `NextPageToken` back unchanged.
- A Stream is best-effort output, not authoritative state. Readers tolerate
  duplicates.

## SubFlow

```go
func (step awaitReview) WaitFor(_ dex.Context, documentID string) (*dex.Wait, error) {
	return dex.Until(dex.SubFlow(step.child, documentID)), nil
}

func (awaitReview) Execute(ctx dex.Context, _ string) (*dex.StepDecision, error) {
	result, err := dex.SubFlowResult(ctx)
	if err != nil {
		return nil, err
	}
	var review string
	if err := result.DecodeSingleOutput(&review); err != nil {
		return nil, err
	}
	return dex.GracefulComplete(review), nil
}
```

- `dex.SubFlow(childFlow, input, dex.SubFlowOptions{...})` is a Wait condition
  owned by the parent. Register parent and child in the same Registry.
- `SubFlowOptions` sets the child's timeout, reuse policy, initial Attributes,
  and condition ID. `dex.SubFlowID(ctx)` returns the child's Flow ID.
- Runnable: [SubFlow example](https://github.com/superdurable/dex/blob/main/examples/go/primitives/subflow/parent_flow.go).

## Client

```go
func startAndWait(ctx context.Context, client *dex.Client, flow *OrderApprovalFlow, order Order) (string, error) {
	flowID := "order-" + order.OrderID
	if _, err := client.StartFlow(ctx, flow, flowID, order, dex.StartFlowOptions{}); err != nil {
		return "", err
	}
	if err := client.InvokeRPC(ctx, flowID, flow.SubmitApproval,
		ApprovalDecision{IsApproved: true, Approver: "ops"}, nil); err != nil {
		return "", err
	}
	result, err := client.WaitForFlow(ctx, flowID, dex.WaitForFlowOptions{NeedsResults: true})
	if err != nil {
		return "", err
	}
	if result.Status != dex.FlowCompleted {
		return "", errors.New("order flow did not complete: " + result.ErrorMessage)
	}
	var status string
	if _, err := client.GetAttributes(ctx, flowID, dex.ReadAttribute(OrderStatus, &status)); err != nil {
		return "", err
	}
	return status, nil
}

func waitForCaseRevision(ctx context.Context, client *dex.Client, flowID string, revision int64) (int64, error) {
	var current int64
	err := client.WaitForAttributeMatch(ctx, flowID, CaseRevision,
		dex.AttributeMatchGreaterThanOrEqual(revision), &current, dex.WaitForAttributeOptions{})
	return current, err
}
```

| Need | Go Client call |
| --- | --- |
| Start a Flow | `StartFlow(ctx, flow, flowID, input, options)` |
| Wait for the terminal result | `WaitForFlow(ctx, flowID, options)` |
| Read committed Attributes | `GetAttributes(ctx, flowID, reads...)` |
| Mutate state or compute a snapshot | `InvokeRPC` / `InvokeRPCWithOptions` |
| Wait for a committed Attribute value | `WaitForAttributeMatch` / `WaitForAttributeMapInstanceMatch` |
| Wait for one Step execution | `WaitForStepCompletion(ctx, flowID, dex.StepExecutionID{StepType: ...}, options)` |
| One-off status of a known Flow | `DescribeFlow(ctx, flowID)` |
| Find Flows by indexed Attributes | `SearchFlows(ctx, query, pageSize, nextPageToken)` |
| Read or list Stream output | `ReadStream` / `ListStreamMessages` |
| Stop a Flow | `StopFlow(ctx, flowID, dex.StopOptions{...})` |

- Pass a bounded `context.Context` to every call.
- `WaitForFlow` may return `*dex.LongPollTimeoutError` when the Server ends one
  long poll. Call it again. A failed Flow is a `FlowResult.Status`, not a Go
  error.
- `WaitForAttributeMatch` needs an open run. After the run closes it returns
  `*dex.FlowNotActiveOrNotFoundError`; read the final value with
  `GetAttributes`.
- The output pointer of an Attribute-match wait receives the actual matched
  value. A revision Attribute is a coalescing watermark: one wait can skip
  intermediate values. Reload state after the wait.
- The Client has no direct state write. Write Flow-owned state through a typed
  RPC.

## Client Attribute reads

- `client.GetAttributes(ctx, flowID, reads...)` reads committed values of
  registered Attributes in one call, without a Worker. It reads a retained
  closed run too.
- Pass `dex.ReadAttribute(attribute, &value)` or
  `dex.ReadAttributeMapInstance(attributeMap, instance, &value)` for each value.
- An unset value leaves its destination unchanged. Use the returned
  `AttributeReadResult.IsSet` or `IsMapInstanceSet` when unset must differ
  from a stored zero value.
- On any error no destination is written.
- Each definition must equal one registered by a Flow in the Client's
  Registry: same name, kind, value type, and options.

## StepOptions, durability, and heartbeat

- `StepOptions` holds per-method timeouts, `HeartbeatTimeout`, `WaitForRetry`
  and `ExecuteRetry` (`*dex.RetryPolicy`), failure routes, durability, locks,
  and loads. Leave a field zero to use the default.
- Route an exhausted `Execute` with
  `ExecuteFailure: dex.ProceedToOnExecuteFailure(recoveryStep{}, nil)`. Route an
  exhausted `WaitFor` with `WaitForFailure: dex.ProceedOnWaitForFailure`; the
  following `Execute` sees `ctx.WaitForMethodFailed()`.
- Durability: `StepDurabilitySync` persists before the next move.
  `StepDurabilityAsync` is faster but can rerun recent work after a node
  failure. Set a Flow default with `FlowConfig.StepDurability` through
  `StartFlowOptions.ConfigOverride`, and override `WaitForDurability` or
  `ExecuteDurability` per method.
- `Context` embeds `context.Context`. Pass `ctx` into external calls; its
  deadline is the attempt deadline. Check `ctx.Err()` in long loops; Dex
  cancels the context on timeout, heartbeat loss, Step cancellation, or Worker
  stop.
- `ctx.FlowID()` plus `ctx.StepExecutionID()` is stable across retries of one
  Step execution. Use it as an idempotency key. `ctx.Attempt()` is not one.
- `ctx.FlowStartedAt()` is the run's start time. Use it, not `time.Now()`, for
  deterministic identities.

```go
func (importRecords) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteMethodTimeout: time.Hour,
		HeartbeatTimeout:     time.Minute,
	}
}

func (importRecords) Execute(ctx dex.Context, batchCount int) (*dex.StepDecision, error) {
	var checkpoint ImportCheckpoint
	if _, err := ctx.GetLastHeartbeatValue(&checkpoint); err != nil {
		return nil, err
	}
	for batch := checkpoint.NextBatch; batch < batchCount; batch++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// Import one batch idempotently here.
		if err := ctx.RecordHeartbeat(ImportCheckpoint{NextBatch: batch + 1}); err != nil {
			return nil, err
		}
	}
	return dex.GracefulComplete(batchCount), nil
}
```

- `RecordHeartbeat` keeps a long attempt alive and stores a checkpoint for the
  next attempt. Checkpoints are lossy: a retry can see an older value. Never
  use a checkpoint as the only proof of an external effect.
- `GetLastHeartbeatValue` returns false and leaves the target unchanged when
  no checkpoint exists.
- Step cancellation is cooperative. A canceled attempt's late writes and
  decision are discarded, but its external calls already happened.
