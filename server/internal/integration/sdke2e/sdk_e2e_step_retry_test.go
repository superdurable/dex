package sdke2e

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/sdk-go/dex"
	p "github.com/superdurable/dex/server/internal/persistence"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type retryAttemptCounter struct {
	count atomic.Int32
}

var retryAttemptCounters sync.Map

func retryCounterForRun(runID string) *retryAttemptCounter {
	value, _ := retryAttemptCounters.LoadOrStore(runID, &retryAttemptCounter{})
	return value.(*retryAttemptCounter)
}

func startRetryE2EWithRunsPb(t *testing.T, registry *dex.Registry) (*dex.Client, *dex.Worker, string, pb.RunsServiceClient) {
	app, _, _ := startE2EServer(t)
	runConn, err := grpc.NewClient(app.RunGRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { runConn.Close() })

	matchConn, err := grpc.NewClient(app.MatchingGRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { matchConn.Close() })

	taskListName := "retry-e2e-" + uuid.NewString()
	client := dex.NewClient(registry, runConn, "default")
	worker := dex.NewWorker(registry, matchConn, runConn, "default", dex.WorkerOptions{
		TaskListName:      taskListName,
		RunConcurrency:    1,
		HeartbeatInterval: 100 * time.Millisecond,
	})
	return client, worker, taskListName, pb.NewRunsServiceClient(runConn)
}

type RetrySuccessFlow struct {
	dex.FlowDefaults
}

func (RetrySuccessFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep[any](&RetryFailThenSucceedStep{}),
	}
}

type RetryFailThenSucceedStep struct {
	dex.StepDefaults[any]
}

func (s *RetryFailThenSucceedStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteMethodTimeout: 2 * time.Second,
		ExecuteMethodRetryPolicy: &dex.RetryPolicy{
			MaxAttempts:        10,
			InitialInterval:    400 * time.Millisecond,
			BackoffCoefficient: 1.0,
			MaximumInterval:    500 * time.Millisecond,
		},
	}
}

func (s *RetryFailThenSucceedStep) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	counter := retryCounterForRun(ctx.RunID())
	attempt := counter.count.Add(1)
	if attempt < 4 {
		return nil, dex.ErrorWrap(fmt.Errorf("transient failure attempt %d", attempt))
	}
	return dex.Complete(nil), nil
}

func TestSDKE2E_StepRetry_InProcessThenSucceeds(t *testing.T) {
	registry := dex.NewRegistry()
	registry.Register(&RetrySuccessFlow{})

	client, worker, taskListName, runsPb := startRetryE2EWithRunsPb(t, registry)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	runID := uuid.NewString()
	retryAttemptCounters.Delete(runID)

	startWorkerBg(t, worker)

	require.NoError(t, client.StartRunWithOptions(ctx, runID, &RetrySuccessFlow{}, &dex.RunOptions{TaskListName: taskListName}))

	require.Eventually(t, func() bool {
		resp, getErr := runsPb.GetRun(ctx, &pb.GetRunRequest{Namespace: "default", RunId: runID})
		if getErr != nil || !resp.Found {
			return false
		}
		for stepExeID, active := range resp.ActiveStepExecutions {
			if active.ExecuteRetryState == nil {
				continue
			}
			if active.ExecuteRetryState.CurrentAttempts >= 1 {
				assert.Contains(t, stepExeID, "RetryFailThenSucceedStep")
				assert.NotEmpty(t, active.ExecuteRetryState.LastErrorStackTrace)
				assert.Contains(t, active.ExecuteRetryState.LastErrorStackTrace, ".Execute")
				assert.Contains(t, active.ExecuteRetryState.LastErrorStackTrace, "+0x")
				assert.Contains(t, active.ExecuteRetryState.LastErrorStackTrace, ".invokeOnce")
				assert.Greater(t, active.ExecuteRetryState.FirstAttemptTimeMs, int64(0))
				return true
			}
		}
		return false
	}, 15*time.Second, 200*time.Millisecond)

	require.Eventually(t, func() bool {
		got, err := client.GetRun(ctx, runID)
		return err == nil && got != nil && got.Status == int32(dex.RunStatusCompleted)
	}, 30*time.Second, 300*time.Millisecond)

	assert.GreaterOrEqual(t, retryCounterForRun(runID).count.Load(), int32(4))
}

