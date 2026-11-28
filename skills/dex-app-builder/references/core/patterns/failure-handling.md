# Failure handling patterns

Part of the [design patterns](../patterns.md), which hold the pseudocode conventions and the runnable examples.

## Failure handling

Dex already retries a failed method with backoff. Use these when retries alone are not enough. Compensation Steps are idempotent, because Dex retries them. Without a failure route, a compensation that exhausts its retries fails the Flow; add one when the Flow must record that outcome.

### WaitFor failure recovery

Derived from: [WaitFor and Execute](../../../SKILL.md#waitfor-and-execute).

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

Derived from: [WaitFor and Execute](../../../SKILL.md#waitfor-and-execute), [Commits](../../../SKILL.md#commits-and-isolation), [Workers](../../../SKILL.md#workers-attempts-and-time-bounds).

Compensate after Execute retries are exhausted. Go folder: `recovery`.

```text
UpdateQuantity.execute -> goTo(ChargePayment)
ChargePayment options: execute retry; execute failure route = VoidPayment
VoidPayment.execute    -> compensate; goTo(RestoreQuantity)       // reads context.recoveryError()
RestoreQuantity.execute -> compensate; forceFail("payment failed")
```

- A Step whose retries ran out may still have taken effect, for example when the provider applied the change and then timed out. When the failed Step made an external call, compensation starts with that Step, as `VoidPayment` does for `ChargePayment`: undo it with its original reference, and make every undo a no-op for an action that never happened.
- A recovery Step receives the failed Step's input unchanged, and gets the failure through the recovery error on the Context.
- Failing the Flow after compensation is a legitimate outcome.

### Fail fast on permanent errors

Derived from: [WaitFor and Execute](../../../SKILL.md#waitfor-and-execute), [Workers and time bounds](../../../SKILL.md#workers-attempts-and-time-bounds).

A returned error is retried until the budget runs out, even when the provider confirmed it is permanent. Route a confirmed permanent error instead, as the [suggestion](../../../SKILL.md#suggestions) says; for a permanent error in a WaitFor read, see [step.md](../step.md#waitfor-versus-execute).

Runnable examples: [Go](https://github.com/superdurable/dex/tree/main/examples/go/patterns/fail-fast), [Python](https://github.com/superdurable/dex/tree/main/examples/python/dex_examples/patterns/fail-fast), [Java](https://github.com/superdurable/dex/tree/main/examples/java/src/main/java/io/superdurable/dex/patterns/failfast), [TypeScript](https://github.com/superdurable/dex/tree/main/examples/typescript/src/patterns/fail-fast), [Rust](https://github.com/superdurable/dex/tree/main/examples/rust/src/control_patterns/fail_fast).

### Manual recovery

Derived from: [WaitFor and Execute](../../../SKILL.md#waitfor-and-execute), [Conditions](../../../SKILL.md#conditions-and-channel-messages), [Locks](../../../SKILL.md#repeated-execution-snapshots-and-locks).

Escalate an exhausted failure to a person. Go folder: `intervention`.

```text
DoWork options: bounded execute retry; execute failure route = AwaitOperator
AwaitOperator.waitFor -> AnyOf(Retry.ForOne(), Skip.ForOne())
AwaitOperator.execute -> if Retry consumed: goTo(DoWork)
                         else: forceFail("skipped by operator")
rpc retry -> Retry.publish();  rpc skip -> Skip.publish()
```

- The decision is durable through the Channel. When operators use the application's own UI, persist the failure in an Attribute that UI reads.
- Only the winning Channel consumes, so a losing retry or skip stays queued. When the work can fail again and that leftover decision must not decide the next failure, or when the RPC caller must learn whether its decision counted, the decision RPCs check that the work waits for a decision and claim it in the same commit, under a lock on that work's record, as in [Close a reply window](control-structures.md#close-a-reply-window-with-an-attribute-lock). Otherwise the RPCs only publish.

### Graceful timeout

Derived from: [Flow timeout](../../../SKILL.md#flow-timeout-completion-and-cancellation).

Handle the overall Flow deadline with application logic. Go folder: `timeout`.

- A fixed deadline for the whole Flow, known at start, is the Flow timeout, not a competing Step. A deadline that can move while the Flow runs is a Timer raced with the change Channel, or the [deadline watchdog](polling-and-timers.md#deadline-watchdog), because start options cannot change.
- Use the handler policy and a timeout handler when the Flow must record a business outcome or clean up; otherwise the fail or cancel policy is enough. See [flow.md](../flow.md#timeout-handler).
- Active Steps keep running while the handler runs, and its ForceComplete or ForceFail cancels them, so waiting Steps need no deadline check of their own. To start closing when the Timer fires, use the fail or cancel policy.
- When no new Step may start once Dex processes the timeout, use the fail or cancel timeout policy, or give the handler a short retry budget and let it withdraw work by its stable reference. The timeout can fire late, so a strict external cutoff is also enforced at admission or at the receiver.
- When the handler must also report to another system and holds a lock an RPC takes, it records the outcome and moves to an unlocked Step that reports idempotently and then force-closes. Keep that handler SYNC, so the outcome is stored before the report.
