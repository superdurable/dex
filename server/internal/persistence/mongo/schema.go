package mongo

import (
	"context"
	"fmt"

	"github.com/superdurable/dex/server/config"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Per-store schema helpers. Each helper creates indexes for the collections
// owned by one logical store (e.g. EnsureSchemaRuns covers `runs` + the
// co-located `task_dlq`; blobs have their own helper since they may live
// on a dedicated cluster). All helpers are idempotent — MongoDB's
// CreateOne is a no-op when an index with the same spec already exists,
// so calling them repeatedly is safe.
//
// None of these helpers attempt sharding commands (sh.shardCollection),
// since those require a mongos router and are not available in
// standalone/replica-set mode. Production sharding is configured at deploy
// time via v0.js (which runs through mongosh against a sharded cluster);
// these Go helpers only create indexes.
//
// EnsureSchemaForDatabase creates EVERY collection's indexes in a single
// database — used by per-package test mains that route every store to one
// throw-away database for test isolation.
//
// EnsureSchemaForConfig is the per-store variant: it iterates
// AllStoreNames, resolves each store's URI / database via cfg.For(store),
// and only creates the indexes owned by that store. This is the path used
// by integration tests that exercise the per-store database layout (and
// mirrors what v0.js does in production for the same five databases).

// EnsureSchema creates EVERY collection's indexes in a single database.
// Used by tests that route every store to the same database via testDBName.
// New code should prefer the per-store helpers below or
// EnsureSchemaForConfig.
func EnsureSchema(ctx context.Context, db *mongo.Database) error {
	for _, fn := range []func(context.Context, *mongo.Database) error{
		EnsureSchemaShards,
		EnsureSchemaRuns,
		EnsureSchemaBlobs,
		EnsureSchemaTasklists,
		EnsureSchemaVisibility,
		EnsureSchemaHistory,
	} {
		if err := fn(ctx, db); err != nil {
			return err
		}
	}
	return nil
}

// EnsureSchemaShards creates the shards collection's indexes. The shards
// collection has no compound index today; it is keyed by _id = shard_id.
// This helper is a no-op for non-sharded standalone deployments where _id
// uniqueness is implicit.
func EnsureSchemaShards(ctx context.Context, db *mongo.Database) error {
	// No compound indexes required: ClaimShard / RenewShardLease all key by
	// _id = shard_id which already has the implicit unique index.
	return nil
}

// EnsureSchemaRuns creates indexes for the run/task outbox collections that
// live in the runs cluster: runs (run_row + immediate_task + timer_task +
// ops_fifo_task) and the co-located task_dlq. Blobs are NOT covered here —
// see EnsureSchemaBlobs.
func EnsureSchemaRuns(ctx context.Context, db *mongo.Database) error {
	specs := []mongoIndexSpec{
		{
			collection: collRuns,
			model: mongo.IndexModel{
				Keys: bson.D{
					{Key: "shard_id", Value: 1},
					{Key: "row_type", Value: 1},
					{Key: "namespace", Value: 1},
					{Key: "sort_key", Value: 1},
					{Key: "id", Value: 1},
				},
				Options: options.Index().SetUnique(true).SetName("pk_idx"),
			},
		},
		{
			// Unique on (shard_id, task_id) — dedups the lease-handoff
			// race where the same task gets DLQ'd by two owners. DLQStore
			// .WriteDLQ silently swallows the resulting duplicate-key on
			// the second write. shard_id satisfies MongoDB's shard-key-
			// prefix requirement for sharded unique indexes.
			collection: collTaskDLQ,
			model: mongo.IndexModel{
				Keys:    bson.D{{Key: fieldShardID, Value: 1}, {Key: "task_id", Value: 1}},
				Options: options.Index().SetUnique(true).SetName("pk_idx"),
			},
		},
		{
			// Polling index for inspect / purge by time. Non-unique.
			collection: collTaskDLQ,
			model: mongo.IndexModel{
				Keys:    bson.D{{Key: fieldShardID, Value: 1}, {Key: "dlq_at", Value: 1}},
				Options: options.Index().SetName("dlq_poll_idx"),
			},
		},
	}
	return ensureIndexes(ctx, db, specs)
}

// EnsureSchemaBlobs creates the blobs collection's indexes. Lives in its
// own logical cluster (see config.MongoPersistenceConfig.Blobs) so blob
// payloads can be hosted on dedicated storage independently of the run
// state.
func EnsureSchemaBlobs(ctx context.Context, db *mongo.Database) error {
	specs := []mongoIndexSpec{
		{
			collection: collBlobs,
			model: mongo.IndexModel{
				Keys: bson.D{
					{Key: "shard_id", Value: 1},
					{Key: "namespace", Value: 1},
					{Key: "run_id", Value: 1},
					{Key: "id", Value: 1},
				},
				Options: options.Index().SetUnique(true).SetName("pk_idx"),
			},
		},
	}
	return ensureIndexes(ctx, db, specs)
}

// EnsureSchemaTasklists creates the tasklist collection's indexes. Metadata
// rows + task rows share the collection (same tasklist_key for single-shard
// transactions). The compound index serves GetTasks range scans.
func EnsureSchemaTasklists(ctx context.Context, db *mongo.Database) error {
	specs := []mongoIndexSpec{
		{
			collection: collTasklist,
			model: mongo.IndexModel{
				Keys:    bson.D{{Key: "tasklist_key", Value: 1}, {Key: fieldRowType, Value: 1}, {Key: fieldTaskID, Value: 1}},
				Options: options.Index().SetName("tasklist_range_read_idx"),
			},
		},
	}
	return ensureIndexes(ctx, db, specs)
}

// EnsureSchemaVisibility creates the visibility collection's indexes. Sharded
// by namespace; PK = (namespace, run_id); two list indexes — by start_time
// (creation order) and by updated_at (recent activity / end time for terminal
// statuses). See docs/visibility-store-design.md for rationale.
func EnsureSchemaVisibility(ctx context.Context, db *mongo.Database) error {
	specs := []mongoIndexSpec{
		{
			collection: collVisibility,
			model: mongo.IndexModel{
				Keys:    bson.D{{Key: fieldNamespace, Value: 1}, {Key: fieldRunID, Value: 1}},
				Options: options.Index().SetUnique(true).SetName("pk_idx"),
			},
		},
		{
			collection: collVisibility,
			model: mongo.IndexModel{
				Keys: bson.D{
					{Key: fieldNamespace, Value: 1},
					{Key: fieldFlowType, Value: 1},
					{Key: fieldStatus, Value: 1},
					{Key: fieldStartTime, Value: -1},
					{Key: fieldRunID, Value: 1},
				},
				Options: options.Index().SetName("list_by_start_time_idx"),
			},
		},
		{
			collection: collVisibility,
			model: mongo.IndexModel{
				Keys: bson.D{
					{Key: fieldNamespace, Value: 1},
					{Key: fieldFlowType, Value: 1},
					{Key: fieldStatus, Value: 1},
					{Key: fieldUpdatedAt, Value: -1},
					{Key: fieldRunID, Value: 1},
				},
				Options: options.Index().SetName("list_by_updated_at_idx"),
			},
		},
	}
	return ensureIndexes(ctx, db, specs)
}

// EnsureSchemaHistory creates the history collection's indexes. Sharded by
// run_id; PK = (run_id, event_id) for ordered reads.
func EnsureSchemaHistory(ctx context.Context, db *mongo.Database) error {
	specs := []mongoIndexSpec{
		{
			collection: collHistory,
			model: mongo.IndexModel{
				Keys:    bson.D{{Key: fieldRunID, Value: 1}, {Key: fieldEventID, Value: 1}},
				Options: options.Index().SetUnique(true).SetName("pk_idx"),
			},
		},
	}
	return ensureIndexes(ctx, db, specs)
}

type mongoIndexSpec struct {
	collection string
	model      mongo.IndexModel
}

func ensureIndexes(ctx context.Context, db *mongo.Database, specs []mongoIndexSpec) error {
	for _, idx := range specs {
		if _, err := db.Collection(idx.collection).Indexes().CreateOne(ctx, idx.model); err != nil {
			return err
		}
	}
	return nil
}

// EnsureSchemaForDatabase creates every collection's indexes in a single
// named database. Used by per-package test mains that route every store to
// one throw-away database for test isolation. Production deployments run
// v0.js via mongosh and use the per-store database layout; for that path
// see EnsureSchemaForConfig.
func EnsureSchemaForDatabase(ctx context.Context, uri string, dbName string) error {
	client, err := connectMongo(ctx, uri)
	if err != nil {
		return err
	}
	defer client.Disconnect(ctx)
	db := client.Database(dbName)

	// Drop all collections first to avoid duplicate-key errors from stale
	// test data when (re)creating unique indexes.
	for _, coll := range allCollectionNames() {
		_ = db.Collection(coll).Drop(ctx)
	}

	return EnsureSchema(ctx, db)
}

// EnsureSchemaForConfig connects to each per-store cluster (resolved via
// cfg.For(store)) and ensures the indexes for that store only. Drops the
// store's collections first so tests get a clean slate. Use this in
// integration tests and one-shot setup scripts that want the per-store
// database layout.
func EnsureSchemaForConfig(ctx context.Context, cfg config.MongoPersistenceConfig) error {
	for _, store := range config.AllStoreNames() {
		resolved := cfg.For(store)
		if err := ensureSchemaForOneStore(ctx, store, resolved); err != nil {
			return fmt.Errorf("ensure schema for store %q: %w", store, err)
		}
	}
	return nil
}

func ensureSchemaForOneStore(ctx context.Context, store string, resolved config.MongoConfig) error {
	if resolved.Database == "" {
		return fmt.Errorf("ensure schema: store %q resolved to empty database (set per-store config)", store)
	}
	client, err := connectMongo(ctx, resolved.URI)
	if err != nil {
		return err
	}
	defer client.Disconnect(ctx)
	db := client.Database(resolved.Database)

	for _, coll := range collectionsForStore(store) {
		_ = db.Collection(coll).Drop(ctx)
	}

	switch store {
	case config.StoreShards:
		return EnsureSchemaShards(ctx, db)
	case config.StoreRuns:
		return EnsureSchemaRuns(ctx, db)
	case config.StoreBlobs:
		return EnsureSchemaBlobs(ctx, db)
	case config.StoreTasklists:
		return EnsureSchemaTasklists(ctx, db)
	case config.StoreVisibility:
		return EnsureSchemaVisibility(ctx, db)
	case config.StoreHistory:
		return EnsureSchemaHistory(ctx, db)
	default:
		return fmt.Errorf("unknown store %q", store)
	}
}

func collectionsForStore(store string) []string {
	switch store {
	case config.StoreShards:
		return []string{collShards}
	case config.StoreRuns:
		return []string{collRuns, collTaskDLQ}
	case config.StoreBlobs:
		return []string{collBlobs}
	case config.StoreTasklists:
		return []string{collTasklist}
	case config.StoreVisibility:
		return []string{collVisibility}
	case config.StoreHistory:
		return []string{collHistory}
	default:
		return nil
	}
}

func allCollectionNames() []string {
	return []string{
		collRuns, collBlobs, collTaskDLQ,
		collShards,
		collTasklist,
		collVisibility,
		collHistory,
	}
}
