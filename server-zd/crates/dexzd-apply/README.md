# dexzd-apply

## Purpose

Deterministic state machine that applies one server-zd command to Flow state and returns its writes, effects, and reply.

The crate also derives run search documents and exposes pure reads for summaries,
Attributes, Channels, current state, history, wait predicates, and stored values.
Callers provide a consistent `FlowStateReader` snapshot for each read.

Each logical WaitFor or Execute call, including the Flow timeout handler, loads
its state snapshot after acquiring its first attempt's locks. Retries reuse its
Attributes, Channel messages, Channel infos, and selections. The serialized Step
invocation retains that snapshot and its locks through backoff, pending retries,
and node restart. Conflicting RPCs fail immediately while those locks are held.
Locks release when the logical method ends. Value pins transfer to each retry
and release when the method ends; time travel rebuilds pins for retained snapshots in running, backoff, and pending invocations.

## Search outbox

While a partition's `SearchProjectionModeRecord` is enabled, every search
document change and run purge appends a `SearchOutbox` partition index entry.
Its seq is the command's `proposed_at_micros`, which the committer raises in log
order within a partition, so speculative and committed apply write the same key.
Only `SetSearchProjectionModeCommand` and `SearchOutboxAckCommand` write the mode
record; disabling deletes the queue, and an ack deletes the entries after the
previous acked seq through its own seq. Enabling a disabled mode records the
enabling command's seq as `enabled_at_seq`, so SQL progress below it is known to
miss changes. An ack whose seq is not below its own command's seq is rejected
with INVALID_ARGUMENT and writes nothing.
Replayed commands append nothing; the outer command appends with its own seq.
An append at or below the acknowledged seq is an invariant violation.

## History offload mode

`SetHistoryOffloadModeCommand` sets whether a partition offloads run history to
object storage. Enabling writes the partition's `HistoryOffloadModeRecord`, and
disabling deletes it, so an absent record means disabled. A command that names
another partition than the one it was routed to is rejected with
INVALID_ARGUMENT and writes nothing. A command that keeps the mode writes
nothing.

## History segments

A run that starts while its partition offloads history keeps an open history
segment in its `RunRecord`. Each command that records history adds its history
record, checkpoint, and superseded value bytes to it. At the end of the command,
apply seals `[open first seq, command seq]` when the segment reached the run's
`history_segment_target_bytes` (4 MiB when zero), when 10,000 values were
superseded inside it, or when the command closed the run. Sealing writes a SEALED
`HistorySegment` row, one `HistorySegmentValue` row per value then in table G
with a superseded seq inside the range, and the run's `HistoryUploadPending`
index row, and emits `HistorySegmentSealed`. A value revived later keeps its row,
so the segment's content never changes. Runs that started while offload was off
keep no segment state. While the partition does not offload, apply seals nothing,
and each command of a run with segment state moves the open segment past itself.
The seqs and events of those commands belong to no segment, and their local rows
follow window pruning, so a segment opened after offload is turned on again never
spans rows that pruning may have deleted.

`HistorySegmentUploadedCommand` records no history, takes no run seq, and is not
bound to an attempt. It marks the run's oldest SEALED segment, the first one
after the uploaded seq U, UPLOADED and sets U to its last seq. It deletes the
pending index row once no SEALED segment remains. An acknowledgment that names
another segment replies OK and writes nothing; one whose checksum is not 16
bytes or whose object size is zero is rejected with INVALID_ARGUMENT.

Window pruning deletes history records, checkpoints, and superseded values only
at or below U while the partition offloads history and, whatever the mode, while
the run has a SEALED segment. So turning offload off never deletes sealed content
before its acknowledgment; once every SEALED segment is acknowledged, pruning
returns to the window rule. Segment rows count as history bytes, never toward the
run state limit. Run purge deletes the pending index row with the run. Retention postpones a closed run while it has a SEALED
segment, as it does for an unacknowledged outbox message, so purging never
abandons an upload.

## Value and storage accounting

Each run tracks encoded Flow and History row bytes, including keys. Live and superseded value bytes use the encoded value sizes in birth records; garbage rows identify the superseded portion. Apply updates these totals from each command's writes and deletes.

The run state limit covers active state: Flow state rows and live values. Superseded values and history bytes are tracked separately and never count toward it. A client value write that grows active state beyond the limit returns `RESOURCE_EXHAUSTED` without writes. A Worker result that does so becomes a non-retryable attempt failure. Attempt starts, timers, stop, and purge can proceed when the run is full. The superseded-value cap advances the time travel window through semantic points and collects values only after their last-holder sequence leaves it. The current semantic point always remains available, so a command can temporarily leave superseded bytes over the cap.

Attribute Sync snapshots contain complete values and do not retain value-table rows. Historical replay rebuilds attempt state without marking the run-scoped sync outbox: intermediate references before the retained window may already be collected. Time travel marks the restored state and records synced writes from reapplied input against live values.

SubFlow child-start messages also carry complete values without retaining value-table rows. Historical rebuild skips their run-scoped outbox writes while restoring the wait identities. Live child starts still resolve every referenced input and Attribute value.

Permission mappings accumulate grants in the attempt-scoped
`DexWorkQueuePermissions` Attribute. History stores the original commands, so
rebuilding must replay their mappings rather than recompute grants from only the
final Attribute values. A rebuild can start at a checkpoint before the retained
window. If a required permission or mapping Attribute value was collected,
TimeTravel returns `FAILED_PRECONDITION` without changing state. Target diagnostics
return no reconstructable target for the same case. Live projection and reapplied
input retain their existing behavior.
