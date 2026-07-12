# Task Processing: Batch Read/Delete Design

This document describes the watermark-based batch read and delete system for
the three task queues (immediate, timer, and OpsFIFO). The design maximizes
MongoDB I/O throughput by using range operations instead of per-task
operations.

## 1. Architecture Overview

Each logical shard has SIX goroutines (was four — OpsFIFO adds two more) working
in three independent pipelines:

```
Per-Shard:
  ImmediateBatchReader ─┐
                         ├──>  [shared WorkerPool]   ──>  ImmediateBatchDeleter (BTree pending set + min-watermark)
  TimerBatchReader     ─┘      (parallel processing)      TimerBatchDeleter     (BTree pending set + min-watermark)

  OpsBatchReader  ────INLINE────────────────────────>  OpsBatchDeleter (single atomic int64; no pending set)
  (no worker pool; FIFO per shard; indefinite retry)
```

| Component | Goroutines | Lifetime | Notes |
|-----------|-----------|----------|-------|
| `ImmediateBatchReader` | 1 per shard | claim → release | submits to WorkerPool |
| `TimerBatchReader` | 1 per shard | claim → release | submits to WorkerPool |
| `OpsBatchReader` | 1 per shard | claim → release | INLINE execution; per-shard FIFO; no worker pool |
| `ImmediateBatchDeleter` | 1 per shard | claim → release | BTree pending + min-watermark |
| `TimerBatchDeleter` | 1 per shard | claim → release | BTree pending + min-watermark |
| `OpsBatchDeleter` | 1 per shard | claim → release | atomic int64; whole-batch atomic completion |
| `WorkerPool` | N workers (shared) | instance lifetime | immediate + timer only; ops never submits |

All components are created in `ShardTaskProcessorFactory.StartComponents()` and
receive `ShardHandle.shutdownCh` for graceful shutdown. The OpsFIFO variant
(reader + deleter) is wired only when both `HistoryStore` and `VisibilityStore`
are configured (always true in production); see [ops-fifo-queue-design.md](ops-fifo-queue-design.md)
for the full FIFO + retry-forever semantics.

## 2. Immediate Task Sequencing (TaskSeq)

### Problem

Immediate tasks must be processable via range read/delete. This requires a
monotonically increasing sort key. Random UUIDs as IDs don't guarantee
ordering, so a newly inserted task could land within an already-read
(and potentially already-deleted) range.

### Solution: RangeID + LocalSeq

The `SortKey` field on `ImmediateTaskRow` is now a **TaskSeq**:

```
TaskSeq (int64) = (int64(RangeID) << 32) | int64(LocalSeq)
```

| Component | Type | Storage | Reset |
|-----------|------|---------|-------|
| **RangeID** | int32 | `ShardMetadata.RangeID` in MongoDB | `$inc` on each `ClaimShard` |
| **LocalSeq** | int32 | In-memory `atomic.Int32` in `shardState` | 0 on each claim |

**Allocation**: `ShardManager.GetNextImmediateTaskSeq(shardID)` atomically
increments `localSeq` and composes `(rangeID << 32) | localSeq`. If `localSeq`
reaches `math.MaxInt32`, the shard manager panics (instance restart resets via
new claim).

**Ordering guarantee**: After a shard handoff, the new owner's RangeID is
strictly greater than the previous owner's. Since RangeID occupies the upper
32 bits, all new TaskSeqs are strictly greater than any value the previous
owner wrote:

```
Owner A: RangeID=5, last task = (5 << 32) | 99999 = 21475036159
Owner B: RangeID=6, first task = (6 << 32) | 1    = 25769803777  (always greater)
```

### Where TaskSeq is allocated

The `RunEngine.newImmediateTask()` helper calls `shardManager.GetNextImmediateTaskSeq(shardID)` and sets the result as `SortKey`. All 5 immediate task
creation sites in `run_engine.go` use this helper:

1. `StartRun` — initial dispatch
2. `ProcessExternalChannelMessagesReceived` (AllStepsWaiting → Pending) — resume dispatch
3. `ProcessExternalChannelMessagesReceived` (Running) — channel message task
4. `ProcessHeartbeatTimerFired` — resume dispatch after heartbeat failure
5. `tryProcessStepWaitForTimerFired` — resume dispatch after timer condition satisfied

### Request forwarding

Because TaskSeq allocation requires shard ownership, `StartRun` and
`PublishToChannel` (the only RunsService APIs that create immediate tasks)
forward requests to the shard owner via `RunsServiceHandler.tryForward()`.

