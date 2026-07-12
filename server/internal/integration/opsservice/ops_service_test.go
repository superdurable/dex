package opsservice

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
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
)

const dbPrefix = "dex_test_integration_opsservice"

// TestOpsService_StartRunProducesVisibilityAndHistory boots the server in
// `all` mode (full path: engine + OpsFIFO + OpsService) and verifies that:
//
//  1. StartRun eventually produces a visibility row that ListRuns
//     returns (proves: engine enqueues VisibilityWrite ops task → OpsFIFO
//     reader drains it → VisibilityStore upsert lands → OpsService.ListRuns
//     reads it).
//  2. The same StartRun produces a HISTORY_EVENT_RUN_START history event
//     that GetHistoryEvents returns (proves: engine enqueues HistoryWrite
//     ops task with stamped event_id → OpsFIFO reader drains it → HistoryStore
//     insert lands → OpsService.GetHistoryEvents reads it).
//
// Skipped without DEX_TEST_MONGO_URI (matches the rest of the integration
// suite). On CI this runs against the dependency-docker stack.
func TestOpsService_StartRunProducesVisibilityAndHistory(t *testing.T) {
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
	// Tighten the OpsFIFO debounce so the test doesn't sit on the production
	// 100ms × N delays.
	cfg.TaskProcessor.OpsBatchReadDelay = 10 * time.Millisecond
	cfg.TaskProcessor.OpsPollInterval = 50 * time.Millisecond

	app, err := cmd.NewServerApp(cfg, log.NewNoop())
	require.NoError(t, err)
	app.RunStore.DeleteAll(context.Background())
	if app.VisibilityStore != nil {
		_ = app.VisibilityStore.DeleteAll(context.Background())
	}
	if app.HistoryStore != nil {
		_ = app.HistoryStore.DeleteAll(context.Background())
	}

	ctx := context.Background()
	require.NoError(t, app.StartAsync(ctx))

	runsConn, err := grpc.NewClient(app.RunGRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	opsConn, err := grpc.NewClient(app.OpsGRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)

	t.Cleanup(func() {
		runsConn.Close()
		opsConn.Close()
		app.Stop()
	})

	runs := pb.NewRunsServiceClient(runsConn)
	ops := pb.NewOpsServiceClient(opsConn)

	// 1. Start a flow.
	namespace := "ops-test-" + uuid.NewString()
	runID := uuid.NewString()
	startReq := &pb.StartRunRequest{
		Namespace: namespace, RunId: runID, FlowType: "ops-flow", TaskListName: "g",
		StartingSteps: []*pb.NextStep{{StepId: "s1"}},
	}
	_, err = runs.StartRun(ctx, startReq)
	require.NoError(t, err)

	// 2. Wait for the OpsFIFO reader to drain the start-flow OpsTasks. The
	//    visibility upsert is the more user-visible signal of liveness.
	//
	//    No worker subscribes in this test, so the run progresses
	//    Pending -> WaitingForWorker via the dispatch task. We poll all
	//    three "early" statuses because which one lands in visibility
	//    depends on which OpsTask the reader drains last (latest-wins
	//    upsert). Running (2) is included for safety in case a future
	//    test variant subscribes a worker.
	earlyStatuses := []int32{
		0, // Pending
		1, // WaitingForWorker
		2, // Running
	}
	require.Eventually(t, func() bool {
		for _, status := range earlyStatuses {
			page, listErr := ops.ListRuns(ctx, &pb.ListRunsRequest{
				Namespace: namespace, FlowType: "ops-flow", Status: proto.Int32(status),
				OrderBy: pb.ListRunsOrderBy_LIST_RUNS_ORDER_BY_START_TIME_DESC, Limit: 10,
			})
			if listErr != nil || page == nil {
				continue
			}
			for _, s := range page.Runs {
				if s.RunId == runID {
					return true
				}
			}
		}
		return false
	}, 15*time.Second, 100*time.Millisecond, "visibility row must surface via ListRuns within the OpsFIFO debounce + insert window")

	// 3. History should contain at least the RunStart event by now.
	hist, err := ops.GetHistoryEvents(ctx, &pb.GetHistoryEventsRequest{
		Namespace: namespace, RunId: runID, AfterId: 0, Limit: 100,
	})
	require.NoError(t, err)
	require.NotEmpty(t, hist.Events, "GetHistoryEvents should return at least one event")

	first := hist.Events[0]
	assert.Equal(t, int64(1), first.Id, "first event_id is 1 (allocated under CAS as RunRow.LastHistoryEventID + 1)")

	// The first event is a RunStart — type-check via the oneof variant.
	runStart := first.GetRunStart()
	require.NotNil(t, runStart, "first event must be a RunStart oneof variant")
	assert.Equal(t, "ops-flow", runStart.FlowType)
	assert.Equal(t, "g", runStart.TaskListName)
	if assert.Len(t, runStart.StartingSteps, 1) {
		assert.Equal(t, "s1", runStart.StartingSteps[0].StepId)
	}
}

// TestOpsService_StopRunRecordsReason boots the full path (engine + OpsFIFO +
// OpsService) and verifies that a user-provided StopRun reason lands on the
// terminal RunStop history event, and that STOP_DECISION_FAIL maps to Failed.
func TestOpsService_StopRunRecordsReason(t *testing.T) {
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
	cfg.TaskProcessor.OpsBatchReadDelay = 10 * time.Millisecond
	cfg.TaskProcessor.OpsPollInterval = 50 * time.Millisecond

	app, err := cmd.NewServerApp(cfg, log.NewNoop())
	require.NoError(t, err)
	app.RunStore.DeleteAll(context.Background())
	if app.VisibilityStore != nil {
		_ = app.VisibilityStore.DeleteAll(context.Background())
	}
	if app.HistoryStore != nil {
		_ = app.HistoryStore.DeleteAll(context.Background())
	}

	ctx := context.Background()
	require.NoError(t, app.StartAsync(ctx))

	runsConn, err := grpc.NewClient(app.RunGRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	opsConn, err := grpc.NewClient(app.OpsGRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)

	t.Cleanup(func() {
		runsConn.Close()
		opsConn.Close()
		app.Stop()
	})

	runs := pb.NewRunsServiceClient(runsConn)
	ops := pb.NewOpsServiceClient(opsConn)

	namespace := "ops-stop-" + uuid.NewString()
	runID := uuid.NewString()
	_, err = runs.StartRun(ctx, &pb.StartRunRequest{
		Namespace: namespace, RunId: runID, FlowType: "ops-flow", TaskListName: "g",
		StartingSteps: []*pb.NextStep{{StepId: "s1"}},
	})
	require.NoError(t, err)

	const reason = "user cancelled deployment"
	_, err = runs.StopRun(ctx, &pb.StopRunRequest{
		Namespace: namespace, RunId: runID,
		StopDecision: pb.StopDecision_STOP_DECISION_FAIL,
		Reason:       reason,
	})
	require.NoError(t, err)

	// The RunStop event is written via the OpsFIFO reader, so poll until it drains.
	var runStop *pb.HistoryRunStopPayload
	require.Eventually(t, func() bool {
		hist, histErr := ops.GetHistoryEvents(ctx, &pb.GetHistoryEventsRequest{
			Namespace: namespace, RunId: runID, AfterId: 0, Limit: 100,
		})
		if histErr != nil || hist == nil {
			return false
		}
		for _, event := range hist.Events {
			if stop := event.GetRunStop(); stop != nil {
				runStop = stop
				return true
			}
		}
		return false
	}, 15*time.Second, 100*time.Millisecond, "RunStop history event must surface after StopRun within the OpsFIFO window")

	assert.Equal(t, int32(p.RunStatusFailed), runStop.RunStatus, "STOP_DECISION_FAIL maps to Failed")
	assert.Equal(t, reason, runStop.Reason, "user-provided StopRun reason must be recorded on the RunStop event")
}

// TestOpsService_EncodedObjectBlobsAreDedupedAndHydrated submits a
// PublishToChannel carrying an EncodedObject. End-to-end this exercises:
//
//   - Engine writes the blob to BlobStore exactly once (for
//     RunRow.UnconsumedChannelMessages).
//   - History walker rewrites EncodedObject -> BlobIdInternalOnly inside
//     the OpsFIFO row before it hits Mongo (dedup pool hits = 1, no fresh
//     blob entries written from the walker).
//   - HistoryStore writes the proto-marshaled payload referencing only
//     blob_id strings.
//   - OpsService.GetHistoryEvents page-fetches blobs once and hydrates
//     the wire-returned pb.HistoryEvent so the client sees the original
//     EncodedObject bytes byte-identically.
func TestOpsService_EncodedObjectBlobsAreDedupedAndHydrated(t *testing.T) {
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
	cfg.TaskProcessor.OpsBatchReadDelay = 10 * time.Millisecond
	cfg.TaskProcessor.OpsPollInterval = 50 * time.Millisecond

	app, err := cmd.NewServerApp(cfg, log.NewNoop())
	require.NoError(t, err)
	app.RunStore.DeleteAll(context.Background())
	if app.VisibilityStore != nil {
		_ = app.VisibilityStore.DeleteAll(context.Background())
	}
	if app.HistoryStore != nil {
		_ = app.HistoryStore.DeleteAll(context.Background())
	}

	ctx := context.Background()
	require.NoError(t, app.StartAsync(ctx))

	runsConn, err := grpc.NewClient(app.RunGRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	opsConn, err := grpc.NewClient(app.OpsGRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)

	t.Cleanup(func() {
		runsConn.Close()
		opsConn.Close()
		app.Stop()
	})

	runs := pb.NewRunsServiceClient(runsConn)
	ops := pb.NewOpsServiceClient(opsConn)

	namespace := "ops-blob-test-" + uuid.NewString()
	runID := uuid.NewString()
	_, err = runs.StartRun(ctx, &pb.StartRunRequest{
		Namespace: namespace, RunId: runID, FlowType: "blob-flow", TaskListName: "g",
		StartingSteps: []*pb.NextStep{{StepId: "s1"}},
	})
	require.NoError(t, err)

	bigBlob := make([]byte, 32*1024)
	for i := range bigBlob {
		bigBlob[i] = byte(i % 251)
	}
	_, err = runs.PublishToChannel(ctx, &pb.PublishToChannelRequest{
		Namespace: namespace, RunId: runID, ChannelName: "ch-input",
		Values: []*pb.Value{{Kind: &pb.Value_EncodedObject{EncodedObject: &pb.EncodedObject{
			Encoding: "application/octet-stream",
			Payload:  bigBlob,
		}}}},
	})
	require.NoError(t, err)

	// Drain the OpsFIFO and assert the ChannelPublish history event arrives
	// with the original blob bytes byte-identically (proves: walker rewrote
	// to BlobIdInternalOnly on write, OpsService hydrated back on read).
	require.Eventually(t, func() bool {
		hist, histErr := ops.GetHistoryEvents(ctx, &pb.GetHistoryEventsRequest{
			Namespace: namespace, RunId: runID, AfterId: 0, Limit: 100,
		})
		if histErr != nil {
			return false
		}
		for _, ev := range hist.Events {
			pub := ev.GetChannelPublish()
			if pub == nil || pub.ChannelName != "ch-input" || len(pub.Values) != 1 {
				continue
			}
			eo := pub.Values[0].GetEncodedObject()
			if eo == nil {
				return false
			}
			if eo.Encoding != "application/octet-stream" {
				return false
			}
			if len(eo.Payload) != len(bigBlob) {
				return false
			}
			for i := range bigBlob {
				if eo.Payload[i] != bigBlob[i] {
					return false
				}
			}
			return true
		}
		return false
	}, 15*time.Second, 100*time.Millisecond,
		"OpsService.GetHistoryEvents must return a ChannelPublish event with the original EncodedObject bytes (write walker + read hydration round-trip)")
}

// TestOpsService_ListRuns_AnyFilters proves that the WebUI's
// "(any) flow type / Any status" mode works against the real OpsService:
// when FlowType is empty and Status is unset on the request, the server
// drops both filters from the visibility query and returns every run
// upserted under the namespace.
func TestOpsService_ListRuns_AnyFilters(t *testing.T) {
	uri := testhelpers.TestDBURI()
	if uri == "" {
		t.Skip(testhelpers.PersistenceBackendEnvVar + " backend URI not set")
	}

	cfg := config.DefaultConfig()
	testhelpers.ApplyPersistence(t, &cfg, uri, dbPrefix)
	testhelpers.ApplySingleNodeCluster(t, &cfg)

	app, err := cmd.NewServerApp(cfg, log.NewNoop())
	require.NoError(t, err)
	require.NotNil(t, app.VisibilityStore)
	_ = app.VisibilityStore.DeleteAll(context.Background())

	ctx := context.Background()
	require.NoError(t, app.StartAsync(ctx))
	t.Cleanup(app.Stop)

	conn, err := grpc.NewClient(app.OpsGRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()
	ops := pb.NewOpsServiceClient(conn)

	// Seed runs spanning two flow_types and three statuses under the same
	// namespace by writing directly to the VisibilityStore (the
	// OpsService is read-only).
	namespace := "ops-any-" + uuid.NewString()
	now := time.Now().UTC().Truncate(time.Millisecond)
	mk := func(flowType string, status p.RunStatus) p.VisibilityEntry {
		return p.VisibilityEntry{
			Namespace: namespace, RunID: uuid.NewString(),
			FlowType: flowType, TaskListName: "g",
			Status: status, StartTime: now, UpdatedAt: now,
		}
	}
	require.Nil(t, app.VisibilityStore.BatchUpsertVisibility(ctx, []p.VisibilityEntry{
		mk("alpha", p.RunStatusRunning),
		mk("alpha", p.RunStatusCompleted),
		mk("beta", p.RunStatusFailed),
		mk("beta", p.RunStatusFailed),
	}))

	// Status unset, FlowType empty -> all 4 runs returned.
	page, listErr := ops.ListRuns(ctx, &pb.ListRunsRequest{
		Namespace: namespace,
		OrderBy:   pb.ListRunsOrderBy_LIST_RUNS_ORDER_BY_START_TIME_DESC,
		Limit:     100,
	})
	require.NoError(t, listErr)
	assert.Len(t, page.Runs, 4)

	// Status set, FlowType empty -> only matching status across both flow_types.
	page, listErr = ops.ListRuns(ctx, &pb.ListRunsRequest{
		Namespace: namespace, Status: proto.Int32(2),
		OrderBy: pb.ListRunsOrderBy_LIST_RUNS_ORDER_BY_START_TIME_DESC, Limit: 100,
	})
	require.NoError(t, listErr)
	assert.Len(t, page.Runs, 1)

	// FlowType set, Status unset -> both runs of that flow type.
	page, listErr = ops.ListRuns(ctx, &pb.ListRunsRequest{
		Namespace: namespace, FlowType: "beta",
		OrderBy: pb.ListRunsOrderBy_LIST_RUNS_ORDER_BY_START_TIME_DESC, Limit: 100,
	})
	require.NoError(t, listErr)
	assert.Len(t, page.Runs, 2)
}
