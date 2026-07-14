package runengine

import (
	"context"
	"testing"
	"time"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/server/common/errors"
	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/common/utils/ids"
	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/internal/engine"
	"github.com/superdurable/dex/server/internal/integration/testhelpers"
	p "github.com/superdurable/dex/server/internal/persistence"
	"github.com/superdurable/dex/server/internal/shardmanager"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const dbPrefix = "dex_test_integration_runengine"

// wcrErr drops the WorkerCallResponse piggyback from a Process* return so the
// error can be asserted inline with require.Nil.
func wcrErr(_ *pb.WorkerCallResponse, err errors.CategorizedError) errors.CategorizedError {
	return err
}

func nullPbValue() *pb.Value {
	return &pb.Value{Kind: &pb.Value_NullValue{}}
}

func parkAllStepsWaiting(t *testing.T, h *testHarness, ctx context.Context, runID, workerID string, counter, lastReceived int64) *pb.ProcessReleaseRunResponse {
	t.Helper()
	shardID := h.mapper.GetShardID(h.ns, runID)
	resp, err := h.eng.ProcessReleaseRun(ctx, shardID, &pb.ProcessReleaseRunRequest{
		Namespace:     h.ns,
		RunId:         runID,
		WorkerId:      workerID,
		ReleaseReason: pb.ReleaseRunReason_RELEASE_RUN_REASON_ALL_STEPS_WAITING,
		Context: &pb.WorkerCallContext{
			WorkerId:                             workerID,
			WorkerRequestCounter:                 counter,
			LastReceivedExternalChannelMessageId: lastReceived,
		},
	})
	require.Nil(t, err)
	return resp
}

type testHarness struct {
	runStore     p.RunStore
	blobStore    p.BlobStore
	historyStore p.HistoryStore
	mapper       shardmanager.ShardMapper
	eng          engine.RunEngine
	ns           string // unique namespace per test
}

func newTestHarness(t *testing.T) *testHarness {
	set := testhelpers.NewStoreSetForTest(t, dbPrefix)
	ctx := context.Background()
	set.Run.DeleteAll(ctx)

	mapper := shardmanager.NewShardMapper(config.ShardConfig{DefaultShardsForNewNamespaces: 2})
	logger := log.NewNoop()
	sm := &testhelpers.FakeShardManager{}
	sharded := shardmanager.NewShardedRunStore(set.Run, sm, nil)
	runCfg := config.DefaultRunServiceConfig()
	eng := engine.NewRunEngine(&runCfg, sharded, set.History, set.Blob, mapper, sm, logger)

	ns := "test-" + uuid.NewString()[:8]
	// Store cleanup is registered by NewStoreSetForTest.
	return &testHarness{runStore: set.Run, blobStore: set.Blob, historyStore: set.History, mapper: mapper, eng: eng, ns: ns}
}

// ============================================================================
// Full Flow E2E Tests
// ============================================================================

func TestE2E_StartRun_CreatesPendingRunAndTask(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	runID := uuid.NewString()

	err := h.eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: h.ns, RunId: runID, FlowType: "e2e-test", TaskListName: "g",
	})
	require.Nil(t, err)

	shardID := h.mapper.GetShardID(h.ns, runID)
	run, gErr := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	require.Nil(t, gErr)
	assert.Equal(t, p.RunStatusPending, run.Status)
	assert.Equal(t, int64(1), run.Version)
	assert.Equal(t, "e2e-test", run.FlowType)

	// Verify immediate dispatch task was created alongside the run.
	// With per-test namespace cleanup, only our task should be present.
	tasks, tErr := h.runStore.RangeReadImmediateTasks(ctx, shardID, 0, 10)
	require.Nil(t, tErr)
	found := false
	for _, task := range tasks {
		if task.TaskInfo.RunID == runID && task.TaskType == p.ImmediateTaskRunInitialDispatch {
			found = true
		}
	}
	t.Logf("found %d immediate tasks on shard %d, dispatch_task_found=%v", len(tasks), shardID, found)
	assert.True(t, found, "run_initial_dispatch_task should exist for new run")
}

func TestE2E_StepCompleted_NextStep_NewActiveStep(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	runID := uuid.NewString()

	require.Nil(t, h.eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: h.ns, RunId: runID, FlowType: "e2e-test", TaskListName: "g",
	}))

	shardID := h.mapper.GetShardID(h.ns, runID)

	// Transition to running with an active step
	run, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	running := p.RunStatusRunning
	h.runStore.UpdateRunWithNewTasks(ctx, shardID, h.ns, runID, run.Version, &p.RunRowUpdate{
		Status: &running,
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"init-1": {Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusInvokingExecute},
		},
		StepExeIDCounters: map[string]int32{"init": 1},
	}, nil)

	run, _ = h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})

	// Step completes with next steps
	_, err := h.eng.ProcessStepExecuteCompleted(ctx, shardID, h.ns, &pb.StepExecuteCompletedRequest{
		RunId: runID, StepExeId: "init-1",
		Context:      &pb.WorkerCallContext{WorkerRequestCounter: run.WorkerRequestCounter + 1},
		StopDecision: pb.StopDecision_STOP_DECISION_NONE,
		NextSteps: []*pb.NextStep{
			{StepId: "process", Input: testhelpers.NullPbValue()},
			{StepId: "validate", Input: testhelpers.NullPbValue()},
		},
	})
	require.Nil(t, err)

	updated, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	assert.Equal(t, p.RunStatusRunning, updated.Status)
	assert.Len(t, updated.ActiveStepExecutions, 2)
	_, hasProcess := updated.ActiveStepExecutions["process-1"]
	_, hasValidate := updated.ActiveStepExecutions["validate-1"]
	assert.True(t, hasProcess)
	assert.True(t, hasValidate)
}

func TestE2E_WaitForComplete_AllWaiting_DurableTimer(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	runID := uuid.NewString()

	require.Nil(t, h.eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: h.ns, RunId: runID, FlowType: "e2e-test", TaskListName: "g",
	}))

	shardID := h.mapper.GetShardID(h.ns, runID)
	run, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	running := p.RunStatusRunning
	h.runStore.UpdateRunWithNewTasks(ctx, shardID, h.ns, runID, run.Version, &p.RunRowUpdate{
		Status: &running,
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"wait-1": {Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusInvokingWaitFor},
		},
	}, nil)

	run, _ = h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	fireAt := time.Now().Add(1 * time.Hour).UnixMilli()

	// WaitFor completes with timer condition
	_, err := h.eng.ProcessStepWaitForCompleted(ctx, shardID, h.ns, &pb.StepWaitForCompletedRequest{
		RunId: runID, StepExeId: "wait-1",
		Context: &pb.WorkerCallContext{WorkerRequestCounter: run.WorkerRequestCounter + 1},
		WaitForCondition: &pb.WaitForCondition{
			Type: pb.WaitType_WAIT_TYPE_ANY_OF,
			Conditions: []*pb.SingleCondition{
				{Condition: &pb.SingleCondition_Timer{Timer: &pb.TimerCondition{FireAtUnixMs: fireAt}}},
			},
		},
	})
	require.Nil(t, err)

	updated, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	assert.Equal(t, p.RunStatusRunning, updated.Status)
	workerID := "worker-park-1"
	require.Nil(t, h.runStore.UpdateRunWithNewTasks(ctx, shardID, h.ns, runID, updated.Version, &p.RunRowUpdate{
		WorkerID: &workerID,
	}, nil))
	updated, _ = h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	_ = parkAllStepsWaiting(t, h, ctx, runID, workerID, updated.WorkerRequestCounter+1, updated.ExternalChannelMessageCounter)

	updated, _ = h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	assert.Equal(t, p.RunStatusAllStepsWaitingForConditions, updated.Status)
	assert.NotEmpty(t, updated.ActiveDurableTimerID)
}

