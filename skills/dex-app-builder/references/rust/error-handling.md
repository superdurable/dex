# Rust error handling and recovery

Read [../core/error-handling.md](../core/error-handling.md) first for the
rules. This page maps them to Rust. Keep three kinds of failure apart:

| Kind | Where | Rust form |
| --- | --- | --- |
| Business outcome | Step `execute` | Return `Ok(StepDecision::...)` (`force_fail`, a recovery `go_to`, a result) |
| Retryable handler failure | Step or RPC handler | Return `Err(HandlerError)` |
| Client or service failure | Code that calls `Client` | Match `SdkError` variants |

Handlers return `HandlerResult<T>`; Client calls return `SdkResult<T>`. Do not
turn either into a string before your policy has inspected it.

## Handler failures

- `Err(HandlerError)` from `wait_for` or `execute` fails that attempt. Dex
  discards every Attribute write and Channel publish the attempt staged, then
  retries according to `wait_for_retry` or `execute_retry`.
- Build errors with a stable error type:
  `HandlerError::new("PaymentDeclined", message)`, or
  `HandlerError::from_error(error)` to keep a library error's type name.
  Recovery Steps see that type.
- There is no `From<SdkError>` or blanket `From<E>` for `HandlerError`. Use
  `.map_err(HandlerError::from_error)?` on `SdkResult` and library results.
  `Context` and definition methods already return `HandlerResult`.
- Never swallow an error to return success. That commits partial state and
  hides the retry.
- An external call may succeed even when the attempt fails afterward. Make
  every external effect idempotent: derive a key from durable data, such as
  `format!("{}-charge", context.flow_id())`, or record a receipt.
- An expected "not ready" or business rejection is not an error. Model it as a
  loop branch, a Timer, or a decision.
- `HandlerError::retry_after(seconds, error_type, message)` sets the delay
  before the next retry. Retry limits still apply. It is not a polling loop.

```rust
pub fn call_rate_limited_provider(context: &Context) -> HandlerResult<StepDecision> {
    if provider_is_rate_limited() {
        return Err(HandlerError::retry_after(
            30,
            "ProviderRateLimited",
            format!("rate limited on attempt {}", context.attempt()),
        ));
    }
    Ok(StepDecision::dead_end())
}
```

## Recovery Steps

When Execute retries run out, `on_execute_failure_proceed_to(&Recovery)` runs a
registered recovery Step with the same input. Without a route, the Flow fails.

```rust
#[derive(Default)]
struct RecordChargeFailure;

impl Step for RecordChargeFailure {
    type Input = OrderRequest;

    fn execute(&self, context: &mut Context, order: Self::Input) -> HandlerResult<StepDecision> {
        let failure = context
            .recovery_error()
            .ok_or_else(|| HandlerError::new("MissingRecoveryError", "no recovery error"))?;
        let may_have_charged = !matches!(failure.attempt_progress(), StepAttemptProgress::NotStarted);
        let detail = format!("{}: {}", failure.error_type(), failure.detail());
        if may_have_charged {
            let idempotency_key = format!("{}-charge", context.flow_id());
            refund_if_charged(&idempotency_key).map_err(HandlerError::from_error)?;
        }
        ORDER_STATUS.set(context, format!("charge failed: {detail}"))?;
        Ok(StepDecision::force_fail(format!("order {} not charged", order.order_id)))
    }
}
```

- `context.recovery_error()` returns `Option<&RecoveryErrorInfo>` with
  `error_type()`, `detail()`, and `attempt_progress()`.
- `StepAttemptProgress::NotStarted` guarantees no handler attempt ran. Only
  SYNC Steps can report it. `NotCompleted` and `Unspecified` mean an attempt
  may have run; reconcile external effects before repeating them. An ASYNC
  Step always reports `NotCompleted`. The enum is non-exhaustive, so test for
  `NotStarted` and treat everything else as "may have run".
