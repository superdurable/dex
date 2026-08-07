# Failure Recovery

Two independent but related mechanisms: the step-API backoff retry, and the
failure-recovery policy that kicks in once those retries are exhausted. Let the
step retry a bounded number of times; if it still fails, move to a recovery step.

## Step retry and what happens after it is exhausted

Both step methods (`wait_for` and `execute`) retry on any failure. Without an
explicit policy there is no attempt limit, so a step can keep failing until the
flow times out. `RetryPolicy` fields:

- `initial_interval` — wait before the first retry.
- `maximum_interval` — cap on the wait between retries.
- `maximum_attempts` — retry count; `0` means infinite.
- `total_duration` — total retry window; `None` means infinite.
- `backoff_coefficient` — how fast the interval grows.

`maximum_attempts` or `total_duration` **must** be set for failure recovery to
run at all; otherwise the flow times out before recovery gets a chance. If both
are set, whichever is reached first ends the retries.

For `execute`, the recovery target is set with
`StepOptions(...).on_execute_failure_proceed_to(step)`. Without it, exhausting
retries fails the flow. For `wait_for`, `wait_for_failure=WaitForFailurePolicy.PROCEED`
lets the step run `execute` instead of failing the flow.

## Without and with the failure-recovery feature

Without it, every step needs its own attempt-counting branch:

```python
def execute(self, context: Context, input: None) -> StepDecision:
    try:
        self._do_execute()
    except RetriableError:
        if context.attempt > 5:
            return go_to(self.failure_recovery, None)
        raise
```

With it, the step body stays clean and the policy lives in the options:

```python
def get_step_options(self) -> StepOptions:
    return StepOptions(
        execute_retry=RetryPolicy(maximum_attempts=5),
    ).on_execute_failure_proceed_to(self.failure_recovery)

def execute(self, context: Context, input: None) -> StepDecision:
    return self._do_execute()
```

## The Saga pattern

The Saga pattern splits a transaction into steps that can be rolled back if any
of them fails, using compensations to return the system to its last known good
state. Payment processing and online purchases are the classic examples.

## This flow

A simplified e-commerce order: reduce the available quantity, then charge the
buyer. Both actions can fail, so both have recovery steps that roll back what
was already applied.

1. `UpdateItemQuantity` — reduces the stock count; on exhausted retries it
   proceeds to `UpdateQuantityRecovery`.
2. `ChargeForItems` — processes payment; on exhausted retries it proceeds to
   `VoidPaymentRecovery`.
3. `UpdateQuantityRecovery` — restores the stock count and fails the flow.
4. `VoidPaymentRecovery` — voids the payment, then moves to
   `UpdateQuantityRecovery`.
5. `DatabaseConnection` — stands in for a database that can fail to update.
6. `PaymentProcessor` — always fails, to force the flow into recovery.

## References

- [Saga pattern deep dive](https://medium.com/@qlong/saga-pattern-deep-dive-with-indeed-workflow-engine-b7e82c59e51f)
