package sdke2e

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These e2e tests guard the channel-reservation / condition paths that had no
// end-to-end coverage: competing consumers (only as many promoted as messages),
// greedy multi-branch AnyOf, multi-channel AllOf, and blob-backed channel
// values round-tripping through the worker.

func startWorkerBg(t *testing.T, worker *dex.Worker) {
	t.Helper()
	workerDone := make(chan error, 1)
	go func() { workerDone <- worker.Start() }()
	t.Cleanup(func() { worker.Stop(); <-workerDone })
	waitWorkerReady(t, worker)
}

// waitWorkerReady blocks until the worker's poller is registered so the next
// StartRun sync-matches instead of taking the slow async-backlog path.
func waitWorkerReady(t *testing.T, worker *dex.Worker) {
	t.Helper()
	select {
	case <-worker.Ready():
	case <-time.After(10 * time.Second):
		t.Fatal("worker did not become ready within 10s")
	}
}

// ============================================================================
// 1. Competing consumers: one message => exactly one winner (no over-promotion)
// ============================================================================

var (
	competeExecCount atomic.Int32
	competeCh        = dex.NewChannel[map[string]any]("compete-ch")
)

type competeDispatch struct{ dex.StepDefaults[any] }

func (s *competeDispatch) Execute(_ dex.Context, _ any) (dex.StepDecision, error) {
	return dex.GoToMany(
		dex.MovementOf[any](&competeWaiter{}, nil),
		dex.MovementOf[any](&competeWaiter{}, nil),
		dex.MovementOf[any](&competeWaiter{}, nil),
	), nil
}

type competeWaiter struct {
	dex.DefaultStepId
	dex.DefaultStepOptions
}

func (s *competeWaiter) WaitFor(_ dex.Context, _ any) (dex.WaitForCondition, error) {
	// Generous timer so no branch fires on its own during the test.
	return dex.AnyOf(competeCh.Condition(), dex.Timer(2*time.Minute)), nil
}

func (s *competeWaiter) Execute(_ dex.Context, _ any) (dex.StepDecision, error) {
	competeExecCount.Add(1)
	return dex.DeadEnd(), nil
}

type competeFlow struct{ dex.FlowDefaults }

func (f *competeFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep[any](&competeDispatch{}),
		dex.NonStartingStep[any](&competeWaiter{}),
	}
}

func (f *competeFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{Channels: []dex.ChannelDef{dex.DefineChannel(competeCh)}}
}

func TestSDKE2E_CompetingConsumers_OneMessageOneWinner(t *testing.T) {
	competeExecCount.Store(0)
	registry := dex.NewRegistry()
	registry.Register(&competeFlow{})
	client, worker, taskListName := startSDKE2E(t, registry)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	startWorkerBg(t, worker)

	runID := uuid.NewString()
	require.NoError(t, client.StartRunWithOptions(ctx, runID, &competeFlow{}, &dex.RunOptions{TaskListName: taskListName}))
	requireRunStatusReached(t, client, runID, dex.RunStatusAllStepsWaitingForConditions, 30*time.Second)

	// One message: exactly ONE of the three waiters may consume it. Before the
	// promotion fix all three promoted (two with phantom/empty reservations).
	require.NoError(t, client.PublishToChannel(ctx, runID, competeCh.Name, map[string]any{"n": 1}))
	require.Eventually(t, func() bool { return competeExecCount.Load() == 1 },
		30*time.Second, 50*time.Millisecond, "exactly one waiter must consume the single message")

	// The run must re-park with the other two still waiting (NOT Completed) —
	// proof that the other two were not over-promoted.
	requireRunStatusReached(t, client, runID, dex.RunStatusAllStepsWaitingForConditions, 30*time.Second)
	require.Equal(t, int32(1), competeExecCount.Load(), "over-promotion would run more than one waiter for one message")

	// Two more messages drain the remaining waiters.
	require.NoError(t, client.PublishToChannel(ctx, runID, competeCh.Name, map[string]any{"n": 2}))
	require.NoError(t, client.PublishToChannel(ctx, runID, competeCh.Name, map[string]any{"n": 3}))
	requireRunStatusReached(t, client, runID, dex.RunStatusCompleted, 45*time.Second)
	assert.Equal(t, int32(3), competeExecCount.Load())
}

