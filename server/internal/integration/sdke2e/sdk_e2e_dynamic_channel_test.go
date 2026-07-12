package sdke2e

import (
	"context"
	"testing"
	"time"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/server/cmd"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// ============================================================================
// Dynamic channel e2e tests
//
// These tests exercise the SDK's DynamicChannel[T] family — the wire/server
// path is name-agnostic (channel names are opaque strings), so "dynamic"
// reduces to a client-side naming convention. The tests prove:
//
//   1. External publish to a specific dynamic instance only unblocks the
//      step waiting on THAT instance (per-key isolation across the family).
//   2. A step's persistence.PublishToChannel into a dynamic instance round-trips
//      through the server and unblocks an unrelated sibling step waiting
//      on the same instance (true server-mediated pub/sub, not in-process).
//   3. An internal publish to a dynamic instance with no waiter lands in
//      RunRow.UnconsumedChannelMessages keyed by the resolved full name.
//   4. The condition_results emitted in StepExecuteCompleted history events
//      carry the resolved full name (prefix+key), not just the key.
//
// All four cover paths the benchmark dynamicChannel flow exercises end-to-end
// in dev-stack, but in a small isolated SDK e2e harness so failures point
// directly at the dynamic-channel layer rather than at the benchmark wiring.
// ============================================================================

// Two dynamic channel families used across the tests below. The string
// prefixes already include any delimiter (the SDK does NOT insert one
// between prefix and key).
var (
	dynUpdates = dex.NewDynamicChannel[map[string]any]("dyn-updates-")
	dynAcks    = dex.NewDynamicChannel[map[string]any]("dyn-acks-")
	dynOrphan  = dex.NewDynamicChannel[map[string]any]("dyn-orphan-")

	dynKeyK1Done       = dex.NewStateKey[bool]("k1_done")
	dynKeyK2Done       = dex.NewStateKey[bool]("k2_done")
	dynKeyProducerDone = dex.NewStateKey[bool]("producer_done")
	dynKeyConsumerDone = dex.NewStateKey[bool]("consumer_done")
	dynKeyAckPayload   = dex.NewStateKey[string]("ack_payload")
	dynKeyDone         = dex.NewStateKey[bool]("done")
	dynKeyTriggered    = dex.NewStateKey[bool]("triggered")
	dynKeyOrd1Done     = dex.NewStateKey[bool]("ord_1_done")
	dynKeyOrd2Done     = dex.NewStateKey[bool]("ord_2_done")
)

// ----------------------------------------------------------------------------
// Test 1: per-key isolation
//
// dispatch fan-outs to two `dynKeyWaitStep` instances, one per key (k1, k2).
// Each waits on dynUpdates.Of(input.Key). External publish targets ONLY k1.
// Assert: k1 branch's Execute records K1Done=true; k2 branch is still
// in WAITING_FOR_CONDITION. We then StopRun to cleanly tear down k2.
// ----------------------------------------------------------------------------

type DynKeyState struct {
	K1Done bool `json:"k1_done"`
	K2Done bool `json:"k2_done"`
}

type DynKeyInput struct {
	Key string `json:"key"`
}

type dynKeyDispatchStep struct {
	dex.StepDefaults[any]
}

func (s *dynKeyDispatchStep) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	return dex.GoToMany(
		dex.MovementOf(&dynKeyWaitStep{}, DynKeyInput{Key: "k1"}),
		dex.MovementOf(&dynKeyWaitStep{}, DynKeyInput{Key: "k2"}),
	), nil
}

type dynKeyWaitStep struct {
	dex.DefaultStepId
	dex.DefaultStepOptions
}

func (s *dynKeyWaitStep) WaitFor(_ dex.Context, input DynKeyInput) (dex.WaitForCondition, error) {
	return dex.AnyOf(
		dynUpdates.Condition(input.Key),
		// Generous timer so neither branch fires on its own during the test.
		dex.Timer(2*time.Minute),
	), nil
}

