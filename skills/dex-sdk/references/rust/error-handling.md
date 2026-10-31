# Rust error handling

Before designing sequential writes and reads, use the [shared read-after-write matrix](../core/read-after-write.md). A typed read-only RPC or `Client::get_attributes` call issued after a successful direct-state RPC write observes that write when it reads the written state; search indexes, Attribute Store projections and triggered business completion are separate. Follow this page for the language-specific error and timeout model.

Separate handler failures from controller/client failures. A Step or RPC returns `HandlerResult<T>` and uses `HandlerError` to control retryable Worker behavior. A Client operation returns `SdkResult<T>` and exposes typed `SdkError` variants. Do not collapse either into strings before policy or HTTP mapping has examined it.

## Handler failures and retries

Return errors with `?` when a durable read or write fails. For an application failure, construct a stable error type and useful message. `HandlerError::retry_after` overrides the next retry delay for that failure; `StepOptions::execute_retry` still bounds attempts. Use it only as explicit backoff for a transient failure. It is not a polling loop: an external “not ready yet” status is a normal branch of the [Polling pattern](patterns.md#polling), never an error. The excerpt below only demonstrates the delay override.

[Runnable source](https://github.com/superdurable/dex/blob/sdk-rust/v1.31.0/examples/rust/src/primitives/custom_retry/flow.rs)
<!-- dex-source: examples/rust/src/primitives/custom_retry/flow.rs -->
```rust
fn options(&self) -> StepOptions<Self::Input> {
    StepOptions::new().execute_retry(RetryPolicy::new().maximum_attempts(5))
}

fn execute(
    &self,
    context: &mut Context,
    ready_after_attempt: Self::Input,
) -> HandlerResult<StepDecision> {
    if i32::try_from(context.attempt()).unwrap_or(i32::MAX) < ready_after_attempt {
        return Err(HandlerError::retry_after(
            7,
            "CustomRetry",
            format!("not ready on attempt {}", context.attempt()),
        ));
    }
    Ok(StepDecision::graceful_complete(String::from("ready")))
}
```

Make Execute side effects idempotent across attempts. Dex retries a logical method execution; a remote service may have accepted the previous call even when the Worker did not observe the response.

## WaitFor versus Execute failure

Configure retry policies for the phase that can fail. WaitFor should normally be pure durable preparation. If WaitFor exhaustion is explicitly recoverable, set `WaitForFailurePolicy::Proceed`; Execute must then check `context.wait_for_method_failed()` before selecting a recovery transition.

[Runnable source](https://github.com/superdurable/dex/blob/sdk-rust/v1.31.0/examples/rust/src/primitives/proceed_on_wait_failure/flow.rs)
<!-- dex-source: examples/rust/src/primitives/proceed_on_wait_failure/flow.rs -->
```rust
fn options(&self) -> StepOptions<Self::Input> {
    StepOptions::new()
        .wait_for_failure(WaitForFailurePolicy::Proceed)
        .wait_for_retry(RetryPolicy::new().maximum_attempts(2))
}

fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
    Err(HandlerError::new(
        "ProceedOnWaitFailure",
        "planned WaitFor failure",
    ))
}
```

Never enable Proceed without a deliberate Execute branch; otherwise failed preparation is mistaken for successful readiness.

## Recovery choices

- Compensation Step: use when completed effects can be reversed deterministically.
- Manual recovery: wait on operator Channels after automatic retries exhaust.
- Graceful timeout handler: make a final bounded decision when the Flow deadline wins.
- Force fail: use when no safe continuation exists.
- Dead end: terminate only one branch; do not confuse it with failing the whole Flow.

Record recovery progress durably before triggering another non-idempotent action.

A recovery Step reads `Context::recovery_error()`. The Rust SDK has no time-limit predicates at its v1.20.0 baseline, so branch only on error types the application defines and never on a fixed Dex time-limit spelling; see [recovery error types](../core/error-handling.md#recovery-error-types).

## Client errors

Match `SdkError` variants that affect external behavior, such as `FlowAlreadyStarted`, `RequestTimeout`, `RpcLockConflict`, Channel message absence, or Worker invocation failure. Durable Step and Attribute waits never expose transport long-poll expiry; they reattach transparently and preserve the total request budget. `RequestTimeout` means that caller-visible budget expired, not that the Flow or the wait condition failed. Controllers should map known conflicts and invalid requests distinctly from infrastructure failures. Preserve `source()` chains in logs. Do not retry every `SdkError`; only retry operations whose semantics and request IDs make repetition safe.

Unlike the class-based SDKs, `SdkError` is one enum containing both service-backed variants and local `FlowDefinition`, `InvalidArgument`, `ValueMapping`, and `InvalidStepResult` defects. Never discard every `SdkError` to implement a remote-failure policy. Match the documented service variant for the operation, or use `service_error().is_some()` only at a narrow boundary that intentionally handles every remote variant identically.

When the API promises a critical operation has completed, follow the shared [required business milestone rule](../core/error-handling.md#wait-for-a-required-business-milestone): after start success or matching-request deduplication, call `Client::wait_for_attribute_match` or `Client::wait_for_step_completion` for the committed condition or the specific persistence Step execution. Publish that condition only after the business effect commits. Bound the wait; timeout does not undo acceptance or prove the operation failed. An acceptance-only response needs no wait.

For an idempotent start, set one stable `StartFlowOptions::request_id(...)` and `ignore_already_started(true)` only when a retry of that same logical request may attach to the existing run. A remaining `SdkError::FlowAlreadyStarted` means a different Request ID owns the Flow ID. Treat it as a domain conflict unless the coordinator contract deliberately redirects the command to that existing Flow. Another service-backed variant can leave acceptance unknown; retry with the same identities or read owning domain state, never a dedicated start-deduplication table.

For an explicitly best-effort external `Client::write_stream`, suppress only `SdkError::Service { .. }`, the remote fallback produced by that operation. Propagate local argument and value-mapping variants. Context Stream writes inside a handler remain `HandlerResult` work and must participate in the Step's retry or recovery policy.

## Flow close cleanup

From Rust SDK v1.20.0 or later, match `SdkError::FlowClosing { .. }` for a cleanup-sensitive Client call rejected with `ErrorSubStatus::FlowClosing` and FailedPrecondition. Wait for or re-read the retained terminal Flow state before deciding whether another action is needed. `SdkError` and `ErrorSubStatus` are non-exhaustive; include a wildcard arm in exhaustive matches. See the [affected calls and backend boundaries](../core/error-handling.md#flow-close-cleanup-rejection). [Released variants](https://github.com/superdurable/dex/blob/sdk-rust/v1.31.0/sdk-rust/crates/dex-sdk/src/sdk_error.rs)

## Indexed search limits

With server-zd `search.backend: postgres`, read the shared [indexed search limits](../core/error-handling.md#indexed-search-limits) first. At Rust SDK v1.31.0, a StartFlow or RPC rejected for an indexed Keyword value over 1,024 bytes returns `SdkError::Service { .. }` whose `ServiceError::code()` is `InvalidArgument` and whose `sub_status()` is `ErrorSubStatus::Uncategorized`. A Worker whose Attribute index registration exceeds capacity returns `SdkError::Service` from `start`, with the Server's FailedPrecondition status in its detail. The generated `dex_protocol::dex::ErrorSubStatus::AttributeIndexCapacityExceeded` and `IndexedKeywordTooLong` values exist only in the protocol bindings, and their enum is not the public `dex_sdk::ErrorSubStatus`; do not branch on them. A Flow that fails because a Step result carried such a value reports Flow error type `FlowErrorType::WorkerApiFailed` at Rust SDK v1.31.0. [Released Flow error type mapping](https://github.com/superdurable/dex/blob/sdk-rust/v1.31.0/sdk-rust/crates/dex-sdk/src/client.rs) Validate indexed Keyword lengths before the write. [Released Rust error mapping](https://github.com/superdurable/dex/blob/sdk-rust/v1.31.0/sdk-rust/crates/dex-sdk/src/sdk_error.rs)

## Closed-Flow races

Follow the shared [closed-Flow race rule](../core/error-handling.md#closed-flow-races). `SdkError::FlowNotActiveOrNotFound` from a mutation says it found no active target; it does not say the requested action succeeded. First reconcile from authoritative domain state and operation invariants. Call `client.describe_flow(flow_id)` only when an otherwise unknown terminal distinction changes the outcome. It is a blocking call, so keep it off the async executor like other Client calls. It reads the current or latest run without waiting for completion. It returns `SdkResult<FlowInfo>` with `flow_id`, `run_id`, `flow_type`, `status`, and `started_at`; it has no close time. Treat `FlowStatus::Completed` as idempotent success only when successful completion guarantees the requested condition. For another terminal status or `SdkError::FlowNotFound { .. }`, return an explicit domain failure or unknown outcome. If the Flow still appears running, query its domain state before a bounded, idempotent retry. [Released Client](https://github.com/superdurable/dex/blob/sdk-rust/v1.31.0/sdk-rust/crates/dex-sdk/src/client.rs) and [FlowInfo](https://github.com/superdurable/dex/blob/sdk-rust/v1.31.0/sdk-rust/crates/dex-sdk/src/flow_info.rs)

## Query-only Get failures

Follow the shared [missing query target rule](../core/error-handling.md#missing-query-targets). For a business Get confirmed to have no registration or invocation locks, no transaction, no returned durable effects, and no Server policy that runs every RPC transactionally, catch `SdkError::FlowNotActiveOrNotFound` or `SdkError::FlowNotFound` and return the contract's not-found result directly. Retained closed executions remain readable, so do not call WaitForFlow, describe/search/history APIs, or add a timeout probe or retry to distinguish missing from closed. Preserve other errors and any explicit retention/unavailable contract. Do not apply this translation to mutations or active-only RPC paths.

A business Get that reads with `Client::get_attributes` translates `SdkError::FlowNotFound` the same way. That call never returns `SdkError::FlowNotActiveOrNotFound`, because a closed run stays readable until purge. Let `FlowDefinition`, `ValueMapping`, and `InvalidArgument` stay visible as local defects. One exception: a `ValueMapping` from `get_attributes` for a large value that could not be resolved is retryable, because a purge or a concurrent overwrite between the read and the large-value resolution also causes it. Retry the call once to read fresh state; a repeated failure is a defect. See [Client Attribute reads](primitives.md#client-attribute-reads) for the full error list.

## Terminal decisions

`graceful_complete` waits for the Flow's branch semantics; `force_complete` terminates immediately; `force_fail` fails immediately. Assert the intended Flow status in integration tests. A successful method return is not evidence that buffered Stream output or unrelated sibling work has finished.
