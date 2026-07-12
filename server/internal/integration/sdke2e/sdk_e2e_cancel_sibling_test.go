package sdke2e

import (
	"context"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	cancelKeyFastRan = dex.NewStateKey[bool]("fast_ran")
	cancelKeySlowRan = dex.NewStateKey[bool]("slow_ran")
)

// ============================================================================
// CancelSiblingStepExecution end-to-end coverage.
//
// These tests exercise the full SDK → worker → server commit path for
// StepDecision.WithCancelingSiblingStepExecution. Two scenarios:
//
//  1. Waiting-state cancel: the cancelled sibling is parked in
//     WAITING_FOR_CONDITION on a never-published channel; absent the
//     cancel the run would block forever. The cancelling step's
//     completion must arrive at the engine carrying the resolved
//     step_exe_id and the engine must delete it from
//     ActiveStepExecutions in the same commit.
//  2. In-flight Execute cancel: both siblings transition to
//     INVOKING_EXECUTE. The slow sibling's Execute blocks on its
//     ctx.Done(); the fast sibling cancels it. Verifies (a) the
//     slow goroutine observes ctx cancellation (well-behaved Step
//     contract) and (b) the worker DOES NOT send a redundant
//     StepExecuteCompleted RPC for the cancelled exe-id (it would
//     get rejected as a stale exe-id anyway, but suppressing it is
//     the documented contract).
// ============================================================================

// CancelE2EState is the shared state for both cancel-sibling test
// flows. The bools mark which branch actually ran Execute so the test
// can distinguish "cancelled before running" from "ran to completion".
type CancelE2EState struct {
	FastRan bool `json:"fast_ran"`
	SlowRan bool `json:"slow_ran"`
}

// --- Flow A: waiting-state cancel ----------------------------------------

// cancelDispatchStep fans out to fastWaitStep + slowWaitStep. Both are
// non-starting WaitFor steps so each parks in WAITING_FOR_CONDITION
// before any Execute runs.
type cancelDispatchStep struct {
	dex.StepDefaults[any]
}

func (s *cancelDispatchStep) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	return dex.GoToMany(
		dex.MovementOf(&fastWaitStep{}, nil),
		dex.MovementOf(&slowWaitStep{}, nil),
	), nil
}

// fastWaitStep waits on its own dynamic-channel instance. When that
// publish arrives, its Execute runs, marks state.FastRan, and CANCELS
// the sibling slowWaitStep so the run can complete despite slow's
// channel never being published.
type fastWaitStep struct {
	dex.DefaultStepId
	dex.DefaultStepOptions
}

func (s *fastWaitStep) WaitFor(_ dex.Context, _ any) (dex.WaitForCondition, error) {
	return dex.AnyOf(cancelE2EChannel.Condition("fast")), nil
}

func (s *fastWaitStep) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	if err := cancelKeyFastRan.SetValue(ctx, true); err != nil {
		return nil, err
	}
	return dex.DeadEnd().
		WithCancelingSiblingStepExecution(dex.CancelOf(&slowWaitStep{})), nil
}

// slowWaitStep waits forever (channel "slow" is never published).
// If cancellation works, this Execute is never reached.
type slowWaitStep struct {
	dex.DefaultStepId
	dex.DefaultStepOptions
}

func (s *slowWaitStep) WaitFor(_ dex.Context, _ any) (dex.WaitForCondition, error) {
	// 30 minute timer just to keep the test deterministic if cancellation
	// regresses — the test will fail on the WaitForRunComplete timeout
	// instead of hanging.
	return dex.AnyOf(
		cancelE2EChannel.Condition("slow"),
		dex.Timer(30*time.Minute),
	), nil
}

func (s *slowWaitStep) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	if err := cancelKeySlowRan.SetValue(ctx, true); err != nil {
		return nil, err
	}
	return dex.DeadEnd(), nil
}

type CancelE2EFlow struct {
	dex.FlowDefaults
}

