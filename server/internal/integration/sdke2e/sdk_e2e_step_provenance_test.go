package sdke2e

import (
	"context"
	"sort"
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
	provKeyA = dex.NewStateKey[bool]("a")
	provKeyB = dex.NewStateKey[bool]("b")
)

// ============================================================================
// Regression: StepExecuteCompleted.from_step_exe_id must survive re-dispatch
//
// The bug we're guarding against: a parent step's GoToMany spawns N children
// with WaitFor. The children block in WAITING_FOR_CONDITION; the run
// transitions to AllStepsWaitingForConditions and the worker stream closes.
// When an external event later wakes the run up and a (possibly different)
// worker picks it up via PollForRun, the child has lost the in-memory
// linkage to its parent — and so its StepExecuteCompleted RPC carries
// from_step_exe_id="". The WebUI's buildGraph then renders the child as a
// direct successor of __start instead of the real parent.
//
// Before the fix, the WebUI graph for benchmark/dynamicChannelFlow showed
// dispatchOrdersStep AND all 6 fan-out children as siblings of RUN_START
// (recurring user-visible bug; reported with screenshot in the chat that
// produced this fix).
//
// The fix lives in:
//   - protocol-grpc/protos/dex.proto: from_step_exe_id added to
//     ActiveStepExecution (the message that goes back to workers in
//     PollResponse).
//   - server/internal/persistence/interfaces.go: FromStepExeID added to
//     persistence.ActiveStepExecution (so it survives re-dispatch).
//   - server/internal/engine/run_engine.go: every ActiveStepExecution
//     write site preserves it; tryProcessStepExecuteCompleted overrides
//     req.FromStepExeId from the persisted value before history is written.
//   - sdk-go/dex/worker.go: stamps stepTask.fromStepExeID from
//     PollResponse on every load path.
//
// This test asserts the SERVER-AUTHORITATIVE history correctness: regardless
// of what value a re-dispatched worker sends, the StepExecuteCompleted
// history event for any non-starting step MUST carry the parent's
// step_exe_id (not "").
// ============================================================================

// ProvFanOutState carries one boolean per child branch so we can assert all
// branches actually completed (and through the WaitFor → Execute resume path).
type ProvFanOutState struct {
	A bool `json:"a"`
	B bool `json:"b"`
}

// provFanOutDispatchStep is the shared parent — does a GoToMany into two
// children, each waiting on its own dynamic-channel instance. Two children
// (instead of one) is the minimum that makes "fan-out" observable in the
// graph: with only one child you can't tell whether the engine is using the
// parent or accidentally falling back to __start.
type provFanOutDispatchStep struct {
	dex.StepDefaults[any]
}

func (s *provFanOutDispatchStep) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	return dex.GoToMany(
		dex.MovementOf(&provFanOutChildA{}, nil),
		dex.MovementOf(&provFanOutChildB{}, nil),
	), nil
}

// provFanOutChildA / provFanOutChildB: each waits on its own dynamic
// channel instance so external publishes can target one without unblocking
// the other. The WaitFor is the load-bearing detail — it forces the run to
// reach AllStepsWaiting (worker releases stream), so when the publish wakes
// the run up the child enters its Execute via the re-dispatch path that
// historically lost the parent linkage.
type provFanOutChildA struct {
	dex.DefaultStepId
	dex.DefaultStepOptions
}

func (s *provFanOutChildA) WaitFor(_ dex.Context, _ any) (dex.WaitForCondition, error) {
	return dex.AnyOf(provFanOutChannel.Condition("a")), nil
}

func (s *provFanOutChildA) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	if err := provKeyA.SetValue(ctx, true); err != nil {
		return nil, err
	}
	return dex.DeadEnd(), nil
}

type provFanOutChildB struct {
	dex.DefaultStepId
	dex.DefaultStepOptions
}

func (s *provFanOutChildB) WaitFor(_ dex.Context, _ any) (dex.WaitForCondition, error) {
	return dex.AnyOf(provFanOutChannel.Condition("b")), nil
}

