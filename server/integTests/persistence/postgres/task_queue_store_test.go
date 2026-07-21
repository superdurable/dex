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

package postgres_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	p "github.com/superdurable/dex/server/internal/persistence"
)

var queueSeq atomic.Int64

type queueKey struct {
	namespace   string
	queueName   string
	partitionID int32
}

func nextQueueKey(t *testing.T) queueKey {
	t.Helper()
	n := queueSeq.Add(1)
	return queueKey{
		namespace:   fmt.Sprintf("tq-ns-%d", n),
		queueName:   fmt.Sprintf("tq-queue-%d", n),
		partitionID: int32(n % 16),
	}
}

func claimOK(t *testing.T, ctx context.Context, key queueKey, memberID, address string) *p.TaskQueueInfo {
	t.Helper()
	info, err := taskQueueStore.ClaimTaskQueue(ctx, key.namespace, key.queueName, key.partitionID, memberID, address)
	require.NoError(t, err)
	require.NotNil(t, info)
	return info
}

func makeTask(taskID int64, runID string, shardID int32) *p.TaskQueueTaskRow {
	return &p.TaskQueueTaskRow{
		TaskID:  taskID,
		RunID:   runID,
		ShardID: shardID,
	}
}

func TestTaskQueueStore_FirstClaim(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key := nextQueueKey(t)

	before := time.Now()
	info := claimOK(t, ctx, key, "member-a", "127.0.0.1:9001")
	require.Equal(t, key.namespace, info.Namespace)
	require.Equal(t, key.queueName, info.TaskQueueName)
	require.Equal(t, key.partitionID, info.PartitionID)
	require.Equal(t, int32(1), info.RangeID)
	require.Equal(t, int64(0), info.AckLevel)
	require.Equal(t, "member-a", info.OwnerMemberID)
	require.Equal(t, "127.0.0.1:9001", info.OwnerAddress)
	require.False(t, info.ClaimedAt.Before(before))
}

func TestTaskQueueStore_ReclaimBumpsRangePreservesAck(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key := nextQueueKey(t)

	first := claimOK(t, ctx, key, "member-a", "addr-a")
	require.NoError(t, taskQueueStore.UpdateTaskQueueInfo(ctx, key.namespace, key.queueName, key.partitionID, first.RangeID, 42))

	second := claimOK(t, ctx, key, "member-b", "addr-b")
	require.Equal(t, first.RangeID+1, second.RangeID)
	require.Equal(t, int64(42), second.AckLevel)
	require.Equal(t, "member-b", second.OwnerMemberID)
	require.Equal(t, "addr-b", second.OwnerAddress)

	// Same member reclaim also bumps range_id.
	third := claimOK(t, ctx, key, "member-b", "addr-b2")
	require.Equal(t, second.RangeID+1, third.RangeID)
	require.Equal(t, int64(42), third.AckLevel)
	require.Equal(t, "addr-b2", third.OwnerAddress)
}

func TestTaskQueueStore_GetInfo_NotFoundThenAfterClaim(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key := nextQueueKey(t)

	_, err := taskQueueStore.GetTaskQueueInfo(ctx, key.namespace, key.queueName, key.partitionID)
	require.Error(t, err)
	require.True(t, err.IsNotFoundError(), "got %v", err)

	claimed := claimOK(t, ctx, key, "member-a", "addr-a")
	got, err := taskQueueStore.GetTaskQueueInfo(ctx, key.namespace, key.queueName, key.partitionID)
	require.NoError(t, err)
	require.Equal(t, claimed.RangeID, got.RangeID)
	require.Equal(t, claimed.AckLevel, got.AckLevel)
	require.Equal(t, claimed.OwnerMemberID, got.OwnerMemberID)
	require.Equal(t, claimed.OwnerAddress, got.OwnerAddress)
}

func TestTaskQueueStore_UpdateInfo_OK(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key := nextQueueKey(t)

	info := claimOK(t, ctx, key, "member-a", "addr-a")
	require.NoError(t, taskQueueStore.UpdateTaskQueueInfo(ctx, key.namespace, key.queueName, key.partitionID, info.RangeID, 100))

	got, err := taskQueueStore.GetTaskQueueInfo(ctx, key.namespace, key.queueName, key.partitionID)
	require.NoError(t, err)
	require.Equal(t, int64(100), got.AckLevel)
	require.Equal(t, info.RangeID, got.RangeID)
}

