// Dead Letter Queue operations for the dex task_dlq collection.
// Run via: mongosh "${DEX_MONGO_URI}" deploy/gcp-gke/dlq-ops.js
//
// Actions (set DEX_DLQ_ACTION env var):
//   inspect (default) - Summary counts + 10 most recent DLQ entries
//   replay            - Re-enqueue DLQ entries as new RunInitialDispatch tasks
//   purge             - Delete DLQ entries older than N hours
//
// Options:
//   DEX_DLQ_SHARD  - Filter by shard_id (replay only)
//   DEX_DLQ_HOURS  - Age threshold in hours for purge (default: 24)
//   DEX_DLQ_LIMIT  - Max entries to replay per invocation (default: 1000)

// task_dlq and runs both live in the dex_runs database (see v0.js).
db = db.getSiblingDB("dex_runs");

const action = process.env.DEX_DLQ_ACTION || "inspect";

const ROW_TYPE_IMMEDIATE = 2;
const ROW_TYPE_TIMER = 3;

const immediateTaskTypes = {
  0: "RunInitialDispatch",
  1: "RunResumeDispatch",
};

const timerTaskTypes = {
  0: "RunHeartbeat",
  1: "StepWaitForTimer",
};

function taskTypeName(rowType, taskType) {
  if (rowType === ROW_TYPE_TIMER) {
    return timerTaskTypes[taskType] || "TimerUnknown(" + taskType + ")";
  }
  return immediateTaskTypes[taskType] || "Unknown(" + taskType + ")";
}

function queueTypeName(rowType) {
  if (rowType === ROW_TYPE_TIMER) return "timer";
  if (rowType === ROW_TYPE_IMMEDIATE) return "immediate";
  return "unknown(" + rowType + ")";
}

// ============================================================================
// inspect
// ============================================================================

function doInspect() {
  print("=== DLQ Summary ===");

  const total = db.task_dlq.countDocuments();
  print("  Total entries: " + total);

  if (total === 0) {
    print("  (empty)");
    return;
  }

  // Count by error_category
  print("\n--- By Error Category ---");
  const byCat = db.task_dlq.aggregate([
    { $group: { _id: "$error_category", count: { $sum: 1 } } },
    { $sort: { count: -1 } },
  ]).toArray();
  byCat.forEach((doc) => {
    print("  " + (doc._id || "(unknown)") + ": " + doc.count);
  });

  // Count by task_list_name
  print("\n--- By Tasklist ---");
  const byTaskList = db.task_dlq.aggregate([
    { $group: { _id: "$task_list_name", count: { $sum: 1 } } },
    { $sort: { count: -1 } },
    { $limit: 20 },
  ]).toArray();
  byTaskList.forEach((doc) => {
    print("  " + (doc._id || "(unknown)") + ": " + doc.count);
  });

  // Count by member_id (which instance wrote the DLQ entry)
  print("\n--- By Member ---");
  const byMember = db.task_dlq.aggregate([
    { $group: { _id: "$member_id", count: { $sum: 1 } } },
    { $sort: { count: -1 } },
  ]).toArray();
  byMember.forEach((doc) => {
    print("  " + (doc._id || "(unknown)") + ": " + doc.count);
  });

  // 10 most recent entries
  print("\n=== 10 Most Recent DLQ Entries ===");
  const recent = db.task_dlq.find()
    .sort({ dlq_at: -1 })
    .limit(10)
    .toArray();

  recent.forEach((doc) => {
    const tname = taskTypeName(doc.row_type, doc.task_type);
    const qname = queueTypeName(doc.row_type);
    print("\n  task_id=" + doc.task_id
      + "  queue=" + qname
      + "  type=" + tname
      + "  shard=" + doc.shard_id);
    print("    run_id=" + doc.run_id
      + "  ns=" + doc.namespace
      + "  taskList=" + doc.task_list_name);
    print("    error_category=" + doc.error_category);
    print("    error=" + doc.error);
    print("    created_at=" + doc.created_at
      + "  dlq_at=" + doc.dlq_at
      + "  member=" + doc.member_id);
  });
}

// ============================================================================
// replay
// ============================================================================

function doReplay() {
  const shardFilter = process.env.DEX_DLQ_SHARD;
  const limit = parseInt(process.env.DEX_DLQ_LIMIT || "1000", 10);

  const filter = {};
  if (shardFilter !== undefined && shardFilter !== "") {
    filter.shard_id = parseInt(shardFilter, 10);
  }

  const entries = db.task_dlq.find(filter).sort({ dlq_at: 1 }).limit(limit).toArray();

  if (entries.length === 0) {
    print("No DLQ entries to replay" + (shardFilter ? " for shard " + shardFilter : ""));
    return;
  }

  print("Replaying " + entries.length + " DLQ entries...");

  let replayed = 0;
  let failed = 0;

  entries.forEach((doc) => {
    // Create a new RunInitialDispatch immediate task in the runs collection.
    // Uses the original shard_id so the shard owner's batch reader picks it up.
    // sort_key=0 ensures it is read on the next poll (below any valid TaskSeq).
    // The batch reader's lastSeq cursor may have advanced past 0, but after a
    // shard handoff (RangeID bump), the new TaskSeq space starts fresh.
    // For immediate replay without waiting for handoff, we use sort_key from
    // the original task so it lands in the current range.
    const newTaskId = UUID().toString().replace(/^UUID\("(.*)"\)$/, "$1") || doc.task_id + "-replay";
    const taskDoc = {
      shard_id: doc.shard_id,
      row_type: 2,  // RowTypeImmediateTask
      namespace: "",
      sort_key: doc.sort_key,
      id: newTaskId,
      task_type: doc.task_type,
      task_info: {
        run_id: doc.run_id,
        namespace: doc.namespace,
        task_list_name: doc.task_list_name,
      },
      created_at: new Date(),
    };

    try {
      db.runs.insertOne(taskDoc);
      db.task_dlq.deleteOne({ _id: doc._id });
      replayed++;
    } catch (e) {
      print("  FAILED to replay task_id=" + doc.task_id + ": " + e.message);
      failed++;
    }
  });

  print("Replay complete: " + replayed + " replayed, " + failed + " failed");
}

// ============================================================================
// purge
// ============================================================================

function doPurge() {
  const hours = parseInt(process.env.DEX_DLQ_HOURS || "24", 10);
  const cutoff = new Date(Date.now() - hours * 60 * 60 * 1000);

  print("Purging DLQ entries older than " + hours + " hours (before " + cutoff.toISOString() + ")...");

  const result = db.task_dlq.deleteMany({ dlq_at: { $lt: cutoff } });
  print("Purged " + result.deletedCount + " entries");
}

// ============================================================================
// dispatch
// ============================================================================

switch (action) {
  case "inspect":
    doInspect();
    break;
  case "replay":
    doReplay();
    break;
  case "purge":
    doPurge();
    break;
  default:
    print("Unknown action: " + action);
    print("Valid actions: inspect, replay, purge");
    print("Set via: DEX_DLQ_ACTION=<action>");
}
