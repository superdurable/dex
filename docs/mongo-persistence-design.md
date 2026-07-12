# MongoDB Persistence Design

This document describes how dex uses MongoDB for persistence, covering
the collection layout, indexing and sharding strategy, CAS-based concurrency
control, the transactional outbox pattern, and the deliberate avoidance of
cross-collection transactions.

## 1. Collections Overview

dex uses 7 MongoDB collections, grouped into 6 logical "stores" that
each get their own Mongo connection and are configured independently via
[`PersistenceConfig.Mongo`](../server/config/persistence.go) (see §1.1):

| Collection    | Logical store | Purpose                                                              | MongoDB-Sharded | Shard Key            |
|---------------|---------------|----------------------------------------------------------------------|-----------------|----------------------|
| `runs`        | runs          | Run rows + immediate tasks + timer tasks + **OpsFIFO tasks** (unified) | Yes             | `shard_id` (hashed)  |
| `task_dlq`    | runs          | Dead-letter queue for failed tasks (co-located with runs)            | Yes             | `shard_id` (hashed)  |
| `blobs`       | blobs         | Large value storage (encoded objects) — own cluster for storage tiering | Yes             | `shard_id` (hashed)  |
| `shards`      | shards        | Logical shard ownership/leasing                                      | Yes             | `_id` (hashed)       |
| `tasklist`    | tasklists     | Tasklist metadata + tasks (Cadence-style fenced ownership)           | Yes             | `tasklist_key` (hashed) |
| `visibility`  | visibility    | Per-namespace run listing for `OpsService.ListRuns`              | Yes             | `namespace` (hashed) |
| `history`     | history       | Per-run append-only event log for `OpsService.GetHistoryEvents`      | Yes             | `run_id` (hashed)    |

Each store implementation (`RunStore`, `BlobStore`, `ShardStore`,
`TasklistStore`, `VisibilityStore`, `HistoryStore`, `DLQStore`) opens
its own `*mongo.Client` and may point to a completely different
MongoDB cluster. Transactions never cross collection boundaries — and
never cross stores.

Note: `blobs` lives in its own logical store (and its own database by
default) so operators can host blob payloads on dedicated, possibly
cheaper-tier storage independently of the run state. `task_dlq` stays
co-located with `runs` because DLQ rows reference task rows by
`(shard_id, task_id)` and there's no operational reason to separate them.

### 1.1 Per-Store Mongo Configuration

`PersistenceConfig.Mongo` ships defaults at the top level (URI + timeouts)
plus a per-store block (database is per-store only):

```yaml
persistence:
  mongo:
    uri: mongodb://default-cluster        # default URI; per-store inherits
    visibility:
      uri: mongodb+srv://viz-cluster      # override URI for visibility only
      database: viz_db                    # override database too
    history:
      database: hist_db                   # same cluster as parent, different DB
```

The resolver `MongoPersistenceConfig.For(store)` returns a fully-resolved
`MongoConfig` for the named store. `DefaultMongoPersistenceConfig` ships
distinct per-store database names (`dex_runs`, `dex_visibility`,
etc.) so the production schema in [`v0.js`](../server/internal/persistence/mongo/schema/v0.js)
and the integration tests both work without operator configuration.
Single-cluster deployments only need to set the parent `uri` (or
`DEX_MONGO_URI`) — every store reuses that connection while still
landing in its own database.

The store name aliases are defined in [`server/config/persistence.go`](../server/config/persistence.go):
`shards`, `runs`, `blobs`, `tasklists`, `visibility`, `history` are the
six distinct logical clusters; `dlq` is an alias for `runs` since DLQ
rows reference task rows in the same collection. (Earlier iterations
had `blobs` aliased to `runs` too — that was changed so blob payloads
can live on a dedicated cluster.)

## 2. The `runs` Collection: Unified Multi-Row-Type Design

The `runs` collection stores four row types in a single collection:

| RowType | Value | Description |
|---|---|---|
| `run_row` | 1 | The run's core state (status, steps, channels, counters, `last_history_event_id`) |
| `immediate_task_row` | 2 | Tasks processed immediately (run_dispatch, channel_received) |
| `timer_task_row` | 3 | Tasks scheduled for future fire time (heartbeat, durable timer) |
| `ops_fifo_task_row` | 4 | Per-shard FIFO observability outbox tasks (history + visibility writes); see [ops-fifo-queue-design.md](ops-fifo-queue-design.md) |

### Why Unified?

Storing run rows and task rows in the same collection enables **single-shard
MongoDB transactions** for the outbox pattern (see Section 4). Since all row
types share the same `shard_id`, they hash to the same chunk in the `runs`
collection and therefore land on the same MongoDB physical shard, making
transactions cheap (single-shard) rather than expensive (distributed).

This guarantee holds even with hashed sharding because the hash is
deterministic per `shard_id` value and the chunk-to-shard mapping for a
single collection is stable: same `shard_id` → same chunk → same physical
shard. (Cross-collection co-location, however, is not guaranteed; see
"Trade-off vs range sharding" in the MongoDB Sharding subsection below.)

### Primary Key / Compound Index

A single compound unique index serves as PK, task polling, and future FIFO:

```javascript
{ shard_id: 1, row_type: 1, namespace: 1, sort_key: 1, id: 1 }  // unique
```

The `sort_key` and `namespace` fields have overloaded meanings per row type:

| Row Type | namespace | sort_key | id |
|---|---|---|---|
| `run_row` (1) | actual namespace | `0` | run_id (UUID) |
| `immediate_task_row` (2) | `""` (empty) | `0` | task_id (UUID) |
| `timer_task_row` (3) | `""` (empty) | `fire_at_unix_ms` | task_id (UUID) |

This single index supports all access patterns:

- **PK lookup**: exact match on all 5 fields.
- **Timer polling**: prefix `(shard_id, 3, "")` + range `sort_key <= now`,
  ordered by `(sort_key, id)`.
- **Immediate task polling**: prefix `(shard_id, 2, "", 0)` + cursor on `id`.
- **Range deletion**: immediate tasks deleted by `id <= upToID`; timer tasks
  deleted by exact `(sort_key, id)`.

### MongoDB Sharding

The `runs` collection uses **hashed sharding** on `shard_id`:

```javascript
db.runs.createIndex({ shard_id: "hashed" });
sh.shardCollection("dex.runs", { shard_id: "hashed" });
```

`blobs` and `history` use the same `{ shard_id: "hashed" }` shard key. The
small/lookup-only collections (`shards`, `tasklist`) use hashed sharding
on their composite `_id` / `tasklist_key`.

#### Why hashed instead of range

Hashed sharding gives us, from day one and without any operator action:

- **Even pre-allocation across shards.** `shardCollection` with a hashed
  key immediately creates multiple empty chunks and distributes them across
  every Atlas shard. There is no `[MinKey, MaxKey)` warm-up chunk and no
  `splitAt + moveChunk` dance.
- **No write hot spots from a non-uniform `shard_id` distribution.** With
  range sharding, any clustering of `shard_id` values (e.g. only the lower
  range of `[0, MaxShards)` is in use, or workers preferentially claim a
  contiguous block) sends most writes to a single physical shard, even
  though many chunks exist on paper. Hash-spreading the same `shard_id`
  values across the key space defuses that.
- **Equality-on-`shard_id` queries still route to a single shard.** All
  hot read/write paths (`GetRun`, immediate/timer task polling, range
  delete) filter by an exact `shard_id`, so they remain
  single-shard-targeted under hashed sharding.

#### Why this still allows the compound unique index

MongoDB lets a hashed shard key coexist with a compound unique index iff
the shard key field is a **prefix** of that unique index. The `runs` PK is

```javascript
{ shard_id: 1, row_type: 1, namespace: 1, sort_key: 1, id: 1 }  // unique
```

`shard_id` is the leading field, so `shardCollection({ shard_id: "hashed" })`
satisfies the prefix rule and the unique constraint continues to be
enforced cluster-wide. This is verified end-to-end by
`TestUniqueConstraintWithHashedSharding` in
`server/internal/persistence/mongo/shard_distribution_test.go`, which
asserts both that a duplicate insert through `RunStore.CreateRunWithTasks`
is rejected as a `conflict`-category `CategorizedError` and that the
underlying driver error still satisfies `mongo.IsDuplicateKeyError`.