type RetryTimeoutFlow struct {
	dex.FlowDefaults
}

func (RetryTimeoutFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep[any](&RetryTimeoutStep{}),
	}
}

type RetryTimeoutStep struct {
	dex.StepDefaults[any]
}

func (s *RetryTimeoutStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteMethodTimeout: 200 * time.Millisecond,
		ExecuteMethodRetryPolicy: &dex.RetryPolicy{
			MaxAttempts:     10,
			InitialInterval: 100 * time.Millisecond,
		},
	}
}

func (s *RetryTimeoutStep) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	counter := retryCounterForRun(ctx.RunID())
	attempt := counter.count.Add(1)
	if attempt <= 2 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
			return dex.Complete(nil), nil
		}
	}
	return dex.Complete(nil), nil
}

func TestSDKE2E_StepRetry_TimeoutSurfacesInRetryState(t *testing.T) {
	registry := dex.NewRegistry()
	registry.Register(&RetryTimeoutFlow{})

	client, worker, taskListName, runsPb := startRetryE2EWithRunsPb(t, registry)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	runID := uuid.NewString()
	startWorkerBg(t, worker)

	require.NoError(t, client.StartRunWithOptions(ctx, runID, &RetryTimeoutFlow{}, &dex.RunOptions{TaskListName: taskListName}))

	require.Eventually(t, func() bool {
		resp, getErr := runsPb.GetRun(ctx, &pb.GetRunRequest{Namespace: "default", RunId: runID})
		if getErr != nil || !resp.Found {
			return false
		}
		for _, active := range resp.ActiveStepExecutions {
			if active.ExecuteRetryState == nil || active.ExecuteRetryState.CurrentAttempts < 1 {
				continue
			}
			if active.ExecuteRetryState.LastError != "" {
				return true
			}
		}
		return false
	}, 20*time.Second, 200*time.Millisecond)

	require.Eventually(t, func() bool {
		got, err := client.GetRun(ctx, runID)
		return err == nil && got != nil && got.Status == int32(dex.RunStatusCompleted)
	}, 30*time.Second, 300*time.Millisecond)
}

type RetryResumeFlow struct {
	dex.FlowDefaults
}

func (RetryResumeFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep[any](&RetryResumeStep{}),
	}
}

type RetryResumeStep struct {
	dex.StepDefaults[any]
}

func (s *RetryResumeStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteMethodRetryPolicy: &dex.RetryPolicy{
			MaxAttempts:        10,
			InitialInterval:    500 * time.Millisecond,
			BackoffCoefficient: 1.0,
		},
	}
}

func (s *RetryResumeStep) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	counter := retryCounterForRun(ctx.RunID())
	attempt := counter.count.Add(1)
	if attempt == 1 {
		return nil, dex.ErrorWrap(fmt.Errorf("first attempt fails for resume test"))
	}
	return dex.Complete(nil), nil
}