// ============================================================================
// 2. Greedy multi-branch AnyOf: both branches ready => consume from BOTH
// ============================================================================

var (
	greedyChA   = dex.NewChannel[map[string]any]("greedy-a")
	greedyChB   = dex.NewChannel[map[string]any]("greedy-b")
	greedyOKKey = dex.NewStateKey[bool]("greedy_ok")
)

type greedyDispatch struct{ dex.StepDefaults[any] }

func (s *greedyDispatch) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	if err := greedyChA.Publish(ctx, map[string]any{"v": "a"}); err != nil {
		return nil, err
	}
	if err := greedyChB.Publish(ctx, map[string]any{"v": "b"}); err != nil {
		return nil, err
	}
	return dex.GoTo[any](&greedyWaiter{}, nil), nil
}

type greedyWaiter struct {
	dex.DefaultStepId
	dex.DefaultStepOptions
}

func (s *greedyWaiter) WaitFor(_ dex.Context, _ any) (dex.WaitForCondition, error) {
	return dex.AnyOf(greedyChA.Condition(), greedyChB.Condition()), nil
}

func (s *greedyWaiter) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	a, err := greedyChA.GetConsumedMessages(ctx)
	if err != nil {
		return nil, err
	}
	b, err := greedyChB.GetConsumedMessages(ctx)
	if err != nil {
		return nil, err
	}
	// Greedy AnyOf: BOTH satisfied branches consume their message.
	if err := greedyOKKey.SetValue(ctx, len(a) == 1 && len(b) == 1); err != nil {
		return nil, err
	}
	return dex.Complete(nil), nil
}

type greedyFlow struct{ dex.FlowDefaults }

func (f *greedyFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep[any](&greedyDispatch{}),
		dex.NonStartingStep[any](&greedyWaiter{}),
	}
}

func (f *greedyFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		StateKeys: []dex.StateKeyDef{dex.DefineStateKey(greedyOKKey)},
		Channels:  []dex.ChannelDef{dex.DefineChannel(greedyChA), dex.DefineChannel(greedyChB)},
	}
}

func TestSDKE2E_GreedyAnyOf_BothBranchesConsumed(t *testing.T) {
	registry := dex.NewRegistry()
	registry.Register(&greedyFlow{})
	client, worker, taskListName := startSDKE2E(t, registry)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	startWorkerBg(t, worker)

	runID := uuid.NewString()
	require.NoError(t, client.StartRunWithOptions(ctx, runID, &greedyFlow{}, &dex.RunOptions{TaskListName: taskListName}))
	requireRunStatusReached(t, client, runID, dex.RunStatusCompleted, 45*time.Second)

	ok, err := greedyOKKey.GetRunValue(client, ctx, runID)
	require.NoError(t, err)
	assert.True(t, ok, "greedy AnyOf must consume from BOTH ready channels, not just the first")
}

// ============================================================================
// 3. Multi-channel AllOf: both channels satisfied => consume both
// ============================================================================

var (
	allOfChC   = dex.NewChannel[map[string]any]("allof-c")
	allOfChD   = dex.NewChannel[map[string]any]("allof-d")
	allOfOKKey = dex.NewStateKey[bool]("allof_ok")
)

type allOfDispatch struct{ dex.StepDefaults[any] }

func (s *allOfDispatch) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	if err := allOfChC.Publish(ctx, map[string]any{"v": "c"}); err != nil {
		return nil, err
	}
	if err := allOfChD.Publish(ctx, map[string]any{"v": "d"}); err != nil {
		return nil, err
	}
	return dex.GoTo[any](&allOfWaiter{}, nil), nil
}

type allOfWaiter struct {
	dex.DefaultStepId
	dex.DefaultStepOptions
}

func (s *allOfWaiter) WaitFor(_ dex.Context, _ any) (dex.WaitForCondition, error) {
	return dex.AllOf(allOfChC.Condition(), allOfChD.Condition()), nil
}