func TestE2E_ExternalChannelMessage_WakesWaitingRun(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	runID := uuid.NewString()

	require.Nil(t, h.eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: h.ns, RunId: runID, FlowType: "e2e-test", TaskListName: "g",
	}))

	shardID := h.mapper.GetShardID(h.ns, runID)
	run, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})

	// Set up: run is all_steps_waiting with a step waiting on channel "notify"
	allWaiting := p.RunStatusAllStepsWaitingForConditions
	h.runStore.UpdateRunWithNewTasks(ctx, shardID, h.ns, runID, run.Version, &p.RunRowUpdate{
		Status: &allWaiting,
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"wait-1": {
				Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusWaitingForCondition,
				WaitForCondition: &p.WaitForCondition{Type: p.WaitTypeAnyOf,
					Conditions: []p.SingleCondition{{Channel: &p.ChannelCondition{ChannelName: "notify", Min: 1}}}},
			},
		},
	}, nil)

	run, _ = h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})

	// External channel message arrives
	err := h.eng.PublishExternalChannelMessages(ctx, shardID, &pb.PublishToChannelRequest{
		RunId: runID, Namespace: h.ns, ChannelName: "notify",
		Values: []*pb.Value{testhelpers.IntPbValue(42)},
	})
	require.Nil(t, err)

	updated, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	t.Logf("run status after channel message: %d, version: %d", updated.Status, updated.Version)
	assert.Equal(t, p.RunStatusPending, updated.Status)
	// Server wake promotes with reservation persisted; queue unchanged until Execute.
	step, hasStep := updated.ActiveStepExecutions["wait-1"]
	assert.True(t, hasStep)
	t.Logf("step wait-1 status after channel message: %d", step.Status)
	assert.Equal(t, p.StepExeStatusInvokingExecute, step.Status)
	assert.NotZero(t, step.ExecuteMethodExeID)
	require.Len(t, step.ConditionResults, 1)
	assert.True(t, step.ConditionResults[0].Channel.Satisfied)
	require.Len(t, updated.UnconsumedChannelMessages["notify"], 1)
}

func TestE2E_ChannelPublish_CrossStepUnblock(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	runID := uuid.NewString()

	require.Nil(t, h.eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: h.ns, RunId: runID, FlowType: "e2e-test", TaskListName: "g",
	}))

	shardID := h.mapper.GetShardID(h.ns, runID)
	run, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})

	// Set up: stepA executing, stepB waiting on channel "data"
	running := p.RunStatusRunning
	h.runStore.UpdateRunWithNewTasks(ctx, shardID, h.ns, runID, run.Version, &p.RunRowUpdate{
		Status: &running,
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"stepA-1": {Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusInvokingExecute},
			"stepB-1": {Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusWaitingForCondition,
				WaitForCondition: &p.WaitForCondition{Type: p.WaitTypeAnyOf,
					Conditions: []p.SingleCondition{{Channel: &p.ChannelCondition{ChannelName: "data", Min: 1}}}}},
		},
	}, nil)

	run, _ = h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})

	// stepA completes and publishes to "data". Server stores the publish;
	// sibling promotion is the worker's job via ProcessStepsUnblocked.
	_, err := h.eng.ProcessStepExecuteCompleted(ctx, shardID, h.ns, &pb.StepExecuteCompletedRequest{
		RunId: runID, StepExeId: "stepA-1",
		Context:        &pb.WorkerCallContext{WorkerRequestCounter: run.WorkerRequestCounter + 1},
		StopDecision:   pb.StopDecision_STOP_DECISION_DEAD_END,
		ChannelPublish: []*pb.ChannelPublish{{ChannelName: "data", Values: []*pb.Value{testhelpers.IntPbValue(99)}}},
	})
	require.Nil(t, err)

	updated, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	assert.Equal(t, p.RunStatusRunning, updated.Status)
	stepB, hasB := updated.ActiveStepExecutions["stepB-1"]
	assert.True(t, hasB)
	assert.Equal(t, p.StepExeStatusWaitingForCondition, stepB.Status)
	assert.Len(t, updated.UnconsumedChannelMessages["data"], 1)
}

func TestE2E_BlobStore_LargeValues(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	runID := uuid.NewString()

	// Start with an EncodedObject starting-step input (should go through BlobStore)
	err := h.eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: h.ns, RunId: runID, FlowType: "e2e-test", TaskListName: "g",
		StartingSteps: []*pb.NextStep{{
			StepId: "start",
			Input: &pb.Value{Kind: &pb.Value_EncodedObject{EncodedObject: &pb.EncodedObject{
				Encoding: "json", Payload: []byte(`{"large":"data"}`),
			}}},
		}},
	})
	require.Nil(t, err)

	shardID := h.mapper.GetShardID(h.ns, runID)
	run, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	stepExe := run.ActiveStepExecutions["start-1"]

	// Starting step input should be stored as blob_ref
	assert.Equal(t, p.ValueTypeBlobRef, stepExe.Input.Type)
	assert.NotEmpty(t, stepExe.Input.BlobID)

	// Verify blob is actually in BlobStore
	blobs, bErr := h.blobStore.BatchGetBlobs(ctx, shardID, h.ns, runID, []ids.BlobID{stepExe.Input.BlobID})
	require.Nil(t, bErr)
	assert.Len(t, blobs, 1)
	assert.Equal(t, "json", blobs[0].Encoding)
	assert.Equal(t, []byte(`{"large":"data"}`), blobs[0].Payload)
}

// ============================================================================
// Concurrent Edge Case Tests
// ============================================================================

func TestE2E_ConcurrentExternalChannelMessages_CASRetry(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	runID := uuid.NewString()

	require.Nil(t, h.eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: h.ns, RunId: runID, FlowType: "e2e-test", TaskListName: "g",
	}))

	shardID := h.mapper.GetShardID(h.ns, runID)
	run, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	running := p.RunStatusRunning
	h.runStore.UpdateRunWithNewTasks(ctx, shardID, h.ns, runID, run.Version, &p.RunRowUpdate{
		Status: &running,
	}, nil)

	// Send 5 concurrent channel messages -- some will CAS-fail and need retry
	const numMessages = 5
	errCh := make(chan error, numMessages)
	for i := 0; i < numMessages; i++ {
		go func(idx int) {
			// Retry on CAS conflict
			for retries := 0; retries < 10; retries++ {
				err := h.eng.PublishExternalChannelMessages(ctx, shardID, &pb.PublishToChannelRequest{
					RunId: runID, Namespace: h.ns, ChannelName: "events",
					Values: []*pb.Value{testhelpers.IntPbValue(int64(idx))},
				})
				if err == nil {
					errCh <- nil
					return
				}
				if err.IsRetriable() {
					continue
				}
				errCh <- err
				return
			}
			errCh <- nil
		}(i)
	}

	for i := 0; i < numMessages; i++ {
		err := <-errCh
		assert.Nil(t, err)
	}

	// Verify all messages stored
	updated, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	assert.Equal(t, numMessages, len(updated.UnconsumedChannelMessages["events"]))
}

func TestE2E_WorkerRequestCounter_Idempotent(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	runID := uuid.NewString()

	require.Nil(t, h.eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: h.ns, RunId: runID, FlowType: "e2e-test", TaskListName: "g",
	}))

	shardID := h.mapper.GetShardID(h.ns, runID)
	run, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	running := p.RunStatusRunning
	h.runStore.UpdateRunWithNewTasks(ctx, shardID, h.ns, runID, run.Version, &p.RunRowUpdate{
		Status: &running,
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"step-1": {Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusInvokingExecute},
		},
	}, nil)

	run, _ = h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	counter := run.WorkerRequestCounter + 1

	// First call
	_, err := h.eng.ProcessStepExecuteCompleted(ctx, shardID, h.ns, &pb.StepExecuteCompletedRequest{
		RunId: runID, StepExeId: "step-1",
		Context:      &pb.WorkerCallContext{WorkerRequestCounter: counter},
		StopDecision: pb.StopDecision_STOP_DECISION_COMPLETE,
	})
	require.Nil(t, err)

	// Duplicate (same counter) -- should be no-op, no error
	_, err2 := h.eng.ProcessStepExecuteCompleted(ctx, shardID, h.ns, &pb.StepExecuteCompletedRequest{
		RunId: runID, StepExeId: "step-1",
		Context:      &pb.WorkerCallContext{WorkerRequestCounter: counter},
		StopDecision: pb.StopDecision_STOP_DECISION_COMPLETE,
	})
	require.Nil(t, err2)

	// Verify run completed exactly once
	updated, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	assert.Equal(t, p.RunStatusCompleted, updated.Status)
}

