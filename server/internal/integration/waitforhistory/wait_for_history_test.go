package waitforhistory

import (
	"context"
	"sync"
	"testing"
	"time"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/server/cmd"
	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/internal/integration/testhelpers"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// runStatusCompleted is the terminal RunStatus a STOP_DECISION_COMPLETE run
// reaches (persistence.RunStatusCompleted).
const runStatusCompleted = int32(4)

func untilEventID(id int64) *pb.WaitForHistoryEventRequest_UntilEventId {
	return &pb.WaitForHistoryEventRequest_UntilEventId{UntilEventId: id}
}

func untilRunStop() *pb.WaitForHistoryEventRequest_UntilRunStop {
	return &pb.WaitForHistoryEventRequest_UntilRunStop{UntilRunStop: true}
}

// bootWaitForHistoryServer boots the server in `all` mode (engine + OpsFIFO +
// OpsService) so WaitForHistoryEvent is exercised against a live OpsFIFO
// reader — the producer that inserts history rows and rings the notifier.
// This package boots exactly one server, so it owns every shard for the run.
func bootWaitForHistoryServer(t *testing.T) (pb.RunsServiceClient, pb.OpsServiceClient) {
	t.Helper()
	uri := testhelpers.TestDBURI()
	if uri == "" {
		t.Skip(testhelpers.PersistenceBackendEnvVar + " backend URI not set")
	}

	cfg := config.DefaultConfig()
	testhelpers.ApplyPersistence(t, &cfg, uri, dbPrefix)
	testhelpers.ApplySingleNodeCluster(t, &cfg)
	cfg.Shard.MaxShards = 2
	cfg.Shard.DefaultShardsForNewNamespaces = 2
	cfg.Shard.ShutdownGracefulPeriod = 100 * time.Millisecond
	cfg.TaskProcessor.OpsBatchReadDelay = 10 * time.Millisecond
	cfg.TaskProcessor.OpsPollInterval = 50 * time.Millisecond

	app, err := cmd.NewServerApp(cfg, log.NewNoop())
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, app.StartAsync(ctx))

	// Wait until this single node owns every shard, so runs mapping to any
	// shard can be started immediately by the sub-tests below.
	require.Eventually(t, func() bool {
		return len(app.ShardManager.GetOwnedShards()) >= int(cfg.Shard.MaxShards)
	}, 30*time.Second, 50*time.Millisecond, "server must claim all shards before the tests start flows")

	runsConn, err := grpc.NewClient(app.RunGRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	opsConn, err := grpc.NewClient(app.OpsGRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)

	t.Cleanup(func() {
		runsConn.Close()
		opsConn.Close()
		app.Stop()
	})
	return pb.NewRunsServiceClient(runsConn), pb.NewOpsServiceClient(opsConn)
}

// startRunAwaitFirstEvent starts a run in a fresh namespace and waits until the
// RunStart event (id 1) is readable, so callers begin from a known tip.
func startRunAwaitFirstEvent(t *testing.T, runs pb.RunsServiceClient, ops pb.OpsServiceClient) (namespace, runID string) {
	t.Helper()
	ctx := context.Background()
	namespace = "wfh-" + uuid.NewString()
	runID = uuid.NewString()
	_, err := runs.StartRun(ctx, &pb.StartRunRequest{
		Namespace: namespace, RunId: runID, FlowType: "wfh-flow", TaskListName: "g",
		StartingSteps: []*pb.NextStep{{StepId: "s1"}},
	})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		hist, hErr := ops.GetHistoryEvents(ctx, &pb.GetHistoryEventsRequest{
			Namespace: namespace, RunId: runID, AfterId: 0, Limit: 10,
		})
		return hErr == nil && len(hist.Events) >= 1
	}, 15*time.Second, 50*time.Millisecond, "RunStart event must be readable before the test begins")
	return namespace, runID
}

func publishOne(t *testing.T, runs pb.RunsServiceClient, ns, runID string) {
	t.Helper()
	_, err := runs.PublishToChannel(context.Background(), &pb.PublishToChannelRequest{
		Namespace: ns, RunId: runID, ChannelName: "ch",
		Values: []*pb.Value{{Kind: &pb.Value_NullValue{}}},
	})
	require.NoError(t, err)
}

func stopRun(t *testing.T, runs pb.RunsServiceClient, ns, runID string) {
	t.Helper()
	_, err := runs.StopRun(context.Background(), &pb.StopRunRequest{
		Namespace: ns, RunId: runID, StopDecision: pb.StopDecision_STOP_DECISION_COMPLETE,
	})
	require.NoError(t, err)
}

