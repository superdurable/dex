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
	"common-go/ids"
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/server/common/utils/ptr"
	p "github.com/superdurable/dex/server/internal/persistence"
)

func runScope(t *testing.T) (int32, string, string) {
	t.Helper()
	return nextShardID(t), "runns-" + ids.NewUID().String(), "run-" + ids.NewUID().String()
}

func intValue(n int64) p.Value {
	return p.Value{Type: p.ValueTypeInt, IntVal: ptr.Any(n)}
}

func newRunRow(shardID int32, namespace, runID string) *p.RunRow {
	return &p.RunRow{
		ShardID:                       shardID,
		RowType:                       p.RowTypeRun,
		Namespace:                     namespace,
		ID:                            runID,
		FlowType:                      "flow",
		TaskQueueName:                 "tq",
		HeartbeatTimeoutSeconds:       30,
		Status:                        p.RunStatusPending,
		Version:                       1,
		WorkerID:                      "worker-1",
		Attributes:                    map[string]p.Value{"a": intValue(1)},
		UnconsumedChannelMessages:     map[string][]p.ChannelMessage{"ch1": {{ID: 1, Value: intValue(10)}}},
		StepExeIDCounters:             map[string]int32{"s1": 1},
		ActiveStepExecutions:          map[string]p.ActiveStepExecution{"s1": {Status: p.StepExeStatusInvokingExecute, Input: intValue(7)}},
		WorkerRequestCounter:          2,
		ExternalChannelMessageCounter: 3,
		StepMethodExeCounter:          4,
		HeartbeatTimerID:              ids.NewUID(),
		ActiveDurableTimerID:          ids.NewUID(),
		DurableTimerFiredAt:           99,
		LastHistoryEventID:            5,
	}
}

func immTask(shardID int32, namespace, runID string, seq int64) p.TaskRow {
	return p.TaskRow{Immediate: &p.ImmediateTaskRow{
		ShardID:   shardID,
		RowType:   p.RowTypeImmediateTask,
		Namespace: namespace,
		SortKey:   seq,
		ID:        ids.NewUID(),
		TaskType:  p.ImmediateTaskTypeRunInitialDispatch,
		TaskInfo:  p.ImmediateTaskInfo{RunID: runID, Namespace: namespace, TaskQueueName: "tq"},
	}}
}

func timerTask(shardID int32, namespace, runID string, sortKey int64, id ids.UID) p.TaskRow {
	return p.TaskRow{Timer: &p.TimerTaskRow{
		ShardID:   shardID,
		RowType:   p.RowTypeTimerTask,
		Namespace: namespace,
		SortKey:   sortKey,
		ID:        id,
		TaskType:  p.TimerTaskTypeStepWaitForTimer,
		TaskInfo:  p.TimerTaskInfo{RunID: runID, Namespace: namespace},
	}}
}

// ---- CreateRunWithTasks / GetRun ----

func TestRunStore_CreateAndGetRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID, namespace, runID := runScope(t)

	run := newRunRow(shardID, namespace, runID)
	require.NoError(t, runStore.CreateRunWithTasks(ctx, run, nil))

	got, err := runStore.GetRun(ctx, shardID, namespace, runID)
	require.NoError(t, err)
	require.Equal(t, p.RowTypeRun, got.RowType)
	require.Equal(t, int64(1), got.Version)
	require.Equal(t, "flow", got.FlowType)
	require.Equal(t, p.RunStatusPending, got.Status)
	require.Equal(t, "worker-1", got.WorkerID)
	require.Equal(t, run.Attributes, got.Attributes)
	require.Equal(t, run.UnconsumedChannelMessages, got.UnconsumedChannelMessages)
	require.Equal(t, run.StepExeIDCounters, got.StepExeIDCounters)
	require.Equal(t, run.ActiveStepExecutions, got.ActiveStepExecutions)
	require.Equal(t, int64(2), got.WorkerRequestCounter)
	require.Equal(t, int64(4), got.StepMethodExeCounter)
	require.Equal(t, run.HeartbeatTimerID, got.HeartbeatTimerID)
	require.Equal(t, run.ActiveDurableTimerID, got.ActiveDurableTimerID)
	require.Equal(t, int64(99), got.DurableTimerFiredAt)
	require.Equal(t, int64(5), got.LastHistoryEventID)
	require.True(t, got.LastHeartbeatTime.IsZero(), "unset heartbeat round-trips as zero")
	require.False(t, got.CreatedAt.IsZero())
	require.Equal(t, got.CreatedAt, got.UpdatedAt)
}

