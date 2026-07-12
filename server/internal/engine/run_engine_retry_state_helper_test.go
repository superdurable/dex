package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/server/common/utils/ids"
	p "github.com/superdurable/dex/server/internal/persistence"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProcessRecordHeartbeat_RejectsOversizedRetryState(t *testing.T) {
	eng, runStore, _ := getTestEngineWithBlobs(t)
	ctx := context.Background()
	runID := uuid.NewString()
	ns := "test-ns"

	require.Nil(t, eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: ns, RunId: runID, FlowType: "test", TaskListName: "g",
	}))

	shardID := testMapper().GetShardID(ns, runID)
	run, _ := runStore.GetRun(ctx, shardID, ns, runID, p.GetRunOptions{})
	running := p.RunStatusRunning
	workerID := "worker-1"
	require.Nil(t, runStore.UpdateRunWithNewTasks(ctx, shardID, ns, runID, run.Version, &p.RunRowUpdate{
		Status:   &running,
		WorkerID: &workerID,
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"retryStep-1": {
				Input:  p.Value{Type: p.ValueTypeNull},
				Status: p.StepExeStatusInvokingExecute,
			},
		},
	}, nil))

	run, _ = runStore.GetRun(ctx, shardID, ns, runID, p.GetRunOptions{})
	_, hbErr := eng.ProcessRecordHeartbeat(ctx, shardID, &pb.ProcessRecordHeartbeatRequest{
		Namespace: ns,
		RunId:     runID,
		Context: &pb.WorkerCallContext{
			WorkerId:             workerID,
			WorkerRequestCounter: run.WorkerRequestCounter + 1,
			ActiveStepRetryUpdates: map[string]*pb.StepRetryStateUpdate{
				"retryStep-1": {
					ExecuteRetryState: &pb.StepRetryState{
						FirstAttemptTimeMs: time.Now().UnixMilli(),
						CurrentAttempts:    1,
						LastError:          strings.Repeat("x", 5000),
					},
				},
			},
		},
	})
	require.NotNil(t, hbErr)
	assert.True(t, hbErr.IsInvalidInputError())
}

func TestProcessRecordHeartbeat_PersistsRetryState(t *testing.T) {
	eng, runStore, blobStore := getTestEngineWithBlobs(t)
	ctx := context.Background()
	runID := uuid.NewString()
	ns := "test-ns"

	require.Nil(t, eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: ns, RunId: runID, FlowType: "test", TaskListName: "g",
	}))

	shardID := testMapper().GetShardID(ns, runID)
	run, _ := runStore.GetRun(ctx, shardID, ns, runID, p.GetRunOptions{})
	running := p.RunStatusRunning
	workerID := "worker-1"
	require.Nil(t, runStore.UpdateRunWithNewTasks(ctx, shardID, ns, runID, run.Version, &p.RunRowUpdate{
		Status:   &running,
		WorkerID: &workerID,
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"retryStep-1": {
				Input:  p.Value{Type: p.ValueTypeNull},
				Status: p.StepExeStatusInvokingExecute,
			},
		},
	}, nil))

	run, _ = runStore.GetRun(ctx, shardID, ns, runID, p.GetRunOptions{})
	_, err := eng.ProcessRecordHeartbeat(ctx, shardID, &pb.ProcessRecordHeartbeatRequest{
		Namespace: ns,
		RunId:     runID,
		Context: &pb.WorkerCallContext{
			WorkerId:             workerID,
			WorkerRequestCounter: run.WorkerRequestCounter + 1,
			ActiveStepRetryUpdates: map[string]*pb.StepRetryStateUpdate{
				"retryStep-1": {
					ExecuteRetryState: &pb.StepRetryState{
						FirstAttemptTimeMs:  time.Now().UnixMilli(),
						CurrentAttempts:     2,
						LastError:           "attempt failed",
						LastErrorStackTrace: "goroutine 1 [running]:\nmain.main()",
					},
				},
			},
		},
	})
	require.Nil(t, err)

	updated, _ := runStore.GetRun(ctx, shardID, ns, runID, p.GetRunOptions{})
	require.NotNil(t, updated.ActiveStepExecutions["retryStep-1"].ExecuteRetryState)
	assert.Equal(t, int32(2), updated.ActiveStepExecutions["retryStep-1"].ExecuteRetryState.CurrentAttempts)
	assert.Equal(t, "attempt failed", updated.ActiveStepExecutions["retryStep-1"].ExecuteRetryState.LastError)
	assert.Equal(t, "goroutine 1 [running]:\nmain.main()", updated.ActiveStepExecutions["retryStep-1"].ExecuteRetryState.LastErrorStackTrace)

	blobs, getErr := blobStore.BatchGetBlobs(ctx, shardID, ns, runID, []ids.BlobID{})
	require.Nil(t, getErr)
	assert.Empty(t, blobs)
}