Other APIs that only create timer tasks (`ProcessStepExecuteCompleted`,
`ProcessStepWaitForCompleted`) do NOT need forwarding because timer `SortKey`
is timestamp-based. `HandleRunDispatchResult` is an internal engine call (not a
gRPC API) and also only creates timer tasks.

## 3. Timer Task TimeSkewBuffer

### Problem

Timer tasks use `fire_at_unix_ms` as SortKey. Any node can create a timer
task at any time. Without constraints, a task could be inserted with a
`fire_at` that falls within an already-committed (and already-deleted) range.

### Solution

`RunEngineConfig.TimeSkewBuffer` (default 5s) clamps timer task `fire_at`
to be at least `now + TimeSkewBuffer`:

```go
// In createDurableTimerIfNeeded():
minAllowed := time.Now().Add(e.cfg.TimeSkewBuffer).UnixMilli()
if minFireAt < minAllowed {
    minFireAt = minAllowed
}
```

Heartbeat timers (30s into the future) are naturally above the buffer.
Step wait-for timers with very short durations (<5s) get clamped.

### Safety proof

The timer batch reader reads up to `now + TimerMinLookAheadDuration` (default
1s). For a task with `fire_at = T`:

- The task was created at most at `T - TimeSkewBuffer` (because of clamping).
- The reader reads up to `now + MinLookAhead`.
- The reader can only reach `T` when `now + MinLookAhead >= T`, i.e.,
  `now >= T - MinLookAhead`.
- At that point, the task was created at least
  `TimeSkewBuffer - MinLookAhead = 4s` ago.
- Since DB writes are durable well within 4 seconds, the task is guaranteed
  to be readable.

### Validation

`TaskProcessorConfig.Validate()` enforces:

```
TimeSkewBuffer - TimerMinLookAheadDuration >= 1 second
```

This is called at startup in `main.go`. Startup fails if the constraint
is violated.

## 4. Watermark-Based Deletion

### Data Structures

Each deleter maintains:

| Field | Type | Purpose |
|-------|------|---------|
| `pendingSet` | `btree.BTreeG` | Ordered set of in-flight task keys |
| `watermark` | `int64` (immediate) or `(int64, string)` (timer) | Highest safe-to-delete position |
| `completedAbove` | `[]string` | Task IDs completed above watermark (for shutdown cleanup) |
| `doneCh` | `chan TaskCompletion` | Receives completions from worker pool |
| `committedOffset` | same as watermark | The offset persisted to shard metadata |

### Key types

- **Immediate tasks**: `immediateTaskKey{seq int64}` — ordered by `seq` (TaskSeq)
- **Timer tasks**: `timerTaskKey{sortKey int64, id string}` — ordered by `(sortKey, id)`

### Step-by-step flow

```
1. BatchReader polls DB:
     tasks = RangeReadImmediateTasks(shardID, afterSeq=lastSeq, limit=100)

2. For each task:
     deleter.InsertPending(task.SortKey, task.ID)   // add to BTree
     workerPool.Submit(TaskItem{
         DoneCh:  deleter.DoneCh(),                 // completion channel
         TaskKey: TaskCompletion{SortKey, ID},       // key to send on done
     })
     lastSeq = task.SortKey                         // advance reader cursor

3. WorkerPool processes the task (with bounded retry: 3 attempts).
   On completion (success OR failure):
     doneCh <- TaskCompletion{SortKey, ID}

4. BatchDeleter.Run() receives from doneCh:
     pendingSet.Delete(key)                         // remove from BTree
     if key > watermark:
         completedAbove = append(completedAbove, taskID)  // for shutdown
     advanceWatermark:
         if min, ok := pendingSet.Min(); ok && min-1 > watermark:
             watermark = min - 1

5. Periodically (ImmediateDeleteInterval, default 5s + jitter):
     if watermark > committedOffset:
         RangeDeleteImmediateTasks(shardID, watermark)    // single DeleteMany

6. On lease renewal (LeaseRenewInterval, default 10s + jitter):
     metadata = factory.GetMetadataForShard(shardID)      // reads watermarks
     RenewShardLease(shardID, ..., metadata)              // persists atomically
```

### Why always send DoneCh on failure

Failed tasks (retry exhausted) also send to `DoneCh`. Without this, a single
permanently-failing task would block the watermark forever, preventing range
deletion of all subsequent completed tasks. The failed task's dispatch effect
is recovered through higher-level mechanisms (heartbeat timeout re-creates
dispatch tasks for orphaned runs).

### Shutdown cleanup

When `shutdownCh` is closed:

1. **Drain**: read remaining completions from `doneCh`.
2. **DeleteByIDBatch**: tasks in `completedAbove` (completed but above watermark,
   so not range-deletable) are deleted in pages of `ShutdownDeleteBatchSize`
   (default 1000) to avoid overloading MongoDB.
3. Tasks still in `pendingSet` (not completed) are left in the DB for the next
   shard owner to re-process.

## 5. Offset Commit Lifecycle

Offsets are committed to shard metadata **atomically with lease renewal** via:

```go
ShardStore.RenewShardLease(ctx, shardID, memberID, version, duration, metadata)
```

The `metadata` is provided by `ShardTaskProcessorFactory.GetMetadataForShard()`,
which queries the immediate and timer deleters for their current watermarks.

### Shard metadata fields

| Field | Type | Set by |
|-------|------|--------|
| `RangeID` | int32 | `ClaimShard` (`$inc`) |
| `ImmediateTaskCommittedSeq` | int64 | `RenewShardLease` |
| `TimerTaskCommittedSortKey` | int64 | `RenewShardLease` |
| `TimerTaskCommittedID` | string | `RenewShardLease` |

### Shard claim flow

```
1. ClaimShard:
   - $inc metadata.range_id
   - Return Shard with Metadata (including committed offsets)

2. StartComponents:
   - Read committed offsets from ShardHandle.Metadata()
   - Pass to reader (initial cursor) and deleter (initial watermark)

3. Reader starts from committed offset
   - Only reads tasks with SortKey > committedOffset
   - No re-reading of already-processed tasks
```

## 6. MongoDB Operations

### Index

```javascript
// Unified compound index (pk_idx) on runs collection:
{ shard_id: 1, row_type: 1, namespace: 1, sort_key: 1, id: 1 }
```

### Immediate task operations

| Operation | Query |
|-----------|-------|
| **RangeRead** | `shard_id=X, row_type=2, namespace=""`, `sort_key > afterSeq`, sorted by `(sort_key, id)`, limit N |
| **RangeDelete** | `shard_id=X, row_type=2, namespace=""`, `sort_key <= watermark` |
| **DeleteByIDBatch** (shutdown) | `shard_id=X, row_type=2, namespace=""`, `id $in [...]` |

### Timer task operations

| Operation | Query |
|-----------|-------|
| **RangeRead** | `shard_id=X, row_type=3, namespace=""`, `sort_key <= lookAhead`, compound cursor `(sort_key, id) > (afterSK, afterID)` |
| **RangeDelete** | `shard_id=X, row_type=3, namespace=""`, `(sort_key < wmSK) OR (sort_key = wmSK AND id <= wmID)` |
| **DeleteByIDBatch** (shutdown) | `shard_id=X, row_type=3, namespace=""`, `id $in [...]` |

## 7. Metrics

| Metric | Type | Tier | Tags | Emitted by |
|--------|------|------|------|------------|
| `batch_read_success_counter` | Counter | Info | `task_kind` | readers |
| `batch_read_failed_counter` | Counter | Info | `task_kind` | readers |
| `batch_read_count` | Histogram | Info | `task_kind` | readers |
| `range_delete_success_counter` | Counter | Info | `task_kind` | deleters |
| `range_delete_failed_counter` | Counter | Info | `task_kind` | deleters |
| `task_watermark` | Gauge | Info | `task_kind` | deleters |
| `task_pending_set_size` | Gauge | Debug | `task_kind` | deleters |
| `task_seq_allocated_counter` | Counter | Debug | — | shard manager |
| `task_scheduled_to_pickup_latency` | Latency | Info | `task_kind` | worker pool |

## 8. Edge Cases

### Shard handoff mid-processing

Tasks submitted to the worker pool but not completed remain in the DB. The
new owner reads from the committed offset and re-processes them. Idempotent
handlers (see `immediate-task-queue-design.md` section 3) ensure safety.

### Out-of-order completion

If tasks complete out of order (e.g., task 3 completes before task 1), the
watermark does NOT advance until task 1 completes. This is because
`advanceWatermark()` uses `pendingSet.Min()`:

```
Insert: 10, 20, 30    pendingSet = {10, 20, 30}   watermark = 0
Remove: 30             pendingSet = {10, 20}        watermark = 9   (min=10, 10-1=9)
Remove: 10             pendingSet = {20}            watermark = 19  (min=20, 20-1=19)
Remove: 20             pendingSet = {}              watermark = 19  (empty, no change)
```

### Multiple timer tasks at same fire_at

Timer tasks can share a `SortKey` (fire_at_unix_ms). The compound key
`timerTaskKey{sortKey, id}` ensures unique ordering via UUID comparison
within the same `sortKey`. Range delete uses compound comparison:
`(sort_key < wm.sortKey) OR (sort_key = wm.sortKey AND id <= wm.id)`.

