# dexzd-engine

## Purpose

Runtime that proposes, commits, and applies server-zd commands and runs their effects: dispatch, timers, deliveries, and recovery.

## Extension record submission

An embedding application defines each extension record's payload. server-zd stamps,
routes, orders, and acknowledges the record without decoding its payload.
`CommandCommitter::submit_extension_record(routing_hash, payload, urgency)` derives
the partition from `routing_hash`. The committer's record kind is fixed at construction.
A Flow committer rejects extension records with `ExtensionSubmitError::WrongRecordKind`.
An extension committer rejects Flow commands with `CommitError::Unavailable`.
Neither wrong-kind submission stamps or enqueues a record.

Before stamping, the extension entry checks the largest possible stamped encoding
against `min(MAX_RECORD_LENGTH, max_unapplied_bytes)`. A record that could exceed
that limit returns `ExtensionSubmitError::RecordTooLarge` without stamping or
enqueueing. Other submission failures match the Flow entry's failures and are
wrapped in `ExtensionSubmitError::Commit`.

Both entries use the same stamping and enqueueing function, per-partition stamp
lock, watermarks, and unapplied byte budget. The function stamps the node epoch,
command ID, and `proposed_at_micros = max(wall clock, partition watermark + 1)`.
Encoding and enqueueing occur under the partition lock, so log order equals stamp
order within that partition. The watermark advances only after enqueueing.
The function returns the stamped value and its ticket, or a `CommitError` when
nothing was enqueued.

The extension entry returns an `ExtensionStamp` and an `ExtensionCommitTicket`.
The stamp contains the same values as the log envelope; apply reads them to order
the partition's records and identify waiting submitters. The ticket resolves after
the record commits and applies to the local store. Its `ExtensionCommitted` result
includes the position, stamp, and application's apply outcome. server-zd passes
that opaque outcome unchanged to the submitter, which downcasts it to its own
type. `ExtensionCommitTicket::wait` returns a pipeline error on failure, or
`CommitError::NodeStopping` if the pipeline drops the ticket without resolving it.

## Extension record apply

A node applies one kind of record, chosen by its dependencies. Server-zd's own
nodes pass `CommittedRecordHandling::FlowCommand` in `EngineNodeDependencies` and
`HotStandbyDependencies`, and their hooks factory returns
`CommittedRecordSink::FlowCommand`. An embedding application passes
`CommittedRecordHandling::ExtensionRecord` with its `ExtensionRecordApplier`, and
its hooks factory returns `CommittedRecordSink::ExtensionRecord` with its
`ExtensionRecordSink`. Both are constructor inputs; nothing is wired later. The
committer's record kind follows the dependencies.

Startup checks the choice. A node or hot standby whose record kind contradicts
`cluster.embedding_application` fails with `EngineStartError::InvalidConfig`
before it reads or creates any cluster metadata: only a named embedding
application applies extension records, and it applies nothing else. A hooks
factory that returns the other kind of sink fails the start with the same error
after the lease, which the start then releases.

For each frame of an extension node, the frame applier:

1. skips a frame the local store already applied;
2. decodes every record and halts the partition when one is a Flow command, has an
   invalid command ID, or names another partition in its envelope or through
   `PartitionHasher::partition_of(routing_hash)`;
3. calls `ExtensionRecordApplier::apply_record` once per record, in log order,
   over committed state plus the writes of earlier records in the frame; a returned
   `ExtensionApplyHalt` or a panic halts the partition;
4. with `CommitConfig::debug_replay_checks`, calls `apply_record` a second time and halts
   the partition when the two `writes` differ; the second outcome is dropped;
5. writes all records' writes and the `PartitionMeta` atomically.
   `last_command_timestamp_micros` comes from the envelopes, and
   `last_behavior_version` keeps its value because extension records carry no
   behavior version;
6. takes the waiting tickets under the write admission, calls
   `ExtensionRecordSink::publish_frame` once with every record's stamp, outcome,
   and `has_waiter`, then checks the lease and moves each waited outcome into its
   ticket's `ExtensionCommitted`.

Server-zd never decodes a payload, so it checks only that the routing hash selects
the frame's partition. The applier checks that the hash matches the record's own
routing key and that its writes stay in that key's partition.