func (s *dynKeyWaitStep) Execute(ctx dex.Context, input DynKeyInput) (dex.StepDecision, error) {
	switch input.Key {
	case "k1":
		if err := dynKeyK1Done.SetValue(ctx, true); err != nil {
			return nil, err
		}
	case "k2":
		if err := dynKeyK2Done.SetValue(ctx, true); err != nil {
			return nil, err
		}
	}
	return dex.DeadEnd(), nil
}

type DynKeyFlow struct {
	dex.FlowDefaults
}

func (f *DynKeyFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep[any](&dynKeyDispatchStep{}),
		dex.NonStartingStep[DynKeyInput](&dynKeyWaitStep{}),
	}
}

func (f *DynKeyFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		StateKeys: []dex.StateKeyDef{
			dex.DefineStateKey(dynKeyK1Done),
			dex.DefineStateKey(dynKeyK2Done),
		},
		DynamicChannels: []dex.ChannelDef{dex.DefineDynamicChannel(dynUpdates)},
	}
}

func TestSDKE2E_DynamicChannel_ExternalPublishOnlyUnblocksMatchingKey(t *testing.T) {
	registry := dex.NewRegistry()
	registry.Register(&DynKeyFlow{})

	client, worker, taskListName := startSDKE2E(t, registry)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	startWorkerBg(t, worker)

	runID := uuid.NewString()
	require.NoError(t, client.StartRunWithOptions(ctx, runID, &DynKeyFlow{}, &dex.RunOptions{TaskListName: taskListName}))

	// Wait until both wait steps have registered (run reaches AllStepsWaiting),
	// so we know the publish lands on a fully-armed flow rather than
	// racing the dispatch fan-out.
	requireRunStatusReached(t, client, runID, dex.RunStatusAllStepsWaitingForConditions, 30*time.Second)

	// Publish ONLY to k1's dynamic instance. The full wire name is
	// "dyn-updates-k1" — same name the consumer side built via
	// dynUpdates.Condition("k1").
	require.NoError(t, client.PublishToDynamicChannel(ctx, runID, dynUpdates.Prefix, "k1",
		map[string]any{"value": "delivered-to-k1"}))

	// Poll until k1's Execute committed K1Done=true. Then verify K2Done
	// remains false: the k2 branch must still be in WAITING_FOR_CONDITION.
	require.Eventually(t, func() bool {
		k1Done, err := dynKeyK1Done.GetRunValue(client, ctx, runID)
		return err == nil && k1Done
	}, 10*time.Second, 100*time.Millisecond,
		"k1 branch should complete after external publish to dyn-updates-k1")

	k1Done, err := dynKeyK1Done.GetRunValue(client, ctx, runID)
	require.NoError(t, err)
	k2Done, err := dynKeyK2Done.GetRunValue(client, ctx, runID)
	require.NoError(t, err)
	assert.True(t, k1Done, "K1Done must be true (publish targeted k1)")
	assert.False(t, k2Done,
		"K2Done must remain false (no publish to dyn-updates-k2; cross-talk would be a regression)")

	// Tear down the k2 branch cleanly so the test doesn't leave a
	// zombie WAITING_FOR_CONDITION run hanging.
	require.NoError(t, client.StopRun(ctx, runID, dex.StopRunComplete, ""))
}

// ----------------------------------------------------------------------------
// Test 2: internal publish unblocks sibling step
//
// dispatch fan-outs to producerStep + consumerStep. Producer waits on
// dynUpdates.Of("k1"); consumer waits on dynAcks.Of("k1"). External publish
// to dynUpdates.Of("k1") unblocks producer, whose Execute publishes
// dynAcks.Of("k1") via persistence.PublishToChannel — that publish round-trips
// through the server and unblocks the consumer. Both branches DeadEnd; run
// reaches Completed when both branches retire.
// ----------------------------------------------------------------------------

type DynSiblingState struct {
	ProducerDone bool   `json:"producer_done"`
	ConsumerDone bool   `json:"consumer_done"`
	AckPayload   string `json:"ack_payload"`
}

type dynSiblingDispatchStep struct {
	dex.StepDefaults[any]
}

