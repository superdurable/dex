// Copyright (c) 2025 superdurable
// SPDX-License-Identifier: MIT

package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/superdurable/dex/server/common/utils/ids"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	p "github.com/superdurable/dex/server/internal/persistence"
)

func intVal(v int64) p.Value { return p.Value{Type: p.ValueTypeInt, IntVal: &v} }

// ---------------- RunStore ----------------

func TestPG_RunStore_CreateGetUpdateCAS(t *testing.T) {
	uri := testURI()
	ctx := context.Background()
	store, err := NewRunStore(ctx, testPoolConfig(uri))
	require.Nil(t, err)
	defer store.Close()
	require.NoError(t, store.DeleteAll(ctx))

	ns := "ns-" + uuid.NewString()
	runID := uuid.NewString()
	run := &p.RunRow{
		ShardID: 1, Namespace: ns, ID: runID, FlowType: "ft", TaskListName: "g",
		Status:   p.RunStatusPending,
		StateMap: map[string]p.Value{"k": intVal(7)},
	}
	require.Nil(t, store.CreateRunWithTasks(ctx, run, []p.TaskRow{
		{Immediate: &p.ImmediateTaskRow{ShardID: 1, SortKey: 100, TaskType: p.ImmediateTaskRunInitialDispatch, TaskInfo: p.ImmediateTaskInfo{RunID: runID, Namespace: ns}}},
	}))

	got, gErr := store.GetRun(ctx, 1, ns, runID, p.GetRunOptions{})
	require.Nil(t, gErr)
	assert.Equal(t, int64(1), got.Version)
	assert.Equal(t, p.RunStatusPending, got.Status)
	require.Contains(t, got.StateMap, "k")
	assert.Equal(t, int64(7), *got.StateMap["k"].IntVal)

	// Duplicate create -> conflict.
	dupErr := store.CreateRunWithTasks(ctx, &p.RunRow{ShardID: 1, Namespace: ns, ID: runID}, nil)
	require.NotNil(t, dupErr)
	assert.True(t, dupErr.IsConflictError())

	// CAS update at version 1 -> Running, merge state delta.
	running := p.RunStatusRunning
	require.Nil(t, store.UpdateRunWithNewTasks(ctx, 1, ns, runID, 1, &p.RunRowUpdate{
		Status:   &running,
		StateMap: map[string]p.Value{"k2": intVal(9)},
	}, nil))

	got2, _ := store.GetRun(ctx, 1, ns, runID, p.GetRunOptions{})
	assert.Equal(t, int64(2), got2.Version)
	assert.Equal(t, p.RunStatusRunning, got2.Status)
	assert.Equal(t, int64(7), *got2.StateMap["k"].IntVal, "existing key preserved")
	assert.Equal(t, int64(9), *got2.StateMap["k2"].IntVal, "delta key added")

	// Stale CAS at version 1 -> version mismatch.
	stale := store.UpdateRunWithNewTasks(ctx, 1, ns, runID, 1, &p.RunRowUpdate{Status: &running}, nil)
	require.NotNil(t, stale)
	assert.True(t, stale.IsCASError())

	// Missing run -> version mismatch (mirror Mongo).
	missing := store.UpdateRunWithNewTasks(ctx, 1, ns, "nope", 1, &p.RunRowUpdate{Status: &running}, nil)
	require.NotNil(t, missing)
	assert.True(t, missing.IsCASError())
}

