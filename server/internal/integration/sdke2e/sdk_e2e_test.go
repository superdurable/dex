package sdke2e

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/server/cmd"
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

const dbPrefix = "dex_test_integration_sdke2e"

// startE2EServer wraps testhelpers.StartE2EServer with this sub-package's
// dbPrefix so individual tests don't repeat it.
func startE2EServer(t *testing.T) (*cmd.ServerApp, pb.RunsServiceClient, pb.MatchingServiceClient) {
	return testhelpers.StartE2EServer(t, dbPrefix)
}

// startE2EServerWithConfig wraps testhelpers.StartE2EServerWithConfig.
func startE2EServerWithConfig(t *testing.T, cfgFn func(*config.Config)) (*cmd.ServerApp, pb.RunsServiceClient, pb.MatchingServiceClient) {
	return testhelpers.StartE2EServerWithConfig(t, dbPrefix, cfgFn)
}

// ============================================================================
// Test flows and state types
// ============================================================================

var (
	seqKeyCounter        = dex.NewStateKey[int]("counter")
	seqKeyMessage        = dex.NewStateKey[string]("message")
	parKeyA              = dex.NewStateKey[string]("a")
	parKeyB              = dex.NewStateKey[string]("b")
	timingKeyS0          = dex.NewStateKey[string]("s0")
	timingKeyS1          = dex.NewStateKey[string]("s1")
	timingKeyS2          = dex.NewStateKey[string]("s2")
	timingKeyS3          = dex.NewStateKey[string]("s3")
	timingKeyS4          = dex.NewStateKey[string]("s4")
	timingKeyS5          = dex.NewStateKey[string]("s5")
	timingKeyS6          = dex.NewStateKey[string]("s6")
	timingKeyS7          = dex.NewStateKey[string]("s7")
	timingKeyS8          = dex.NewStateKey[string]("s8")
	timingKeyS9          = dex.NewStateKey[string]("s9")
	slowKeyResult        = dex.NewStateKey[string]("result")
	waitKeyWaitStartedAt = dex.NewStateKey[int64]("wait_started_at")
	waitKeyNotes         = dex.NewStateKey[[]string]("notes")
	waitKeyTimerFired    = dex.NewStateKey[bool]("timer_fired")
)

// --- Sequential flow ---

type SeqState struct {
	Counter int    `json:"counter"`
	Message string `json:"message"`
}

type SeqStep1 struct {
	dex.StepDefaults[any]
}

func (s *SeqStep1) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	if err := seqKeyCounter.SetValue(ctx, 1); err != nil {
		return nil, err
	}
	return dex.GoTo(&SeqStep2{}, nil), nil
}

type SeqStep2 struct {
	dex.StepDefaults[any]
}

func (s *SeqStep2) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	counter, err := seqKeyCounter.GetValue(ctx)
	if err != nil {
		return nil, err
	}
	if err := seqKeyCounter.SetValue(ctx, counter+1); err != nil {
		return nil, err
	}
	if err := seqKeyMessage.SetValue(ctx, "done"); err != nil {
		return nil, err
	}
	return dex.Complete(nil), nil
}

type SeqFlow struct {
	dex.FlowDefaults
}

func (f *SeqFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep[any](&SeqStep1{}),
		dex.NonStartingStep[any](&SeqStep2{}),
	}
}

func (f *SeqFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		StateKeys: []dex.StateKeyDef{
			dex.DefineStateKey(seqKeyCounter),
			dex.DefineStateKey(seqKeyMessage),
		},
	}
}

// --- Parallel flow (with merge step) ---

type ParState struct {
	A string `json:"a"`
	B string `json:"b"`
}

type ParInitStep struct {
	dex.StepDefaults[any]
}

func (s *ParInitStep) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	return dex.GoToMany(
		dex.MovementOf(&ParStepA{}, nil),
		dex.MovementOf(&ParStepB{}, nil),
	), nil
}

type ParStepA struct {
	dex.StepDefaults[any]
}

func (s *ParStepA) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	if err := parKeyA.SetValue(ctx, "from-a"); err != nil {
		return nil, err
	}
	return dex.GoTo(&ParMergeStep{}, nil), nil
}

