# Python error handling

Python idioms for the rules in [error handling](../core/error-handling.md):
error layers, start identity, closed-Flow races, recovery, and retry
ownership. Read the core page before you design a Client boundary.

## Error layers

| Layer | Python form | Who handles it |
| --- | --- | --- |
| Application failure in `wait_for`, `execute`, an RPC, or `handle_timeout` | any raised exception | Dex: Step retry, then the failure route or Flow failure |
| Dex outcome of a Client call | a `DexServiceError` subclass | the caller, by type |
| Local definition or mapping defect | `FlowDefinitionError`, `ValueMappingError`, `ValueError`, `TypeError` | fix the code; do not retry |
| Caller cancellation or deadline | `asyncio.CancelledError`, `TimeoutError` | the caller |

Catch the narrowest documented type. Never parse `str(error)`. Let unexpected
defects keep their traceback.

| Exception | Meaning |
| --- | --- |
| `FlowAlreadyStartedError` | The Flow ID is taken by a start Dex could not match to this request |
| `FlowNotFoundError` | No Flow with that ID, or its latest run is being purged |
| `FlowNotActiveOrNotFoundError` | No active run accepted the call; it says nothing about whether the action happened |
| `FlowClosingError` | The Flow is closing; re-read its retained terminal state before acting again |
| `RpcLockConflictError` | Lock contention; retry the call with bounded backoff |
| `LongPollTimeoutError` | A long poll expired; the Flow is still running; call again |
| `RequestTimeoutError` | The caller's total request budget expired; the Flow did not fail |
| `WorkerInvocationError` | The Worker handler failed; see `worker_error_type` and `worker_error_detail` |
| `ChannelMessageNotFoundError` | A pending message ID was already consumed or deleted |
| `AttributeMapNotLoadedError`, `ChannelMessagesNotLoadedError` | The handler read a map instance or pending messages it did not load |
| `DexServiceError` | Base of every remote Dex outcome; has `code`, `sub_status`, `detail`, `operation`, `flow_id` |

`DexServiceError` subclasses `RuntimeError`, but so do local SDK defects.
Catching `RuntimeError` is never a substitute for a specific type. Catch
`DexServiceError` itself only at a narrow boundary whose policy treats every
remote outcome the same, such as a best-effort external `write_stream`.

## Start a Flow idempotently

```python
from dataclasses import dataclass

from dex import (
    AsyncClient, Flow, FlowAlreadyStartedError, IdReusePolicy, StartFlowOptions,
)


@dataclass(frozen=True)
class SignupRequest:
    request_id: str
    email: str


class SignupConflictError(Exception):
    pass


async def start_signup(
    client: AsyncClient, flow: Flow[SignupRequest], request: SignupRequest
) -> str:
    options = StartFlowOptions(
        request_id=f"signup-{request.request_id}",
        ignore_already_started=True,
        id_reuse_policy=IdReusePolicy.DISALLOW,
    )
    try:
        return await client.start_flow(flow, f"signup-{request.email}", request, options)
    except FlowAlreadyStartedError as error:
        raise SignupConflictError(request.email) from error
```

- Call `start_flow` first. Do not preflight with an RPC, `describe_flow`, or a
  search to avoid `FlowAlreadyStartedError`.
- Bind `request_id` to the complete immutable request, and keep it and
  `ignore_already_started=True` identical on every retry. Dex compares only the
  request ID, not the input.
- `request_id` alone still raises on a retry; `ignore_already_started` alone
  does not hide a different request. A `None` request ID gets a fresh one per
  call.
- A remaining `FlowAlreadyStartedError` is a domain conflict unless the
  contract lets the existing Flow serve this request. Only then, read its
  state with a typed read RPC or `get_attributes` in that branch.
- Another `DexServiceError` can leave acceptance unknown. Retry with the same
  identities, or read the owning domain state. Never add a separate
  start-deduplication table.
- When the API promises the work is done, not just accepted, follow the start
  with a bounded `wait_for_attribute_match` or `wait_for_step_completion`. A
  timeout there does not undo the start.
