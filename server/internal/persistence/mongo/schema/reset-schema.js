// Drop all dex collections so v0.js can recreate them from scratch.
// Run via: mongosh "mongodb+srv://..." reset-schema.js
//
// This removes all data, indexes, and sharding configuration for every
// dex collection across every per-store database. You MUST re-run
// v0.js after this script.
//
// WARNING: This is destructive and irreversible.

function dropIfPresent(db, collName) {
  if (db.getCollectionNames().indexOf(collName) >= 0) {
    db[collName].drop();
    print("Dropped: " + db.getName() + "." + collName);
  } else {
    print("SKIP (not found): " + db.getName() + "." + collName);
  }
}

var byDB = {
  dex_shards:     ["shards"],
  dex_runs:       ["runs", "task_dlq"],
  dex_blobs:      ["blobs"],
  dex_tasklists:  ["tasklist"],
  dex_visibility: ["visibility"],
  dex_history:    ["history"],
};

for (var dbName in byDB) {
  var d = db.getSiblingDB(dbName);
  var colls = byDB[dbName];
  for (var i = 0; i < colls.length; i++) {
    dropIfPresent(d, colls[i]);
  }
}

print("Schema reset complete. Re-run v0.js to recreate.");