#### Trade-off vs range sharding

The thing hashed sharding **does not** preserve is **cross-collection
co-location**: with hashed shard keys, each collection hashes `shard_id`
into its own chunk layout, and MongoDB does not promise that
`runs[i] / blobs[i] / history[i]` for the same `shard_id = i` end up on
the same physical Atlas shard. In practice — when these collections are
sharded at the same time and start with identical chunk counts and
balancer placement — they do co-locate today, but the design must not
rely on that:

- **No cross-collection transactions** are used (see Section 5), so
  losing the strict guarantee does not break correctness.
- **Single-shard transactions in `runs`** (outbox: `run_row` +
  `immediate_task_row` for the same `shard_id`) remain single-shard,
  because they live entirely inside one collection.
- **Reading blobs for a run** may, in the worst case, hit a different
  physical shard than the run's `runs` row. This is an extra network hop
  but still a single-shard query per collection.

Distribution is verified empirically by
`TestShardDistribution_RandomShardIDs`, which writes through every store
API with random `shard_id` values and aggregates per-shard document
counts via `$shardedDataDistribution`.

## 3. CAS (Compare-And-Swap) Concurrency Control

### Run Row Versioning

Every `RunRow` has a `version` field (int64, starting at 1). All writes use
**optimistic concurrency control**:

1. **Read**: `GetRun()` returns the current `version`.
2. **Write**: `UpdateRunWithNewTasks()` includes `version` in the filter:

```go
filter := bson.M{
    "shard_id": shardID, "row_type": RowTypeRun,
    "namespace": namespace, "sort_key": 0, "id": runID,
    "version": expectedVersion,   // CAS check
}
updateDoc["$inc"] = bson.M{"version": 1}  // increment on success
```

If `MatchedCount == 0`, the version has changed (concurrent modification).
The caller receives `(false, nil)` and can retry or handle accordingly.

### Why CAS Instead of Locks

- **No lock contention**: operations on different runs within the same shard
  never block each other.
- **Simpler implementation**: no distributed lock manager needed.
- **Natural retry**: task processors already retry on failure, so CAS
  conflicts are handled by the existing retry loop.

### Where CAS Is Used

| Operation | CAS Field | On Conflict |
|---|---|---|
| `UpdateRunWithNewTasks` | `run.version` | Return `(false, nil)`, caller retries |
| `ClaimShard` | `shard.version` | Return `VersionMismatchError` |
| `RenewShardLease` | `shard.version + member_id` | Signal shard lost |
| `ReleaseShard` | `shard.version + member_id` | Log warning, continue |
| `ClaimTasklist` | `tasklist.range_id` (monotonic) | Return current `range_id` (the new owner uses it as their fence) |
| `CreateTasks` / `UpdateTasklistMetadata` | `tasklist.range_id` | Return `RangeIDMismatchError` (caller treats as ownership lost) |

### Shard Store CAS

The `shards` collection uses the same version-based CAS. `ClaimShard` checks
both `version` and lease expiry:

```go
// CAS update: only succeeds if version matches
coll.FindOneAndUpdate(ctx,
    bson.M{"_id": shardID, "version": oldVersion},
    bson.M{"$set": bson.M{"version": newVersion, "member_id": memberID, ...}},
)
```

## 4. Transactional Outbox Pattern

### Problem

When a run state changes, we often need to atomically create task rows
alongside the run update. For example:

- `StartRun`: insert `run_row` + `immediate_task_row(run_initial_dispatch_task)`
- `ProcessStepExecuteCompleted`: update `run_row` + optionally insert
  `timer_task_row(step_wait_for_timer)` or `immediate_task_row(run_resume)`
- `ProcessExternalChannelMessagesReceived`: update `run_row` + insert
  `immediate_task_row(channel_messages_received_task)`

