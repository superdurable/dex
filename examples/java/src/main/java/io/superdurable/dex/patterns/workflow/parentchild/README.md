# Parent–SubFlow design pattern

`ParentFlowV2` fans out independent child executions while limiting concurrency. Each worker branch
dequeues one task and declares a durable SubFlow condition:

```java
public Wait waitFor(Context context, Integer request) {
    return Wait.until(SubFlow.run(ChildFlow.class, request.toString()));
}

public StepDecision execute(Context context, Integer request) {
    FlowResult child = SubFlow.getConditionResults(context);
    return StepDecision.goTo(loopForNextTask, null);
}
```

Dex generates the child Flow ID, starts or reuses a normal backend Workflow, and durably waits for
its terminal result. There is no polling Step, completion Channel, Client injection, or manual
already-started handling.

SubFlows are independent executions. Closing the parent does not cancel them. With `Wait.anyOf`, a
losing SubFlow result has status `RUNNING`; use its Flow ID with `Client.stopFlow` when the
application wants explicit cleanup.