- Never branch on Dex time-limit error spellings. Branch on your own error
  types and on attempt progress.
- A recovery Step must be idempotent and must work after a partial external
  success. Persist the facts it needs before the risky call.
- Bound retries by attempts or `total_duration` so recovery runs in time.

### WaitFor failure

`wait_for_failure(WaitForFailurePolicy::Proceed)` runs `execute` even when
`wait_for` exhausts its retries. Then `execute` must check
`context.wait_for_method_failed()` first; otherwise a failed preparation looks
like readiness.

```rust
fn execute(&self, context: &mut Context, job: Self::Input) -> HandlerResult<StepDecision> {
    if context.wait_for_method_failed() {
        return Ok(StepDecision::force_fail(format!("could not prepare {job}")));
    }
    Ok(StepDecision::graceful_complete(job))
}
```

Manual recovery, Flow deadlines, and `dead_end()` versus `force_fail` are in
[../core/error-handling.md](../core/error-handling.md).

## Cancellation

Cancellation is cooperative and does not undo external effects. Long loops
check `context.is_cancelled()` between bounded units of work and return
promptly, for example with `StepDecision::dead_end()`. A canceled attempt's
late decision, writes, retries, and recovery are discarded. Never block a
single call indefinitely; give it a timeout.

## Client errors

`SdkError` is one non-exhaustive enum. It holds both remote failures and local
defects, so never treat every `SdkError` as a remote failure.

