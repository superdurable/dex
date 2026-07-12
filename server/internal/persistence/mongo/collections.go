package mongo

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readconcern"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
)

// OperationTimeouts holds the per-operation timeout caps applied by each store.
// If the caller's context already has a tighter deadline, the caller's wins.
type OperationTimeouts struct {
	Short time.Duration // single-doc ops: FindOne, UpdateOne, InsertOne, etc.
	Long  time.Duration // multi-doc ops: Find+cursor, DeleteMany, BulkWrite, etc.
}

// DefaultOperationTimeouts returns sensible defaults matching DefaultMongoConfig.
func DefaultOperationTimeouts() OperationTimeouts {
	return OperationTimeouts{Short: 5 * time.Second, Long: 30 * time.Second}
}

// cappedCtx returns a context with a timeout cap. If the parent context already
// has a tighter deadline, it is returned as-is (cancel is a no-op).
// cappedCtx returns a context with a timeout cap. If the parent context already
// has a tighter deadline, it is returned as-is (cancel is a no-op).
func cappedCtx(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if deadline, ok := parent.Deadline(); ok {
		if time.Until(deadline) <= timeout {
			return parent, func() {}
		}
	}
	return context.WithTimeout(parent, timeout)
}

// connectMongo creates a MongoDB client with strong consistency guarantees:
//   - WriteConcern majority: writes are durable (acknowledged by replica majority)
//   - ReadConcern majority: reads only return committed data
//   - ReadPreference primary: all reads go to the primary
//
// These are required for correctness of CAS (compare-and-swap) operations
// used throughout shard leasing, run state transitions, and tasklist
// ownership (range_id fencing).
func connectMongo(ctx context.Context, uri string) (*mongo.Client, error) {
	return mongo.Connect(ctx, options.Client().
		ApplyURI(uri).
		SetWriteConcern(writeconcern.Majority()).
		SetReadConcern(readconcern.Majority()).
		SetReadPreference(readpref.Primary()))
}

// resolveDatabase returns name when non-empty, else fallback. Constructors
// pass the per-store default (defaultRunsDatabase, defaultShardsDatabase, etc.)
// as fallback so an empty argument resolves to the matching store's
// production default name. Production deployments configure databases via
// config.MongoPersistenceConfig — these constants are the in-code mirror of
// the per-store databases declared in v0.js.
func resolveDatabase(name, fallback string) string {
	if name == "" {
		return fallback
	}
	return name
}

// Per-store default database names. Mirrors the databases provisioned by
// v0.js (server/internal/persistence/mongo/schema/v0.js) and the per-store
// defaults in config.DefaultMongoPersistenceConfig. DLQ rows are co-located
// with run state (DLQ entries reference task rows in `runs`), so the DLQ
// constructor falls back to defaultRunsDatabase. Blobs get their own
// database so operators can host blob payloads on a dedicated cluster
// independently of the run state.
const (
	defaultRunsDatabase       = "dex_runs"
	defaultBlobsDatabase      = "dex_blobs"
	defaultShardsDatabase     = "dex_shards"
	defaultTasklistsDatabase  = "dex_tasklists"
	defaultVisibilityDatabase = "dex_visibility"
	defaultHistoryDatabase    = "dex_history"
)

const (
	collRuns       = "runs"
	collBlobs      = "blobs"
	collShards     = "shards"
	collTasklist   = "tasklist"
	collVisibility = "visibility"
	collHistory    = "history"
)

const (
	fieldShardID   = "shard_id"
	fieldRowType   = "row_type"
	fieldNamespace = "namespace"
	fieldSortKey   = "sort_key"
	fieldID        = "id"
	fieldVersion   = "version"
	fieldStatus    = "status"
	fieldMemberID  = "member_id"
	fieldRunID     = "run_id"
	fieldCreatedAt = "created_at"
	fieldUpdatedAt = "updated_at"
	fieldData      = "data"

	fieldLeaseExpiresAt = "lease_expires_at"
	fieldClaimedAt      = "claimed_at"
	fieldReleasedAt     = "released_at"
	fieldMetadata       = "metadata"

	fieldFlowType           = "flow_type"
	fieldEventID            = "event_id"
	fieldEventType          = "event_type"
	fieldStartTime          = "start_time"
	fieldLastHistoryEventID = "last_history_event_id"

	fieldStateMap                      = "state_map"
	fieldUnconsumedChannelMessages     = "unconsumed_channel_messages"
	fieldStepExeIDCounters             = "step_exe_id_counters"
	fieldActiveStepExecutions          = "active_step_executions"
	fieldWorkerRequestCounter          = "worker_request_counter"
	fieldStepMethodExeCounter          = "step_method_exe_counter"
	fieldExternalChannelMessageCounter = "external_channel_message_counter"
	fieldLastHeartbeatTime             = "last_heartbeat_time"
	fieldHeartbeatTimerID              = "heartbeat_timer_id"
	fieldActiveDurableTimerID          = "active_durable_timer_id"
	fieldDurableTimerFireAt            = "durable_timer_fire_at"

	fieldTaskType = "task_type"
	fieldTaskInfo = "task_info"

	fieldTaskListName = "task_list_name"
	fieldWorkerID     = "worker_id"
	fieldPartitionID  = "partition_id"
	fieldRangeID      = "range_id"
	fieldAckLevel     = "ack_level"
	fieldTaskID       = "task_id"

	fieldOwnerMemberID = "owner_member_id"
	fieldOwnerAddress  = "owner_address"
	fieldMongoID       = "_id"
)

// Row types for the tasklist collection.
const (
	rowTypeTasklistMetadata int32 = 1
	rowTypeTasklistTask     int32 = 2
)
