# Channel and ChannelMap

A Channel is an ordered, durable, typed message queue scoped to one Flow run. A Step's WaitFor waits on it. One matching wait consumes each message. A canceled Step returns what it consumed; see [Wait and consume](#wait-and-consume).

Use a Channel for queued intent: commands, approvals, and events. To join parallel branches, use a Join: a ChannelMap of branch results that counts the branches and keys each round; see [patterns.md](patterns/parallel.md#await-parallel-steps). Use Attributes for current state and Streams for best-effort progress.

A ChannelMap partitions one message contract by a runtime key, such as an approval per tool-call ID. Each instance is its own FIFO queue. Instance keys cannot contain `/`.

## Publish

- From outside a Flow, publish only through a typed RPC. The Client has no direct publish.
- Steps, timeout handlers, and RPCs publish as staged effects. Publications commit with the method's result, or not at all.
- Messages published before any Step waits stay pending until a later wait consumes them.
- A message goes to one matching wait only. It is never copied to other waiting Steps.

## Wait and consume

| Condition | Proceeds when | Consumes |
| --- | --- | --- |
| ForOne | at least one message is pending | exactly one |
| ForN(n) | at least n are pending | exactly n |
| AtLeast(n) | at least n are pending | every pending message |
| AtMost(n) | at once, even when empty | up to n |
| AtLeastAtMost(min, max) | at least min are pending | from min up to max |

- AtMost does not wait by itself. To wait for at least one message and take up to a limit, use AtLeastAtMost with a minimum of 1.
- Several Steps may wait on one Channel; Dex offers each new message to the waiters in the order their waits started, and a wait that needs more messages than are queued is skipped. See [Conditions and Channel messages](../../SKILL.md#conditions-and-channel-messages). So interchangeable one-message consumers, such as a worker pool, may share a Channel; which one receives a message depends on timing. When it matters which Step receives a message, or consumers wait for different counts, give each its own Channel or ChannelMap key. Different Step types may consume one Channel in different phases of the Flow. Waiter order is in the [Channel reference](https://docs.superdurable.io/reference/server/runtime-data#channel-waiter-order).
- Only the winning alternative of AnyOf or AnyCombinationOf consumes. See [step.md](step.md#waits-and-conditions).
- In Execute, check that the Channel result is present before reading it. When another Condition won, nothing was consumed.
- Consumed messages leave the queue. Publication and consumption remain visible only in the run's recent history window. Pending queue state is not history.
- A wait consumes when it is satisfied, before Execute runs. If a decision or an RPC cancels the Step before its Execute commits, Dex returns the consumed messages to the front of their Channels or ChannelMap instances, in their original order. Another wait can take them, so a message can reach Execute twice. Make message processing idempotent, for example keyed by a business ID in the message.
- Dex drops the consumed messages only when the Flow close itself cancels the Step: any close, such as a StopFlow cancel or a Fail or Cancel Flow timeout, cancels every Step still active; graceful completion cancels none ([Conditions](../../SKILL.md#conditions-and-channel-messages)). Do not publish work in the same result that force-closes the Flow (ForceComplete, ForceFail, or a closing ForceCompleteIfChannelsEmpty) if the work must survive. The exact cases, and what a canceling commit does with its own publications, are in the [Channel reference](https://docs.superdurable.io/reference/server/runtime-data#canceled-step-messages).
- A Channel queue is not conversation history. Keep consumed messages in Attributes when the application must show or rebuild them.
- Before the Flow completes, drain each externally published Channel whose late messages must still be handled, with ForceCompleteIfChannelsEmpty rather than a size check first; see [patterns.md](patterns/channels-and-updates.md#drain-channels). Leave a cancel or withdrawal Channel undrained once the decision is final: draining it would let a late cancel override the decision. See [Cancel with child Flows](patterns/control-structures.md#cancel-with-child-flows).

## Read pending messages

- Steps, timeout handlers, and RPCs always receive Channel sizes, and ChannelMap keys and sizes, without a load.
- Reading pending message envelopes needs an explicit Channel or ChannelMap load: the whole ChannelMap, or exact instances.
- A loaded empty queue is empty. Reading an unloaded queue is a usage error.
- Every pending message has a server-assigned message ID.
- A pending-message read is a snapshot. Other handlers can consume, delete, or publish while the current handler runs.
- The snapshot also ignores the current handler's own staged publishes and deletes. To return the queue after a change, compute it from the snapshot and your own changes.

For a durable queue UI, expose a typed read-only RPC that loads the Channel and lists pending messages in FIFO order without consuming them.

## Delete, move, or edit pending messages

- Only a pending message can be deleted. A missing message fails a transactional RPC and is a no-op from a Step or a non-transactional RPC. See [Pending messages](https://docs.superdurable.io/reference/server/runtime-data#manage-pending-messages).
- Steps and timeout handlers may delete a pending message in the same result as Attribute writes, publications, and a decision. A known message ID can be deleted without loading the envelope.
- Deletion is a result side effect, not part of the Step decision. From a Step, deletion of an absent message is best effort and the other effects still apply.
- Dex applies a successful handler's effects in this order: Attribute writes, then pending-message deletions, then publications, then the decision.
- Editing a message means deleting it and publishing a replacement with a new ID. It does not rewrite history or conversation Attributes.

Move or edit atomically with a transactional RPC:

1. The caller identifies the message by its ID and never resends the original value; an edit also sends the replacement value.
2. The RPC loads the source Channel and finds the original value in its snapshot.
3. It stages the deletion and the publication to the destination.
4. A missing message rejects the whole transaction, including every Attribute write and publication.

```text
rpc moveQueuedMessageToSteeredMessages(messageId)    // transactional, loads queuedMessages
    message = queuedMessages.findPending(messageId)
    if message is absent: fail with Channel-message-not-found
    queuedMessages.delete(messageId)
    steeredMessages.publish(message.value)
```

Rules:

- Attribute locking already makes an RPC transactional. A deletion without an Attribute lock must select the SDK's transactional RPC option explicitly.
- Transactional validation protects an ID-only move from concurrent consumption. It does not isolate a decision based on the whole snapshot. For that, every cooperating Step and RPC uses one shared Attribute lock.
- Without transactional execution, a missing deletion may be a no-op while the other effects commit. Reconcile from a fresh pending list.
- Channel-message-not-found means the message is no longer pending: consumed, deleted, or moved by an earlier attempt of this call. Pass a stable RPC request ID so a retried move returns its recorded reply; otherwise reload the pending list before deciding, and in a UI let the user choose again.

## Self-check

- Does each Channel have a clear producer, and do consumers that share it take one message each and handle any message the same way?
- Does every Execute check which Condition won before reading a Channel result?
- Are external publications whose late messages must be handled drained before completion, with the atomic complete-if-empty decision?
- Do moves and edits use a transactional RPC that identifies the message by its ID?
- Is conversation history kept in Attributes, not in the queue?
