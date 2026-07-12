package sdke2e

import (
	"context"
	"testing"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Dynamic state key e2e tests
//
// These tests exercise the SDK's DynamicStateKey[T] family. Like dynamic
// channels, the wire/server path is name-agnostic (state keys are opaque
// strings in the run state map). The tests prove:
//
//  1. Sequential write/read/update across steps round-trips through the
//     server and is readable via GetRunValue after completion.
//  2. Parallel fan-out writes distinct instance keys in the same family
//     without cross-talk; merge step reads both instances.
//  3. Dynamic state written in WaitFor survives AllStepsWaiting resume and
//     is updateable in Execute on the same instance key.
//  4. GetRunValue returns partial dynamic state while the run is still
//     in-flight (before Execute resumes after a durable timer).
// ============================================================================

type dynOrderRecord struct {
	Item   string `json:"item"`
	Status string `json:"status"`
}

var (
	dynStateOrders = dex.NewDynamicStateKey[dynOrderRecord]("dyn-orders/")
	dynStateDone   = dex.NewStateKey[bool]("dyn_state_done")
)

// ----------------------------------------------------------------------------
// Test 1: sequential read/write/update
// ----------------------------------------------------------------------------

type dynStateSeqWriteStep struct {
	dex.StepDefaults[any]
}

func (s *dynStateSeqWriteStep) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	if err := dynStateOrders.SetValue(ctx, "ord-1", dynOrderRecord{
		Item:   "widget",
		Status: "pending",
	}); err != nil {
		return nil, err
	}
	return dex.GoTo(&dynStateSeqUpdateStep{}, nil), nil
}

type dynStateSeqUpdateStep struct {
	dex.StepDefaults[any]
}

func (s *dynStateSeqUpdateStep) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	existing, err := dynStateOrders.GetValue(ctx, "ord-1")
	if err != nil {
		return nil, err
	}
	if err := dynStateOrders.SetValue(ctx, "ord-1", dynOrderRecord{
		Item:   existing.Item,
		Status: "shipped",
	}); err != nil {
		return nil, err
	}
	if err := dynStateDone.SetValue(ctx, true); err != nil {
		return nil, err
	}
	return dex.Complete(nil), nil
}

type DynStateSeqFlow struct {
	dex.FlowDefaults
}

func (f *DynStateSeqFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep[any](&dynStateSeqWriteStep{}),
		dex.NonStartingStep[any](&dynStateSeqUpdateStep{}),
	}
}

func (f *DynStateSeqFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		StateKeys: []dex.StateKeyDef{dex.DefineStateKey(dynStateDone)},
		DynamicStateKeys: []dex.StateKeyDef{
			dex.DefineDynamicStateKey(dynStateOrders),
		},
	}
}

func TestSDKE2E_DynamicStateKey_SequentialReadWriteUpdate(t *testing.T) {
	registry := dex.NewRegistry()
	registry.Register(&DynStateSeqFlow{})

	client, worker, taskListName := startSDKE2E(t, registry)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	startWorkerBg(t, worker)

	runID := uuid.NewString()
	require.NoError(t, client.StartRunWithOptions(ctx, runID, &DynStateSeqFlow{}, &dex.RunOptions{TaskListName: taskListName}))

	status, err := client.WaitForRunComplete(ctx, runID)
	require.NoError(t, err)
	assert.Equal(t, dex.RunStatusCompleted, status)

	order, err := dynStateOrders.GetRunValue(client, ctx, runID, "ord-1")
	require.NoError(t, err)
	assert.Equal(t, "widget", order.Item)
	assert.Equal(t, "shipped", order.Status)

	done, err := dynStateDone.GetRunValue(client, ctx, runID)
	require.NoError(t, err)
	assert.True(t, done)

	result, err := client.GetRun(ctx, runID)
	require.NoError(t, err)
	_, ok := result.State["dyn-orders/ord-1"]
	assert.True(t, ok, "raw GetRun state must contain resolved wire name prefix+instanceKey")
}

// ----------------------------------------------------------------------------
// Test 2: parallel per-instance isolation
// ----------------------------------------------------------------------------

type dynStateBranchInput struct {
	OrderID string `json:"order_id"`
}

type dynStateParDispatchStep struct {
	dex.StepDefaults[any]
}

func (s *dynStateParDispatchStep) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	return dex.GoToMany(
		dex.MovementOf(&dynStateParWriteStep{}, dynStateBranchInput{OrderID: "order-a"}),
		dex.MovementOf(&dynStateParWriteStep{}, dynStateBranchInput{OrderID: "order-b"}),
	), nil
}

type dynStateParWriteStep struct {
	dex.StepDefaults[dynStateBranchInput]
}

func (s *dynStateParWriteStep) Execute(ctx dex.Context, input dynStateBranchInput) (dex.StepDecision, error) {
	if err := dynStateOrders.SetValue(ctx, input.OrderID, dynOrderRecord{
		Item:   input.OrderID,
		Status: "created",
	}); err != nil {
		return nil, err
	}
	return dex.GoTo(&dynStateParMergeStep{}, nil), nil
}

type dynStateParMergeStep struct {
	dex.StepDefaults[any]
}