func (s *dynSiblingDispatchStep) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	return dex.GoToMany(
		dex.MovementOf(&dynProducerStep{}, nil),
		dex.MovementOf(&dynConsumerStep{}, nil),
	), nil
}

type dynProducerStep struct {
	dex.DefaultStepId
	dex.DefaultStepOptions
}

func (s *dynProducerStep) WaitFor(_ dex.Context, _ any) (dex.WaitForCondition, error) {
	return dex.AnyOf(dynUpdates.Condition("k1")), nil
}

func (s *dynProducerStep) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	if err := dynKeyProducerDone.SetValue(ctx, true); err != nil {
		return nil, err
	}
	if err := dynAcks.Publish(ctx, "k1", map[string]any{"ack": "from-producer"}); err != nil {
		return nil, err
	}
	return dex.DeadEnd(), nil
}

type dynConsumerStep struct {
	dex.DefaultStepId
	dex.DefaultStepOptions
}

func (s *dynConsumerStep) WaitFor(_ dex.Context, _ any) (dex.WaitForCondition, error) {
	return dex.AnyOf(dynAcks.Condition("k1")), nil
}

func (s *dynConsumerStep) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	msgs, err := dynAcks.GetConsumedMessages(ctx, "k1")
	if err != nil {
		return nil, err
	}
	payload := ""
	if len(msgs) > 0 {
		if v, ok := msgs[0]["ack"].(string); ok {
			payload = v
		}
	}
	if err := dynKeyConsumerDone.SetValue(ctx, true); err != nil {
		return nil, err
	}
	if err := dynKeyAckPayload.SetValue(ctx, payload); err != nil {
		return nil, err
	}
	return dex.DeadEnd(), nil
}

type DynSiblingFlow struct {
	dex.FlowDefaults
}

func (f *DynSiblingFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep[any](&dynSiblingDispatchStep{}),
		dex.NonStartingStep[any](&dynProducerStep{}),
		dex.NonStartingStep[any](&dynConsumerStep{}),
	}
}

func (f *DynSiblingFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		StateKeys: []dex.StateKeyDef{
			dex.DefineStateKey(dynKeyProducerDone),
			dex.DefineStateKey(dynKeyConsumerDone),
			dex.DefineStateKey(dynKeyAckPayload),
		},
		DynamicChannels: []dex.ChannelDef{
			dex.DefineDynamicChannel(dynUpdates),
			dex.DefineDynamicChannel(dynAcks),
		},
	}
}

func TestSDKE2E_DynamicChannel_InternalPublishUnblocksSiblingStep(t *testing.T) {
	registry := dex.NewRegistry()
	registry.Register(&DynSiblingFlow{})

	client, worker, taskListName := startSDKE2E(t, registry)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	startWorkerBg(t, worker)

	runID := uuid.NewString()
	require.NoError(t, client.StartRunWithOptions(ctx, runID, &DynSiblingFlow{}, &dex.RunOptions{TaskListName: taskListName}))

	// Wait until both branches have armed their WaitFors.
	requireRunStatusReached(t, client, runID, dex.RunStatusAllStepsWaitingForConditions, 30*time.Second)

	// External publish to producer's input channel — the only thing that
	// kicks off the chain. The consumer must NOT have been already
	// satisfied by anything else (it's waiting on dynAcks.Of("k1") which
	// nothing has touched yet).
	require.NoError(t, client.PublishToDynamicChannel(ctx, runID, dynUpdates.Prefix, "k1",
		map[string]any{"trigger": "external"}))

	// Wait for run to terminate. Both branches DeadEnd → run completes.
	status, err := client.WaitForRunComplete(ctx, runID)
	require.NoError(t, err)
	producerDone, err := dynKeyProducerDone.GetRunValue(client, ctx, runID)
	require.NoError(t, err)
	consumerDone, err := dynKeyConsumerDone.GetRunValue(client, ctx, runID)
	require.NoError(t, err)
	ackPayload, err := dynKeyAckPayload.GetRunValue(client, ctx, runID)
	require.NoError(t, err)
	assert.Equal(t, dex.RunStatusCompleted, status)

	// The smoking gun for "internal publish unblocked the sibling": the
	// consumer's Execute must have run AND seen the ack payload that
	// only the producer's Execute could have written.
	assert.True(t, producerDone, "producer must complete")
	assert.True(t, consumerDone, "consumer must complete (proves internal publish reached the sibling)")
	assert.Equal(t, "from-producer", ackPayload,
		"consumer must receive the exact payload published by producer's PublishToChannel")
}

