// Reset all dex data for a fresh benchmark run.
// Run via: mongosh "mongodb+srv://..." reset-data.js
//
// This script deletes ALL documents from every dex collection but
// preserves indexes and sharding configuration. After running this you do
// NOT need to re-run v0.js.
//
// WARNING: This is destructive. All runs, tasks, blobs, shard leases,
// tasklist metadata + queues, visibility rows, and history events
// will be permanently deleted.

function deleteAll(db, collName) {
  var result = db[collName].deleteMany({});
  print("Deleted " + result.deletedCount + " documents from " + db.getName() + "." + collName);
}

// dex_runs: run state + immediate/timer/ops task outbox + DLQ.
var runsDB = db.getSiblingDB("dex_runs");
deleteAll(runsDB, "runs");
deleteAll(runsDB, "task_dlq");

// dex_blobs: large value storage (own database for storage tiering).
deleteAll(db.getSiblingDB("dex_blobs"), "blobs");

// dex_shards: shard leases.
deleteAll(db.getSiblingDB("dex_shards"), "shards");

// dex_tasklists: tasklist ownership metadata + Cadence-style task queue.
deleteAll(db.getSiblingDB("dex_tasklists"), "tasklist");

// dex_visibility: list view of runs.
deleteAll(db.getSiblingDB("dex_visibility"), "visibility");

// dex_history: append-only event log per run.
deleteAll(db.getSiblingDB("dex_history"), "history");

print("All dex data has been reset.");