A frame that is skipped, fails its store write, or halts drops every outcome; a
halt fails the waiting tickets of the frame at once, and later frames of the halted
partition fail theirs as they arrive. An outcome without a waiter is dropped after
`publish_frame` returns, so replaying a long log keeps no outcomes.

`is_replayed` is true when the frame's epoch is older than the serving node's
epoch. Those frames come from recovery's log replay, before the partition's
`PartitionEffectRebuilder` runs; their records have no waiter. A sink applies only
idempotent in-memory updates for them and starts no live work, because the
rebuild owns pending work. A hot standby applies records with the applier and
never calls a sink; the takeover's serving start creates the sink.

The calling contract for both traits:

- `apply_record` and `publish_frame` run on the blocking thread of a partition's
  applier lane. Records of one partition arrive in log order; partitions run
  concurrently.
- `apply_record` is a pure function of the partition, stamp, payload, and state.
  Equal inputs return equal writes, because replay checks apply every record twice
  and compare them. Keep no mutable state in the applier, so a caught panic leaves
  nothing poisoned.
- `publish_frame` only updates memory and never blocks. The lane waits for it
  before it writes the next frame, so a sink never submits a record or waits for a
  commit inside it. Hand follow-up submissions to a separate task.
- A sink that `publish_frame` updates synchronously is up to date before any
  submitter's ticket resolves, so a submitter's next read observes its record.

Server-zd-only diagnostics stay off on an extension node: the semantic-point and
time-travel checks of `CommitConfig::debug_replay_checks`, the scan of history rows before
replay, and committed-command metrics. They parse server-zd's own rows, which an
embedding application's rows are not.

## History segment reads

GetHistoryEvents runs apply's read model in rounds. Each round pins a committed view, reads the page from local rows and the segments loaded so far, and releases the view. When the model needs more uploaded segments, `HistorySegmentReader` fetches their headers and records blocks after the view is gone, at most `history.objectReadConcurrency` at once, so slow object storage never holds a view. The next round reads a new view, so a segment pruned meanwhile reads as pruned and a purged run as not found. The last round, the eighth by default, returns the shorter page of the events before the first unloaded segment, with a next page token, so every read makes progress with bounded work. A segment object still missing on a second round fails the read with UNAVAILABLE. Decoded headers stay in a bounded memory cache. Header and records block bytes also go to a `dex-blob-cache` disk cache, in `<localStorage.directory>-history-read-cache` so that setting the store aside never moves it, keyed by object key, content checksum, and part, which needs no invalidation because uploaded objects never change; keys of hard-delete value references are refused. The reader is created once per process and opens the disk cache on first use.

While the partition of a Flow it names offloads history, LoadBlobs takes at most 1000 entries; with offload off it takes any number. It checks every entry's run and reads local values on one committed view, which apply's `locate_offloaded_value` also uses to name the UPLOADED segment of each value that offload deleted. After the view is released, each such value comes from the disk cache, keyed by its value reference and the segment's content checksum, or else from one ranged read per segment that spans its requested values, which then go to the cache. A segment object that is missing makes it look the value up again on a new view: a purged run or a passed value is omitted, and a value whose segment is still UPLOADED fails the request with UNAVAILABLE.

Before a proposal commits a command that references values of its run by value reference, such as a Worker result that passes its Step input to a next Step, the builder checks the speculative state for values that offload deleted locally and that the window still holds. That state is the one the command applies on, so no command in between can delete a value. Instead of proposing, it reads those values, with no view held, from the disk cache or the uploaded segments as LoadBlobs does, and proposes one `ValueRevive` per value. It reads and revives them in batches whose recorded sizes fit `max_speculative_bytes_per_node`, at least one value each, and waits for each batch to commit before reading the next, so a small command that references many large values holds at most one batch in memory. Object reads share the `history.objectReadConcurrency` slots. It then builds the command again, which now finds its values locally and commits unchanged. A revive that apply rejects, such as for a purged run, ends the reviving, and so do three rounds that each found values to revive. The command is then proposed as built, and apply replies as it does to any unknown reference. Each committed revive that restored its value counts in `dexzd_history_value_revivals_total`; one that found the value already local, such as after a concurrent request revived it, writes nothing and does not count. Their reads count as LoadBlobs reads in `dexzd_history_reads_total`.

## Time travel history reads

