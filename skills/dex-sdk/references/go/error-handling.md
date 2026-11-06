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

- Call `StartFlow` directly. Do not preflight with an RPC, `DescribeFlow`, or
  search.
- A retry of the same logical start reuses the same `RequestID` and
  `IgnoreError: true`. The same request then returns the existing run instead
  of an error.
- `RequestID` alone still returns `*dex.FlowAlreadyStartedError`.
  `IgnoreError` alone does not hide a conflict with a different request.
- A nil `RequestID` makes the SDK generate a new one for each call. A pointer
  to an empty string is invalid.
- Bind the request ID to the complete immutable request. The Server does not
  compare payloads on a matching ID.
- `IDReuseDisallow` prevents a second execution after the first one closes.
- After a lost response, retry with the same identities, or read the owning
  domain state. Do not wrap every `*dex.ServiceError` in a replay.
- When the API must confirm a business milestone, wait for it after the start
  with `WaitForAttributeMatch` or `WaitForStepCompletion` under a deadline. A
  timeout does not undo the start.
- Start dependent top-level Flows from a Step through the injected Client, with
  stable request IDs. Never chain a second `StartFlow` in the HTTP handler.

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

- This translation is valid only for a query RPC: no locks in `RPCOptions` or
  per-call options, no `IsTransactional`, and no returned effects. Such an RPC
  still reads a retained closed run, so a not-found error means the Flow is
  gone.
- Do not add `WaitForFlow`, history reads, or retries to tell "missing" from
  "closed".
- `GetAttributes` reads closed runs too. It returns only
  `*dex.FlowNotFoundError` for a missing Flow; translate it the same way.

## Closed-Flow races

- `*dex.FlowNotActiveOrNotFoundError` from a mutation means no open run took
  the call. It does not mean the action succeeded.
- First reconcile from domain state and the operation's invariants.
- Call `client.DescribeFlow(ctx, flowID)` only when the terminal status changes
  the answer. Treat `dex.FlowCompleted` as success only if completion
  guarantees the requested condition. Any other status, or
  `*dex.FlowNotFoundError`, is a domain failure or an unknown outcome.
- `*dex.FlowClosingError`: wait for or re-read the retained terminal state
  before deciding.

## Retry ownership

```go
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

- Return the error and let the Step retry policy retry. Never write an
  in-memory retry loop around Step work.
- `dex.RetryAfter(delay, cause)` sets the next retry delay. Use it only when the
  provider gives a meaningful delay, such as a rate-limit hint. It is not a
  polling loop.
- `dex.ErrorWithStack(err)` records the origin stack for diagnostics.
- Return every Context operation error. Do not continue as if a failed write
  committed.
- Use an idempotency key from durable identity for every external effect.

## Recovery Step

```go
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
- Keep a final branch for every other failure.
- A compensation Step is idempotent and carries enough input to find the
  original commit.
- A Flow-level soft timeout: implement `HandleTimeout(ctx dex.Context)
  (*dex.StepDecision, error)` on the Flow and set a positive
  `StartFlowOptions.Timeout`. It returns an explicit decision.
