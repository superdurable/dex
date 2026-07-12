package dex

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// Fix 2: callRunRPC must gate on the passed ctx, not w.rootCtx — otherwise the
// shutdown-time releaseRunBestEffort (detached ctx) never runs its call().
func TestCallRunRPC_RunsCallUnderLiveCtxEvenWhenRootCtxDone(t *testing.T) {
	rootCtx, cancelRoot := context.WithCancel(context.Background())
	cancelRoot() // worker already shutting down
	w := &Worker{rootCtx: rootCtx, log: NewDefaultLogger()}

	called := false
	ownershipLost, err := w.callRunRPC(context.Background(), "op", "run-1", 0, func() error {
		called = true
		return nil
	})
	assert.True(t, called, "call() must run under a live passed ctx even when rootCtx is done")
	assert.False(t, ownershipLost)
	assert.NoError(t, err)
}

// Fix 1: on graceful shutdown while parking, callRunRPC returns a ctx error;
// releaseRunAllStepsWaiting must exit gracefully, not panic(nil).
func TestReleaseRunAllStepsWaiting_ShutdownExitsWithoutPanic(t *testing.T) {
	rootCtx, cancel := context.WithCancel(context.Background())
	cancel()
	w := &Worker{rootCtx: rootCtx, log: NewDefaultLogger()}

	exit, err := w.releaseRunAllStepsWaiting("run-1", &pb.WorkerCallContext{}, make(chan *pb.ExternalEvent, 1))
	assert.True(t, exit)
	assert.Error(t, err) // context.Canceled propagated
}

type fakeMatchClient struct {
	pb.MatchingServiceClient
	pollForRun func(context.Context, *pb.PollForRunRequest) (*pb.PollForRunResponse, error)
}

func (f *fakeMatchClient) PollForRun(ctx context.Context, in *pb.PollForRunRequest, _ ...grpc.CallOption) (*pb.PollForRunResponse, error) {
	return f.pollForRun(ctx, in)
}

// Fix 3: a per-poll DeadlineExceeded must NOT kill the poller (no respawn);
// the loop reconnects and only exits once rootCtx is actually cancelled.
func TestPollForRunLoop_DeadlineExceededReconnectsUntilShutdown(t *testing.T) {
	rootCtx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int32
	fake := &fakeMatchClient{
		pollForRun: func(context.Context, *pb.PollForRunRequest) (*pb.PollForRunResponse, error) {
			if calls.Add(1) >= 3 {
				cancel() // simulate shutdown after a few transient timeouts
			}
			// Raw context.DeadlineExceeded is the case the old errors.Is check
			// mishandled (it exited the poller on the first one).
			return nil, context.DeadlineExceeded
		},
	}
	w := &Worker{
		rootCtx:      rootCtx,
		log:          NewDefaultLogger(),
		matchClient:  fake,
		runSlots:     make(chan struct{}, 1),
		ready:        make(chan struct{}),
		namespace:    "ns",
		taskListName: "tl",
		workerID:     "w-1",
	}

	done := make(chan struct{})
	go func() { w.pollForRunLoop(); close(done) }()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("pollForRunLoop did not exit after shutdown")
	}
	require.GreaterOrEqual(t, calls.Load(), int32(3),
		"must keep polling across DeadlineExceeded instead of exiting on the first")
}