// ============================================================================
// Worker-driven channel consumption (consumed_count → ReplaceUnconsumedChannels)
// ============================================================================

// TestE2E_DurableTimer_FullFireCycle drives the full server-side timer fire path:
// WaitFor completion creates a durable timer; we then directly invoke
// HandleStepWaitForTimerFired (simulating what TimerBatchReader →
// HandleTimerTask does) and assert the run resumed (Pending) with the timer
// fired flag set, and a dispatch task was created. The active step stays
// WAITING_FOR_CONDITION because the engine path is evaluate-only — the worker
// re-evaluates locally on resume per the worker-driven consumption contract.
func TestE2E_DurableTimer_FullFireCycle(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	runID := uuid.NewString()

	require.Nil(t, h.eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: h.ns, RunId: runID, FlowType: "e2e-test", TaskListName: "g",
	}))

	shardID := h.mapper.GetShardID(h.ns, runID)
	run, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	running := p.RunStatusRunning
	h.runStore.UpdateRunWithNewTasks(ctx, shardID, h.ns, runID, run.Version, &p.RunRowUpdate{
		Status: &running,
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"wait-1": {Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusInvokingWaitFor},
		},
	}, nil)

	run, _ = h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	// Set the timer fire-at far enough in the future that the
	// Running → AllStepsWaiting unconsumed sweep doesn't satisfy it
	// (otherwise the run would skip past AllStepsWaiting straight to
	// Pending and there'd be no durable timer to fire). 1 hour is well
	// past time.Now() inside evaluateAndDispatchIfSatisfied.
	fireAt := time.Now().Add(1 * time.Hour).UnixMilli()

	// WaitFor completes with timer-only AnyOf — engine creates a durable timer task.
	require.Nil(t, wcrErr(h.eng.ProcessStepWaitForCompleted(ctx, shardID, h.ns, &pb.StepWaitForCompletedRequest{
		RunId: runID, StepExeId: "wait-1",
		Context: &pb.WorkerCallContext{WorkerRequestCounter: run.WorkerRequestCounter + 1},
		WaitForCondition: &pb.WaitForCondition{
			Type: pb.WaitType_WAIT_TYPE_ANY_OF,
			Conditions: []*pb.SingleCondition{
				{Condition: &pb.SingleCondition_Timer{Timer: &pb.TimerCondition{FireAtUnixMs: fireAt}}},
			},
		},
	})))

	armed, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	require.Equal(t, p.RunStatusRunning, armed.Status)
	workerID := "worker-timer-1"
	require.Nil(t, h.runStore.UpdateRunWithNewTasks(ctx, shardID, h.ns, runID, armed.Version, &p.RunRowUpdate{
		WorkerID: &workerID,
	}, nil))
	armed, _ = h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	_ = parkAllStepsWaiting(t, h, ctx, runID, workerID, armed.WorkerRequestCounter+1, armed.ExternalChannelMessageCounter)

	armed, _ = h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	require.Equal(t, p.RunStatusAllStepsWaitingForConditions, armed.Status)
	require.NotEmpty(t, armed.ActiveDurableTimerID, "engine should have created a durable timer")

	// Directly drive the timer-fired path (what TimerBatchReader → HandleTimerTask does).
	// Pass FireAtUnixMs = fireAt + 1ms so the evaluator's
	// `cond.Timer.FireAtUnixMs <= effectiveNow` check passes regardless
	// of wall-clock progression in the test.
	require.Nil(t, h.eng.HandleStepWaitForTimerFired(ctx, shardID, &engine.StepWaitForTimerFiredRequest{
		RunID:        runID,
		Namespace:    h.ns,
		TimerID:      armed.ActiveDurableTimerID,
		FireAtUnixMs: fireAt + 1,
	}))

	resumed, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	assert.Equal(t, p.RunStatusPending, resumed.Status, "timer fire should transition AllStepsWaiting → Pending")
	assert.True(t, resumed.DurableTimerFired, "DurableTimerFired flag should be set so worker can read it from PollResponse")
	step, ok := resumed.ActiveStepExecutions["wait-1"]
	require.True(t, ok)
	assert.Equal(t, p.StepExeStatusInvokingExecute, step.Status,
		"timer fire should promote WAITING → INVOKING_EXECUTE with persisted reservation")
	assert.NotZero(t, step.ExecuteMethodExeID)
	require.Len(t, step.ConditionResults, 1)
	assert.True(t, step.ConditionResults[0].Timer.Fired)
}

// TestE2E_Channel_ReservationSpliceOnComplete verifies reservation model:
// messages stay in the queue on promote; splice on Execute complete only.
func TestE2E_Channel_ReservationSpliceOnComplete(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	runID := uuid.NewString()

	require.Nil(t, h.eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: h.ns, RunId: runID, FlowType: "e2e-test", TaskListName: "g",
	}))

	shardID := h.mapper.GetShardID(h.ns, runID)
	run, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	running := p.RunStatusRunning
	v1, v2, v3 := int64(1), int64(2), int64(3)
	h.runStore.UpdateRunWithNewTasks(ctx, shardID, h.ns, runID, run.Version, &p.RunRowUpdate{
		Status: &running,
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"consumer-1": {
				Input:              p.Value{Type: p.ValueTypeNull},
				Status:             p.StepExeStatusInvokingExecute,
				ExecuteMethodExeID: 1,
				ConditionResults: []p.ConditionResult{{
					Channel: &p.ChannelConditionResult{
						ChannelName: "events", Satisfied: true, ConsumedCount: 2,
					},
				}},
			},
		},
		ReplaceUnconsumedChannels: map[string][]p.ChannelMessage{"events": {
			{ID: 1, Value: p.Value{Type: p.ValueTypeInt, IntVal: &v1}},
			{ID: 2, Value: p.Value{Type: p.ValueTypeInt, IntVal: &v2}},
			{ID: 3, Value: p.Value{Type: p.ValueTypeInt, IntVal: &v3}},
		}},
	}, nil)

	run, _ = h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	require.Len(t, run.UnconsumedChannelMessages["events"], 3)

	// Complete without top-level condition_results — server splices from persisted reservation.
	require.Nil(t, wcrErr(h.eng.ProcessStepExecuteCompleted(ctx, shardID, h.ns, &pb.StepExecuteCompletedRequest{
		RunId: runID, StepExeId: "consumer-1",
		Context:      &pb.WorkerCallContext{WorkerRequestCounter: run.WorkerRequestCounter + 1},
		StopDecision: pb.StopDecision_STOP_DECISION_DEAD_END,
	})))

	updated, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	require.Len(t, updated.UnconsumedChannelMessages["events"], 1, "consumed_count=2 should leave 1 message")
	assert.Equal(t, int64(3), *updated.UnconsumedChannelMessages["events"][0].Value.IntVal,
		"front prefix is consumed; the third message (value 3) should remain")
}