func (s *provFanOutChildB) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	if err := provKeyB.SetValue(ctx, true); err != nil {
		return nil, err
	}
	return dex.DeadEnd(), nil
}

type ProvFanOutFlow struct {
	dex.FlowDefaults
}

func (f *ProvFanOutFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep[any](&provFanOutDispatchStep{}),
		dex.NonStartingStep[any](&provFanOutChildA{}),
		dex.NonStartingStep[any](&provFanOutChildB{}),
	}
}

func (f *ProvFanOutFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		StateKeys: []dex.StateKeyDef{
			dex.DefineStateKey(provKeyA),
			dex.DefineStateKey(provKeyB),
		},
		DynamicChannels: []dex.ChannelDef{dex.DefineDynamicChannel(provFanOutChannel)},
	}
}

var provFanOutChannel = dex.NewDynamicChannel[map[string]any]("prov-fanout-")

// TestSDKE2E_StepProvenance_FromStepExeIDSurvivesReDispatch is the regression
// test for the WebUI graph bug where children of a fan-out parent rendered
// as siblings of __start. The smoking gun is the from_step_exe_id field on
// each child's StepExecuteCompleted history event — it MUST equal the
// parent's step_exe_id (not "" and not the child's own id).
func TestSDKE2E_StepProvenance_FromStepExeIDSurvivesReDispatch(t *testing.T) {
	registry := dex.NewRegistry()
	registry.Register(&ProvFanOutFlow{})

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

	taskListName := "sdk-e2e-prov-" + uuid.NewString()
	client := dex.NewClient(registry, runConn, "default")
	worker := dex.NewWorker(registry, matchConn, runConn, "default", dex.WorkerOptions{
		TaskListName:   taskListName,
		RunConcurrency: 1,
	})
	opsPb := pb.NewOpsServiceClient(opsConn)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	startWorkerBg(t, worker)

	runID := uuid.NewString()
	require.NoError(t, client.StartRunWithOptions(ctx, runID, &ProvFanOutFlow{}, &dex.RunOptions{TaskListName: taskListName}))

	// Wait for the run to settle into AllStepsWaiting (both children have
	// armed their WaitFor conditions and the worker has released the
	// stream). This is the critical state for the regression: the next
	// dispatch will be a RE-dispatch where the worker has no in-memory
	// linkage to the parent. With the bug, the child's eventual
	// StepExecuteCompleted would carry from_step_exe_id="".
	require.Eventually(t, func() bool {
		got, gErr := client.GetRun(ctx, runID)
		require.NoError(t, gErr)
		require.NotNil(t, got)
		return got.Status == dex.RunStatusAllStepsWaitingForConditions
	}, 5*time.Second, 50*time.Millisecond,
		"run must reach AllStepsWaiting before the publish so the wake-up exercises the re-dispatch path")

	require.NoError(t, client.PublishToDynamicChannel(ctx, runID, provFanOutChannel.Prefix, "a", map[string]any{"v": "ka"}))
	require.NoError(t, client.PublishToDynamicChannel(ctx, runID, provFanOutChannel.Prefix, "b", map[string]any{"v": "kb"}))

	status, err := client.WaitForRunComplete(ctx, runID)
	require.NoError(t, err)
	childA, err := provKeyA.GetRunValue(client, ctx, runID)
	require.NoError(t, err)
	childB, err := provKeyB.GetRunValue(client, ctx, runID)
	require.NoError(t, err)
	require.Equal(t, dex.RunStatusCompleted, status)
	require.True(t, childA, "child A must complete")
	require.True(t, childB, "child B must complete")

	const dispatchExeID = "sdke2e.provFanOutDispatchStep-1"
	const childAExeID = "sdke2e.provFanOutChildA-1"
	const childBExeID = "sdke2e.provFanOutChildB-1"

	// History is written via OpsFIFO after the run row CAS; poll until every
	// event this test asserts on is visible (same pattern as ops_service_test).
	var executeParents, waitForParents map[string]string
	var executeStepIDs, waitForStepIDs []string
	require.Eventually(t, func() bool {
		hist, histErr := opsPb.GetHistoryEvents(ctx, &pb.GetHistoryEventsRequest{
			Namespace: "default",
			RunId:     runID,
			AfterId:   0,
			Limit:     200,
		})
		if histErr != nil {
			return false
		}

		executeParents = map[string]string{}
		waitForParents = map[string]string{}
		executeStepIDs = nil
		waitForStepIDs = nil
		for _, ev := range hist.Events {
			if exec := ev.GetStepExecuteCompleted(); exec != nil {
				executeParents[exec.StepExeId] = exec.FromStepExeId
				executeStepIDs = append(executeStepIDs, exec.StepExeId)
			}
			if wait := ev.GetStepWaitForCompleted(); wait != nil {
				waitForParents[wait.StepExeId] = wait.FromStepExeId
				waitForStepIDs = append(waitForStepIDs, wait.StepExeId)
			}
		}
		_, hasDispatch := executeParents[dispatchExeID]
		_, hasChildAExec := executeParents[childAExeID]
		_, hasChildBExec := executeParents[childBExeID]
		_, hasChildAWait := waitForParents[childAExeID]
		_, hasChildBWait := waitForParents[childBExeID]
		return hasDispatch && hasChildAExec && hasChildBExec && hasChildAWait && hasChildBWait
	}, 5*time.Second, 50*time.Millisecond,
		"history must include all StepExecuteCompleted and StepWaitForCompleted events")
	sort.Strings(executeStepIDs)
	sort.Strings(waitForStepIDs)

	// --- StepExecuteCompleted assertions ---

	require.Contains(t, executeParents, dispatchExeID,
		"history must include the dispatch step's StepExecuteCompleted; got events for: %v", executeStepIDs)
	require.Contains(t, executeParents, childAExeID,
		"history must include child A's StepExecuteCompleted; got events for: %v", executeStepIDs)
	require.Contains(t, executeParents, childBExeID,
		"history must include child B's StepExecuteCompleted; got events for: %v", executeStepIDs)

	assert.Equal(t, "", executeParents[dispatchExeID],
		"starting step's from_step_exe_id must be empty (no parent)")

	// THE EXECUTE-SIDE REGRESSION ASSERTIONS. Pre-fix both would equal "".
	assert.Equal(t, dispatchExeID, executeParents[childAExeID],
		"child A's StepExecuteCompleted MUST carry the dispatch step's exe_id as from_step_exe_id "+
			"(server-authoritative override; preserved across the AllStepsWaiting re-dispatch). "+
			"If this fails the WebUI graph will render child A as a sibling of dispatch under __start.")
	assert.Equal(t, dispatchExeID, executeParents[childBExeID],
		"child B's StepExecuteCompleted MUST carry the dispatch step's exe_id as from_step_exe_id")

	// --- StepWaitForCompleted assertions ---
	//
	// The WaitFor history event must ALSO carry from_step_exe_id, because
	// the WebUI's buildGraph uses it to draw an incoming edge for any
	// child that has only reached the WaitFor stage. In this test both
	// children eventually completed Execute, but the WaitFor event was
	// committed first and must carry the parent at that earlier moment.

	require.Contains(t, waitForParents, childAExeID,
		"history must include child A's StepWaitForCompleted; got events for: %v", waitForStepIDs)
	require.Contains(t, waitForParents, childBExeID,
		"history must include child B's StepWaitForCompleted; got events for: %v", waitForStepIDs)

	assert.Equal(t, dispatchExeID, waitForParents[childAExeID],
		"child A's StepWaitForCompleted MUST carry the dispatch step's exe_id as from_step_exe_id "+
			"(server reads from persisted ActiveStepExecution.FromStepExeID before writing the history payload). "+
			"If this fails the WebUI graph will render child A as a floating Waiting node when it never reaches Execute.")
	assert.Equal(t, dispatchExeID, waitForParents[childBExeID],
		"child B's StepWaitForCompleted MUST carry the dispatch step's exe_id as from_step_exe_id")
}