func TestTaskQueueStore_UpdateInfo_StaleRange(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key := nextQueueKey(t)

	first := claimOK(t, ctx, key, "member-a", "addr-a")
	_ = claimOK(t, ctx, key, "member-b", "addr-b")

	err := taskQueueStore.UpdateTaskQueueInfo(ctx, key.namespace, key.queueName, key.partitionID, first.RangeID, 7)
	require.Error(t, err)
	require.True(t, err.IsConflictError(), "got %v", err)
}

func TestTaskQueueStore_UpdateInfo_MissingRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key := nextQueueKey(t)

	err := taskQueueStore.UpdateTaskQueueInfo(ctx, key.namespace, key.queueName, key.partitionID, 1, 1)
	require.Error(t, err)
	require.True(t, err.IsConflictError(), "got %v", err)
}

func TestTaskQueueStore_CreateTasks_ThenGetTasks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key := nextQueueKey(t)

	info := claimOK(t, ctx, key, "member-a", "addr-a")
	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	tasks := []*p.TaskQueueTaskRow{
		{TaskID: 10, RunID: "run-b", ShardID: 2, CreatedAt: createdAt},
		{TaskID: 5, RunID: "run-a", ShardID: 1}, // zero CreatedAt filled by store
	}
	require.NoError(t, taskQueueStore.CreateTasks(ctx, key.namespace, key.queueName, key.partitionID, info.RangeID, tasks))

	got, err := taskQueueStore.GetTasks(ctx, key.namespace, key.queueName, key.partitionID, 0, 100, 10)
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, int64(5), got[0].TaskID)
	require.Equal(t, "run-a", got[0].RunID)
	require.Equal(t, int32(1), got[0].ShardID)
	require.False(t, got[0].CreatedAt.IsZero())
	require.Equal(t, int64(10), got[1].TaskID)
	require.Equal(t, "run-b", got[1].RunID)
	require.Equal(t, int32(2), got[1].ShardID)
	require.True(t, got[1].CreatedAt.Equal(createdAt) || got[1].CreatedAt.Truncate(time.Microsecond).Equal(createdAt))
	require.Equal(t, key.namespace, got[0].Namespace)
	require.Equal(t, key.queueName, got[0].TaskQueueName)
	require.Equal(t, key.partitionID, got[0].PartitionID)
}

func TestTaskQueueStore_CreateTasks_EmptyNoOp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key := nextQueueKey(t)

	info := claimOK(t, ctx, key, "member-a", "addr-a")
	require.NoError(t, taskQueueStore.CreateTasks(ctx, key.namespace, key.queueName, key.partitionID, info.RangeID, nil))
	require.NoError(t, taskQueueStore.CreateTasks(ctx, key.namespace, key.queueName, key.partitionID, info.RangeID, []*p.TaskQueueTaskRow{}))

	got, err := taskQueueStore.GetTaskQueueInfo(ctx, key.namespace, key.queueName, key.partitionID)
	require.NoError(t, err)
	require.Equal(t, info.RangeID, got.RangeID)
	require.Equal(t, info.AckLevel, got.AckLevel)
}

func TestTaskQueueStore_CreateTasks_StaleRangeAfterSteal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key := nextQueueKey(t)

	ownerA := claimOK(t, ctx, key, "member-a", "addr-a")
	require.NoError(t, taskQueueStore.CreateTasks(ctx, key.namespace, key.queueName, key.partitionID, ownerA.RangeID, []*p.TaskQueueTaskRow{
		makeTask(1, "run-1", 1),
	}))

	ownerB := claimOK(t, ctx, key, "member-b", "addr-b")
	err := taskQueueStore.CreateTasks(ctx, key.namespace, key.queueName, key.partitionID, ownerA.RangeID, []*p.TaskQueueTaskRow{
		makeTask(2, "run-stale", 1),
	})
	require.Error(t, err)
	require.True(t, err.IsConflictError(), "got %v", err)

	require.NoError(t, taskQueueStore.CreateTasks(ctx, key.namespace, key.queueName, key.partitionID, ownerB.RangeID, []*p.TaskQueueTaskRow{
		makeTask(3, "run-3", 1),
	}))

	got, getErr := taskQueueStore.GetTasks(ctx, key.namespace, key.queueName, key.partitionID, 0, 100, 10)
	require.NoError(t, getErr)
	require.Len(t, got, 2)
	require.Equal(t, int64(1), got[0].TaskID)
	require.Equal(t, int64(3), got[1].TaskID)
}

