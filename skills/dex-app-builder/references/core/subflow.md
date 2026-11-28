# SubFlow

A SubFlow is a child Flow run that a parent Step starts in WaitFor and waits on as a Condition. The child has its own identity, history, retries, and lifecycle. The parent's Execute reads the child's result.

A separate top-level Flow that a Step starts through a Client is not a SubFlow. See [flow.md](flow.md#own-downstream-starts-durably). A child job that needs its own business identity, such as a number people look it up, retry it, or stop it by, is such a top-level child Flow; see [flow.md](flow.md#child-jobs-with-their-own-identity).

## When to use a SubFlow

Usually prefer parallel Steps inside one Flow; see [Parallel Steps before SubFlows](../../SKILL.md#suggestions). Use a SubFlow when the child needs its own lifecycle, history, or retries, or when one fan-out is very large. Define the child's input, output, cancellation, and failure handling. Code reuse or provider abstraction alone is better served by ordinary language helpers.

## Shape

```text
ParentStep.waitFor(input)
    return AllOf(SubFlow(ChildFlow, childInput1), SubFlow(ChildFlow, childInput2))

ParentStep.execute(context, input)
    result1 = subFlowResult(context, 0)        // output and child Flow ID
    result2 = subFlowResult(context, 1)
    return gracefulComplete(combine(result1, result2))
```

- The child Flow type must be registered on the Worker, and its start Step input must match the input the parent passes.
- Use AllOf to wait for every child. Under AnyOf, the other children keep running after the wait ends. Execute can stop them or pass their Flow IDs on.
- When the fan-out grows with the data, bound the number of parallel children; active Step executions per run are [limited](../../SKILL.md#default-limits). Use Channels, a counter Attribute, and back pressure for a long-running parent. See [patterns.md](patterns/parallel.md#parallel-subflows).

## Identity and reuse

The child Flow ID derivation, the rule that a Step type starting a SubFlow must not contain `-`, the SubFlow reuse policy table, and reuse across later parent runs are in [SubFlows and child Flows](../../SKILL.md#subflows-and-child-flows).

- The application cannot choose the child Flow ID, so a child that needs its own business ID is a top-level child Flow.
- A Step that only waits on Timers or Channels may keep a `-` in its Step type. See [Step types that start SubFlows](https://docs.superdurable.io/reference/server/runtime-data#subflow-step-types).
- SubFlow options are the start options plus a SubFlow reuse policy.
- A parent retry never duplicates the child, because the generated Request ID matches. More detail is in [SubFlowReusePolicy](https://docs.superdurable.io/reference/server/runtime-data#subflow-reuse-policy) and [Later parent runs](https://docs.superdurable.io/reference/server/runtime-data#subflow-parent-lifecycle).

## Failures

- When Dex rejects a child start before the child begins, the parent Step's failure carries the rejection detail. Keep it visible in the recovery route.
- A child failure arrives as its result, not as a parent failure; check it.
- A child keeps running when its parent closes, and a SubFlow without its own Flow timeout can wait forever when no Worker takes its Steps; see [SubFlows and child Flows](../../SKILL.md#subflows-and-child-flows). When the child must end, stop it explicitly or set its Flow timeout.

## Self-check

- Does the child need its own lifecycle, history, or retries, or is the fan-out very large? Otherwise, would parallel Steps do?
- Is the child start input typed and matching the child's start Step?
- Are parent completion, child cancellation, failure, and duplicate submission defined?
- Is a fan-out that grows with the data bounded?
- Does every Step type that starts a SubFlow avoid `-`?
