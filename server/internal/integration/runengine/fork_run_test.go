package runengine

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/server/internal/integration/testhelpers"
	p "github.com/superdurable/dex/server/internal/persistence"
)

func intPbValue(v int64) *pb.Value {
	return &pb.Value{Kind: &pb.Value_IntValue{IntValue: v}}
}

func TestE2E_ForkRun_ToWaitForRestoresSnapshot(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	runID := uuid.NewString()

	require.Nil(t, h.eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: h.ns, RunId: runID, FlowType: "fork-test", TaskListName: "g",
	}))

	shardID := h.mapper.GetShardID(h.ns, runID)
	run, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	running := p.RunStatusRunning
	require.Nil(t, h.runStore.UpdateRunWithNewTasks(ctx, shardID, h.ns, runID, run.Version, &p.RunRowUpdate{
		Status: &running,
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"wait-1": {Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusInvokingWaitFor},
		},
	}, nil))

	run, _ = h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	fireAt := time.Now().Add(1 * time.Hour).UnixMilli()
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

	events, err := h.historyStore.GetHistoryEvents(ctx, h.ns, runID, 0, 50)
	require.Nil(t, err)
	var waitEventID int64
	for _, event := range events {
		if event.Payload.StepWaitForCompleted != nil {
			waitEventID = event.EventID
			require.NotNil(t, event.Payload.StepWaitForCompleted.Snapshot)
		}
	}
	require.NotZero(t, waitEventID)

	run, _ = h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	workerID := "worker-fork-1"
	require.Nil(t, h.runStore.UpdateRunWithNewTasks(ctx, shardID, h.ns, runID, run.Version, &p.RunRowUpdate{
		WorkerID: &workerID,
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"wait-1": {
				Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusInvokingExecute,
				ExecuteMethodExeID: 1,
			},
		},
	}, nil))

	run, _ = h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	require.Nil(t, wcrErr(h.eng.ProcessStepExecuteCompleted(ctx, shardID, h.ns, &pb.StepExecuteCompletedRequest{
		RunId: runID, StepExeId: "wait-1",
		Context:      &pb.WorkerCallContext{WorkerRequestCounter: run.WorkerRequestCounter + 1},
		StopDecision: pb.StopDecision_STOP_DECISION_DEAD_END,
		StateToUpsert: map[string]*pb.Value{
			"fork_key": intPbValue(99),
		},
	})))

	run, _ = h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	counterBefore := run.WorkerRequestCounter

	_, forkErr := h.eng.ForkRun(ctx, &pb.ForkRunRequest{
		Namespace: h.ns, RunId: runID, ToEventId: waitEventID, Reason: "rewind",
	})
	require.Nil(t, forkErr)

	restored, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	assert.Empty(t, restored.WorkerID)
	assert.Equal(t, counterBefore+1000, restored.WorkerRequestCounter)
	_, hasForkKey := restored.StateMap["fork_key"]
	assert.False(t, hasForkKey, "state written after fork point must be cleared")
	step, ok := restored.ActiveStepExecutions["wait-1"]
	require.True(t, ok)
	assert.Equal(t, p.StepExeStatusWaitingForCondition, step.Status)

	allEvents, _ := h.historyStore.GetHistoryEvents(ctx, h.ns, runID, 0, 50)
	var sawFork bool
	for _, event := range allEvents {
		if event.Payload.RunFork != nil {
			sawFork = true
			assert.Equal(t, waitEventID, event.Payload.RunFork.ForkToEventId)
		}
	}
	assert.True(t, sawFork)
}

func TestE2E_ForkRun_ToRunStart_RespawnsStartingSteps(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	runID := uuid.NewString()

	require.Nil(t, h.eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace:    h.ns,
		RunId:        runID,
		FlowType:     "fork-test",
		TaskListName: "g",
		StartingSteps: []*pb.NextStep{
			{StepId: "alpha", Input: testhelpers.NullPbValue()},
		},
	}))

	shardID := h.mapper.GetShardID(h.ns, runID)
	run, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	running := p.RunStatusRunning
	v := int64(7)
	require.Nil(t, h.runStore.UpdateRunWithNewTasks(ctx, shardID, h.ns, runID, run.Version, &p.RunRowUpdate{
		Status:   &running,
		StateMap: map[string]p.Value{"stale": {Type: p.ValueTypeInt, IntVal: &v}},
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"alpha-1": {Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusInvokingExecute},
		},
		StepExeIDCounters: map[string]int32{"alpha": 1},
	}, nil))

	_, forkErr := h.eng.ForkRun(ctx, &pb.ForkRunRequest{
		Namespace: h.ns, RunId: runID, ToEventId: 1,
	})
	require.Nil(t, forkErr)

	restored, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	_, hasStale := restored.StateMap["stale"]
	assert.False(t, hasStale)
	_, hasAlpha := restored.ActiveStepExecutions["alpha-1"]
	assert.True(t, hasAlpha)
	assert.Equal(t, p.RunStatusPending, restored.Status)

	tasks, _ := h.runStore.RangeReadImmediateTasks(ctx, shardID, 0, 20)
	foundResume := false
	for _, task := range tasks {
		if task.TaskInfo.RunID == runID && task.TaskType == p.ImmediateTaskRunResumeDispatch {
			foundResume = true
		}
	}
	assert.True(t, foundResume)
}

