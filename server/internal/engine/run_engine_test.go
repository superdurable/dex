package engine

import (
	"context"
	"os"
	"testing"
	"time"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/server/common/errors"
	"github.com/superdurable/dex/server/common/log"
	"github.com/superdurable/dex/server/common/utils/ids"
	"github.com/superdurable/dex/server/config"
	"github.com/superdurable/dex/server/internal/metrics"
	p "github.com/superdurable/dex/server/internal/persistence"
	"github.com/superdurable/dex/server/internal/persistence/mongo"
	"github.com/superdurable/dex/server/internal/shardmanager"
	"github.com/google/uuid"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testShardManager implements shardmanager.ShardManager for tests.
// GetCappedContext returns the parent context with a generous deadline.
type testShardManager struct {
	immediateSeq int64
	opsFIFOSeq   int64
}

func (m *testShardManager) Start(_ context.Context) error       { return nil }
func (m *testShardManager) Stop()                               {}
func (m *testShardManager) GetOwnedShards() []int32             { return []int32{0, 1} }
func (m *testShardManager) IsLocalShard(_ int32) bool           { return true }
func (m *testShardManager) SignalShardLost(_ int32)             {}
func (m *testShardManager) GetShardOwnerAddress(_ int32) string { return "" }
func (m *testShardManager) GetCappedContext(ctx context.Context, _ int32) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, 30*time.Second)
}
func (m *testShardManager) AcquireImmediateTaskSeqLock(_ int32) (func(), errors.CategorizedError) {
	return func() {}, nil
}
func (m *testShardManager) GetNextImmediateTaskSeq(_ int32) (int64, error) {
	m.immediateSeq++
	return m.immediateSeq, nil
}
func (m *testShardManager) AcquireOpsFIFOTaskSeqLock(_ int32) (func(), errors.CategorizedError) {
	return func() {}, nil
}
func (m *testShardManager) GetNextOpsFIFOTaskSeq(_ int32) (int64, error) {
	m.opsFIFOSeq++
	return m.opsFIFOSeq, nil
}
func (m *testShardManager) GetShardVersion(_ int32) int64                       { return 1 }
func (m *testShardManager) SetMetadataCallback(_ shardmanager.MetadataCallback) {}
func (m *testShardManager) AwaitShardReady(_ context.Context, _ int32) errors.CategorizedError {
	return nil
}

func getTestEngine(t *testing.T) (RunEngine, p.RunStore) {
	uri := os.Getenv("DEX_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("DEX_TEST_MONGO_URI not set")
	}
	ctx := context.Background()
	runStore, err := mongo.NewRunStoreWithDatabase(ctx, uri, testDBName, mongo.DefaultOperationTimeouts())
	require.Nil(t, err)
	runStore.DeleteAll(ctx)
	t.Cleanup(func() { runStore.Close() })

	mapper := shardmanager.NewShardMapper(config.ShardConfig{DefaultShardsForNewNamespaces: 2})
	logger := log.NewNoop()
	sm := &testShardManager{}
	sharded := shardmanager.NewShardedRunStore(runStore, sm, nil)
	runCfg := config.DefaultRunServiceConfig()
	engine := NewRunEngine(&runCfg, sharded, nil, mapper, sm, logger)
	return engine, runStore
}

func testMapper() shardmanager.ShardMapper {
	return shardmanager.NewShardMapper(config.ShardConfig{DefaultShardsForNewNamespaces: 2})
}

func nullPbValue() *pb.Value {
	return &pb.Value{Kind: &pb.Value_NullValue{NullValue: pb.NullValue_NULL_VALUE}}
}

func initMetricsForTest(t *testing.T) {
	t.Helper()

	cfg := config.DefaultMetricsConfig()
	cfg.Provider = config.MetricsProviderPrometheus
	cfg.MetricPrefix = "test_dex_"
	cfg.Prometheus.ListenAddress = ""

	require.NoError(t, metrics.Initialize(context.Background(), &cfg, log.NewNoop()))
	t.Cleanup(func() {
		_ = metrics.Close(context.Background())
	})
}

func metricFamilyBySubstring(t *testing.T, families []*dto.MetricFamily, needle string) *dto.MetricFamily {
	t.Helper()
	for _, family := range families {
		if family.GetName() == needle {
			return family
		}
	}
	require.FailNow(t, "metric family not found", needle)
	return nil
}

func intPbValue(v int64) *pb.Value {
	return &pb.Value{Kind: &pb.Value_IntValue{IntValue: v}}
}

// ============================================================================
// StartRun Tests
// ============================================================================

func TestStartRun_HappyPath(t *testing.T) {
	eng, runStore := getTestEngine(t)
	ctx := context.Background()
	runID := uuid.NewString()

	err := eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: "test-ns", RunId: runID, FlowType: "test", TaskListName: "g",
	})
	require.Nil(t, err)

	shardID := testMapper().GetShardID("test-ns", runID)
	run, gErr := runStore.GetRun(ctx, shardID, "test-ns", runID, p.GetRunOptions{})
	require.Nil(t, gErr)
	assert.Equal(t, p.RunStatusPending, run.Status)
	assert.Equal(t, int64(1), run.Version)
}

func TestStartRun_EmitsAttemptStartedMetric(t *testing.T) {
	initMetricsForTest(t)

	eng, _ := getTestEngine(t)
	ctx := context.Background()
	runID := uuid.NewString()

	err := eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: "test-ns", RunId: runID, FlowType: "test", TaskListName: "g",
	})
	require.Nil(t, err)

	families, gatherErr := metrics.PrometheusRegistry().Gather()
	require.NoError(t, gatherErr)

	counterFamily := metricFamilyBySubstring(t, families, "test_dex_run_attempt_started_counter_total")
	require.Len(t, counterFamily.Metric, 1)
	require.Equal(t, 1.0, counterFamily.Metric[0].Counter.GetValue())
}

func TestStartRun_DuplicateRunID(t *testing.T) {
	eng, _ := getTestEngine(t)
	ctx := context.Background()
	runID := uuid.NewString()

	err := eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: "test-ns", RunId: runID, FlowType: "test", TaskListName: "g",
	})
	require.Nil(t, err)

	err2 := eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: "test-ns", RunId: runID, FlowType: "test", TaskListName: "g",
	})
	require.NotNil(t, err2)
}

// ============================================================================
// ProcessStepExecuteCompleted Tests
// ============================================================================

func TestExecuteCompleted_StateUpsert(t *testing.T) {
	eng, runStore := getTestEngine(t)
	ctx := context.Background()
	runID := uuid.NewString()

	require.Nil(t, eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: "test-ns", RunId: runID, FlowType: "test", TaskListName: "g",
	}))

	shardID := testMapper().GetShardID("test-ns", runID)

	// Set up a running state with an active step
	run, _ := runStore.GetRun(ctx, shardID, "test-ns", runID, p.GetRunOptions{})
	running := p.RunStatusRunning
	runStore.UpdateRunWithNewTasks(ctx, shardID, "test-ns", runID, run.Version, &p.RunRowUpdate{
		Status: &running,
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"step-1": {Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusInvokingExecute},
		},
	}, nil)

	run, _ = runStore.GetRun(ctx, shardID, "test-ns", runID, p.GetRunOptions{})

	_, err := eng.ProcessStepExecuteCompleted(ctx, shardID, "test-ns", &pb.StepExecuteCompletedRequest{
		RunId: runID, StepExeId: "step-1",
		Context:       &pb.WorkerCallContext{WorkerRequestCounter: run.WorkerRequestCounter + 1},
		StopDecision:  pb.StopDecision_STOP_DECISION_COMPLETE,
		StateToUpsert: map[string]*pb.Value{"counter": intPbValue(42)},
	})
	require.Nil(t, err)

	updated, _ := runStore.GetRun(ctx, shardID, "test-ns", runID, p.GetRunOptions{})
	assert.Equal(t, p.RunStatusCompleted, updated.Status)
	assert.NotNil(t, updated.StateMap["counter"].IntVal)
	assert.Equal(t, int64(42), *updated.StateMap["counter"].IntVal)
}