func TestPG_RunStore_ChannelMessageMerge(t *testing.T) {
	uri := testURI()
	ctx := context.Background()
	store, err := NewRunStore(ctx, testPoolConfig(uri))
	require.Nil(t, err)
	defer store.Close()
	require.NoError(t, store.DeleteAll(ctx))

	ns := "ns-" + uuid.NewString()
	runID := uuid.NewString()
	require.Nil(t, store.CreateRunWithTasks(ctx, &p.RunRow{ShardID: 2, Namespace: ns, ID: runID}, nil))

	// Set two messages on channel "c".
	require.Nil(t, store.UpdateRunWithNewTasks(ctx, 2, ns, runID, 1, &p.RunRowUpdate{
		ReplaceUnconsumedChannels: map[string][]p.ChannelMessage{"c": {{ID: 1, Value: intVal(11)}, {ID: 2, Value: intVal(22)}}},
	}, nil))
	got, _ := store.GetRun(ctx, 2, ns, runID, p.GetRunOptions{})
	require.Len(t, got.UnconsumedChannelMessages["c"], 2)

	// Replace with consumed tail plus one new message.
	require.Nil(t, store.UpdateRunWithNewTasks(ctx, 2, ns, runID, 2, &p.RunRowUpdate{
		ReplaceUnconsumedChannels: map[string][]p.ChannelMessage{"c": {{ID: 2, Value: intVal(22)}, {ID: 3, Value: intVal(33)}}},
	}, nil))
	got2, _ := store.GetRun(ctx, 2, ns, runID, p.GetRunOptions{})
	msgs := got2.UnconsumedChannelMessages["c"]
	require.Len(t, msgs, 2)
	assert.Equal(t, int64(2), msgs[0].ID)
	assert.Equal(t, int64(3), msgs[1].ID)
}

func TestPG_RunStore_TaskRangeReadDelete(t *testing.T) {
	uri := testURI()
	ctx := context.Background()
	store, err := NewRunStore(ctx, testPoolConfig(uri))
	require.Nil(t, err)
	defer store.Close()
	require.NoError(t, store.DeleteAll(ctx))

	ns := "ns-" + uuid.NewString()
	runID := uuid.NewString()
	tasks := []p.TaskRow{
		{Immediate: &p.ImmediateTaskRow{ShardID: 5, SortKey: 10, TaskType: p.ImmediateTaskRunInitialDispatch, TaskInfo: p.ImmediateTaskInfo{RunID: runID}}},
		{Immediate: &p.ImmediateTaskRow{ShardID: 5, SortKey: 20, TaskType: p.ImmediateTaskRunResumeDispatch, TaskInfo: p.ImmediateTaskInfo{RunID: runID}}},
		{Timer: &p.TimerTaskRow{ShardID: 5, SortKey: 1000, TaskType: p.TimerTaskRunHeartbeat, TaskInfo: p.TimerTaskInfo{RunID: runID}}},
	}
	require.Nil(t, store.CreateRunWithTasks(ctx, &p.RunRow{ShardID: 5, Namespace: ns, ID: runID}, tasks))

	imm, e1 := store.RangeReadImmediateTasks(ctx, 5, 0, 10)
	require.Nil(t, e1)
	require.Len(t, imm, 2)
	assert.Equal(t, int64(10), imm[0].SortKey)
	assert.Equal(t, int64(20), imm[1].SortKey)

	// afterSeq cursor skips the first.
	imm2, _ := store.RangeReadImmediateTasks(ctx, 5, 10, 10)
	require.Len(t, imm2, 1)
	assert.Equal(t, int64(20), imm2[0].SortKey)

	require.Nil(t, store.RangeDeleteImmediateTasks(ctx, 5, 10))
	imm3, _ := store.RangeReadImmediateTasks(ctx, 5, 0, 10)
	require.Len(t, imm3, 1)
	assert.Equal(t, int64(20), imm3[0].SortKey)

	tmr, e2 := store.RangeReadTimerTasks(ctx, 5, 2000, 0, ids.TaskID{}, 10)
	require.Nil(t, e2)
	require.Len(t, tmr, 1)
	assert.Equal(t, int64(1000), tmr[0].SortKey)
}

// ---------------- ShardStore ----------------