func TestRunStore_CreateDuplicateConflict(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID, namespace, runID := runScope(t)

	require.NoError(t, runStore.CreateRunWithTasks(ctx, newRunRow(shardID, namespace, runID), nil))
	err := runStore.CreateRunWithTasks(ctx, newRunRow(shardID, namespace, runID), nil)
	require.Error(t, err)
	require.True(t, err.IsConflictError(), "got %v", err)
}

func TestRunStore_GetNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID, namespace, runID := runScope(t)

	_, err := runStore.GetRun(ctx, shardID, namespace, runID)
	require.Error(t, err)
	require.True(t, err.IsNotFoundError(), "got %v", err)
}

func TestRunStore_CreateWrongRowType(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID, namespace, runID := runScope(t)

	run := newRunRow(shardID, namespace, runID)
	run.RowType = p.RowTypeImmediateTask
	err := runStore.CreateRunWithTasks(ctx, run, nil)
	require.Error(t, err)
	require.True(t, err.IsInvalidInputError(), "got %v", err)
}

func TestRunStore_CreateWithTasksReadBack(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID, namespace, runID := runScope(t)

	run := newRunRow(shardID, namespace, runID)
	tasks := []p.TaskRow{
		immTask(shardID, namespace, runID, 1),
		timerTask(shardID, namespace, runID, 100, ids.NewUID()),
	}
	require.NoError(t, runStore.CreateRunWithTasks(ctx, run, tasks))

	imm, err := runStore.RangeReadImmediateTasks(ctx, shardID, 0, 10)
	require.NoError(t, err)
	require.Len(t, imm, 1)
	require.False(t, imm[0].ID.IsZero(), "task ID assigned")

	timers, err := runStore.RangeReadTimerTasks(ctx, shardID, 1000, 0, ids.UID{}, 10)
	require.NoError(t, err)
	require.Len(t, timers, 1)
	require.Equal(t, int64(100), timers[0].SortKey)
}

// A failing task insert must roll back the whole create (run must not exist).
func TestRunStore_CreateRollbackOnBadTask(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID, namespace, runID := runScope(t)

	// Two tasks sharing one primary key (shard, sort_key, id) — the second insert conflicts.
	dupID := ids.NewUID()
	tasks := []p.TaskRow{
		timerTask(shardID, namespace, runID, 5, dupID),
		timerTask(shardID, namespace, runID, 5, dupID),
	}
	err := runStore.CreateRunWithTasks(ctx, newRunRow(shardID, namespace, runID), tasks)
	require.Error(t, err)

	_, getErr := runStore.GetRun(ctx, shardID, namespace, runID)
	require.True(t, getErr.IsNotFoundError(), "run must not persist after rollback: %v", getErr)
}

// ---- UpdateRunWithNewTasks + buildRunUpdateSet branches ----

func createBaseRun(t *testing.T, ctx context.Context) (int32, string, string) {
	t.Helper()
	shardID, namespace, runID := runScope(t)
	require.NoError(t, runStore.CreateRunWithTasks(ctx, newRunRow(shardID, namespace, runID), nil))
	return shardID, namespace, runID
}

func TestRunStore_UpdateCAS(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID, namespace, runID := createBaseRun(t, ctx)

	err := runStore.UpdateRunWithNewTasks(ctx, shardID, namespace, runID, 999,
		&p.RunRowUpdate{Status: ptr.Any(p.RunStatusRunning)}, nil)
	require.Error(t, err)
	require.True(t, err.IsCASError(), "got %v", err)

	got, err := runStore.GetRun(ctx, shardID, namespace, runID)
	require.NoError(t, err)
	require.Equal(t, int64(1), got.Version, "failed CAS must not mutate")
	require.Equal(t, p.RunStatusPending, got.Status)
}