func TestExecuteCompleted_EmitsRunSuccessMetrics(t *testing.T) {
	initMetricsForTest(t)

	eng, runStore := getTestEngine(t)
	ctx := context.Background()
	runID := uuid.NewString()

	require.Nil(t, eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: "test-ns", RunId: runID, FlowType: "test", TaskListName: "g",
	}))

	shardID := testMapper().GetShardID("test-ns", runID)
	run, gErr := runStore.GetRun(ctx, shardID, "test-ns", runID, p.GetRunOptions{})
	require.Nil(t, gErr)

	running := p.RunStatusRunning
	updateErr := runStore.UpdateRunWithNewTasks(ctx, shardID, "test-ns", runID, run.Version, &p.RunRowUpdate{
		Status: &running,
	}, nil)
	require.Nil(t, updateErr)

	_, completeErr := eng.ProcessStepExecuteCompleted(ctx, shardID, "test-ns", &pb.StepExecuteCompletedRequest{
		RunId:        runID,
		StepExeId:    "missing-step-is-ok-for-metric-path",
		Context:      &pb.WorkerCallContext{WorkerRequestCounter: 1},
		StopDecision: pb.StopDecision_STOP_DECISION_COMPLETE,
	})
	require.Nil(t, completeErr)

	families, gatherErr := metrics.PrometheusRegistry().Gather()
	require.NoError(t, gatherErr)

	successFamily := metricFamilyBySubstring(t, families, "test_dex_run_success_counter_total")
	require.Len(t, successFamily.Metric, 1)
	require.Equal(t, 1.0, successFamily.Metric[0].Counter.GetValue())

	latencyFamily := metricFamilyBySubstring(t, families, "test_dex_run_execution_latency")
	require.Len(t, latencyFamily.Metric, 1)
	require.Greater(t, latencyFamily.Metric[0].Histogram.GetSampleCount(), uint64(0))
}

func TestExecuteCompleted_Idempotent(t *testing.T) {
	eng, runStore := getTestEngine(t)
	ctx := context.Background()
	runID := uuid.NewString()

	require.Nil(t, eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: "test-ns", RunId: runID, FlowType: "test", TaskListName: "g",
	}))

	shardID := testMapper().GetShardID("test-ns", runID)
	run, _ := runStore.GetRun(ctx, shardID, "test-ns", runID, p.GetRunOptions{})
	running := p.RunStatusRunning
	runStore.UpdateRunWithNewTasks(ctx, shardID, "test-ns", runID, run.Version, &p.RunRowUpdate{
		Status: &running,
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"step-1": {Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusInvokingExecute},
		},
	}, nil)

	run, _ = runStore.GetRun(ctx, shardID, "test-ns", runID, p.GetRunOptions{})
	counter := run.WorkerRequestCounter + 1

	// First call
	_, err := eng.ProcessStepExecuteCompleted(ctx, shardID, "test-ns", &pb.StepExecuteCompletedRequest{
		RunId: runID, StepExeId: "step-1",
		Context:      &pb.WorkerCallContext{WorkerRequestCounter: counter},
		StopDecision: pb.StopDecision_STOP_DECISION_COMPLETE,
	})
	require.Nil(t, err)

	// Duplicate (same counter) should be no-op
	_, err2 := eng.ProcessStepExecuteCompleted(ctx, shardID, "test-ns", &pb.StepExecuteCompletedRequest{
		RunId: runID, StepExeId: "step-1",
		Context:      &pb.WorkerCallContext{WorkerRequestCounter: counter},
		StopDecision: pb.StopDecision_STOP_DECISION_COMPLETE,
	})
	require.Nil(t, err2)
}

func TestExecuteCompleted_NextSteps(t *testing.T) {
	eng, runStore := getTestEngine(t)
	ctx := context.Background()
	runID := uuid.NewString()

	require.Nil(t, eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: "test-ns", RunId: runID, FlowType: "test", TaskListName: "g",
	}))

	shardID := testMapper().GetShardID("test-ns", runID)
	run, _ := runStore.GetRun(ctx, shardID, "test-ns", runID, p.GetRunOptions{})
	running := p.RunStatusRunning
	runStore.UpdateRunWithNewTasks(ctx, shardID, "test-ns", runID, run.Version, &p.RunRowUpdate{
		Status: &running,
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"init-1": {Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusInvokingExecute},
		},
		StepExeIDCounters: map[string]int32{"init": 1},
	}, nil)

	run, _ = runStore.GetRun(ctx, shardID, "test-ns", runID, p.GetRunOptions{})

	_, err := eng.ProcessStepExecuteCompleted(ctx, shardID, "test-ns", &pb.StepExecuteCompletedRequest{
		RunId: runID, StepExeId: "init-1",
		Context:      &pb.WorkerCallContext{WorkerRequestCounter: run.WorkerRequestCounter + 1},
		StopDecision: pb.StopDecision_STOP_DECISION_NONE,
		NextSteps:    []*pb.NextStep{{StepId: "process", Input: nullPbValue(), WaitForMethodExeId: 2}},
	})
	require.Nil(t, err)

	updated, _ := runStore.GetRun(ctx, shardID, "test-ns", runID, p.GetRunOptions{})
	assert.Equal(t, p.RunStatusRunning, updated.Status)
	// init-1 removed, process-1 added
	_, hasInit := updated.ActiveStepExecutions["init-1"]
	assert.False(t, hasInit)
	processStep, hasProcess := updated.ActiveStepExecutions["process-1"]
	assert.True(t, hasProcess)
	assert.Equal(t, p.StepExeStatusInvokingWaitFor, processStep.Status)
	assert.Equal(t, int64(2), processStep.WaitForMethodExeID)
	assert.Equal(t, int32(1), updated.StepExeIDCounters["process"])
}

func TestExecuteCompleted_AllWaiting_CreatesTimer(t *testing.T) {
	eng, runStore := getTestEngine(t)
	ctx := context.Background()
	runID := uuid.NewString()

	require.Nil(t, eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: "test-ns", RunId: runID, FlowType: "test", TaskListName: "g",
	}))

	shardID := testMapper().GetShardID("test-ns", runID)
	run, _ := runStore.GetRun(ctx, shardID, "test-ns", runID, p.GetRunOptions{})
	running := p.RunStatusRunning
	fireAt := time.Now().Add(1 * time.Hour).UnixMilli()
	runStore.UpdateRunWithNewTasks(ctx, shardID, "test-ns", runID, run.Version, &p.RunRowUpdate{
		Status: &running,
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"wait-1": {
				Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusWaitingForCondition,
				WaitForCondition: &p.WaitForCondition{Type: p.WaitTypeAnyOf,
					Conditions: []p.SingleCondition{{Timer: &p.TimerCondition{FireAtUnixMs: fireAt}}}},
			},
			"exec-1": {Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusInvokingExecute},
		},
	}, nil)

	run, _ = runStore.GetRun(ctx, shardID, "test-ns", runID, p.GetRunOptions{})

	// Complete exec-1, leaving only wait-1 (which is waiting_for_condition)
	_, err := eng.ProcessStepExecuteCompleted(ctx, shardID, "test-ns", &pb.StepExecuteCompletedRequest{
		RunId: runID, StepExeId: "exec-1",
		Context:      &pb.WorkerCallContext{WorkerRequestCounter: run.WorkerRequestCounter + 1},
		StopDecision: pb.StopDecision_STOP_DECISION_DEAD_END,
	})
	require.Nil(t, err)

	updated, _ := runStore.GetRun(ctx, shardID, "test-ns", runID, p.GetRunOptions{})
	assert.Equal(t, p.RunStatusRunning, updated.Status)
	workerID := "worker-exec-all-waiting"
	require.Nil(t, runStore.UpdateRunWithNewTasks(ctx, shardID, "test-ns", runID, updated.Version, &p.RunRowUpdate{
		WorkerID: &workerID,
	}, nil))
	updated, _ = runStore.GetRun(ctx, shardID, "test-ns", runID, p.GetRunOptions{})
	_, err = eng.ProcessReleaseRun(ctx, shardID, &pb.ProcessReleaseRunRequest{
		Namespace:     "test-ns",
		RunId:         runID,
		WorkerId:      workerID,
		ReleaseReason: pb.ReleaseRunReason_RELEASE_RUN_REASON_ALL_STEPS_WAITING,
		Context: &pb.WorkerCallContext{
			WorkerId:                             workerID,
			WorkerRequestCounter:                 updated.WorkerRequestCounter + 1,
			LastReceivedExternalChannelMessageId: updated.ExternalChannelMessageCounter,
		},
	})
	require.Nil(t, err)

	updated, _ = runStore.GetRun(ctx, shardID, "test-ns", runID, p.GetRunOptions{})
	assert.Equal(t, p.RunStatusAllStepsWaitingForConditions, updated.Status)
	assert.NotEmpty(t, updated.ActiveDurableTimerID)
}

// ============================================================================
// Channel Publish Tests (server does NOT reevaluate — worker's responsibility)
// ============================================================================

