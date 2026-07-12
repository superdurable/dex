package dex

import (
	"strconv"
	"testing"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/sdk-go/dex/evaluate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReEvaluateWaitingSteps_PromotesAndClearsWaitingKey(t *testing.T) {
	re := &runExecutor{
		allActiveStepExecutions: map[string]*pb.ActiveStepExecution{
			"waiter-1": {
				Status: pb.StepExecutionStatus_STEP_EXECUTION_STATUS_WAITING_FOR_CONDITION,
				WaitForCondition: &pb.WaitForCondition{
					Type: pb.WaitType_WAIT_TYPE_ANY_OF,
					Conditions: []*pb.SingleCondition{{
						Condition: &pb.SingleCondition_Timer{Timer: &pb.TimerCondition{FireAtUnixMs: 1}},
					}},
				},
			},
		},
		keysOfWaitingStepTasks: map[string]bool{"waiter-1": true},
		timerMgr:               newTimerManager(100),
	}

	promoted, err := re.reEvaluateWaitingSteps()
	require.NoError(t, err)
	require.Len(t, promoted, 1)
	assert.Equal(t, "waiter-1", promoted[0].stepExeID)
	assert.NotContains(t, re.keysOfWaitingStepTasks, "waiter-1")
	assert.Equal(t, pb.StepExecutionStatus_STEP_EXECUTION_STATUS_INVOKING_EXECUTE, re.allActiveStepExecutions["waiter-1"].Status)
}

func TestHandleExecuteCompletion_CancelSiblingsSameParent(t *testing.T) {
	parent := "parent-1"
	siblingTask := &stepInvocationTask{stepExeId: "sibling-1", kind: stepTaskMethodKindExecute}

	re := &runExecutor{
		worker: &Worker{},
		allActiveStepExecutions: map[string]*pb.ActiveStepExecution{
			"caller-1":  {FromStepExeId: parent},
			"sibling-1": {FromStepExeId: parent},
			"otherParent": {FromStepExeId: "other-1"},
		},
		runningStepTasks: map[string]*stepInvocationTask{
			"sibling-1": siblingTask,
		},
		keysOfWaitingStepTasks: map[string]bool{"waiter-1": true},
		unconsumedChannels:     map[string][]*pb.Value{},
	}

	cancelIDs := resolveCancelTargets("caller-1", re.allActiveStepExecutions, cancelStepIDSet("sibling"))
	assert.Equal(t, []string{"sibling-1"}, cancelIDs)

	for _, stepExeID := range cancelIDs {
		delete(re.allActiveStepExecutions, stepExeID)
		delete(re.pendingStepTasks, stepExeID)
		delete(re.runningStepTasks, stepExeID)
		delete(re.keysOfWaitingStepTasks, stepExeID)
	}
	assert.NotContains(t, re.allActiveStepExecutions, "sibling-1")
	assert.NotContains(t, re.runningStepTasks, "sibling-1")
}

func TestHandleExecuteCompletion_SplicesOwnReservation(t *testing.T) {
	re := &runExecutor{
		allActiveStepExecutions: map[string]*pb.ActiveStepExecution{
			"consumer-1": {
				ExecuteMethodExeId: 1,
				ConditionResults: []*pb.ConditionResult{{
					Result: &pb.ConditionResult_Channel{
						Channel: &pb.ChannelConditionResult{
							ChannelName: "events", Satisfied: true, ConsumedCount: 1,
						},
					},
				}},
			},
		},
		unconsumedChannels: map[string][]*pb.Value{
			"events": {
				{Kind: &pb.Value_IntValue{IntValue: 1}},
				{Kind: &pb.Value_IntValue{IntValue: 2}},
			},
		},
	}

	evaluate.SpliceUnconsumed([]string{"consumer-1"}, re.allActiveStepExecutions, re.unconsumedChannels)
	require.Len(t, re.unconsumedChannels["events"], 1)
	assert.Equal(t, int64(2), re.unconsumedChannels["events"][0].GetIntValue())
}

// Regression for the data race where step goroutines read the shared
// allActiveStepExecutions map while the main loop mutated it. After the fix the
// runner reads task.activeStepExe (snapshotted by kickOffStepTask), so this is
// race-free. Run with -race to guard against regression.
func TestStepRunner_ReadsTaskSnapshotNotSharedMap_NoRace(t *testing.T) {
	re := &runExecutor{
		allActiveStepExecutions: map[string]*pb.ActiveStepExecution{
			"s-1": {
				Input:             &pb.Value{Kind: &pb.Value_IntValue{IntValue: 7}},
				FromStepExeId:     "p-1",
				ExecuteRetryState: &pb.StepRetryState{CurrentAttempts: 1},
			},
		},
	}
	task := &stepInvocationTask{stepExeId: "s-1", kind: stepTaskMethodKindExecute}
	// Mirror kickOffStepTask's snapshot without launching the real goroutine.
	task.activeStepExe = re.allActiveStepExecutions["s-1"]
	smr := &stepMethodRunner{executor: re, task: task}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 2000; i++ {
			key := "x-" + strconv.Itoa(i)
			re.allActiveStepExecutions[key] = &pb.ActiveStepExecution{}
			delete(re.allActiveStepExecutions, key)
		}
	}()
	for i := 0; i < 2000; i++ {
		_ = smr.initRetryStateFromActive()
		require.NotNil(t, smr.task.activeStepExe.Input)
	}
	<-done
}

// Out-of-order message ids within one event must all be buffered if newer than
// lastSeen: dedup compares against the pre-loop snapshot, not a running max.
func TestApplyExternalEvent_OutOfOrderIDs_AllNewBuffered(t *testing.T) {
	re := &runExecutor{unconsumedChannels: map[string][]*pb.Value{}}
	re.lastSeenExtMsgID.Store(2)

	event := &pb.ExternalEvent{
		Event: &pb.ExternalEvent_ChannelMessagesReceived{
			ChannelMessagesReceived: &pb.ChannelMessagesReceived{
				ChannelName: "ch",
				Messages: []*pb.ChannelMessage{
					{Id: 5, Value: &pb.Value{Kind: &pb.Value_IntValue{IntValue: 5}}},
					{Id: 3, Value: &pb.Value{Kind: &pb.Value_IntValue{IntValue: 3}}},
					{Id: 4, Value: &pb.Value{Kind: &pb.Value_IntValue{IntValue: 4}}},
				},
			},
		},
	}
	changed, stop := re.applyExternalEvent(event)
	require.True(t, changed)
	require.False(t, stop)
	require.Len(t, re.unconsumedChannels["ch"], 3)
	assert.Equal(t, int64(5), re.lastSeenExtMsgID.Load())
}

func TestRunStateSnapshotAndMergeState(t *testing.T) {
	re := &runExecutor{}
	re.initRunStateFromPoll(map[string]*pb.Value{
		"count": {Kind: &pb.Value_IntValue{IntValue: 1}},
	})

	snapshot := re.getStateSnapshot()
	assert.Equal(t, int64(1), snapshot["count"].GetIntValue())

	snapshot["count"] = &pb.Value{Kind: &pb.Value_IntValue{IntValue: 99}}
	current := re.state.Load()
	require.NotNil(t, current)
	assert.Equal(t, int64(1), (*current)["count"].GetIntValue())

	re.mergeStateUpsert(map[string]*pb.Value{
		"count": {Kind: &pb.Value_IntValue{IntValue: 2}},
	})
	current = re.state.Load()
	require.NotNil(t, current)
	assert.Equal(t, int64(2), (*current)["count"].GetIntValue())
}
