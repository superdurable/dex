# Parent-Child Pattern (Option 2)

How to start a child flow and wait for it to finish. Useful for fanning out
executions for parallelism, or any time a parent must wait on a child.

Dex offers two ways to do this.

## Option 1: the child signals the parent on completion

This is what [scalableparallel](../scalableparallel) implements. It is the most
efficient, but also the most involved:

1. The parent starts the child with `ignore_already_started=True` and a
   `request_id`.
2. The parent uses a `ChannelMap` instance per child to wait for completion.

Each child can only notify one parent, so this does not work when several
parents may wait on the same child. For example, a billing refund request (the
parent) may cover multiple invoices (the children), and different refund
requests may share invoices.

## Option 2: the parent polls with the client API

`ParentFlowV2` implements this option. The parent calls
`client.wait_for_flow(...)` from a step and catches `LongPollTimeoutError`,
because the call only waits about 10 seconds by default. A timer condition
spaces out the retries, doubling the wait up to 10 seconds.

This avoids `ignore_already_started` + `request_id` and dynamic channels
entirely, and it supports many-to-many parent/child relationships.

The trade-off is Temporal action usage: every `AwaitChildWorkflowCompletion`
iteration costs actions until the child finishes. If children can run for days,
prefer option 1. If they normally finish within minutes, this is the simplest
approach.

## Steps

- `Init` — publishes N tasks to the task queue and starts
  `CONCURRENCY_PER_PARENT_WORKFLOW` parallel `LoopForNextTask` branches.
- `LoopForNextTask` — waits for a task on the queue.
- `StartChildWorkflow` — starts a `ChildFlow`, ignoring `FLOW_ALREADY_STARTED`.
- `AwaitChildWorkflowCompletion` — waits for the child, backing off on timeout,
  then loops back for the next task.
