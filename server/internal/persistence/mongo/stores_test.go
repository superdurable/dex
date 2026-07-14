package mongo

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/superdurable/dex/server/common/utils/ids"
	p "github.com/superdurable/dex/server/internal/persistence"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func getMongoURI(t *testing.T) string {
	uri := os.Getenv("DEX_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("DEX_TEST_MONGO_URI not set; skipping integration test")
	}
	return uri
}

// getRunStoreWithCleanup creates a RunStore and cleans the DB before the test.
// Cleanup runs before (not after) because t.Cleanup may not execute on
// panic/timeout, leaving stale data that poisons the next run.
func getRunStoreWithCleanup(t *testing.T) p.RunStore {
	store, err := NewRunStoreWithDatabase(context.Background(), getMongoURI(t), testDBName, DefaultOperationTimeouts())
	require.Nil(t, err)
	store.DeleteAll(context.Background())
	t.Cleanup(func() { store.Close() })
	return store
}

// ============================================================================
// ShardStore Tests
// ============================================================================

func TestShardStore_ClaimAndRelease(t *testing.T) {
	ctx := context.Background()
	store, err := NewShardStoreWithDatabase(ctx, getMongoURI(t), testDBName, DefaultOperationTimeouts())
	require.Nil(t, err)
	defer store.Close()

	shard, cErr := store.ClaimShard(ctx, 0, "member-1", 30*time.Second)
	require.Nil(t, cErr)
	assert.Equal(t, int32(0), shard.ShardID)
	assert.Equal(t, "member-1", shard.MemberID)
	assert.True(t, shard.Version >= 1)
	assert.False(t, shard.LeaseExpiresAt.IsZero())

	leaseExp, rErr := store.RenewShardLease(ctx, 0, "member-1", shard.Version, 30*time.Second, nil)
	require.Nil(t, rErr)
	assert.False(t, leaseExp.IsZero())

	relErr := store.ReleaseShard(ctx, 0, "member-1", shard.Version)
	require.Nil(t, relErr)

	shard2, cErr2 := store.ClaimShard(ctx, 0, "member-2", 30*time.Second)
	require.Nil(t, cErr2)
	assert.Equal(t, "member-2", shard2.MemberID)
	assert.True(t, shard2.Version > shard.Version)
}

// ============================================================================
// RunStore Tests
// ============================================================================

func TestRunStore_CreateAndGet(t *testing.T) {
	ctx := context.Background()
	store := getRunStoreWithCleanup(t)

	runID := uuid.NewString()
	taskID := ids.NewTaskID()

	run := &p.RunRow{
		ShardID: 0, Namespace: "test-ns", ID: runID,
		FlowType: "subscription", TaskListName: "default", Status: p.RunStatusPending,
		StateMap:                  map[string]p.Value{},
		UnconsumedChannelMessages: map[string][]p.ChannelMessage{},
		StepExeIDCounters:         map[string]int32{},
		ActiveStepExecutions:      map[string]p.ActiveStepExecution{},
	}
	tasks := []p.TaskRow{{
		Immediate: &p.ImmediateTaskRow{
			ShardID: 0, ID: taskID, TaskType: p.ImmediateTaskRunInitialDispatch,
			TaskInfo: p.ImmediateTaskInfo{RunID: runID, Namespace: "test-ns", TaskListName: "default"},
		},
	}}

	cErr := store.CreateRunWithTasks(ctx, run, tasks)
	require.Nil(t, cErr)

	got, gErr := store.GetRun(ctx, 0, "test-ns", runID, p.GetRunOptions{})
	require.Nil(t, gErr)
	assert.Equal(t, runID, got.ID)
	assert.Equal(t, p.RunStatusPending, got.Status)
	assert.Equal(t, int64(1), got.Version)
}