func (f *CancelE2EFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep[any](&cancelDispatchStep{}),
		dex.NonStartingStep[any](&fastWaitStep{}),
		dex.NonStartingStep[any](&slowWaitStep{}),
	}
}

func (f *CancelE2EFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		StateKeys: []dex.StateKeyDef{
			dex.DefineStateKey(cancelKeyFastRan),
			dex.DefineStateKey(cancelKeySlowRan),
		},
		DynamicChannels: []dex.ChannelDef{dex.DefineDynamicChannel(cancelE2EChannel)},
	}
}

var cancelE2EChannel = dex.NewDynamicChannel[map[string]any]("cancel-e2e-")

// TestSDKE2E_CancelSibling_WaitingStateGetsCancelled is the headline
// regression: a sibling parked in WAITING_FOR_CONDITION can be
// cancelled by a peer's StepDecision and the run completes promptly
// without that sibling's Execute ever running.
func TestSDKE2E_CancelSibling_WaitingStateGetsCancelled(t *testing.T) {
	registry := dex.NewRegistry()
	registry.Register(&CancelE2EFlow{})

	app, _, _ := startE2EServer(t)

	runConn, err := grpc.NewClient(app.RunGRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { runConn.Close() })

	matchConn, err := grpc.NewClient(app.MatchingGRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { matchConn.Close() })

	opsConn, err := grpc.NewClient(app.OpsGRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { opsConn.Close() })

	taskListName := "sdk-e2e-cancel-waiting-" + uuid.NewString()
	client := dex.NewClient(registry, runConn, "default")
	worker := dex.NewWorker(registry, matchConn, runConn, "default", dex.WorkerOptions{
		TaskListName:   taskListName,
		RunConcurrency: 1,
	})
	opsPb := pb.NewOpsServiceClient(opsConn)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	startWorkerBg(t, worker)

	runID := uuid.NewString()
	require.NoError(t, client.StartRunWithOptions(ctx, runID, &CancelE2EFlow{}, &dex.RunOptions{TaskListName: taskListName}))

	// Wait for AllStepsWaiting so both siblings are parked on their
	// dynamic-channel waits before we publish "fast". This makes the
	// publish trigger the documented re-dispatch path the cancellation
	// must traverse.
	require.Eventually(t, func() bool {
		got, gErr := client.GetRun(ctx, runID)
		require.NoError(t, gErr)
		return got != nil && got.Status == dex.RunStatusAllStepsWaitingForConditions
	}, 5*time.Second, 50*time.Millisecond, "run must reach AllStepsWaiting before the cancel-issuing publish")

	require.NoError(t, client.PublishToDynamicChannel(ctx, runID, cancelE2EChannel.Prefix, "fast", map[string]any{"v": "go"}))

	status, err := client.WaitForRunComplete(ctx, runID)
	require.NoError(t, err)
	fastRan, err := cancelKeyFastRan.GetRunValue(client, ctx, runID)
	require.NoError(t, err)
	slowRan, err := cancelKeySlowRan.GetRunValue(client, ctx, runID)
	require.NoError(t, err)
	assert.Equal(t, dex.RunStatusCompleted, status, "run must complete promptly via the cancel path")
	assert.True(t, fastRan, "fast sibling must have run Execute")
	assert.False(t, slowRan, "slow sibling MUST NOT have run Execute (it was cancelled while WAITING_FOR_CONDITION)")

	// Verify the StepExecuteCompleted history event for fast carries
	// the cancelled exe-id. The OpsFIFO outbox drains asynchronously
	// after the engine commits, so poll until fast's event surfaces
	// before reading its CanceledStepExecutions field. (Same pattern
	// as sdk_e2e_step_provenance_test.go.)
	const fastExeID = "sdke2e.fastWaitStep-1"
	const slowExeID = "sdke2e.slowWaitStep-1"
	var fastCancelled []string
	var sawSlowExecute bool
	require.Eventually(t, func() bool {
		hist, hErr := opsPb.GetHistoryEvents(ctx, &pb.GetHistoryEventsRequest{
			Namespace: "default", RunId: runID, Limit: 200,
		})
		if hErr != nil {
			return false
		}
		fastCancelled = nil
		sawSlowExecute = false
		var sawFast bool
		for _, ev := range hist.Events {
			if exec := ev.GetStepExecuteCompleted(); exec != nil {
				if exec.StepExeId == fastExeID {
					sawFast = true
					fastCancelled = append(fastCancelled, exec.CanceledStepExecutions...)
				}
				if exec.StepExeId == slowExeID {
					sawSlowExecute = true
				}
			}
		}
		return sawFast
	}, 10*time.Second, 100*time.Millisecond,
		"fast's StepExecuteCompleted must surface in history within the OpsFIFO drain window")

	require.False(t, sawSlowExecute, "history MUST NOT contain slow's StepExecuteCompleted (it was cancelled)")
	sort.Strings(fastCancelled)
	assert.Equal(t, []string{slowExeID}, fastCancelled,
		"fast's StepExecuteCompleted must carry slow's exe_id in canceled_step_executions")
}