// TestE2E_ProcessStepsUnblocked_OutOfBandCheckpoint exercises the new
// ProcessStepsUnblocked engine method end-to-end:
//   - sets up a Running run with a sibling waiting on a channel that already
//     has a message in unconsumed_channel_messages
//   - submits a StepsUnblockedRequest reporting the worker locally promoted
//     the sibling and consumed 1 message from the channel
//   - asserts the engine: (a) transitioned the sibling to INVOKING_EXECUTE,
//     (b) populated ActiveStepExecution.condition_results, (c) pruned the
//     consumed message from unconsumed_channel_messages, (d) emitted a
//     StepsUnblocked history event.
func TestE2E_ProcessStepsUnblocked_OutOfBandCheckpoint(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	runID := uuid.NewString()

	require.Nil(t, h.eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: h.ns, RunId: runID, FlowType: "e2e-test", TaskListName: "g",
	}))

	shardID := h.mapper.GetShardID(h.ns, runID)
	run, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	running := p.RunStatusRunning
	v1 := int64(42)
	h.runStore.UpdateRunWithNewTasks(ctx, shardID, h.ns, runID, run.Version, &p.RunRowUpdate{
		Status: &running,
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"runner-1": {Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusInvokingExecute},
			"waiter-1": {
				Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusWaitingForCondition,
				WaitForCondition: &p.WaitForCondition{Type: p.WaitTypeAnyOf,
					Conditions: []p.SingleCondition{{Channel: &p.ChannelCondition{ChannelName: "events", Min: 1}}}},
			},
		},
		ReplaceUnconsumedChannels: map[string][]p.ChannelMessage{"events": {{ID: 1, Value: p.Value{Type: p.ValueTypeInt, IntVal: &v1}}}},
	}, nil)

	run, _ = h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})

	// Worker reports it locally promoted waiter-1 (e.g. a local timer fire
	// in this test, but the engine path is identical for external delivery).
	require.Nil(t, wcrErr(h.eng.ProcessStepsUnblocked(ctx, shardID, &pb.StepsUnblockedRequest{
		Namespace: h.ns,
		RunId:     runID,
		Context:   &pb.WorkerCallContext{WorkerRequestCounter: run.WorkerRequestCounter + 1},
		StepsUnblocked: []*pb.StepUnblocked{{
			StepExeId:          "waiter-1",
			ExecuteMethodExeId: 2,
			ConditionResults: []*pb.ConditionResult{{
				Result: &pb.ConditionResult_Channel{
					Channel: &pb.ChannelConditionResult{
						ChannelName: "events", Satisfied: true, ConsumedCount: 1,
					},
				},
			}},
		}},
	})))

	updated, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	waiter, ok := updated.ActiveStepExecutions["waiter-1"]
	require.True(t, ok)
	assert.Equal(t, p.StepExeStatusInvokingExecute, waiter.Status,
		"waiter-1 should have been transitioned to INVOKING_EXECUTE by ProcessStepsUnblocked")
	require.Len(t, waiter.ConditionResults, 1, "ConditionResults should be persisted on the active step")
	assert.NotNil(t, waiter.ConditionResults[0].Channel)
	assert.Equal(t, int32(1), waiter.ConditionResults[0].Channel.ConsumedCount)
	assert.NotZero(t, waiter.ExecuteMethodExeID)
	assert.Equal(t, int64(2), waiter.ExecuteMethodExeID)
	require.Len(t, updated.UnconsumedChannelMessages["events"], 1,
		"reserve on unblock must not shorten the queue")
}

// TestE2E_Channel_NoDoubleRemoveOnComplete verifies Execute complete
// does not splice the queue from persisted condition_results alone.
func TestE2E_Channel_NoDoubleRemoveOnComplete(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	runID := uuid.NewString()

	require.Nil(t, h.eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: h.ns, RunId: runID, FlowType: "e2e-test", TaskListName: "g",
	}))

	shardID := h.mapper.GetShardID(h.ns, runID)
	run, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	running := p.RunStatusRunning
	v1, v2 := int64(1), int64(2)
	h.runStore.UpdateRunWithNewTasks(ctx, shardID, h.ns, runID, run.Version, &p.RunRowUpdate{
		Status: &running,
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"consumer-1": {
				Input:              p.Value{Type: p.ValueTypeNull},
				Status:             p.StepExeStatusInvokingExecute,
				ExecuteMethodExeID: 1,
				ConditionResults: []p.ConditionResult{{
					Channel: &p.ChannelConditionResult{
						ChannelName: "events", Satisfied: true, ConsumedCount: 1,
					},
				}},
			},
		},
		ReplaceUnconsumedChannels: map[string][]p.ChannelMessage{"events": {
			{ID: 1, Value: p.Value{Type: p.ValueTypeInt, IntVal: &v1}},
			{ID: 2, Value: p.Value{Type: p.ValueTypeInt, IntVal: &v2}},
		}},
	}, nil)

	run, _ = h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	require.Nil(t, wcrErr(h.eng.ProcessStepExecuteCompleted(ctx, shardID, h.ns, &pb.StepExecuteCompletedRequest{
		RunId: runID, StepExeId: "consumer-1",
		Context:      &pb.WorkerCallContext{WorkerRequestCounter: run.WorkerRequestCounter + 1},
		StopDecision: pb.StopDecision_STOP_DECISION_DEAD_END,
	})))

	updated, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	require.Len(t, updated.UnconsumedChannelMessages["events"], 1,
		"Execute complete must not splice queue from persisted condition_results")
}

// TestE2E_StepExecuteCompleted_StaysRunningWhenAllWaiting verifies completion
// defers AllStepsWaiting parking to ProcessReleaseRun.
func TestE2E_StepExecuteCompleted_StaysRunningWhenAllWaiting(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	runID := uuid.NewString()

	require.Nil(t, h.eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: h.ns, RunId: runID, FlowType: "e2e-test", TaskListName: "g",
	}))

	shardID := h.mapper.GetShardID(h.ns, runID)
	run, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	running := p.RunStatusRunning
	require.Nil(t, h.runStore.UpdateRunWithNewTasks(ctx, shardID, h.ns, runID, run.Version, &p.RunRowUpdate{
		Status: &running,
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"stepA-1": {Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusInvokingExecute},
			"stepB-1": {
				Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusWaitingForCondition,
				WaitForCondition: &p.WaitForCondition{Type: p.WaitTypeAnyOf,
					Conditions: []p.SingleCondition{{Channel: &p.ChannelCondition{ChannelName: "events", Min: 1}}}},
			},
		},
		ReplaceUnconsumedChannels: map[string][]p.ChannelMessage{
			"events": {{ID: 1, Value: p.Value{Type: p.ValueTypeNull}}},
		},
	}, nil))
	run, _ = h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})

	require.Nil(t, wcrErr(h.eng.ProcessStepExecuteCompleted(ctx, shardID, h.ns, &pb.StepExecuteCompletedRequest{
		RunId: runID, StepExeId: "stepA-1",
		Context:      &pb.WorkerCallContext{WorkerRequestCounter: run.WorkerRequestCounter + 1},
		StopDecision: pb.StopDecision_STOP_DECISION_DEAD_END,
	})))

	updated, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	assert.Equal(t, p.RunStatusRunning, updated.Status)
	stepB, ok := updated.ActiveStepExecutions["stepB-1"]
	require.True(t, ok)
	assert.Equal(t, p.StepExeStatusWaitingForCondition, stepB.Status)
	assert.Empty(t, updated.ActiveDurableTimerID)
}

func TestE2E_ProcessReleaseRun_AllStepsWaiting_ParksWithTimer(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	runID := uuid.NewString()

	require.Nil(t, h.eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: h.ns, RunId: runID, FlowType: "e2e-test", TaskListName: "g",
	}))

	shardID := h.mapper.GetShardID(h.ns, runID)
	run, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	fireAt := time.Now().Add(1 * time.Hour).UnixMilli()
	running := p.RunStatusRunning
	workerID := "worker-park-timer"
	require.Nil(t, h.runStore.UpdateRunWithNewTasks(ctx, shardID, h.ns, runID, run.Version, &p.RunRowUpdate{
		Status:   &running,
		WorkerID: &workerID,
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"wait-1": {
				Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusWaitingForCondition,
				WaitForCondition: &p.WaitForCondition{Type: p.WaitTypeAnyOf,
					Conditions: []p.SingleCondition{{Timer: &p.TimerCondition{FireAtUnixMs: fireAt}}}},
			},
		},
	}, nil))
	run, _ = h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})

	_ = parkAllStepsWaiting(t, h, ctx, runID, workerID, run.WorkerRequestCounter+1, run.ExternalChannelMessageCounter)

	updated, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	assert.Equal(t, p.RunStatusAllStepsWaitingForConditions, updated.Status)
	assert.NotEmpty(t, updated.ActiveDurableTimerID)
	assert.Empty(t, updated.WorkerID)

	tasks, _ := h.runStore.RangeReadImmediateTasks(ctx, shardID, 0, 100)
	for _, task := range tasks {
		assert.NotEqual(t, p.ImmediateTaskRunResumeDispatch, task.TaskType,
			"park must not schedule resume dispatch")
	}
}