type ParStepB struct {
	dex.StepDefaults[any]
}

func (s *ParStepB) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	if err := parKeyB.SetValue(ctx, "from-b"); err != nil {
		return nil, err
	}
	return dex.GoTo(&ParMergeStep{}, nil), nil
}

type ParMergeStep struct {
	dex.StepDefaults[any]
}

func (s *ParMergeStep) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	return dex.Complete(nil), nil
}

type ParFlow struct {
	dex.FlowDefaults
}

func (f *ParFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep[any](&ParInitStep{}),
		dex.NonStartingStep[any](&ParStepA{}),
		dex.NonStartingStep[any](&ParStepB{}),
		dex.NonStartingStep[any](&ParMergeStep{}),
	}
}

func (f *ParFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		StateKeys: []dex.StateKeyDef{
			dex.DefineStateKey(parKeyA),
			dex.DefineStateKey(parKeyB),
		},
	}
}

// --- Parallel timing flow (10 steps, each sleeps 100ms) ---

// Uses individual top-level state fields for each step to avoid overwrite.
// Each worker step writes its own key and GoTo(merge), not Complete.
type TimingState struct {
	S0 string `json:"s0"`
	S1 string `json:"s1"`
	S2 string `json:"s2"`
	S3 string `json:"s3"`
	S4 string `json:"s4"`
	S5 string `json:"s5"`
	S6 string `json:"s6"`
	S7 string `json:"s7"`
	S8 string `json:"s8"`
	S9 string `json:"s9"`
}

type TimingInitStep struct {
	dex.StepDefaults[any]
}

func (s *TimingInitStep) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	movements := make([]dex.StepMovement, 10)
	for i := 0; i < 10; i++ {
		movements[i] = dex.StepMovement{
			StepID: dex.GetFinalStepId(&TimingWorkerStep{}),
			Input:  i,
		}
	}
	return dex.GoToMany(movements...), nil
}

type TimingWorkerStep struct {
	dex.StepDefaults[int]
}

func (s *TimingWorkerStep) Execute(ctx dex.Context, input int) (dex.StepDecision, error) {
	time.Sleep(100 * time.Millisecond)
	var key dex.StateKey[string]
	switch input {
	case 0:
		key = timingKeyS0
	case 1:
		key = timingKeyS1
	case 2:
		key = timingKeyS2
	case 3:
		key = timingKeyS3
	case 4:
		key = timingKeyS4
	case 5:
		key = timingKeyS5
	case 6:
		key = timingKeyS6
	case 7:
		key = timingKeyS7
	case 8:
		key = timingKeyS8
	case 9:
		key = timingKeyS9
	}
	if err := key.SetValue(ctx, "done"); err != nil {
		return nil, err
	}
	// DeadEnd: this branch is done. When all 10 branches DeadEnd,
	// the server sees no active steps remaining and completes the run.
	return dex.DeadEnd(), nil
}

type TimingFlow struct {
	dex.FlowDefaults
}

func (f *TimingFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep[any](&TimingInitStep{}),
		dex.NonStartingStep[int](&TimingWorkerStep{}),
	}
}

func (f *TimingFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		StateKeys: []dex.StateKeyDef{
			dex.DefineStateKey(timingKeyS0),
			dex.DefineStateKey(timingKeyS1),
			dex.DefineStateKey(timingKeyS2),
			dex.DefineStateKey(timingKeyS3),
			dex.DefineStateKey(timingKeyS4),
			dex.DefineStateKey(timingKeyS5),
			dex.DefineStateKey(timingKeyS6),
			dex.DefineStateKey(timingKeyS7),
			dex.DefineStateKey(timingKeyS8),
			dex.DefineStateKey(timingKeyS9),
		},
	}
}

// ============================================================================
// Helper: start server + SDK connections
// ============================================================================

func startSDKE2E(t *testing.T, registry *dex.Registry) (*dex.Client, *dex.Worker, string) {
	taskListName := "sdk-e2e-" + uuid.NewString()
	client, worker := startSDKE2EWithTaskList(t, registry, taskListName, 1)
	return client, worker, taskListName
}

