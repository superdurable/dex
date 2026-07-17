package persistence

import (
	"common-go/ids"
	"context"
	"time"

	"github.com/superdurable/dex/server/internal/errors"
)

// ShardReleaseEntry identifies a shard to release with its expected version.
type ShardReleaseEntry struct {
	ShardID         int32
	ExpectedVersion int64
}

type Shard struct {
	ShardID        int32
	Version        int64
	MemberID       string
	ClaimedAt      time.Time
	LeaseExpiresAt time.Time
	ReleasedAt     *time.Time
	Metadata       ShardMetadata
}

type ShardMetadata struct {
	// RangeID is incremented on each ClaimShard.
	RangeID int32 `bson:"range_id" json:"range_id"`

	// ImmediateTaskCommittedSeq is the watermark up to which immediate tasks
	// have been committed (processed and safe to delete).
	ImmediateTaskCommittedSeq int64 `bson:"immediate_task_committed_seq" json:"immediate_task_committed_seq"`

	// TimerTaskCommittedSortKey + TimerTaskCommittedID form the compound
	// watermark for timer tasks. Timer tasks use (SortKey, ID) as their
	// ordering key since multiple timers can share the same SortKey.
	TimerTaskCommittedSortKey int64   `bson:"timer_task_committed_sort_key" json:"timer_task_committed_sort_key"`
	TimerTaskCommittedID      ids.UID `bson:"timer_task_committed_id" json:"timer_task_committed_id"`
}

// ShardStore manages shard ownership.
type ShardStore interface {
	// ClaimShard claims a shard, incrementing RangeID in metadata. Returns the
	// shard with updated RangeID so the caller can initialize TaskSeq generation.
	ClaimShard(ctx context.Context, shardID int32, memberID string, leaseDuration time.Duration) (*Shard, errors.CategorizedError)
	// RenewShardLease renews the lease and atomically persists committed task
	// offsets from metadata.
	RenewShardLease(ctx context.Context, shardID int32, memberID string, expectedVersion int64, leaseDuration time.Duration, metadata *ShardMetadata) (leaseExpiresAt time.Time, _ errors.CategorizedError)
	ReleaseShard(ctx context.Context, shardID int32, memberID string, expectedVersion int64) errors.CategorizedError
	BatchReleaseShards(ctx context.Context, memberID string, entries []ShardReleaseEntry) errors.CategorizedError
	Close() error
}

// RunStore manages run rows, immediate tasks, and timer tasks (all in the runs collection).
type RunStore interface {
	// CreateRunWithTasks atomically inserts a run_row and one or more task rows.
	CreateRunWithTasks(ctx context.Context, run *RunRow, tasks []TaskRow) errors.CategorizedError
	GetRun(ctx context.Context, shardID int32, namespace, runID string) (*RunRow, errors.CategorizedError)

	// UpdateRunWithNewTasks does a CAS update on the run_row (version check)
	// and atomically inserts new task rows. Returns CASError on version mismatch.
	UpdateRunWithNewTasks(ctx context.Context, shardID int32, namespace, runID string,
		expectedVersion int64, update *RunRowUpdate, newTasks []TaskRow) errors.CategorizedError

	// RangeReadImmediateTasks read task in range — ordered by (sort_key, id) using the pk_idx index.
	// Immediate tasks: sort_key is TaskSeq (RangeID<<32 | LocalSeq), afterSeq-based cursor.
	// Timer tasks: sort_key is fire_at_unix_ms, compound (afterSortKey, afterID) cursor.
	// OpsFIFO tasks: sort_key is per-shard OpsFIFO TaskSeq, afterSeq-based cursor (same shape as immediate).
	RangeReadImmediateTasks(ctx context.Context, shardID int32, afterSeq int64, limit int) ([]*ImmediateTaskRow, errors.CategorizedError)
	RangeReadTimerTasks(ctx context.Context, shardID int32, sortKeyUpTo int64, afterSortKey int64, afterID ids.UID, limit int) ([]*TimerTaskRow, errors.CategorizedError)

	// RangeDeleteImmediateTasks etc delete tasks in range  — deletes all tasks up to the given watermark.
	// Immediate: deletes sort_key <= upToSeq (inclusive, watermark is min-1).
	// Timer: deletes (sort_key, id) < (upToSortKey, upToID) (exclusive, watermark is min of pending).
	// OpsFIFO: deletes sort_key <= upToSeq (inclusive, watermark is the highest seq of the
	//      most recently completed batch — see ops_batch_deleter for why this can be
	//      a simple atomic int64 rather than a min-of-pending).
	RangeDeleteImmediateTasks(ctx context.Context, shardID int32, upToSeq int64) errors.CategorizedError
	RangeDeleteTimerTasks(ctx context.Context, shardID int32, upToSortKey int64, upToID ids.UID) errors.CategorizedError

	// DeleteImmediateTasksByIDBatch etc delete tasks by ID batch — used only during shutdown to clean up tasks
	// that completed above the watermark but haven't been range-deleted yet.
	DeleteImmediateTasksByIDBatch(ctx context.Context, shardID int32, UIDs []ids.UID) errors.CategorizedError
	DeleteTimerTasksByIDBatch(ctx context.Context, shardID int32, UIDs []ids.UID) errors.CategorizedError

	// DeleteAll removes all rows (runs + tasks) from the runs collection.
	// Test-only: used to ensure clean state between tests.
	DeleteAll(ctx context.Context) error

	Close() error
}
