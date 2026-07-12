// Package asyncpickup tests the matching service's async-pickup path: a
// DispatchRun that finds no waiting poller persists the task to the
// partition DB; a later PollForRun has the task fetched from DB by the
// reader and delivered via ProcessAsyncMatch. Two levels:
//   - Manager-level integration (real tasklist store + fake RunsClient)
//   - full-stack E2E (StartRun then a late worker poll)
package asyncpickup

import (
	"context"
	"sync"
	"testing"
	"time"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/internal/integration/testhelpers"
	"github.com/superdurable/dex/server/internal/tasklist"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// fakeRunsClient stubs the runs-service loopback the Manager uses on the
// async-pickup path. Only ProcessAsyncMatch is exercised; the embedded
// interface satisfies the rest (and panics if anything else is called,
// surfacing an unexpected code path).
type fakeRunsClient struct {
	pb.RunsServiceClient
	mu    sync.Mutex
	calls int
}

func (f *fakeRunsClient) ProcessAsyncMatch(_ context.Context, in *pb.ProcessAsyncMatchRequest, _ ...grpc.CallOption) (*pb.ProcessAsyncMatchResponse, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	return &pb.ProcessAsyncMatchResponse{
		Outcome:            pb.AsyncMatchOutcome_ASYNC_MATCH_OUTCOME_SUCCESS,
		PollForRunResponse: &pb.PollForRunResponse{Namespace: in.Namespace, RunId: in.RunId, WorkerId: in.WorkerId},
	}, nil
}

func (f *fakeRunsClient) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

type slowFailThenOKRunsClient struct {
	pb.RunsServiceClient
	mu    sync.Mutex
	calls int
}

func (f *slowFailThenOKRunsClient) ProcessAsyncMatch(ctx context.Context, in *pb.ProcessAsyncMatchRequest, _ ...grpc.CallOption) (*pb.ProcessAsyncMatchResponse, error) {
	f.mu.Lock()
	f.calls++
	call := f.calls
	f.mu.Unlock()
	if call == 1 {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return &pb.ProcessAsyncMatchResponse{
		Outcome:            pb.AsyncMatchOutcome_ASYNC_MATCH_OUTCOME_SUCCESS,
		PollForRunResponse: &pb.PollForRunResponse{Namespace: in.Namespace, RunId: in.RunId, WorkerId: in.WorkerId},
	}, nil
}

// ProcessAsyncMatch failure must push the task back even when the poll ctx
// is already cancelled — otherwise readLevel has advanced and the task is lost.
func TestAsyncPickup_Manager_PushBackSurvivesCancelledPollCtx(t *testing.T) {
	storeSet := testhelpers.NewStoreSetForTest(t, dbPrefix)
	fakeRuns := &slowFailThenOKRunsClient{}

	ns := "asyncpickup-pushback-" + uuid.NewString()[:8]
	id, err := tasklist.NewIdentifier(ns, "async-tl", 0)
	require.NoError(t, err)

	cfg := config.DefaultMatchingEngineConfig()
	cfg.TaskBufferSize = 1

	mgr := tasklist.NewManager(id, tasklist.ManagerDeps{
		Store:           storeSet.Tasklist,
		RunsClient:      fakeRuns,
		MemberID:        "test-node",
		MatchingAddress: "127.0.0.1:0",
		Logger:          log.NewNoop(),
		Config:          cfg,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	require.Nil(t, mgr.Start(ctx))
	t.Cleanup(mgr.Stop)

	runID := uuid.NewString()
	require.Nil(t, mgr.WriteTask(ctx, runID, 0))

	shortPollCtx, shortCancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer shortCancel()

	_, pollErr := mgr.PollForRun(shortPollCtx, "worker-1", false)
	require.NotNil(t, pollErr)

	resp, retryErr := mgr.PollForRun(ctx, "worker-1", false)
	require.Nil(t, retryErr)
	require.NotNil(t, resp)
	require.Equal(t, runID, resp.RunId)

	fakeRuns.mu.Lock()
	require.Equal(t, 2, fakeRuns.calls)
	fakeRuns.mu.Unlock()
}

// Manager-level integration: a task written with no poller waiting (the
// async-dispatch fallback) is later picked up by a blocking poll, which
// fetches it from DB and delivers it via ProcessAsyncMatch.
func TestAsyncPickup_Manager_WriteThenPoll(t *testing.T) {
	storeSet := testhelpers.NewStoreSetForTest(t, dbPrefix)
	fakeRuns := &fakeRunsClient{}

	ns := "asyncpickup-mgr-" + uuid.NewString()[:8]
	// Root partition (partition 0) → no forwarder, so membership and the
	// remote client are unnecessary for this path.
	id, err := tasklist.NewIdentifier(ns, "async-tl", 0)
	require.NoError(t, err)

	mgr := tasklist.NewManager(id, tasklist.ManagerDeps{
		Store:           storeSet.Tasklist,
		RunsClient:      fakeRuns,
		MemberID:        "test-node",
		MatchingAddress: "127.0.0.1:0",
		Logger:          log.NewNoop(),
		Config:          config.DefaultMatchingEngineConfig(),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	require.Nil(t, mgr.Start(ctx))
	t.Cleanup(mgr.Stop)

	runID := uuid.NewString()
	// No poller is waiting → this mirrors DispatchRun's async-miss
	// fallback, persisting the task to the partition DB.
	require.Nil(t, mgr.WriteTask(ctx, runID, 0))

	// Blocking poll: reader fetches the row from DB into the matcher's
	// buffer, deliverTaskToPoller runs ProcessAsyncMatch, worker gets it.
	resp, pollErr := mgr.PollForRun(ctx, "worker-1", false)
	require.Nil(t, pollErr)
	require.NotEmpty(t, resp.RunId, "blocking poll should pick up the persisted task")
	require.Equal(t, runID, resp.RunId)
	require.Equal(t, 1, fakeRuns.callCount(), "async pickup must drive exactly one ProcessAsyncMatch")
}

// Manager-level integration: a non-blocking poll issued before the reader
// has buffered the task returns empty (no busy task), and a subsequent
// poll picks it up once buffered. Guards the TryLocalPoll empty path.
func TestAsyncPickup_Manager_NonBlockingEmptyThenReady(t *testing.T) {
	storeSet := testhelpers.NewStoreSetForTest(t, dbPrefix)
	fakeRuns := &fakeRunsClient{}

	ns := "asyncpickup-nb-" + uuid.NewString()[:8]
	id, err := tasklist.NewIdentifier(ns, "async-tl", 0)
	require.NoError(t, err)

	mgr := tasklist.NewManager(id, tasklist.ManagerDeps{
		Store:           storeSet.Tasklist,
		RunsClient:      fakeRuns,
		MemberID:        "test-node",
		MatchingAddress: "127.0.0.1:0",
		Logger:          log.NewNoop(),
		Config:          config.DefaultMatchingEngineConfig(),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	require.Nil(t, mgr.Start(ctx))
	t.Cleanup(mgr.Stop)

	// Nothing written yet → non-blocking poll returns an empty response.
	empty, pollErr := mgr.PollForRun(ctx, "worker-1", true)
	require.Nil(t, pollErr)
	require.Empty(t, empty.RunId)

	runID := uuid.NewString()
	require.Nil(t, mgr.WriteTask(ctx, runID, 0))

	// Eventually the reader buffers it and a non-blocking poll succeeds.
	require.Eventually(t, func() bool {
		resp, err := mgr.PollForRun(ctx, "worker-1", true)
		return err == nil && resp.RunId == runID
	}, 10*time.Second, 20*time.Millisecond)
	require.Equal(t, 1, fakeRuns.callCount())
}

// Full-stack E2E: StartRun dispatches with no worker polling, so matching
// persists the task (async-pickup path). A worker that polls afterwards
// receives the run — exercising run-service → engine → taskprocessor →
// matching → tasklist → DB → reader → ProcessAsyncMatch → worker.
func TestAsyncPickup_E2E_DispatchThenLatePoll(t *testing.T) {
	_, runsClient, matchClient := testhelpers.StartE2EServerWithConfig(t, dbPrefix, func(cfg *config.Config) {
		// Single partition isolates the async-pickup path from fan-in.
		cfg.Tasklist.NumWritePartitions = 1
		cfg.Tasklist.NumReadPartitions = 1
		// Workers poll with a short 2s budget; shrink the safety buffer so
		// the budget stays positive.
		cfg.MatchingService.LongPollSafetyBuffer = 200 * time.Millisecond
		cfg.MatchingService.LongPollDefaultTimeout = 2 * time.Second
	})

	ns := "asyncpickup-e2e-" + uuid.NewString()[:8]
	runID := uuid.NewString()
	tasklistName := "async-e2e-tl"

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := runsClient.StartRun(ctx, &pb.StartRunRequest{
		Namespace:    ns,
		RunId:        runID,
		FlowType:     "async-test",
		TaskListName: tasklistName,
	})
	require.NoError(t, err)

	got := pollUntilRun(t, matchClient, ns, tasklistName, "worker-1", runID, 30*time.Second)
	require.NotNil(t, got, "worker should receive the dispatched run via async DB pickup")
	require.Equal(t, runID, got.RunId)
}

// pollUntilRun loops PollForRun like a real worker until it receives the
// expected run or the deadline expires. Each call uses a short per-poll
// budget so the worker re-polls frequently.
func pollUntilRun(t *testing.T, matchClient pb.MatchingServiceClient, ns, tasklistName, workerID, runID string, overall time.Duration) *pb.PollForRunResponse {
	t.Helper()
	deadline := time.Now().Add(overall)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		resp, err := matchClient.PollForRun(ctx, &pb.PollForRunRequest{
			Namespace:    ns,
			TaskListName: tasklistName,
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