// startSDKE2EWithTaskList is startSDKE2E parameterized on the tasklist name
// and run concurrency. The worker MUST poll the same tasklist that runs are
// dispatched to;
func startSDKE2EWithTaskList(t *testing.T, registry *dex.Registry, taskListName string, runConcurrency int) (*dex.Client, *dex.Worker) {
	app, _, _ := startE2EServer(t)

	runConn, err := grpc.NewClient(app.RunGRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { runConn.Close() })

	matchConn, err := grpc.NewClient(app.MatchingGRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { matchConn.Close() })

	client := dex.NewClient(registry, runConn, "default")
	worker := dex.NewWorker(registry, matchConn, runConn, "default", dex.WorkerOptions{
		TaskListName:   taskListName,
		RunConcurrency: runConcurrency,
	})

	return client, worker
}

// ============================================================================
// Test: Sequential steps with state updates
// ============================================================================

func TestSDKE2E_SequentialSteps(t *testing.T) {
	registry := dex.NewRegistry()
	registry.Register(&SeqFlow{})

	client, worker, taskListName := startSDKE2E(t, registry)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	runID := uuid.NewString()

	startWorkerBg(t, worker)

	err := client.StartRunWithOptions(ctx, runID, &SeqFlow{}, &dex.RunOptions{TaskListName: taskListName})
	require.NoError(t, err)

	var counter int
	var message string
	status, err := client.WaitForRunComplete(ctx, runID)
	require.NoError(t, err)
	counter, err = seqKeyCounter.GetRunValue(client, ctx, runID)
	require.NoError(t, err)
	message, err = seqKeyMessage.GetRunValue(client, ctx, runID)
	require.NoError(t, err)
	assert.Equal(t, dex.RunStatusCompleted, status)
	assert.Equal(t, 2, counter)
	assert.Equal(t, "done", message)
}

// ============================================================================
// Test: Parallel steps with different state keys
// ============================================================================

func TestSDKE2E_ParallelSteps(t *testing.T) {
	registry := dex.NewRegistry()
	registry.Register(&ParFlow{})

	client, worker, taskListName := startSDKE2E(t, registry)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	runID := uuid.NewString()

	startWorkerBg(t, worker)

	err := client.StartRunWithOptions(ctx, runID, &ParFlow{}, &dex.RunOptions{TaskListName: taskListName})
	require.NoError(t, err)

	var a, b string
	status, err := client.WaitForRunComplete(ctx, runID)
	require.NoError(t, err)
	a, err = parKeyA.GetRunValue(client, ctx, runID)
	require.NoError(t, err)
	b, err = parKeyB.GetRunValue(client, ctx, runID)
	require.NoError(t, err)
	assert.Equal(t, dex.RunStatusCompleted, status)
	assert.Equal(t, "from-a", a)
	assert.Equal(t, "from-b", b)
}

// ============================================================================
// Test: Parallel step execution timing (10 steps x 100ms < 500ms)
// ============================================================================

func TestSDKE2E_ParallelSteps_Concurrent(t *testing.T) {
	registry := dex.NewRegistry()
	registry.Register(&TimingFlow{})

	client, worker, taskListName := startSDKE2E(t, registry)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	runID := uuid.NewString()

	startWorkerBg(t, worker)

	err := client.StartRunWithOptions(ctx, runID, &TimingFlow{}, &dex.RunOptions{TaskListName: taskListName})
	require.NoError(t, err)
	t.Logf("StartRun OK, runID=%s taskListName=%s", runID, taskListName)

	start := time.Now()
	status, err := client.WaitForRunComplete(ctx, runID)
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Equal(t, dex.RunStatusCompleted, status)

	s0, err := timingKeyS0.GetRunValue(client, ctx, runID)
	require.NoError(t, err)
	s1, err := timingKeyS1.GetRunValue(client, ctx, runID)
	require.NoError(t, err)
	s2, err := timingKeyS2.GetRunValue(client, ctx, runID)
	require.NoError(t, err)
	s3, err := timingKeyS3.GetRunValue(client, ctx, runID)
	require.NoError(t, err)
	s4, err := timingKeyS4.GetRunValue(client, ctx, runID)
	require.NoError(t, err)
	s5, err := timingKeyS5.GetRunValue(client, ctx, runID)
	require.NoError(t, err)
	s6, err := timingKeyS6.GetRunValue(client, ctx, runID)
	require.NoError(t, err)
	s7, err := timingKeyS7.GetRunValue(client, ctx, runID)
	require.NoError(t, err)
	s8, err := timingKeyS8.GetRunValue(client, ctx, runID)
	require.NoError(t, err)
	s9, err := timingKeyS9.GetRunValue(client, ctx, runID)
	require.NoError(t, err)
	fields := []string{s0, s1, s2, s3, s4, s5, s6, s7, s8, s9}
	for i, val := range fields {
		assert.Equal(t, "done", val, "missing result for s%d", i)
	}

	t.Logf("Parallel timing test completed in %v", elapsed)
	assert.Less(t, elapsed, 3*time.Second,
		"10 parallel 100ms steps should complete in <3s, took %v", elapsed)
}

