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
