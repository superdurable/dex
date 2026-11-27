# SubFlow

A SubFlow is a child Flow run that a parent Step starts in WaitFor and waits on as a Condition. The child has its own identity, history, retries, and lifecycle. The parent's Execute reads the child's result.

A separate top-level Flow that a Step starts through a Client is not a SubFlow. See [flow.md](flow.md#own-downstream-starts-durably). A child job that needs its own business identity, such as a number people look it up, retry it, or stop it by, is such a top-level child Flow; see [flow.md](flow.md#child-jobs-with-their-own-identity).

## When to use a SubFlow

Prefer parallel Steps inside one Flow. Use a SubFlow when the child needs its own lifecycle, history, or retries, or when one fan-out is very large. Define the child's input, output, cancellation, and failure handling. Code reuse or provider abstraction alone is better served by ordinary language helpers.

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
- Bound the number of parallel children. Use Channels, a counter Attribute, and back pressure for a long-running parent. See [patterns.md](patterns/parallel.md#parallel-subflows).

## Identity and reuse

- Dex generates the child Flow ID from the parent Flow ID, the Step execution ID, and the Condition index. The application cannot choose it, so a child that needs its own business ID is a top-level child Flow.
- Name every Step that starts SubFlows without `-` in its Step type. Dex fails such a WaitFor attempt without retry. A Step that only waits on Timers or Channels may keep a dash. See [Step types that start SubFlows](https://docs.superdurable.io/reference/server/runtime-data#subflow-step-types).
- A later run of the same parent Flow ID can generate the same child Flow ID, so the start follows the SubFlow reuse policy. The default policy reuses the earlier run's running or completed child. Use a different parent Flow ID when both children must keep running. See [Later parent runs](https://docs.superdurable.io/reference/server/runtime-data#subflow-parent-lifecycle).
- SubFlow options are the start options plus a SubFlow reuse policy. The default restarts a child only when the previous one exited abnormally. ATTACH never restarts. ALWAYS_RESTART terminates a running child and starts a fresh one.
- A parent retry never duplicates the child: the Request ID Dex generates for the child start attaches to a running child or returns its terminal result under every policy. The policy table is in [SubFlowReusePolicy](https://docs.superdurable.io/reference/server/runtime-data#subflow-reuse-policy).

## Failures

- When Dex rejects a child start before the child begins, the parent Step's failure carries the rejection detail. Keep it visible in the recovery route.
- A child that fails returns its failure as the result. The parent decides the business outcome.
- A child keeps running when its parent closes. Stop it explicitly, or set a Flow timeout on the child so it ends on its own.
- A SubFlow without its own Flow timeout can wait forever when no Worker takes its Steps.
- Delivery of a child result to the parent is not proof that the parent consumed it or finished later work.

## Self-check

- Does the child need its own lifecycle, history, or retries, or is the fan-out very large? Otherwise, would parallel Steps do?
- Is the child start input typed and matching the child's start Step?
- Are parent completion, child cancellation, failure, and duplicate submission defined?
- Is fan-out bounded?