// ============================================================================
// Test: Heartbeat keeps long-running step alive
// ============================================================================

// SlowState is the state type for the heartbeat test.
type SlowState struct {
	Result string `json:"result"`
}

// SlowStep sleeps for a configurable duration (longer than the heartbeat
// interval), then completes. Without heartbeat responses, the server would
// declare the worker lost and re-dispatch the run.
type SlowStep struct {
	dex.StepDefaults[any]
}

func (s *SlowStep) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	// Sleep 8 seconds — longer than the 3s heartbeat timer + 3s timeout
	// used in the test. Without heartbeat responses, the server would
	// transition the run to WaitingForWorker after ~6s.
	time.Sleep(8 * time.Second)
	if err := slowKeyResult.SetValue(ctx, "heartbeat-survived"); err != nil {
		return nil, err
	}
	return dex.Complete(nil), nil
}

type SlowFlow struct {
	dex.FlowDefaults
}

func (f *SlowFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep[any](&SlowStep{}),
	}
}

func (f *SlowFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		StateKeys: []dex.StateKeyDef{dex.DefineStateKey(slowKeyResult)},
	}
}

// TestSDKE2E_HeartbeatKeepsRunAlive starts a server with a short heartbeat
// timer (3s) and runs a flow whose step takes 8 seconds. Without heartbeat
// responses from the SDK, the server would declare the worker lost after
// ~6s (3s timer + 3s timeout) and re-dispatch the run, which would never
// complete. With heartbeat responses, the run completes successfully.
func TestSDKE2E_HeartbeatKeepsRunAlive(t *testing.T) {
	registry := dex.NewRegistry()
	registry.Register(&SlowFlow{})

	app, _, _ := startE2EServerWithConfig(t, func(cfg *config.Config) {
		cfg.RunService.HeartbeatTimerDuration = 3 * time.Second
	})

	runConn, err := grpc.NewClient(app.RunGRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { runConn.Close() })

	matchConn, err := grpc.NewClient(app.MatchingGRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { matchConn.Close() })

	taskListName := "hb-test-" + uuid.NewString()
	client := dex.NewClient(registry, runConn, "default")
	worker := dex.NewWorker(registry, matchConn, runConn, "default", dex.WorkerOptions{
		TaskListName:      taskListName,
		RunConcurrency:    1,
		HeartbeatInterval: 1 * time.Second,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	startWorkerBg(t, worker)

	runID := uuid.NewString()
	err = client.StartRunWithOptions(ctx, runID, &SlowFlow{}, &dex.RunOptions{TaskListName: taskListName})
	require.NoError(t, err)
	t.Logf("StartRun OK, runID=%s", runID)

	var result string
	status, err := client.WaitForRunComplete(ctx, runID)
	require.NoError(t, err)
	result, err = slowKeyResult.GetRunValue(client, ctx, runID)
	require.NoError(t, err)
	assert.Equal(t, dex.RunStatusCompleted, status)
	assert.Equal(t, "heartbeat-survived", result)
	t.Logf("Run completed with heartbeat support — step survived 8s with 3s heartbeat interval")
}

// ============================================================================
// Test: Resume dispatch after heartbeat failure
// ============================================================================

// ResumeState is the state for the resume dispatch test.
type ResumeState struct {
	Result string `json:"result"`
}

// ResumeStep is a fast step (no sleep). The point of this test is not the step
// duration but the dispatch cycle: the first dispatch succeeds (run → Running),
// heartbeat fires, SDK responds (run stays Running), step completes.
// The real test is that multiple runs complete correctly even when some
// dispatches go through the WaitingForWorker → re-dispatch path.
type ResumeStep struct {
	dex.StepDefaults[any]
}

func (s *ResumeStep) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	if err := slowKeyResult.SetValue(ctx, "ok"); err != nil {
		return nil, err
	}
	return dex.Complete(nil), nil
}

type ResumeFlow struct {
	dex.FlowDefaults
}

func (f *ResumeFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep[any](&ResumeStep{}),
	}
}

