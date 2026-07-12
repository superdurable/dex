// Inspect run statuses in the dex database.
// Run via: mongosh "${DEX_MONGO_URI}" deploy/gcp-gke/inspect-runs.js
//
// Output:
//   1. Aggregate count of runs grouped by status.
//   2. The 10 most recent runs that are NOT Running or Completed (i.e. stuck
//      or abnormal runs worth investigating).
//
// NOTE: The "runs" collection stores runs, immediate tasks, and timer tasks
// in the same collection (distinguished by row_type). This script filters on
// row_type=1 (RowTypeRun) to exclude task rows.

// The runs collection lives in the dex_runs database (see v0.js).
db = db.getSiblingDB("dex_runs");

const ROW_TYPE_RUN = 1;

const statusNames = {
  0: "Pending",
  1: "WaitingForWorker",
  2: "Running",
  3: "AllStepsWaitingForConditions",
  4: "Completed",
  5: "Failed",
};

// --- 1. Status summary ---
print("=== Run Status Summary ===");
const counts = db.runs.aggregate([
  { $match: { row_type: ROW_TYPE_RUN } },
  { $group: { _id: "$status", count: { $sum: 1 } } },
  { $sort: { count: -1 } },
]).toArray();

let total = 0;
counts.forEach((doc) => {
  const name = statusNames[doc._id] || "Unknown(" + doc._id + ")";
  print("  " + name + " (" + doc._id + "): " + doc.count);
  total += doc.count;
});
print("  Total: " + total);

// --- 2. Recent non-Running/non-Completed runs ---
print("\n=== 10 Most Recent Non-Running/Non-Completed Runs ===");
const abnormal = db.runs.find(
  { row_type: ROW_TYPE_RUN, status: { $nin: [2, 4] } },
  { _id: 0, id: 1, namespace: 1, status: 1, shard_id: 1, task_list_name: 1, created_at: 1 }
).sort({ created_at: -1 }).limit(10).toArray();

if (abnormal.length === 0) {
  print("  (none)");
} else {
  abnormal.forEach((doc) => {
    const name = statusNames[doc.status] || "Unknown(" + doc.status + ")";
    print("  " + doc.id
      + "  status=" + name + "(" + doc.status + ")"
      + "  ns=" + doc.namespace
      + "  taskList=" + doc.task_list_name
      + "  shard=" + doc.shard_id
      + "  created=" + doc.created_at);
  });
}

// --- 3. Pending tasks per tasklist (Cadence-style task queue) ---
// tasklist_key = "namespace/task_list_name/partition_id" (composite field).
print("\n=== Tasklist Queues (tasklist) by Tasklist ===");
const asyncCounts = db.tasklist.aggregate([
  { $match: { row_type: 2 } }, // task rows only (row_type=1 is metadata)
  { $group: { _id: "$tasklist_key", count: { $sum: 1 } } },
  { $sort: { count: -1 } },
]).toArray();

if (asyncCounts.length === 0) {
  print("  (empty)");
} else {
  let asyncTotal = 0;
  asyncCounts.forEach((doc) => {
    print("  " + doc._id + ": " + doc.count);
    asyncTotal += doc.count;
  });
  print("  Total: " + asyncTotal);
}

// --- 4. Per-run diagnostics for the abnormal runs found above ---
const immediateTaskTypes = { 0: "RunInitialDispatch", 1: "RunResumeDispatch" };
const timerTaskTypes = { 0: "RunHeartbeat", 1: "StepWaitForTimer" };
const ROW_TYPE_IMMEDIATE = 2;
const ROW_TYPE_TIMER = 3;

if (abnormal.length > 0) {
  print("\n=== Per-Run Diagnostics ===");
  abnormal.forEach((doc) => {
    const rid = doc.id;
    const sname = statusNames[doc.status] || "Unknown(" + doc.status + ")";
    print("\n--- " + rid + "  status=" + sname + "  taskList=" + doc.task_list_name + "  shard=" + doc.shard_id + " ---");

    // Check tasklist queue for any pending dispatch tasks for this run.
    const asyncRec = db.tasklist.findOne({ row_type: 2, run_id: rid });
    if (asyncRec) {
      print("  [async_match] FOUND  task_id=" + asyncRec.task_id
        + "  group_key=" + asyncRec.group_key
        + "  shard=" + asyncRec.shard_id + "  created=" + asyncRec.created_at);
    } else {
      print("  [async_match] NOT FOUND");
    }

    // Check immediate tasks for this run
    const immTasks = db.runs.find(
      { row_type: ROW_TYPE_IMMEDIATE, shard_id: doc.shard_id, "task_info.run_id": rid }
    ).toArray();
    if (immTasks.length > 0) {
      immTasks.forEach((t) => {
        const tname = immediateTaskTypes[t.task_type] || "Unknown(" + t.task_type + ")";
        print("  [immediate_task] id=" + t.id + "  type=" + tname + "(" + t.task_type + ")"
          + "  sort_key=" + t.sort_key + "  created=" + t.created_at);
      });
    } else {
      print("  [immediate_task] NONE");
    }

    // Check timer tasks for this run
    const timerTasks = db.runs.find(
      { row_type: ROW_TYPE_TIMER, shard_id: doc.shard_id, "task_info.run_id": rid }
    ).toArray();
    if (timerTasks.length > 0) {
      timerTasks.forEach((t) => {
        const tname = timerTaskTypes[t.task_type] || "Unknown(" + t.task_type + ")";
        print("  [timer_task] id=" + t.id + "  type=" + tname + "(" + t.task_type + ")"
          + "  sort_key=" + t.sort_key + "  fire_at=" + new Date(t.sort_key) + "  created=" + t.created_at);
      });
    } else {
      print("  [timer_task] NONE");
    }
  });
}