func TestExecuteCompleted_ChannelPublishStored_NoServerReevaluation(t *testing.T) {
	eng, runStore := getTestEngine(t)
	ctx := context.Background()
	runID := uuid.NewString()

	require.Nil(t, eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: "test-ns", RunId: runID, FlowType: "test", TaskListName: "g",
	}))

	shardID := testMapper().GetShardID("test-ns", runID)

	// step-A is invoking_execute, step-B is waiting on channel "notify"
	run, _ := runStore.GetRun(ctx, shardID, "test-ns", runID, p.GetRunOptions{})
	running := p.RunStatusRunning
	runStore.UpdateRunWithNewTasks(ctx, shardID, "test-ns", runID, run.Version, &p.RunRowUpdate{
		Status: &running,
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"stepA-1": {Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusInvokingExecute},
			"stepB-1": {Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusWaitingForCondition,
				WaitForCondition: &p.WaitForCondition{Type: p.WaitTypeAnyOf,
					Conditions: []p.SingleCondition{{Channel: &p.ChannelCondition{ChannelName: "notify", Min: 1}}}}},
		},
	}, nil)

	run, _ = runStore.GetRun(ctx, shardID, "test-ns", runID, p.GetRunOptions{})

	// step-A completes with DeadEnd and publishes to "notify".
	//
	//Server stores the message but does NOT reevaluate step-B (worker's job).
	_, err := eng.ProcessStepExecuteCompleted(ctx, shardID, "test-ns", &pb.StepExecuteCompletedRequest{
		RunId: runID, StepExeId: "stepA-1",
		Context:        &pb.WorkerCallContext{WorkerRequestCounter: run.WorkerRequestCounter + 1},
		StopDecision:   pb.StopDecision_STOP_DECISION_DEAD_END,
		ChannelPublish: []*pb.ChannelPublish{{ChannelName: "notify", Values: []*pb.Value{intPbValue(99)}}},
	})
	require.Nil(t, err)

	updated, _ := runStore.GetRun(ctx, shardID, "test-ns", runID, p.GetRunOptions{})
	_, hasA := updated.ActiveStepExecutions["stepA-1"]
	assert.False(t, hasA) // step-A removed (DeadEnd)
	stepB, hasB := updated.ActiveStepExecutions["stepB-1"]
	assert.True(t, hasB)
	assert.Equal(t, p.StepExeStatusWaitingForCondition, stepB.Status)
	assert.Equal(t, p.RunStatusRunning, updated.Status)
	assert.Len(t, updated.UnconsumedChannelMessages["notify"], 1)
}

func TestWaitForCompleted_ChannelPublishStored_NoServerReevaluation(t *testing.T) {
	eng, runStore := getTestEngine(t)
	ctx := context.Background()
	runID := uuid.NewString()

	require.Nil(t, eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: "test-ns", RunId: runID, FlowType: "test", TaskListName: "g",
	}))

	shardID := testMapper().GetShardID("test-ns", runID)

	// step-A is invoking_wait_for, step-B is waiting on channel "data"
	run, _ := runStore.GetRun(ctx, shardID, "test-ns", runID, p.GetRunOptions{})
	running := p.RunStatusRunning
	runStore.UpdateRunWithNewTasks(ctx, shardID, "test-ns", runID, run.Version, &p.RunRowUpdate{
		Status: &running,
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"stepA-1": {Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusInvokingWaitFor},
			"stepB-1": {Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusWaitingForCondition,
				WaitForCondition: &p.WaitForCondition{Type: p.WaitTypeAnyOf,
					Conditions: []p.SingleCondition{{Channel: &p.ChannelCondition{ChannelName: "data", Min: 1}}}}},
		},
	}, nil)

	run, _ = runStore.GetRun(ctx, shardID, "test-ns", runID, p.GetRunOptions{})
	futureTimer := time.Now().Add(1 * time.Hour).UnixMilli()

	// step-A's WaitFor completes and publishes to "data" channel
	_, err := eng.ProcessStepWaitForCompleted(ctx, shardID, "test-ns", &pb.StepWaitForCompletedRequest{
		RunId: runID, StepExeId: "stepA-1",
		Context: &pb.WorkerCallContext{WorkerRequestCounter: run.WorkerRequestCounter + 1},
		WaitForCondition: &pb.WaitForCondition{
			Type:       pb.WaitType_WAIT_TYPE_ANY_OF,
			Conditions: []*pb.SingleCondition{{Condition: &pb.SingleCondition_Timer{Timer: &pb.TimerCondition{FireAtUnixMs: futureTimer}}}},
		},
		ChannelPublish: []*pb.ChannelPublish{{ChannelName: "data", Values: []*pb.Value{intPbValue(42)}}},
	})
	require.Nil(t, err)

	updated, _ := runStore.GetRun(ctx, shardID, "test-ns", runID, p.GetRunOptions{})
	// step-A should be waiting_for_condition (its own WaitFor timer)
	stepA, hasA := updated.ActiveStepExecutions["stepA-1"]
	assert.True(t, hasA)
	assert.Equal(t, p.StepExeStatusWaitingForCondition, stepA.Status)
	// step-B promoted by boundary sweep with reservation persisted.
	stepB, hasB := updated.ActiveStepExecutions["stepB-1"]
	assert.True(t, hasB)
	assert.Equal(t, p.StepExeStatusWaitingForCondition, stepB.Status)
	assert.Equal(t, p.RunStatusRunning, updated.Status)
	assert.Len(t, updated.UnconsumedChannelMessages["data"], 1)
}

// ============================================================================
// HandleStepWaitForTimerFired Tests
// ============================================================================

func TestStepTimerFired_AnyOf_SatisfiesAndResumes(t *testing.T) {
	eng, runStore := getTestEngine(t)
	ctx := context.Background()
	runID := uuid.NewString()

	require.Nil(t, eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: "test-ns", RunId: runID, FlowType: "test", TaskListName: "g",
	}))

	shardID := testMapper().GetShardID("test-ns", runID)
	run, _ := runStore.GetRun(ctx, shardID, "test-ns", runID, p.GetRunOptions{})
	timerID := ids.NewTaskID()
	fireAt := int64(1000) // in the past
	allWaiting := p.RunStatusAllStepsWaitingForConditions
	runStore.UpdateRunWithNewTasks(ctx, shardID, "test-ns", runID, run.Version, &p.RunRowUpdate{
		Status: &allWaiting, ActiveDurableTimerID: &timerID, DurableTimerFireAt: &fireAt,
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"wait-1": {Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusWaitingForCondition,
				WaitForCondition: &p.WaitForCondition{Type: p.WaitTypeAnyOf,
					Conditions: []p.SingleCondition{{Timer: &p.TimerCondition{FireAtUnixMs: fireAt}}}}},
		},
	}, nil)

	err := eng.HandleStepWaitForTimerFired(ctx, shardID, &StepWaitForTimerFiredRequest{
		RunID: runID, Namespace: "test-ns", TimerID: timerID,
		FireAtUnixMs: fireAt,
	})
	require.Nil(t, err)

	updated, _ := runStore.GetRun(ctx, shardID, "test-ns", runID, p.GetRunOptions{})
	assert.Equal(t, p.RunStatusPending, updated.Status)
	step, exists := updated.ActiveStepExecutions["wait-1"]
	assert.True(t, exists)
	assert.Equal(t, p.StepExeStatusInvokingExecute, step.Status)
	assert.NotZero(t, step.ExecuteMethodExeID)
}

func TestStepTimerFired_AllOf_TimerSatisfied_ChannelNot(t *testing.T) {
	eng, runStore := getTestEngine(t)
	ctx := context.Background()
	runID := uuid.NewString()

	require.Nil(t, eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: "test-ns", RunId: runID, FlowType: "test", TaskListName: "g",
	}))

	shardID := testMapper().GetShardID("test-ns", runID)
	run, _ := runStore.GetRun(ctx, shardID, "test-ns", runID, p.GetRunOptions{})
	timerID := ids.NewTaskID()
	fireAt := int64(1000)
	allWaiting := p.RunStatusAllStepsWaitingForConditions
	// AllOf: timer(past) + channel("ch1", min=1) — channel not satisfied
	runStore.UpdateRunWithNewTasks(ctx, shardID, "test-ns", runID, run.Version, &p.RunRowUpdate{
		Status: &allWaiting, ActiveDurableTimerID: &timerID, DurableTimerFireAt: &fireAt,
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"wait-1": {Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusWaitingForCondition,
				WaitForCondition: &p.WaitForCondition{Type: p.WaitTypeAllOf,
					Conditions: []p.SingleCondition{
						{Timer: &p.TimerCondition{FireAtUnixMs: fireAt}},
						{Channel: &p.ChannelCondition{ChannelName: "ch1", Min: 1}},
					}}},
		},
	}, nil)

	err := eng.HandleStepWaitForTimerFired(ctx, shardID, &StepWaitForTimerFiredRequest{
		RunID: runID, Namespace: "test-ns", TimerID: timerID,
		FireAtUnixMs: fireAt,
	})
	require.Nil(t, err)

	updated, _ := runStore.GetRun(ctx, shardID, "test-ns", runID, p.GetRunOptions{})
	// Still all_steps_waiting: timer fired but channel not satisfied for AllOf
	assert.Equal(t, p.RunStatusAllStepsWaitingForConditions, updated.Status)
	step, exists := updated.ActiveStepExecutions["wait-1"]
	assert.True(t, exists)
	assert.Equal(t, p.StepExeStatusWaitingForCondition, step.Status)
}