- Dependent starts belong to a Step with an injected Client and stable
  downstream identities, not to a retry loop in the HTTP handler.

## Closed-Flow races

`FlowNotActiveOrNotFoundError` from a mutation means no active run took it. It
does not mean the action happened.

1. Reconcile from authoritative domain state and the operation's invariants.
2. Only when a terminal distinction changes the answer, call
   `describe_flow(flow_id)`. `FlowInfo` has `flow_id`, `run_id`, `flow_type`,
   `status`, and `started_at`.
3. Treat `FlowStatus.COMPLETED` as success only when completion guarantees the
   requested condition. Another terminal status or `FlowNotFoundError` is an
   explicit domain failure or unknown outcome.
4. If the Flow still runs, re-read domain state before one bounded,
   idempotent retry.

## Query-only reads

- A read RPC with no locks, no transaction, and no returned effects can serve a
  retained closed run. Catch `FlowNotActiveOrNotFoundError` or
  `FlowNotFoundError` there and return the contract's not-found result
  directly. Do not probe with `wait_for_flow`, history, or search.
- `get_attributes` never raises `FlowNotActiveOrNotFoundError`. Translate its
  `FlowNotFoundError` the same way. Let `FlowDefinitionError`,
  `ValueMappingError`, `ValueError`, and `TypeError` surface as defects.
- Never apply this translation to a mutation or an active-only RPC.

## Recovery and attempt progress

```python
from dex import Context, Step, StepAttemptProgress, StepDecision, force_fail, graceful_complete


class ReconcilePayment(Step[SignupRequest]):
    def execute(self, context: Context, input: SignupRequest) -> StepDecision:
        failure = context.recovery_error
        if failure is None:
            return force_fail("recovery Step reached without a failure")
        if failure.attempt_progress is StepAttemptProgress.NOT_STARTED:
            return graceful_complete("payment never attempted")
        return graceful_complete(f"reconcile payment for {input.request_id}")
```

- `context.recovery_error` is `RecoveryErrorInfo` with `detail`,
  `error_type`, and `attempt_progress`. It is `None` outside a recovery path.
- Only `NOT_STARTED` proves no handler ran, and only SYNC methods report it.
  Treat `NOT_COMPLETED` and `UNSPECIFIED` as possibly run: reconcile external
  effects before you repeat them.
- Python has no time-limit predicates. Never compare `error_type` with a
  fixed Dex string; branch only on error types your application raises.
- A recovery Step carries enough durable input to identify the original
  commit, and its compensation is idempotent.

## Retry ownership

- Raise to let Step retry decide. Never wrap Step work in an in-memory retry
  loop; it dies with the Worker and hides attempts.
- `raise retry_after(seconds, cause)` only when the application knows a
  meaningful delay, such as a provider rate-limit hint. "Not ready yet" is a
  polling branch, not an error.
- Derive idempotency keys for external calls from the Flow ID and Step
  execution ID. Persist the effect, or move to a recovery Step, before you
  acknowledge it externally.
- Every Context call can raise. Let it propagate; never continue as if a
  failed write, delete, heartbeat, or Stream output committed.
- Do not catch `asyncio.CancelledError` to keep doing work after cancellation.
- In a sync generator, every heartbeat and Stream output is yielded; a
  swallowed output never reaches Dex.

## Client Attribute read errors

| Exception | Cause |
| --- | --- |
| `FlowNotFoundError` | No Flow, or the latest run is being purged; a closed run is still readable |
| `FlowDefinitionError` | No registered Flow declares a matching definition (raised before any request) |
| `ValueError` | Empty Flow ID, no reads, a duplicate read, or an invalid server response |
| `TypeError` | A read that is not an `Attribute` or `AttributeMapInstance`, such as a bare `AttributeMap` |
| `ValueMappingError` | A stored value cannot be decoded as its value type |
| `DexServiceError` | Another service or large-value load failure |

On any error the call returns no values.