// ----------------------------------------------------------------------------
// Test 3: internal publish without a waiter lands in unconsumed
//
// A single Complete step publishes to dynOrphan.Of("o1") via
// persistence.PublishToChannel, then completes. Because no other step waits on the
// orphan instance, the message sits in RunRow.UnconsumedChannelMessages
// keyed by the resolved name "dyn-orphan-o1".
// ----------------------------------------------------------------------------

type DynOrphanState struct {
	Done bool `json:"done"`
}

type dynOrphanStep struct {
	dex.StepDefaults[any]
}

func (s *dynOrphanStep) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	if err := dynKeyDone.SetValue(ctx, true); err != nil {
		return nil, err
	}
	if err := dynOrphan.Publish(ctx, "o1", map[string]any{"hello": "world"}); err != nil {
		return nil, err
	}
	return dex.Complete(nil), nil
}

type DynOrphanFlow struct {
	dex.FlowDefaults
}

func (f *DynOrphanFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep[any](&dynOrphanStep{}),
	}
}

func (f *DynOrphanFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		StateKeys:       []dex.StateKeyDef{dex.DefineStateKey(dynKeyDone)},
		DynamicChannels: []dex.ChannelDef{dex.DefineDynamicChannel(dynOrphan)},
	}
}

func TestSDKE2E_DynamicChannel_InternalPublishWithoutWaiterLandsInUnconsumed(t *testing.T) {
	registry := dex.NewRegistry()
	registry.Register(&DynOrphanFlow{})

	client, worker, taskListName, runsPb := startSDKE2EWithRunsPb(t, registry)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	startWorkerBg(t, worker)

	runID := uuid.NewString()
	require.NoError(t, client.StartRunWithOptions(ctx, runID, &DynOrphanFlow{}, &dex.RunOptions{TaskListName: taskListName}))

	status, err := client.WaitForRunComplete(ctx, runID)
	require.NoError(t, err)
	done, err := dynKeyDone.GetRunValue(client, ctx, runID)
	require.NoError(t, err)
	require.Equal(t, dex.RunStatusCompleted, status)
	assert.True(t, done)

	// Drop down to the proto API to inspect unconsumed_channel_messages,
	// which the typed dex.RunResult does NOT expose.
	resp, err := runsPb.GetRun(ctx, &pb.GetRunRequest{
		Namespace: "default",
		RunId:     runID,
	})
	require.NoError(t, err)
	require.True(t, resp.Found)

	// The full resolved name is prefix+key = "dyn-orphan-o1".
	got, ok := resp.UnconsumedChannelMessages["dyn-orphan-o1"]
	require.Truef(t, ok,
		"unconsumed_channel_messages must contain key %q (full prefix+key); got keys: %v",
		"dyn-orphan-o1", channelMapKeys(resp.UnconsumedChannelMessages))
	require.Len(t, got.Messages, 1, "exactly one orphan publish was emitted by the step")
}

// ----------------------------------------------------------------------------
// Test 4: condition_results carry the resolved full name
//
// Reuses the producer side of the sibling shape (single-step variant). After
// the run completes, fetch history events via OpsService.GetHistoryEvents
// and assert the StepExecuteCompleted carries condition_results whose channel
// name equals "dyn-updates-fullname-key" — i.e., prefix+key, not just the key.
// ----------------------------------------------------------------------------

type DynNameState struct {
	Triggered bool `json:"triggered"`
}

type dynNameWaitStep struct {
	dex.DefaultStepId
	dex.DefaultStepOptions
}

func (s *dynNameWaitStep) WaitFor(_ dex.Context, _ any) (dex.WaitForCondition, error) {
	return dex.AnyOf(dynUpdates.Condition("fullname-key")), nil
}