ResetFlow computes its result in the same kind of rounds, on blocking threads. Each round opens the run's history from a new committed view and the uploaded segments fetched so far, computes, and releases the view. When the computation reaches unloaded segments, the reader fetches their records and asked-for checkpoints blocks after the view is gone, and the next round computes again. A round that reaches a segment also fetches the later uploaded segments its read goes on to need, at most 1024 segments a round, so a computation usually takes three rounds; one that needs more than 16 fails with UNAVAILABLE. A round runs at most `history.objectReadConcurrency` segment reads at once and charges each part as it arrives against `history.timeTravelReplayMemoryBytes`, apart from the computation itself. The first part past the budget ends the ResetFlow with RESOURCE_EXHAUSTED and drops the reads still running. A segment object missing twice fails it with UNAVAILABLE. These reads share the header and disk caches and the read slots with GetHistoryEvents but are not counted in `dexzd_history_reads_total`. Values that offload deleted locally come from the same disk cache or ranged reads as LoadBlobs, fetched between rounds when a round needs them. A replay stops at each such value, so it may take a round per value; no count limits these rounds. Each must fetch a new value, and every value, cached or read, counts against the same memory budget.

## History segment uploads

The effect runner uploads each committed SEALED history segment to the primary object store and proposes `HistorySegmentUploaded`. One task per run uploads its segments oldest first. The committed `HistorySegmentSealed` effect, partition rebuild from the `HistoryUploadPending` index, and a periodic rescan start that task, so a restart, takeover, or snapshot restore never loses an upload. A segment's key and content come from committed state alone. A repeated upload therefore finds an equal content checksum and acknowledges the existing object; an object with another checksum is replaced with an If-Match write. Failed object storage calls retry with backoff and never delay commands; a permanent rejection waits for the next rescan. Every call ends when the node stops, so no history request outlives its owner. A run purge waits one minute, so the client side of every write that an earlier owner or an abandoned attempt started has ended. It then deletes the run's segment objects by their catalog keys, sweeps the run's `history/` prefix, and proposes `RunPurge`. Dropping a call does not fence a write that the object store already accepted: such a write can still land after the purge. It leaks an object of a purged run, whose run ID is never reused, so no read returns it. A periodic orphan sweep of `history/` removes these objects.

## Expired history object deletion

The committed `HistorySegmentsExpired` effect and partition rebuild from the `HistoryObjectDeletePending` index start one task per run that deletes the objects of the run's EXPIRED segments oldest first. Apply keeps no index row for a run whose delete switch is off, so such runs start no work at a restart. Before each attempt, retries after an object storage failure included, the task reads the run's EXPIRED segments and its switch from a new committed view, recomputes the due batch, and stops when the switch is off, the run is gone or purging, or none remains. Turning the switch off stops deletions that have not started; a `delete_many` already sent to object storage may still complete. It deletes an object only once the owner's wall clock passed the segment's `expired_at_micros` by one hour, so a read that found the segment UPLOADED before its expiry finishes with the object; until then it sleeps. After each `delete_many` it proposes `HistorySegmentObjectsDeleted` with the deleted first seqs, batched and unbound to an attempt. A record that leaves its oldest segment in place fails the task, so it never loops. Failed object storage calls retry with backoff.

Every `history_orphan_sweep_interval`, 6 hours by default and starting one interval after startup, the lease holder lists the whole `history/` prefix. It deletes each object at least an hour old whose run is in a partition it owns and has no run record in committed state, and counts them in `dexzd_history_orphan_objects_deleted_total`. A segment object exists only after the seal that names it committed, so the run record of any listed object of a live run is in the owner's committed view. The age rule keeps the sweep away from a write by a newer owner. Keys of another layout are skipped.

## SubFlow hard-delete Attributes

WaitFor results upload hard-delete values before the parent command enters the log. Object reference indices cover the parent Attribute writes, then each SubFlow's initial Attributes in condition order. Apply places the SubFlow references in its child-start outbox message without doing I/O.

The delivery effect reads the parent-owned staging objects and uploads child-owned objects before proposing the inbox command. The inbox carries the child references. An acknowledgment schedules the staging objects for deletion. Pending-upload recovery retains objects referenced by an outbox message as well as current or pending-delete Attribute objects. Rejected deliveries and attachments delete their unused child uploads; an interrupted delivery leaves ledger entries for recovery.