func TestWaitForCompleted_PreservesExecuteRetryState(t *testing.T) {
	eng, runStore, _ := getTestEngineWithBlobs(t)
	ctx := context.Background()
	runID := uuid.NewString()
	ns := "test-ns"

	require.Nil(t, eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: ns, RunId: runID, FlowType: "test", TaskListName: "g",
	}))

	shardID := testMapper().GetShardID(ns, runID)
	run, _ := runStore.GetRun(ctx, shardID, ns, runID, p.GetRunOptions{})
	running := p.RunStatusRunning
	executeRetry := &p.RetryState{
		FirstAttemptTime: time.Now(),
		CurrentAttempts:  1,
		LastError:        "prior execute failure",
	}
	require.Nil(t, runStore.UpdateRunWithNewTasks(ctx, shardID, ns, runID, run.Version, &p.RunRowUpdate{
		Status: &running,
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"step-1": {
				Input:             p.Value{Type: p.ValueTypeNull},
				Status:            p.StepExeStatusInvokingWaitFor,
				ExecuteRetryState: executeRetry,
			},
		},
	}, nil))

	run, _ = runStore.GetRun(ctx, shardID, ns, runID, p.GetRunOptions{})
	_, err := eng.ProcessStepWaitForCompleted(ctx, shardID, ns, &pb.StepWaitForCompletedRequest{
		Namespace: ns,
		RunId:     runID,
		StepExeId: "step-1",
		Context:   &pb.WorkerCallContext{WorkerRequestCounter: run.WorkerRequestCounter + 1},
		WaitForCondition: &pb.WaitForCondition{
			Type: pb.WaitType_WAIT_TYPE_ANY_OF,
			Conditions: []*pb.SingleCondition{
				{Condition: &pb.SingleCondition_Timer{
					Timer: &pb.TimerCondition{FireAtUnixMs: time.Now().Add(time.Hour).UnixMilli()},
				}},
			},
		},
	})
	require.Nil(t, err)

	updated, _ := runStore.GetRun(ctx, shardID, ns, runID, p.GetRunOptions{})
	step := updated.ActiveStepExecutions["step-1"]
	require.NotNil(t, step)
	assert.Equal(t, p.StepExeStatusWaitingForCondition, step.Status)
	require.NotNil(t, step.ExecuteRetryState)
	assert.Equal(t, int32(1), step.ExecuteRetryState.CurrentAttempts)
	assert.Nil(t, step.WaitForRetryState)
}