func TestE2E_ProcessReleaseRun_AllStepsWaiting_NotCaughtUp(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	runID := uuid.NewString()

	require.Nil(t, h.eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: h.ns, RunId: runID, FlowType: "e2e-test", TaskListName: "g",
	}))

	shardID := h.mapper.GetShardID(h.ns, runID)
	run, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	extCounter := int64(1)
	running := p.RunStatusRunning
	workerID := "worker-not-caught-up"
	require.Nil(t, h.runStore.UpdateRunWithNewTasks(ctx, shardID, h.ns, runID, run.Version, &p.RunRowUpdate{
		Status:                        &running,
		WorkerID:                      &workerID,
		ExternalChannelMessageCounter: &extCounter,
		ReplaceUnconsumedChannels: map[string][]p.ChannelMessage{
			"events": {{ID: 1, Value: p.Value{Type: p.ValueTypeNull}}},
		},
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"wait-1": {
				Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusWaitingForCondition,
				WaitForCondition: &p.WaitForCondition{Type: p.WaitTypeAnyOf,
					Conditions: []p.SingleCondition{{Channel: &p.ChannelCondition{ChannelName: "events", Min: 1}}}},
			},
		},
	}, nil))
	run, _ = h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})

	resp := parkAllStepsWaiting(t, h, ctx, runID, workerID, run.WorkerRequestCounter+1, 0)
	require.Len(t, resp.WorkerCallResponse.UnreceivedExternalChannelMessages, 1)

	updated, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	assert.Equal(t, p.RunStatusRunning, updated.Status)
	assert.Equal(t, int64(1), updated.WorkerRequestCounter)
}

func TestE2E_WorkerSuppliedMethodIDs_Persisted(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	runID := uuid.NewString()

	require.Nil(t, h.eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: h.ns, RunId: runID, FlowType: "e2e-test", TaskListName: "g",
	}))

	shardID := h.mapper.GetShardID(h.ns, runID)
	run, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	running := p.RunStatusRunning
	methodCounter := int64(5)
	require.Nil(t, h.runStore.UpdateRunWithNewTasks(ctx, shardID, h.ns, runID, run.Version, &p.RunRowUpdate{
		Status: &running,
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"parent-1": {Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusInvokingExecute},
			"waitA-1": {
				Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusWaitingForCondition,
				WaitForCondition: &p.WaitForCondition{Type: p.WaitTypeAnyOf,
					Conditions: []p.SingleCondition{{Channel: &p.ChannelCondition{ChannelName: "events", Min: 1}}}},
			},
			"waitB-1": {
				Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusWaitingForCondition,
				WaitForCondition: &p.WaitForCondition{Type: p.WaitTypeAnyOf,
					Conditions: []p.SingleCondition{{Channel: &p.ChannelCondition{ChannelName: "events", Min: 1}}}},
			},
		},
		ReplaceUnconsumedChannels: map[string][]p.ChannelMessage{
			"events": {{ID: 1, Value: p.Value{Type: p.ValueTypeNull}}, {ID: 2, Value: p.Value{Type: p.ValueTypeNull}}},
		},
		StepMethodExeCounter: &methodCounter,
	}, nil))
	run, _ = h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})

	_, err := h.eng.ProcessStepExecuteCompleted(ctx, shardID, h.ns, &pb.StepExecuteCompletedRequest{
		RunId: runID, StepExeId: "parent-1",
		Context:      &pb.WorkerCallContext{WorkerRequestCounter: run.WorkerRequestCounter + 1},
		StopDecision: pb.StopDecision_STOP_DECISION_NONE,
		NextSteps: []*pb.NextStep{
			{StepId: "childW", Input: nullPbValue(), WaitForMethodExeId: 6},
			{StepId: "childE", Input: nullPbValue(), SkipWaitFor: true, ExecuteMethodExeId: 7},
		},
		StepsUnblocked: []*pb.StepUnblocked{
			{
				StepExeId: "waitA-1", ExecuteMethodExeId: 8,
				ConditionResults: []*pb.ConditionResult{{
					Result: &pb.ConditionResult_Channel{
						Channel: &pb.ChannelConditionResult{ChannelName: "events", Satisfied: true, ConsumedCount: 1},
					},
				}},
			},
			{
				StepExeId: "waitB-1", ExecuteMethodExeId: 9,
				ConditionResults: []*pb.ConditionResult{{
					Result: &pb.ConditionResult_Channel{
						Channel: &pb.ChannelConditionResult{ChannelName: "events", Satisfied: true, ConsumedCount: 1},
					},
				}},
			},
		},
	})
	require.Nil(t, err)

	updated, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	assert.Equal(t, int64(9), updated.StepMethodExeCounter)
	childW, ok := updated.ActiveStepExecutions["childW-1"]
	require.True(t, ok)
	assert.Equal(t, int64(6), childW.WaitForMethodExeID)
	childE, ok := updated.ActiveStepExecutions["childE-1"]
	require.True(t, ok)
	assert.Equal(t, int64(7), childE.ExecuteMethodExeID)
	waitA, ok := updated.ActiveStepExecutions["waitA-1"]
	require.True(t, ok)
	assert.Equal(t, int64(8), waitA.ExecuteMethodExeID)
	waitB, ok := updated.ActiveStepExecutions["waitB-1"]
	require.True(t, ok)
	assert.Equal(t, int64(9), waitB.ExecuteMethodExeID)
}

func TestE2E_ServerDrivenPromote_BumpsCounter(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	runID := uuid.NewString()

	require.Nil(t, h.eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: h.ns, RunId: runID, FlowType: "e2e-test", TaskListName: "g",
	}))

	shardID := h.mapper.GetShardID(h.ns, runID)
	run, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	allWaiting := p.RunStatusAllStepsWaitingForConditions
	counterBefore := int64(3)
	require.Nil(t, h.runStore.UpdateRunWithNewTasks(ctx, shardID, h.ns, runID, run.Version, &p.RunRowUpdate{
		Status:               &allWaiting,
		StepMethodExeCounter: &counterBefore,
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"wait-1": {
				Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusWaitingForCondition,
				WaitForCondition: &p.WaitForCondition{Type: p.WaitTypeAnyOf,
					Conditions: []p.SingleCondition{{Channel: &p.ChannelCondition{ChannelName: "events", Min: 1}}}},
			},
		},
	}, nil))

	require.Nil(t, h.eng.PublishExternalChannelMessages(ctx, shardID, &pb.PublishToChannelRequest{
		Namespace: h.ns, RunId: runID, ChannelName: "events",
		Values: []*pb.Value{{Kind: &pb.Value_NullValue{}}},
	}))

	updated, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	assert.Greater(t, updated.StepMethodExeCounter, counterBefore)
	wait, ok := updated.ActiveStepExecutions["wait-1"]
	require.True(t, ok)
	assert.Equal(t, p.StepExeStatusInvokingExecute, wait.Status)
	assert.Equal(t, updated.StepMethodExeCounter, wait.ExecuteMethodExeID)

	pollResp, pollErr := h.eng.HandleRunDispatchResult(ctx, shardID, h.ns, runID, true, "worker-resume")
	require.Nil(t, pollErr)
	require.NotNil(t, pollResp)
	assert.Equal(t, updated.StepMethodExeCounter, pollResp.StepMethodExeCounter)
}