func TestRunStore_GetRun_SecondaryPreferredOption(t *testing.T) {
	// Sanity check that ReadPrefSecondaryPreferred is wired through the
	// store: the per-call collection handle is built with the alternate
	// read preference and the round-trip succeeds. We can't observe the
	// actual replica that served the read in a single-node test setup,
	// but we can prove the option does not break the call.
	ctx := context.Background()
	store := getRunStoreWithCleanup(t)

	runID := uuid.NewString()
	run := &p.RunRow{
		ShardID: 0, Namespace: "test-ns", ID: runID,
		FlowType: "test", TaskListName: "g", Status: p.RunStatusPending,
		StateMap: map[string]p.Value{}, UnconsumedChannelMessages: map[string][]p.ChannelMessage{},
		StepExeIDCounters: map[string]int32{}, ActiveStepExecutions: map[string]p.ActiveStepExecution{},
	}
	tasks := []p.TaskRow{{Immediate: &p.ImmediateTaskRow{
		ShardID: 0, ID: ids.NewTaskID(), TaskType: p.ImmediateTaskRunInitialDispatch,
		TaskInfo: p.ImmediateTaskInfo{RunID: runID, Namespace: "test-ns", TaskListName: "g"},
	}}}
	require.Nil(t, store.CreateRunWithTasks(ctx, run, tasks))

	got, gErr := store.GetRun(ctx, 0, "test-ns", runID, p.GetRunOptions{
		ReadPreference: p.ReadPrefSecondaryPreferred,
	})
	require.Nil(t, gErr)
	assert.Equal(t, runID, got.ID)
	assert.Equal(t, p.RunStatusPending, got.Status)
}

func TestRunStore_CreateDuplicate(t *testing.T) {
	ctx := context.Background()
	store := getRunStoreWithCleanup(t)

	runID := uuid.NewString()
	run := &p.RunRow{
		ShardID: 0, Namespace: "test-ns", ID: runID,
		FlowType: "test", TaskListName: "g", Status: p.RunStatusPending,
		StateMap: map[string]p.Value{}, UnconsumedChannelMessages: map[string][]p.ChannelMessage{},
		StepExeIDCounters: map[string]int32{}, ActiveStepExecutions: map[string]p.ActiveStepExecution{},
	}
	tasks := []p.TaskRow{{Immediate: &p.ImmediateTaskRow{
		ShardID: 0, ID: ids.NewTaskID(), TaskType: p.ImmediateTaskRunInitialDispatch,
		TaskInfo: p.ImmediateTaskInfo{RunID: runID, Namespace: "test-ns", TaskListName: "g"},
	}}}

	require.Nil(t, store.CreateRunWithTasks(ctx, run, tasks))

	run2 := *run
	run2.ID = runID
	tasks2 := []p.TaskRow{{Immediate: &p.ImmediateTaskRow{
		ShardID: 0, ID: ids.NewTaskID(), TaskType: p.ImmediateTaskRunInitialDispatch,
		TaskInfo: p.ImmediateTaskInfo{RunID: runID, Namespace: "test-ns", TaskListName: "g"},
	}}}
	cErr := store.CreateRunWithTasks(ctx, &run2, tasks2)
	require.NotNil(t, cErr)
	assert.True(t, cErr.GetCategory() == "conflict")
}

func TestRunStore_UpdateWithCAS(t *testing.T) {
	ctx := context.Background()
	store := getRunStoreWithCleanup(t)

	runID := uuid.NewString()
	run := &p.RunRow{
		ShardID: 0, Namespace: "test-ns", ID: runID,
		FlowType: "test", TaskListName: "g", Status: p.RunStatusPending,
		StateMap: map[string]p.Value{}, UnconsumedChannelMessages: map[string][]p.ChannelMessage{},
		StepExeIDCounters: map[string]int32{}, ActiveStepExecutions: map[string]p.ActiveStepExecution{},
	}
	require.Nil(t, store.CreateRunWithTasks(ctx, run, []p.TaskRow{{Immediate: &p.ImmediateTaskRow{
		ShardID: 0, ID: ids.NewTaskID(), TaskType: p.ImmediateTaskRunInitialDispatch,
		TaskInfo: p.ImmediateTaskInfo{RunID: runID, Namespace: "test-ns", TaskListName: "g"},
	}}}))

	running := p.RunStatusRunning
	uErr := store.UpdateRunWithNewTasks(ctx, 0, "test-ns", runID, 1,
		&p.RunRowUpdate{Status: &running}, nil)
	require.Nil(t, uErr)

	// Stale version should return CAS error
	uErr2 := store.UpdateRunWithNewTasks(ctx, 0, "test-ns", runID, 1,
		&p.RunRowUpdate{Status: &running}, nil)
	require.NotNil(t, uErr2)
	assert.True(t, uErr2.IsCASError())

	got, _ := store.GetRun(ctx, 0, "test-ns", runID, p.GetRunOptions{})
	assert.Equal(t, p.RunStatusRunning, got.Status)
	assert.Equal(t, int64(2), got.Version)
}