func TestStepTimerFired_AllOf_BothSatisfied(t *testing.T) {
	eng, runStore := getTestEngine(t)
	ctx := context.Background()
	runID := uuid.NewString()

	require.Nil(t, eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: "test-ns", RunId: runID, FlowType: "test", TaskListName: "g",
	}))

	shardID := testMapper().GetShardID("test-ns", runID)
	run, _ := runStore.GetRun(ctx, shardID, "test-ns", runID, p.GetRunOptions{})
	timerID := ids.NewTaskID()
	fireAt := int64(1000)
	allWaiting := p.RunStatusAllStepsWaitingForConditions
	v1 := int64(42)
	// AllOf: timer(past) + channel("ch1", min=1) — both satisfied
	runStore.UpdateRunWithNewTasks(ctx, shardID, "test-ns", runID, run.Version, &p.RunRowUpdate{
		Status: &allWaiting, ActiveDurableTimerID: &timerID, DurableTimerFireAt: &fireAt,
		ReplaceUnconsumedChannels: map[string][]p.ChannelMessage{"ch1": {{ID: 1, Value: p.Value{Type: p.ValueTypeInt, IntVal: &v1}}}},
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"wait-1": {Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusWaitingForCondition,
				WaitForCondition: &p.WaitForCondition{Type: p.WaitTypeAllOf,
					Conditions: []p.SingleCondition{
						{Timer: &p.TimerCondition{FireAtUnixMs: fireAt}},
						{Channel: &p.ChannelCondition{ChannelName: "ch1", Min: 1}},
					}}},
		},
	}, nil)

	err := eng.HandleStepWaitForTimerFired(ctx, shardID, &StepWaitForTimerFiredRequest{
		RunID: runID, Namespace: "test-ns", TimerID: timerID,
		FireAtUnixMs: fireAt,
	})
	require.Nil(t, err)

	updated, _ := runStore.GetRun(ctx, shardID, "test-ns", runID, p.GetRunOptions{})
	assert.Equal(t, p.RunStatusPending, updated.Status)
	step, exists := updated.ActiveStepExecutions["wait-1"]
	assert.True(t, exists)
	assert.Equal(t, p.StepExeStatusInvokingExecute, step.Status)
	assert.NotZero(t, step.ExecuteMethodExeID)
	// Reservation keeps messages in queue until Execute completes.
	assert.Len(t, updated.UnconsumedChannelMessages["ch1"], 1)
}

func TestStepTimerFired_StepAlreadyTransitioned(t *testing.T) {
	eng, runStore := getTestEngine(t)
	ctx := context.Background()
	runID := uuid.NewString()

	require.Nil(t, eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: "test-ns", RunId: runID, FlowType: "test", TaskListName: "g",
	}))

	shardID := testMapper().GetShardID("test-ns", runID)
	run, _ := runStore.GetRun(ctx, shardID, "test-ns", runID, p.GetRunOptions{})
	timerID := ids.NewTaskID()
	allWaiting := p.RunStatusAllStepsWaitingForConditions
	// wait-1 has a timer, wait-2 has a later timer
	origFireAt := int64(1000)
	futureFireAt := time.Now().Add(2 * time.Hour).UnixMilli()
	runStore.UpdateRunWithNewTasks(ctx, shardID, "test-ns", runID, run.Version, &p.RunRowUpdate{
		Status: &allWaiting, ActiveDurableTimerID: &timerID, DurableTimerFireAt: &origFireAt,
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"wait-1": {Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusInvokingExecute}, // already transitioned!
			"wait-2": {Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusWaitingForCondition,
				WaitForCondition: &p.WaitForCondition{Type: p.WaitTypeAnyOf,
					Conditions: []p.SingleCondition{{Timer: &p.TimerCondition{FireAtUnixMs: futureFireAt}}}}},
		},
	}, nil)

	// Timer fires but wait-1 is already transitioned. wait-2 has a future timer.
	// The existing durable timer (fireAt=1000) is <= wait-2's fireAt, so lazy
	// reuse keeps the same timer — no new timer is created.
	err := eng.HandleStepWaitForTimerFired(ctx, shardID, &StepWaitForTimerFiredRequest{
		RunID: runID, Namespace: "test-ns", TimerID: timerID,
		FireAtUnixMs: origFireAt,
	})
	require.Nil(t, err)

	updated, _ := runStore.GetRun(ctx, shardID, "test-ns", runID, p.GetRunOptions{})
	// A brand-new durable timer is armed (not the consumed/dangling one) for
	// wait-2's future fire time.
	assert.NotEmpty(t, updated.ActiveDurableTimerID)
	assert.NotEqual(t, timerID, updated.ActiveDurableTimerID)
	assert.Equal(t, futureFireAt, updated.DurableTimerFireAt)
	// Run stays in AllStepsWaiting (no step satisfied yet).
	assert.Equal(t, p.RunStatusAllStepsWaitingForConditions, updated.Status)
}

func TestStepTimerFired_StaleTimer(t *testing.T) {
	eng, _ := getTestEngine(t)
	ctx := context.Background()
	runID := uuid.NewString()

	require.Nil(t, eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: "test-ns", RunId: runID, FlowType: "test", TaskListName: "g",
	}))

	shardID := testMapper().GetShardID("test-ns", runID)

	// Timer fires but run is not in all_steps_waiting (still pending)
	err := eng.HandleStepWaitForTimerFired(ctx, shardID, &StepWaitForTimerFiredRequest{
		RunID: runID, Namespace: "test-ns", TimerID: ids.NewTaskID(),
		FireAtUnixMs: 1000,
	})
	require.Nil(t, err) // no-op, no error
}

// ============================================================================
// GetRun Tests
// ============================================================================

func TestGetRun_HappyPath(t *testing.T) {
	eng, _ := getTestEngine(t)
	ctx := context.Background()
	runID := uuid.NewString()
	ns := "test-ns"

	require.Nil(t, eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: ns, RunId: runID, FlowType: "myflow", TaskListName: "g1",
	}))

	resp, err := eng.GetRun(ctx, ns, runID, nil)
	require.Nil(t, err)
	require.True(t, resp.Found)
	assert.Equal(t, runID, resp.RunId)
	assert.Equal(t, ns, resp.Namespace)
	assert.Equal(t, "myflow", resp.FlowType)
	assert.Equal(t, "g1", resp.TaskListName)
	assert.Equal(t, int32(p.RunStatusPending), resp.Status)
	assert.Greater(t, resp.Version, int64(0))
	assert.Greater(t, resp.ServerTimestampMs, int64(0))
	assert.False(t, resp.DurableTimerFired)
}

func TestGetRun_StatusFilterMatch(t *testing.T) {
	eng, _ := getTestEngine(t)
	ctx := context.Background()
	runID := uuid.NewString()
	ns := "test-ns"

	require.Nil(t, eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: ns, RunId: runID, FlowType: "test", TaskListName: "g",
	}))

	// Pending matches filter [Pending, WaitingForWorker]
	resp, err := eng.GetRun(ctx, ns, runID, []p.RunStatus{
		p.RunStatusPending, p.RunStatusWaitingForWorker,
	})
	require.Nil(t, err)
	assert.True(t, resp.Found)
}

func TestGetRun_StatusFilterNoMatch(t *testing.T) {
	eng, _ := getTestEngine(t)
	ctx := context.Background()
	runID := uuid.NewString()
	ns := "test-ns"

	require.Nil(t, eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: ns, RunId: runID, FlowType: "test", TaskListName: "g",
	}))

	// Run is Pending, filter for Running only -> not found
	resp, err := eng.GetRun(ctx, ns, runID, []p.RunStatus{p.RunStatusRunning})
	require.Nil(t, err)
	assert.False(t, resp.Found)
}

func TestGetRun_NotFound(t *testing.T) {
	eng, _ := getTestEngine(t)
	ctx := context.Background()

	resp, err := eng.GetRun(ctx, "test-ns", "nonexistent-"+uuid.NewString(), nil)
	require.Nil(t, err)
	assert.False(t, resp.Found)
}

// ============================================================================
// StopRun Tests
// ============================================================================