func TestTaskQueueStore_CreateTasks_DuplicateTaskID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key := nextQueueKey(t)

	info := claimOK(t, ctx, key, "member-a", "addr-a")
	require.NoError(t, taskQueueStore.CreateTasks(ctx, key.namespace, key.queueName, key.partitionID, info.RangeID, []*p.TaskQueueTaskRow{
		makeTask(50, "run-1", 1),
	}))

	// Batch with a new row plus a duplicate: whole txn must roll back.
	err := taskQueueStore.CreateTasks(ctx, key.namespace, key.queueName, key.partitionID, info.RangeID, []*p.TaskQueueTaskRow{
		makeTask(51, "run-new", 1),
		makeTask(50, "run-dup", 1),
	})
	require.Error(t, err)
	require.True(t, err.IsConflictError(), "got %v", err)

	got, getErr := taskQueueStore.GetTasks(ctx, key.namespace, key.queueName, key.partitionID, 0, 100, 10)
	require.NoError(t, getErr)
	require.Len(t, got, 1)
	require.Equal(t, int64(50), got[0].TaskID)
	require.Equal(t, "run-1", got[0].RunID)
}

func TestTaskQueueStore_GetTasks_BoundsAndBatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key := nextQueueKey(t)

	info := claimOK(t, ctx, key, "member-a", "addr-a")
	require.NoError(t, taskQueueStore.CreateTasks(ctx, key.namespace, key.queueName, key.partitionID, info.RangeID, []*p.TaskQueueTaskRow{
		makeTask(1, "r1", 1),
		makeTask(2, "r2", 1),
		makeTask(3, "r3", 1),
		makeTask(4, "r4", 1),
		makeTask(5, "r5", 1),
	}))

	// (readLevel, maxReadLevel] — exclusive lower, inclusive upper.
	got, err := taskQueueStore.GetTasks(ctx, key.namespace, key.queueName, key.partitionID, 1, 3, 10)
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, int64(2), got[0].TaskID)
	require.Equal(t, int64(3), got[1].TaskID)

	page1, err := taskQueueStore.GetTasks(ctx, key.namespace, key.queueName, key.partitionID, 0, 5, 2)
	require.NoError(t, err)
	require.Len(t, page1, 2)
	require.Equal(t, int64(1), page1[0].TaskID)
	require.Equal(t, int64(2), page1[1].TaskID)

	page2, err := taskQueueStore.GetTasks(ctx, key.namespace, key.queueName, key.partitionID, page1[1].TaskID, 5, 2)
	require.NoError(t, err)
	require.Len(t, page2, 2)
	require.Equal(t, int64(3), page2[0].TaskID)
	require.Equal(t, int64(4), page2[1].TaskID)
}

func TestTaskQueueStore_DeleteTasksLessThan(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key := nextQueueKey(t)

	info := claimOK(t, ctx, key, "member-a", "addr-a")
	require.NoError(t, taskQueueStore.CreateTasks(ctx, key.namespace, key.queueName, key.partitionID, info.RangeID, []*p.TaskQueueTaskRow{
		makeTask(1, "r1", 1),
		makeTask(2, "r2", 1),
		makeTask(3, "r3", 1),
		makeTask(4, "r4", 1),
	}))

	// limit is intentionally ignored — all task_id <= 2 are deleted.
	n, err := taskQueueStore.DeleteTasksLessThan(ctx, key.namespace, key.queueName, key.partitionID, 2, 1)
	require.NoError(t, err)
	require.Equal(t, 2, n)

	got, getErr := taskQueueStore.GetTasks(ctx, key.namespace, key.queueName, key.partitionID, 0, 100, 10)
	require.NoError(t, getErr)
	require.Len(t, got, 2)
	require.Equal(t, int64(3), got[0].TaskID)
	require.Equal(t, int64(4), got[1].TaskID)
}

