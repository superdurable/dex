# Go error handling

Read [core error handling](../core/error-handling.md) first. It defines the
start-first rule, closed-Flow races, recovery, and attempt progress. This page
gives the Go error types and idioms.

## Three error layers

| Layer | Where it comes from | What to do |
| --- | --- | --- |
| Handler error | Your `WaitFor`, `Execute`, RPC, or `HandleTimeout` returns an error | Dex applies the retry policy, then the failure route |
| Typed Client error | A Dex outcome, such as "already started" or "not found" | Match the concrete type with `errors.As` and decide |
| Context or transport error | Caller cancellation, deadline, connectivity | Retry or report at the caller; it says nothing about the Flow |

- Match errors with `errors.As` or `errors.Is`. Never parse error text.
- Every remote Client error unwraps to `*dex.ServiceError`, which carries
  `Code` and `SubStatus` for diagnostics. Catch `*dex.ServiceError` only at a
  narrow boundary that treats every remote outcome the same.
- Local definition, value-mapping, and argument errors are not service errors.
  Let them surface as defects.

| Go type | Meaning |
| --- | --- |
| `*dex.FlowAlreadyStartedError` | The Flow ID belongs to another start request |
| `*dex.FlowNotFoundError` | No Flow with this ID exists (or it is being purged) |
| `*dex.FlowNotActiveOrNotFoundError` | No open run accepted the call; it does not say the action happened |
| `*dex.FlowClosingError` | The run is closing; wait for or re-read the terminal state |
| `*dex.RPCLockConflictError` | Another invocation holds a requested lock; retry the call |
| `*dex.LongPollTimeoutError` | One server long poll ended; call again |
| `*dex.RequestTimeoutError` | The caller's request budget expired; the Flow did not fail |
| `*dex.WorkerInvocationError` | The RPC handler returned an error |
| `*dex.FlowDefinitionError` | A definition is not registered in this Registry |
| `*dex.ValueMappingError` | A value cannot be encoded or decoded as its type |
| `*dex.AttributeNotFoundError` | Inside a handler: the Attribute is unset |
| `*dex.AttributeMapNotLoadedError`, `*dex.ChannelMessagesNotLoadedError` | Inside a handler: the instance or Channel was not loaded |

## Idempotent start

```go
var ErrOrderConflict = errors.New("order already exists with a different request")

func StartOrder(ctx context.Context, client *dex.Client, flow *OrderApprovalFlow, requestID string, order Order) error {
	_, err := client.StartFlow(ctx, flow, "order-"+order.OrderID, order, dex.StartFlowOptions{
		RequestID:      &requestID,
		AlreadyStarted: &dex.AlreadyStartedOptions{IgnoreError: true},
		IDReusePolicy:  dex.IDReuseDisallow,
	})
	var alreadyStarted *dex.FlowAlreadyStartedError
	if errors.As(err, &alreadyStarted) {
		return ErrOrderConflict
	}
	return err
}
```

- The stable start identity is `StartFlowOptions.RequestID` (a `*string`)
  plus `AlreadyStarted: &dex.AlreadyStartedOptions{IgnoreError: true}`. Keep
  both across retries. Either one alone still returns
  `*dex.FlowAlreadyStartedError`.
- A nil `RequestID` makes the SDK generate a new one for each call. A pointer
  to an empty string is invalid.
- `IDReusePolicy: dex.IDReuseDisallow` prevents a second execution after the
  first one closes.
- Wait for a required business milestone with `WaitForAttributeMatch` or
  `WaitForStepCompletion` under a deadline.
- A Step starts a dependent top-level Flow through a late-bound Client, with
  a request ID derived from its own Flow ID:

```go
type ClientProvider func() *dex.Client

type startShipment struct {
	dex.StepDefaultsNoWaitFor[Order]
	clientProvider ClientProvider
}

func (step startShipment) Execute(ctx dex.Context, order Order) (*dex.StepDecision, error) {
	requestID := ctx.FlowID() + "/start-shipment"
	_, err := step.clientProvider().StartFlow(ctx, &OrderApprovalFlow{}, "order-"+order.OrderID, order,
		dex.StartFlowOptions{
			RequestID:      &requestID,
			AlreadyStarted: &dex.AlreadyStartedOptions{IgnoreError: true},
			IDReusePolicy:  dex.IDReuseDisallow,
		})
	if err != nil {
		return nil, err
	}
	return dex.GracefulComplete(order.OrderID), nil
}
```