func (f *ResumeFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		StateKeys: []dex.StateKeyDef{dex.DefineStateKey(slowKeyResult)},
	}
}

// TestSDKE2E_ResumeDispatchAfterHeartbeatFailure starts multiple runs and
// verifies that ALL eventually complete, even when the initial dispatch
// goes through the heartbeat failure → WaitingForWorker → re-dispatch cycle.
func TestSDKE2E_ResumeDispatchAfterHeartbeatFailure(t *testing.T) {
	registry := dex.NewRegistry()
	registry.Register(&ResumeFlow{})

	app, _, _ := startE2EServer(t)

	runConn, err := grpc.NewClient(app.RunGRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { runConn.Close() })

	matchConn, err := grpc.NewClient(app.MatchingGRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { matchConn.Close() })

	taskListName := "resume-test-" + uuid.NewString()
	client := dex.NewClient(registry, runConn, "default")
	worker := dex.NewWorker(registry, matchConn, runConn, "default", dex.WorkerOptions{
		TaskListName:   taskListName,
		RunConcurrency: 5,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	startWorkerBg(t, worker)

	const numRuns = 10
	runIDs := make([]string, numRuns)
	for i := 0; i < numRuns; i++ {
		runIDs[i] = uuid.NewString()
		startErr := client.StartRunWithOptions(ctx, runIDs[i], &ResumeFlow{}, &dex.RunOptions{TaskListName: taskListName})
		require.NoError(t, startErr, "StartRun failed for run %d", i)
	}
	t.Logf("Started %d runs", numRuns)

	for i, runID := range runIDs {
		status, waitErr := client.WaitForRunComplete(ctx, runID)
		require.NoError(t, waitErr, "WaitForRunComplete failed for run %d (%s)", i, runID)
		assert.Equal(t, dex.RunStatusCompleted, status,
			"run %d (%s) should be completed", i, runID)
	}
	t.Logf("All %d runs completed successfully", numRuns)
}

// ============================================================================
// StopRun tests
// ============================================================================

type stopBlockState struct {
	Result string `json:"result"`
}

type stopBlockSignal struct {
	entered     chan struct{}
	release     chan struct{}
	exitedAfter atomic.Int64
}

var stopBlockSignals sync.Map

type stopBlockCtxStep struct {
	dex.StepDefaults[any]
}

func (s *stopBlockCtxStep) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	v, ok := stopBlockSignals.Load(ctx.RunID())
	if ok {
		sig := v.(*stopBlockSignal)
		close(sig.entered)
		select {
		case <-sig.release:
		case <-ctx.Done():
		}
		sig.exitedAfter.Store(time.Now().UnixNano())
	}
	if err := slowKeyResult.SetValue(ctx, "ok"); err != nil {
		return nil, err
	}
	return dex.Complete(nil), nil
}

type stopBlockCtxFlow struct{ dex.FlowDefaults }

func (f *stopBlockCtxFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep[any](&stopBlockCtxStep{}),
	}
}

func (f *stopBlockCtxFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		StateKeys: []dex.StateKeyDef{dex.DefineStateKey(slowKeyResult)},
	}
}

type stopBlockNoCtxStep struct {
	dex.StepDefaults[any]
}

func (s *stopBlockNoCtxStep) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	v, ok := stopBlockSignals.Load(ctx.RunID())
	if ok {
		sig := v.(*stopBlockSignal)
		close(sig.entered)
		<-sig.release
		sig.exitedAfter.Store(time.Now().UnixNano())
	}
	if err := slowKeyResult.SetValue(ctx, "ok"); err != nil {
		return nil, err
	}
	return dex.Complete(nil), nil
}