func TestStopRun_NotFound(t *testing.T) {
	eng, _ := getTestEngine(t)
	ctx := context.Background()

	wasActive, taskListName, _, err := eng.StopRun(ctx, "test-ns", "nonexistent-"+uuid.NewString(), pb.StopDecision_STOP_DECISION_COMPLETE, "")
	require.NotNil(t, err)
	assert.True(t, err.IsNotFoundError())
	assert.False(t, wasActive)
	assert.Equal(t, "", taskListName)
}

func TestStopRun_AlreadyTerminal(t *testing.T) {
	cases := []p.RunStatus{p.RunStatusCompleted, p.RunStatusFailed}
	for _, terminal := range cases {
		t.Run(terminal.Name(), func(t *testing.T) {
			eng, runStore := getTestEngine(t)
			ctx := context.Background()
			runID := uuid.NewString()
			ns := "test-ns"

			require.Nil(t, eng.StartRun(ctx, &pb.StartRunRequest{
				Namespace: ns, RunId: runID, FlowType: "test", TaskListName: "g",
			}))
			shardID := testMapper().GetShardID(ns, runID)

			// Force terminal status directly via the store.
			run, _ := runStore.GetRun(ctx, shardID, ns, runID, p.GetRunOptions{})
			term := terminal
			require.Nil(t, runStore.UpdateRunWithNewTasks(ctx, shardID, ns, runID, run.Version,
				&p.RunRowUpdate{Status: &term}, nil))

			wasActive, taskListName, _, err := eng.StopRun(ctx, ns, runID, pb.StopDecision_STOP_DECISION_COMPLETE, "")
			require.Nil(t, err)
			assert.False(t, wasActive)
			assert.Equal(t, "g", taskListName)

			after, _ := runStore.GetRun(ctx, shardID, ns, runID, p.GetRunOptions{})
			assert.Equal(t, terminal, after.Status, "status should be unchanged")
		})
	}
}

func TestStopRun_FromActiveStatus(t *testing.T) {
	srcCases := []p.RunStatus{
		p.RunStatusPending,
		p.RunStatusWaitingForWorker,
		p.RunStatusRunning,
		p.RunStatusAllStepsWaitingForConditions,
	}
	stopCases := []struct {
		stopDecision pb.StopDecision
		wantStatus   p.RunStatus
	}{
		{pb.StopDecision_STOP_DECISION_COMPLETE, p.RunStatusCompleted},
		{pb.StopDecision_STOP_DECISION_FAIL, p.RunStatusFailed},
	}
	for _, src := range srcCases {
		for _, stopCase := range stopCases {
			t.Run(src.Name()+"_"+stopCase.wantStatus.Name(), func(t *testing.T) {
				eng, runStore := getTestEngine(t)
				ctx := context.Background()
				runID := uuid.NewString()
				ns := "test-ns"

				require.Nil(t, eng.StartRun(ctx, &pb.StartRunRequest{
					Namespace: ns, RunId: runID, FlowType: "test", TaskListName: "g",
				}))
				shardID := testMapper().GetShardID(ns, runID)

				if src != p.RunStatusPending {
					run, _ := runStore.GetRun(ctx, shardID, ns, runID, p.GetRunOptions{})
					s := src
					timerID := ids.NewTaskID()
					require.Nil(t, runStore.UpdateRunWithNewTasks(ctx, shardID, ns, runID, run.Version,
						&p.RunRowUpdate{Status: &s, HeartbeatTimerID: &timerID}, nil))
				}

				wasActive, taskListName, _, err := eng.StopRun(ctx, ns, runID, stopCase.stopDecision, "")
				require.Nil(t, err)
				assert.True(t, wasActive)
				assert.Equal(t, "g", taskListName)

				after, _ := runStore.GetRun(ctx, shardID, ns, runID, p.GetRunOptions{})
				assert.Equal(t, stopCase.wantStatus, after.Status)
				assert.True(t, after.HeartbeatTimerID.IsZero(), "heartbeat timer ID should be cleared")
			})
		}
	}
}

func TestStopRun_Idempotent(t *testing.T) {
	eng, runStore := getTestEngine(t)
	ctx := context.Background()
	runID := uuid.NewString()
	ns := "test-ns"

	require.Nil(t, eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: ns, RunId: runID, FlowType: "test", TaskListName: "g",
	}))
	shardID := testMapper().GetShardID(ns, runID)

	wasActive1, _, _, err := eng.StopRun(ctx, ns, runID, pb.StopDecision_STOP_DECISION_COMPLETE, "")
	require.Nil(t, err)
	assert.True(t, wasActive1)

	wasActive2, gid2, _, err2 := eng.StopRun(ctx, ns, runID, pb.StopDecision_STOP_DECISION_COMPLETE, "")
	require.Nil(t, err2)
	assert.False(t, wasActive2, "second StopRun on terminal run should return wasActive=false")
	assert.Equal(t, "g", gid2)

	after, _ := runStore.GetRun(ctx, shardID, ns, runID, p.GetRunOptions{})
	assert.Equal(t, p.RunStatusCompleted, after.Status)
}

// TestStopRun_WithReasonFailsRun verifies StopRun(FAIL, reason) succeeds and
// transitions the run to Failed. The reason is recorded on the RunStop history
// event; that end-to-end assertion lives in the opsservice integration test
// (this harness has no HistoryStore / OpsFIFO reader to read it back).
func TestStopRun_WithReasonFailsRun(t *testing.T) {
	eng, runStore := getTestEngine(t)
	ctx := context.Background()
	runID := uuid.NewString()
	ns := "test-ns"

	require.Nil(t, eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: ns, RunId: runID, FlowType: "test", TaskListName: "g",
	}))

	_, _, _, err := eng.StopRun(ctx, ns, runID, pb.StopDecision_STOP_DECISION_FAIL, "user cancelled deployment")
	require.Nil(t, err)

	shardID := testMapper().GetShardID(ns, runID)
	after, _ := runStore.GetRun(ctx, shardID, ns, runID, p.GetRunOptions{})
	assert.Equal(t, p.RunStatusFailed, after.Status)
}

func TestStopRun_InvalidStopDecision(t *testing.T) {
	eng, _ := getTestEngine(t)
	ctx := context.Background()
	runID := uuid.NewString()
	ns := "test-ns"

	require.Nil(t, eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: ns, RunId: runID, FlowType: "test", TaskListName: "g",
	}))

	_, _, _, err := eng.StopRun(ctx, ns, runID, pb.StopDecision_STOP_DECISION_NONE, "")
	require.NotNil(t, err)
	assert.True(t, err.IsInvalidInputError())
}

func TestStopRun_ReasonTooLong(t *testing.T) {
	eng, _ := getTestEngine(t)
	ctx := context.Background()
	runID := uuid.NewString()
	ns := "test-ns"

	require.Nil(t, eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: ns, RunId: runID, FlowType: "test", TaskListName: "g",
	}))

	longReason := string(make([]byte, 2049))
	_, _, _, err := eng.StopRun(ctx, ns, runID, pb.StopDecision_STOP_DECISION_COMPLETE, longReason)
	require.NotNil(t, err)
	assert.True(t, err.IsInvalidInputError())
}

func TestStopRun_DoesNotEnqueueTasks(t *testing.T) {
	// StopRun must not enqueue any immediate task — it only does a CAS
	// status update so it never blocks the immediate task queue.
	eng, runStore := getTestEngine(t)
	ctx := context.Background()
	runID := uuid.NewString()
	ns := "test-ns"

	require.Nil(t, eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: ns, RunId: runID, FlowType: "test", TaskListName: "g",
	}))
	shardID := testMapper().GetShardID(ns, runID)

	// One task already exists from StartRun (initial dispatch). Capture the
	// max sort key so we can assert StopRun doesn't add new ones.
	tasksBefore, rdErr := runStore.RangeReadImmediateTasks(ctx, shardID, 0, 100)
	require.Nil(t, rdErr)
	tasksBeforeCount := len(tasksBefore)

	_, _, _, sErr := eng.StopRun(ctx, ns, runID, pb.StopDecision_STOP_DECISION_COMPLETE, "")
	require.Nil(t, sErr)

	tasksAfter, rdErr2 := runStore.RangeReadImmediateTasks(ctx, shardID, 0, 100)
	require.Nil(t, rdErr2)
	assert.Equal(t, tasksBeforeCount, len(tasksAfter),
		"StopRun must not enqueue any new immediate tasks")
}