### Clock skew across nodes

The `TimeSkewBuffer` (5s default) absorbs clock differences between nodes.
NTP-synchronized nodes typically have <1s skew, leaving 4s of margin.

### LocalSeq overflow

`math.MaxInt32` = 2,147,483,647 tasks per shard claim. If reached, the
shard manager panics. The instance restarts, re-claims with incremented
RangeID, and continues. In practice, this limit is unreachable.

## 9. Configuration

| Config | Default | Location | Description |
|--------|---------|----------|-------------|
| `TimeSkewBuffer` | 5s | `RunEngineConfig` | Minimum timer fire_at offset from now |
| `TimeSkewBuffer` | 5s | `TaskProcessorConfig` | Used for validation only |
| `ShutdownDeleteBatchSize` | 1000 | `TaskProcessorConfig` | Page size for shutdown DeleteByIDBatch |
| `ImmediateDeleteInterval` | 5s | `TaskProcessorConfig` | Range delete frequency for immediate tasks |
| `ImmediateDeleteIntervalJitter` | 1s | `TaskProcessorConfig` | Jitter on delete interval |
| `TimerDeleteInterval` | 5s | `TaskProcessorConfig` | Range delete frequency for timer tasks |
| `TimerDeleteIntervalJitter` | 1s | `TaskProcessorConfig` | Jitter on delete interval |
| `ImmediatePollInterval` | 500ms | `TaskProcessorConfig` | Sleep between empty immediate task polls |
| `ImmediateBatchReadLimit` | 100 | `TaskProcessorConfig` | Max immediate tasks per DB read |
| `TimerBatchReadLimit` | 100 | `TaskProcessorConfig` | Max timer tasks per DB read |
| `TimerMinLookAheadDuration` | 1s | `TaskProcessorConfig` | Timer read upper bound: `now + this` |
| `TimerMaxLookAheadDuration` | 60s | `TaskProcessorConfig` | Timer look-ahead when queue is empty |

## 10. Dead Letter Queue (DLQ)

### Problem

When a task exhausts its retry policy (default 30 minutes for immediate tasks),
`processItem` marks it complete so the watermark can advance and range-delete
can proceed. But the task's side effect (e.g., dispatching a Pending run) was
never achieved. Without a record of the failure, the run is stuck forever with
no visibility into what went wrong.

### Solution: `task_dlq` collection

Failed tasks are written to a `task_dlq` collection with full diagnostic
context before the completion signal is sent. This preserves the task's
identity and failure reason for operator inspection and replay.

### Collection schema

```
task_dlq: {
  _id:            ObjectId (auto),
  shard_id:       int32,           // same shard as the original task
  task_id:        string,          // original task UUID
  task_type:      int32,           // ImmediateTaskType enum
  run_id:         string,
  namespace:      string,
  task_list_name: string,
  sort_key:       int64,           // original TaskSeq
  error:          string,          // final error message after retries
  error_category: string,          // e.g. "conflict", "unavailable"
  created_at:     Date,            // when original task was created
  dlq_at:         Date,            // when written to DLQ
  member_id:      string           // which instance wrote the entry
}
```

Shard key: `{shard_id: "hashed"}` (consistent with `runs` collection).
Index: `{shard_id: 1, dlq_at: 1}` for time-ordered polling per shard.

### Write path

```
processItem(item):
  err = processWithRetry(item)      // retry for up to 30 minutes
  if err != nil && item is immediate:
    dlqStore.WriteDLQ(entry)         // best-effort, logged on failure
  doneCh <- TaskCompletion           // always advance watermark
```

DLQ writes are best-effort: if the write itself fails, the error is logged
with `CounterTaskDLQWriteFailed` (Critical tier) but the watermark still
advances. The task is lost in this case, but the critical metric alerts
operators.

### Operational tooling

`deploy/gcp-gke/dlq-ops.js` provides three actions:

| Action | Description | Env vars |
|--------|-------------|----------|
| `inspect` (default) | Summary counts + 10 most recent entries | -- |
| `replay` | Re-enqueue DLQ entries as dispatch tasks | `DEX_DLQ_SHARD`, `DEX_DLQ_LIMIT` |
| `purge` | Delete entries older than N hours | `DEX_DLQ_HOURS` (default 24) |

### Metrics

| Metric | Type | Tier | Description |
|--------|------|------|-------------|
| `task_dlq_written_counter` | Counter | Info | Task written to DLQ |
| `task_dlq_write_failed_counter` | Counter | Critical | DLQ write failed (task truly lost) |