## Query-only Get and closed Flows

```go
func GetOrder(ctx context.Context, client *dex.Client, flow *OrderApprovalFlow, orderID string) (OrderView, error) {
	var status string
	err := client.InvokeRPC(ctx, "order-"+orderID, flow.GetOrderStatus, nil, &status)
	var notActive *dex.FlowNotActiveOrNotFoundError
	var notFound *dex.FlowNotFoundError
	if errors.As(err, &notActive) || errors.As(err, &notFound) {
		return OrderView{IsMissing: true}, nil
	}
	if err != nil {
		return OrderView{}, err
	}
	return OrderView{Status: status}, nil
}
```

- Translate only for a query RPC: no `RPCOptions.LockAttributes`, no
  `RPCInvokeOptions` locks, no `IsTransactional`, and no `NextSteps` or
  cancellation in the result.
- `GetAttributes` returns only `*dex.FlowNotFoundError` for a missing Flow;
  translate it the same way.

## Closed-Flow races

- `*dex.FlowNotActiveOrNotFoundError` from a mutation means no open run took
  the call, not that the action happened. Reconcile from domain state first.
- When the terminal status changes the answer, call
  `client.DescribeFlow(ctx, flowID)` and read `FlowInfo.Status`
  (`dex.FlowCompleted`, `dex.FlowFailed`, `dex.FlowCanceled`, ...).
- `*dex.FlowClosingError`: wait for or re-read the terminal state before
  deciding.

## Retry ownership

```go
type chargeCard struct {
	dex.StepDefaultsNoWaitFor[Order]
	provider ProviderClient
}

func (chargeCard) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteFailure: dex.ProceedToOnExecuteFailure(recordChargeFailure{}, nil),
	}
}

func (step chargeCard) Execute(ctx dex.Context, order Order) (*dex.StepDecision, error) {
	err := step.provider.Charge(ctx, chargeIdempotencyKey(ctx.FlowID()), order.Amount)
	var rateLimited *rateLimitedError
	if errors.As(err, &rateLimited) {
		return nil, dex.RetryAfter(rateLimited.retryAfter, err)
	}
	if err != nil {
		return nil, dex.ErrorWithStack(err)
	}
	return dex.GracefulComplete(order.OrderID), nil
}
```

- Return the error; the Step retry policy retries. Never loop in memory.
- `dex.RetryAfter(delay, cause)` sets the next retry delay. Use it only with a
  provider's delay hint, never as a polling loop.
- `dex.ErrorWithStack(err)` records the origin stack for diagnostics.

## Recovery Step

```go
type recordChargeFailure struct {
	dex.StepDefaultsNoWaitFor[Order]
}

func (recordChargeFailure) Execute(ctx dex.Context, order Order) (*dex.StepDecision, error) {
	failure := ctx.RecoveryError()
	if failure == nil {
		return dex.ForceFail("charge recovery started without a failure"), nil
	}
	if failure.AttemptProgress != dex.StepAttemptProgressNotStarted {
		// An attempt may have charged the card: reconcile before deciding.
		return dex.GoTo(reconcileCharge{}, order), nil
	}
	reason := "charge failed"
	if failure.IsMethodTimeout() || failure.IsHeartbeatTimeout() {
		reason = "charge timed out"
	}
	if err := OrderStatus.Set(ctx, "charge-failed"); err != nil {
		return nil, err
	}
	return dex.ForceFail(fmt.Sprintf("%s: %s", reason, failure.Detail)), nil
}
```

- `ctx.RecoveryError()` returns the final failure that selected this route, or
  nil outside a recovery path. It has `ErrorType`, `Detail`, and
  `AttemptProgress`.
- `dex.StepAttemptProgressNotStarted` guarantees the handler never ran.
  `StepAttemptProgressNotCompleted` and `StepAttemptProgressUnspecified` mean
  it may have run: reconcile external effects before repeating them.
- Tell Dex time limits apart with `IsMethodTimeout()`, `IsHeartbeatTimeout()`,
  and `IsRetryTotalDurationExceeded()`. Never compare `ErrorType` with a
  string. The predicates are nil-safe.
- A Flow-level soft timeout: implement `HandleTimeout(ctx dex.Context)
  (*dex.StepDecision, error)` on the Flow and set a positive
  `StartFlowOptions.Timeout`. It returns an explicit decision.