func TestRunStore_UpdateScalarPartial(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID, namespace, runID := createBaseRun(t, ctx)

	err := runStore.UpdateRunWithNewTasks(ctx, shardID, namespace, runID, 1, &p.RunRowUpdate{
		Status:               ptr.Any(p.RunStatusRunning),
		WorkerID:             ptr.Any("worker-2"),
		WorkerRequestCounter: ptr.Any(int64(42)),
	}, nil)
	require.NoError(t, err)

	got, err := runStore.GetRun(ctx, shardID, namespace, runID)
	require.NoError(t, err)
	require.Equal(t, int64(2), got.Version)
	require.Equal(t, p.RunStatusRunning, got.Status)
	require.Equal(t, "worker-2", got.WorkerID)
	require.Equal(t, int64(42), got.WorkerRequestCounter)
	// Untouched fields unchanged.
	require.Equal(t, int64(4), got.StepMethodExeCounter)
	require.Equal(t, map[string]p.Value{"a": intValue(1)}, got.Attributes)
}

func TestRunStore_UpdateEmptyBumpsVersion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID, namespace, runID := createBaseRun(t, ctx)

	require.NoError(t, runStore.UpdateRunWithNewTasks(ctx, shardID, namespace, runID, 1, &p.RunRowUpdate{}, nil))

	got, err := runStore.GetRun(ctx, shardID, namespace, runID)
	require.NoError(t, err)
	require.Equal(t, int64(2), got.Version)
	require.Equal(t, p.RunStatusPending, got.Status)
}

func TestRunStore_UpdateAttributesDelta(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID, namespace, runID := createBaseRun(t, ctx)

	err := runStore.UpdateRunWithNewTasks(ctx, shardID, namespace, runID, 1, &p.RunRowUpdate{
		Attributes: map[string]p.Value{"a": intValue(100), "b": intValue(2)},
	}, nil)
	require.NoError(t, err)

	got, err := runStore.GetRun(ctx, shardID, namespace, runID)
	require.NoError(t, err)
	require.Equal(t, intValue(100), got.Attributes["a"], "existing key overwritten")
	require.Equal(t, intValue(2), got.Attributes["b"], "new key upserted")
}

func TestRunStore_UpdateReplaceAttributes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID, namespace, runID := createBaseRun(t, ctx)

	err := runStore.UpdateRunWithNewTasks(ctx, shardID, namespace, runID, 1, &p.RunRowUpdate{
		ReplaceAttributes: ptr.Any(map[string]p.Value{"only": intValue(9)}),
	}, nil)
	require.NoError(t, err)

	got, err := runStore.GetRun(ctx, shardID, namespace, runID)
	require.NoError(t, err)
	require.Equal(t, map[string]p.Value{"only": intValue(9)}, got.Attributes, "old keys dropped")
}

func TestRunStore_UpdateStepExeIDCountersDelta(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID, namespace, runID := createBaseRun(t, ctx)

	err := runStore.UpdateRunWithNewTasks(ctx, shardID, namespace, runID, 1, &p.RunRowUpdate{
		StepExeIDCounters: map[string]int32{"s1": 9, "s2": 3},
	}, nil)
	require.NoError(t, err)

	got, err := runStore.GetRun(ctx, shardID, namespace, runID)
	require.NoError(t, err)
	require.Equal(t, int32(9), got.StepExeIDCounters["s1"])
	require.Equal(t, int32(3), got.StepExeIDCounters["s2"])
}

