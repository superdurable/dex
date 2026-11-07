# testdata/v1

The baseline format of server-zd before its first release: key layout version 1, metadata
format version 1, behavior version 1, and snapshot format version 1.

## How it was produced

```bash
DEXZD_FORMAT_COMPAT_OUTPUT=$PWD/testdata/v1 \
  cargo test -p dexzd-engine --test format_compat generate_format_compat_testdata -- --ignored
```

The generator ran the real engine node with the deterministic applier, the in-memory object
store, and the in-memory local store. A scripted clock and identifiers derived from seed
`0x5eed_2026_1024` replace wall-clock time and random UUIDs, so a rerun writes the same bytes.
No effect runtime ran; the workload proposed every command itself, including timer firings,
cross-Flow deliveries, acknowledgments, and hard-delete cleanups.

One node, `node-a` with incarnation 1, served a cluster of four partitions. The cluster object
was written before the node started.

## Workload

Before the snapshot:

- `order-sync`: SYNC Steps with a Flow timeout. A WaitFor waits on a timer and a Channel; the
  timer fires. Execute heartbeats with a checkpoint, fails with a Worker error, and succeeds on
  the retry after its backoff timer fires. The success writes Attributes, records an event,
  publishes to a Channel, and moves to a Step that locks an Attribute. A Channel message is
  deleted, and a hard-delete Attribute value stays live to the end. One Attribute is longer
  than the inline threshold and lives in the Value column family.
- `profile-review`: a WaitFor on a Channel keeps the run open while a hard-delete value is
  replaced; the replaced object is deleted and the deletion committed.
- `fulfillment-parent`: a WaitFor stages a SubFlow hard-delete Attribute. The child start
  carries field 6 with a parent-owned staging reference, then a child-owned object reference
  in the delivered InboxMessage. The parent's outbox message is acknowledged and its staging
  object is deleted.
- `account-sync`: a Flow that targets the `accounts` Attribute Store starts with two synced
  Attributes and one unsynced Attribute, then a SetAttributes changes a synced one. Its
  Attribute Sync entries and pending index are pending in the snapshot. The entries carry the
  last synced `plans/basic="trial"` and `status="active"` values.
- Partitions 0 and 1 enable history offload, so the snapshot holds their mode records.

Then the node took a snapshot. After the snapshot:

- The child completes, its completion wakes the parent, and both outbox messages are
  acknowledged. The parent completes gracefully.
- `account-sync`: an Attribute Sync ack up to the start command's seq deletes one entry and
  leaves the newer change pending; a drop ack for that key clears the store's entries.
- `held-close`: a Step decision completes the Flow while the owner stamps a transactional RPC
  as in flight, so the close waits without outbox entries; a pending close check records it.
- `order-sync`: a transactional RPC that locks an Attribute commits, then the locking Step
  runs and completes the Flow.
- `order-async`: ASYNC Steps whose events wait for the batch window. They share a log batch
  with a prompt Channel publication to `profile-review` in another partition. Step-execution
  locals are written.
- `profile-review`: the Channel message completes the wait. Execute clears the hard-delete
  value; the object is deleted and the deletion committed.
- `travel-flow`: two Steps and a client Attribute write complete the run. Time travel to the
  second Step's Execute, with writes reapplied, reopens it in run attempt 2, which completes.
  As in every time travel of this log, the owner first commits the result it computed in a
  TimeTravelStage command, and the TimeTravel command names that staged result.
- `idle-flow`: a timer wait is skipped, the Flow config is updated, and the Flow is canceled.
- `short-lived-flow`: a run with a 60-second retention completes in partition 0, which
  offloads history, so its close seals a segment. A HistorySegmentUploaded acknowledgment
  marks it uploaded, because retention postpones while a segment is SEALED. Then the retention
  timer fires and the run is purged.
- `handout-sync`: an Execute that locks an Attribute is started and returned three times, with
  reasons UNSUPPORTED, LEGACY_NO_CAPACITY, and NOT_DELIVERED, then fails before any start. Its
  retries fail as STARTED_ATTEMPT_INTERRUPTED after a heartbeat with a task ID, as a not-run
  REQUEST_TOO_LARGE, and with a Worker error, which exhausts four attempts. The recovery Step
  receives `attempt_progress` NOT_COMPLETED and completes the Flow. Every hand-out command
  carries its task ID.
- `handout-travel`: a running Execute holding a lock is completed, then a time travel that
  restores running attempts as pending reopens it; it starts again and completes.
- `search-flow`: the search projection of its partition is enabled, an Attribute write queues
  a search outbox entry, and a SearchOutboxAck deletes it.
- Runs that start in partitions 0 and 1 after history offload is enabled keep history segment
  state, and each seals its open segment when it closes.
- `history-flow-3`, in partition 1: a replaced Attribute supersedes a value, the stop seals the
  run's only history segment with that value listed, and a HistorySegmentUploaded
  acknowledgment marks the segment uploaded and removes the run's pending index row.
- Partition 0 disables history offload, which deletes its mode record.
- `legacy-running` and `legacy-closed`, in partition 2, which never offloads history, run with an
  eight-event window, a checkpoint every two commands, and 1,500-byte segments. Thirty commands
  that alternately replace a long `profile` Attribute and publish to a Channel let the window
  prune `legacy-running`, which stays running without segment state. `legacy-closed` applies four
  such commands and is canceled; its 60-second retention timer stays pending.
- `legacy-migrating`, in partition 3, records the same thirty commands. Then partition 3 enables
  history offload, and each of the run's next two commands seals one migration segment.

Every `FlowCommand` kind of behavior version 1 appears in the log. Hand-out commands outside
`handout-sync` and `handout-travel` carry no task ID, as an older log does. Log cleanup did not run,
because no snapshot was older than the deletion delay, so the log keeps every batch.

## Contents

| Path | Content |
| --- | --- |
| `objects/cluster`, `objects/manifest` | Cluster object and manifest |
| `objects/registry/attribute-indexes` | Attribute index registry with one keyword index |
| `objects/log/node-a/0000000001/` | Every log batch, starting with the EpochStart at seq 1 |
| `objects/snapshot/node-a/<lineage>/` | Snapshot manifest 1 and its memory store file |
| `objects/hard-delete/` | The hard-delete values that are still live |
| `state.txt` | Committed state after the last command; see `../README.md` |
