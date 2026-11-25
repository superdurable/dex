# Python error handling

Python exceptions and idioms for the rules in
[error handling](../core/error-handling.md).

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

- Pass the same `request_id` and `ignore_already_started=True` on every
  retry of one logical start. A `None` request ID gets a fresh one per call.
- Catch `FlowAlreadyStartedError` after `start_flow`; never preflight with an
  RPC, `describe_flow`, or `search_flows`. The decision rules are in
  [error handling](../core/error-handling.md).
- For a completed-work promise, follow the start with a bounded
  `wait_for_attribute_match` or `wait_for_step_completion`.

## Closed-Flow races

Catch `FlowNotActiveOrNotFoundError` from a mutation, then reconcile as
[error handling](../core/error-handling.md) describes. When the terminal status
matters, call `describe_flow(flow_id)`; `FlowInfo` has `flow_id`, `run_id`,
`flow_type`, `status`, and `started_at`. Compare `status` with
`FlowStatus.COMPLETED` and other `FlowStatus` members.

## Query-only reads

- At a confirmed query-only RPC boundary, catch `FlowNotActiveOrNotFoundError`
  or `FlowNotFoundError` and return the not-found result directly.
- `get_attributes` never raises `FlowNotActiveOrNotFoundError`; translate its
  `FlowNotFoundError` the same way. Let `FlowDefinitionError`,
  `ValueMappingError`, `ValueError`, and `TypeError` surface as defects.

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
- Compare `attempt_progress` with `StepAttemptProgress.NOT_STARTED`; treat
  `NOT_COMPLETED` and `UNSPECIFIED` as possibly run.
- Python has no time-limit predicates. Never compare `error_type` with a
  fixed Dex string; branch only on error types your application raises.

## Retry ownership

- Raise to let Step retry decide.
- `raise retry_after(seconds, cause)` sets the next retry delay when the
  application knows it, such as a provider rate-limit hint.
- Every Context call can raise. Let it propagate.
- Do not catch `asyncio.CancelledError` to keep working after cancellation.
- In a sync generator, yield every heartbeat and Stream output; a swallowed
  output never reaches Dex.

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