| Variant | Meaning | Typical handling |
| --- | --- | --- |
| `FlowAlreadyStarted` | The Flow ID is taken by another request | Domain conflict, unless the contract redirects to the existing Flow |
| `FlowNotFound` | No such Flow | Not found |
| `FlowNotActiveOrNotFound` | No active target for this call | See [Closed-Flow races](#closed-flow-races) |
| `FlowClosing` | The Flow is closing | Re-read the terminal state, then decide |
| `RpcLockConflict` | RPC locks are held | Retry with backoff if the RPC is idempotent |
| `WorkerInvocation` | The RPC handler failed; `worker` holds its error type and detail | Map to the handler's business error |
| `ChannelMessageNotFound` | The message is no longer pending | Treat as already consumed |
| `RequestTimeout` | The caller's wait budget expired | Outcome unknown; the Flow keeps running |
| `LongPollTimeout` | A bounded long poll ended while the Flow runs | Wait again or report pending |
| `Service` | Any other remote failure | Keep `service.code()` and `sub_status()` for logs |
| `FlowDefinition`, `InvalidArgument`, `ValueMapping`, `InvalidStepResult` | Local defect in code, definitions, or data | Surface; do not retry |

```rust
pub fn classify(error: &SdkError) -> &'static str {
    match error {
        SdkError::FlowAlreadyStarted { .. } => "conflict",
        SdkError::RpcLockConflict { .. } => "retry-later",
        SdkError::RequestTimeout { .. } => "still-pending",
        SdkError::FlowDefinition { .. }
        | SdkError::InvalidArgument { .. }
        | SdkError::ValueMapping { .. } => "local-defect",
        other
            if other
                .service_error()
                .is_some_and(|service| service.code() == GrpcCode::Unavailable) =>
        {
            "unavailable"
        }
        _ => "internal",
    }
}
```

- `error.service_error()` returns the `ServiceError` of any remote variant,
  with `code()`, `sub_status()`, `detail()`, and `operation()`. Use it only at
  a boundary that treats every remote failure alike.
- Always include a wildcard arm. `ErrorSubStatus` is non-exhaustive too.
- Never branch on detail text. Keep `source()` chains in logs.

## Idempotent start

```rust
pub fn start_order(client: &Client, request: OrderRequest, request_id: &str) -> SdkResult<String> {
    let flow_id = format!("order-{}", request.order_id);
    let options = StartFlowOptions::new()
        .request_id(request_id)
        .ignore_already_started(true)
        .timeout(Duration::from_secs(7 * 24 * 3600));
    client.start_flow_with_options(&OrderFlow::default(), &flow_id, request, options)
}
```

- One stable request ID per logical request, with `ignore_already_started(true)`. A remaining
  `SdkError::FlowAlreadyStarted` means another request owns the Flow ID. Rules for unknown
  acceptance are in [../core/error-handling.md](../core/error-handling.md).

## Waiting for a business milestone

When the API promises an effect happened, wait with a request timeout on
an Attribute the Flow writes after the effect commits, or on the Step's
completion. See [../core/error-handling.md](../core/error-handling.md).

## Query-only reads

For a business Get whose RPC has no locks, no transaction, and no returned
effects, a missing Flow and a closed Flow need no lifecycle probe:

```rust
pub fn get_status_or_not_found(client: &Client, flow_id: &str) -> SdkResult<Option<String>> {
    match client.invoke_rpc_without_input(flow_id, GET_ORDER_STATUS) {
        Ok(status) => Ok(Some(status)),
        Err(SdkError::FlowNotActiveOrNotFound { .. } | SdkError::FlowNotFound { .. }) => Ok(None),
        Err(error) => Err(error),
    }
}
```

`get_attributes` returns only `SdkError::FlowNotFound` for a missing Flow; a
closed Flow stays readable until purge. Do not apply this translation to
mutations or to RPCs that need an active Flow.

## Closed-Flow races

`SdkError::FlowNotActiveOrNotFound` from a mutation says no active target took
the call. It does not say the action happened. Reconcile from domain state
first. Call `describe_flow` only when the terminal status changes the answer.

```rust
pub fn cancel_order(client: &Client, flow_id: &str) -> Result<CancelOutcome, SdkError> {
    match client.invoke_rpc_without_input(flow_id, CANCEL_ORDER) {
        Ok(()) => Ok(CancelOutcome::Canceled),
        Err(SdkError::FlowNotActiveOrNotFound { .. }) => match client.describe_flow(flow_id) {
            Ok(info) if info.status == FlowStatus::Completed => Ok(CancelOutcome::AlreadyFinished),
            Ok(_) | Err(SdkError::FlowNotFound { .. }) => Ok(CancelOutcome::Unknown),
            Err(error) => Err(error),
        },
        Err(SdkError::FlowClosing { .. }) => Ok(CancelOutcome::Unknown),
        Err(error) => Err(error),
    }
}
```

- The full reconcile rules are in
  [../core/error-handling.md](../core/error-handling.md).

## Best-effort Stream writes

An external `client.write_stream` that may be lost suppresses only
`SdkError::Service`, logs the Flow ID and code without the payload, and
continues. A disabled or full Stream Store also arrives this way. Local
variants still surface. A Stream write inside a handler is ordinary
`HandlerResult` work under the Step's retry policy.

```rust
match client.write_stream(flow_id, &ORDER_PROGRESS, "api", message) {
    Ok(()) => Ok(()),
    Err(SdkError::Service { service }) => {
        tracing::warn!(flow_id, code = ?service.code(), "progress write dropped");
        Ok(())
    }
    Err(error) => Err(error),
}
```

## Checklist

- [ ] Business outcomes are decisions; only retryable failures return `Err`.
- [ ] Errors carry stable types; library errors use `HandlerError::from_error`.
- [ ] External effects carry durable idempotency keys.
- [ ] Retries are bounded where a recovery route must run.
- [ ] Recovery reads attempt progress and reconciles before repeating effects.
- [ ] `Proceed` WaitFor policy has a `wait_for_method_failed()` branch.
- [ ] `SdkError` matches have a wildcard arm; local defects stay visible.
- [ ] Starts use a stable request ID; milestone waits have a timeout.
- [ ] Logs carry Flow ID, run ID, Step execution ID, and attempt; never secrets or large payloads.