func TestRunStore_UpdateActiveStepExecutionsUpsertDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID, namespace, runID := createBaseRun(t, ctx)

	newASE := p.ActiveStepExecution{Status: p.StepExeStatusWaitingForCondition, Input: intValue(55)}
	err := runStore.UpdateRunWithNewTasks(ctx, shardID, namespace, runID, 1, &p.RunRowUpdate{
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"s1":      nil,     // delete existing
			"s2":      &newASE, // upsert new
			"missing": nil,     // deleting absent key is a no-op
		},
	}, nil)
	require.NoError(t, err)

	got, err := runStore.GetRun(ctx, shardID, namespace, runID)
	require.NoError(t, err)
	require.NotContains(t, got.ActiveStepExecutions, "s1")
	require.Equal(t, newASE, got.ActiveStepExecutions["s2"])
}

func TestRunStore_UpdateReplaceActiveStepExecutions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID, namespace, runID := createBaseRun(t, ctx)

	repl := map[string]p.ActiveStepExecution{"x": {Status: p.StepExeStatusInvokingExecute, Input: intValue(1)}}
	err := runStore.UpdateRunWithNewTasks(ctx, shardID, namespace, runID, 1, &p.RunRowUpdate{
		ReplaceActiveStepExecutions: ptr.Any(repl),
	}, nil)
	require.NoError(t, err)

	got, err := runStore.GetRun(ctx, shardID, namespace, runID)
	require.NoError(t, err)
	require.Equal(t, repl, got.ActiveStepExecutions)
}

func TestRunStore_UpdateReplaceUnconsumedChannelsPerChannel(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID, namespace, runID := createBaseRun(t, ctx)

	err := runStore.UpdateRunWithNewTasks(ctx, shardID, namespace, runID, 1, &p.RunRowUpdate{
		ReplaceUnconsumedChannels: map[string][]p.ChannelMessage{
			"ch1": {{ID: 2, Value: intValue(20)}}, // replace existing channel
			"ch2": {{ID: 3, Value: intValue(30)}}, // create new channel
		},
	}, nil)
	require.NoError(t, err)

	got, err := runStore.GetRun(ctx, shardID, namespace, runID)
	require.NoError(t, err)
	require.Equal(t, []p.ChannelMessage{{ID: 2, Value: intValue(20)}}, got.UnconsumedChannelMessages["ch1"])
	require.Equal(t, []p.ChannelMessage{{ID: 3, Value: intValue(30)}}, got.UnconsumedChannelMessages["ch2"])
}

func TestRunStore_UpdateReplaceAllUnconsumedChannels(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID, namespace, runID := createBaseRun(t, ctx)

	repl := map[string][]p.ChannelMessage{"fresh": {{ID: 7, Value: intValue(70)}}}
	err := runStore.UpdateRunWithNewTasks(ctx, shardID, namespace, runID, 1, &p.RunRowUpdate{
		ReplaceAllUnconsumedChannels: ptr.Any(repl),
	}, nil)
	require.NoError(t, err)

	got, err := runStore.GetRun(ctx, shardID, namespace, runID)
	require.NoError(t, err)
	require.Equal(t, repl, got.UnconsumedChannelMessages, "old channels dropped")
}

func TestRunStore_UpdateWithNewTasks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID, namespace, runID := createBaseRun(t, ctx)

	err := runStore.UpdateRunWithNewTasks(ctx, shardID, namespace, runID, 1,
		&p.RunRowUpdate{Status: ptr.Any(p.RunStatusRunning)},
		[]p.TaskRow{immTask(shardID, namespace, runID, 7)})
	require.NoError(t, err)

	imm, err := runStore.RangeReadImmediateTasks(ctx, shardID, 0, 10)
	require.NoError(t, err)
	require.Len(t, imm, 1)
	require.Equal(t, int64(7), imm[0].SortKey)
}

// A4: replacing and delta-merging the same column in one update is rejected.
func TestRunStore_UpdateReplaceDeltaConflict(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID, namespace, runID := createBaseRun(t, ctx)

	err := runStore.UpdateRunWithNewTasks(ctx, shardID, namespace, runID, 1, &p.RunRowUpdate{
		ReplaceAttributes: ptr.Any(map[string]p.Value{"only": intValue(1)}),
		Attributes:        map[string]p.Value{"b": intValue(2)},
	}, nil)
	require.Error(t, err)
	require.True(t, err.IsInvalidInputError(), "got %v", err)
}