func TestSDKE2E_StepRetry_ResumeAfterReleaseRun(t *testing.T) {
	registry := dex.NewRegistry()
	registry.Register(&RetryResumeFlow{})

	app, _, _ := startE2EServer(t)
	runConn, err := grpc.NewClient(app.RunGRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { runConn.Close() })
	matchConn, err := grpc.NewClient(app.MatchingGRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { matchConn.Close() })

	taskListName := "retry-resume-" + uuid.NewString()
	client := dex.NewClient(registry, runConn, "default")
	worker1 := dex.NewWorker(registry, matchConn, runConn, "default", dex.WorkerOptions{
		TaskListName: taskListName, RunConcurrency: 1, HeartbeatInterval: 100 * time.Millisecond,
	})
	worker2 := dex.NewWorker(registry, matchConn, runConn, "default", dex.WorkerOptions{
		TaskListName: taskListName, RunConcurrency: 1, HeartbeatInterval: 100 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	runID := uuid.NewString()
	retryAttemptCounters.Delete(runID)

	workerDone1 := make(chan error, 1)
	go func() { workerDone1 <- worker1.Start() }()
	waitWorkerReady(t, worker1)

	require.NoError(t, client.StartRunWithOptions(ctx, runID, &RetryResumeFlow{}, &dex.RunOptions{TaskListName: taskListName}))

	require.Eventually(t, func() bool {
		return retryCounterForRun(runID).count.Load() >= 1
	}, 30*time.Second, 200*time.Millisecond)

	shardID := app.ShardMapper.GetShardID("default", runID)
	runRow, getErr := app.RunStore.GetRun(ctx, shardID, "default", runID, p.GetRunOptions{})
	require.Nil(t, getErr)
	require.NotEmpty(t, runRow.WorkerID)

	worker1.Stop()
	<-workerDone1

	_, releaseErr := pb.NewRunsServiceClient(runConn).ProcessReleaseRun(ctx, &pb.ProcessReleaseRunRequest{
		Namespace:     "default",
		RunId:         runID,
		WorkerId:      runRow.WorkerID,
		ReleaseReason: pb.ReleaseRunReason_RELEASE_RUN_REASON_YIELD_TO_ANOTHER_WORKER,
	})
	require.NoError(t, releaseErr)

	workerDone2 := make(chan error, 1)
	go func() { workerDone2 <- worker2.Start() }()
	defer func() { worker2.Stop(); <-workerDone2 }()

	require.Eventually(t, func() bool {
		got, err := client.GetRun(ctx, runID)
		return err == nil && got != nil && got.Status == int32(dex.RunStatusCompleted)
	}, 45*time.Second, 300*time.Millisecond)

	assert.GreaterOrEqual(t, retryCounterForRun(runID).count.Load(), int32(2))
}

type RetryExhaustFlow struct {
	dex.FlowDefaults
}

func (RetryExhaustFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep[any](&RetryExhaustStep{}),
	}
}

type RetryExhaustStep struct {
	dex.StepDefaults[any]
}

func (s *RetryExhaustStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteMethodRetryPolicy: &dex.RetryPolicy{
			MaxAttempts:     2,
			InitialInterval: 50 * time.Millisecond,
		},
	}
}

func (s *RetryExhaustStep) Execute(_ dex.Context, _ any) (dex.StepDecision, error) {
	return nil, dex.ErrorWrap(fmt.Errorf("always fails"))
}

func TestSDKE2E_StepRetry_ExhaustionFailsRun(t *testing.T) {
	registry := dex.NewRegistry()
	registry.Register(&RetryExhaustFlow{})

	client, worker, taskListName, opsPb := startSDKE2EWithOpsPb(t, registry)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	runID := uuid.NewString()
	startWorkerBg(t, worker)

	require.NoError(t, client.StartRunWithOptions(ctx, runID, &RetryExhaustFlow{}, &dex.RunOptions{TaskListName: taskListName}))

	require.Eventually(t, func() bool {
		got, err := client.GetRun(ctx, runID)
		return err == nil && got != nil && got.Status == int32(dex.RunStatusFailed)
	}, 20*time.Second, 200*time.Millisecond)

	hist, err := opsPb.GetHistoryEvents(ctx, &pb.GetHistoryEventsRequest{
		Namespace: "default",
		RunId:     runID,
	})
	require.NoError(t, err)
	var executeReport *pb.StepMethodReport
	for _, event := range hist.Events {
		exec := event.GetStepExecuteCompleted()
		if exec == nil || exec.ExecuteMethod == nil {
			continue
		}
		executeReport = exec.ExecuteMethod
		break
	}
	require.NotNil(t, executeReport)
	assert.Equal(t, pb.StepMethodOutcome_STEP_METHOD_OUTCOME_FAILED, executeReport.Outcome)
	assert.NotEmpty(t, executeReport.Error)
	assert.NotEmpty(t, executeReport.ErrorStackTrace)
	assert.Contains(t, executeReport.ErrorStackTrace, ".Execute")
	assert.Contains(t, executeReport.ErrorStackTrace, "+0x")
	assert.Contains(t, executeReport.ErrorStackTrace, ".invokeOnce")
	assert.GreaterOrEqual(t, executeReport.AttemptCount, int32(2))
}

