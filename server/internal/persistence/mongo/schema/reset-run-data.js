// Reset run-level dex data for a fresh benchmark run.
// Run via: mongosh "mongodb+srv://..." reset-run-data.js
//
// This script deletes ALL documents from the run-related collections but
// preserves indexes and sharding configuration. Shard leases (dex_shards)
// are intentionally kept so the next run can claim already-leased shards
// without waiting for lease expiry.
//
// WARNING: This is destructive. All runs, tasks, blobs, tasklist
// metadata + queues, visibility rows, and history events will be
// permanently deleted.

function deleteAll(db, collName) {
  var result = db[collName].deleteMany({});
  print("Deleted " + result.deletedCount + " documents from " + db.getName() + "." + collName);
}

var runsDB = db.getSiblingDB("dex_runs");
deleteAll(runsDB, "runs");
deleteAll(runsDB, "task_dlq");
deleteAll(db.getSiblingDB("dex_blobs"), "blobs");
deleteAll(db.getSiblingDB("dex_tasklists"), "tasklist");
deleteAll(db.getSiblingDB("dex_visibility"), "visibility");
deleteAll(db.getSiblingDB("dex_history"), "history");

print("All dex run data has been reset.");
