package mongo

import (
	"context"
	"math/rand"
	"os"
	"testing"
	"time"

	"github.com/superdurable/dex/server/common/utils/ids"
	p "github.com/superdurable/dex/server/internal/persistence"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// Per-store databases initialized by v0.js with hashed shard keys. The test
// is meaningful only when these DBs are sharded across multiple physical
// MongoDB shards (e.g. dependency-docker-mongo-2shards.yaml). On a single replica
// set the test still passes but reports a single shard.
const (
	shardDistRunsDB       = defaultRunsDatabase       // runs + task_dlq live here
	shardDistBlobsDB      = defaultBlobsDatabase      // blobs has its own database (own potential cluster)
	shardDistShardsDB     = defaultShardsDatabase     // shards collection
	shardDistTasklistsDB  = defaultTasklistsDatabase  // tasklist collection
	shardDistVisibilityDB = defaultVisibilityDatabase // visibility collection (unused by this test, listed for distribution view)
	shardDistHistoryDB    = defaultHistoryDatabase    // history collection (unused by this test, listed for distribution view)
	shardDistTestNS       = "shard_dist_test"
)

// TestShardDistribution_RandomShardIDs writes data through the actual store
// APIs (RunStore, BlobStore, TasklistStore, ShardStore) using uniformly
// random shard_id values in [0, NumShards), then queries
// $shardedDataDistribution to report how documents landed across the
// physical MongoDB shards.
//
// Run with:
//
//	cd server
//	docker compose -f dependency-docker-mongo-2shards.yaml up -d
//	# wait for mongo-init to finish (it runs v0.js)
//	DEX_TEST_MONGO_URI=mongodb://localhost:27018 \
//	  go test -count=1 -run TestShardDistribution_RandomShardIDs -v \
//	  ./server/internal/persistence/mongo
func TestShardDistribution_RandomShardIDs(t *testing.T) {
	uri := os.Getenv("DEX_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("DEX_TEST_MONGO_URI not set; skipping integration test")
	}

	const (
		numShards            = 128
		numRuns              = 256
		numShardClaimers     = 128
		numTasklistClaims    = 128
		numTasklistTaskBatch = 256
	)

	ctx := context.Background()

	runStore, err := NewRunStoreWithDatabase(ctx, uri, shardDistRunsDB, DefaultOperationTimeouts())
	require.Nil(t, err)
	defer runStore.Close()

	blobStore, err := NewBlobStoreWithDatabase(ctx, uri, shardDistBlobsDB, DefaultOperationTimeouts())
	require.Nil(t, err)
	defer blobStore.Close()

	tasklistStore, err := NewTasklistStoreWithDatabase(ctx, uri, shardDistTasklistsDB, DefaultOperationTimeouts())
	require.Nil(t, err)
	defer tasklistStore.Close()

	shardStore, err := NewShardStoreWithDatabase(ctx, uri, shardDistShardsDB, DefaultOperationTimeouts())
	require.Nil(t, err)
	defer shardStore.Close()

	// Use a separate raw client for cleanup + the $shardedDataDistribution
	// admin aggregation. The dex_* DBs on the local 2-shard cluster
	// are dedicated to this kind of test, so we wipe these collections
	// fully before/after — namespace-scoped cleanup isn't enough because:
	//   - `runs` immediate-task rows are stored with namespace="" (see
	//     immediateTaskRowToDoc), so a {namespace: ...} filter misses them.
	//   - `tasklist` documents use tasklist_key (not namespace-scoped),
	//     so a namespace-only filter would miss them too.
	rawClient, rawErr := connectMongo(ctx, uri)
	require.NoError(t, rawErr)
	defer rawClient.Disconnect(ctx)

	cleanupAll := func() {
		_, _ = rawClient.Database(shardDistRunsDB).Collection(collRuns).DeleteMany(ctx, bson.M{})
		_, _ = rawClient.Database(shardDistBlobsDB).Collection(collBlobs).DeleteMany(ctx, bson.M{})
		_, _ = rawClient.Database(shardDistHistoryDB).Collection(collHistory).DeleteMany(ctx, bson.M{})
		_, _ = rawClient.Database(shardDistTasklistsDB).Collection(collTasklist).DeleteMany(ctx, bson.M{})
		_, _ = rawClient.Database(shardDistShardsDB).Collection(collShards).DeleteMany(ctx, bson.M{})
	}
	cleanupAll()
	defer cleanupAll()

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	// 1+2. RunStore.CreateRunWithTasks → writes to `runs`
	//      BlobStore.BatchInsertBlobs   → writes to `blobs`
	for i := 0; i < numRuns; i++ {
		sid := int32(rng.Intn(numShards))
		runID := uuid.NewString()
		run := &p.RunRow{
			ShardID:                   sid,
			Namespace:                 shardDistTestNS,
			ID:                        runID,
			FlowType:                  "test",
			TaskListName:              "tl-default",
			Status:                    p.RunStatusPending,
			StateMap:                  map[string]p.Value{},
			UnconsumedChannelMessages: map[string][]p.ChannelMessage{},
			StepExeIDCounters:         map[string]int32{},
			ActiveStepExecutions:      map[string]p.ActiveStepExecution{},
		}
		tasks := []p.TaskRow{{Immediate: &p.ImmediateTaskRow{
			ShardID:  sid,
			ID:       ids.NewTaskID(),
			TaskType: p.ImmediateTaskRunInitialDispatch,
			TaskInfo: p.ImmediateTaskInfo{RunID: runID, Namespace: shardDistTestNS, TaskListName: "tl-default"},
		}}}
		require.Nil(t, runStore.CreateRunWithTasks(ctx, run, tasks))

		require.Nil(t, blobStore.BatchInsertBlobs(ctx, sid, shardDistTestNS, runID,
			[]p.BlobEntry{{BlobID: ids.NewBlobID(), Encoding: "raw", Payload: []byte("payload")}}))
	}

	// 3. ShardStore.ClaimShard → writes to `shards` (hashed on _id = shard_id).
	//    Lease conflicts when the same sid is claimed twice are expected and
	//    fine; the doc still exists either way.
	for i := 0; i < numShardClaimers; i++ {
		sid := int32(rng.Intn(numShards))
		_, _ = shardStore.ClaimShard(ctx, sid, "member-"+uuid.NewString(), 30*time.Second)
	}

	// 4. TasklistStore.ClaimTasklist → writes metadata row to `tasklist` (hashed on tasklist_key).
	for i := 0; i < numTasklistClaims; i++ {
		tlName := "tl-" + uuid.NewString()
		md, _ := tasklistStore.ClaimTasklist(ctx, shardDistTestNS, tlName, 0, "member-x", "127.0.0.1:7234")
		if md != nil {
			// 5. TasklistStore.CreateTasks → writes task rows to the same collection.
			now := time.Now()
			_ = tasklistStore.CreateTasks(ctx, shardDistTestNS, tlName, 0, md.RangeID, []*p.TasklistTaskRow{
				{Namespace: shardDistTestNS, TasklistName: tlName, PartitionID: 0, TaskID: int64(i*100 + 1), RunID: uuid.NewString(), ShardID: 0, CreatedAt: now},
			})
		}
	}

	for i := 0; i < numTasklistTaskBatch; i++ {
		tlName := "tl-batch-" + uuid.NewString()
		md, _ := tasklistStore.ClaimTasklist(ctx, shardDistTestNS, tlName, 0, "member-x", "127.0.0.1:7234")
		if md != nil {
			now := time.Now()
			_ = tasklistStore.CreateTasks(ctx, shardDistTestNS, tlName, 0, md.RangeID, []*p.TasklistTaskRow{
				{Namespace: shardDistTestNS, TasklistName: tlName, PartitionID: 0, TaskID: int64(i*100 + 1), RunID: uuid.NewString(), ShardID: 0, CreatedAt: now},
			})
		}
	}

	// ============================================================
	// Inspect distribution via $shardedDataDistribution
	// ============================================================
	type shardEntry struct {
		ShardName      string `bson:"shardName"`
		OwnedDocuments int64  `bson:"numOwnedDocuments"`
		OwnedSizeBytes int64  `bson:"ownedSizeBytes"`
		OrphanedDocs   int64  `bson:"numOrphanedDocs"`
	}
	type distEntry struct {
		Ns     string       `bson:"ns"`
		Shards []shardEntry `bson:"shards"`
	}

	adminDB := rawClient.Database("admin")
	cursor, aggErr := adminDB.Aggregate(ctx, mongo.Pipeline{
		bson.D{{Key: "$shardedDataDistribution", Value: bson.D{}}},
	})
	if aggErr != nil {
		t.Logf("Cluster does not support $shardedDataDistribution "+
			"(likely a single replica set, not a sharded cluster): %v", aggErr)
		return
	}
	defer cursor.Close(ctx)

	interesting := map[string]struct{}{
		shardDistRunsDB + "." + collRuns:          {},
		shardDistBlobsDB + "." + collBlobs:        {},
		shardDistHistoryDB + "." + collHistory:    {},
		shardDistShardsDB + "." + collShards:      {},
		shardDistTasklistsDB + "." + collTasklist: {},
	}

	var rows []distEntry
	require.NoError(t, cursor.All(ctx, &rows))

	t.Logf("\n=== Per-shard distribution (per-store DBs) ===")
	sawDist := false
	for _, e := range rows {
		if _, ok := interesting[e.Ns]; !ok {
			continue
		}
		sawDist = true
		var total int64
		for _, s := range e.Shards {
			total += s.OwnedDocuments
		}
		t.Logf("\nns=%s (total docs=%d)", e.Ns, total)
		for _, s := range e.Shards {
			pct := 0.0
			if total > 0 {
				pct = 100.0 * float64(s.OwnedDocuments) / float64(total)
			}
			t.Logf("  %-10s docs=%-6d (%5.1f%%) bytes=%-8d orphans=%d",
				s.ShardName, s.OwnedDocuments, pct, s.OwnedSizeBytes, s.OrphanedDocs)
		}
	}
	require.True(t, sawDist,
		"expected at least one dex_*.* collection in $shardedDataDistribution output")
}

// TestUniqueConstraintWithHashedSharding verifies that the compound unique
// index on { shard_id, row_type, namespace, sort_key, id } still rejects
// duplicate inserts after we switched the runs collection to hashed
// sharding on shard_id (see v0.js).
//
// MongoDB allows hashed shard keys to coexist with a compound unique index
// only when the shard key field is a prefix of that index — which is the
// case here. This test makes sure the constraint actually fires end-to-end
// through the RunStore API and is surfaced as a CategorizedError with
// category "conflict" (not bypassed or remapped to a generic internal error).
func TestUniqueConstraintWithHashedSharding(t *testing.T) {
	uri := os.Getenv("DEX_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("DEX_TEST_MONGO_URI not set; skipping integration test")
	}

	ctx := context.Background()

	runStore, err := NewRunStoreWithDatabase(ctx, uri, shardDistRunsDB, DefaultOperationTimeouts())
	require.Nil(t, err)
	defer runStore.Close()

	// Wipe runs so a previous test invocation doesn't pollute the assertions.
	rawClient, rawErr := connectMongo(ctx, uri)
	require.NoError(t, rawErr)
	defer rawClient.Disconnect(ctx)
	_, _ = rawClient.Database(shardDistRunsDB).Collection(collRuns).DeleteMany(ctx, bson.M{})

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	makeRun := func(sid int32, runID string) (*p.RunRow, []p.TaskRow) {
		run := &p.RunRow{
			ShardID:                   sid,
			Namespace:                 shardDistTestNS,
			ID:                        runID,
			FlowType:                  "test",
			TaskListName:              "tl-default",
			Status:                    p.RunStatusPending,
			StateMap:                  map[string]p.Value{},
			UnconsumedChannelMessages: map[string][]p.ChannelMessage{},
			StepExeIDCounters:         map[string]int32{},
			ActiveStepExecutions:      map[string]p.ActiveStepExecution{},
		}
		tasks := []p.TaskRow{{Immediate: &p.ImmediateTaskRow{
			ShardID:  sid,
			ID:       ids.NewTaskID(),
			TaskType: p.ImmediateTaskRunInitialDispatch,
			TaskInfo: p.ImmediateTaskInfo{RunID: runID, Namespace: shardDistTestNS, TaskListName: "tl-default"},
		}}}
		return run, tasks
	}

	// Sanity sweep: try a handful of random shard_ids so we exercise both
	// physical shards (the hashed key sends each sid to a different chunk).
	for i := 0; i < 8; i++ {
		sid := int32(rng.Intn(128))
		runID := uuid.NewString()

		// First insert succeeds.
		run, tasks := makeRun(sid, runID)
		require.Nil(t, runStore.CreateRunWithTasks(ctx, run, tasks),
			"first insert should succeed for shard_id=%d", sid)

		// Re-insert the same (shard_id, namespace, runID): the unique
		// compound index must reject this with a duplicate-key error,
		// which run_store.go translates into a categorized "conflict" error.
		run2, tasks2 := makeRun(sid, runID)
		dupErr := runStore.CreateRunWithTasks(ctx, run2, tasks2)
		require.NotNil(t, dupErr,
			"duplicate insert should fail for shard_id=%d runID=%s", sid, runID)
		assert.Equal(t, "conflict", string(dupErr.GetCategory()),
			"duplicate insert should be a conflict error, got: %v", dupErr)
	}

	// Cross-check: the underlying driver still classifies the error as
	// IsDuplicateKeyError so that run_store.go's branch (`if mongo.
	// IsDuplicateKeyError(err) { ... NewConflictError(...) }`) keeps firing.
	t.Run("DriverLevelDuplicateKey", func(t *testing.T) {
		coll := rawClient.Database(shardDistRunsDB).Collection(collRuns)
		// Build a minimal run_row doc directly so we can observe the raw
		// driver error (CreateRunWithTasks already swallows it into a
		// CategorizedError).
		sid := int32(rng.Intn(128))
		doc := bson.M{
			"shard_id": sid, "row_type": int32(p.RowTypeRun),
			"namespace": shardDistTestNS, "sort_key": int64(0), "id": uuid.NewString(),
			"version": int64(1), "status": int32(p.RunStatusPending),
		}
		_, err := coll.InsertOne(ctx, doc)
		require.NoError(t, err)
		_, err = coll.InsertOne(ctx, doc)
		require.Error(t, err)
		assert.True(t, mongo.IsDuplicateKeyError(err),
			"expected mongo.IsDuplicateKeyError to be true; got %T: %v", err, err)
	})
}