func TestSDKE2E_StepRetry_RecoveredRunSurfacesErrorStackTraceInHistory(t *testing.T) {
	registry := dex.NewRegistry()
	registry.Register(&RetrySuccessFlow{})

	client, worker, taskListName, opsPb := startSDKE2EWithOpsPb(t, registry)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	runID := uuid.NewString()
	retryAttemptCounters.Delete(runID)

	startWorkerBg(t, worker)

	require.NoError(t, client.StartRunWithOptions(ctx, runID, &RetrySuccessFlow{}, &dex.RunOptions{TaskListName: taskListName}))

	require.Eventually(t, func() bool {
		got, err := client.GetRun(ctx, runID)
		return err == nil && got != nil && got.Status == int32(dex.RunStatusCompleted)
	}, 30*time.Second, 300*time.Millisecond)

	hist, err := opsPb.GetHistoryEvents(ctx, &pb.GetHistoryEventsRequest{
		Namespace: "default",
		RunId:     runID,
	})
	require.NoError(t, err)
	var executeReport *pb.StepMethodReport
	for _, event := range hist.Events {
		exec := event.GetStepExecuteCompleted()
		if exec == nil || exec.ExecuteMethod == nil {
			continue
		}
		executeReport = exec.ExecuteMethod
		break
	}
	require.NotNil(t, executeReport)
	assert.Equal(t, pb.StepMethodOutcome_STEP_METHOD_OUTCOME_SUCCEEDED, executeReport.Outcome)
	assert.NotEmpty(t, executeReport.ErrorStackTrace)
	assert.Contains(t, executeReport.ErrorStackTrace, ".Execute")
	assert.Contains(t, executeReport.ErrorStackTrace, "+0x")
	assert.Contains(t, executeReport.ErrorStackTrace, ".invokeOnce")
	assert.GreaterOrEqual(t, executeReport.AttemptCount, int32(4))
}

type RetryNoRetryEscapeFlow struct {
	dex.FlowDefaults
}

func (RetryNoRetryEscapeFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep[any](&RetryNoRetryEscapeStep{}),
	}
}

type RetryNoRetryEscapeStep struct {
	dex.StepDefaults[any]
}

func (s *RetryNoRetryEscapeStep) Execute(_ dex.Context, _ any) (dex.StepDecision, error) {
	return dex.Fail("terminal without retry"), nil
}

func TestSDKE2E_StepRetry_NoRetryWhenFailWithoutError(t *testing.T) {
	registry := dex.NewRegistry()
	registry.Register(&RetryNoRetryEscapeFlow{})

	client, worker, taskListName, runsPb := startRetryE2EWithRunsPb(t, registry)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	runID := uuid.NewString()
	startWorkerBg(t, worker)

	require.NoError(t, client.StartRunWithOptions(ctx, runID, &RetryNoRetryEscapeFlow{}, &dex.RunOptions{TaskListName: taskListName}))

	require.Eventually(t, func() bool {
		got, err := client.GetRun(ctx, runID)
		return err == nil && got != nil && got.Status == int32(dex.RunStatusFailed)
	}, 15*time.Second, 200*time.Millisecond)

	activeSteps := getRunActiveSteps(t, runsPb, ctx, runID)
	for _, active := range activeSteps {
		assert.Nil(t, active.ExecuteRetryState)
		assert.Nil(t, active.WaitForRetryState)
	}
}

type RetryOptionsFlow struct {
	dex.FlowDefaults
}

func (RetryOptionsFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep[any](&RetryOptionsStep{}),
	}
}

type RetryOptionsStep struct {
	dex.StepDefaults[any]
}

func (s *RetryOptionsStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteMethodTimeout: 30 * time.Second,
		ExecuteMethodRetryPolicy: &dex.RetryPolicy{
			MaxAttempts: 5,
		},
	}
}

func (s *RetryOptionsStep) Execute(_ dex.Context, _ any) (dex.StepDecision, error) {
	return dex.Complete(nil), nil
}

func TestSDKE2E_StepRetry_StartingStepOptionsSnapshotInHistory(t *testing.T) {
	registry := dex.NewRegistry()
	registry.Register(&RetryOptionsFlow{})

	client, worker, taskListName, opsPb := startSDKE2EWithOpsPb(t, registry)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	runID := uuid.NewString()
	startWorkerBg(t, worker)

	require.NoError(t, client.StartRunWithOptions(ctx, runID, &RetryOptionsFlow{}, &dex.RunOptions{TaskListName: taskListName}))

	require.Eventually(t, func() bool {
		got, err := client.GetRun(ctx, runID)
		return err == nil && got != nil && got.Status == int32(dex.RunStatusCompleted)
	}, 20*time.Second, 200*time.Millisecond)

	hist, err := opsPb.GetHistoryEvents(ctx, &pb.GetHistoryEventsRequest{
		Namespace: "default",
		RunId:     runID,
	})
	require.NoError(t, err)
	require.NotEmpty(t, hist.Events)
	runStart := hist.Events[0].GetRunStart()
	require.NotNil(t, runStart)
	require.NotEmpty(t, runStart.StartingSteps)
	require.NotNil(t, runStart.StartingSteps[0].StepOptionsSnapshot)
	assert.Equal(t, int64(30000), runStart.StartingSteps[0].StepOptionsSnapshot.ExecuteMethodTimeoutMs)
	require.NotNil(t, runStart.StartingSteps[0].StepOptionsSnapshot.ExecuteMethodRetryPolicy)
	assert.Equal(t, int32(5), runStart.StartingSteps[0].StepOptionsSnapshot.ExecuteMethodRetryPolicy.MaxAttempts)
}