If the run update succeeds but the task insert fails (or vice versa), the
system enters an inconsistent state.

### Solution

All run+task writes use a **single MongoDB transaction** within the `runs`
collection:

```go
sess.WithTransaction(ctx, func(sc mongo.SessionContext) (interface{}, error) {
    // 1. Update run_row (CAS on version)
    coll.UpdateOne(sc, filter, updateDoc)

    // 2. Insert task rows
    for _, task := range newTasks {
        coll.InsertOne(sc, taskRowToDoc(task))
    }
    return nil, nil
})
```

Because all row types share `shard_id` as the MongoDB shard key, this is a
**single-shard transaction** — it hits only one MongoDB physical shard and
avoids the performance overhead of distributed transactions.

### Task Processing Guarantees

Task rows are **at-least-once**: the task processor may re-execute a task if
it crashes after processing but before deletion. All task handlers are
designed to be **idempotent**:

- `run_initial_dispatch_task`: checks `run.status == Pending` before proceeding.
- `StepExecuteCompletedRequest`: checks `worker_request_counter`.
- `heartbeat_timer`: checks `heartbeat_timer_id` matches.
- `step_wait_for_timer`: checks `active_durable_timer_id` matches.

## 5. Collection Isolation: No Cross-Collection Transactions

### Design Principle

Each store interface (`RunStore`, `BlobStore`, `ShardStore`,
`TasklistStore`, `VisibilityStore`, `HistoryStore`) is independent. They
may connect to different MongoDB instances. **Transactions never span
multiple collections.**

### How This Works Per Operation

**StartRun** (RunStore only):
- Single transaction: insert `run_row` + insert `immediate_task_row`.
- Blob writes (if input is an encoded object) happen **before** the
  transaction. If the transaction fails, the blob becomes an orphan (cleaned
  up by garbage collection, not yet implemented).

**ProcessStepExecuteCompleted** (RunStore + BlobStore):
1. Write blobs to `BlobStore` (separate operation, no transaction).
2. Single transaction in `RunStore`: CAS update `run_row` + insert new tasks.
- If step 2 fails, blobs from step 1 are orphans. This is acceptable because:
  - Orphan blobs are harmless (referenced by no run).
  - Blob IDs use UUID v7 (time-based), making age-based cleanup easy.

**ProcessExternalChannelMessagesReceived** (RunStore + BlobStore):
- Same pattern: blobs first, then run transaction.

### Why Not Cross-Collection Transactions?

1. **Performance**: MongoDB distributed transactions (multi-shard or
   multi-collection) have significant overhead — coordination across mongos,
   two-phase commit, and lock contention.
2. **Operational flexibility**: stores can live on different MongoDB instances
   or even different database systems in the future.
3. **Simplicity**: each store's transaction scope is self-contained and easy
   to reason about.
4. **Acceptable trade-off**: the only downside is potential orphan blobs,
   which are harmless and can be garbage-collected by age.

## 6. Per-Collection Index and Sharding Details

### `runs` Collection

```
Index: { shard_id, row_type, namespace, sort_key, id }  UNIQUE
Shard Key: { shard_id }  HASHED
```

All hot queries include an **equality** filter on `shard_id` (PK lookup,
timer/immediate polling, range delete), so they are routed to a single
shard. Hashed sharding is allowed alongside the compound unique index
because `shard_id` is the leading field of that index (see "Why this
still allows the compound unique index" in Section 2's MongoDB Sharding
subsection).

### `blobs` Collection

```
Index: { shard_id, namespace, run_id, id }  UNIQUE
Shard Key: { shard_id }  HASHED
```

