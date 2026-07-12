# Timer Task Queue Design

This document describes the design of the timer task queue (TTP — Timer Task
Processor), which processes tasks scheduled for a future time (e.g.,
`run_heartbeat_task`, `step_wait_for_timer_task`).

## 1. Per-Shard Design

Each logical shard has its own independent timer task processing pipeline:

```
Per-Shard:
  TimerBatchReader  -->  [shared WorkerPool]  -->  TimerBatchDeleter
       |                        |                        |
  polls DB for timers    processes tasks        deletes fired timers
  (TimerGate pattern)    (retries on failure)   (individual delete)
```

- **TimerBatchReader**: one goroutine per shard, polls `runs` collection for
  `row_type=3` (timer_task) with `shard_id` filter. Uses the **TimerGate**
  pattern to sleep until the next timer fires.
- **WorkerPool**: instance-level shared pool across all shards (same pool as
  the immediate task processor).
- **TimerBatchDeleter**: one goroutine per shard, receives completed task IDs
  and periodically deletes them.

Components are created when a shard is claimed and destroyed when released
(via `ComponentFactory`). Each receives a `ShardHandle.shutdownCh` for
graceful shutdown signaling.

## 2. Durability via Outbox Pattern

Timer tasks are created **atomically** with run state changes using MongoDB
single-shard transactions:

### Heartbeat Timer (on run pickup)

```
Transaction:
  UPDATE run_row (status=Running, heartbeat_timer_id=T1)
  INSERT timer_task_row (run_heartbeat_task, sort_key=now+30s, id=T1)
```

### Heartbeat Renewal

```
Transaction:
  UPDATE run_row (last_heartbeat_time=now, heartbeat_timer_id=T2)
  INSERT timer_task_row (run_heartbeat_task, sort_key=now+30s, id=T2)
```

### Durable Timer (all steps waiting)

When `ProcessStepExecuteCompleted` or `ProcessStepWaitForCompleted`
computes that all steps are in `WAITING_FOR_CONDITION`:

```
Transaction:
  UPDATE run_row (status=AllStepsWaitingForConditions, active_durable_timer_id=T3)
  INSERT timer_task_row (step_wait_for_timer, sort_key=minFireAtUnixMs, id=T3)
```

All transactions are **single-shard** because run rows and task rows share
the same `shard_id` in the unified `runs` collection.

### Timer Task Storage Layout

Timer tasks are stored in the `runs` collection with a special compound key:

```
{ shard_id, row_type=3, namespace="", sort_key=fire_at_unix_ms, id=task_id }
```

The `sort_key` field stores the scheduled fire time in unix milliseconds,
enabling efficient range queries for polling.

### Task Types

| Type | Value | Purpose |
|---|---|---|
| `run_heartbeat_task` | 0 | Verify worker liveness, renew or re-match run |
| `step_wait_for_timer` | 1 | Fire durable timer for waiting step condition |

## 3. Idempotency Design

Timer tasks are **at-least-once**: the reader may re-read a task if the
deleter hasn't cleaned it up yet, or if the process crashes. All handlers
are idempotent:

### run_heartbeat_task

```go
run := GetRun()
if run.Status != Running:          return  // stale: run already released
if run.HeartbeatTimerID != taskID: return  // superseded by newer heartbeat
// proceed with heartbeat check
```

Two guards:
1. **Status check**: if the run isn't `Running`, the heartbeat is stale.
2. **Timer ID check**: `heartbeat_timer_id` on the run row is updated each
   time a new heartbeat timer is created. If the current task's ID doesn't
   match, a newer heartbeat has superseded it.

### step_wait_for_timer

```go
run := GetRun()
if run.Status != AllStepsWaitingForConditions: return  // stale
if run.ActiveDurableTimerID != taskID:         return  // superseded
// proceed with evaluate-only condition check (no step modification)
```

Same two-guard pattern. The `active_durable_timer_id` is updated whenever
a new durable timer is created (either from a fresh all-steps-waiting
transition or when re-scheduling after a partial evaluation). Timer fields
are never eagerly cleared — lazy reuse maximizes the value of existing timers.

### Why Timer ID Matching Works

Each timer task has a unique UUID as its ID. When a timer is created, the
same UUID is stored on the run row (`heartbeat_timer_id` or
`active_durable_timer_id`). Only the timer whose ID matches the run row's
field is the "active" timer. All others are stale and silently ignored.

This handles:
- **Duplicate firing**: same timer read twice -> second execution sees
  matching ID, proceeds, but the run state has already been updated by the
  first execution, so the status/counter check catches it.
- **Superseded timers**: old timer fires after a new one was created -> ID
  mismatch, skip.

## 4. Performance / Efficiency Design

### TimerGate Pattern

Unlike the immediate task reader (which polls at a fixed interval), the timer
reader uses an adaptive sleep strategy:

```
loop:
  1. Wait until nextWakeupTime (or newTimerCh notification)
  2. Poll: sort_key <= now + MinLookAheadDuration
  3. Split results into:
     - readyTasks (sort_key <= now) -> submit to WorkerPool
     - futureTask (first task with sort_key > now) -> set nextWakeupTime
  4. If no tasks found -> nextWakeupTime = now + MaxLookAheadDuration
```

This avoids unnecessary polling:
- When there are near-future timers, the reader sleeps exactly until the
  next one fires.
- When the queue is empty, it sleeps for `MaxLookAheadDuration` (60s default).
- When a new timer is inserted (notified via `newTimerCh`), the reader
  wakes up immediately to check.

### Look-Ahead Window

The reader polls with a look-ahead window:

```go
PollTimerTasks(ctx, shardID, sortKeyUpTo=now+MinLookAhead, afterSortKey, afterID, limit)
```

- **MinLookAheadDuration** (1s default): read timers firing in the next 1s.
  Larger = fewer polls but more tasks in memory.
- **MaxLookAheadDuration** (60s default): when the queue is empty, peek one
  timer up to 60s in the future to set the next wakeup.

### Cursor-Based Pagination (Compound)

Timer tasks use a compound cursor `(sort_key, id)`:

```go
filter = {
  shard_id, row_type=3, namespace="",
  sort_key <= sortKeyUpTo,
  $or: [
    {sort_key > afterSortKey},
    {sort_key = afterSortKey, id > afterID}
  ]
}
sort: {sort_key ASC, id ASC}
```

This handles multiple timers with the same `sort_key` (fire time) correctly.

### Individual Timer Deletion

Unlike immediate tasks (which use range delete), timer tasks are deleted
individually:

```go
DeleteTimerTask(ctx, shardID, sortKey, taskID)
// DeleteOne: shard_id=X, row_type=3, namespace="", sort_key=X, id=taskID
```

This is because timer tasks have non-sequential sort keys (fire times), so
a range delete would either miss tasks or delete unprocessed ones. Each timer
is deleted by its exact `(sort_key, id)` coordinate.

### newTimerCh: Instant Wake-Up on Insert

When a new timer task is inserted (e.g., a heartbeat timer or durable timer),
the inserting code can signal `newTimerCh` to wake up the reader immediately.
This avoids waiting for the next poll cycle, reducing latency for
time-sensitive timers.

### Offset Tracking and Recovery

The deleter tracks:

- **receivedSortKey / receivedID**: compound offset from worker pool
  completions.
- **committedSortKey / committedID**: last offset persisted to shard metadata
  (`ShardMetadata.TimerTaskDeleteOffsetSortKey` / `TimerTaskDeleteOffsetID`).

On shard takeover, the new owner reads the committed offset and starts the
reader from there. Timers between the committed offset and the actual
last-processed timer may be re-read, but idempotent handlers make this safe.

### Shared Worker Pool

Same instance-level `WorkerPool` as the immediate task processor. Timer tasks
and immediate tasks share the pool:

```
WorkerPool {
    taskChan: buffered channel (numWorkers * 2)
    workers:  numWorkers goroutines consuming from taskChan
}
```

Default `NumWorkers = 4`. The pool routes to the appropriate handler based on
the `TaskRow` union type (`.Immediate` vs `.Timer`).

### Retry with Exponential Backoff

Same retry policy as immediate tasks:

```
Initial: 100ms, Max: 30s, Expiration: 1 hour
```

After 1 hour:
- **Retriable errors**: Error level log with full task details.
- **Non-retriable errors**: Warn level log.

### Dead Letter Queue (DLQ)

Same log-based DLQ as the immediate task processor. Permanently failed timer
tasks are logged with full details (task ID, type, run ID, namespace, sort_key,
task info JSON) for manual replay. See the immediate task queue design doc for
the planned improvement to a dedicated MongoDB `dead_letter_tasks` collection.

### Configuration

| Parameter | Default | Purpose |
|---|---|---|
| `TimerBatchReadLimit` | 100 | Max tasks per DB poll |
| `TimerMinLookAheadDuration` | 1s | Read timers firing within this window |
| `TimerMaxLookAheadDuration` | 60s | How far to peek for next wakeup when queue is empty |
| `TimerDeleteInterval` | 5s | How often to delete fired timers (+ jitter) |
| `TimerDeleteIntervalJitter` | 1s | Jitter on delete interval |
| `TimerCommitInterval` | 5s | How often to commit offset to shard metadata (+ jitter) |
| `TimerCommitIntervalJitter` | 1s | Jitter on commit interval |

## 5. Graceful Shutdown

When a shard is released (rebalance or server shutdown):

1. **ShardHandle.shutdownCh** is closed.
2. **TimerBatchReader**: detects `shutdownCh` in its select loop (either in
   the wait phase or the poll phase), stops polling, and returns. Any tasks
   already submitted to the worker pool will still be processed.
3. **WorkerPool**: continues processing in-flight timer tasks. The pool is
   instance-level and outlives individual shards.
4. **TimerBatchDeleter**: detects `shutdownCh`, performs a final delete of
   any pending tasks, and returns.
5. **ShutdownGracefulPeriod** (5s default): the shard manager sleeps to allow
   in-flight operations to complete before releasing the lease in DB.

All DB operations use **capped contexts** (`GetCappedContext`) with deadlines
set to `leaseExpiresAt - LeaseExpiryBuffer`. If the shard is released, the
context is already cancelled, causing any in-flight DB call to fail fast.

### What Happens to Unprocessed Timers on Crash

If the server crashes:

1. The shard's lease expires after `LeaseDuration` (30s default).
2. A new owner claims the shard.
3. The new owner's `TimerBatchReader` starts from the committed offset.
4. Any timers that should have fired during the downtime have
   `sort_key <= now`, so they are immediately read and processed.
5. Handler idempotency ensures no duplicate side effects.

The maximum timer delay after a crash is `LeaseDuration` (time for lease to
expire) + `ClaimRetryInterval` (time for new owner to claim). With graceful
shutdown (lease released early), the delay is only the rebalance time.

### Server-Level Shutdown

```
WorkerPool.Stop()
  -> cancel context
  -> wg.Wait() (waits for all workers to finish current task)
```

The worker pool drains naturally: context cancellation exits the select loop,
and `wg.Wait()` ensures all in-progress tasks complete (or their capped
contexts expire).