func TestTaskQueueStore_DeleteTasksByIDBatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key := nextQueueKey(t)

	info := claimOK(t, ctx, key, "member-a", "addr-a")
	require.NoError(t, taskQueueStore.CreateTasks(ctx, key.namespace, key.queueName, key.partitionID, info.RangeID, []*p.TaskQueueTaskRow{
		makeTask(10, "r10", 1),
		makeTask(20, "r20", 1),
		makeTask(30, "r30", 1),
	}))

	require.NoError(t, taskQueueStore.DeleteTasksByIDBatch(ctx, key.namespace, key.queueName, key.partitionID, nil))
	require.NoError(t, taskQueueStore.DeleteTasksByIDBatch(ctx, key.namespace, key.queueName, key.partitionID, []int64{}))
	require.NoError(t, taskQueueStore.DeleteTasksByIDBatch(ctx, key.namespace, key.queueName, key.partitionID, []int64{999}))

	require.NoError(t, taskQueueStore.DeleteTasksByIDBatch(ctx, key.namespace, key.queueName, key.partitionID, []int64{10, 30}))

	// Unfenced: still works after another member steals the claim.
	_ = claimOK(t, ctx, key, "member-b", "addr-b")
	require.NoError(t, taskQueueStore.DeleteTasksByIDBatch(ctx, key.namespace, key.queueName, key.partitionID, []int64{20}))

	got, err := taskQueueStore.GetTasks(ctx, key.namespace, key.queueName, key.partitionID, 0, 100, 10)
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestTaskQueueStore_NamespaceQueuePartitionIsolation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	keyA := nextQueueKey(t)
	keyB := nextQueueKey(t)

	infoA := claimOK(t, ctx, keyA, "member-a", "addr-a")
	infoB := claimOK(t, ctx, keyB, "member-b", "addr-b")
	require.NoError(t, taskQueueStore.CreateTasks(ctx, keyA.namespace, keyA.queueName, keyA.partitionID, infoA.RangeID, []*p.TaskQueueTaskRow{
		makeTask(1, "run-a", 1),
	}))
	require.NoError(t, taskQueueStore.CreateTasks(ctx, keyB.namespace, keyB.queueName, keyB.partitionID, infoB.RangeID, []*p.TaskQueueTaskRow{
		makeTask(1, "run-b", 2),
	}))

	gotA, err := taskQueueStore.GetTasks(ctx, keyA.namespace, keyA.queueName, keyA.partitionID, 0, 100, 10)
	require.NoError(t, err)
	require.Len(t, gotA, 1)
	require.Equal(t, "run-a", gotA[0].RunID)

	gotB, err := taskQueueStore.GetTasks(ctx, keyB.namespace, keyB.queueName, keyB.partitionID, 0, 100, 10)
	require.NoError(t, err)
	require.Len(t, gotB, 1)
	require.Equal(t, "run-b", gotB[0].RunID)
}

// Concurrent claims all succeed (steal-on-claim); only the highest range_id can CreateTasks.
func TestTaskQueueStore_ConcurrentClaim_FencedWrite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key := nextQueueKey(t)
	const claimers = 8

	var wg sync.WaitGroup
	infos := make([]*p.TaskQueueInfo, claimers)
	claimErrs := make([]error, claimers)
	for i := 0; i < claimers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			info, err := taskQueueStore.ClaimTaskQueue(ctx, key.namespace, key.queueName, key.partitionID,
				fmt.Sprintf("member-%d", i), fmt.Sprintf("addr-%d", i))
			infos[i] = info
			if err != nil {
				claimErrs[i] = err
			}
		}(i)
	}
	wg.Wait()

	var maxRange int32
	seen := make(map[int32]struct{}, claimers)
	for i := 0; i < claimers; i++ {
		require.NoError(t, claimErrs[i], "claimer %d", i)
		require.NotNil(t, infos[i])
		_, dup := seen[infos[i].RangeID]
		require.False(t, dup, "duplicate range_id %d", infos[i].RangeID)
		seen[infos[i].RangeID] = struct{}{}
		if infos[i].RangeID > maxRange {
			maxRange = infos[i].RangeID
		}
	}
	require.Equal(t, claimers, len(seen))
	require.Equal(t, int32(claimers), maxRange)

	var createOK atomic.Int32
	var unexpected atomic.Int32
	wg = sync.WaitGroup{}
	for i := 0; i < claimers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := taskQueueStore.CreateTasks(ctx, key.namespace, key.queueName, key.partitionID, infos[i].RangeID, []*p.TaskQueueTaskRow{
				makeTask(int64(1000+i), fmt.Sprintf("run-%d", i), 1),
			})
			if err == nil {
				createOK.Add(1)
				return
			}
			if !err.IsConflictError() {
				unexpected.Add(1)
			}
		}(i)
	}
	wg.Wait()

	require.Equal(t, int32(0), unexpected.Load(), "non-Conflict errors from CreateTasks")
	require.Equal(t, int32(1), createOK.Load(), "only latest range_id can create")

	got, err := taskQueueStore.GetTasks(ctx, key.namespace, key.queueName, key.partitionID, 0, 2000, 20)
	require.NoError(t, err)
	require.Len(t, got, 1)
}