Blob IDs are UUID v7 (time-based). Blob queries always filter by
`shard_id` and route to a single shard. Co-location with the corresponding
`runs` row is **not guaranteed** under hashed sharding (see "Trade-off vs
range sharding" in Section 2's MongoDB Sharding subsection), which is
acceptable because no transaction spans `runs` and `blobs`.

## 6.1 Production Atlas Deployment Notes

For production, dex expects a **MongoDB Atlas sharded cluster**,
not just a single replica set, because every persistence collection is
sharded — the small-and-fixed ones (`shards`, `tasklist`) on hashed
composite IDs, and the high-traffic ones (`runs`, `blobs`, `history`)
on hashed `shard_id`.

The recommended connection pattern is **URI-first**:

- use a full `mongodb+srv://...` Atlas URI
- inject it via environment variable / Kubernetes Secret
- reference it from config as `Persistence.Mongo.URI`
- keep driver auth, TLS, and SRV behavior in the URI instead of splitting
  username/password/authSource fields in application config

The database name is configurable separately as `Persistence.Mongo.Database`
(default `dex`), but authentication and transport should normally stay in
the Atlas URI itself.

This keeps deployment simpler and aligns with Atlas defaults such as SRV record
resolution, TLS, and SCRAM authentication.

Batch operations:
- `BatchInsertBlobs`: `InsertMany` with all blobs for a single run.
- `BatchGetBlobs`: `Find` with `id: {$in: blobIDs}` filtered by
  `(shard_id, namespace, run_id)`.

## 6.2 Shard-key strategies: two families

Collections fall into two families that deliberately use **different**
shard key strategies. The short answer is that the `runs` / `blobs` /
`history` family partitions by the engine's own logical `shard_id`
(there is one), while the `tasklist` / `shards` family has no such
`shard_id` to hash on and instead partitions by the **composite natural
key** of the row, stored as a single string `_id` (or a separate
`tasklist_key` field that mirrors that composite).

| Family | Collections | Shard key | Why this key |
|---|---|---|---|
| Run execution | `runs`, `blobs`, `history` | `{ shard_id: "hashed" }` | Every operation already carries a logical `shard_id`; hashing it spreads writes evenly and same-`shard_id` rows stay single-shard. |
| Tasklist state | `tasklist`, `shards` | `{ _id: "hashed" }` or `{ tasklist_key: "hashed" }` | No logical `shard_id` exists for these rows; they are addressed by `(namespace, task_list_name, partition_id[, task_id])`. The composite string gives a single field to hash. |

### Why not `{ namespace: 1, task_list_name: "hashed" }` (compound hashed) for tasklist?

At first glance a compound hashed shard key with a non-hashed prefix looks
attractive: different `task_list_name`s inside the same namespace would spread
across shards, same `(namespace, task_list_name)` stays on one shard, and no
namespace hot shard. It is also compatible with a compound unique index on
`(namespace, task_list_name, partition_id[, task_id])` via the prefix rule.

The blocker is MongoDB's initial-chunk behavior for this shape of shard
key. Per the [v8.0 Hashed Sharding — Shard an Empty Collection](https://www.mongodb.com/docs/v8.0/core/hashed-sharding/#shard-an-empty-collection)
docs:

> If the compound hashed shard key has one or more non-hashed fields as the
> prefix (i.e. the hashed field is not the first field in the shard key):
> with no zones and zone ranges specified for the empty or non-existing
> collection and `preSplitHashedZones` is `false` or omitted, **MongoDB
> does not perform any initial chunk creation or distribution** when
> sharding the collection.

In other words, on `shardCollection` the collection starts with a
**single `[MinKey, MaxKey)` chunk on one physical shard**, and every
write lands on that one shard until the balancer notices and splits.
We observed this directly in the local 2-shard cluster: with the
equivalent `{ namespace: 1, task_list_name: "hashed" }` shape on the
`tasklist` collection, all 128 / 256 test documents ended up on a
single shard. To distribute on day one we would need to configure
zones and `preSplitHashedZones`, which is a significant operational
burden for a small-collection workload.

Contrast this with single-field hashed (`{ _id: "hashed" }` /
`{ tasklist_key: "hashed" }`), where the same section of the docs says the
shard operation **creates 1 chunk per shard by default** and migrates
across the cluster. Starting empty-then-growing just works.

### Why not `{ namespace: "hashed", task_list_name: "hashed" }` (hash both)?

The other instinct is "just hash both fields so neither can be a hot
prefix". MongoDB does not support this. Per the [v8.0 Create a Hashed
Index](https://www.mongodb.com/docs/v8.0/core/indexes/index-types/index-hashed/create/)
docs:

> When you create a compound hashed index, you must specify `hashed` as
> the value of **a single index key**. For other index keys, specify the
> sort order (`1` or `-1`).

The server enforces this explicitly. Against our local 2-shard cluster:

```
createIndex({ namespace: "hashed", task_list_name: "hashed" })
  → "A maximum of one index field is allowed to be hashed
     but found 2 for 'key' { namespace: 'hashed', task_list_name: 'hashed' }"

sh.shardCollection(..., { namespace: "hashed", task_list_name: "hashed" })
  → "Shard key { namespace: 'hashed', task_list_name: 'hashed' } can contain
     at most one 'hashed' field"
```

So the legal shard-key shapes are exactly three:

| Shape | Example | Auto pre-split on 8.0? |
|---|---|---|
| Single-field hashed | `{ x: "hashed" }` | Yes, 1 chunk per shard |
| Compound hashed, hashed field first | `{ x: "hashed", y: 1 }` | Yes, 1 chunk per shard |
| Compound hashed, non-hashed prefix | `{ x: 1, y: "hashed" }` | No — needs zones |

### How the composite string gets us the "hash both fields" effect

Because we can only hash one field, we concatenate the two at the
application layer and hash the concatenation:

```
_id          = namespace + "/" + task_list_name + "/" + partition_id  (metadata: "m/" prefix; tasks: "t/" prefix + "/" + task_id)
tasklist_key = namespace + "/" + task_list_name + "/" + partition_id
```

`hash(namespace + "/" + task_list_name + "/" + partition_id)` distributes
different `(ns, tln, partition)` triples across the hash space just as
well as hashing the fields independently would. This gives us the same
"no namespace hot shard" behavior we originally wanted from
`{ namespace: 1, task_list_name: "hashed" }`, but via a **single hashed
field** that MongoDB is happy to auto pre-split.

Summarizing what the composite-string approach buys us:

1. A single hashed field to shard on, which triggers the 8.0 auto
   pre-split behavior with no zone configuration.
2. Effective "hash on multiple fields" — MongoDB won't let us declare
   that directly, but concatenating at the application layer is
   equivalent for distribution purposes.
3. Natural cluster-wide uniqueness on the composite key for tasklist
   metadata rows (without needing a separate unique index).

### Why `tasklist` splits `_id` and `tasklist_key`

The `tasklist` collection holds two row types — metadata (one per
partition) and tasks (many per partition) — but shards on a
**separate, coarser** `tasklist_key` field rather than on `_id`. The
reason is granularity:

- `_id` for a task row is `t/<tasklist_key>/<task_id>`, unique per
  `(ns, tln, partition, task_id)`. If we sharded on `_id`, every task
  inside one partition would hash independently, scattering the
  partition's queue across all shards and breaking the single-shard
  routing that `CreateTasks` / `GetTasks` / `DeleteTasksLessThan`
  rely on for the fenced metadata-update + task-write transaction.
- `tasklist_key = namespace + "/" + task_list_name + "/" + partition_id`
  → same value for the metadata row AND every task in the partition.
  Sharding on it keeps the partition's metadata + queue co-located
  on a single physical shard.

So on `tasklist`:

- `_id` provides cluster-wide uniqueness (includes `task_id` for tasks).
- `tasklist_key` is the shard-routing key (excludes `task_id`, coarser).

The redundancy between `tasklist_key` and the prefix of `_id` is
intentional — storing the routing key as its own field lets MongoDB
index it and use it in the `$shardKey` portion of filter/upsert
operations without having to parse the composite `_id` string.

### Cost

The cost of the whole approach — a denormalized composite `_id` string,
plus a redundant `tasklist_key` field — is intentional and documented in
the per-collection sections below.

### `shards` Collection

```
Primary Key: _id = shard_id
Shard Key: { _id }  HASHED
```

Small, fixed-size collection (one row per logical shard). MongoDB-sharded
with hashed `_id` mainly for consistency with the other small collections;
all operations are direct `_id` lookups, which route to a single shard.

### `tasklist` Collection (Merged Tasklist Ownership + Task Queue)

```
Shard Key: { tasklist_key }  HASHED
Indexes:
  { tasklist_key, row_type }                 -- metadata row lookup
  { tasklist_key, row_type, task_id }        -- task range read + delete-less-than

Row types:
  row_type=1: metadata row (_id = "m/" + tasklist_key)
  row_type=2: task row     (_id = "t/" + tasklist_key + "/" + task_id)
```

Metadata (`range_id`, `ack_level`, `owner_member_id`, `owner_address`)
and the task queue rows live in one collection, sharded on
`tasklist_key` (= `namespace + "/" + task_list_name + "/" + partition_id`).
All rows for a single partition share the same `tasklist_key` and land
on the same Atlas shard, enabling single-shard transactions for fenced
writes (the `CreateTasks` operation atomically verifies the partition's
`range_id` on the metadata row before inserting the task rows).

**Why merged**: tasklist ownership uses Cadence-style fencing (monotonic
`range_id` incremented on each `ClaimTasklist`, no lease renewal). The
fenced `CreateTasks` / `UpdateTasklistMetadata` need to atomically
verify `range_id` on the metadata row before writing — same-collection
placement makes this a cheap single-shard transaction instead of a
cross-collection one.

**Why no lease renewal**: tasklist count is unbounded (every unique
`(namespace, task_list_name, partition_id)` triple creates a partition
on demand). Lease-based renewal would require periodic DB writes per
partition regardless of activity. Since the vast majority of dispatches
are sync matched (worker already long-polling), most partitions are
idle most of the time. Cadence-style fencing eliminates all periodic
I/O — the only tasklist DB operations are `ClaimTasklist` (once per
ownership change) and fenced `CreateTasks` (only when sync match
fails). Stale owners are detected on their next fenced write (range_id
mismatch), not by timer.

See [matching-tasklist-redesign.md](matching-tasklist-redesign.md) for
the full ownership contract, partitioning + forwarding model, sticky
external-event delivery, and migration notes.

### `history` Collection

```
Index: { shard_id, run_id, id }  UNIQUE
Shard Key: { shard_id }  HASHED
```

Append-only log. `id` is an auto-incremented integer per run. Same hashed
sharding rationale as `runs` (no write hot spots, equality-on-`shard_id`
queries route to a single shard).

The `payload` binary contains a proto-marshaled `pb.History*Payload`
selected by `payload_type`, with one twist: every `pb.Value` that would
otherwise carry an inline `EncodedObject` is rewritten to a server-internal
`EncodedObjectBlobIdInternalOnly` variant holding just the `blob_id` of
the bytes (which live in the `blobs` collection). The rewrite happens
inside the same engine converters that translate inbound `pb.Value` into
`p.Value{BlobRef}` for the RunRow update — a single in-place mutation
covers both consumers (RunRow + history) so blob bytes are written
exactly once per logical Value. See
[history-store-design.md §6 "Blob extraction"](history-store-design.md)
for the full mechanics, including the OpsService read-side hydration path.

## 7. RunRowUpdate: MongoDB Update Operators

The `buildRunUpdateDoc` function translates a `RunRowUpdate` struct into
MongoDB update operators, using different operators for different field
semantics:

| Field Category | MongoDB Operator | Semantics |
|---|---|---|
| `version` | `$inc: {version: 1}` | Always increment (CAS) |
| Scalar fields (status, counters, timestamps) | `$set` | Overwrite |
| `state_map` entries | `$set` on dot-path `state_map.fieldName` | Upsert individual keys (delta) |
| `step_exe_id_counters` | `$set` on dot-path | Overwrite individual counters |
| `active_step_executions` (non-nil) | `$set` on dot-path | Upsert step |
| `active_step_executions` (nil) | `$unset` on dot-path | Delete step |
| `unconsumed_channel_messages` | `$push` with `$each` | Append to channel array |
| `replace_unconsumed_channels` | `$set` on dot-path | Replace entire channel array (for consumption) |

This allows a single `UpdateOne` call to express complex partial updates
without reading and rewriting the entire document.