// --- Flow B: in-flight Execute cancel ----------------------------------

// inflightSlowExecuteObserved tracks whether the slow Execute saw its
// ctx cancelled. Set from inside the slow step's Execute via atomic
// store so the test's main goroutine can read it.
var inflightSlowExecuteObserved atomic.Bool

// inflightDispatchStep fans out two NoWaitFor children so they both
// transition straight to INVOKING_EXECUTE — exercising the in-flight
// goroutine cancellation path.
type inflightDispatchStep struct {
	dex.StepDefaults[any]
}

func (s *inflightDispatchStep) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	return dex.GoToMany(
		dex.MovementOf(&inflightFastStep{}, nil),
		dex.MovementOf(&inflightSlowStep{}, nil),
	), nil
}

// inflightFastStep does a brief sleep so the slow sibling's goroutine
// has time to start blocking on ctx.Done() before fast's decision
// arrives at the worker's main select. Then DeadEnds + cancels slow.
type inflightFastStep struct {
	dex.StepDefaults[any]
}

func (s *inflightFastStep) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	// Small sleep to give the slow goroutine a chance to enter its
	// ctx-blocking select before we cancel it. Without this the test
	// race is between "slow blocks, fast cancels, slow returns
	// ctx.Canceled" and "fast cancels before slow has even started its
	// select" (still correct, but not testing what we want).
	time.Sleep(100 * time.Millisecond)
	if err := cancelKeyFastRan.SetValue(ctx, true); err != nil {
		return nil, err
	}
	return dex.DeadEnd().
		WithCancelingSiblingStepExecution(dex.CancelOf(&inflightSlowStep{})), nil
}

// inflightSlowStep blocks on ctx.Done() forever. A working
// in-flight ctx-cancellation path means it returns ctx.Canceled
// promptly when fast cancels it. The completion is then suppressed
// by the worker (cancelledExeIDs check) and never reaches the engine.
type inflightSlowStep struct {
	dex.StepDefaults[any]
}

func (s *inflightSlowStep) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	select {
	case <-ctx.Done():
		inflightSlowExecuteObserved.Store(true)
		return dex.DeadEnd(), ctx.Err()
	case <-time.After(20 * time.Second):
		// Fail-loud fallback so a regression doesn't silently hang the test.
		return dex.Fail("inflightSlowStep was NOT cancelled within 20s — ctx-cancel path regressed"), nil
	}
}

type InflightCancelFlow struct {
	dex.FlowDefaults
}

func (f *InflightCancelFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep[any](&inflightDispatchStep{}),
		dex.NonStartingStep[any](&inflightFastStep{}),
		dex.NonStartingStep[any](&inflightSlowStep{}),
	}
}

func (f *InflightCancelFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		StateKeys: []dex.StateKeyDef{
			dex.DefineStateKey(cancelKeyFastRan),
			dex.DefineStateKey(cancelKeySlowRan),
		},
	}
}

