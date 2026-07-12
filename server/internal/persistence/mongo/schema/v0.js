// Schema version 0 for MongoDB (sharded cluster).
// Idempotent: safe to run multiple times.
//
// Usage:
//   mongosh "${URI}" v0.js
//
// Each logical store lives in its OWN database so that production deployments
// can host them on independent Atlas clusters (configure per-store URI via
// DEXPersistenceConfig.Mongo.<store>). On a single cluster, this script
// just creates 5 sibling databases under the same connection.
//
// Databases:
//   dex_shards     — shard ownership / lease metadata
//   dex_runs       — run state + immediate/timer/ops_fifo task outbox + DLQ
//   dex_blobs      — large value storage (cheaper-tier-friendly; isolated from runs)
//   dex_tasklists  — tasklist metadata + task dispatch queue (Cadence-style)
//   dex_visibility — list view of runs (sharded by namespace)
//   dex_history    — append-only event log per run (sharded by run_id)
//
// All sharded collections use hashed sharding, so MongoDB pre-allocates
// chunks across all shards on shardCollection — no manual pre-split or
// moveChunk is required.

function tryRun(desc, fn) {
  try { fn(); print("OK: " + desc); }
  catch (e) { print("SKIP: " + desc + " (" + e.message + ")"); }
}

// =============================================================================
// dex_shards: shards collection
// (logical shard ownership, NOT MongoDB shards)
//
// Rows are created on first ClaimShard (upsert). NumberOfShards is a config
// value; the shard manager iterates 0..N-1 and claims each.
// =============================================================================

db = db.getSiblingDB("dex_shards");
tryRun("enableSharding dex_shards", function () { sh.enableSharding("dex_shards"); });

// _id = shard_id (int). Hashed sharding on _id distributes lease documents
// across Atlas shards. The collection is small (128 docs) so this is mainly
// for consistency with other collections.
tryRun("hashed index shards", function () { db.shards.createIndex({ _id: "hashed" }); });
tryRun("shardCollection dex_shards.shards", function () { sh.shardCollection("dex_shards.shards", { _id: "hashed" }); });

// =============================================================================
// dex_runs: runs + task_dlq
// =============================================================================
//
// `runs` holds run_row + immediate_task_row + timer_task_row +
// ops_fifo_task_row, distinguished by row_type.
//
// Sharding strategy: hashed on shard_id.
//
// Why hashed instead of range:
//   - Pre-distributes chunks across all Atlas shards from day one (no need
//     for pre-split + manual moveChunk / waiting for the balancer).
//   - Avoids monotonic write hot spots on shard_id ranges.
//   - Equality lookups on shard_id still route to a single Atlas shard, so
//     same-shard_id transactions inside this collection (e.g., StartRun:
//     run_row + immediate_task_row + ops_fifo_task_row in the same `runs`
//     collection) remain single-shard transactions.
//
// Trade-off vs range sharding:
//   - Cross-collection (and now cross-database) co-location is no longer
//     guaranteed: runs[i] and blobs[i] may land on different Atlas shards
//     (or different Atlas clusters entirely) because each collection
//     hashes shard_id independently. Any logic that needs an atomic
//     transaction across these collections for the same shard_id must be
//     rethought. (dex already avoids cross-collection transactions
//     between runs ↔ blobs by writing blobs first, then run rows that
//     reference them via blob_id — see RunStore.CreateRunWithTasks.)
//   - Range scans over shard_id (e.g., shard_id BETWEEN a AND b) cannot be
//     precisely routed and become scatter/gather. Current access patterns
//     are all (shard_id, ...) equality + range on sort_key, so this is OK.
//
// task_dlq is co-located with runs (DLQ rows reference task rows in the
// same collection by shard_id + task id), so it lives in this database.
// =============================================================================

db = db.getSiblingDB("dex_runs");
tryRun("enableSharding dex_runs", function () { sh.enableSharding("dex_runs"); });