func TestPG_ShardStore_ClaimRenewRelease(t *testing.T) {
	uri := testURI()
	ctx := context.Background()
	store, err := NewShardStore(ctx, testPoolConfig(uri))
	require.Nil(t, err)
	defer store.Close()

	shardID := int32(1000 + time.Now().Nanosecond()%1000)
	sh, cErr := store.ClaimShard(ctx, shardID, "m1", time.Minute)
	require.Nil(t, cErr)
	assert.Equal(t, int64(1), sh.Version)
	assert.Equal(t, int32(1), sh.Metadata.RangeID)

	// Re-claim by same member increments range_id + version.
	sh2, _ := store.ClaimShard(ctx, shardID, "m1", time.Minute)
	assert.Equal(t, int64(2), sh2.Version)
	assert.Equal(t, int32(2), sh2.Metadata.RangeID)

	// Renew persists committed offsets, preserves range_id.
	_, rErr := store.RenewShardLease(ctx, shardID, "m1", 2, time.Minute, &p.ShardMetadata{ImmediateTaskCommittedSeq: 42})
	require.Nil(t, rErr)
	// Stale version renew -> mismatch.
	_, rErr2 := store.RenewShardLease(ctx, shardID, "m1", 1, time.Minute, nil)
	require.NotNil(t, rErr2)
	assert.True(t, rErr2.IsCASError())

	// Different member cannot claim while lease valid.
	_, lErr := store.ClaimShard(ctx, shardID, "m2", time.Minute)
	require.NotNil(t, lErr)
	assert.True(t, lErr.IsConflictError())

	require.Nil(t, store.ReleaseShard(ctx, shardID, "m1", 2))
}

// ---------------- BlobStore ----------------

func TestPG_BlobStore_RoundTrip(t *testing.T) {
	uri := testURI()
	ctx := context.Background()
	store, err := NewBlobStore(ctx, testPoolConfig(uri))
	require.Nil(t, err)
	defer store.Close()

	ns, runID := "ns-"+uuid.NewString(), uuid.NewString()
	id := ids.NewBlobID()
	require.Nil(t, store.BatchInsertBlobs(ctx, 1, ns, runID, []p.BlobEntry{{BlobID: id, Encoding: "json", Payload: []byte(`{"a":1}`)}}))
	// Idempotent re-insert.
	require.Nil(t, store.BatchInsertBlobs(ctx, 1, ns, runID, []p.BlobEntry{{BlobID: id, Encoding: "json", Payload: []byte(`{"a":1}`)}}))

	got, gErr := store.BatchGetBlobs(ctx, 1, ns, runID, []ids.BlobID{id})
	require.Nil(t, gErr)
	require.Len(t, got, 1)
	assert.Equal(t, "json", got[0].Encoding)
	assert.Equal(t, []byte(`{"a":1}`), got[0].Payload)
}

// ---------------- TasklistStore ----------------

func TestPG_TasklistStore_ClaimFenceGetDelete(t *testing.T) {
	uri := testURI()
	ctx := context.Background()
	store, err := NewTasklistStore(ctx, testPoolConfig(uri))
	require.Nil(t, err)
	defer store.Close()

	ns, tl := "ns-"+uuid.NewString(), "tl"
	md, cErr := store.ClaimTasklist(ctx, ns, tl, 0, "m1", "addr1")
	require.Nil(t, cErr)
	assert.Equal(t, int32(1), md.RangeID)

	// Re-claim increments range_id (fences old owner).
	md2, _ := store.ClaimTasklist(ctx, ns, tl, 0, "m2", "addr2")
	assert.Equal(t, int32(2), md2.RangeID)

	// CreateTasks with stale range_id -> fence error.
	stale := store.CreateTasks(ctx, ns, tl, 0, 1, []*p.TasklistTaskRow{{TaskID: 1, RunID: "r"}})
	require.NotNil(t, stale)
	assert.True(t, stale.IsConflictError())

	// CreateTasks with current range_id -> ok.
	require.Nil(t, store.CreateTasks(ctx, ns, tl, 0, 2, []*p.TasklistTaskRow{
		{TaskID: 100, RunID: "r1"}, {TaskID: 101, RunID: "r2"}, {TaskID: 102, RunID: "r3"},
	}))

	got, _ := store.GetTasks(ctx, ns, tl, 0, 99, 200, 10)
	require.Len(t, got, 3)
	assert.Equal(t, int64(100), got[0].TaskID)

	n, _ := store.DeleteTasksLessThan(ctx, ns, tl, 0, 101, 10)
	assert.Equal(t, 2, n)
	got2, _ := store.GetTasks(ctx, ns, tl, 0, 99, 200, 10)
	require.Len(t, got2, 1)
	assert.Equal(t, int64(102), got2[0].TaskID)
}