// TestStopRun_BlocksLateStepCompletion verifies the requirement that a worker
// which finishes a step after the run was stopped will see "expected Running"
// and can shut down cleanly. Mirrors the SDK's isRunTerminatedError detection.
func TestStopRun_BlocksLateStepCompletion(t *testing.T) {
	eng, runStore := getTestEngine(t)
	ctx := context.Background()
	runID := uuid.NewString()
	ns := "test-ns"

	require.Nil(t, eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: ns, RunId: runID, FlowType: "test", TaskListName: "g",
	}))
	shardID := testMapper().GetShardID(ns, runID)

	// Move to Running with one active step (worker thinks it is executing).
	run, _ := runStore.GetRun(ctx, shardID, ns, runID, p.GetRunOptions{})
	running := p.RunStatusRunning
	require.Nil(t, runStore.UpdateRunWithNewTasks(ctx, shardID, ns, runID, run.Version, &p.RunRowUpdate{
		Status: &running,
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"step-1": {Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusInvokingExecute},
		},
	}, nil))

	// Stop the run.
	_, _, _, sErr := eng.StopRun(ctx, ns, runID, pb.StopDecision_STOP_DECISION_COMPLETE, "")
	require.Nil(t, sErr)

	// Worker reports completion after the stop; engine must reject with
	// an InvalidInput "expected Running" error so the SDK can detect it.
	_, completeErr := eng.ProcessStepExecuteCompleted(ctx, shardID, ns, &pb.StepExecuteCompletedRequest{
		RunId: runID, StepExeId: "step-1",
		Context:      &pb.WorkerCallContext{WorkerRequestCounter: 1},
		StopDecision: pb.StopDecision_STOP_DECISION_COMPLETE,
	})
	require.NotNil(t, completeErr)
	assert.True(t, completeErr.IsInvalidInputError())
	assert.Contains(t, completeErr.Error(), "expected Running")
}

func TestStopRun_BlocksLateWaitForCompletion(t *testing.T) {
	eng, runStore := getTestEngine(t)
	ctx := context.Background()
	runID := uuid.NewString()
	ns := "test-ns"

	require.Nil(t, eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: ns, RunId: runID, FlowType: "test", TaskListName: "g",
	}))
	shardID := testMapper().GetShardID(ns, runID)

	run, _ := runStore.GetRun(ctx, shardID, ns, runID, p.GetRunOptions{})
	running := p.RunStatusRunning
	require.Nil(t, runStore.UpdateRunWithNewTasks(ctx, shardID, ns, runID, run.Version, &p.RunRowUpdate{
		Status: &running,
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"step-1": {Input: p.Value{Type: p.ValueTypeNull}, Status: p.StepExeStatusInvokingWaitFor},
		},
	}, nil))

	_, _, _, sErr := eng.StopRun(ctx, ns, runID, pb.StopDecision_STOP_DECISION_COMPLETE, "")
	require.Nil(t, sErr)

	_, waitErr := eng.ProcessStepWaitForCompleted(ctx, shardID, ns, &pb.StepWaitForCompletedRequest{
		RunId: runID, StepExeId: "step-1",
		Context: &pb.WorkerCallContext{WorkerRequestCounter: 1},
		WaitForCondition: &pb.WaitForCondition{
			Type: pb.WaitType_WAIT_TYPE_ANY_OF,
			Conditions: []*pb.SingleCondition{{Condition: &pb.SingleCondition_Timer{
				Timer: &pb.TimerCondition{FireAtUnixMs: time.Now().Add(time.Hour).UnixMilli()},
			}}},
		},
	})
	require.NotNil(t, waitErr)
	assert.True(t, waitErr.IsInvalidInputError())
	assert.Contains(t, waitErr.Error(), "expected Running")
}

// TestHeartbeatSkippedWhenStopped removed: the new heartbeat protocol
// no longer probes the worker via matching. HandleHeartbeatTimeout
// short-circuits in tryProcessHeartbeatTimeout when the run is
// terminal — covered indirectly by the StopRun + ReleaseRun tests.

// getTestEngineWithBlobs creates an engine backed by both RunStore and BlobStore.
func getTestEngineWithBlobs(t *testing.T) (RunEngine, p.RunStore, p.BlobStore) {
	uri := os.Getenv("DEX_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("DEX_TEST_MONGO_URI not set")
	}
	ctx := context.Background()
	runStore, err := mongo.NewRunStoreWithDatabase(ctx, uri, testDBName, mongo.DefaultOperationTimeouts())
	require.Nil(t, err)
	runStore.DeleteAll(ctx)
	t.Cleanup(func() { runStore.Close() })

	blobStore, bErr := mongo.NewBlobStoreWithDatabase(ctx, uri, testDBName, mongo.DefaultOperationTimeouts())
	require.Nil(t, bErr)
	t.Cleanup(func() { blobStore.Close() })

	mapper := shardmanager.NewShardMapper(config.ShardConfig{DefaultShardsForNewNamespaces: 2})
	logger := log.NewNoop()
	sm := &testShardManager{}
	sharded := shardmanager.NewShardedRunStore(runStore, sm, nil)
	runCfg := config.DefaultRunServiceConfig()
	eng := NewRunEngine(&runCfg, sharded, blobStore, mapper, sm, logger)
	return eng, runStore, blobStore
}

func TestGetRun_ResolvesStateBlobs(t *testing.T) {
	eng, runStore, _ := getTestEngineWithBlobs(t)
	ctx := context.Background()
	runID := uuid.NewString()
	ns := "test-ns"

	// StartRun with an EncodedObject starting-step input — it goes through BlobStore
	require.Nil(t, eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: ns, RunId: runID, FlowType: "test", TaskListName: "g",
		StartingSteps: []*pb.NextStep{{
			StepId: "start",
			Input: &pb.Value{Kind: &pb.Value_EncodedObject{EncodedObject: &pb.EncodedObject{
				Encoding: "json", Payload: []byte(`{"key":"value"}`),
			}}},
		}},
	}))

	shardID := testMapper().GetShardID(ns, runID)
	run, _ := runStore.GetRun(ctx, shardID, ns, runID, p.GetRunOptions{})
	stepExe := run.ActiveStepExecutions["start-1"]
	t.Logf("start-1 input: type=%d blobID=%s", stepExe.Input.Type, stepExe.Input.BlobID)
	assert.Equal(t, p.ValueTypeBlobRef, stepExe.Input.Type)
	assert.NotEmpty(t, stepExe.Input.BlobID)

	// Also upsert a state field with a blob ref
	running := p.RunStatusRunning
	runStore.UpdateRunWithNewTasks(ctx, shardID, ns, runID, run.Version, &p.RunRowUpdate{
		Status:   &running,
		StateMap: map[string]p.Value{"my_field": {Type: p.ValueTypeBlobRef, BlobID: stepExe.Input.BlobID}},
	}, nil)

	resp, err := eng.GetRun(ctx, ns, runID, nil)
	require.Nil(t, err)
	require.True(t, resp.Found)

	// State field should be resolved from blob ref to EncodedObject
	stateVal, ok := resp.State["my_field"]
	require.True(t, ok, "state map should contain my_field")
	t.Logf("state my_field kind: %T", stateVal.Kind)
	enc := stateVal.GetEncodedObject()
	require.NotNil(t, enc, "state my_field should be an EncodedObject")
	assert.Equal(t, "json", enc.Encoding)
	assert.Equal(t, []byte(`{"key":"value"}`), enc.Payload)
}

func TestGetRun_ResolvesChannelMessageBlobs(t *testing.T) {
	eng, runStore, _ := getTestEngineWithBlobs(t)
	ctx := context.Background()
	runID := uuid.NewString()
	ns := "test-ns"

	// Start a flow then publish an EncodedObject channel message
	require.Nil(t, eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: ns, RunId: runID, FlowType: "test", TaskListName: "g",
	}))

	shardID := testMapper().GetShardID(ns, runID)
	run, _ := runStore.GetRun(ctx, shardID, ns, runID, p.GetRunOptions{})
	running := p.RunStatusRunning
	runStore.UpdateRunWithNewTasks(ctx, shardID, ns, runID, run.Version, &p.RunRowUpdate{
		Status: &running,
	}, nil)

	// Publish a channel message with EncodedObject
	pubErr := eng.PublishExternalChannelMessages(ctx, shardID, &pb.PublishToChannelRequest{
		RunId: runID, Namespace: ns, ChannelName: "events",
		Values: []*pb.Value{
			intPbValue(42),
			{Kind: &pb.Value_EncodedObject{EncodedObject: &pb.EncodedObject{
				Encoding: "msgpack", Payload: []byte{0x01, 0x02},
			}}},
		},
	})
	require.Nil(t, pubErr)

	resp, err := eng.GetRun(ctx, ns, runID, nil)
	require.Nil(t, err)
	require.True(t, resp.Found)

	msgs := resp.UnconsumedChannelMessages["events"]
	require.NotNil(t, msgs, "should have events channel")
	require.Len(t, msgs.Messages, 2)

	// First value: int (no blob resolution needed)
	assert.NotNil(t, msgs.Messages[0].Value.GetIntValue)
	t.Logf("channel msg[0] kind: %T", msgs.Messages[0].Value.Kind)

	// Second value: EncodedObject resolved from blob ref
	enc := msgs.Messages[1].Value.GetEncodedObject()
	require.NotNil(t, enc, "channel msg[1] should be resolved EncodedObject")
	t.Logf("channel msg[1] encoding=%s payload_len=%d", enc.Encoding, len(enc.Payload))
	assert.Equal(t, "msgpack", enc.Encoding)
	assert.Equal(t, []byte{0x01, 0x02}, enc.Payload)
}

