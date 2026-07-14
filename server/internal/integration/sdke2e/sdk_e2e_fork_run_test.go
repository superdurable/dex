package sdke2e

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/sdk-go/dex"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// TestSDKE2E_ForkRun_EvictsHoldingWorker verifies ForkRun bumps the worker
// request counter and cancels a long-running Execute on the stale worker.
func TestSDKE2E_ForkRun_EvictsHoldingWorker(t *testing.T) {
	registry := dex.NewRegistry()
	registry.Register(&stopBlockCtxFlow{})

	app, _, _ := startE2EServer(t)
	client, worker, taskListName, runsPb := wireClientsAndWorker(t, app, registry)

	opsConn, err := grpc.NewClient(app.OpsGRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { opsConn.Close() })
	opsPb := pb.NewOpsServiceClient(opsConn)

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	runID := uuid.NewString()
	sig := &stopBlockSignal{entered: make(chan struct{}), release: make(chan struct{})}
	stopBlockSignals.Store(runID, sig)
	defer stopBlockSignals.Delete(runID)
	defer close(sig.release)

	startWorkerBg(t, worker)

	require.NoError(t, client.StartRunWithOptions(ctx, runID, &stopBlockCtxFlow{}, &dex.RunOptions{TaskListName: taskListName}))

	select {
	case <-sig.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("step did not enter Execute within 10s")
	}

	gotBefore, err := runsPb.GetRun(ctx, &pb.GetRunRequest{Namespace: "default", RunId: runID})
	require.NoError(t, err)
	counterBefore := gotBefore.WorkerRequestCounter

	_, err = runsPb.ForkRun(ctx, &pb.ForkRunRequest{
		Namespace: "default", RunId: runID, ToEventId: 1, Reason: "e2e-evict",
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return sig.exitedAfter.Load() != 0
	}, 10*time.Second, 50*time.Millisecond,
		"stale worker Execute should return after ForkRun eviction")

	gotAfter, err := runsPb.GetRun(ctx, &pb.GetRunRequest{Namespace: "default", RunId: runID})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, gotAfter.WorkerRequestCounter, counterBefore+1000)
	assert.Empty(t, gotAfter.WorkerId)

	hist, err := opsPb.GetHistoryEvents(ctx, &pb.GetHistoryEventsRequest{
		Namespace: "default", RunId: runID, AfterId: 0, Limit: 50,
	})
	require.NoError(t, err)
	var sawFork bool
	for _, event := range hist.Events {
		if event.GetRunFork() != nil {
			sawFork = true
		}
	}
	assert.True(t, sawFork)
}