func (s *dynStateParMergeStep) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	orderA, err := dynStateOrders.GetValue(ctx, "order-a")
	if err != nil {
		return nil, err
	}
	orderB, err := dynStateOrders.GetValue(ctx, "order-b")
	if err != nil {
		return nil, err
	}
	if orderA.Item != "order-a" || orderB.Item != "order-b" {
		return dex.Fail("dynamic state instances crossed or missing"), nil
	}
	if err := dynStateDone.SetValue(ctx, true); err != nil {
		return nil, err
	}
	return dex.Complete(nil), nil
}

type DynStateParFlow struct {
	dex.FlowDefaults
}

func (f *DynStateParFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep[any](&dynStateParDispatchStep{}),
		dex.NonStartingStep[dynStateBranchInput](&dynStateParWriteStep{}),
		dex.NonStartingStep[any](&dynStateParMergeStep{}),
	}
}

func (f *DynStateParFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		StateKeys: []dex.StateKeyDef{dex.DefineStateKey(dynStateDone)},
		DynamicStateKeys: []dex.StateKeyDef{
			dex.DefineDynamicStateKey(dynStateOrders),
		},
	}
}

func TestSDKE2E_DynamicStateKey_ParallelPerInstanceIsolation(t *testing.T) {
	registry := dex.NewRegistry()
	registry.Register(&DynStateParFlow{})

	client, worker, taskListName := startSDKE2E(t, registry)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	startWorkerBg(t, worker)

	runID := uuid.NewString()
	require.NoError(t, client.StartRunWithOptions(ctx, runID, &DynStateParFlow{}, &dex.RunOptions{TaskListName: taskListName}))

	status, err := client.WaitForRunComplete(ctx, runID)
	require.NoError(t, err)
	assert.Equal(t, dex.RunStatusCompleted, status)

	orderA, err := dynStateOrders.GetRunValue(client, ctx, runID, "order-a")
	require.NoError(t, err)
	orderB, err := dynStateOrders.GetRunValue(client, ctx, runID, "order-b")
	require.NoError(t, err)
	assert.Equal(t, "order-a", orderA.Item)
	assert.Equal(t, "created", orderA.Status)
	assert.Equal(t, "order-b", orderB.Item)
	assert.Equal(t, "created", orderB.Status)

	other, err := dynStateOrders.GetRunValue(client, ctx, runID, "order-c")
	require.NoError(t, err)
	assert.Equal(t, dynOrderRecord{}, other, "missing instance must decode to zero value")
}

// ----------------------------------------------------------------------------
// Test 3: WaitFor write survives resume; mid-run GetRunValue
// ----------------------------------------------------------------------------

type dynStateWaitStep struct {
	dex.DefaultStepId
	dex.DefaultStepOptions
}

func (s *dynStateWaitStep) WaitFor(ctx dex.Context, _ any) (dex.WaitForCondition, error) {
	if err := dynStateOrders.SetValue(ctx, "parked", dynOrderRecord{
		Item:   "parked-item",
		Status: "waiting",
	}); err != nil {
		return nil, err
	}
	return dex.AnyOf(dex.Timer(2 * time.Second)), nil
}

func (s *dynStateWaitStep) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	existing, err := dynStateOrders.GetValue(ctx, "parked")
	if err != nil {
		return nil, err
	}
	if existing.Item != "parked-item" || existing.Status != "waiting" {
		return dex.Fail("dynamic state lost across AllStepsWaiting resume"), nil
	}
	if err := dynStateOrders.SetValue(ctx, "parked", dynOrderRecord{
		Item:   existing.Item,
		Status: "resumed",
	}); err != nil {
		return nil, err
	}
	if err := dynStateDone.SetValue(ctx, true); err != nil {
		return nil, err
	}
	return dex.Complete(nil), nil
}

type DynStateWaitFlow struct {
	dex.FlowDefaults
}

func (f *DynStateWaitFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep[any](&dynStateWaitStep{}),
	}
}

func (f *DynStateWaitFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		StateKeys: []dex.StateKeyDef{dex.DefineStateKey(dynStateDone)},
		DynamicStateKeys: []dex.StateKeyDef{
			dex.DefineDynamicStateKey(dynStateOrders),
		},
	}
}

func TestSDKE2E_DynamicStateKey_SurvivesWaitForResumeAndReadableMidRun(t *testing.T) {
	registry := dex.NewRegistry()
	registry.Register(&DynStateWaitFlow{})

	client, worker, taskListName := startSDKE2E(t, registry)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	startWorkerBg(t, worker)

	runID := uuid.NewString()
	require.NoError(t, client.StartRunWithOptions(ctx, runID, &DynStateWaitFlow{}, &dex.RunOptions{TaskListName: taskListName}))

	requireRunStatusReached(t, client, runID, dex.RunStatusAllStepsWaitingForConditions, 10*time.Second)

	midRun, err := dynStateOrders.GetRunValue(client, ctx, runID, "parked")
	require.NoError(t, err)
	assert.Equal(t, "parked-item", midRun.Item)
	assert.Equal(t, "waiting", midRun.Status)

	status, err := client.WaitForRunComplete(ctx, runID)
	require.NoError(t, err)
	assert.Equal(t, dex.RunStatusCompleted, status)

	final, err := dynStateOrders.GetRunValue(client, ctx, runID, "parked")
	require.NoError(t, err)
	assert.Equal(t, "parked-item", final.Item)
	assert.Equal(t, "resumed", final.Status)
}