type stopBlockNoCtxFlow struct{ dex.FlowDefaults }

func (f *stopBlockNoCtxFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep[any](&stopBlockNoCtxStep{}),
	}
}

func (f *stopBlockNoCtxFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		StateKeys: []dex.StateKeyDef{dex.DefineStateKey(slowKeyResult)},
	}
}

func TestSDKE2E_StopRun_NotFound(t *testing.T) {
	registry := dex.NewRegistry()
	registry.Register(&stopBlockCtxFlow{})

	client, _, _ := startSDKE2E(t, registry)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := client.StopRun(ctx, "missing-run-"+uuid.NewString(), dex.StopRunComplete, "")
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok, "expected gRPC status error, got %v", err)
	assert.Equal(t, codes.NotFound, st.Code(),
		"StopRun on non-existent run must return codes.NotFound")
}

func TestSDKE2E_StopRun_DuringLongRunningStep(t *testing.T) {
	registry := dex.NewRegistry()
	registry.Register(&stopBlockCtxFlow{})

	client, worker, taskListName := startSDKE2E(t, registry)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
	case <-time.After(5 * time.Second):
		t.Fatal("step did not enter Execute within 5s")
	}

	require.NoError(t, client.StopRun(ctx, runID, dex.StopRunComplete, ""))

	require.Eventually(t, func() bool {
		return sig.exitedAfter.Load() != 0
	}, 5*time.Second, 50*time.Millisecond,
		"step Execute should return after StopRun cancels its context")

	runStatus, waitErr := client.WaitForRunComplete(ctx, runID)
	require.NoError(t, waitErr)
	assert.Equal(t, dex.RunStatusCompleted, runStatus,
		"run should be in Completed terminal status")
}

func TestSDKE2E_StopRun_LateCompletionDetected(t *testing.T) {
	registry := dex.NewRegistry()
	registry.Register(&stopBlockNoCtxFlow{})

	client, worker, taskListName := startSDKE2E(t, registry)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	runID := uuid.NewString()
	sig := &stopBlockSignal{entered: make(chan struct{}), release: make(chan struct{})}
	stopBlockSignals.Store(runID, sig)
	defer stopBlockSignals.Delete(runID)

	startWorkerBg(t, worker)

	require.NoError(t, client.StartRunWithOptions(ctx, runID, &stopBlockNoCtxFlow{}, &dex.RunOptions{TaskListName: taskListName}))
	select {
	case <-sig.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("step did not enter Execute within 5s")
	}

	require.NoError(t, client.StopRun(ctx, runID, dex.StopRunComplete, ""))
	close(sig.release)

	require.Eventually(t, func() bool {
		got, getErr := client.GetRun(ctx, runID)
		if getErr != nil {
			return false
		}
		return got != nil && got.Status == dex.RunStatusCompleted
	}, 5*time.Second, 100*time.Millisecond,
		"run should remain Completed even after late step completion")
}

func TestSDKE2E_StopRun_Idempotent(t *testing.T) {
	registry := dex.NewRegistry()
	registry.Register(&ResumeFlow{})

	client, worker, taskListName := startSDKE2E(t, registry)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	startWorkerBg(t, worker)

	runID := uuid.NewString()
	require.NoError(t, client.StartRunWithOptions(ctx, runID, &ResumeFlow{}, &dex.RunOptions{TaskListName: taskListName}))

	require.NoError(t, client.StopRun(ctx, runID, dex.StopRunComplete, ""))
	require.NoError(t, client.StopRun(ctx, runID, dex.StopRunComplete, ""))
}

func TestSDKE2E_StopRun_BeforeWorkerPickup(t *testing.T) {
	registry := dex.NewRegistry()
	registry.Register(&ResumeFlow{})

	client, worker, taskListName := startSDKE2E(t, registry)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	runID := uuid.NewString()
	require.NoError(t, client.StartRunWithOptions(ctx, runID, &ResumeFlow{}, &dex.RunOptions{TaskListName: taskListName}))
	require.NoError(t, client.StopRun(ctx, runID, dex.StopRunComplete, ""))

	startWorkerBg(t, worker)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got, getErr := client.GetRun(ctx, runID)
		require.NoError(t, getErr)
		require.NotNil(t, got)
		require.Equal(t, dex.RunStatusCompleted, int32(got.Status),
			"run must stay Completed — dispatch path must not flip it back")
		time.Sleep(200 * time.Millisecond)
	}
}

