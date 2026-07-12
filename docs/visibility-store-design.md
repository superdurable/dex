# Visibility Store Design

The visibility store backs the `OpsService.ListRuns` API: it is a
namespace-sharded summary of every run's high-level status, suitable for
"all running flows of type X" dashboards and operator UIs without touching
the run state in the `runs` collection.

## 1. Collection Layout

| Field         | Type      | Notes                                                                 |
|---------------|-----------|-----------------------------------------------------------------------|
| `namespace`   | string    | Tenant / project identifier; shard key.                               |
| `run_id`      | string    | UUID; unique within `namespace`.                                      |
| `flow_type`   | string    | Registered flow type (filter / facet).                                |
| `task_list_name` | string | Tasklist that owns this run's dispatch, mirrored from the run row.    |
| `status`      | int32     | `persistence.RunStatus` enum (Pending..Failed).                       |
| `start_time`  | datetime  | Set on insert via `$setOnInsert` — immutable after first write.       |
| `updated_at`  | datetime  | Bumped on every status transition; for terminal statuses it doubles as the run's end time. |

The collection is sharded by `{namespace: "hashed"}` so all rows for a
single tenant land on one Atlas shard, and `ListRuns(namespace=...)`
queries route to a single shard.

### 1.1 Indexes

```
PK   { namespace: 1, run_id: 1 }                                                 unique
list { namespace: 1, flow_type: 1, status: 1, start_time: -1, run_id: 1 }
list { namespace: 1, flow_type: 1, status: 1, updated_at: -1, run_id: 1 }
```

The PK satisfies "unique by `(namespace, run_id)`" and the MongoDB
shard-key-prefix-of-unique-index requirement (the unique index includes
`namespace` as the leftmost field).

### 1.2 Why `updated_at` instead of a separate `end_time`

The original spec called for a second compound index on `end_time`. We
instead use `updated_at`, with these properties:

- For **terminal statuses** (Completed, Failed) `updated_at`
  equals the moment the run reached its terminal status. Querying
  `(ns, ft, Completed)` ordered by `updated_at` DESC therefore returns
  recently-finished runs — exactly what an `end_time` index would give.
- For **active statuses** the same index doubles as a "recently active"
  view: most-recently-changing runs first.

There is no write-time branch ("if terminal then set end_time, else don't")
and no second mutable field. One column, two query patterns.

## 2. Write Path: `BatchUpsertVisibility`

Writes come exclusively from the OpsFIFO batch executor (see
[ops-fifo-queue-design.md](ops-fifo-queue-design.md)). The store API is:

```go
BatchUpsertVisibility(ctx, entries []VisibilityEntry) errors.CategorizedError
```

Implementation: one `UpdateOne(upsert=true)` per entry inside a single
`BulkWrite(ordered=true)`. The update document is

```
$setOnInsert: { start_time: <ts> }
$set:         { flow_type, task_list_name, status, updated_at }
```

`$setOnInsert` makes the upsert idempotent on `start_time`: even if a
buggy or replayed write supplies a different `StartTime`, the original
value is preserved. Combined with the OpsFIFO writer's pre-batch merge
(see §3) this means every run has exactly one row whose `start_time`
matches the `RunRow.CreatedAt` from the engine's `StartRun` path.

## 3. Batch-Merge Optimization (OpsFIFO writer side)

Before calling `BatchUpsertVisibility`, the OpsFIFO batch reader folds
multiple visibility entries for the same `(namespace, run_id)` into a
single upsert:

| Field                                | Source                          |
|--------------------------------------|---------------------------------|
| `status`, `updated_at`, `flow_type`, `task_list_name` | LATEST entry (highest SortKey). |
| `start_time`                         | EARLIEST non-zero entry.        |

This collapses N rapid status updates per run into one `UpdateOne` and
makes `BatchUpsertVisibility` order-independent against the merged batch.
The DB-side `$setOnInsert` is the belt-and-suspenders guard for cases
where the merge runs across batches.

## 4. Read Path: `ListRuns`

```go
ListRuns(ctx, ListRunsQuery) (*ListRunsResult, errors.CategorizedError)
```

`ListRunsQuery` requires `Namespace` (the index prefix), `FlowType`,
and `Status` — without all three the supported indexes don't apply and
the query would scatter. Additional access patterns require additional
indexes.

`OrderBy` selects between the two compound indexes (`start_time` /
`updated_at`); both orderings are descending.

Pagination is cursor-based with an opaque `<unix_millis>:<run_id>` page
token. The next-page filter is the standard compound-key cursor:

```
{ <orderField>: { $lt: lastTime } }
OR
{ <orderField>: lastTime, run_id: { $gt: lastRunID } }
```

The page size defaults to and is clamped at 1000 (mirrors the OpsService
`GetHistoryEvents` cap).

## 5. Failure / Replay Semantics

- **Replay-safe**: `BatchUpsertVisibility` is idempotent by `(namespace, run_id)`,
  so the OpsFIFO retry loop can replay the same batch without producing
  spurious duplicates or rolling back state.
- **No transactional coupling** to the runs cluster: visibility lives in
  its own database (and may live in its own Mongo cluster), so the
  `runs` cluster can outage independently — the visibility view will lag
  but never corrupt.
- **Lag bound**: the OpsFIFO debounce (default 100 ms) plus one
  `BatchUpsertVisibility` round trip. Under steady state the ListRuns
  view trails the engine by sub-second; during a downstream visibility
  cluster outage the lag grows until the cluster recovers (see
  `ops_fifo_task_lag_latency`).

## 6. Test Coverage

- [`server/internal/persistence/mongo/visibility_store_test.go`](../server/internal/persistence/mongo/visibility_store_test.go)
  — idempotency, ordering, pagination, filter selectivity, namespace-required guard.
- [`server/internal/integration/ops_service_test.go`](../server/internal/integration/ops_service_test.go)
  — `StartRun → OpsFIFO → BatchUpsertVisibility → ListRuns` end-to-end.
