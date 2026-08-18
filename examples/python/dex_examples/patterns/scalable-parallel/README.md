# Scalable Parallel Processing

Runs a large number of tasks in parallel across child flows, with bounded
concurrency and a bounded queue per parent. When a parent's queue is full the
request is retried against another parent, so throughput scales horizontally
instead of piling everything into one flow.

For smaller fan-outs, [parallel](../parallel) is simpler. For the polling
variant of parent-child coordination, see [parentchild](../parentchild).

## Flows

- `RequestReceiverFlow` — generates a batch of task IDs and enqueues it into a
  randomly chosen parent through the `enqueue` RPC.
- `ParentFlow` — owns the task queue, starts child flows up to the concurrency
  limit, and completes once the queue drains.
- `ChildFlow` — does the work, then notifies its parent through the
  `complete_child_workflow` RPC.

## Backpressure

`enqueue` returns `False` when the batch would push the queue past
`MAX_BUFFERED_TASKS`. `Request` turns that into an `EnqueueFailedError`, so the
step's own retry picks a parent again on the next attempt. If the chosen parent
does not exist yet, the `FLOW_NOT_EXISTS` error is caught and the receiver
starts it with the batch as its input. This keeps any single parent's history
bounded regardless of total task volume.

## Completion signalling

The child notifies its parent instead of the parent polling the child. The
parent waits on a `ChannelMap` entry keyed by child flow ID, so each completion
wakes exactly the branch that started it. The child learns its parent's ID from
the `ParentWorkflowId` attribute, which the parent sets through
`StartFlowOptions.with_attribute` at start time. This is the cheapest option in
Temporal actions, but each child can only notify one parent.

## Steps

`ParentFlow`

- `Init` — publishes every task in the batch to the queue and moves to
  `LoopForNextMessage`.
- `LoopForNextMessage` — waits for a new task (only while below
  `CONCURRENCY_PER_PARENT_WORKFLOW`) or for any pending child to signal
  completion. It starts a child for each new task, drops children that finished,
  and force-completes once nothing is pending and the queue is empty.

`ChildFlow`

- `Processing` — waits on a random 0-59 second timer to simulate work, then
  signals the parent, ignoring `FLOW_NOT_EXISTS` in case the parent already
  completed.

`RequestReceiverFlow`

- `Request` — generates the batch and enqueues it, handling the full-queue and
  missing-parent cases.

## Duplicate protection

Children are started with `ignore_already_started=True`, `IdReusePolicy.DISALLOW`,
and the step execution ID as the `request_id`, so a step retry cannot start the
same child twice. A `FLOW_ALREADY_STARTED` error means another branch owns that
child, and the parent simply does not wait on it.