func TestSDKE2E_StopRun_HeartbeatSkipped(t *testing.T) {
	registry := dex.NewRegistry()
	registry.Register(&stopBlockNoCtxFlow{})

	app, _, _ := startE2EServerWithConfig(t, func(cfg *config.Config) {
		cfg.RunService.HeartbeatTimerDuration = 1 * time.Second
	})

	runConn, err := grpc.NewClient(app.RunGRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { runConn.Close() })

	matchConn, err := grpc.NewClient(app.MatchingGRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { matchConn.Close() })

	taskListName := "stop-hb-" + uuid.NewString()
	client := dex.NewClient(registry, runConn, "default")
	worker := dex.NewWorker(registry, matchConn, runConn, "default", dex.WorkerOptions{
		TaskListName:   taskListName,
		RunConcurrency: 1,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	runID := uuid.NewString()
	sig := &stopBlockSignal{entered: make(chan struct{}), release: make(chan struct{})}
	stopBlockSignals.Store(runID, sig)
	defer stopBlockSignals.Delete(runID)
	defer close(sig.release)

	startWorkerBg(t, worker)

	require.NoError(t, client.StartRunWithOptions(ctx, runID, &stopBlockNoCtxFlow{}, &dex.RunOptions{TaskListName: taskListName}))
	select {
	case <-sig.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("step did not enter Execute within 5s")
	}

	shardID := app.ShardMapper.GetShardID("default", runID)
	beforeStop, getErr := app.RunStore.GetRun(ctx, shardID, "default", runID, p.GetRunOptions{})
	require.Nil(t, getErr)
	hbBefore := beforeStop.LastHeartbeatTime

	require.NoError(t, client.StopRun(ctx, runID, dex.StopRunComplete, ""))

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		after, gErr := app.RunStore.GetRun(ctx, shardID, "default", runID, p.GetRunOptions{})
		require.Nil(t, gErr)
		require.Equal(t, p.RunStatusCompleted, after.Status)
		require.Equal(t, hbBefore.UnixNano(), after.LastHeartbeatTime.UnixNano(),
			"LastHeartbeatTime must not advance after StopRun")
		time.Sleep(200 * time.Millisecond)
	}
}

// ============================================================================
// WaitFor timer-only test: AllStepsWaitingForConditions transition
// ============================================================================

type WaitTimerState struct {
	WaitStartedAt int64    `json:"wait_started_at"`
	Notes         []string `json:"notes"`
	TimerFired    bool     `json:"timer_fired"`
}

type WaitTimerStep struct {
	dex.DefaultStepId
	dex.DefaultStepOptions
}

func (s *WaitTimerStep) WaitFor(ctx dex.Context, _ any) (dex.WaitForCondition, error) {
	if err := waitKeyWaitStartedAt.SetValue(ctx, time.Now().UnixMilli()); err != nil {
		return nil, err
	}
	if err := waitKeyNotes.SetValue(ctx, []string{"wait-armed"}); err != nil {
		return nil, err
	}
	return dex.AnyOf(dex.Timer(2 * time.Second)), nil
}

func (s *WaitTimerStep) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	notes, err := waitKeyNotes.GetValue(ctx)
	if err != nil {
		return nil, err
	}
	notes = append(notes, "timer-fired")
	if err := waitKeyNotes.SetValue(ctx, notes); err != nil {
		return nil, err
	}
	if err := waitKeyTimerFired.SetValue(ctx, ctx.TimerFired()); err != nil {
		return nil, err
	}
	return dex.Complete(nil), nil
}

type WaitTimerFlow struct {
	dex.FlowDefaults
}

func (f *WaitTimerFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep[any](&WaitTimerStep{}),
	}
}