func (s *dynNameWaitStep) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	if err := dynKeyTriggered.SetValue(ctx, true); err != nil {
		return nil, err
	}
	return dex.Complete(nil), nil
}

type DynNameFlow struct {
	dex.FlowDefaults
}

func (f *DynNameFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep[any](&dynNameWaitStep{}),
	}
}

func (f *DynNameFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		StateKeys:       []dex.StateKeyDef{dex.DefineStateKey(dynKeyTriggered)},
		DynamicChannels: []dex.ChannelDef{dex.DefineDynamicChannel(dynUpdates)},
	}
}

func TestSDKE2E_DynamicChannel_ConditionResultsCarryFullName(t *testing.T) {
	registry := dex.NewRegistry()
	registry.Register(&DynNameFlow{})

	client, worker, taskListName, opsPb := startSDKE2EWithOpsPb(t, registry)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	startWorkerBg(t, worker)

	runID := uuid.NewString()
	require.NoError(t, client.StartRunWithOptions(ctx, runID, &DynNameFlow{}, &dex.RunOptions{TaskListName: taskListName}))

	requireRunStatusReached(t, client, runID, dex.RunStatusAllStepsWaitingForConditions, 30*time.Second)

	require.NoError(t, client.PublishToDynamicChannel(ctx, runID, dynUpdates.Prefix, "fullname-key",
		map[string]any{"value": "trigger"}))

	status, err := client.WaitForRunComplete(ctx, runID)
	require.NoError(t, err)
	triggered, err := dynKeyTriggered.GetRunValue(client, ctx, runID)
	require.NoError(t, err)
	require.Equal(t, dex.RunStatusCompleted, status)
	require.True(t, triggered)

	// Pull all history events; find the StepExecuteCompleted whose
	// condition_results reference the channel that the step waited on.
	hist, err := opsPb.GetHistoryEvents(ctx, &pb.GetHistoryEventsRequest{
		Namespace: "default",
		RunId:     runID,
		AfterId:   0,
		Limit:     200,
	})
	require.NoError(t, err)
	require.NotEmpty(t, hist.Events, "history must contain at least RunStart + StepExecuteCompleted")

	var found bool
	for _, ev := range hist.Events {
		exec := ev.GetStepExecuteCompleted()
		if exec == nil {
			continue
		}
		for _, cr := range exec.ConditionResults {
			ch := cr.GetChannel()
			if ch == nil {
				continue
			}
			// The single bit being verified: server received the FULL
			// resolved channel name (prefix+key), not just "fullname-key"
			// or just "dyn-updates-".
			require.Equal(t, "dyn-updates-fullname-key", ch.ChannelName,
				"condition_results.channel.channel_name must equal prefix+key (the full resolved dynamic-channel name)")
			require.True(t, ch.Satisfied, "channel condition must be satisfied (publish landed)")
			require.GreaterOrEqual(t, ch.ConsumedCount, int32(1), "consumed_count must reflect the consumed message")
			found = true
		}
	}
	require.True(t, found, "expected at least one StepExecuteCompleted carrying a channel ConditionResult")
}

// ----------------------------------------------------------------------------
// Test helpers (local to this file)
// ----------------------------------------------------------------------------

// startSDKE2EWithRunsPb is startSDKE2E plus a raw pb.RunsServiceClient on
// the same connection so callers can inspect fields the typed SDK doesn't
// expose (e.g., unconsumed_channel_messages on GetRunResponse).
func startSDKE2EWithRunsPb(t *testing.T, registry *dex.Registry) (*dex.Client, *dex.Worker, string, pb.RunsServiceClient) {
	app, _, _ := startE2EServer(t)
	client, worker, taskListName, runsPb := wireClientsAndWorker(t, app, registry)
	return client, worker, taskListName, runsPb
}