func TestGetRun_ResolvesActiveStepInputBlobs(t *testing.T) {
	eng, runStore, blobStore := getTestEngineWithBlobs(t)
	ctx := context.Background()
	runID := uuid.NewString()
	ns := "test-ns"

	require.Nil(t, eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: ns, RunId: runID, FlowType: "test", TaskListName: "g",
	}))

	shardID := testMapper().GetShardID(ns, runID)
	run, _ := runStore.GetRun(ctx, shardID, ns, runID, p.GetRunOptions{})

	// Manually insert a blob and create an active step with a blob ref input
	blobID := ids.NewBlobID()
	require.Nil(t, blobStore.BatchInsertBlobs(ctx, shardID, ns, runID, []p.BlobEntry{
		{BlobID: blobID, Encoding: "json", Payload: []byte(`{"step":"input"}`)},
	}))

	running := p.RunStatusRunning
	runStore.UpdateRunWithNewTasks(ctx, shardID, ns, runID, run.Version, &p.RunRowUpdate{
		Status: &running,
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"step-1": {
				Input:  p.Value{Type: p.ValueTypeBlobRef, BlobID: blobID},
				Status: p.StepExeStatusInvokingExecute,
			},
		},
	}, nil)

	resp, err := eng.GetRun(ctx, ns, runID, nil)
	require.Nil(t, err)
	require.True(t, resp.Found)

	pbStep, ok := resp.ActiveStepExecutions["step-1"]
	require.True(t, ok, "should have step-1")
	t.Logf("step-1 input kind: %T, status: %v", pbStep.Input.Kind, pbStep.Status)
	assert.Equal(t, pb.StepExecutionStatus_STEP_EXECUTION_STATUS_INVOKING_EXECUTE, pbStep.Status)

	enc := pbStep.Input.GetEncodedObject()
	require.NotNil(t, enc, "step input should be resolved EncodedObject")
	assert.Equal(t, "json", enc.Encoding)
	assert.Equal(t, []byte(`{"step":"input"}`), enc.Payload)
}

func TestGetRun_PrimitiveValuesPassThrough(t *testing.T) {
	eng, runStore, _ := getTestEngineWithBlobs(t)
	ctx := context.Background()
	runID := uuid.NewString()
	ns := "test-ns"

	require.Nil(t, eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: ns, RunId: runID, FlowType: "test", TaskListName: "g",
	}))

	shardID := testMapper().GetShardID(ns, runID)
	run, _ := runStore.GetRun(ctx, shardID, ns, runID, p.GetRunOptions{})

	boolTrue := true
	doubleVal := 3.14
	running := p.RunStatusRunning
	runStore.UpdateRunWithNewTasks(ctx, shardID, ns, runID, run.Version, &p.RunRowUpdate{
		Status: &running,
		StateMap: map[string]p.Value{
			"count":  {Type: p.ValueTypeInt, IntVal: func() *int64 { v := int64(10); return &v }()},
			"pi":     {Type: p.ValueTypeDouble, DoubleVal: &doubleVal},
			"active": {Type: p.ValueTypeBool, BoolVal: &boolTrue},
			"empty":  {Type: p.ValueTypeNull},
		},
	}, nil)

	resp, err := eng.GetRun(ctx, ns, runID, nil)
	require.Nil(t, err)
	require.True(t, resp.Found)

	assert.Equal(t, int64(10), resp.State["count"].GetIntValue())
	assert.InDelta(t, 3.14, resp.State["pi"].GetDoubleValue(), 0.001)
	assert.True(t, resp.State["active"].GetBoolValue())
	assert.NotNil(t, resp.State["empty"].GetNullValue)
	t.Logf("state: count=%v pi=%v active=%v empty_kind=%T",
		resp.State["count"].Kind, resp.State["pi"].Kind,
		resp.State["active"].Kind, resp.State["empty"].Kind)
}

func TestGetRun_ActiveStepWithWaitForCondition(t *testing.T) {
	eng, runStore, _ := getTestEngineWithBlobs(t)
	ctx := context.Background()
	runID := uuid.NewString()
	ns := "test-ns"

	require.Nil(t, eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: ns, RunId: runID, FlowType: "test", TaskListName: "g",
	}))

	shardID := testMapper().GetShardID(ns, runID)
	run, _ := runStore.GetRun(ctx, shardID, ns, runID, p.GetRunOptions{})

	allWaiting := p.RunStatusAllStepsWaitingForConditions
	runStore.UpdateRunWithNewTasks(ctx, shardID, ns, runID, run.Version, &p.RunRowUpdate{
		Status: &allWaiting,
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"wait-1": {
				Input:  p.Value{Type: p.ValueTypeNull},
				Status: p.StepExeStatusWaitingForCondition,
				WaitForCondition: &p.WaitForCondition{
					Type: p.WaitTypeAnyOf,
					Conditions: []p.SingleCondition{
						{Timer: &p.TimerCondition{FireAtUnixMs: 99999}},
						{Channel: &p.ChannelCondition{ChannelName: "notify", Min: 1, Max: 5}},
					},
				},
			},
		},
	}, nil)

	resp, err := eng.GetRun(ctx, ns, runID, nil)
	require.Nil(t, err)
	require.True(t, resp.Found)

	pbStep := resp.ActiveStepExecutions["wait-1"]
	require.NotNil(t, pbStep)
	require.NotNil(t, pbStep.WaitForCondition)
	t.Logf("wait-1 condition type=%v, num_conditions=%d",
		pbStep.WaitForCondition.Type, len(pbStep.WaitForCondition.Conditions))

	assert.Equal(t, pb.WaitType_WAIT_TYPE_ANY_OF, pbStep.WaitForCondition.Type)
	require.Len(t, pbStep.WaitForCondition.Conditions, 2)

	timerCond := pbStep.WaitForCondition.Conditions[0].GetTimer()
	require.NotNil(t, timerCond)
	assert.Equal(t, int64(99999), timerCond.FireAtUnixMs)

	chanCond := pbStep.WaitForCondition.Conditions[1].GetChannel()
	require.NotNil(t, chanCond)
	assert.Equal(t, "notify", chanCond.ChannelName)
	assert.Equal(t, int32(1), chanCond.Min)
	assert.Equal(t, int32(5), chanCond.Max)
}

func TestGetRun_ReturnsRetryState(t *testing.T) {
	eng, runStore, _ := getTestEngineWithBlobs(t)
	ctx := context.Background()
	runID := uuid.NewString()
	ns := "test-ns"

	require.Nil(t, eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: ns, RunId: runID, FlowType: "test", TaskListName: "g",
	}))

	shardID := testMapper().GetShardID(ns, runID)
	run, _ := runStore.GetRun(ctx, shardID, ns, runID, p.GetRunOptions{})

	nowMs := time.Now().UnixMilli()
	running := p.RunStatusRunning
	runStore.UpdateRunWithNewTasks(ctx, shardID, ns, runID, run.Version, &p.RunRowUpdate{
		Status: &running,
		ActiveStepExecutions: map[string]*p.ActiveStepExecution{
			"step-1": {
				Input:  p.Value{Type: p.ValueTypeNull},
				Status: p.StepExeStatusInvokingWaitFor,
				WaitForRetryState: &p.RetryState{
					FirstAttemptTime: time.UnixMilli(nowMs),
					CurrentAttempts:  3,
					LastError:        "error-500",
				},
			},
			"step-2": {
				Input:  p.Value{Type: p.ValueTypeNull},
				Status: p.StepExeStatusInvokingExecute,
				ExecuteRetryState: &p.RetryState{
					FirstAttemptTime: time.UnixMilli(nowMs),
					CurrentAttempts:  1,
				},
			},
		},
	}, nil)

	resp, err := eng.GetRun(ctx, ns, runID, nil)
	require.Nil(t, err)
	require.True(t, resp.Found)

	step1 := resp.ActiveStepExecutions["step-1"]
	require.NotNil(t, step1)
	require.NotNil(t, step1.WaitForRetryState)
	assert.Equal(t, nowMs, step1.WaitForRetryState.FirstAttemptTimeMs)
	assert.Equal(t, int32(3), step1.WaitForRetryState.CurrentAttempts)
	assert.Equal(t, "error-500", step1.WaitForRetryState.LastError)
	assert.Nil(t, step1.ExecuteRetryState)

	step2 := resp.ActiveStepExecutions["step-2"]
	require.NotNil(t, step2)
	require.NotNil(t, step2.ExecuteRetryState)
	assert.Equal(t, nowMs, step2.ExecuteRetryState.FirstAttemptTimeMs)
	assert.Equal(t, int32(1), step2.ExecuteRetryState.CurrentAttempts)
	assert.Nil(t, step2.WaitForRetryState)
}

