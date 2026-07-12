package runsapi

import (
	"context"
	"testing"
	"time"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/server/cmd"
	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/internal/integration/testhelpers"
	p "github.com/superdurable/dex/server/internal/persistence"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

const dbPrefix = "dex_test_integration_runsapi"

func startTestServer(t *testing.T) (*cmd.ServerApp, pb.RunsServiceClient) {
	uri := testhelpers.TestDBURI()
	if uri == "" {
		t.Skip(testhelpers.PersistenceBackendEnvVar + " backend URI not set")
	}

	cfg := config.DefaultConfig()
	testhelpers.ApplyPersistence(t, &cfg, uri, dbPrefix)
	testhelpers.ApplySingleNodeCluster(t, &cfg)
	cfg.Shard.MaxShards = 2
	cfg.Shard.DefaultShardsForNewNamespaces = 2
	cfg.Shard.LeaseDuration = 60 * time.Second
	cfg.Shard.ShutdownGracefulPeriod = 100 * time.Millisecond

	logger := log.NewNoop()
	app, err := cmd.NewServerApp(cfg, logger)
	require.NoError(t, err)
	app.RunStore.DeleteAll(context.Background())

	ctx := context.Background()
	require.NoError(t, app.StartAsync(ctx))

	addr := app.GRPCAddress()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)

	t.Cleanup(func() {
		conn.Close()
		app.Stop()
	})

	client := pb.NewRunsServiceClient(conn)
	return app, client
}

// ============================================================================
// Single Mode API Tests
// ============================================================================

func TestAPI_StartRun_HappyPath(t *testing.T) {
	app, client := startTestServer(t)
	ctx := context.Background()
	runID := uuid.NewString()

	resp, err := client.StartRun(ctx, &pb.StartRunRequest{
		Namespace: "test-ns", RunId: runID, FlowType: "api-test", TaskListName: "g",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)

	shardID := app.ShardMapper.GetShardID("test-ns", runID)
	run, gErr := app.RunStore.GetRun(ctx, shardID, "test-ns", runID, p.GetRunOptions{})
	require.Nil(t, gErr)
	assert.Equal(t, p.RunStatusPending, run.Status)
	assert.Equal(t, "api-test", run.FlowType)
}

func TestAPI_StartRun_Duplicate(t *testing.T) {
	_, client := startTestServer(t)
	ctx := context.Background()
	runID := uuid.NewString()

	_, err := client.StartRun(ctx, &pb.StartRunRequest{
		Namespace: "test-ns", RunId: runID, FlowType: "api-test", TaskListName: "g",
	})
	require.NoError(t, err)

	_, err2 := client.StartRun(ctx, &pb.StartRunRequest{
		Namespace: "test-ns", RunId: runID, FlowType: "api-test", TaskListName: "g",
	})
	require.Error(t, err2)
	st, ok := status.FromError(err2)
	require.True(t, ok)
	assert.Equal(t, codes.AlreadyExists, st.Code())
}

func TestAPI_PublishToChannel_AllWaiting_UnblocksStep(t *testing.T) {
	app, client := startTestServer(t)
	ctx := context.Background()
	runID := uuid.NewString()

	_, err := client.StartRun(ctx, &pb.StartRunRequest{
		Namespace: "test-ns", RunId: runID, FlowType: "api-test", TaskListName: "g",
	})
	require.NoError(t, err)

	shardID := app.ShardMapper.GetShardID("test-ns", runID)
	run, _ := app.RunStore.GetRun(ctx, shardID, "test-ns", runID, p.GetRunOptions{})

	allWaiting := p.RunStatusAllStepsWaitingForConditions
	app.RunStore.UpdateRunWithNewTasks(ctx, shardID, "test-ns", runID, run.Version, &p.RunRowUpdate{
		Status: &allWaiting,
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"wait-1": {
				Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusWaitingForCondition,
				WaitForCondition: &p.WaitForCondition{Type: p.WaitTypeAnyOf,
					Conditions: []p.SingleCondition{{Channel: &p.ChannelCondition{ChannelName: "notify", Min: 1}}}},
			},
		},
	}, nil)

	_, pubErr := client.PublishToChannel(ctx, &pb.PublishToChannelRequest{
		Namespace: "test-ns", RunId: runID, ChannelName: "notify",
		Values: []*pb.Value{{Kind: &pb.Value_IntValue{IntValue: 42}}},
	})
	require.NoError(t, pubErr)

	updated, _ := app.RunStore.GetRun(ctx, shardID, "test-ns", runID, p.GetRunOptions{})
	t.Logf("run status after PublishToChannel: %d, version: %d", updated.Status, updated.Version)
	assert.Equal(t, p.RunStatusPending, updated.Status)
	// Server wake promotes with reservation; worker picks up INVOKING_EXECUTE on dispatch.
	step, exists := updated.ActiveStepExecutions["wait-1"]
	assert.True(t, exists)
	t.Logf("step wait-1 status: %d", step.Status)
	assert.Equal(t, p.StepExeStatusInvokingExecute, step.Status)
	assert.NotZero(t, step.ExecuteMethodExeID)
}