type retryProceedInput struct {
	Token string
}

type RetryExecuteProceedFlow struct {
	dex.FlowDefaults
}

func (RetryExecuteProceedFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep[retryProceedInput](&RetryExecuteProceedFailingStep{}),
		dex.NonStartingStep(&RetryProceedHandlerStep{}),
	}
}

type RetryExecuteProceedFailingStep struct {
	dex.StepDefaults[retryProceedInput]
}

func (s *RetryExecuteProceedFailingStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteMethodRetryPolicy: &dex.RetryPolicy{
			MaxAttempts:     2,
			InitialInterval: 50 * time.Millisecond,
		},
		ExecuteMethodProceedToAfterRetryExhausted: &RetryProceedHandlerStep{},
	}
}

func (s *RetryExecuteProceedFailingStep) Execute(_ dex.Context, _ retryProceedInput) (dex.StepDecision, error) {
	return nil, dex.ErrorWrap(fmt.Errorf("always fails"))
}

type RetryWaitForProceedFlow struct {
	dex.FlowDefaults
}

func (RetryWaitForProceedFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep[retryProceedInput](&RetryWaitForProceedFailingStep{}),
		dex.NonStartingStep(&RetryProceedHandlerStep{}),
	}
}

type RetryWaitForProceedFailingStep struct {
	dex.DefaultStepId
	dex.DefaultStepOptions
}

func (s *RetryWaitForProceedFailingStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		WaitForMethodRetryPolicy: &dex.RetryPolicy{
			MaxAttempts:     2,
			InitialInterval: 50 * time.Millisecond,
		},
		WaitForMethodProceedToAfterRetryExhausted: &RetryProceedHandlerStep{},
	}
}

func (s *RetryWaitForProceedFailingStep) WaitFor(_ dex.Context, _ retryProceedInput) (dex.WaitForCondition, error) {
	return nil, dex.ErrorWrap(fmt.Errorf("waitfor always fails"))
}

func (s *RetryWaitForProceedFailingStep) Execute(_ dex.Context, _ retryProceedInput) (dex.StepDecision, error) {
	return dex.DeadEnd(), nil
}

type RetryProceedHandlerStep struct {
	dex.StepDefaults[retryProceedInput]
}

func (s *RetryProceedHandlerStep) Execute(ctx dex.Context, input retryProceedInput) (dex.StepDecision, error) {
	if ctx.FromStepExecutionID() == "" {
		return nil, fmt.Errorf("expected from step exe id")
	}
	if dex.StepIDFromStepExecutionID(ctx.FromStepExecutionID()) == "" {
		return nil, fmt.Errorf("expected from step id")
	}
	if input.Token == "" {
		return nil, fmt.Errorf("expected token input")
	}
	return dex.Complete(nil), nil
}

func TestSDKE2E_StepRetry_ExecuteProceedAfterExhaustion(t *testing.T) {
	registry := dex.NewRegistry()
	registry.Register(&RetryExecuteProceedFlow{})

	client, worker, taskListName, opsPb := startSDKE2EWithOpsPb(t, registry)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	runID := uuid.NewString()
	token := "proceed-token"
	startWorkerBg(t, worker)

	require.NoError(t, client.StartRunWithOptions(ctx, runID, &RetryExecuteProceedFlow{}, &dex.RunOptions{TaskListName: taskListName}, retryProceedInput{Token: token}))

	require.Eventually(t, func() bool {
		got, err := client.GetRun(ctx, runID)
		return err == nil && got != nil && got.Status == int32(dex.RunStatusCompleted)
	}, 30*time.Second, 200*time.Millisecond)

	hist, err := opsPb.GetHistoryEvents(ctx, &pb.GetHistoryEventsRequest{Namespace: "default", RunId: runID})
	require.NoError(t, err)
	var sawFailedExecute bool
	for _, event := range hist.Events {
		exec := event.GetStepExecuteCompleted()
		if exec == nil || exec.ExecuteMethod == nil {
			continue
		}
		if exec.ExecuteMethod.Outcome == pb.StepMethodOutcome_STEP_METHOD_OUTCOME_FAILED {
			sawFailedExecute = true
			assert.Equal(t, pb.StopDecision_STOP_DECISION_NONE, exec.StopDecision)
			assert.NotEmpty(t, exec.NextSteps)
		}
	}
	assert.True(t, sawFailedExecute)
}