// TestWaitForHistoryEvent boots one full server and exercises both wait types
// against a live OpsFIFO reader across sub-tests (each in its own namespace).
// The RPC returns the satisfying event id on success and codes.DeadlineExceeded
// when the caller's context deadline elapses first.
func TestWaitForHistoryEvent(t *testing.T) {
	runs, ops := bootWaitForHistoryServer(t)
	ctx := context.Background()

	t.Run("by_id immediate when event already written", func(t *testing.T) {
		ns, runID := startRunAwaitFirstEvent(t, runs, ops)
		start := time.Now()
		resp, err := runs.WaitForHistoryEvent(ctx, &pb.WaitForHistoryEventRequest{
			Namespace: ns, RunId: runID, Condition: untilEventID(1),
		})
		require.NoError(t, err)
		assert.GreaterOrEqual(t, resp.LatestEventId, int64(1))
		assert.Less(t, time.Since(start), 2*time.Second, "fast path must not block")
	})

	t.Run("by_id blocked then signaled by OpsFIFO insert", func(t *testing.T) {
		ns, runID := startRunAwaitFirstEvent(t, runs, ops)

		resCh := make(chan *pb.WaitForHistoryEventResponse, 1)
		errCh := make(chan error, 1)
		go func() {
			resp, err := runs.WaitForHistoryEvent(ctx, &pb.WaitForHistoryEventRequest{
				Namespace: ns, RunId: runID, Condition: untilEventID(2),
			})
			resCh <- resp
			errCh <- err
		}()

		time.Sleep(200 * time.Millisecond)
		publishOne(t, runs, ns, runID) // appends a ChannelPublish event (id 2)

		select {
		case resp := <-resCh:
			require.NoError(t, <-errCh)
			assert.GreaterOrEqual(t, resp.LatestEventId, int64(2), "must return once event 2 is inserted")
		case <-time.After(15 * time.Second):
			t.Fatal("WaitForHistoryEvent did not return after the publish inserted a new event")
		}
	})

	t.Run("by_id blocks until caller deadline (DeadlineExceeded)", func(t *testing.T) {
		ns, runID := startRunAwaitFirstEvent(t, runs, ops)
		start := time.Now()
		// No worker runs, so the run parks at WaitingForWorker (not terminal); an
		// unreachable expected id must block until the caller's own ctx deadline.
		waitCtx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
		defer cancel()
		_, err := runs.WaitForHistoryEvent(waitCtx, &pb.WaitForHistoryEventRequest{
			Namespace: ns, RunId: runID, Condition: untilEventID(99_999),
		})
		require.Error(t, err)
		assert.Equal(t, codes.DeadlineExceeded, status.Code(err))
		elapsed := time.Since(start)
		assert.GreaterOrEqual(t, elapsed, 1*time.Second, "must actually block until the deadline")
		assert.Less(t, elapsed, 5*time.Second)
	})

	t.Run("by_id closed short-circuits an unreachable expected id", func(t *testing.T) {
		ns, runID := startRunAwaitFirstEvent(t, runs, ops)

		resCh := make(chan *pb.WaitForHistoryEventResponse, 1)
		errCh := make(chan error, 1)
		go func() {
			resp, err := runs.WaitForHistoryEvent(ctx, &pb.WaitForHistoryEventRequest{
				Namespace: ns, RunId: runID, Condition: untilEventID(99_999),
			})
			resCh <- resp
			errCh <- err
		}()

		time.Sleep(200 * time.Millisecond)
		stopRun(t, runs, ns, runID) // RunStop insert wakes the by-id waiter via the closed run

		select {
		case resp := <-resCh:
			require.NoError(t, <-errCh)
			assert.GreaterOrEqual(t, resp.LatestEventId, int64(1))
			assert.Less(t, resp.LatestEventId, int64(99_999), "returned the run's actual tip, not the expected id")
			assert.Equal(t, runStatusCompleted, resp.RunStatus, "closed run carries its terminal status")
		case <-time.After(15 * time.Second):
			t.Fatal("WaitForHistoryEvent did not return after the run closed")
		}
	})

	t.Run("run_stop blocks past event advances, wakes on close", func(t *testing.T) {
		ns, runID := startRunAwaitFirstEvent(t, runs, ops)

		resCh := make(chan *pb.WaitForHistoryEventResponse, 1)
		errCh := make(chan error, 1)
		go func() {
			resp, err := runs.WaitForHistoryEvent(ctx, &pb.WaitForHistoryEventRequest{
				Namespace: ns, RunId: runID, Condition: untilRunStop(),
			})
			resCh <- resp
			errCh <- err
		}()

		// A publish advances history but must NOT wake a run_stop waiter.
		time.Sleep(200 * time.Millisecond)
		publishOne(t, runs, ns, runID)
		select {
		case resp := <-resCh:
			t.Fatalf("run_stop returned before the run closed: %+v", resp)
		case <-time.After(1 * time.Second):
		}

		stopRun(t, runs, ns, runID)
		select {
		case resp := <-resCh:
			require.NoError(t, <-errCh)
			assert.GreaterOrEqual(t, resp.LatestEventId, int64(1), "returns the RunStop event id")
			assert.Equal(t, runStatusCompleted, resp.RunStatus)
		case <-time.After(15 * time.Second):
			t.Fatal("run_stop did not return after the run closed")
		}
	})

	t.Run("run_stop immediate when run already closed", func(t *testing.T) {
		ns, runID := startRunAwaitFirstEvent(t, runs, ops)
		stopRun(t, runs, ns, runID)
		// Wait until the RunStop event is inserted (run authoritatively closed):
		// run_stop then returns without error.
		require.Eventually(t, func() bool {
			_, err := runs.WaitForHistoryEvent(ctx, &pb.WaitForHistoryEventRequest{
				Namespace: ns, RunId: runID, Condition: untilRunStop(),
			})
			return err == nil
		}, 15*time.Second, 100*time.Millisecond, "run_stop must observe the closed run")

		start := time.Now()
		resp, err := runs.WaitForHistoryEvent(ctx, &pb.WaitForHistoryEventRequest{
			Namespace: ns, RunId: runID, Condition: untilRunStop(),
		})
		require.NoError(t, err)
		assert.GreaterOrEqual(t, resp.LatestEventId, int64(1))
		assert.Equal(t, runStatusCompleted, resp.RunStatus)
		assert.Less(t, time.Since(start), 2*time.Second, "closed run must short-circuit")
	})

	t.Run("concurrent by_id waiters all wake on one insert", func(t *testing.T) {
		ns, runID := startRunAwaitFirstEvent(t, runs, ops)

		const waiters = 8
		var wg sync.WaitGroup
		errs := make([]error, waiters)
		latests := make([]int64, waiters)
		for i := range waiters {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				resp, err := runs.WaitForHistoryEvent(ctx, &pb.WaitForHistoryEventRequest{
					Namespace: ns, RunId: runID, Condition: untilEventID(2),
				})
				errs[idx] = err
				if err == nil {
					latests[idx] = resp.LatestEventId
				}
			}(i)
		}

		time.Sleep(200 * time.Millisecond)
		publishOne(t, runs, ns, runID)

		wg.Wait()
		for i := range waiters {
			require.NoError(t, errs[i], "waiter %d", i)
			assert.GreaterOrEqual(t, latests[i], int64(2), "waiter %d", i)
		}
	})

	t.Run("missing wait_type returns InvalidArgument", func(t *testing.T) {
		ns, runID := startRunAwaitFirstEvent(t, runs, ops)
		_, err := runs.WaitForHistoryEvent(ctx, &pb.WaitForHistoryEventRequest{
			Namespace: ns, RunId: runID,
		})
		require.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("unknown run blocks until caller deadline", func(t *testing.T) {
		// A run with no history (unknown / not-yet-started) has no latest event,
		// so it does not error immediately — it blocks until the caller's ctx
		// deadline, then returns DeadlineExceeded.
		start := time.Now()
		waitCtx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
		defer cancel()
		_, err := runs.WaitForHistoryEvent(waitCtx, &pb.WaitForHistoryEventRequest{
			Namespace: "wfh-missing-" + uuid.NewString(), RunId: uuid.NewString(),
			Condition: untilEventID(1),
		})
		require.Error(t, err)
		assert.Equal(t, codes.DeadlineExceeded, status.Code(err))
		assert.GreaterOrEqual(t, time.Since(start), 1*time.Second, "must block, not return immediately")
	})

	t.Run("wait started before the run exists wakes on RunStart", func(t *testing.T) {
		ns := "wfh-pre-" + uuid.NewString()
		runID := uuid.NewString()

		resCh := make(chan *pb.WaitForHistoryEventResponse, 1)
		errCh := make(chan error, 1)
		go func() {
			resp, err := runs.WaitForHistoryEvent(ctx, &pb.WaitForHistoryEventRequest{
				Namespace: ns, RunId: runID, Condition: untilEventID(1),
			})
			resCh <- resp
			errCh <- err
		}()

		// Start the run only after the waiter is blocking; its RunStart insert
		// must wake the waiter via the notifier.
		time.Sleep(300 * time.Millisecond)
		_, err := runs.StartRun(ctx, &pb.StartRunRequest{
			Namespace: ns, RunId: runID, FlowType: "wfh-flow", TaskListName: "g",
			StartingSteps: []*pb.NextStep{{StepId: "s1"}},
		})
		require.NoError(t, err)

		select {
		case resp := <-resCh:
			require.NoError(t, <-errCh)
			assert.GreaterOrEqual(t, resp.LatestEventId, int64(1), "must wake once RunStart is inserted")
		case <-time.After(15 * time.Second):
			t.Fatal("WaitForHistoryEvent did not wake after the run was created")
		}
	})
}