// Single unified compound index serves as PK, immediate task polling, timer
// task polling, AND ops_fifo task FIFO polling:
//   { shard_id, row_type, namespace, sort_key, id }
//
// sort_key meaning by row_type:
//   row_type=1 (run_row):            sort_key = 0,                                    id = run_id
//   row_type=2 (immediate_task_row): sort_key = TaskSeq (RangeID<<32 | LocalSeq),     id = task_uuid (namespace="")
//   row_type=3 (timer_task_row):     sort_key = fire_at_unix_ms,                      id = task_uuid (namespace="")
//   row_type=4 (ops_fifo_task_row):  sort_key = TaskSeq (per-shard OpsFIFO counter),  id = task_uuid (namespace="")
//
// Access patterns:
//   PK lookup:          exact match on all 5 fields
//   Timer polling:      prefix (shard_id, 3, "") + range on sort_key <= now
//   Immediate polling:  prefix (shard_id, 2, "") + range on sort_key > committed_seq
//   OpsFIFO polling:    prefix (shard_id, 4, "") + range on sort_key > committed_seq
//   Range delete:       prefix (shard_id, row_type, "") + sort_key <= watermark
//
// shard_id is the prefix of this unique index, which satisfies MongoDB's
// requirement that the shard key be a prefix of any unique index (this is
// what allows the hashed shard key on shard_id below to coexist with this
// unique compound index).
db.runs.createIndex(
  { shard_id: 1, row_type: 1, namespace: 1, sort_key: 1, id: 1 },
  { unique: true, name: "pk_idx" }
);

// Hashed index on shard_id is required before shardCollection if the
// collection already has data. For empty collections sh.shardCollection
// creates it automatically, but the explicit createIndex is idempotent.
tryRun("hashed index dex_runs.runs", function () { db.runs.createIndex({ shard_id: "hashed" }); });
tryRun("shardCollection dex_runs.runs", function () { sh.shardCollection("dex_runs.runs", { shard_id: "hashed" }); });

// task_dlq collection (dead letter queue for failed tasks)
//
// Stores tasks that exhausted retries with full diagnostic context.
// Operators can inspect, replay, or purge entries via deploy/gcp-gke/dlq-ops.js.
// Hashed on shard_id (consistent with runs collection).
//
// Unique on (shard_id, task_id) — dedups the rare lease-handoff race
// where the SAME task gets DLQ'd twice (owner A writes DLQ → loses lease
// before deleting the task from `runs` → owner B replays it → it fails
// → second WriteDLQ). DLQStore.WriteDLQ silently swallows duplicate-key
// errors so the second attempt is a no-op. shard_id is the prefix, which
// satisfies MongoDB's "shard key must be a prefix of any unique index"
// requirement.
db.task_dlq.createIndex(
  { shard_id: 1, task_id: 1 },
  { unique: true, name: "pk_idx" }
);

// Polling index for inspect / purge by time. Non-unique.
db.task_dlq.createIndex(
  { shard_id: 1, dlq_at: 1 },
  { name: "dlq_poll_idx" }
);
tryRun("hashed index dex_runs.task_dlq", function () { db.task_dlq.createIndex({ shard_id: "hashed" }); });
tryRun("shardCollection dex_runs.task_dlq", function () { sh.shardCollection("dex_runs.task_dlq", { shard_id: "hashed" }); });

// =============================================================================
// dex_blobs: blobs collection
// =============================================================================
//
// Stores large/non-primitive values (strings, complex types) referenced
// by Value.blob_ref. id is allocated by the engine when it converts a
// pb.Value EncodedObject to a persistence.Value blob_ref before writing
// the run row.
//
// Hosted in its own database so operators can put blob payloads on a
// dedicated, possibly cheaper-tier cluster (e.g. larger Atlas shards
// with cold-storage tiering) independently of the run state.
//
// Hashed on shard_id (see dex_runs comment for rationale and
// trade-offs). Note that even within the same MongoDB cluster, the blobs
// collection's hash chunk for a given shard_id will not necessarily
// land on the same Atlas shard as the runs collection's chunk for that
// same shard_id — the engine never relies on cross-collection chunk
// co-location.
// =============================================================================

db = db.getSiblingDB("dex_blobs");
tryRun("enableSharding dex_blobs", function () { sh.enableSharding("dex_blobs"); });

db.blobs.createIndex(
  { shard_id: 1, namespace: 1, run_id: 1, id: 1 },
  { unique: true, name: "pk_idx" }
);
tryRun("hashed index dex_blobs.blobs", function () { db.blobs.createIndex({ shard_id: "hashed" }); });
tryRun("shardCollection dex_blobs.blobs", function () { sh.shardCollection("dex_blobs.blobs", { shard_id: "hashed" }); });

// =============================================================================
// dex_tasklists: tasklist (merged metadata + task rows, Cadence-style)
//
// row_type=1: metadata row (one per tasklist partition, _id = "m/" + tasklist_key)
//   tasklist_key, namespace, task_list_name, partition_id, range_id (int32),
//   ack_level (int64), owner_member_id, owner_address, claimed_at
//
// row_type=2: task row (many per partition, _id = "t/" + tasklist_key + "/" + task_id)
//   tasklist_key, task_id (int64), run_id, shard_id, created_at
//
// All rows for one tasklist partition share the same tasklist_key value.
// Shard key: {tasklist_key: "hashed"} — all rows for a tasklist partition
// land on one Atlas shard, enabling single-shard transactions for the
// fenced CreateTasks operation (UpdateOne metadata fence + InsertMany tasks).
//
// Deterministic _id construction:
//   - "m/<tasklist_key>" for metadata, "t/<tasklist_key>/<task_id>" for tasks
//   - Hashed sharding on tasklist_key routes all docs sharing a tasklist_key
//     to the SAME chunk. MongoDB's per-chunk _id uniqueness then covers the
//     global case — no separate compound unique index needed.
// =============================================================================