// TestSDKE2E_StepProvenance_StillWaitingChildHasParentInWaitForHistory is
// the exact reproduction of the WebUI screenshot bug: a fan-out child that
// NEVER reaches Execute (its WaitFor channel is never published) must still
// be renderable in the graph with an incoming edge from its parent. The
// only history event we have for such a child is StepWaitForCompleted, so
// that event MUST carry from_step_exe_id.
//
// We don't satisfy the wait — we just trigger, settle into AllStepsWaiting,
// snapshot history, assert, and StopRun.
func TestSDKE2E_StepProvenance_StillWaitingChildHasParentInWaitForHistory(t *testing.T) {
	registry := dex.NewRegistry()
	registry.Register(&ProvFanOutFlow{})

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

	taskListName := "sdk-e2e-prov-waiting-" + uuid.NewString()
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
	require.NoError(t, client.StartRunWithOptions(ctx, runID, &ProvFanOutFlow{}, &dex.RunOptions{TaskListName: taskListName}))

	// Wait for AllStepsWaiting — both children have armed their WaitFor
	// and committed StepWaitForCompleted history events. We do NOT
	// publish anything; the run will sit here until we StopRun.
	require.Eventually(t, func() bool {
		got, gErr := client.GetRun(ctx, runID)
		require.NoError(t, gErr)
		require.NotNil(t, got)
		return got.Status == dex.RunStatusAllStepsWaitingForConditions
	}, 5*time.Second, 50*time.Millisecond,
		"run must reach AllStepsWaiting so both children commit a StepWaitForCompleted history event")

	// History is written via the OpsFIFO outbox + per-shard reader, so
	// there's a small drain window between the WaitFor RPC committing
	// and the events being readable via OpsService.GetHistoryEvents.
	// Poll until we see both children's StepWaitForCompleted before
	// asserting on the field — without this the test races the drain
	// reader and flakes on a fast machine.
	const dispatchExeID = "sdke2e.provFanOutDispatchStep-1"
	const childAExeID = "sdke2e.provFanOutChildA-1"
	const childBExeID = "sdke2e.provFanOutChildB-1"

	waitForParents := map[string]string{}
	require.Eventually(t, func() bool {
		hist, hErr := opsPb.GetHistoryEvents(ctx, &pb.GetHistoryEventsRequest{
			Namespace: "default", RunId: runID, AfterId: 0, Limit: 200,
		})
		if hErr != nil {
			return false
		}
		waitForParents = map[string]string{}
		for _, ev := range hist.Events {
			wait := ev.GetStepWaitForCompleted()
			if wait == nil {
				continue
			}
			waitForParents[wait.StepExeId] = wait.FromStepExeId
		}
		_, hasA := waitForParents[childAExeID]
		_, hasB := waitForParents[childBExeID]
		return hasA && hasB
	}, 10*time.Second, 100*time.Millisecond,
		"both children's StepWaitForCompleted must surface in history within the OpsFIFO drain window")

	// THE REGRESSION ASSERTION for the screenshot bug. With the pre-fix
	// HistoryStepWaitForCompletedPayload (which lacked from_step_exe_id),
	// both of these would be "". The WebUI's buildGraph would then have
	// no signal for the parent edge of either child — so they'd render
	// as floating "Waiting" nodes detached from dispatch (exactly the
	// screenshot the user reported).
	assert.Equal(t, dispatchExeID, waitForParents[childAExeID],
		"child A's StepWaitForCompleted MUST carry from_step_exe_id even when the child never reaches Execute. "+
			"This is the exact bug the user reported with floating Waiting nodes in the WebUI graph.")
	assert.Equal(t, dispatchExeID, waitForParents[childBExeID],
		"child B's StepWaitForCompleted MUST carry from_step_exe_id even when the child never reaches Execute.")

	// Tear down the still-Waiting run cleanly so the test doesn't leave
	// a zombie hanging around for the durable timer to wake up later.
	require.NoError(t, client.StopRun(ctx, runID, dex.StopRunComplete, ""))
}
