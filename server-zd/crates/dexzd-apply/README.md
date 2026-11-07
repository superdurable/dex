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
keep no segment state until they migrate; see History migration. While the
partition does not offload, apply seals nothing,
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

## Local history deletion

While the partition offloads history, each acknowledgment and each later command
of the run deletes the local rows of UPLOADED segments, within per-command
limits; `history_local_rows_deleted_through_seq` and
`history_values_offloaded_through_seq` record how far it got. Seqs that no
UPLOADED segment covers, such as commands applied while offload was off and the
unsealed rest of an abandoned migration, keep their rows, which follow window
pruning. Turning offload off stops the deletion.

History records and checkpoints go; genesis keeps its record and checkpoint.
Their metadata stays, with a zero `row_size_bytes`, so time travel targets,
history reads, and the window keep working until window pruning deletes the
metadata and the checkpoint seqs. Only a record that is neither semantic nor holds
events, pending method calls, or a close loses its metadata at once, so a long
heartbeat-only Step does not grow metadata; rows written before those fields
existed keep it.

A superseded value goes once its segment lists it and 10 minutes have passed
since `superseded_at_micros`, by command time, which each command stamps on the
garbage rows it adds. Its value row and garbage row are deleted, and its birth
record stays with `is_offloaded`, so LoadBlobs finds the segment that covers
`garbage_seq`. `value_birth` sees no value row, so a reference to an offloaded
value is unknown, as one outside the window is. The pass stops at the first value
still in its grace period. While offload is on, the superseded value byte cap
moves nothing.

## History migration

A run that started while its partition did not offload history has no segment
state. The first command that leaves such a run running while its partition
offloads starts the migration: it sets `history_migration_started_seq` to its
seq and opens the segment at the run's first seq and event 1. While the run
migrates, the open segment holds every unsealed seq up to the current command
and tracks no bytes, and each command of the run seals one segment of it.

A migration segment ends at a whole seq: the first one where it holds at least
two history records and reaches the target bytes, 10,000 superseded values, or
10,000 history rows. Otherwise it ends at the command's own seq, which ends the
migration. Cutting it reads one bounded page of history metadata and of table G.
Because segments end only at whole seqs, a migration step is bounded by the
largest single command in the migrated range, as sealing is: one seq whose
command superseded 30,000 values seals all of them in one step.
Two records per segment let the migration gain on the run, since each command
adds one record. The segment holds the rows still stored in its seqs, so seqs
that window pruning deleted earlier are holes inside the reported pruned event
range. Its event range comes from the metadata `last_assigned_event_id`; records
written before that field are decoded instead. Pruning keeps every row above U
while the partition offloads, so nothing is deleted before its segment uploads.

A closed run never starts migrating, so retention purges its local history
directly. A run that closes during its migration seals one more segment at its
closing command and then keeps the rest local until purge; a time travel that
reopens it continues the migration. Turning offload off ends a migration, and the
unsealed rest joins the gap of commands applied while offload is off.

## History segment reads

`get_history_events_from_segments` runs the GetHistoryEvents read model over a
`HistorySource` that takes each seq from where the segment catalog says: a seq
that an UPLOADED segment covers comes only from that segment's records, which
the caller loaded from object storage into `LoadedHistorySegments`, and every
other seq comes from local rows. So the local genesis copy is never read once it
is uploaded, and commands that belong to no segment still read locally. Records
that window pruning removed locally, below the first HistoryRecord metadata row
after genesis, stay hidden in the segments too. When the read reaches an uploaded
segment that is not loaded, it returns `NeedsSegments` instead of a page; the
caller fetches them and reads again with a new committed view. A caller out of
fetch rounds asks instead for the shorter page of the events before the first
unloaded segment, whose next page token continues at it. The source starts
a page's search from a binary search of local history metadata, so a page loads
only the segments that hold its events. Catalog lookups seek by seq with one-row scans and skip
pruned seqs, so a long run of expired rows is never scanned; a read that examines 4096 catalog
rows ends its page there. Each fetch round names at most 16 MiB of objects, required segments
first, and one larger required object only alone. A page holds at most 1000 events, and a token
added to a shorter page never pushes it over the response limit.

`compute_time_travel_from_segments` runs the time travel computation over the
same source without a catalog budget, so no stream ends early. A checkpoint that
an UPLOADED segment covers comes from that segment's checkpoints, which the
caller loads apart from its records. When the computation reaches unloaded
records or checkpoints, it returns `NeedsSegments` instead of its result: each
reached segment with the later uploaded segments that its stream goes on to
read, at most 1024 segments in all, and the asked-for checkpoints. The caller fetches them and computes again
on a new committed view.

A reapplied external input can name a value by reference. The computation
stages each such value that the store lacks, read from the offloaded values, and
fails with `FAILED_PRECONDITION` when no source has it. Before it forks, apply
checks that the store holds every such value, after it restored the staged ones.
A missing one rejects the TimeTravel with `ABORTED` and its value reference, and
the transaction is discarded, so the owner computes again.

## Value and storage accounting

Each run tracks encoded Flow and History row bytes, including keys. Live and superseded value bytes use the encoded value sizes in birth records; garbage rows identify the superseded portion. Apply updates these totals from each command's writes and deletes. A birth record of an offloaded value counts as history bytes with a zero value size, so offloading changes neither live nor state bytes.

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