// ============================================================================
// HandleRunDispatchResult Tests
// ============================================================================

func TestHandleRunDispatchResult_TransitionToRunning(t *testing.T) {
	eng, runStore := getTestEngine(t)
	ctx := context.Background()
	runID := uuid.NewString()
	ns := "test-ns"

	require.Nil(t, eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: ns, RunId: runID, FlowType: "test", TaskListName: "g",
	}))

	shardID := testMapper().GetShardID(ns, runID)

	// Sync match: transition to Running + heartbeat timer
	_, err := eng.HandleRunDispatchResult(ctx, shardID, ns, runID, true, "test-worker")
	require.Nil(t, err)

	// Verify run is now Running with heartbeat
	run, readErr := runStore.GetRun(ctx, shardID, ns, runID, p.GetRunOptions{})
	require.Nil(t, readErr)
	assert.Equal(t, p.RunStatusRunning, run.Status)
	assert.NotEmpty(t, run.HeartbeatTimerID)
	assert.False(t, run.LastHeartbeatTime.IsZero())
}

func TestHandleRunDispatchResult_AsyncMatch(t *testing.T) {
	eng, runStore := getTestEngine(t)
	ctx := context.Background()
	runID := uuid.NewString()
	ns := "test-ns"

	require.Nil(t, eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: ns, RunId: runID, FlowType: "test", TaskListName: "g",
	}))

	shardID := testMapper().GetShardID(ns, runID)

	// Async match: transition to WaitingForWorker
	_, err := eng.HandleRunDispatchResult(ctx, shardID, ns, runID, false, "test-worker")
	require.Nil(t, err)

	// Verify run is now WaitingForWorker
	run, readErr := runStore.GetRun(ctx, shardID, ns, runID, p.GetRunOptions{})
	require.Nil(t, readErr)
	assert.Equal(t, p.RunStatusWaitingForWorker, run.Status)
}

func TestHandleRunDispatchResult_Idempotent(t *testing.T) {
	eng, runStore := getTestEngine(t)
	ctx := context.Background()
	runID := uuid.NewString()
	ns := "test-ns"

	require.Nil(t, eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: ns, RunId: runID, FlowType: "test", TaskListName: "g",
	}))

	shardID := testMapper().GetShardID(ns, runID)

	_, _err := eng.HandleRunDispatchResult(ctx, shardID, ns, runID, true, "test-worker")
	require.Nil(t, _err)

	// Calling again with transitionToRunning=true is a no-op (already Running)
	_, err := eng.HandleRunDispatchResult(ctx, shardID, ns, runID, true, "test-worker")
	require.Nil(t, err)

	run, readErr := runStore.GetRun(ctx, shardID, ns, runID, p.GetRunOptions{})
	require.Nil(t, readErr)
	assert.Equal(t, p.RunStatusRunning, run.Status)

	// Calling with transitionToRunning=false is also a no-op (already past WaitingForWorker)
	_, err = eng.HandleRunDispatchResult(ctx, shardID, ns, runID, false, "test-worker")
	require.Nil(t, err)
}

// TestHandleRunDispatchResult_ResumeFromWaitingForWorker verifies resume
// dispatch after heartbeat timer fire: Running → WaitingForWorker → Running.
func TestHandleRunDispatchResult_ResumeFromWaitingForWorker(t *testing.T) {
	eng, runStore := getTestEngine(t)
	ctx := context.Background()
	runID := uuid.NewString()
	ns := "test-ns"

	require.Nil(t, eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: ns, RunId: runID, FlowType: "test", TaskListName: "g",
	}))
	shardID := testMapper().GetShardID(ns, runID)

	_, err := eng.HandleRunDispatchResult(ctx, shardID, ns, runID, true, "test-worker")
	require.Nil(t, err)
	run, readErr := runStore.GetRun(ctx, shardID, ns, runID, p.GetRunOptions{})
	require.Nil(t, readErr)
	assert.Equal(t, p.RunStatusRunning, run.Status)
	timerID := run.HeartbeatTimerID

	err = eng.HandleHeartbeatTimeout(ctx, shardID, &HeartbeatTimerFiredRequest{
		RunID: runID, Namespace: ns, TimerID: timerID,
	})
	require.Nil(t, err)
	run, readErr = runStore.GetRun(ctx, shardID, ns, runID, p.GetRunOptions{})
	require.Nil(t, readErr)
	assert.Equal(t, p.RunStatusWaitingForWorker, run.Status)

	_, err = eng.HandleRunDispatchResult(ctx, shardID, ns, runID, true, "test-worker")
	require.Nil(t, err)

	run, readErr = runStore.GetRun(ctx, shardID, ns, runID, p.GetRunOptions{})
	require.Nil(t, readErr)
	assert.Equal(t, p.RunStatusRunning, run.Status)
	assert.NotEmpty(t, run.HeartbeatTimerID)
	assert.NotEqual(t, timerID, run.HeartbeatTimerID)
}

// ============================================================================
// DurableTimerFired Flag Tests
// ============================================================================

func TestDurableTimerFired_SetOnTimerFire(t *testing.T) {
	eng, runStore := getTestEngine(t)
	ctx := context.Background()
	runID := uuid.NewString()
	ns := "test-ns"

	require.Nil(t, eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: ns, RunId: runID, FlowType: "test", TaskListName: "g",
	}))

	shardID := testMapper().GetShardID(ns, runID)

	// Transition to Running to simulate worker picking up
	_, err := eng.HandleRunDispatchResult(ctx, shardID, ns, runID, true, "test-worker")
	require.Nil(t, err)

	// Complete WaitFor with a timer condition to create a durable timer
	_, err = eng.ProcessStepWaitForCompleted(ctx, shardID, ns, &pb.StepWaitForCompletedRequest{
		Namespace: ns, RunId: runID,
		StepExeId: "step1-0",
		Context:   &pb.WorkerCallContext{WorkerRequestCounter: 0},
		WaitForCondition: &pb.WaitForCondition{
			Type: pb.WaitType_WAIT_TYPE_ANY_OF,
			Conditions: []*pb.SingleCondition{{
				Condition: &pb.SingleCondition_Timer{
					Timer: &pb.TimerCondition{FireAtUnixMs: 1000},
				},
			}},
		},
	})
	require.Nil(t, err)

	// Verify DurableTimerFired is false before timer fires
	run, readErr := runStore.GetRun(ctx, shardID, ns, runID, p.GetRunOptions{})
	require.Nil(t, readErr)
	assert.False(t, run.DurableTimerFired)

	// Fire the durable timer
	timerID := run.ActiveDurableTimerID
	if !timerID.IsZero() {
		err = eng.HandleStepWaitForTimerFired(ctx, shardID, &StepWaitForTimerFiredRequest{
			RunID: runID, Namespace: ns, TimerID: timerID, FireAtUnixMs: 1000,
		})
		require.Nil(t, err)

		// Verify DurableTimerFired is now true
		run, readErr = runStore.GetRun(ctx, shardID, ns, runID, p.GetRunOptions{})
		require.Nil(t, readErr)
		assert.True(t, run.DurableTimerFired)
	}
}

func TestGetRun_DurableTimerFiredReturned(t *testing.T) {
	eng, runStore := getTestEngine(t)
	ctx := context.Background()
	runID := uuid.NewString()
	ns := "test-ns"

	require.Nil(t, eng.StartRun(ctx, &pb.StartRunRequest{
		Namespace: ns, RunId: runID, FlowType: "test", TaskListName: "g",
	}))

	shardID := testMapper().GetShardID(ns, runID)

	// Manually set DurableTimerFired to true
	fired := true
	updateErr := runStore.UpdateRunWithNewTasks(ctx, shardID, ns, runID, 1, &p.RunRowUpdate{
		DurableTimerFired: &fired,
	}, nil)
	require.Nil(t, updateErr)

	// GetRun should return DurableTimerFired=true
	resp, err := eng.GetRun(ctx, ns, runID, nil)
	require.Nil(t, err)
	assert.True(t, resp.Found)
	assert.True(t, resp.DurableTimerFired)
}
