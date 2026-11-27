# Failure handling patterns

Part of the [design patterns](../patterns.md), which hold the pseudocode conventions and the runnable examples.

## Failure handling

Dex already retries a failed method with backoff. Use these when retries alone are not enough. Compensation runs in reverse business order, is idempotent, and has its own failure route. Never catch an error only to report success.

### WaitFor failure recovery

A failure while establishing the wait is recoverable. Go folder: `primitives/proceed-on-wait-failure`.

```text
Lookup options: bounded WaitFor retry; WaitFor failure policy = proceed
Lookup.waitFor -> may fail (for example a check of Dex state or input, or a read-only, idempotent external call)
Lookup.execute(context, input)
    if context.waitForMethodFailed(): goTo(Recover, input)    // read context.recoveryError()
    else: normal path
```

Dex does not route directly to another Step. It runs the same Step's Execute with unchanged input, and Execute decides.

### Execute failure recovery

Compensate after Execute retries are exhausted. Go folder: `recovery`.

```text
UpdateQuantity.execute -> goTo(ChargePayment)
ChargePayment options: execute retry; execute failure route = VoidPayment
VoidPayment.execute    -> compensate; goTo(RestoreQuantity)       // reads context.recoveryError()
RestoreQuantity.execute -> compensate; forceFail("payment failed")
```

- A Step whose retries ran out may still have taken effect, for example when the provider applied the change and then timed out. Compensation starts with that Step, as `VoidPayment` does for `ChargePayment`: undo it with its original reference, and make every undo a no-op for an action that never happened.
- A recovery Step receives the failed Step's input unchanged, and gets the failure through the recovery error on the Context. Recovery Steps must be idempotent; they can be retried.
- Failing the Flow after compensation is a legitimate outcome.

### Fail fast on permanent errors

Stop retrying when a provider confirms that an error is permanent, such as a rejected input or a closed account.

- Execute classifies the provider error. A permanent error routes to a failure-handling Step or returns ForceFail. Only an error that can succeed on a later attempt goes to Step retry.
- When a read-only, idempotent provider call in WaitFor gets a permanent error, WaitFor records it in a Step execution local value and returns a Wait that is ready immediately, or the WaitFor failure policy proceeds to Execute; Execute routes on it. See [step.md](../step.md#waitfor-versus-execute).
- Runnable examples: [Go](https://github.com/superdurable/dex/tree/main/examples/go/patterns/fail-fast), [Python](https://github.com/superdurable/dex/tree/main/examples/python/dex_examples/patterns/fail-fast), [Java](https://github.com/superdurable/dex/tree/main/examples/java/src/main/java/io/superdurable/dex/patterns/failfast), [TypeScript](https://github.com/superdurable/dex/tree/main/examples/typescript/src/patterns/fail-fast), [Rust](https://github.com/superdurable/dex/tree/main/examples/rust/src/control_patterns/fail_fast).

### Manual recovery

Escalate an exhausted failure to a person. Go folder: `intervention`.

```text
DoWork options: bounded execute retry; execute failure route = AwaitOperator
AwaitOperator.waitFor -> AnyOf(Retry.ForOne(), Skip.ForOne())
AwaitOperator.execute -> if Retry consumed: goTo(DoWork)
                         else: forceFail("skipped by operator")
rpc retry -> Retry.publish();  rpc skip -> Skip.publish()
```

Persist the failure so the operator can see it, and make the decision durable through the Channel.

When a retry and a skip must not both count, the decision RPC checks that the work waits for a decision and claims it in the same commit, under a lock on that work's record, as in [Close a reply window](control-structures.md#close-a-reply-window-with-an-attribute-lock).

### Graceful timeout

Handle the overall Flow deadline with application logic. Go folder: `timeout`.

- The deadline belongs to the Flow, not a competing Step. Start the Flow with a positive timeout and the handler policy, and implement the Flow's timeout handler.
- The handler has Execute semantics and its own options. On success, Dex applies its staged writes, deletions, publications, and decision. If it exhausts its retries, the Flow fails, or it routes to a no-input recovery Step.
- Record a business outcome and perform bounded cleanup. See [flow.md](../flow.md#timeout-handler).
- Active Steps, including Steps that wait on a Channel or a Timer, keep running until the handler commits. ForceComplete or ForceFail then starts closing the Flow and cancels them, so those Steps need no deadline check of their own. GracefulComplete, or a GoTo to a Step that completes gracefully, waits until every other active Step ends. To start closing when the Timer fires, use the fail or cancel timeout policy.
- When no work may start after the deadline, use the fail or cancel timeout policy, or give the handler a short retry budget and let it withdraw work by its stable reference.
- When the handler must also report to another system and holds a lock an RPC takes, it records the outcome and moves to an unlocked Step that reports idempotently and then force-closes. Keep that handler SYNC, so the outcome is stored before the report.