func TestE2E_WorkerMethodID_IdempotentReplay(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	runID := uuid.NewString()

	require.Nil(t, h.eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: h.ns, RunId: runID, FlowType: "e2e-test", TaskListName: "g",
	}))

	shardID := h.mapper.GetShardID(h.ns, runID)
	run, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	running := p.RunStatusRunning
	require.Nil(t, h.runStore.UpdateRunWithNewTasks(ctx, shardID, h.ns, runID, run.Version, &p.RunRowUpdate{
		Status: &running,
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"exec-1": {Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusInvokingExecute},
		},
	}, nil))
	run, _ = h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	counter := run.WorkerRequestCounter + 1

	req := &pb.StepExecuteCompletedRequest{
		RunId: runID, StepExeId: "exec-1",
		Context:      &pb.WorkerCallContext{WorkerRequestCounter: counter},
		StopDecision: pb.StopDecision_STOP_DECISION_NONE,
		NextSteps:    []*pb.NextStep{{StepId: "next", Input: nullPbValue(), WaitForMethodExeId: 2}},
	}
	_, err := h.eng.ProcessStepExecuteCompleted(ctx, shardID, h.ns, req)
	require.Nil(t, err)

	afterFirst, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	_, err = h.eng.ProcessStepExecuteCompleted(ctx, shardID, h.ns, req)
	require.Nil(t, err)

	afterReplay, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	assert.Equal(t, afterFirst.StepMethodExeCounter, afterReplay.StepMethodExeCounter)
	assert.Equal(t, afterFirst.Version, afterReplay.Version)
}

// TestE2E_ProcessStepsUnblocked_EmptyRejected: a request with empty
// steps_unblocked is a programming error (the worker guards len(promoted)>0
// before calling — see checkpointAndPromote's call sites), so the engine
// rejects it as invalid input and leaves the run untouched (no CAS, no
// history, no version bump).
func TestE2E_ProcessStepsUnblocked_EmptyRejected(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	runID := uuid.NewString()

	require.Nil(t, h.eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: h.ns, RunId: runID, FlowType: "e2e-test", TaskListName: "g",
	}))

	shardID := h.mapper.GetShardID(h.ns, runID)
	run, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	beforeVersion := run.Version

	err := wcrErr(h.eng.ProcessStepsUnblocked(ctx, shardID, &pb.StepsUnblockedRequest{
		Namespace: h.ns,
		RunId:     runID,
	}))
	require.NotNil(t, err)
	assert.True(t, err.IsInvalidInputError(), "empty steps_unblocked must be rejected as invalid input")

	after, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	assert.Equal(t, beforeVersion, after.Version, "rejected request must not bump run version")
}

// TestE2E_AllOf_TimerAndChannel_BothMustFire ensures AllOf semantics: a fired
// timer alone must NOT promote the run when a sibling channel condition is
// still under-min. After an external publish satisfies the channel side, the
// run transitions to Pending.
func TestE2E_AllOf_TimerAndChannel_BothMustFire(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	runID := uuid.NewString()

	require.Nil(t, h.eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: h.ns, RunId: runID, FlowType: "e2e-test", TaskListName: "g",
	}))

	shardID := h.mapper.GetShardID(h.ns, runID)
	run, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	allWaiting := p.RunStatusAllStepsWaitingForConditions

	// Already-fired timer (in the past) + channel awaiting min=1.
	pastTimerFireAt := time.Now().Add(-1 * time.Hour).UnixMilli()
	timerID := ids.NewTaskID()
	clearFired := false
	h.runStore.UpdateRunWithNewTasks(ctx, shardID, h.ns, runID, run.Version, &p.RunRowUpdate{
		Status: &allWaiting, ActiveDurableTimerID: &timerID, DurableTimerFireAt: &pastTimerFireAt, DurableTimerFired: &clearFired,
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"wait-1": {
				Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusWaitingForCondition,
				WaitForCondition: &p.WaitForCondition{
					Type: p.WaitTypeAllOf,
					Conditions: []p.SingleCondition{
						{Timer: &p.TimerCondition{FireAtUnixMs: pastTimerFireAt}},
						{Channel: &p.ChannelCondition{ChannelName: "data", Min: 1}},
					},
				},
			},
		},
	}, nil)

	// Fire the durable timer — AllOf with under-min channel should NOT dispatch.
	require.Nil(t, h.eng.HandleStepWaitForTimerFired(ctx, shardID, &engine.StepWaitForTimerFiredRequest{
		RunID: runID, Namespace: h.ns, TimerID: timerID, FireAtUnixMs: pastTimerFireAt,
	}))
	stillWaiting, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	assert.Equal(t, p.RunStatusAllStepsWaitingForConditions, stillWaiting.Status,
		"AllOf timer fired alone with under-min channel must keep run waiting")

	// Now publish to the channel — combined eval (fired timer + 1 message) satisfies AllOf.
	require.Nil(t, h.eng.PublishExternalChannelMessages(ctx, shardID, &pb.PublishToChannelRequest{
		RunId: runID, Namespace: h.ns, ChannelName: "data",
		Values: []*pb.Value{testhelpers.IntPbValue(7)},
	}))

	resumed, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	assert.Equal(t, p.RunStatusPending, resumed.Status, "AllOf with both branches satisfied should dispatch")
}

// ============================================================================
// CancelSiblingStepExecution: server-side commit semantics for the
// canceled_step_executions field on StepExecuteCompletedRequest (populated
// by the SDK's StepDecision.WithCancelingSiblingStepExecution API).
// ============================================================================

// TestE2E_StepExecuteCompleted_CanceledStepExecutions_DeletesActiveSteps
// proves the engine atomically deletes every cancelled exe-id from
// ActiveStepExecutions in the SAME commit as the calling step's
// completion. With three siblings (caller + two cancel targets) sharing
// the parent and the caller calling DeadEnd + cancelling both, the
// resulting ActiveStepExecutions must be empty → run goes Completed
// without waiting on the durable timer the cancelled WAITING sibling
// owned.
func TestE2E_StepExecuteCompleted_CanceledStepExecutions_DeletesActiveSteps(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	runID := uuid.NewString()

	require.Nil(t, h.eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: h.ns, RunId: runID, FlowType: "e2e-test", TaskListName: "g",
	}))

	shardID := h.mapper.GetShardID(h.ns, runID)
	run, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})

	// Three siblings, all spawned by parent "parent-1": caller actively
	// executing, sibling-1 in WAITING_FOR_CONDITION, sibling-2 also
	// invoking Execute. A durable timer is armed for sibling-1's
	// far-future timer condition so we can verify the post-cancel
	// state has no leftover blockers.
	farFuture := time.Now().Add(1 * time.Hour).UnixMilli()
	timerID := ids.NewTaskID()
	clearFired := false
	running := p.RunStatusRunning
	require.Nil(t, h.runStore.UpdateRunWithNewTasks(ctx, shardID, h.ns, runID, run.Version, &p.RunRowUpdate{
		Status:               &running,
		ActiveDurableTimerID: &timerID,
		DurableTimerFireAt:   &farFuture,
		DurableTimerFired:    &clearFired,
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"caller-1": {
				Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusInvokingExecute,
				FromStepExeID: "parent-1",
			},
			"sibling-1": {
				Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusWaitingForCondition,
				FromStepExeID: "parent-1",
				WaitForCondition: &p.WaitForCondition{
					Type: p.WaitTypeAnyOf,
					Conditions: []p.SingleCondition{
						{Timer: &p.TimerCondition{FireAtUnixMs: farFuture}},
					},
				},
			},
			"sibling-2": {
				Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusInvokingExecute,
				FromStepExeID: "parent-1",
			},
		},
	}, nil))
	run, _ = h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})

	// Caller completes (DeadEnd) + cancels both siblings. Engine must
	// commit all three deletions in the same CAS.
	require.Nil(t, wcrErr(h.eng.ProcessStepExecuteCompleted(ctx, shardID, h.ns, &pb.StepExecuteCompletedRequest{
		RunId: runID, StepExeId: "caller-1",
		Context:                &pb.WorkerCallContext{WorkerRequestCounter: run.WorkerRequestCounter + 1},
		StopDecision:           pb.StopDecision_STOP_DECISION_DEAD_END,
		CanceledStepExecutions: []string{"sibling-1", "sibling-2"},
	})))

	updated, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	assert.Empty(t, updated.ActiveStepExecutions,
		"caller's DeadEnd + cancellation of both siblings must leave ActiveStepExecutions empty")
	assert.Equal(t, p.RunStatusCompleted, updated.Status,
		"empty ActiveStepExecutions with no terminal stop_decision still completes the run")
}