func TestAPI_PublishToChannel_RunningRun(t *testing.T) {
	app, client := startTestServer(t)
	ctx := context.Background()
	runID := uuid.NewString()

	_, err := client.StartRun(ctx, &pb.StartRunRequest{
		Namespace: "test-ns", RunId: runID, FlowType: "api-test", TaskListName: "g",
	})
	require.NoError(t, err)

	shardID := app.ShardMapper.GetShardID("test-ns", runID)
	run, _ := app.RunStore.GetRun(ctx, shardID, "test-ns", runID, p.GetRunOptions{})

	running := p.RunStatusRunning
	app.RunStore.UpdateRunWithNewTasks(ctx, shardID, "test-ns", runID, run.Version, &p.RunRowUpdate{
		Status: &running,
	}, nil)

	_, pubErr := client.PublishToChannel(ctx, &pb.PublishToChannelRequest{
		Namespace: "test-ns", RunId: runID, ChannelName: "events",
		Values: []*pb.Value{{Kind: &pb.Value_IntValue{IntValue: 99}}},
	})
	require.NoError(t, pubErr)

	updated, _ := app.RunStore.GetRun(ctx, shardID, "test-ns", runID, p.GetRunOptions{})
	assert.Equal(t, p.RunStatusRunning, updated.Status)
	assert.Len(t, updated.UnconsumedChannelMessages["events"], 1)
}

// TestAPI_StopRun_NotFound: stopping a run that does not exist surfaces
// a gRPC NotFound to the caller. (Replaces the previous "Unimplemented"
// test now that StopRun is implemented end-to-end.)
func TestAPI_StopRun_NotFound(t *testing.T) {
	_, client := startTestServer(t)
	ctx := context.Background()

	_, err := client.StopRun(ctx, &pb.StopRunRequest{
		Namespace: "test-ns", RunId: "missing-" + uuid.NewString(),
		StopDecision: pb.StopDecision_STOP_DECISION_COMPLETE,
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

// TestAPI_StopRun_HappyPath: stopping a Pending run transitions it to
// Completed and is observable via GetRun.
func TestAPI_StopRun_HappyPath(t *testing.T) {
	app, client := startTestServer(t)
	ctx := context.Background()
	runID := uuid.NewString()

	_, err := client.StartRun(ctx, &pb.StartRunRequest{
		Namespace: "test-ns", RunId: runID, FlowType: "api-test", TaskListName: "g",
	})
	require.NoError(t, err)

	_, err = client.StopRun(ctx, &pb.StopRunRequest{
		Namespace: "test-ns", RunId: runID,
		StopDecision: pb.StopDecision_STOP_DECISION_COMPLETE,
		Reason:       "cleanup before deploy",
	})
	require.NoError(t, err)

	shardID := app.ShardMapper.GetShardID("test-ns", runID)
	run, gErr := app.RunStore.GetRun(ctx, shardID, "test-ns", runID, p.GetRunOptions{})
	require.Nil(t, gErr)
	assert.Equal(t, p.RunStatusCompleted, run.Status)

	// Idempotent: a second StopRun is a no-op success.
	_, err = client.StopRun(ctx, &pb.StopRunRequest{
		Namespace: "test-ns", RunId: runID, StopDecision: pb.StopDecision_STOP_DECISION_FAIL,
	})
	require.NoError(t, err)
}