func TestE2E_ForkRun_RejectsInvalidTargets(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	runID := uuid.NewString()

	require.Nil(t, h.eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: h.ns, RunId: runID, FlowType: "fork-test", TaskListName: "g",
	}))

	shardID := h.mapper.GetShardID(h.ns, runID)
	run, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	completed := p.RunStatusCompleted
	require.Nil(t, h.runStore.UpdateRunWithNewTasks(ctx, shardID, h.ns, runID, run.Version, &p.RunRowUpdate{
		Status: &completed,
	}, nil))
	run, _ = h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	require.Nil(t, h.historyStore.BatchInsertHistory(ctx, []p.HistoryEvent{{
		RunID:        runID,
		EventID:      run.LastHistoryEventID + 1,
		Namespace:    h.ns,
		OccurredAtMs: time.Now().UnixMilli(),
		Payload:      p.HistoryEventPayload{RunStop: &pb.HistoryRunStopPayload{RunStatus: int32(p.RunStatusCompleted)}},
	}}))

	_, err := h.eng.ForkRun(ctx, &pb.ForkRunRequest{Namespace: h.ns, RunId: runID, ToEventId: 2})
	require.NotNil(t, err)
	assert.True(t, err.IsInvalidInputError())

	_, err = h.eng.ForkRun(ctx, &pb.ForkRunRequest{Namespace: h.ns, RunId: runID, ToEventId: 999})
	require.NotNil(t, err)
	assert.True(t, err.IsInvalidInputError())
}

func TestE2E_ForkRun_PastTimerSetsDurableTimerFired(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	runID := uuid.NewString()

	require.Nil(t, h.eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: h.ns, RunId: runID, FlowType: "fork-test", TaskListName: "g",
	}))

	shardID := h.mapper.GetShardID(h.ns, runID)
	run, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	running := p.RunStatusRunning
	require.Nil(t, h.runStore.UpdateRunWithNewTasks(ctx, shardID, h.ns, runID, run.Version, &p.RunRowUpdate{
		Status: &running,
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"wait-1": {Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusInvokingWaitFor},
		},
	}, nil))

	run, _ = h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	pastFireAt := time.Now().Add(-1 * time.Hour).UnixMilli()
	require.Nil(t, wcrErr(h.eng.ProcessStepWaitForCompleted(ctx, shardID, h.ns, &pb.StepWaitForCompletedRequest{
		RunId: runID, StepExeId: "wait-1",
		Context: &pb.WorkerCallContext{WorkerRequestCounter: run.WorkerRequestCounter + 1},
		WaitForCondition: &pb.WaitForCondition{
			Type: pb.WaitType_WAIT_TYPE_ANY_OF,
			Conditions: []*pb.SingleCondition{
				{Condition: &pb.SingleCondition_Timer{Timer: &pb.TimerCondition{FireAtUnixMs: pastFireAt}}},
			},
		},
	})))

	events, _ := h.historyStore.GetHistoryEvents(ctx, h.ns, runID, 0, 20)
	var waitEventID int64
	for _, event := range events {
		if event.Payload.StepWaitForCompleted != nil {
			waitEventID = event.EventID
		}
	}
	require.NotZero(t, waitEventID)

	_, forkErr := h.eng.ForkRun(ctx, &pb.ForkRunRequest{
		Namespace: h.ns, RunId: runID, ToEventId: waitEventID,
	})
	require.Nil(t, forkErr)

	restored, _ := h.runStore.GetRun(ctx, shardID, h.ns, runID, p.GetRunOptions{})
	assert.True(t, restored.DurableTimerFired)
}