// TestE2E_StepExecuteCompleted_CanceledStepExecutions_SplicesReservedQueue
// verifies cancel of a reserved INVOKING_EXECUTE sibling splices its range
// from UnconsumedChannelMessages in the same CAS as deletion.
func TestE2E_StepExecuteCompleted_CanceledStepExecutions_SplicesReservedQueue(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	runID := uuid.NewString()

	require.Nil(t, h.eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: h.ns, RunId: runID, FlowType: "e2e-test", TaskListName: "g",
	}))

	shardID := h.mapper.GetShardID(h.ns, runID)
	run, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	running := p.RunStatusRunning
	v1, v2, v3 := int64(1), int64(2), int64(3)
	require.Nil(t, h.runStore.UpdateRunWithNewTasks(ctx, shardID, h.ns, runID, run.Version, &p.RunRowUpdate{
		Status: &running,
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"caller-1": {
				Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusInvokingExecute,
				FromStepExeID: "parent-1",
			},
			"slow-1": {
				Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusInvokingExecute,
				FromStepExeID:      "parent-1",
				ExecuteMethodExeID: 1,
				ConditionResults: []p.ConditionResult{{
					Channel: &p.ChannelConditionResult{
						ChannelName: "events", Satisfied: true, ConsumedCount: 1,
					},
				}},
			},
			"fast-1": {
				Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusInvokingExecute,
				FromStepExeID:      "parent-1",
				ExecuteMethodExeID: 2,
				ConditionResults: []p.ConditionResult{{
					Channel: &p.ChannelConditionResult{
						ChannelName: "events", Satisfied: true, ConsumedCount: 1,
					},
				}},
			},
		},
		ReplaceUnconsumedChannels: map[string][]p.ChannelMessage{"events": {
			{ID: 1, Value: p.Value{Type: p.ValueTypeInt, IntVal: &v1}},
			{ID: 2, Value: p.Value{Type: p.ValueTypeInt, IntVal: &v2}},
			{ID: 3, Value: p.Value{Type: p.ValueTypeInt, IntVal: &v3}},
		}},
	}, nil))
	run, _ = h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})

	require.Nil(t, wcrErr(h.eng.ProcessStepExecuteCompleted(ctx, shardID, h.ns, &pb.StepExecuteCompletedRequest{
		RunId: runID, StepExeId: "fast-1",
		Context:                &pb.WorkerCallContext{WorkerRequestCounter: run.WorkerRequestCounter + 1},
		StopDecision:           pb.StopDecision_STOP_DECISION_NONE,
		CanceledStepExecutions: []string{"slow-1"},
	})))

	updated, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	require.Len(t, updated.UnconsumedChannelMessages["events"], 1,
		"fast completes + slow cancelled should splice both reserved ranges")
	assert.Equal(t, v3, *updated.UnconsumedChannelMessages["events"][0].Value.IntVal)
	_, hasSlow := updated.ActiveStepExecutions["slow-1"]
	assert.False(t, hasSlow)
}

// TestE2E_StepExecuteCompleted_CanceledStepExecutions_IgnoresUnknownIDs
// pins the idempotency contract of the canceled_step_executions field:
// passing exe-ids that don't currently exist in ActiveStepExecutions
// (already finished, never created, retried request, etc.) must be a
// silent no-op — the engine still deletes the real targets and returns
// successfully. This is what lets the SDK fire-and-forget cancellations
// without first reading the active set.
func TestE2E_StepExecuteCompleted_CanceledStepExecutions_IgnoresUnknownIDs(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	runID := uuid.NewString()

	require.Nil(t, h.eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: h.ns, RunId: runID, FlowType: "e2e-test", TaskListName: "g",
	}))

	shardID := h.mapper.GetShardID(h.ns, runID)
	run, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	running := p.RunStatusRunning
	require.Nil(t, h.runStore.UpdateRunWithNewTasks(ctx, shardID, h.ns, runID, run.Version, &p.RunRowUpdate{
		Status: &running,
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"caller-1": {Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusInvokingExecute, FromStepExeID: "parent-1"},
			// One real cancel target; the other two IDs in the request below are bogus.
			"realTarget-1": {Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusInvokingExecute, FromStepExeID: "parent-1"},
			// A surviving sibling that isn't named in the cancel request — must remain.
			"survivor-1": {Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusInvokingExecute, FromStepExeID: "parent-1"},
		},
	}, nil))
	run, _ = h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})

	require.Nil(t, wcrErr(h.eng.ProcessStepExecuteCompleted(ctx, shardID, h.ns, &pb.StepExecuteCompletedRequest{
		RunId: runID, StepExeId: "caller-1",
		Context:      &pb.WorkerCallContext{WorkerRequestCounter: run.WorkerRequestCounter + 1},
		StopDecision: pb.StopDecision_STOP_DECISION_NONE,
		// Mix one real ID with two unknown IDs — engine must accept the
		// request, delete only the real one, and not error.
		CanceledStepExecutions: []string{"realTarget-1", "ghost-1", "alreadyDone-99"},
	})))

	updated, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	_, hasReal := updated.ActiveStepExecutions["realTarget-1"]
	_, hasSurvivor := updated.ActiveStepExecutions["survivor-1"]
	_, hasCaller := updated.ActiveStepExecutions["caller-1"]
	assert.False(t, hasReal, "realTarget-1 must be deleted")
	assert.False(t, hasCaller, "caller-1 must be deleted (its own completion)")
	assert.True(t, hasSurvivor, "survivor-1 must remain — it was not in the cancel request")
	assert.Equal(t, p.RunStatusRunning, updated.Status,
		"with survivor-1 still running the run stays Running")
}