func TestRunStore_UpdateWithNewTasks(t *testing.T) {
	ctx := context.Background()
	store := getRunStoreWithCleanup(t)

	runID := uuid.NewString()
	run := &p.RunRow{
		ShardID: 0, Namespace: "test-ns", ID: runID,
		FlowType: "test", TaskListName: "g", Status: p.RunStatusPending,
		StateMap: map[string]p.Value{}, UnconsumedChannelMessages: map[string][]p.ChannelMessage{},
		StepExeIDCounters: map[string]int32{}, ActiveStepExecutions: map[string]p.ActiveStepExecution{},
	}
	require.Nil(t, store.CreateRunWithTasks(ctx, run, []p.TaskRow{{Immediate: &p.ImmediateTaskRow{
		ShardID: 0, ID: ids.NewTaskID(), TaskType: p.ImmediateTaskRunInitialDispatch,
		TaskInfo: p.ImmediateTaskInfo{RunID: runID, Namespace: "test-ns", TaskListName: "g"},
	}}}))

	running := p.RunStatusRunning
	timerID := ids.NewTaskID()
	uErr := store.UpdateRunWithNewTasks(ctx, 0, "test-ns", runID, 1,
		&p.RunRowUpdate{Status: &running},
		[]p.TaskRow{{Timer: &p.TimerTaskRow{
			ShardID: 0, ID: timerID, SortKey: time.Now().Add(30 * time.Second).UnixMilli(),
			TaskType: p.TimerTaskRunHeartbeat, TaskInfo: p.TimerTaskInfo{RunID: runID, Namespace: "test-ns"},
		}}},
	)
	require.Nil(t, uErr)

	// Verify timer task was created by checking run status changed
	got, gErr := store.GetRun(ctx, 0, "test-ns", runID, p.GetRunOptions{})
	require.Nil(t, gErr)
	assert.Equal(t, p.RunStatusRunning, got.Status)
	assert.Equal(t, int64(2), got.Version)
}

func TestRunStore_ImmediateTaskPolling(t *testing.T) {
	ctx := context.Background()
	store := getRunStoreWithCleanup(t)

	for i := 0; i < 3; i++ {
		taskID := ids.NewTaskID()
		runID := uuid.NewString()
		run := &p.RunRow{
			ShardID: 1, Namespace: "test-ns", ID: runID,
			FlowType: "test", TaskListName: "g", Status: p.RunStatusPending,
			StateMap: map[string]p.Value{}, UnconsumedChannelMessages: map[string][]p.ChannelMessage{},
			StepExeIDCounters: map[string]int32{}, ActiveStepExecutions: map[string]p.ActiveStepExecution{},
		}
		seq := int64((i + 1) * 100) // SortKey = TaskSeq: 100, 200, 300
		require.Nil(t, store.CreateRunWithTasks(ctx, run, []p.TaskRow{{Immediate: &p.ImmediateTaskRow{
			ShardID: 1, ID: taskID, SortKey: seq, TaskType: p.ImmediateTaskRunInitialDispatch,
			TaskInfo: p.ImmediateTaskInfo{RunID: runID, Namespace: "test-ns", TaskListName: "g"},
		}}}))
	}

	tasks, tErr := store.RangeReadImmediateTasks(ctx, 1, 0, 100)
	require.Nil(t, tErr)
	assert.GreaterOrEqual(t, len(tasks), 3)

	// Cursor pagination by SortKey
	if len(tasks) > 0 {
		tasks2, _ := store.RangeReadImmediateTasks(ctx, 1, tasks[0].SortKey, 100)
		for _, t2 := range tasks2 {
			assert.Greater(t, t2.SortKey, tasks[0].SortKey)
		}
	}

	// Range delete up to last task's SortKey
	if len(tasks) > 0 {
		dErr := store.RangeDeleteImmediateTasks(ctx, 1, tasks[len(tasks)-1].SortKey)
		require.Nil(t, dErr)
	}
}