func TestProcessStepWaitForCompleted_MethodFailedAfterRetry(t *testing.T) {
	eng, runStore, _ := getTestEngineWithBlobs(t)
	ctx := context.Background()
	runID := uuid.NewString()
	ns := "test-ns"

	require.Nil(t, eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: ns, RunId: runID, FlowType: "test", TaskListName: "g",
	}))

	shardID := testMapper().GetShardID(ns, runID)
	run, _ := runStore.GetRun(ctx, shardID, ns, runID, p.GetRunOptions{})
	running := p.RunStatusRunning
	require.Nil(t, runStore.UpdateRunWithNewTasks(ctx, shardID, ns, runID, run.Version, &p.RunRowUpdate{
		Status: &running,
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"step-1": {
				Input:  p.Value{Type: p.ValueTypeNull},
				Status: p.StepExeStatusInvokingWaitFor,
			},
		},
	}, nil))

	run, _ = runStore.GetRun(ctx, shardID, ns, runID, p.GetRunOptions{})
	_, err := eng.ProcessStepWaitForCompleted(ctx, shardID, ns, &pb.StepWaitForCompletedRequest{
		Namespace: ns,
		RunId:     runID,
		StepExeId: "step-1",
		Context:   &pb.WorkerCallContext{WorkerRequestCounter: run.WorkerRequestCounter + 1},
		WaitForMethod: &pb.StepMethodReport{
			Outcome:         pb.StepMethodOutcome_STEP_METHOD_OUTCOME_FAILED,
			Error:           "waitfor exhausted",
			ErrorStackTrace: "stack",
			AttemptCount:    3,
		},
	})
	require.Nil(t, err)

	updated, _ := runStore.GetRun(ctx, shardID, ns, runID, p.GetRunOptions{})
	assert.Equal(t, p.RunStatusFailed, updated.Status)
	assert.NotContains(t, updated.ActiveStepExecutions, "step-1")
}

func TestProcessStepExecuteCompleted_MethodFailedProceedToNextStep(t *testing.T) {
	eng, runStore, _ := getTestEngineWithBlobs(t)
	ctx := context.Background()
	runID := uuid.NewString()
	ns := "test-ns"

	require.Nil(t, eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: ns, RunId: runID, FlowType: "test", TaskListName: "g",
	}))

	shardID := testMapper().GetShardID(ns, runID)
	run, _ := runStore.GetRun(ctx, shardID, ns, runID, p.GetRunOptions{})
	running := p.RunStatusRunning
	require.Nil(t, runStore.UpdateRunWithNewTasks(ctx, shardID, ns, runID, run.Version, &p.RunRowUpdate{
		Status: &running,
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"fail-1": {Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusInvokingExecute},
		},
	}, nil))

	run, _ = runStore.GetRun(ctx, shardID, ns, runID, p.GetRunOptions{})
	_, err := eng.ProcessStepExecuteCompleted(ctx, shardID, ns, &pb.StepExecuteCompletedRequest{
		Namespace:    ns,
		RunId:        runID,
		StepExeId:    "fail-1",
		Context:      &pb.WorkerCallContext{WorkerRequestCounter: run.WorkerRequestCounter + 1},
		StopDecision: pb.StopDecision_STOP_DECISION_NONE,
		ExecuteMethod: &pb.StepMethodReport{
			Outcome:      pb.StepMethodOutcome_STEP_METHOD_OUTCOME_FAILED,
			Error:        "exhausted",
			AttemptCount: 2,
		},
		NextSteps: []*pb.NextStep{{StepId: "handler", Input: nullPbValue(), SkipWaitFor: true}},
	})
	require.Nil(t, err)

	updated, _ := runStore.GetRun(ctx, shardID, ns, runID, p.GetRunOptions{})
	assert.Equal(t, p.RunStatusRunning, updated.Status)
	handler, ok := updated.ActiveStepExecutions["handler-1"]
	require.True(t, ok)
	assert.Equal(t, p.StepExeStatusInvokingExecute, handler.Status)
	assert.Equal(t, "fail-1", handler.FromStepExeID)
}