// ---------------- VisibilityStore ----------------

func TestPG_VisibilityStore_UpsertListPaginate(t *testing.T) {
	uri := testURI()
	ctx := context.Background()
	store, err := NewVisibilityStore(ctx, testPoolConfig(uri))
	require.Nil(t, err)
	defer store.Close()

	ns := "ns-" + uuid.NewString()
	base := time.Now().Truncate(time.Millisecond)
	var entries []p.VisibilityEntry
	for i := 0; i < 3; i++ {
		entries = append(entries, p.VisibilityEntry{
			Namespace: ns, RunID: uuid.NewString(), FlowType: "ft", Status: p.RunStatusRunning,
			StartTime: base.Add(time.Duration(i) * time.Second), UpdatedAt: base.Add(time.Duration(i) * time.Second),
		})
	}
	require.Nil(t, store.BatchUpsertVisibility(ctx, entries))

	// Page 1 (limit 2), start_time DESC.
	page1, lErr := store.ListRuns(ctx, p.ListRunsQuery{Namespace: ns, FlowType: "ft", Limit: 2, OrderBy: p.ListByStartTimeDesc})
	require.Nil(t, lErr)
	require.Len(t, page1.Entries, 2)
	assert.NotEmpty(t, page1.NextPageToken)
	assert.True(t, page1.Entries[0].StartTime.After(page1.Entries[1].StartTime) || page1.Entries[0].StartTime.Equal(page1.Entries[1].StartTime))

	page2, _ := store.ListRuns(ctx, p.ListRunsQuery{Namespace: ns, FlowType: "ft", Limit: 2, OrderBy: p.ListByStartTimeDesc, PageToken: page1.NextPageToken})
	require.Len(t, page2.Entries, 1)

	// Upsert keeps start_time (setOnInsert) but advances updated_at.
	first := entries[0]
	completed := p.VisibilityEntry{Namespace: ns, RunID: first.RunID, FlowType: "ft", Status: p.RunStatusCompleted, StartTime: base.Add(time.Hour), UpdatedAt: base.Add(2 * time.Hour)}
	require.Nil(t, store.BatchUpsertVisibility(ctx, []p.VisibilityEntry{completed}))
	done, _ := store.ListRuns(ctx, p.ListRunsQuery{Namespace: ns, FlowType: "ft", Status: ptrStatus(p.RunStatusCompleted), Limit: 10, OrderBy: p.ListByStartTimeDesc})
	require.Len(t, done.Entries, 1)
	assert.Equal(t, first.StartTime.UnixMilli(), done.Entries[0].StartTime.UnixMilli(), "start_time pinned on insert")
}

func ptrStatus(s p.RunStatus) *p.RunStatus { return &s }

// ---------------- HistoryStore ----------------