func TestRunStore_DottedMapKeys_RoundTrip(t *testing.T) {
	ctx := context.Background()
	store := getRunStoreWithCleanup(t)

	runID := uuid.NewString()
	run := &p.RunRow{
		ShardID: 0, Namespace: "test-ns", ID: runID,
		FlowType: "test", TaskListName: "g", Status: p.RunStatusPending,
		StateMap:                  map[string]p.Value{},
		UnconsumedChannelMessages: map[string][]p.ChannelMessage{},
		StepExeIDCounters:         map[string]int32{},
		ActiveStepExecutions:      map[string]p.ActiveStepExecution{},
	}
	require.Nil(t, store.CreateRunWithTasks(ctx, run, []p.TaskRow{{Immediate: &p.ImmediateTaskRow{
		ShardID: 0, ID: ids.NewTaskID(), TaskType: p.ImmediateTaskRunInitialDispatch,
		TaskInfo: p.ImmediateTaskInfo{RunID: runID, Namespace: "test-ns", TaskListName: "g"},
	}}}))

	// Update with dotted keys in all map fields
	intVal := int64(42)
	uErr := store.UpdateRunWithNewTasks(ctx, 0, "test-ns", runID, 1, &p.RunRowUpdate{
		StateMap: map[string]p.Value{
			"mypkg.MyField": {Type: p.ValueTypeInt, IntVal: &intVal},
		},
		StepExeIDCounters: map[string]int32{
			"mypkg.MyStep": 3,
		},
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"mypkg.MyStep-1": {
				Input:  p.Value{Type: p.ValueTypeNull},
				Status: p.StepExeStatusInvokingExecute,
			},
		},
	}, nil)
	require.Nil(t, uErr)

	// Read back — keys must come back with original dots, not escaped
	got, gErr := store.GetRun(ctx, 0, "test-ns", runID, p.GetRunOptions{})
	require.Nil(t, gErr)

	assert.Equal(t, int32(3), got.StepExeIDCounters["mypkg.MyStep"],
		"dotted key in StepExeIDCounters should survive round-trip")

	stateVal, stateOK := got.StateMap["mypkg.MyField"]
	require.True(t, stateOK, "dotted key in StateMap should survive round-trip")
	assert.Equal(t, p.ValueTypeInt, stateVal.Type)
	assert.Equal(t, int64(42), *stateVal.IntVal)

	stepExe, stepOK := got.ActiveStepExecutions["mypkg.MyStep-1"]
	require.True(t, stepOK, "dotted key in ActiveStepExecutions should survive round-trip")
	assert.Equal(t, p.StepExeStatusInvokingExecute, stepExe.Status)

	// Verify deletion with dotted key also works
	uErr2 := store.UpdateRunWithNewTasks(ctx, 0, "test-ns", runID, got.Version, &p.RunRowUpdate{
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"mypkg.MyStep-1": nil, // delete
		},
	}, nil)
	require.Nil(t, uErr2)

	got2, _ := store.GetRun(ctx, 0, "test-ns", runID, p.GetRunOptions{})
	_, deleted := got2.ActiveStepExecutions["mypkg.MyStep-1"]
	assert.False(t, deleted, "dotted key should be deletable via $unset")
}

// ============================================================================
// BlobStore Tests
// ============================================================================

func TestBlobStore_BatchInsertAndGet(t *testing.T) {
	ctx := context.Background()
	store, err := NewBlobStoreWithDatabase(ctx, getMongoURI(t), testDBName, DefaultOperationTimeouts())
	require.Nil(t, err)
	defer store.Close()

	runID := uuid.NewString()
	blobs := []p.BlobEntry{
		{BlobID: ids.NewBlobID(), Encoding: "json", Payload: []byte(`{"key":"value1"}`)},
		{BlobID: ids.NewBlobID(), Encoding: "json", Payload: []byte(`{"key":"value2"}`)},
	}

	iErr := store.BatchInsertBlobs(ctx, 0, "test-ns", runID, blobs)
	require.Nil(t, iErr)

	blobIDs := []ids.BlobID{blobs[0].BlobID, blobs[1].BlobID}
	got, gErr := store.BatchGetBlobs(ctx, 0, "test-ns", runID, blobIDs)
	require.Nil(t, gErr)
	assert.Equal(t, 2, len(got))

	foundPayloads := map[string]bool{}
	for _, b := range got {
		foundPayloads[string(b.Payload)] = true
		assert.Equal(t, "json", b.Encoding)
	}
	assert.True(t, foundPayloads[`{"key":"value1"}`])
	assert.True(t, foundPayloads[`{"key":"value2"}`])
}

func TestBlobStore_EmptyBatch(t *testing.T) {
	ctx := context.Background()
	store, err := NewBlobStoreWithDatabase(ctx, getMongoURI(t), testDBName, DefaultOperationTimeouts())
	require.Nil(t, err)
	defer store.Close()

	require.Nil(t, store.BatchInsertBlobs(ctx, 0, "ns", "run", nil))
	got, gErr := store.BatchGetBlobs(ctx, 0, "ns", "run", nil)
	require.Nil(t, gErr)
	assert.Nil(t, got)
}