// TestSDKE2E_CancelSibling_InflightExecuteGetsCtxCancelled covers the
// per-task ctx-cancellation contract: a sibling currently inside
// Execute on the same worker must see its ctx cancelled, AND the
// worker must NOT forward the cancelled goroutine's eventual
// completion to the engine (no second StepExecuteCompleted for the
// slow step in history).
func TestSDKE2E_CancelSibling_InflightExecuteGetsCtxCancelled(t *testing.T) {
	inflightSlowExecuteObserved.Store(false)

	registry := dex.NewRegistry()
	registry.Register(&InflightCancelFlow{})

	app, _, _ := startE2EServer(t)

	runConn, err := grpc.NewClient(app.RunGRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { runConn.Close() })

	matchConn, err := grpc.NewClient(app.MatchingGRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { matchConn.Close() })

	opsConn, err := grpc.NewClient(app.OpsGRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { opsConn.Close() })

	taskListName := "sdk-e2e-cancel-inflight-" + uuid.NewString()
	client := dex.NewClient(registry, runConn, "default")
	worker := dex.NewWorker(registry, matchConn, runConn, "default", dex.WorkerOptions{
		TaskListName:   taskListName,
		RunConcurrency: 1,
	})
	opsPb := pb.NewOpsServiceClient(opsConn)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	startWorkerBg(t, worker)

	runID := uuid.NewString()
	require.NoError(t, client.StartRunWithOptions(ctx, runID, &InflightCancelFlow{}, &dex.RunOptions{TaskListName: taskListName}))

	status, err := client.WaitForRunComplete(ctx, runID)
	require.NoError(t, err)
	fastRan, err := cancelKeyFastRan.GetRunValue(client, ctx, runID)
	require.NoError(t, err)
	slowRan, err := cancelKeySlowRan.GetRunValue(client, ctx, runID)
	require.NoError(t, err)
	assert.Equal(t, dex.RunStatusCompleted, status, "run must complete via the cancel-inflight path")
	assert.True(t, fastRan, "fast sibling must have committed its state delta")
	assert.False(t, slowRan,
		"slow sibling's state delta must NOT have been committed (its completion was suppressed by cancellation)")

	// The slow goroutine must have observed ctx cancellation. This is
	// the headline assertion for the per-task ctx-cancel wiring.
	assert.True(t, inflightSlowExecuteObserved.Load(),
		"slow Execute must have observed ctx.Done() within the 20s safety window — confirms per-task cancel func was invoked")

	// History must contain fast's Execute event (with slow's exe_id in
	// canceled_step_executions) but NOT slow's. The latter proves the
	// worker-side suppression: the cancelled goroutine's late completion
	// did NOT round-trip to the engine. Poll until fast's event lands
	// to dodge the OpsFIFO drain race.
	const fastExeID = "sdke2e.inflightFastStep-1"
	const slowExeID = "sdke2e.inflightSlowStep-1"
	var sawSlowExecute bool
	var fastCancelled []string
	require.Eventually(t, func() bool {
		hist, hErr := opsPb.GetHistoryEvents(ctx, &pb.GetHistoryEventsRequest{
			Namespace: "default", RunId: runID, Limit: 200,
		})
		if hErr != nil {
			return false
		}
		fastCancelled = nil
		sawSlowExecute = false
		var sawFast bool
		for _, ev := range hist.Events {
			if exec := ev.GetStepExecuteCompleted(); exec != nil {
				if exec.StepExeId == slowExeID {
					sawSlowExecute = true
				}
				if exec.StepExeId == fastExeID {
					sawFast = true
					fastCancelled = append(fastCancelled, exec.CanceledStepExecutions...)
				}
			}
		}
		return sawFast
	}, 10*time.Second, 100*time.Millisecond,
		"fast's StepExecuteCompleted must surface in history within the OpsFIFO drain window")

	assert.False(t, sawSlowExecute,
		"slow's StepExecuteCompleted MUST NOT appear in history — worker suppression dropped it")
	sort.Strings(fastCancelled)
	assert.Equal(t, []string{slowExeID}, fastCancelled,
		"fast's StepExecuteCompleted must carry slow's exe_id (the resolved cancel target)")
}