db = db.getSiblingDB("dex_tasklists");
tryRun("enableSharding dex_tasklists", function () { sh.enableSharding("dex_tasklists"); });

// Compound index serves the GetTasks range read pattern:
//   filter (tasklist_key, row_type=task) + range (task_id) + sort task_id ASC
// Metadata reads/writes (ClaimTasklist, UpdateTasklistMetadata, the
// conditional update inside CreateTasks) all key by _id and use the
// implicit _id index, so no dedicated index is needed.
db.tasklist.createIndex({ tasklist_key: 1, row_type: 1, task_id: 1 }, { name: "tasklist_task_scan_idx" });

tryRun("hashed index dex_tasklists.tasklist", function () { db.tasklist.createIndex({ tasklist_key: "hashed" }); });
tryRun("shardCollection dex_tasklists.tasklist", function () { sh.shardCollection("dex_tasklists.tasklist", { tasklist_key: "hashed" }); });

// =============================================================================
// dex_visibility: visibility (list view of runs)
//
// Backs OpsService.ListRuns. One row per (namespace, run_id), upserted
// by the OpsFIFO batch executor as the run progresses.
//
// Sharded by namespace so ListRuns(namespace=...) routes to a single
// Atlas shard. PK is (namespace, run_id); two compound indexes serve the
// list-by-start-time and list-by-updated-at queries.
//
// Why updated_at instead of a separate end_time column:
//   - For terminal statuses, updated_at IS the end time (the row stops
//     mutating after the run reaches a terminal state).
//   - For active statuses, updated_at doubles as a "recently active" cursor.
//   - One column, two query patterns, no write-time branch on terminal/non-terminal.
// =============================================================================

db = db.getSiblingDB("dex_visibility");
tryRun("enableSharding dex_visibility", function () { sh.enableSharding("dex_visibility"); });

db.visibility.createIndex(
  { namespace: 1, run_id: 1 },
  { unique: true, name: "pk_idx" }
);

// List by creation time (newest first within (namespace, flow_type, status)).
db.visibility.createIndex(
  { namespace: 1, flow_type: 1, status: 1, start_time: -1, run_id: 1 },
  { name: "list_by_start_time_idx" }
);

// List by last-activity time. For terminal statuses this doubles as the
// "list by end time" index because updated_at moves forward only on real
// state changes; for active statuses it serves "recently active runs".
db.visibility.createIndex(
  { namespace: 1, flow_type: 1, status: 1, updated_at: -1, run_id: 1 },
  { name: "list_by_updated_at_idx" }
);

tryRun("hashed index dex_visibility.visibility", function () { db.visibility.createIndex({ namespace: "hashed" }); });
tryRun("shardCollection dex_visibility.visibility", function () { sh.shardCollection("dex_visibility.visibility", { namespace: "hashed" }); });

// =============================================================================
// dex_history: history (append-only event log per run)
//
// Append-only log of execution events for UI display and time-travel
// debugging. event_id is monotonically allocated per run on
// RunRow.LastHistoryEventID under CAS, so events are gap-free for any
// successfully committed run state.
//
// Sharded by run_id so GetHistoryEvents(run_id=...) routes to a single shard
// and per-run insert ordering is preserved on a single shard. PK is
// (run_id, event_id) for ordered reads and dedup-on-replay.
//
// Note: this is the FIRST collection in the schema sharded by run_id rather
// than shard_id — history reads don't know shard_id (the engine state lives
// in dex_runs, not dex_history), and per-run grouping is the
// natural access pattern.
// =============================================================================

db = db.getSiblingDB("dex_history");
tryRun("enableSharding dex_history", function () { sh.enableSharding("dex_history"); });

db.history.createIndex(
  { run_id: 1, event_id: 1 },
  { unique: true, name: "pk_idx" }
);

tryRun("hashed index dex_history.history", function () { db.history.createIndex({ run_id: "hashed" }); });
tryRun("shardCollection dex_history.history", function () { sh.shardCollection("dex_history.history", { run_id: "hashed" }); });

print("Schema v0 initialized successfully.");