// ============================================================================
// DLQ Store Tests
// ============================================================================

func TestDLQStore_WriteDLQ(t *testing.T) {
	ctx := context.Background()
	store, storeErr := NewDLQStoreWithDatabase(ctx, getMongoURI(t), testDBName, DefaultOperationTimeouts())
	require.Nil(t, storeErr)
	defer store.Close()

	// Immediate task DLQ entry
	entry := &p.DLQEntry{
		ShardID:       42,
		TaskID:        ids.NewTaskID(),
		QueueType:     p.RowTypeImmediateTask,
		TaskType:      int32(p.ImmediateTaskRunInitialDispatch),
		RunID:         uuid.NewString(),
		Namespace:     "default",
		TaskListName:  "benchmark-workers-1",
		SortKey:       17179869572,
		Error:         "range_id mismatch: tasklist default/benchmark-workers-1/0: expected range_id=1",
		ErrorCategory: "conflict",
		CreatedAt:     time.Now().Add(-10 * time.Second),
		MemberID:      "dex-0",
	}

	err := store.WriteDLQ(ctx, entry)
	require.Nil(t, err, "WriteDLQ should succeed for immediate task")

	// Timer task DLQ entry
	entry2 := &p.DLQEntry{
		ShardID:       42,
		TaskID:        ids.NewTaskID(),
		QueueType:     p.RowTypeTimerTask,
		TaskType:      int32(p.TimerTaskRunHeartbeat),
		RunID:         uuid.NewString(),
		Namespace:     "prod",
		TaskListName:  "workers-99",
		SortKey:       25769804181,
		Error:         "heartbeat timeout",
		ErrorCategory: "unavailable",
		CreatedAt:     time.Now(),
		MemberID:      "dex-3",
	}

	err2 := store.WriteDLQ(ctx, entry2)
	require.Nil(t, err2, "WriteDLQ should succeed for timer task")
}

func TestRunStore_ReplaceMapsClearAbsentKeys(t *testing.T) {
	ctx := context.Background()
	store := getRunStoreWithCleanup(t)

	runID := uuid.NewString()
	v1, v2 := int64(1), int64(2)
	run := &p.RunRow{
		ShardID: 0, Namespace: "test-ns", ID: runID,
		FlowType: "test", TaskListName: "g", Status: p.RunStatusPending,
		StateMap: map[string]p.Value{
			"keep": {Type: p.ValueTypeInt, IntVal: &v1},
			"drop": {Type: p.ValueTypeInt, IntVal: &v2},
		},
		ActiveStepExecutions: map[string]p.ActiveStepExecution{
			"step-1": {Status: p.StepExeStatusInvokingExecute},
			"step-2": {Status: p.StepExeStatusWaitingForCondition},
		},
		StepExeIDCounters:         map[string]int32{"a": 1, "b": 2},
		UnconsumedChannelMessages: map[string][]p.ChannelMessage{},
	}
	require.Nil(t, store.CreateRunWithTasks(ctx, run, []p.TaskRow{{Immediate: &p.ImmediateTaskRow{
		ShardID: 0, ID: ids.NewTaskID(), TaskType: p.ImmediateTaskRunInitialDispatch,
		TaskInfo: p.ImmediateTaskInfo{RunID: runID, Namespace: "test-ns", TaskListName: "g"},
	}}}))

	replacedState := map[string]p.Value{"keep": {Type: p.ValueTypeInt, IntVal: &v1}}
	replacedSteps := map[string]p.ActiveStepExecution{
		"step-1": {Status: p.StepExeStatusInvokingExecute},
	}
	replacedCounters := map[string]int32{"a": 1}
	require.Nil(t, store.UpdateRunWithNewTasks(ctx, 0, "test-ns", runID, 1, &p.RunRowUpdate{
		ReplaceStateMap:             &replacedState,
		ReplaceActiveStepExecutions: &replacedSteps,
		ReplaceStepExeIDCounters:    &replacedCounters,
		ReplaceAllUnconsumedChannels: &map[string][]p.ChannelMessage{},
	}, nil))

	got, _ := store.GetRun(ctx, 0, "test-ns", runID, p.GetRunOptions{})
	_, hasDrop := got.StateMap["drop"]
	assert.False(t, hasDrop)
	_, hasStep2 := got.ActiveStepExecutions["step-2"]
	assert.False(t, hasStep2)
	_, hasB := got.StepExeIDCounters["b"]
	assert.False(t, hasB)
}