Every Attribute-carrying command passes the same hard-delete reference check before submission and at apply entry. Missing, duplicate, mismatched, and out-of-range references return an input error. The low-level missing-object invariant therefore indicates an internal call-path bug, rather than unchecked client or Worker input.

## RPC request IDs

While a shared task is active, transactional and locking RPCs share one node-owned task for
each tenant, Flow, resolved run, and request ID. Concurrent identical requests receive the
same result or error. A duplicate locking RPC waits for the original result instead of
returning ABORTED for the original call's locks. A repeated request ID joins the shared task
whatever its payload, as StartFlow compares only the request ID. A committed reply takes
precedence over an in-flight entry.

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
- `HotStandby::status` reports the state and the lag of the follow group furthest behind; a single node follows one group. Lag is zero after a read finds no next batch, so an idle log does not look behind. While the standby waits at a hole that later batches follow, lag is the time it has waited there, so a hole that stays open turns it behind. Otherwise lag is the time since the later of the last applied batch's seal time and the last read that found no next batch. When three or more log reads in a row fail, `read_error` shows why, so an unreachable object storage is not mistaken for an idle node.
- Only a configuration error fails the standby for good: a node identity mismatch, a regressed incarnation, a behavior version this binary does not support, or an invalid configuration. Before it opens a RocksDB directory, it probes the directory's lock without opening the store, so a held lock never touches the holder's files: a lock that another process holds, such as the serving node's, fails it as a configuration error, and one that a lingering handle of this process holds is retried, with a `last_error` that says so. After any other error, and after log cleanup outran it, it closes its store, reports `Restarting`, waits from 1 second doubling to 60 seconds with jitter, and bootstraps again. The delay resets, and its status drops the last error, only once following reads past where earlier periods stopped, or reaches the end of the log there rather than a hole. While the cluster object or the manifest is missing, it rereads them every second.
- The gap check of every start and bootstrap also rejects a store in which a partition applied a batch past the recorded end of a closed stream. Such a store followed a batch that the log does not hold, so it is set aside and the newest snapshot is restored.
- A `LogFollower` takes its write fence and applier pool from its owner. It advances every partition of the pool, so the pool must serve only the followed partitions.
- `EngineNode::local_store_close_waiter` and `HotStandby::local_store_close_waiter` return a `LocalStoreCloseWaiter`. After a step down or a standby shutdown, await its `wait_until_closed` before another process reopens the RocksDB directory.
- Drop the standby or call `HotStandby::shutdown` to stop following. Dropping it stops its store writes at once; `shutdown` also waits for its store to close and returns whether it closed.
- `HotStandby::status_reader` returns a reader of the status that stays valid while a takeover owns the standby, such as while the takeover waits for the lease. Once a handover, a shutdown, or a drop stops the standby, the status shows `Stopped`.

`EngineNode::take_over_from_hot_standby_until_cancelled` is the start of a node that has a hot standby. The two starts differ only where the local store opens:

1. The standby keeps following while another process holds the lease. Cancellation before the lease returns the standby, still following. An error before the lease, such as a standby that follows another node or cluster, returns `HotStandbyTakeoverError` with the standby.
2. Once this process holds the lease and has reread the manifest under it, the standby stops reading, applies its queued frames, and advances its applied positions.
3. It stops its write fence, so no standby lane writes the store again, and its tasks are joined. The start owns this handover: when it fails or is cancelled, it stops the write fence at once and stops the standby beside its own tasks, then releases the lease, all within `EngineNode::TAKEOVER_RELEASE_BUDGET`.
4. The start fails with `InvalidConfig` when the standby, which may have bootstrapped while the start waited for the lease, follows another cluster. It checks that the standby used the configured store and that its write fence stopped only for the handover, not after a failed store write. It then checks that every applied position names this node, and reruns the gap check against the manifest read under the lease.
5. When a check fails, or the standby had no store, the start opens or restores the configured store, as a start without a standby does. It first waits up to 30 seconds for a lane that outlived the handover to close a RocksDB store.
6. The rest of the start is unchanged: a new applier pool with the real hooks, log replay from the applied positions, the EpochStart, and serving.

`EngineNode::is_serving_hot_standby_store` reports whether a takeover served the standby's store or opened the configured store, for example to label takeover metrics.