func TestPG_HistoryStore_InsertGetDedup(t *testing.T) {
	uri := testURI()
	ctx := context.Background()
	store, err := NewHistoryStore(ctx, testPoolConfig(uri))
	require.Nil(t, err)
	defer store.Close()

	ns, runID := "ns-"+uuid.NewString(), uuid.NewString()
	ev := func(id int64) p.HistoryEvent {
		return p.HistoryEvent{Namespace: ns, RunID: runID, EventID: id, OccurredAtMs: id, Payload: p.HistoryEventPayload{RunStart: &pb.HistoryRunStartPayload{}}}
	}
	require.Nil(t, store.BatchInsertHistory(ctx, []p.HistoryEvent{ev(1), ev(2)}))
	// Replay (dedup) — must be a no-op, not an error.
	require.Nil(t, store.BatchInsertHistory(ctx, []p.HistoryEvent{ev(1), ev(2), ev(3)}))

	got, gErr := store.GetHistoryEvents(ctx, ns, runID, 0, 10)
	require.Nil(t, gErr)
	require.Len(t, got, 3)
	assert.Equal(t, int64(1), got[0].EventID)
	assert.Equal(t, int64(3), got[2].EventID)
	assert.NotNil(t, got[0].Payload.RunStart)

	after, _ := store.GetHistoryEvents(ctx, ns, runID, 1, 10)
	require.Len(t, after, 2)
	assert.Equal(t, int64(2), after[0].EventID)
}

func TestPG_RunStore_ReplaceMapsClearAbsentKeys(t *testing.T) {
	uri := testURI()
	ctx := context.Background()
	store, err := NewRunStore(ctx, testPoolConfig(uri))
	require.Nil(t, err)
	defer store.Close()
	require.NoError(t, store.DeleteAll(ctx))

	ns := "ns-" + uuid.NewString()
	runID := uuid.NewString()
	v1, v2 := int64(1), int64(2)
	require.Nil(t, store.CreateRunWithTasks(ctx, &p.RunRow{
		ShardID: 3, Namespace: ns, ID: runID,
		StateMap: map[string]p.Value{
			"keep": {Type: p.ValueTypeInt, IntVal: &v1},
			"drop": {Type: p.ValueTypeInt, IntVal: &v2},
		},
		ActiveStepExecutions: map[string]p.ActiveStepExecution{
			"step-1": {Status: p.StepExeStatusInvokingExecute},
			"step-2": {Status: p.StepExeStatusWaitingForCondition},
		},
		StepExeIDCounters: map[string]int32{"a": 1, "b": 2},
	}, nil))

	replacedState := map[string]p.Value{"keep": intVal(1)}
	replacedSteps := map[string]p.ActiveStepExecution{
		"step-1": {Status: p.StepExeStatusInvokingExecute},
	}
	replacedCounters := map[string]int32{"a": 1}
	require.Nil(t, store.UpdateRunWithNewTasks(ctx, 3, ns, runID, 1, &p.RunRowUpdate{
		ReplaceStateMap:             &replacedState,
		ReplaceActiveStepExecutions: &replacedSteps,
		ReplaceStepExeIDCounters:    &replacedCounters,
		ReplaceAllUnconsumedChannels: &map[string][]p.ChannelMessage{},
	}, nil))

	got, _ := store.GetRun(ctx, 3, ns, runID, p.GetRunOptions{})
	_, hasDrop := got.StateMap["drop"]
	assert.False(t, hasDrop)
	_, hasStep2 := got.ActiveStepExecutions["step-2"]
	assert.False(t, hasStep2)
	_, hasB := got.StepExeIDCounters["b"]
	assert.False(t, hasB)
}

// ---------------- DLQStore ----------------

func TestPG_DLQStore_WriteIdempotent(t *testing.T) {
	uri := testURI()
	ctx := context.Background()
	store, err := NewDLQStore(ctx, testPoolConfig(uri))
	require.Nil(t, err)
	defer store.Close()

	entry := &p.DLQEntry{
		ShardID: 9, TaskID: ids.NewTaskID(), QueueType: p.RowTypeImmediateTask, TaskType: 0,
		RunID: uuid.NewString(), Namespace: "ns", Error: "boom", ErrorCategory: "internal", CreatedAt: time.Now(), MemberID: "m1",
	}
	require.Nil(t, store.WriteDLQ(ctx, entry))
	// Duplicate (shard_id, task_id) -> silent no-op.
	require.Nil(t, store.WriteDLQ(ctx, entry))
}