func (s *allOfWaiter) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	c, err := allOfChC.GetConsumedMessages(ctx)
	if err != nil {
		return nil, err
	}
	d, err := allOfChD.GetConsumedMessages(ctx)
	if err != nil {
		return nil, err
	}
	cOK := len(c) == 1 && c[0]["v"] == "c"
	dOK := len(d) == 1 && d[0]["v"] == "d"
	if err := allOfOKKey.SetValue(ctx, cOK && dOK); err != nil {
		return nil, err
	}
	return dex.Complete(nil), nil
}

type allOfFlow struct{ dex.FlowDefaults }

func (f *allOfFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep[any](&allOfDispatch{}),
		dex.NonStartingStep[any](&allOfWaiter{}),
	}
}

func (f *allOfFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		StateKeys: []dex.StateKeyDef{dex.DefineStateKey(allOfOKKey)},
		Channels:  []dex.ChannelDef{dex.DefineChannel(allOfChC), dex.DefineChannel(allOfChD)},
	}
}

func TestSDKE2E_MultiChannelAllOf_BothChannelsConsumed(t *testing.T) {
	registry := dex.NewRegistry()
	registry.Register(&allOfFlow{})
	client, worker, taskListName := startSDKE2E(t, registry)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	startWorkerBg(t, worker)

	runID := uuid.NewString()
	require.NoError(t, client.StartRunWithOptions(ctx, runID, &allOfFlow{}, &dex.RunOptions{TaskListName: taskListName}))
	requireRunStatusReached(t, client, runID, dex.RunStatusCompleted, 45*time.Second)

	ok, err := allOfOKKey.GetRunValue(client, ctx, runID)
	require.NoError(t, err)
	assert.True(t, ok, "AllOf across two channels must consume one message from each with correct values")
}

// ============================================================================
// 4. Blob-backed channel value round-trips through the worker
// ============================================================================

type blobPayload struct {
	Data string `json:"data"`
}

var (
	blobCh     = dex.NewChannel[blobPayload]("blob-ch")
	blobOKKey  = dex.NewStateKey[bool]("blob_ok")
	blobBigStr = strings.Repeat("dex-blob-roundtrip-", 512) // ~9.5KB → stored via BlobStore
)

type blobDispatch struct{ dex.StepDefaults[any] }

func (s *blobDispatch) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	if err := blobCh.Publish(ctx, blobPayload{Data: blobBigStr}); err != nil {
		return nil, err
	}
	return dex.GoTo[any](&blobWaiter{}, nil), nil
}

type blobWaiter struct {
	dex.DefaultStepId
	dex.DefaultStepOptions
}

func (s *blobWaiter) WaitFor(_ dex.Context, _ any) (dex.WaitForCondition, error) {
	return dex.AnyOf(blobCh.Condition()), nil
}

func (s *blobWaiter) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	msgs, err := blobCh.GetConsumedMessages(ctx)
	if err != nil {
		return nil, err
	}
	match := len(msgs) == 1 && msgs[0].Data == blobBigStr
	if err := blobOKKey.SetValue(ctx, match); err != nil {
		return nil, err
	}
	return dex.Complete(nil), nil
}

type blobFlow struct{ dex.FlowDefaults }

func (f *blobFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep[any](&blobDispatch{}),
		dex.NonStartingStep[any](&blobWaiter{}),
	}
}

func (f *blobFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		StateKeys: []dex.StateKeyDef{dex.DefineStateKey(blobOKKey)},
		Channels:  []dex.ChannelDef{dex.DefineChannel(blobCh)},
	}
}

func TestSDKE2E_BlobBackedChannelValue_RoundTripsToWorker(t *testing.T) {
	registry := dex.NewRegistry()
	registry.Register(&blobFlow{})
	client, worker, taskListName := startSDKE2E(t, registry)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	startWorkerBg(t, worker)

	runID := uuid.NewString()
	require.NoError(t, client.StartRunWithOptions(ctx, runID, &blobFlow{}, &dex.RunOptions{TaskListName: taskListName}))
	requireRunStatusReached(t, client, runID, dex.RunStatusCompleted, 45*time.Second)

	ok, err := blobOKKey.GetRunValue(client, ctx, runID)
	require.NoError(t, err)
	assert.True(t, ok, "blob-backed channel payload must round-trip byte-identically to the worker")
}