// startSDKE2EWithOpsPb is startSDKE2E plus a pb.OpsServiceClient against
// the in-process OpsGRPC listener. Used by tests that need to inspect
// HistoryEvents (condition_results, payload variants, etc).
func startSDKE2EWithOpsPb(t *testing.T, registry *dex.Registry) (*dex.Client, *dex.Worker, string, pb.OpsServiceClient) {
	app, _, _ := startE2EServer(t)
	client, worker, taskListName, _ := wireClientsAndWorker(t, app, registry)

	opsConn, err := grpc.NewClient(app.OpsGRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { opsConn.Close() })

	return client, worker, taskListName, pb.NewOpsServiceClient(opsConn)
}

// wireClientsAndWorker mirrors the run/match/tasklist setup in startSDKE2E,
// but takes app explicitly so callers can build extra clients (ops, raw
// runs) on the same listener addresses.
func wireClientsAndWorker(t *testing.T, app *cmd.ServerApp, registry *dex.Registry) (*dex.Client, *dex.Worker, string, pb.RunsServiceClient) {
	runConn, err := grpc.NewClient(app.RunGRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { runConn.Close() })

	matchConn, err := grpc.NewClient(app.MatchingGRPCAddress(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { matchConn.Close() })

	taskListName := "sdk-e2e-dynchan-" + uuid.NewString()
	client := dex.NewClient(registry, runConn, "default")
	worker := dex.NewWorker(registry, matchConn, runConn, "default", dex.WorkerOptions{
		TaskListName:   taskListName,
		RunConcurrency: 1,
	})
	return client, worker, taskListName, pb.NewRunsServiceClient(runConn)
}

// requireRunStatusReached polls GetRun until the run hits the given target
// status (or a terminal status) or the deadline elapses. Used to fence
// "publish lands AFTER the wait step has armed" so tests are deterministic.
func requireRunStatusReached(t *testing.T, client *dex.Client, runID string, target int32, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		got, err := client.GetRun(context.Background(), runID)
		require.NoError(t, err)
		require.NotNil(t, got)
		if got.Status == target {
			return
		}
		// Bail early if the run already terminated — caller's expected
		// transition cannot happen.
		switch got.Status {
		case dex.RunStatusCompleted, dex.RunStatusFailed:
			t.Fatalf("run %q reached terminal status %d before observing target %d", runID, got.Status, target)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("run %q did not reach status %d within %v", runID, target, within)
}

// channelMapKeys is a tiny diagnostic helper: when an
// UnconsumedChannelMessages assertion fails, we want to see the list of
// keys that ARE present so the failure message is debuggable at a glance.
func channelMapKeys(m map[string]*pb.ChannelMessages) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// ----------------------------------------------------------------------------
// Test 5: late external publish during worker exit window
//
// Reproduces the dev-stack race documented in
// docs/wait-for-conditions-design.md and dev-stack.sh: fan-out two
// per-key wait steps, then publish to BOTH keys back-to-back with no
// delay. The first publish triggers worker pickup; the second publish
// races the worker's processRun exit window. The race resolves via one
// of two converging fixes:
//
//   - SDK exit-drain: the second push lands in the worker's externalMsgCh
//     before the worker exits processRun → drain reads it, promotes the
//     waiting sibling, run completes in-process.
//   - Server Running → AllStepsWaiting unconsumed sweep: the worker
//     completes its first chain and reports the transition; the second
//     publish is already buffered in RunRow.UnconsumedChannelMessages,
//     the engine's sweep catches it and upgrades to Pending.
//
// Whichever path wins, the run MUST complete well under the 2-minute
// durable timer. Without ANY of the fixes, this test would hang for
// minutes — making it a reliable regression detector for the whole
// recovery chain.
// ----------------------------------------------------------------------------

type DynLateState struct {
	Ord1Done bool `json:"ord_1_done"`
	Ord2Done bool `json:"ord_2_done"`
}

type DynLateInput struct {
	Key string `json:"key"`
}

type dynLateDispatchStep struct {
	dex.StepDefaults[any]
}

func (s *dynLateDispatchStep) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	return dex.GoToMany(
		dex.MovementOf(&dynLateWaitStep{}, DynLateInput{Key: "ord-1"}),
		dex.MovementOf(&dynLateWaitStep{}, DynLateInput{Key: "ord-2"}),
	), nil
}

type dynLateWaitStep struct {
	dex.DefaultStepId
	dex.DefaultStepOptions
}

func (s *dynLateWaitStep) WaitFor(_ dex.Context, input DynLateInput) (dex.WaitForCondition, error) {
	return dex.AnyOf(
		dynUpdates.Condition(input.Key),
		dex.Timer(2*time.Minute), // generous safety net; should never fire under the fix
	), nil
}

func (s *dynLateWaitStep) Execute(ctx dex.Context, input DynLateInput) (dex.StepDecision, error) {
	switch input.Key {
	case "ord-1":
		if err := dynKeyOrd1Done.SetValue(ctx, true); err != nil {
			return nil, err
		}
	case "ord-2":
		if err := dynKeyOrd2Done.SetValue(ctx, true); err != nil {
			return nil, err
		}
	}
	return dex.DeadEnd(), nil
}

type DynLateFlow struct {
	dex.FlowDefaults
}

func (f *DynLateFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep[any](&dynLateDispatchStep{}),
		dex.NonStartingStep[DynLateInput](&dynLateWaitStep{}),
	}
}