func TestRunStore_UpdateConcurrentCAS(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID, namespace, runID := createBaseRun(t, ctx)
	const writers = 8

	var wg sync.WaitGroup
	var successes atomic.Int32
	nonCAS := make([]bool, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := runStore.UpdateRunWithNewTasks(ctx, shardID, namespace, runID, 1,
				&p.RunRowUpdate{WorkerRequestCounter: ptr.Any(int64(i))}, nil)
			if err == nil {
				successes.Add(1)
				return
			}
			nonCAS[i] = !err.IsCASError()
		}(i)
	}
	wg.Wait()

	require.Equal(t, int32(1), successes.Load(), "exactly one update wins version 1")
	for i, bad := range nonCAS {
		require.False(t, bad, "loser %d must get CASError", i)
	}
}

// ---- Range read / delete boundaries (A3) ----

func TestRunStore_RangeReadImmediate_AfterSeqOrderLimit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID, namespace, runID := runScope(t)

	run := newRunRow(shardID, namespace, runID)
	tasks := []p.TaskRow{
		immTask(shardID, namespace, runID, 30),
		immTask(shardID, namespace, runID, 10),
		immTask(shardID, namespace, runID, 20),
	}
	require.NoError(t, runStore.CreateRunWithTasks(ctx, run, tasks))

	// afterSeq is exclusive; results ordered by sort_key.
	got, err := runStore.RangeReadImmediateTasks(ctx, shardID, 10, 10)
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, int64(20), got[0].SortKey)
	require.Equal(t, int64(30), got[1].SortKey)

	// limit honored.
	page, err := runStore.RangeReadImmediateTasks(ctx, shardID, 0, 1)
	require.NoError(t, err)
	require.Len(t, page, 1)
	require.Equal(t, int64(10), page[0].SortKey)
}

func TestRunStore_RangeReadTimer_UpToAfterPagination(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID, namespace, runID := runScope(t)

	run := newRunRow(shardID, namespace, runID)
	tasks := []p.TaskRow{
		timerTask(shardID, namespace, runID, 10, ids.NewUID()),
		timerTask(shardID, namespace, runID, 20, ids.NewUID()),
		timerTask(shardID, namespace, runID, 30, ids.NewUID()),
	}
	require.NoError(t, runStore.CreateRunWithTasks(ctx, run, tasks))

	// sortKeyUpTo is inclusive; 30 excluded.
	got, err := runStore.RangeReadTimerTasks(ctx, shardID, 25, 0, ids.UID{}, 10)
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, int64(10), got[0].SortKey)
	require.Equal(t, int64(20), got[1].SortKey)

	// Paginate: first page (afterID zero) then continue past its last key.
	first, err := runStore.RangeReadTimerTasks(ctx, shardID, 100, 0, ids.UID{}, 1)
	require.NoError(t, err)
	require.Len(t, first, 1)
	require.Equal(t, int64(10), first[0].SortKey)

	next, err := runStore.RangeReadTimerTasks(ctx, shardID, 100, first[0].SortKey, first[0].ID, 1)
	require.NoError(t, err)
	require.Len(t, next, 1)
	require.Equal(t, int64(20), next[0].SortKey)
}

func TestRunStore_RangeDeleteImmediate_Inclusive(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID, namespace, runID := runScope(t)

	require.NoError(t, runStore.CreateRunWithTasks(ctx, newRunRow(shardID, namespace, runID), []p.TaskRow{
		immTask(shardID, namespace, runID, 10),
		immTask(shardID, namespace, runID, 20),
		immTask(shardID, namespace, runID, 30),
	}))

	// Inclusive: sort_key <= 20 deleted, 30 kept.
	require.NoError(t, runStore.RangeDeleteImmediateTasks(ctx, shardID, 20))
	got, err := runStore.RangeReadImmediateTasks(ctx, shardID, 0, 10)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, int64(30), got[0].SortKey)
}

