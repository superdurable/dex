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

## Active Step types

A run's search document lists its active Step types. The run record keeps the
run's active Step executions counted by Step type and WaitFor use. Each command
adjusts the counts by the Step execution rows it changed, so its cost does not
grow with the Steps the run has executed. TimeTravel, a switch from DISABLED,
and a run recorded without counts count every Step execution once. While active
Step search is DISABLED, the run record keeps no counts and apply reads no Step
execution for search. The counts are an internal cache, so `state_row_bytes` and
the run-state limit leave them out, and a command is admitted exactly as without
them.

## Ended Step rows

A command deletes the StepExecution row of every Step it ends when
WaitForStepCompletion answers Completed for that Step with or without the row:
a Step that completed, or one that a cancellation without a Flow close ended.
The deletion runs at the end of the command, live and in a history rebuild,
after the command resolved its event links and recorded its pending method
calls and before its checkpoint and storage accounting. Input that a TimeTravel
reapplies leaves the deletion to the TimeTravel's own end, which resolves the
reapplied events' links. The TimeTravel also deletes such rows that it restores
from older checkpoints.

A Failed Step, a Step that a Flow close canceled, a cancellation recorded before
`is_canceled_by_flow_close` existed, and the Flow timeout handler keep their
rows, because their answer, or a second handler start, depends on them. Step
execution numbers come from the StepCounter table, so a deleted row never frees
its number. The StepExecution table therefore holds the active Steps plus those
kept rows, and checkpoints and the run state limit grow with them only.

Checkpoints, snapshots, and staged TimeTravel results that a binary from before
this deletion wrote still hold such rows. A TimeTravel deletes the ones it
restores, in its own command. The debug semantic hash that compares a TimeTravel
target with the live state skips them on both sides, because a deletable row
answers every read as an absent row does.

A run recorded before this deletion keeps such rows. The run record's
`ended_step_row_sweep` tracks deleting them: each live command visits at most
256 Step rows after the last one the previous command visited, deletes the
deletable ones, and marks the sweep complete when a page ends short. FlowStart
records it complete, and a TimeTravel starts it again because its restored rows
may come from an older checkpoint. A replay never sweeps, since the progress is
run-scoped, so a rebuilt state may keep rows the live state deleted, which the
debug semantic hash also skips.

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

Because deletion leaves gaps in that metadata, reads of uploaded history do not
take the window's lower end from the first metadata row. Window pruning records
the last seq it deleted in `history_window_pruned_through_seq`, and reads show
the seqs after it. A run that has not recorded it gets it from its first kept
metadata row before deletion starts.

A superseded value goes once its segment lists it and 10 minutes have passed
since `superseded_at_micros`, by command time, which each command stamps on the
garbage rows it adds. Its value row and garbage row are deleted, and its birth
record stays with `is_offloaded`, so LoadBlobs finds the segment that covers
`garbage_seq`. `value_birth` sees no value row, so a reference to an offloaded
value is unknown, as one outside the window is. The pass stops at the first value
still in its grace period. While offload is on, the superseded value byte cap
moves nothing.

`ValueReviveCommand` restores such a value, so a command that references it
resolves it. Like the acknowledgment, it records no history, takes no run seq,
and is not bound to an attempt. When the run's birth record still marks the value
offloaded and `garbage_seq` is at or after the window start, apply writes the
value row, clears `is_offloaded`, and writes the garbage row again at
`garbage_seq`, stamped with the command's time, so the value is superseded where
it was. Local deletion has usually passed that seq, so a revived value that no
command holds again stays local until window pruning deletes it, as it does any
superseded value. The command must carry a value of the size
its birth record holds; otherwise it is rejected with INVALID_ARGUMENT and writes
nothing. For a value that is local, has no birth record, or that the window
passed, it replies OK and writes nothing. A missing or purging run replies
NOT_FOUND. `offloaded_command_references` lists the
offloaded values that a Worker result, Client write, or RPC result references,
using the same values apply resolves as references.

## Segment expiry

After window pruning, each command of a run with segment state marks EXPIRED, in
seq order, the UPLOADED segments that start after genesis and end at or before
`history_window_pruned_through_seq`, stamps `expired_at_micros` with its proposal
time, and moves `history_expired_through_seq` to the last one. No read uses an
EXPIRED segment: the window shows no seq inside it, and the read paths treat only
UPLOADED segments as uploaded. The segment that holds genesis never expires, since
reads of seqs at or below U, genesis included, use uploaded objects. Expiry deletes the segment's `HistorySegmentValue`
rows and the birth record of each listed value that is still offloaded, names the
listed superseded seq, and has no value row. Time Travel or a revival restored any
other such value, or a later segment lists it, so its birth record stays. One
command expires at most 256 segments and deletes at most 10,000 listed values; a
segment that lists more expires alone, since its seal wrote as many rows in one
command. Expiry does not depend on the offload mode or on the delete switch.

The delete switch is the run's `FlowConfig.delete_expired_history_objects`, or
else the `RunSettings.delete_expired_history_objects` the run recorded at start.
The `HistoryObjectDeletePending` index row exists exactly while the switch is on
and an EXPIRED segment remains, which is while `history_expired_through_seq` is
greater than `history_objects_deleted_through_seq`. Expiry with the switch on
emits `HistorySegmentsExpired`; turning the switch on with UpdateFlowConfig writes
the row and emits it for the backlog, and turning it off deletes the row.
`HistorySegmentObjectsDeletedCommand` records no history, takes no run seq, and is
not bound to an attempt. It deletes each named catalog row while that row is the
oldest EXPIRED one, so objects are recorded deleted oldest first, and it stops at
the first name that is not. A command that deletes nothing replies OK without
writes. Purge deletes the index row with the run's other index rows.

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
fails with `FAILED_PRECONDITION` when no source has it. It asks for every such value, and
every value the restored state holds, before it fails, so an owner learns all the offloaded
values it must fetch from one computation. Before it forks, apply
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