func (f *DynLateFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		StateKeys: []dex.StateKeyDef{
			dex.DefineStateKey(dynKeyOrd1Done),
			dex.DefineStateKey(dynKeyOrd2Done),
		},
		DynamicChannels: []dex.ChannelDef{dex.DefineDynamicChannel(dynUpdates)},
	}
}

func TestSDKE2E_DynamicChannel_LateExternalPublishUnblocksAfterAllStepsWaiting(t *testing.T) {
	registry := dex.NewRegistry()
	registry.Register(&DynLateFlow{})

	client, worker, taskListName := startSDKE2E(t, registry)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	startWorkerBg(t, worker)

	runID := uuid.NewString()
	require.NoError(t, client.StartRunWithOptions(ctx, runID, &DynLateFlow{}, &dex.RunOptions{TaskListName: taskListName}))

	// Wait until both wait steps have armed (run reaches AllStepsWaiting),
	// so the back-to-back publishes definitely land on a fully-prepared
	// flow rather than racing the dispatch fan-out itself.
	requireRunStatusReached(t, client, runID, dex.RunStatusAllStepsWaitingForConditions, 30*time.Second)

	// Back-to-back publishes with NO sleep between them. This is the
	// dev-stack race: the first publish flips the run to Running and
	// hands it to a worker; the second publish lands while the run is
	// still Running (server takes the Running branch and buffers it
	// in UnconsumedChannelMessages), then races the worker's
	// processRun exit window. See the comment block above for the
	// three converging recovery paths.
	require.NoError(t, client.PublishToDynamicChannel(ctx, runID, dynUpdates.Prefix, "ord-1",
		map[string]any{"value": "delivered-ord-1"}))
	require.NoError(t, client.PublishToDynamicChannel(ctx, runID, dynUpdates.Prefix, "ord-2",
		map[string]any{"value": "delivered-ord-2"}))

	// Without any of the fixes, the second branch sits behind the
	// 2-minute durable timer. With the fix(es), it resumes promptly.
	// Allow up to 60s — generous for loaded CI, still well under 2m.
	completionCtx, completionCancel := context.WithTimeout(ctx, 60*time.Second)
	defer completionCancel()
	status, err := client.WaitForRunComplete(completionCtx, runID)
	require.NoError(t, err, "run must complete well under the 2-minute durable timer")
	ord1Done, err := dynKeyOrd1Done.GetRunValue(client, completionCtx, runID)
	require.NoError(t, err)
	ord2Done, err := dynKeyOrd2Done.GetRunValue(client, completionCtx, runID)
	require.NoError(t, err)
	require.Equal(t, dex.RunStatusCompleted, status)
	assert.True(t, ord1Done, "ord-1 branch must complete")
	assert.True(t, ord2Done,
		"ord-2 branch must complete via the late-publish recovery path (would be false under the original race)")
}
