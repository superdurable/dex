# Java error handling and recovery

Read [../core/error-handling.md](../core/error-handling.md) first for the
rules. This page maps them to Java. Keep three kinds of failure apart:

| Kind | Where | Java form |
| --- | --- | --- |
| Business outcome | Step `execute` | Return a `StepDecision` (`forceFail`, a recovery `goTo`, a result DTO) |
| Retryable handler failure | Step or RPC handler | Throw an exception |
| Client or service failure | Code that calls `Client` | Catch a concrete exception from `io.superdurable.dex.exceptions` |

## Handler failures

- Throwing from `waitFor` or `execute` fails that attempt. Dex discards every
  Attribute write and Channel publish the attempt staged, then retries
  according to `waitForRetry` or `executeRetry`.
- The recovery error type Dex records is the thrown exception's fully
  qualified class name, from `getClass().getName()`. Use your own exception
  classes when recovery must branch on the cause.
- Never catch an exception only to return a success decision. That commits
  partial state and hides the retry.
- An external call may succeed even when the attempt fails afterward. Make
  every external effect idempotent: derive a key from durable data, such as
  `context.getFlowId() + "-charge"`, or record a receipt before moving on.
- An expected "not ready" or business rejection is not an exception. Model it
  as a loop branch, a Timer, or a decision.
- To set the delay before the next retry, throw
  `RetryAfterException.after(delay, cause)`. Retry limits still apply.

```java
@Override
public StepDecision execute(Context context, String request) {
    try {
        return StepDecision.gracefulComplete(callProvider(request));
    } catch (RateLimitedException limited) {
        throw RetryAfterException.after(Duration.ofSeconds(30), limited);
    }
}
```

## Recovery Steps

When Execute retries run out, `onExecuteFailureProceedTo(Recovery.class)` runs
a registered recovery Step with the same input. Without a route, the Flow
fails.

```java
final class RecordChargeFailure implements Step<OrderRequest> {
    @Override
    public Class<OrderRequest> getInputType() {
        return OrderRequest.class;
    }

    @Override
    public StepDecision execute(Context context, OrderRequest order) {
        RecoveryErrorInfo failure = context.getRecoveryError();
        if (failure.getAttemptProgress() != StepAttemptProgress.NOT_STARTED) {
            payments.refundIfCharged(context.getFlowId() + "-charge");
        }
        orderStatus.set(context, "charge failed: " + failure.getErrorType());
        return StepDecision.forceFail("order " + order.orderId() + " not charged");
    }
}
```

- `context.getRecoveryError()` gives `getErrorType()`, `getDetail()`, and
  `getAttemptProgress()`.
- `StepAttemptProgress.NOT_STARTED` guarantees no handler attempt ran. Only
  SYNC Steps can report it. `NOT_COMPLETED` and `UNSPECIFIED` mean an attempt
  may have run; reconcile external effects before repeating them. An ASYNC
  Step always reports `NOT_COMPLETED`.
- Never branch on Dex time-limit error spellings. Branch on your own error
  types and on attempt progress.
- A recovery Step must be idempotent and must work after a partial external
  success. Persist the facts it needs before the risky call.
- Bound retries by attempts or `totalDuration` so recovery runs in time.

### WaitFor failure

`waitForFailure(WaitForFailurePolicy.PROCEED)` runs `execute` even when
`waitFor` exhausts its retries. Then `execute` must check
`context.waitForMethodFailed()` first; otherwise a failed preparation looks
like readiness.

```java
@Override
public StepDecision execute(Context context, String job) {
    if (context.waitForMethodFailed()) {
        return StepDecision.forceFail("could not prepare " + job);
    }
    return StepDecision.gracefulComplete(job);
}
```

Manual recovery, Flow deadlines, and `deadEnd()` versus `forceFail` are in
[../core/error-handling.md](../core/error-handling.md).

## Cancellation

Cancellation is cooperative and does not undo external effects.

- Loops check `context.isCancellationRequested()` between bounded units.
- Dex interrupts a canceled Java handler. On `InterruptedException`, call
  `Thread.currentThread().interrupt()` and return promptly, for example with
  `StepDecision.deadEnd()`.
- A canceled attempt's late decision and writes are discarded.

## Client exceptions

Every remote failure extends `DexServiceException`. Local failures do not:
`IllegalArgumentException`, `FlowDefinitionException`,
`ValueMappingException`, and `IllegalStateException` signal a code or data
defect and must stay visible.