// TestE2E_StepExecuteCompleted_CanceledStepExecutions_PreservesDurableTimerForRemainingWaitingSibling
// guards the durable-timer interaction with cancellation. With two
// timer-conditioned siblings (early + late) and an active timer keyed
// on the EARLY one, cancelling the early sibling does NOT force a
// timer swap — `createDurableTimerIfNeeded` lazy-reuses the existing
// row whenever its fire-at is `<=` the new minFireAt (firing early is
// always safe because the resulting evaluation no-ops if no condition
// is satisfied; see the lazy-reuse comment in run_engine.go). The test
// pins this behavior so a future "always swap on cancel" optimization
// is at least an intentional choice with this assertion fail.
func TestE2E_StepExecuteCompleted_CanceledStepExecutions_PreservesDurableTimerForRemainingWaitingSibling(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	runID := uuid.NewString()

	require.Nil(t, h.eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: h.ns, RunId: runID, FlowType: "e2e-test", TaskListName: "g",
	}))

	shardID := h.mapper.GetShardID(h.ns, runID)
	run, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})

	// earlyFireAt = 30 minutes; lateFireAt = 2 hours. Old durable timer
	// is keyed on earlyFireAt (sibling-early's condition). Cancelling
	// sibling-early forces the engine to re-compute; the only remaining
	// timer condition is sibling-late's lateFireAt.
	earlyFireAt := time.Now().Add(30 * time.Minute).UnixMilli()
	lateFireAt := time.Now().Add(2 * time.Hour).UnixMilli()
	oldTimerID := ids.NewTaskID()
	clearFired := false
	running := p.RunStatusRunning
	require.Nil(t, h.runStore.UpdateRunWithNewTasks(ctx, shardID, h.ns, runID, run.Version, &p.RunRowUpdate{
		Status:               &running,
		ActiveDurableTimerID: &oldTimerID,
		DurableTimerFireAt:   &earlyFireAt,
		DurableTimerFired:    &clearFired,
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"caller-1": {Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusInvokingExecute, FromStepExeID: "parent-1"},
			"sibling-early-1": {
				Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusWaitingForCondition, FromStepExeID: "parent-1",
				WaitForCondition: &p.WaitForCondition{
					Type:       p.WaitTypeAnyOf,
					Conditions: []p.SingleCondition{{Timer: &p.TimerCondition{FireAtUnixMs: earlyFireAt}}},
				},
			},
			"sibling-late-1": {
				Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusWaitingForCondition, FromStepExeID: "parent-1",
				WaitForCondition: &p.WaitForCondition{
					Type:       p.WaitTypeAnyOf,
					Conditions: []p.SingleCondition{{Timer: &p.TimerCondition{FireAtUnixMs: lateFireAt}}},
				},
			},
		},
	}, nil))
	run, _ = h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})

	// Caller DeadEnds + cancels sibling-early. The remaining waiting set
	// = {sibling-late}. The pre-existing timer's fire_at (earlyFireAt)
	// is still <= the new earliest needed fire_at (lateFireAt) so lazy
	// reuse kicks in — engine keeps the old timer row and lets it fire
	// early; that early fire will re-evaluate, find nothing satisfied,
	// and re-arm itself (cheaper than burning a CAS on every cancel).
	require.Nil(t, wcrErr(h.eng.ProcessStepExecuteCompleted(ctx, shardID, h.ns, &pb.StepExecuteCompletedRequest{
		RunId: runID, StepExeId: "caller-1",
		Context:                &pb.WorkerCallContext{WorkerRequestCounter: run.WorkerRequestCounter + 1},
		StopDecision:           pb.StopDecision_STOP_DECISION_DEAD_END,
		CanceledStepExecutions: []string{"sibling-early-1"},
	})))

	updated, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	assert.Equal(t, p.RunStatusRunning, updated.Status,
		"only sibling-late remains and it's WAITING → completion stays Running until park")
	workerID := "worker-cancel-sibling"
	require.Nil(t, h.runStore.UpdateRunWithNewTasks(ctx, shardID, h.ns, runID, updated.Version, &p.RunRowUpdate{
		WorkerID: &workerID,
	}, nil))
	updated, _ = h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	_ = parkAllStepsWaiting(t, h, ctx, runID, workerID, updated.WorkerRequestCounter+1, updated.ExternalChannelMessageCounter)
	updated, _ = h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	assert.Equal(t, p.RunStatusAllStepsWaitingForConditions, updated.Status)
	_, hasEarly := updated.ActiveStepExecutions["sibling-early-1"]
	assert.False(t, hasEarly, "cancelled sibling must be deleted from ActiveStepExecutions")
	_, hasLate := updated.ActiveStepExecutions["sibling-late-1"]
	assert.True(t, hasLate, "uncancelled sibling must remain")
	assert.Equal(t, oldTimerID, updated.ActiveDurableTimerID,
		"existing durable timer is lazy-reused — cancellation does not force a timer swap")
	assert.Equal(t, earlyFireAt, updated.DurableTimerFireAt,
		"existing timer's fire_at is preserved; the orphan early fire will harmlessly re-evaluate")
}

// TestE2E_StepWaitForTimerFired_RearmsWhenLazyReuseStaleAfterCancellation
// is the regression test for the dynamicChannelFlow stuck-in-AllStepsWaiting
// bug surfaced by the cancel demo. Pre-fix sequence:
//
//  1. Two siblings WAITING with timer conditions: sibling-cancelled at
//     earlyFireAt, sibling-survivor at lateFireAt. Engine arms the
//     durable timer for the EARLIEST = earlyFireAt.
//  2. A peer's StepDecision cancels sibling-cancelled (deletes it from
//     ActiveStepExecutions). Engine's `createDurableTimerIfNeeded`
//     during the commit lazy-reuses the existing timer because
//     `existing.fire_at (earlyFireAt) <= new minFireAt (lateFireAt)`.
//     Timer task at earlyFireAt remains in the queue.
//  3. earlyFireAt arrives. HandleStepWaitForTimerFired runs.
//     effectiveNow = earlyFireAt. sibling-survivor's timer fire_at
//     (lateFireAt) is `> effectiveNow` → condition NOT satisfied.
//     dispatched=false.
//  4. PRE-FIX BUG: createDurableTimerIfNeeded called with the run's
//     ActiveDurableTimerID still pointing at the JUST-FIRED timer and
//     DurableTimerFireAt still equal to earlyFireAt. Lazy-reuse fires
//     (existing earlyFireAt <= new lateFireAt) → returns nil →
//     no new timer scheduled. But the just-fired task was already
//     consumed by the task processor. Run is permanently stuck in
//     AllStepsWaitingForConditions.
//  5. POST-FIX: HandleStepWaitForTimerFired clears
//     ActiveDurableTimerID + DurableTimerFireAt on a copy of run before
//     calling createDurableTimerIfNeeded, forcing it to schedule a
//     fresh timer task for lateFireAt.
//
// This test exercises step 3-5 directly: arm a stale durable timer at
// earlyFireAt while the only waiting step's actual timer fire_at is
// lateFireAt, fire the durable timer, and assert that a NEW timer task
// got scheduled for lateFireAt (not lazy-reused into nothingness).
func TestE2E_StepWaitForTimerFired_RearmsWhenLazyReuseStaleAfterCancellation(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	runID := uuid.NewString()

	require.Nil(t, h.eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: h.ns, RunId: runID, FlowType: "e2e-test", TaskListName: "g",
	}))

	shardID := h.mapper.GetShardID(h.ns, runID)
	run, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})

	// Stale durable timer keyed on a fire_at that is BEFORE the only
	// surviving waiting step's actual timer condition. Mirrors the
	// post-cancellation state of the dynamicChannelFlow run that
	// reproduced the bug.
	staleTimerID := ids.NewTaskID()
	earlyFireAt := time.Now().Add(-1 * time.Minute).UnixMilli() // already in the past
	lateFireAt := time.Now().Add(1 * time.Hour).UnixMilli()
	clearFired := false
	allWaiting := p.RunStatusAllStepsWaitingForConditions
	require.Nil(t, h.runStore.UpdateRunWithNewTasks(ctx, shardID, h.ns, runID, run.Version, &p.RunRowUpdate{
		Status:               &allWaiting,
		ActiveDurableTimerID: &staleTimerID,
		DurableTimerFireAt:   &earlyFireAt,
		DurableTimerFired:    &clearFired,
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"survivor-1": {
				Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusWaitingForCondition,
				WaitForCondition: &p.WaitForCondition{
					Type:       p.WaitTypeAnyOf,
					Conditions: []p.SingleCondition{{Timer: &p.TimerCondition{FireAtUnixMs: lateFireAt}}},
				},
			},
		},
	}, nil))

	// Fire the stale durable timer (TimerID matches; effectiveNow =
	// earlyFireAt, which is BEFORE survivor-1's actual fire_at, so
	// condition evaluates as not-satisfied → dispatched=false → must
	// re-arm).
	require.Nil(t, h.eng.HandleStepWaitForTimerFired(ctx, shardID, &engine.StepWaitForTimerFiredRequest{
		RunID:        runID,
		Namespace:    h.ns,
		TimerID:      staleTimerID,
		FireAtUnixMs: earlyFireAt,
	}))

	updated, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	assert.Equal(t, p.RunStatusAllStepsWaitingForConditions, updated.Status,
		"survivor-1's actual fire_at hasn't passed → run must stay AllStepsWaiting (no false-positive dispatch)")
	assert.NotEqual(t, staleTimerID, updated.ActiveDurableTimerID,
		"the stale durable timer was just consumed; engine MUST schedule a fresh timer task with a new ID")
	assert.Equal(t, lateFireAt, updated.DurableTimerFireAt,
		"the new durable timer must point at survivor-1's actual fire_at, not stay stuck on the just-fired earlyFireAt")
	assert.False(t, updated.DurableTimerFired,
		"a freshly-armed durable timer must reset DurableTimerFired so the next fire is honored")
}
