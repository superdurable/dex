package matchingfanin

import (
	"context"
	"testing"
	"time"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/internal/integration/testhelpers"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// pollUntilRun loops PollForRun like a real worker until it receives the
// expected run or the deadline expires. Each call uses a short per-poll
// budget so the worker re-polls (and re-forwards to root) frequently.
func pollUntilRun(t *testing.T, matchClient pb.MatchingServiceClient, ns, tasklist, workerID, runID string, overall time.Duration) *pb.PollForRunResponse {
	t.Helper()
	deadline := time.Now().Add(overall)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		resp, err := matchClient.PollForRun(ctx, &pb.PollForRunRequest{
			Namespace:    ns,
			TaskListName: tasklist,
			WorkerId:     workerID,
		})
		cancel()
		if err != nil {
			continue
		}
		if resp != nil && resp.RunId == runID {
			return resp
		}
	}
	return nil
}

// Read fan-in: writes converge on a single (root) partition while polls
// scatter across 4 read partitions. A worker poll that lands on a non-root
// partition must fan in to root (forwarder.ForwardPoll, carrying the real
// workerID) to pick up the dispatched run. Asserts the worker eventually
// receives the run.
func TestFanIn_ReadScatter_PollReceivesDispatch(t *testing.T) {
	app, runsClient, matchClient := testhelpers.StartE2EServerWithConfig(t, dbPrefix, func(cfg *config.Config) {
		cfg.Tasklist.NumWritePartitions = 1
		cfg.Tasklist.NumReadPartitions = 4
		// Workers here poll with a short 2s budget; shrink the safety
		// buffer so the budget stays positive and re-polls stay frequent.
		cfg.MatchingService.LongPollSafetyBuffer = 200 * time.Millisecond
		cfg.MatchingService.LongPollDefaultTimeout = 2 * time.Second
	})
	_ = app

	ns := "fanin-read-" + uuid.NewString()[:8]
	runID := uuid.NewString()
	tasklist := "fanin-tl"

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := runsClient.StartRun(ctx, &pb.StartRunRequest{
		Namespace:    ns,
		RunId:        runID,
		FlowType:     "fanin-test",
		TaskListName: tasklist,
	})
	require.NoError(t, err)

	got := pollUntilRun(t, matchClient, ns, tasklist, "worker-1", runID, 30*time.Second)
	require.NotNil(t, got, "worker should receive the dispatched run via read fan-in to root")
	require.Equal(t, runID, got.RunId)
}

// Write fan-in: polls converge on root (NumReadPartitions=1) while writes
// scatter across 4 partitions. A dispatch landing on a non-root partition
// with no local poller must relay to root (forwarder.ForwardTask) so the
// worker waiting at root sync-matches it. The background worker loop keeps
// a poller waiting at root so the relay can rendezvous.
func TestFanIn_WriteScatter_RootPollerReceivesDispatch(t *testing.T) {
	app, runsClient, matchClient := testhelpers.StartE2EServerWithConfig(t, dbPrefix, func(cfg *config.Config) {
		cfg.Tasklist.NumWritePartitions = 4
		cfg.Tasklist.NumReadPartitions = 1
		cfg.MatchingService.LongPollSafetyBuffer = 200 * time.Millisecond
		cfg.MatchingService.LongPollDefaultTimeout = 2 * time.Second
	})
	_ = app

	ns := "fanin-write-" + uuid.NewString()[:8]
	runID := uuid.NewString()
	tasklist := "fanin-tl"

	// Keep a worker polling root in the background so a relayed dispatch
	// can sync-match the waiting poller.
	delivered := make(chan *pb.PollForRunResponse, 1)
	go func() {
		if got := pollUntilRun(t, matchClient, ns, tasklist, "worker-1", runID, 30*time.Second); got != nil {
			delivered <- got
		}
	}()

	// Give the poller a moment to register at root before dispatching.
	time.Sleep(200 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := runsClient.StartRun(ctx, &pb.StartRunRequest{
		Namespace:    ns,
		RunId:        runID,
		FlowType:     "fanin-test",
		TaskListName: tasklist,
	})
	require.NoError(t, err)

	select {
	case got := <-delivered:
		require.Equal(t, runID, got.RunId)
	case <-time.After(30 * time.Second):
		t.Fatal("worker at root did not receive the dispatched run via write fan-in")
	}
}