| Exception | Meaning | Typical handling |
| --- | --- | --- |
| `FlowAlreadyStartedException` | The Flow ID is taken by another request | Domain conflict, unless the contract redirects to the existing Flow |
| `FlowNotFoundException` | No such Flow (read or wait) | Not found |
| `FlowNotActiveOrNotFoundException` | No active target for this call | See [Closed-Flow races](#closed-flow-races) |
| `FlowClosingException` | The Flow is closing | Re-read the terminal state, then decide |
| `RpcLockConflictException` | RPC locks are held | Retry with backoff if the RPC is idempotent |
| `WorkerInvocationException` | The RPC handler threw | Map to the handler's business error |
| `ChannelMessageNotFoundException` | The message is no longer pending | Treat as already consumed |
| `RequestTimeoutException` | The caller's wait budget expired | Outcome unknown; the Flow keeps running |
| `LongPollTimeoutException` | A bounded `waitForFlow` ended while running | Wait again or report pending |
| `DexServiceException` | Any other remote failure | Keep `getCode()` and `getSubStatus()` for logs |

- Catch the concrete classes whose outcome your code can decide.
- Catch `DexServiceException` only at a boundary that treats every remote
  failure the same, such as availability translation or a best-effort write.
  Never catch `RuntimeException` for that; it hides local defects.
- Never branch on message text or numeric codes.

## Idempotent start

```java
public String startOrder(OrderRequest request, String requestId) {
    String flowId = "order-" + request.orderId();
    return client.startFlow(orderFlow, flowId, request, StartFlowOptions.newBuilder()
            .requestId(requestId)
            .ignoreAlreadyStarted(true)
            .timeout(Duration.ofDays(7))
            .build());
}
```

- One stable request ID per logical request, with `ignoreAlreadyStarted(true)`. A remaining
  `FlowAlreadyStartedException` means another request owns the Flow ID. Rules for unknown
  acceptance are in [../core/error-handling.md](../core/error-handling.md).

## Waiting for a business milestone

When the API promises an effect happened, wait with a request timeout on
an Attribute the Flow writes after the effect commits, or on the Step's
completion. See [../core/error-handling.md](../core/error-handling.md).

## Query-only reads

For a business Get whose RPC has no locks, no transaction, and no returned
effects, a missing Flow and a closed Flow need no lifecycle probe. Translate
not-found directly:

```java
public Optional<String> getStatusByRpc(String flowId) {
    OrderFlow stub = client.newRpcStub(OrderFlow.class, flowId);
    try {
        return Optional.of(client.invokeRPC(stub::getOrderStatus));
    } catch (FlowNotActiveOrNotFoundException | FlowNotFoundException missing) {
        return Optional.empty();
    }
}
```

`client.getAttributes` throws only `FlowNotFoundException` for a missing Flow;
a closed Flow stays readable until purge. Do not apply this translation to
mutations or to RPCs that need an active Flow.

## Closed-Flow races

`FlowNotActiveOrNotFoundException` from a mutation says no active target took
the call. It does not say the action happened. Reconcile from domain state
first. Call `describeFlow` only when the terminal status changes the answer.

```java
public CancelOutcome cancelOrder(String flowId) {
    OrderFlow stub = client.newRpcStub(OrderFlow.class, flowId);
    try {
        client.invokeRPC(stub::cancelOrder);
        return CancelOutcome.CANCELED;
    } catch (FlowClosingException closing) {
        return CancelOutcome.UNKNOWN;
    } catch (FlowNotActiveOrNotFoundException notActive) {
        try {
            FlowInfo info = client.describeFlow(flowId);
            return info.getStatus() == FlowStatus.COMPLETED
                    ? CancelOutcome.ALREADY_FINISHED
                    : CancelOutcome.UNKNOWN;
        } catch (FlowNotFoundException missing) {
            return CancelOutcome.UNKNOWN;
        }
    }
}
```

- The full reconcile rules are in
  [../core/error-handling.md](../core/error-handling.md).

## Best-effort Stream writes

An external `client.writeStream` that may be lost catches
`DexServiceException`, logs the Flow ID and status code without the payload,
and continues. A disabled or full Stream Store also arrives this way. Local
exceptions still surface. Never rebuild business state from a Stream.

```java
try {
    client.writeStream(flowId, orderFlow.progress, "api", message);
} catch (DexServiceException dropped) {
    System.getLogger("orders").log(
            System.Logger.Level.WARNING,
            "progress write dropped for " + flowId + ": " + dropped.getCode());
}
```

## Checklist

- [ ] Business outcomes are decisions; only retryable failures throw.
- [ ] External effects carry durable idempotency keys.
- [ ] Retries are bounded where a recovery route must run.
- [ ] Recovery reads attempt progress and reconciles before repeating effects.
- [ ] `PROCEED` WaitFor policy has a `waitForMethodFailed()` branch.
- [ ] Interrupts restore the flag; loops check cancellation.
- [ ] Client code catches concrete exceptions; local defects stay visible.
- [ ] Starts use a stable request ID; milestone waits have a timeout.
- [ ] Logs carry Flow ID, run ID, Step execution ID, and attempt; never secrets or large payloads.