func TestProcessStepWaitForCompleted_MethodFailedProceedToNextStep(t *testing.T) {
	eng, runStore, _ := getTestEngineWithBlobs(t)
	ctx := context.Background()
	runID := uuid.NewString()
	ns := "test-ns"

	require.Nil(t, eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: ns, RunId: runID, FlowType: "test", TaskListName: "g",
	}))

	shardID := testMapper().GetShardID(ns, runID)
	run, _ := runStore.GetRun(ctx, shardID, ns, runID, p.GetRunOptions{})
	running := p.RunStatusRunning
	require.Nil(t, runStore.UpdateRunWithNewTasks(ctx, shardID, ns, runID, run.Version, &p.RunRowUpdate{
		Status: &running,
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"fail-1": {Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusInvokingWaitFor},
		},
	}, nil))

	run, _ = runStore.GetRun(ctx, shardID, ns, runID, p.GetRunOptions{})
	_, err := eng.ProcessStepWaitForCompleted(ctx, shardID, ns, &pb.StepWaitForCompletedRequest{
		Namespace: ns,
		RunId:     runID,
		StepExeId: "fail-1",
		Context:   &pb.WorkerCallContext{WorkerRequestCounter: run.WorkerRequestCounter + 1},
		WaitForMethod: &pb.StepMethodReport{
			Outcome:      pb.StepMethodOutcome_STEP_METHOD_OUTCOME_FAILED,
			Error:        "waitfor exhausted",
			AttemptCount: 2,
		},
		NextSteps: []*pb.NextStep{{StepId: "handler", Input: nullPbValue(), SkipWaitFor: true}},
	})
	require.Nil(t, err)

	updated, _ := runStore.GetRun(ctx, shardID, ns, runID, p.GetRunOptions{})
	assert.Equal(t, p.RunStatusRunning, updated.Status)
	handler, ok := updated.ActiveStepExecutions["handler-1"]
	require.True(t, ok)
	assert.Equal(t, p.StepExeStatusInvokingExecute, handler.Status)
	assert.Equal(t, "fail-1", handler.FromStepExeID)
}

func TestProcessRecordHeartbeat_ClearsWaitForRetryState(t *testing.T) {
	eng, runStore, _ := getTestEngineWithBlobs(t)
	ctx := context.Background()
	runID := uuid.NewString()
	ns := "test-ns"

	require.Nil(t, eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: ns, RunId: runID, FlowType: "test", TaskListName: "g",
	}))

	shardID := testMapper().GetShardID(ns, runID)
	run, _ := runStore.GetRun(ctx, shardID, ns, runID, p.GetRunOptions{})
	running := p.RunStatusRunning
	workerID := "worker-1"
	waitRetry := &p.RetryState{FirstAttemptTime: time.Now(), CurrentAttempts: 2, LastError: "oops"}
	require.Nil(t, runStore.UpdateRunWithNewTasks(ctx, shardID, ns, runID, run.Version, &p.RunRowUpdate{
		Status:   &running,
		WorkerID: &workerID,
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"step-1": {
				Input:             p.Value{Type: p.ValueTypeNull},
				Status:            p.StepExeStatusInvokingWaitFor,
				WaitForRetryState: waitRetry,
			},
		},
	}, nil))

	run, _ = runStore.GetRun(ctx, shardID, ns, runID, p.GetRunOptions{})
	_, err := eng.ProcessRecordHeartbeat(ctx, shardID, &pb.ProcessRecordHeartbeatRequest{
		Namespace: ns,
		RunId:     runID,
		Context: &pb.WorkerCallContext{
			WorkerId:             workerID,
			WorkerRequestCounter: run.WorkerRequestCounter + 1,
			ActiveStepRetryUpdates: map[string]*pb.StepRetryStateUpdate{
				"step-1": {ClearWaitForRetryState: true},
			},
		},
	})
	require.Nil(t, err)

	updated, _ := runStore.GetRun(ctx, shardID, ns, runID, p.GetRunOptions{})
	require.Nil(t, updated.ActiveStepExecutions["step-1"].WaitForRetryState)
}
