# dexzd-engine

## Purpose

Runtime that proposes, commits, and applies server-zd commands and runs their effects: dispatch, timers, deliveries, and recovery.

## SubFlow hard-delete Attributes

WaitFor results upload hard-delete values before the parent command enters the log. Object reference indices cover the parent Attribute writes, then each SubFlow's initial Attributes in condition order. Apply places the SubFlow references in its child-start outbox message without doing I/O.

The delivery effect reads the parent-owned staging objects and uploads child-owned objects before proposing the inbox command. The inbox carries the child references. An acknowledgment schedules the staging objects for deletion. Pending-upload recovery retains objects referenced by an outbox message as well as current or pending-delete Attribute objects. Rejected deliveries and attachments delete their unused child uploads; an interrupted delivery leaves ledger entries for recovery.

Every Attribute-carrying command passes the same hard-delete reference check before submission and at apply entry. Missing, duplicate, mismatched, and out-of-range references return an input error. The low-level missing-object invariant therefore indicates an internal call-path bug, rather than unchecked client or Worker input.

## RPC request IDs

While a shared task is active, transactional and locking RPCs share one node-owned task for
each tenant, Flow, resolved run, and request ID. Concurrent identical requests receive the
same result or error. A duplicate locking RPC waits for the original result instead of
returning ABORTED for the original call's locks. Conflicting payloads retain the existing
fingerprint rejection. A committed reply takes precedence over an in-flight entry.

A caller disconnect or transport deadline ends only that caller's wait. It does not release
the RPC's locks or let natural Flow close proceed. The RPC timeout starts at the Worker
invocation, after admission, lock synchronization, and snapshot reads. Admission and lock
conflicts return immediately. The SYNC barrier waits for earlier proposals to settle or
the registry to halt, without a separate timer. Snapshot object reads use the object store
timeout (30 seconds for S3 operations and between streamed body chunks). These stages do not consume the Worker timeout.
The Worker gets the full positive timeout_seconds, otherwise api.defaultRpcTimeout
(default 60 seconds). A handler that exceeds its timeout returns DEADLINE_EXCEEDED without
proposing a commit. Proposal and commit can finish after the Worker deadline and return
the committed result.

An independent 60-second server budget, named RPC_COMMIT_PROCESSING_BUDGET, bounds uploads,
proposal, commit waiting, and result processing. Exceeding it returns UNAVAILABLE with an
unknown outcome and releases the shared task. A retry may run the RPC again. An already
submitted command may still commit after the budget expires.

Registry halt also ends the task. Graceful shutdown allows admitted tasks to finish and
commit during the four-second drain, then cancels remaining tasks and awaits their cleanup.
This includes tasks whose callers already disconnected.

limits.maxInFlightRpcsPerNode bounds shared tasks across the node (default 1,000). At
capacity, a new request ID returns RESOURCE_EXHAUSTED; matching duplicates still join an
admitted task, and committed replies still replay. Tasks release their slots, locks, and
natural-close holds on success, error, deadline, halt, shutdown cancellation, or panic. A
panic returns INTERNAL to current waiters and does not leave the request ID stuck.

Completed successful replies use the persisted request deduplication records. In-flight
admission and errors are not persisted. A node crash before the result commits can invoke
the Worker again on retry. Exceeding the server budget or cancelling a task after command
submission leaves the outcome unknown, and a retry may run the RPC again.

The Go, Java, Python, TypeScript, and Rust SDKs generate a fresh request ID for each explicit
RPC invocation and expose no override. Retrying through these APIs may run the RPC again;
[issue #1039](https://github.com/superdurable/dex/issues/1039) tracks stable RPC request IDs
in the SDKs. Low-level FlowService callers can supply a stable request_id. Reusing that ID
replays a retained committed reply, but a retry before an earlier command commits may invoke
the Worker again, even on the same node. If both commands commit, persisted deduplication
applies the first committed command's effects and returns its reply for both commands.

Durable admission, resumable Worker calls, and persisted error replay require a separate
recovery design. Plain RPCs continue to use the non-transactional path without in-flight
deduplication.

## Hot standby

`HotStandby` runs in a process of the node that does not hold the node lease. It keeps the configured local store current from object storage, so a later takeover replays only the last few batches.

- It reads object storage through a read-only wrapper. Every write fails, so it never writes an EpochStart, a snapshot, the manifest, or a deletion.
- It opens the local store, or restores the newest snapshot into it, with the same code as a starting node.
- A `LogFollower` reads the node's log with `StreamTailReader` and applies each accepted batch with the node's frame applier. A batch is one GET; its frames go to the partition applier lanes. The follower applies only the contiguous prefix of each stream and waits at the first missing seq.
- Its committed-command sink does nothing, and it runs no rebuild, timers, dispatch, Attribute Sync, or search projection. A serving start rebuilds those from the local store.
- It advances idle partitions too. Each position records the epoch in effect after its batch, which recovery accepts after the position.
- It rereads the manifest periodically. A closed segment bounds the stream it reads, and a new incarnation becomes the next stream. Log cleanup that passes its next seq or its stored positions makes it restore the newest snapshot. While it waits at one seq, it also checks whether cleanup deleted that seq.
- `HotStandby::status` reports the state and the lag of the follow group furthest behind; a single node follows one group. Lag is zero after a read finds no next batch, so an idle log does not look behind. Otherwise it is the time since the later of the last applied batch's seal time and the last read that found no next batch.
- A `LogFollower` takes its write fence and applier pool from its owner. It advances every partition of the pool, so the pool must serve only the followed partitions.
- Drop the standby or call `HotStandby::shutdown` to stop following.
