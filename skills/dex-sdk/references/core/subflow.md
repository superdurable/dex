# SubFlow

A SubFlow is a child Flow execution that a parent Step starts in WaitFor and waits on as a Condition. The child has its own identity, history, retries, and lifecycle. The parent's Execute reads the child's result.

A separate top-level Flow that a Step starts through a Client is not a SubFlow. See [flow.md](flow.md#own-downstream-starts-durably).

## Evolution gate

Default a new design to no SubFlows. Parallel Steps cover work that shares one Flow identity and lifecycle. Propose a SubFlow only when all of these hold:

1. An existing single-Flow design or running system gives evidence. A concern that it may become complex later is not evidence.
2. The main graph is already impractical to review, evolve, or operate, or one fan-out needs more than about 200 concurrent Step executions.
3. Parallel Steps, batching, Channel coordination, RPCs, and ordinary code abstraction were evaluated and rejected with reasons.
4. Child identity, input and output, parent completion, cancellation, failure, retry, duplicate submission, and concurrency are defined.
5. The user explicitly confirms the SubFlow design.

The 200-Step value is an architecture-review threshold for this skill, not a Dex limit. Crossing it permits a proposal; it does not select a SubFlow. Code reuse, provider abstraction, an independent retry policy, or ordinary parallelism is never enough.

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
- Bound the number of parallel children. Use Channels, a counter Attribute, and back pressure for a long-running parent. See [patterns.md](patterns.md#parallel-subflows).

## Identity and reuse

- Dex generates the child Flow ID from the parent Flow ID, the Step execution ID, and the Condition index.
- A later run of the same parent Flow ID generates the same child Flow ID when it creates the same Step execution ID (same Step type and sequence number) and uses the same Condition index. Step execution numbers restart in each run, so this is common. Its start is then a repeated start under the reuse policy. With the default policy it reuses the earlier run's running or completed child until Dex deletes that child. Always restart starts a fresh child but terminates an old child that is still running. When both children must keep running, use a different parent Flow ID.
- SubFlow options are the start options plus a SubFlow reuse policy.
- The Request ID that Dex generates for the child start always attaches to a running child or returns its terminal result, whatever the policy. So a parent retry never duplicates the child.
- A different Request ID goes through the reuse policy:

| Existing child | Attach | Restart if previous exited abnormally (default) | Always restart |
| --- | --- | --- | --- |
| none | start | start | start |
| running | attach | attach | terminate and start |
| completed | return result | return result | start |
| failed, canceled, timed out, or terminated | return result | start | start |

## Failures

- When Dex rejects a child start before the child begins, the parent Step's failure carries the rejection detail. Keep it visible in the recovery route.
- A child that fails returns its failure as the result. The parent decides the business outcome.
- A child keeps running when its parent Step is canceled or its parent Flow closes. Dex does not stop it. Stop it explicitly, or give it a Flow timeout as a backstop.
- A SubFlow without its own Flow timeout can wait forever when no Worker takes its Steps.
- Delivery of a child result to the parent is not proof that the parent consumed it or finished later work.

## Self-check

- Did the SubFlow pass the evolution gate, with explicit user confirmation?
- Is the child start input typed and matching the child's start Step?
- Are parent completion, child cancellation, failure, and duplicate submission defined?
- Is fan-out bounded?