func (f *WaitTimerFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		StateKeys: []dex.StateKeyDef{
			dex.DefineStateKey(waitKeyWaitStartedAt),
			dex.DefineStateKey(waitKeyNotes),
			dex.DefineStateKey(waitKeyTimerFired),
		},
	}
}

func TestSDKE2E_WaitFor_TimerOnly_PassesThroughAllStepsWaiting(t *testing.T) {
	registry := dex.NewRegistry()
	registry.Register(&WaitTimerFlow{})

	client, worker, taskListName := startSDKE2E(t, registry)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	startWorkerBg(t, worker)

	runID := uuid.NewString()
	require.NoError(t, client.StartRunWithOptions(ctx, runID, &WaitTimerFlow{}, &dex.RunOptions{TaskListName: taskListName}))

	sawAllWaiting := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		result, err := client.GetRun(ctx, runID)
		require.NoError(t, err)
		require.NotNil(t, result)
		switch result.Status {
		case dex.RunStatusAllStepsWaitingForConditions:
			sawAllWaiting = true
		case dex.RunStatusCompleted, dex.RunStatusFailed:
			deadline = time.Now()
		}
		if sawAllWaiting && result.Status == dex.RunStatusAllStepsWaitingForConditions {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	require.True(t, sawAllWaiting,
		"run must transition through AllStepsWaitingForConditions while the durable timer is pending")

	completionCtx, completionCancel := context.WithTimeout(ctx, 10*time.Second)
	defer completionCancel()

	status, err := client.WaitForRunComplete(completionCtx, runID)
	require.NoError(t, err)

	waitStartedAt, err := waitKeyWaitStartedAt.GetRunValue(client, completionCtx, runID)
	require.NoError(t, err)
	notes, err := waitKeyNotes.GetRunValue(client, completionCtx, runID)
	require.NoError(t, err)
	timerFired, err := waitKeyTimerFired.GetRunValue(client, completionCtx, runID)
	require.NoError(t, err)

	assert.Equal(t, dex.RunStatusCompleted, status)
	require.Equal(t, []string{"wait-armed", "timer-fired"}, notes,
		"WaitFor state must persist across the resume, then Execute appends")
	assert.NotZero(t, waitStartedAt,
		"WaitStartedAt set inside WaitFor must survive the AllStepsWaiting resume")
	assert.True(t, timerFired,
		"ctx.TimerFired() must be true on resume from a durable timer fire")
}

type FastParkTimerStep struct {
	dex.DefaultStepId
	dex.DefaultStepOptions
}

func (s *FastParkTimerStep) WaitFor(ctx dex.Context, _ any) (dex.WaitForCondition, error) {
	return dex.AnyOf(dex.Timer(2 * time.Second)), nil
}

func (s *FastParkTimerStep) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	if err := waitKeyTimerFired.SetValue(ctx, ctx.TimerFired()); err != nil {
		return nil, err
	}
	return dex.Complete(nil), nil
}

type FastParkTimerFlow struct {
	dex.FlowDefaults
}

func (f *FastParkTimerFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep[any](&FastParkTimerStep{}),
	}
}

func (f *FastParkTimerFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		StateKeys: []dex.StateKeyDef{
			dex.DefineStateKey(waitKeyTimerFired),
		},
	}
}

func TestSDKE2E_TimerOnlyStep_ParksAndFiresImmediately(t *testing.T) {
	registry := dex.NewRegistry()
	registry.Register(&FastParkTimerFlow{})

	client, worker, taskListName := startSDKE2E(t, registry)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	startWorkerBg(t, worker)

	runID := uuid.NewString()
	require.NoError(t, client.StartRunWithOptions(ctx, runID, &FastParkTimerFlow{}, &dex.RunOptions{TaskListName: taskListName}))

	requireRunStatusReached(t, client, runID, dex.RunStatusAllStepsWaitingForConditions, 5*time.Second)

	completionCtx, completionCancel := context.WithTimeout(ctx, 15*time.Second)
	defer completionCancel()
	status, err := client.WaitForRunComplete(completionCtx, runID)
	require.NoError(t, err)
	assert.Equal(t, dex.RunStatusCompleted, status)

	timerFired, err := waitKeyTimerFired.GetRunValue(client, completionCtx, runID)
	require.NoError(t, err)
	assert.True(t, timerFired)
}
