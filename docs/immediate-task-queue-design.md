# Immediate Task Queue Design

This document describes the design of the immediate task queue (ITP —
Immediate Task Processor), which processes tasks that should be executed
as soon as possible (e.g., `run_initial_dispatch_task`, `channel_messages_received_task`).

## 1. Per-Shard Design

Each logical shard has its own independent immediate task processing pipeline:

```
Per-Shard:
  ImmediateBatchReader  -->  [shared WorkerPool]  -->  ImmediateBatchDeleter
       |                           |                          |
   polls DB for tasks       processes tasks           deletes completed tasks
   (cursor-based)           (retries on failure)      (range-based batch)
```

- **ImmediateBatchReader**: one goroutine per shard, polls `runs` collection
  for `row_type=2` (immediate_task) with `shard_id` filter.
- **WorkerPool**: instance-level shared pool across all shards. Tasks from
  all shards are submitted to the same pool for efficient resource usage.
- **ImmediateBatchDeleter**: one goroutine per shard, receives completed task
  IDs and periodically batch-deletes them.

Components are created when a shard is claimed and destroyed when released
(via `ComponentFactory`). Each receives a `ShardHandle.shutdownCh` for
graceful shutdown signaling.

> **Sibling queues**: the timer queue follows the same outbox + per-shard
> seq + min-watermark deleter pattern, see
> [timer-task-queue-design.md](timer-task-queue-design.md). The
> observability queue (OpsFIFO) reuses the SAME outbox-write +
> per-shard-seq guarantees but processes tasks INLINE in the reader
> goroutine instead of submitting them to a worker pool, because per-run
> ordering is required — see [ops-fifo-queue-design.md](ops-fifo-queue-design.md).
> OpsFIFO uses a SEPARATE per-shard seq counter
> (`shardState.opsFIFOLocalSeq` + `opsFIFOTaskSeqMu`) so the observability outbox
> writer never contends with the immediate-task seq path on the hot
> dispatch critical section.

## 2. Durability via Outbox Pattern

Immediate tasks are created **atomically** with run state changes using
MongoDB single-shard transactions:

### StartRun

```
Transaction (same shard_id, same collection):
  INSERT run_row (status=Pending, version=1)
  INSERT immediate_task_row (run_initial_dispatch_task)
```

### ProcessExternalChannelMessagesReceived (run is Running)

```
Transaction:
  UPDATE run_row (append channel messages, increment version)
  INSERT immediate_task_row (external_channel_messages_received_task)
```

### ProcessStepWaitForTimerFired / ProcessExternalChannelMessagesReceived (condition satisfied)

```
Transaction:
  UPDATE run_row (transition step to invoking_execute, status=Pending)
  INSERT immediate_task_row (run_resume_dispatch_task)
```

Because run rows and task rows share the same `shard_id` partition key in the
unified `runs` collection, these transactions are always **single-shard** —
no distributed transaction overhead.

If the transaction succeeds, both the state change and the task are persisted.
If it fails, neither is persisted, and the caller retries.

### Task Types

| Type | Value | Purpose |
|---|---|---|
| `run_initial_dispatch_task` | 0 | Push a new run to matching service for worker pickup |
| `run_resume_dispatch_task` | 1 | Re-push a run after condition satisfaction (same dispatch flow) |
| `external_channel_messages_received_task` | 2 | Deliver external channel messages to the active worker |

## 3. Idempotency Design

Immediate tasks are **at-least-once**: the reader may re-read a task if
it crashes after processing but before deletion. All handlers are idempotent:

### run_initial_dispatch_task / run_resume_dispatch_task

The handler calls `GetRun()` and checks `run.Status`:
- If not `Pending` -> mark done (already processed or picked up).
- If `Pending` -> push to matching, update status.

Even if the matching push succeeds but `UpdateRunWithNewTasks` fails (or
times out), the retry will call `GetRun()` again, see `Running` (if it
actually succeeded), and mark done.

### external_channel_messages_received_task

Best-effort delivery to the worker via matching engine. If the worker no
longer holds the run, the messages are already persisted in
`unconsumed_channel_messages` on the run row and will be available when
the run resumes.

### worker_request_counter

For `StepExecuteCompletedRequest` and `StepWaitForCompletedRequest` (which are
not immediate tasks but use the same idempotency pattern), the
`worker_request_counter` on the run row ensures duplicate processing is a
no-op:
- Same counter as run's -> duplicate, skip.
- Counter = run's + 1 -> proceed.
- Anything else -> protocol error.

## 4. Performance / Efficiency Design

### Batch Reading with Cursor

The reader polls tasks in batches using cursor-based pagination:

```go
PollImmediateTasks(ctx, shardID, afterID, limit=100)
// filter: shard_id=X, row_type=2, namespace="", sort_key=0, id > afterID
// sort: id ASC
// limit: ImmediateBatchReadLimit (default 100)
```

- **afterID** is the cursor — the last task ID read in the previous batch.
- When the batch returns fewer than `limit` tasks, the reader resets the
  cursor to `""` and sleeps for `ImmediatePollInterval` (500ms default).
- Task IDs are UUIDs, which are lexicographically ordered by creation time
  (UUID v4 sorts randomly, but insertion order is preserved by the index).

### Batch Deletion with Range Delete

Instead of deleting tasks one-by-one, the deleter uses a range delete:

```go
DeleteImmediateTasksUpTo(ctx, shardID, upToID)
// DeleteMany: shard_id=X, row_type=2, namespace="", sort_key=0, id <= upToID
```

This single `DeleteMany` removes all processed tasks up to the high-water
mark, which is much more efficient than individual deletes.

### Offset Tracking and Recovery

The deleter tracks two offsets:

- **receivedOffset**: highest task ID received from the worker pool's
  `CompletedChan`.
- **committedOffset**: last offset persisted to shard metadata.

On shard takeover (after crash or rebalance), the new owner reads the
`committedOffset` from `ShardMetadata.ImmediateTaskDeleteOffset` and starts
the reader from there. Tasks between `committedOffset` and the actual
last-processed task may be re-read, but idempotent handlers make this safe.

### Shared Worker Pool

All shards submit tasks to a single instance-level `WorkerPool`:

```
WorkerPool {
    taskChan: buffered channel (numWorkers * 2)
    workers:  numWorkers goroutines consuming from taskChan
}
```

- **Buffered channel** prevents blocking the batch reader when workers are
  busy.
- **Shared across shards** avoids spawning per-shard thread pools, which
  would waste resources when some shards are idle.
- Default `NumWorkers = 4`.

### Retry with Exponential Backoff

Each task is retried with exponential backoff on retriable errors:

```
Initial: 100ms, Max: 30s, Expiration: 1 hour
```

After 1 hour of retries:
- **Retriable errors** (infrastructure): logged at Error level with full
  task details for manual replay.
- **Non-retriable errors** (business logic): logged at Warn level.

### Dead Letter Queue (DLQ)

Currently, permanently failed tasks are written to structured logs with full
task details (task ID, type, run ID, namespace, task info JSON). This serves
as a log-based DLQ — operators can search logs, reconstruct the task, and
replay it manually after fixing the underlying issue.

**Future improvement**: persist failed tasks to a dedicated `dead_letter_tasks`
collection in MongoDB with the original task payload, error message, failure
timestamp, and retry count. This would enable:
- Querying failed tasks by namespace, run ID, or time range
- Automated retry via admin API
- Dashboard visibility without log aggregation

### Configuration

| Parameter | Default | Purpose |
|---|---|---|
| `ImmediateBatchReadLimit` | 100 | Max tasks per DB poll |
| `ImmediatePollInterval` | 500ms | Sleep when queue is empty |
| `ImmediateDeleteInterval` | 5s | How often to batch-delete (+ jitter) |
| `ImmediateDeleteIntervalJitter` | 1s | Jitter on delete interval |
| `ImmediateCommitInterval` | 5s | How often to commit offset to shard metadata (+ jitter) |
| `ImmediateCommitIntervalJitter` | 1s | Jitter on commit interval |

## 5. Graceful Shutdown

When a shard is released (rebalance or server shutdown):

1. **ShardHandle.shutdownCh** is closed.
2. **ImmediateBatchReader**: detects `shutdownCh` in its select loop, stops
   polling, and returns. Any tasks already submitted to the worker pool
   will still be processed.
3. **WorkerPool**: continues processing in-flight tasks. The pool is
   instance-level and outlives individual shards — it only stops when the
   entire server shuts down.
4. **ImmediateBatchDeleter**: detects `shutdownCh`, performs a final delete
   of any pending offsets, and returns.
5. **ShutdownGracefulPeriod** (5s default): the shard manager sleeps to allow
   in-flight operations to complete before releasing the lease in DB.

All DB operations use **capped contexts** (`GetCappedContext`) with deadlines
set to `leaseExpiresAt - LeaseExpiryBuffer`. If the shard is released, the
context is already cancelled, causing any in-flight DB call to fail fast
rather than completing after the new owner has claimed the shard.

### Server-Level Shutdown

```
WorkerPool.Stop()
  -> cancel context
  -> wg.Wait() (waits for all workers to finish current task)
```

The worker pool drains naturally: the context cancellation causes the select
loop to exit, and `wg.Wait()` ensures all in-progress tasks complete (or
their capped contexts expire).