func TestSDKE2E_StepRetry_WaitForProceedAfterExhaustion(t *testing.T) {
	registry := dex.NewRegistry()
	registry.Register(&RetryWaitForProceedFlow{})

	client, worker, taskListName, opsPb := startSDKE2EWithOpsPb(t, registry)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	runID := uuid.NewString()
	startWorkerBg(t, worker)

	require.NoError(t, client.StartRunWithOptions(ctx, runID, &RetryWaitForProceedFlow{}, &dex.RunOptions{TaskListName: taskListName}, retryProceedInput{Token: "wf-token"}))

	require.Eventually(t, func() bool {
		got, err := client.GetRun(ctx, runID)
		return err == nil && got != nil && got.Status == int32(dex.RunStatusCompleted)
	}, 30*time.Second, 200*time.Millisecond)

	hist, err := opsPb.GetHistoryEvents(ctx, &pb.GetHistoryEventsRequest{Namespace: "default", RunId: runID})
	require.NoError(t, err)
	var sawFailedWaitFor bool
	for _, event := range hist.Events {
		waitFor := event.GetStepWaitForCompleted()
		if waitFor == nil || waitFor.WaitForMethod == nil {
			continue
		}
		if waitFor.WaitForMethod.Outcome == pb.StepMethodOutcome_STEP_METHOD_OUTCOME_FAILED {
			sawFailedWaitFor = true
			assert.NotEmpty(t, waitFor.NextSteps)
		}
	}
	assert.True(t, sawFailedWaitFor)
}

func TestSDKE2E_StepRetry_WaitForExhaustionFailsRun(t *testing.T) {
	registry := dex.NewRegistry()
	registry.Register(&RetryWaitForExhaustFlow{})

	client, worker, taskListName, _ := startSDKE2EWithOpsPb(t, registry)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	runID := uuid.NewString()
	startWorkerBg(t, worker)

	require.NoError(t, client.StartRunWithOptions(ctx, runID, &RetryWaitForExhaustFlow{}, &dex.RunOptions{TaskListName: taskListName}))

	require.Eventually(t, func() bool {
		got, err := client.GetRun(ctx, runID)
		return err == nil && got != nil && got.Status == int32(dex.RunStatusFailed)
	}, 20*time.Second, 200*time.Millisecond)
}

type RetryWaitForExhaustFlow struct {
	dex.FlowDefaults
}

func (RetryWaitForExhaustFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.StartingStep[any](&RetryWaitForExhaustStep{})}
}

type RetryWaitForExhaustStep struct {
	dex.DefaultStepId
	dex.DefaultStepOptions
}

func (s *RetryWaitForExhaustStep) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		WaitForMethodRetryPolicy: &dex.RetryPolicy{
			MaxAttempts:     2,
			InitialInterval: 50 * time.Millisecond,
		},
	}
}

func (s *RetryWaitForExhaustStep) WaitFor(_ dex.Context, _ any) (dex.WaitForCondition, error) {
	return nil, dex.ErrorWrap(fmt.Errorf("always fails"))
}

func (s *RetryWaitForExhaustStep) Execute(_ dex.Context, _ any) (dex.StepDecision, error) {
	return dex.DeadEnd(), nil
}

func getRunActiveSteps(t *testing.T, runsPb pb.RunsServiceClient, ctx context.Context, runID string) map[string]*pb.ActiveStepExecution {
	t.Helper()
	resp, err := runsPb.GetRun(ctx, &pb.GetRunRequest{Namespace: "default", RunId: runID})
	require.NoError(t, err)
	require.True(t, resp.Found)
	return resp.ActiveStepExecutions
}
