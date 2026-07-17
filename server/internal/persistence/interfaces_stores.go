// Copyright (c) 2023-2026 Super Durable, Inc.
//
// This file is part of Dex
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program. If not, see <https://www.gnu.org/licenses/>.

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
	// have been committed -- processed and safe to delete.
	ImmediateTaskCommittedSeq int64 `bson:"immediate_task_committed_seq" json:"immediate_task_committed_seq"`

	// TimerTaskCommittedSortKey + TimerTaskCommittedID is the watermark
	// for timer tasks that are committed.
	// SortKey is firing time which is not unique.
	TimerTaskCommittedSortKey int64   `bson:"timer_task_committed_sort_key" json:"timer_task_committed_sort_key"`
	TimerTaskCommittedID      ids.UID `bson:"timer_task_committed_id" json:"timer_task_committed_id"`
}

// ShardStore manages shard ownership.
type ShardStore interface {
	ClaimShard(ctx context.Context, shardID int32, memberID string, leaseDuration time.Duration) (*Shard, errors.CategorizedError)
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

	UpdateRunWithNewTasks(ctx context.Context, shardID int32, namespace, runID string,
		expectedVersion int64, update *RunRowUpdate, newTasks []TaskRow) errors.CategorizedError

	RangeReadImmediateTasks(ctx context.Context, shardID int32, afterSeq int64, limit int) ([]*ImmediateTaskRow, errors.CategorizedError)
	RangeReadTimerTasks(ctx context.Context, shardID int32, sortKeyUpTo int64, afterSortKey int64, afterID ids.UID, limit int) ([]*TimerTaskRow, errors.CategorizedError)

	RangeDeleteImmediateTasks(ctx context.Context, shardID int32, upToSeq int64) errors.CategorizedError
	RangeDeleteTimerTasks(ctx context.Context, shardID int32, upToSortKey int64, upToID ids.UID) errors.CategorizedError

	DeleteImmediateTasksByIDBatch(ctx context.Context, shardID int32, UIDs []ids.UID) errors.CategorizedError
	DeleteTimerTasksByIDBatch(ctx context.Context, shardID int32, UIDs []ids.UID) errors.CategorizedError

	DeleteAll(ctx context.Context) error

	Close() error
}

type BlobEntry struct {
	BlobID   ids.UID
	Encoding string
	Payload  []byte
}

type BlobStore interface {
	BatchInsert(ctx context.Context, shardID int32, namespace, runID string, blobs []BlobEntry) errors.CategorizedError
	BatchGet(ctx context.Context, shardID int32, namespace, runID string, blobIDs []ids.UID) ([]BlobEntry, errors.CategorizedError)
	Close() error
}

type TaskQueueInfo struct {
	Namespace     string
	TaskQueueName string
	PartitionID   int32
	RangeID       int32
	AckLevel      int64
	OwnerMemberID string
	OwnerAddress  string
	ClaimedAt     time.Time
}

// TaskQueueTaskRow is a single dispatch task in the taskQueue_tasks collection.
type TaskQueueTaskRow struct {
	Namespace     string
	TaskQueueName string
	PartitionID   int32
	TaskID        int64 // (int64(rangeID) << 32) | int64(localSeq)
	RunID         string
	ShardID       int32
	CreatedAt     time.Time
}

type TaskQueueStore interface {
	GetTaskQueueInfo(ctx context.Context, namespace, taskQueueName string, partitionID int32) (*TaskQueueInfo, errors.CategorizedError)
	// ClaimTaskQueue increments the range_id as fencing token
	// Any feature write operations will use the rangeId to fence
	ClaimTaskQueue(ctx context.Context, namespace, taskQueueName string, partitionID int32, memberID, matchingAddress string) (*TaskQueueInfo, errors.CategorizedError)

	// UpdateTaskQueueInfo performs a update
	// Returns OwnerVersionMismatchError if range_id doesn't match.
	UpdateTaskQueueInfo(ctx context.Context, namespace, taskQueueName string, partitionID int32, rangeID int32, ackLevel int64) errors.CategorizedError

	CreateTasks(ctx context.Context, namespace, taskQueueName string, partitionID int32, rangeID int32, tasks []*TaskQueueTaskRow) errors.CategorizedError

	// GetTasks load task in (readLevel, maxReadLevel])
	GetTasks(ctx context.Context, namespace, taskQueueName string, partitionID int32, readLevel, maxReadLevel int64, batchSize int) ([]*TaskQueueTaskRow, errors.CategorizedError)

	// DeleteTasksLessThan deletes task rows where task_id <= ackLevel
	// Note that limit may not be honored
	DeleteTasksLessThan(ctx context.Context, namespace, taskQueueName string, partitionID int32, ackLevel int64, limit int) (int, errors.CategorizedError)

	// DeleteTasksByIDBatch deletes task by a batch of task_ids
	// Note that this is not fenced
	DeleteTasksByIDBatch(ctx context.Context, namespace, taskQueueName string, partitionID int32, taskIDs []int64) errors.CategorizedError

	Close() error
}
