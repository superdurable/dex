package dex

import (
	"context"
	"testing"
	"time"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestRunExecutor() *runExecutor {
	return &runExecutor{
		worker:           &Worker{},
		retryStateSyncCh: make(chan retrySyncEvent, 128),
	}
}

func TestCollectRetryUpdates_DrainsChannel(t *testing.T) {
	executor := newTestRunExecutor()
	state := &pb.StepRetryState{CurrentAttempts: 1, FirstAttemptTimeMs: 100}

	assert.Nil(t, executor.collectRetryUpdates(nil))

	executor.enqueueRetryStateSyncChannel(context.Background(), "step-1", stepTaskMethodKindExecute, state)
	updates := executor.collectRetryUpdates(nil)
	require.NotNil(t, updates)
	require.NotNil(t, updates["step-1"].ExecuteRetryState)

	assert.Nil(t, executor.collectRetryUpdates(nil))
}

func TestCollectRetryUpdates_SecondCollectAfterReEnqueue(t *testing.T) {
	executor := newTestRunExecutor()
	first := &pb.StepRetryState{CurrentAttempts: 1, FirstAttemptTimeMs: 100}
	second := &pb.StepRetryState{CurrentAttempts: 2, FirstAttemptTimeMs: 100}

	executor.enqueueRetryStateSyncChannel(context.Background(), "step-1", stepTaskMethodKindExecute, first)
	firstUpdates := executor.collectRetryUpdates(nil)
	require.NotNil(t, firstUpdates)
	assert.Equal(t, int32(1), firstUpdates["step-1"].ExecuteRetryState.CurrentAttempts)

	executor.enqueueRetryStateSyncChannel(context.Background(), "step-1", stepTaskMethodKindExecute, second)
	secondUpdates := executor.collectRetryUpdates(nil)
	require.NotNil(t, secondUpdates)
	assert.Equal(t, int32(2), secondUpdates["step-1"].ExecuteRetryState.CurrentAttempts)
}

// Regression: a full retryStateSyncCh must not block the step goroutine on a
// canceled task ctx — otherwise cancelInFlightStepTasks (run-loop exit) deadlocks
// waiting for a completion the blocked goroutine can never send.
func TestEnqueueRetryStateSync_CanceledCtxDoesNotBlockWhenFull(t *testing.T) {
	executor := &runExecutor{
		worker:           &Worker{},
		retryStateSyncCh: make(chan retrySyncEvent, 1),
	}
	executor.retryStateSyncCh <- retrySyncEvent{} // fill to capacity

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		executor.enqueueRetryStateSyncChannel(ctx, "step-1", stepTaskMethodKindExecute,
			&pb.StepRetryState{CurrentAttempts: 1})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("enqueueRetryStateSyncChannel blocked on a full channel with a canceled ctx")
	}
}

func TestCollectRetryUpdates_ClearWaitForAlwaysSent(t *testing.T) {
	executor := newTestRunExecutor()
	stepExeID := "step-1"

	updates := executor.collectRetryUpdates(&stepExeID)
	require.NotNil(t, updates)
	assert.True(t, updates["step-1"].ClearWaitForRetryState)
}