func TestRunStore_RangeDeleteTimer_Exclusive(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID, namespace, runID := runScope(t)

	boundaryID := ids.NewUID()
	require.NoError(t, runStore.CreateRunWithTasks(ctx, newRunRow(shardID, namespace, runID), []p.TaskRow{
		timerTask(shardID, namespace, runID, 10, ids.NewUID()),
		timerTask(shardID, namespace, runID, 20, boundaryID),
		timerTask(shardID, namespace, runID, 30, ids.NewUID()),
	}))

	// Exclusive: (sort_key,id) < (20, boundaryID) deletes only sort_key 10.
	require.NoError(t, runStore.RangeDeleteTimerTasks(ctx, shardID, 20, boundaryID))
	got, err := runStore.RangeReadTimerTasks(ctx, shardID, 1000, 0, ids.UID{}, 10)
	require.NoError(t, err)
	require.Len(t, got, 2, "boundary (20) kept, 30 kept")
	require.Equal(t, int64(20), got[0].SortKey)
	require.Equal(t, int64(30), got[1].SortKey)
}

// ---- ByID batch delete / isolation ----

func TestRunStore_DeleteImmediateTasksByIDBatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID, namespace, runID := runScope(t)

	require.NoError(t, runStore.DeleteImmediateTasksByIDBatch(ctx, shardID, nil))

	keep := immTask(shardID, namespace, runID, 10)
	drop := immTask(shardID, namespace, runID, 20)
	require.NoError(t, runStore.CreateRunWithTasks(ctx, newRunRow(shardID, namespace, runID), []p.TaskRow{keep, drop}))

	require.NoError(t, runStore.DeleteImmediateTasksByIDBatch(ctx, shardID, []ids.UID{drop.Immediate.ID}))
	got, err := runStore.RangeReadImmediateTasks(ctx, shardID, 0, 10)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, keep.Immediate.ID, got[0].ID)
}

func TestRunStore_DeleteTimerTasksByIDBatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardID, namespace, runID := runScope(t)

	require.NoError(t, runStore.DeleteTimerTasksByIDBatch(ctx, shardID, nil))

	keepID, dropID := ids.NewUID(), ids.NewUID()
	require.NoError(t, runStore.CreateRunWithTasks(ctx, newRunRow(shardID, namespace, runID), []p.TaskRow{
		timerTask(shardID, namespace, runID, 10, keepID),
		timerTask(shardID, namespace, runID, 20, dropID),
	}))

	require.NoError(t, runStore.DeleteTimerTasksByIDBatch(ctx, shardID, []ids.UID{dropID}))
	got, err := runStore.RangeReadTimerTasks(ctx, shardID, 1000, 0, ids.UID{}, 10)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, keepID, got[0].ID)
}

// Range read/delete and by-ID delete must never cross shard boundaries.
func TestRunStore_TaskShardIsolation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	shardA := nextShardID(t)
	shardB := nextShardID(t)
	namespace := "runns-" + ids.NewUID().String()
	runA := "run-a-" + ids.NewUID().String()
	runB := "run-b-" + ids.NewUID().String()

	require.NoError(t, runStore.CreateRunWithTasks(ctx, newRunRow(shardA, namespace, runA),
		[]p.TaskRow{immTask(shardA, namespace, runA, 10)}))
	require.NoError(t, runStore.CreateRunWithTasks(ctx, newRunRow(shardB, namespace, runB),
		[]p.TaskRow{immTask(shardB, namespace, runB, 10)}))

	// Read scoped to shard.
	gotA, err := runStore.RangeReadImmediateTasks(ctx, shardA, 0, 10)
	require.NoError(t, err)
	require.Len(t, gotA, 1)

	// Delete on shardA does not touch shardB.
	require.NoError(t, runStore.RangeDeleteImmediateTasks(ctx, shardA, 1000))
	gotB, err := runStore.RangeReadImmediateTasks(ctx, shardB, 0, 10)
	require.NoError(t, err)
	require.Len(t, gotB, 1, "shardB unaffected")
}
