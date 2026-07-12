package sdke2e

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Multi-agent benchmark e2e tests
//
// These mirror the design in benchmark/cmd/benchmarkworker/agent_flows.go
// (which lives in a separate Go module so we can't import it directly).
// The local types use a deliberate `ma` / `sa` prefix so they don't
// collide with other sdke2e flows.
//
// What's being proven (vs. existing sdke2e tests):
//
//   - Cross-run StartRun from inside a step's Execute. The parent
//     mainAgent's worker calls client.StartRun against the same gRPC
//     connection it polls from — proving the SDK Worker can recursively
//     launch flows. NOT covered by sdk_e2e_test.go (no parent-launches-
//     child path) or sdk_e2e_dynamic_channel_test.go (cross-flow
//     StartRun is novel).
//   - Cross-run dynamic-channel publish from inside Execute. The child
//     subAgent's worker publishes to a dynamic-channel instance whose
//     fanned-out name lives on the PARENT mainAgent's RunRow. The
//     existing dynamic-channel e2e tests only cover external publishes
//     (via raw RawClient.PublishToChannel) and intra-run internal
//     publishes via persistence.PublishToChannel.
//   - The "stuck-detector" timer re-arm pattern: AnyOf(channel, timer)
//     where on timer-fired the step does GoTo(self) instead of advancing
//     or DeadEnd-ing. The wait-for design doc covers this conceptually;
//     this test locks in that the SDK + engine actually round-trip the
//     re-arm without dropping the step.
//   - Sibling cancellation triggered by a channel publish: covered by
//     sdk_e2e_cancel_sibling_test.go for the dispatch-fan-out pattern,
//     but the multi-agent design uses GoToMany within an LLM-loop
//     branch with a different cancel cause shape.
//
// Scaling: the production benchmark uses 5s/60s sleeps and a 30s timer.
// Tests use 50ms/2s sleeps and a 500ms timer to keep the test suite
// fast while preserving the same race ordering.
// =============================================================================

// ----- Shared types ---------------------------------------------------------

type maMessageKind string

const (
	maKindHumanStartSubAgents maMessageKind = "human.start_subagents"
	maKindHumanStartLLMLoop   maMessageKind = "human.start_llm_loop"
	maKindHumanComplete       maMessageKind = "human.complete"
	maKindSubAgentResponse    maMessageKind = "subagent.response"
)

type maMessage struct {
	Kind         maMessageKind `json:"kind"`
	NumSubAgents int           `json:"num_subagents,omitempty"`
	SubAgentResp *saResponse   `json:"subagent_response,omitempty"`
}

type saRequest struct {
	Source string `json:"source"`
}

type saResponse struct {
	SubAgentRunID string `json:"subagent_run_id"`
	Counter       int    `json:"counter"`
	IsLast        bool   `json:"is_last"`
}

type maInterrupt struct{}

var (
	maSubAgentRequestCh  = dex.NewChannel[saRequest]("ma-test-SubAgentRequest")
	maMainAgentMessageCh = dex.NewChannel[maMessage]("ma-test-MainAgentMessage")
	maInterruptLLMCh     = dex.NewChannel[maInterrupt]("ma-test-InterruptLLM")
	maSubAgentResponseCh = dex.NewDynamicChannel[saResponse]("ma-test-SubAgentResponse-")

	maKeySubAgentResponseCount = dex.NewStateKey[int]("subagent_response_count")
	maKeyFastLLMRunning        = dex.NewStateKey[bool]("fast_llm_running")
	maKeySlowLLMRunning        = dex.NewStateKey[bool]("slow_llm_running")
	saKeyCounter               = dex.NewStateKey[int]("counter")
	saKeyParentRunID           = dex.NewStateKey[string]("parent_run_id")
)

// maTestParams threads scaled-down timing into the flow so each test
// can pick its own deadlines. The flow reads from a package-level var
// since steps can't carry per-test config through their struct.
type maTestParams struct {
	stuckDetectorTimeout time.Duration
	subAgentSleep        time.Duration // sleep between subagent publishes (constant, not random)
	subAgentMaxIters     int
	fastLLMSleep         time.Duration
	slowLLMSleep         time.Duration
}

var (
	maParams       = maTestParams{}
	maClientGlobal *dex.Client
)

// maObservedSubAgentStarts counts how many times startSubAgentStep
// successfully launched a child run; tests assert against it to
// distinguish "subagent was launched but never published" from
// "subagent was never launched at all".
var maObservedSubAgentStarts atomic.Int64

// maObservedStuckDetectorFires counts re-arms on the timer-fired
// branch of waitForSubAgentStep. Tests assert this is > 0 in the
// stuck-subagent scenario.
var maObservedStuckDetectorFires atomic.Int64

// ----- mainAgent flow -------------------------------------------------------

type maInitInput struct {
	MaxConcurrentSubAgents int `json:"max_concurrent_subagents"`
}

type maState struct {
	SubAgentResponseCount int  `json:"subagent_response_count"`
	FastLLMRunning        bool `json:"fast_llm_running"`
	SlowLLMRunning        bool `json:"slow_llm_running"`
}

type maSlotInput struct {
	Slot int `json:"slot"`
}

type maWaitInput struct {
	SubAgentRunID string `json:"subagent_run_id"`
}

type maInitStep struct {
	dex.StepDefaults[maInitInput]
}

func (s *maInitStep) Execute(ctx dex.Context, input maInitInput) (dex.StepDecision, error) {
	n := input.MaxConcurrentSubAgents
	if n <= 0 {
		n = 1
	}
	movements := make([]dex.StepMovement, 0, n+1)
	for i := 0; i < n; i++ {
		movements = append(movements,
			dex.MovementOf(&maStartSubAgentStep{}, maSlotInput{Slot: i}))
	}
	movements = append(movements,
		dex.MovementOf(&maLoopStep{}, nil))
	return dex.GoToMany(movements...), nil
}

type maStartSubAgentStep struct {
	dex.DefaultStepId
	dex.DefaultStepOptions
}

func (s *maStartSubAgentStep) WaitFor(_ dex.Context, _ maSlotInput) (dex.WaitForCondition, error) {
	return dex.AnyOf(maSubAgentRequestCh.ConditionWithMinMax(1, 1)), nil
}

func (s *maStartSubAgentStep) Execute(ctx dex.Context, _ maSlotInput) (dex.StepDecision, error) {
	// Mirror buildSubAgentRunID in agent_flows.go: include parentRunID
	// so concurrent test runs in the same namespace don't collide on
	// the i-th slot's subagent runID.
	subRunID := "ma-test-subagent-" + ctx.RunID() + "-" + ctx.StepExecutionID()
	startCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	err := maClientGlobal.StartRunWithOptions(
		startCtx, subRunID, &saFlow{},
		&dex.RunOptions{TaskListName: dex.DefaultTaskListName},
		saInitInput{ParentRunID: ctx.RunID()},
	)
	if err != nil {
		// In tests we surface the error so the assertion fails with a
		// useful message rather than silently DeadEnd.
		return nil, fmt.Errorf("startSubAgentStep: %w", err)
	}
	maObservedSubAgentStarts.Add(1)
	return dex.GoTo(&maWaitStep{}, maWaitInput{SubAgentRunID: subRunID}), nil
}

type maWaitStep struct {
	dex.DefaultStepId
	dex.DefaultStepOptions
}

func (s *maWaitStep) WaitFor(_ dex.Context, input maWaitInput) (dex.WaitForCondition, error) {
	return dex.AnyOf(
		maSubAgentResponseCh.ConditionWithMinMax(input.SubAgentRunID, 1, 1),
		dex.Timer(maParams.stuckDetectorTimeout),
	), nil
}

func (s *maWaitStep) Execute(ctx dex.Context, input maWaitInput) (dex.StepDecision, error) {
	msgs, err := maSubAgentResponseCh.GetConsumedMessages(ctx, input.SubAgentRunID)
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		maObservedStuckDetectorFires.Add(1)
		return dex.GoTo(&maWaitStep{}, input), nil
	}
	resp := msgs[0]
	bridge := maMessage{Kind: maKindSubAgentResponse, SubAgentResp: &resp}
	if resp.IsLast {
		if err := maMainAgentMessageCh.Publish(ctx, bridge); err != nil {
			return nil, err
		}
		return dex.GoTo(&maStartSubAgentStep{}, maSlotInput{}), nil
	}
	if err := maMainAgentMessageCh.Publish(ctx, bridge); err != nil {
		return nil, err
	}
	return dex.GoTo(&maWaitStep{}, input), nil
}

type maLoopStep struct {
	dex.DefaultStepId
	dex.DefaultStepOptions
}

func (s *maLoopStep) WaitFor(_ dex.Context, _ any) (dex.WaitForCondition, error) {
	return dex.AnyOf(maMainAgentMessageCh.ConditionWithMinMax(1, 100)), nil
}

func (s *maLoopStep) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	msgs, err := maMainAgentMessageCh.GetConsumedMessages(ctx)
	if err != nil {
		return nil, err
	}
	subAgentResponseCount, err := maKeySubAgentResponseCount.GetValue(ctx)
	if err != nil {
		return nil, err
	}

	complete := false
	startLLM := false
	requests := 0
	responses := 0
	for _, m := range msgs {
		switch m.Kind {
		case maKindHumanComplete:
			complete = true
		case maKindHumanStartLLMLoop:
			startLLM = true
		case maKindHumanStartSubAgents:
			n := m.NumSubAgents
			if n <= 0 {
				n = 1
			}
			requests += n
		case maKindSubAgentResponse:
			responses++
		}
	}
	newCount := subAgentResponseCount + responses
	switch {
	case complete:
		if err := maKeySubAgentResponseCount.SetValue(ctx, newCount); err != nil {
			return nil, err
		}
		return dex.Complete(nil), nil
	case startLLM:
		if err := maKeySubAgentResponseCount.SetValue(ctx, newCount); err != nil {
			return nil, err
		}
		if err := maKeyFastLLMRunning.SetValue(ctx, true); err != nil {
			return nil, err
		}
		if err := maKeySlowLLMRunning.SetValue(ctx, true); err != nil {
			return nil, err
		}
		return dex.GoTo(&maStartLLMStep{}, nil), nil
	}
	if err := maKeySubAgentResponseCount.SetValue(ctx, newCount); err != nil {
		return nil, err
	}
	if requests > 0 {
		req := saRequest{Source: "main_agent_loop"}
		for i := 0; i < requests; i++ {
			if err := maSubAgentRequestCh.Publish(ctx, req); err != nil {
				return nil, err
			}
		}
	}
	return dex.GoTo(&maLoopStep{}, nil), nil
}

type maStartLLMStep struct {
	dex.StepDefaults[any]
}

func (s *maStartLLMStep) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	return dex.GoToMany(
		dex.MovementOf(&maFastLLMStep{}, nil),
		dex.MovementOf(&maSlowLLMStep{}, nil),
		dex.MovementOf(&maLLMLoopStep{}, nil),
	), nil
}

type maFastLLMStep struct {
	dex.StepDefaults[any]
}

func (s *maFastLLMStep) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	select {
	case <-ctx.Done():
		return dex.DeadEnd(), nil
	case <-time.After(maParams.fastLLMSleep):
	}
	if err := maKeyFastLLMRunning.SetValue(ctx, false); err != nil {
		return nil, err
	}
	return dex.DeadEnd(), nil
}

type maSlowLLMStep struct {
	dex.StepDefaults[any]
}

func (s *maSlowLLMStep) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	select {
	case <-ctx.Done():
		return dex.DeadEnd(), nil
	case <-time.After(maParams.slowLLMSleep):
	}
	if err := maKeySlowLLMRunning.SetValue(ctx, false); err != nil {
		return nil, err
	}
	return dex.DeadEnd(), nil
}

type maLLMLoopStep struct {
	dex.DefaultStepId
	dex.DefaultStepOptions
}

func (s *maLLMLoopStep) WaitFor(_ dex.Context, _ any) (dex.WaitForCondition, error) {
	return dex.AnyOf(maInterruptLLMCh.ConditionWithMinMax(1, 100)), nil
}

func (s *maLLMLoopStep) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	return dex.GoTo(&maLoopStep{}, nil).
		WithCancelingSiblingStepExecution(
			dex.CancelOf(&maFastLLMStep{}),
			dex.CancelOf(&maSlowLLMStep{}),
		), nil
}

type maFlow struct {
	dex.DefaultFlowType
}

func (f *maFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep[maInitInput](&maInitStep{}),
		dex.NonStartingStep[maSlotInput](&maStartSubAgentStep{}),
		dex.NonStartingStep[maWaitInput](&maWaitStep{}),
		dex.NonStartingStep[any](&maLoopStep{}),
		dex.NonStartingStep[any](&maStartLLMStep{}),
		dex.NonStartingStep[any](&maFastLLMStep{}),
		dex.NonStartingStep[any](&maSlowLLMStep{}),
		dex.NonStartingStep[any](&maLLMLoopStep{}),
	}
}

func (f *maFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		StateKeys: []dex.StateKeyDef{
			dex.DefineStateKey(maKeySubAgentResponseCount),
			dex.DefineStateKey(maKeyFastLLMRunning),
			dex.DefineStateKey(maKeySlowLLMRunning),
		},
		Channels: []dex.ChannelDef{
			dex.DefineChannel(maSubAgentRequestCh),
			dex.DefineChannel(maMainAgentMessageCh),
			dex.DefineChannel(maInterruptLLMCh),
		},
		DynamicChannels: []dex.ChannelDef{dex.DefineDynamicChannel(maSubAgentResponseCh)},
	}
}

// ----- subAgent flow --------------------------------------------------------

type saInitInput struct {
	ParentRunID string `json:"parent_run_id"`
}

type saState struct {
	Counter     int    `json:"counter"`
	ParentRunID string `json:"parent_run_id"`
}

type saInitStep struct {
	dex.StepDefaults[saInitInput]
}

func (s *saInitStep) Execute(ctx dex.Context, input saInitInput) (dex.StepDecision, error) {
	if err := saKeyCounter.SetValue(ctx, 1); err != nil {
		return nil, err
	}
	if err := saKeyParentRunID.SetValue(ctx, input.ParentRunID); err != nil {
		return nil, err
	}
	return dex.GoTo(&saLoopStep{}, nil), nil
}

type saLoopStep struct {
	dex.StepDefaults[any]
}

func (s *saLoopStep) Execute(ctx dex.Context, _ any) (dex.StepDecision, error) {
	select {
	case <-ctx.Done():
		return dex.DeadEnd(), nil
	case <-time.After(maParams.subAgentSleep):
	}
	counter, err := saKeyCounter.GetValue(ctx)
	if err != nil {
		return nil, err
	}
	parentRunID, err := saKeyParentRunID.GetValue(ctx)
	if err != nil {
		return nil, err
	}

	isLast := counter >= maParams.subAgentMaxIters
	resp := saResponse{
		SubAgentRunID: ctx.RunID(),
		Counter:       counter,
		IsLast:        isLast,
	}
	pubCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := maClientGlobal.PublishToDynamicChannel(pubCtx, parentRunID, maSubAgentResponseCh.Prefix, ctx.RunID(), resp); err != nil {
		return nil, fmt.Errorf("subAgentLoopStep publish: %w", err)
	}
	if isLast {
		return dex.Complete(nil), nil
	}
	if err := saKeyCounter.SetValue(ctx, counter+1); err != nil {
		return nil, err
	}
	return dex.GoTo(&saLoopStep{}, nil), nil
}

type saFlow struct {
	dex.DefaultFlowType
}

func (f *saFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep[saInitInput](&saInitStep{}),
		dex.NonStartingStep[any](&saLoopStep{}),
	}
}

func (f *saFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		StateKeys: []dex.StateKeyDef{
			dex.DefineStateKey(saKeyCounter),
			dex.DefineStateKey(saKeyParentRunID),
		},
		DynamicChannels: []dex.ChannelDef{dex.DefineDynamicChannel(maSubAgentResponseCh)},
	}
}

// ----- Stub subAgent flow that NEVER publishes ------------------------------
//
// Used by the stuck-subagent test: we still want StartRun to succeed
// (so the parent's startSubAgentStep can advance to maWaitStep), but
// the subagent must not publish anything so the parent's 30s timer is
// the only signal that fires.

type saHangFlow struct {
	dex.DefaultFlowType
}

func (f *saHangFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.StartingStep[saInitInput](&saHangStep{}),
	}
}

func (f *saHangFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		StateKeys: []dex.StateKeyDef{dex.DefineStateKey(saKeyParentRunID)},
	}
}

type saHangStep struct {
	dex.StepDefaults[saInitInput]
}

// Execute returns DeadEnd immediately so the run reaches Completed
// without ever publishing — exactly the "subagent ran but never replied"
// shape we need to exercise the stuck-detector branch.
func (s *saHangStep) Execute(ctx dex.Context, input saInitInput) (dex.StepDecision, error) {
	if err := saKeyParentRunID.SetValue(ctx, input.ParentRunID); err != nil {
		return nil, err
	}
	return dex.DeadEnd(), nil
}

// ----- Tests ----------------------------------------------------------------

// resetMaTestState clears global counters + reassigns maParams. Each
// test does this in its setup so cross-test counter bleed never produces
// false-positive assertions.
func resetMaTestState(p maTestParams) {
	maParams = p
	maObservedSubAgentStarts.Store(0)
	maObservedStuckDetectorFires.Store(0)
}

// startMaWorker wires a Worker for the given registry, spawns it, and
// hangs the resulting Client off maClientGlobal so the multi-agent
// flow's cross-run StartRun / publish calls reach the same test
// server. Returns the Client + a cleanup func.
func startMaWorker(t *testing.T, registry *dex.Registry) (*dex.Client, func()) {
	t.Helper()
	// The multi-agent flow dispatches the main run AND every cross-run
	// StartRun (sub-agents) onto dex.DefaultTaskListName, so the worker must
	// poll that exact tasklist
	client, worker := startSDKE2EWithTaskList(t, registry, dex.DefaultTaskListName, 10)
	maClientGlobal = client

	workerDone := make(chan error, 1)
	go func() { workerDone <- worker.Start() }()

	cleanup := func() {
		worker.Stop()
		select {
		case <-workerDone:
		case <-time.After(5 * time.Second):
			t.Log("worker did not exit within 5s of cancel")
		}
		maClientGlobal = nil
	}
	waitWorkerReady(t, worker)
	return client, cleanup
}

// TestSDKE2E_MultiAgent_HappyPath proves the cross-run StartRun +
// cross-run dynamic-channel publish round-trip end-to-end:
//   - mainInitStep fans out N=2 startSubAgentStep + 1 mainLoopStep
//   - human start_subagents num=2 message wakes mainLoopStep; it
//     publishes 2 to maSubAgentRequestCh
//   - 2 startSubAgentSteps each StartRun a subAgent run
//   - each subAgent runs Counter 1→3 publishing 3 responses → 6 total
//   - mainLoopStep accumulates SubAgentResponseCount = 6
//   - human complete message terminates the run
func TestSDKE2E_MultiAgent_HappyPath(t *testing.T) {
	resetMaTestState(maTestParams{
		stuckDetectorTimeout: 30 * time.Second,
		subAgentSleep:        50 * time.Millisecond,
		subAgentMaxIters:     3,
		fastLLMSleep:         10 * time.Millisecond,
		slowLLMSleep:         10 * time.Millisecond,
	})

	registry := dex.NewRegistry()
	registry.Register(&maFlow{})
	registry.Register(&saFlow{})

	client, cleanup := startMaWorker(t, registry)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	runID := "ma-happy-" + uuid.NewString()
	require.NoError(t, client.StartRunWithOptions(ctx, runID, &maFlow{},
		&dex.RunOptions{TaskListName: dex.DefaultTaskListName},
		maInitInput{MaxConcurrentSubAgents: 2},
	))

	// All N+1 fan-out steps must be in WAITING_FOR_CONDITION before
	// the first publish lands so we get a deterministic drain ordering.
	requireRunStatusReached(t, client, runID, dex.RunStatusAllStepsWaitingForConditions, 30*time.Second)

	// Stage 1: ask for 2 subagents.
	require.NoError(t, client.PublishToChannel(ctx, runID, maMainAgentMessageCh.Name,
		maMessage{Kind: maKindHumanStartSubAgents, NumSubAgents: 2}))

	// Parent accumulates all 6 responses (2 subagents × 3 each). ~450ms
	// locally; 240s headroom absorbs shared-DB fan-in contention when the
	// whole suite runs in parallel against a loaded CI Mongo/Postgres.
	require.Eventually(t, func() bool {
		count, err := maKeySubAgentResponseCount.GetRunValue(client, ctx, runID)
		return err == nil && count >= 6
	}, 240*time.Second, 100*time.Millisecond,
		"parent must accumulate 6 SubAgentResponses (2 subagents × 3 each)")

	require.GreaterOrEqual(t, maObservedSubAgentStarts.Load(), int64(2),
		"startSubAgentStep must have launched at least 2 child runs")

	// Stage 2: tell the agent we're done.
	require.NoError(t, client.PublishToChannel(ctx, runID, maMainAgentMessageCh.Name,
		maMessage{Kind: maKindHumanComplete}))

	status, err := client.WaitForRunComplete(ctx, runID)
	require.NoError(t, err)
	subAgentResponseCount, err := maKeySubAgentResponseCount.GetRunValue(client, ctx, runID)
	require.NoError(t, err)
	assert.Equal(t, dex.RunStatusCompleted, status,
		"complete message must terminate the run cleanly")
	assert.GreaterOrEqual(t, subAgentResponseCount, 6,
		"final SubAgentResponseCount must be at least 6")
}

// TestSDKE2E_MultiAgent_LLMInterruptCancelsSlowLLM exercises the
// sibling-cancellation path: maSlowLLMStep is told to sleep 10s, but
// /agentInterruptLLM fires after 100ms, llmLoopStep wakes and emits
// WithCancelingSiblingStepExecution(slowLLMStep) → ctx-cancel kills the
// sleep, the run completes within the test's 20s budget rather than
// hanging until slowLLMSleep elapses.
func TestSDKE2E_MultiAgent_LLMInterruptCancelsSlowLLM(t *testing.T) {
	resetMaTestState(maTestParams{
		stuckDetectorTimeout: 30 * time.Second,
		subAgentSleep:        50 * time.Millisecond,
		subAgentMaxIters:     3,
		// fastLLMSleep is short — it should DeadEnd naturally before
		// the interrupt arrives. slowLLMSleep is LONG so the cancel
		// kicks in: if cancellation didn't work, the test would time
		// out at the WaitForRunComplete deadline.
		fastLLMSleep: 50 * time.Millisecond,
		slowLLMSleep: 40 * time.Second,
	})

	registry := dex.NewRegistry()
	registry.Register(&maFlow{})
	registry.Register(&saFlow{})

	client, cleanup := startMaWorker(t, registry)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	runID := "ma-interrupt-" + uuid.NewString()
	require.NoError(t, client.StartRunWithOptions(ctx, runID, &maFlow{},
		&dex.RunOptions{TaskListName: dex.DefaultTaskListName},
		maInitInput{MaxConcurrentSubAgents: 1},
	))
	requireRunStatusReached(t, client, runID, dex.RunStatusAllStepsWaitingForConditions, 30*time.Second)

	// Skip the subagent dance — go straight to start_llm_loop so
	// we land in the 3-way fan-out.
	require.NoError(t, client.PublishToChannel(ctx, runID, maMainAgentMessageCh.Name,
		maMessage{Kind: maKindHumanStartLLMLoop}))

	// Give the worker time to dispatch fastLLMStep + slowLLMStep +
	// llmLoopStep. fastLLMStep DeadEnds at ~50ms; slowLLMStep is
	// guaranteed to still be in INVOKING_EXECUTE for the next ~10s.
	time.Sleep(200 * time.Millisecond)

	// Fire the interrupt. llmLoopStep wakes, GoTo mainLoopStep with
	// cancel of fastLLMStep + slowLLMStep. fastLLMStep is likely
	// already dead (silently ignored by the resolver); slowLLMStep is
	// in-flight and gets ctx-cancelled.
	require.NoError(t, client.PublishToChannel(ctx, runID, maInterruptLLMCh.Name, maInterrupt{}))

	// We're back at mainLoopStep — terminate.
	// Small sleep to ensure the cancel commit lands before we publish
	// complete (otherwise complete might race the cancel and we'd
	// observe the run in a weird mid-cancel state).
	time.Sleep(500 * time.Millisecond)
	require.NoError(t, client.PublishToChannel(ctx, runID, maMainAgentMessageCh.Name,
		maMessage{Kind: maKindHumanComplete}))

	// The smoking gun for "cancellation worked": completion arrives
	// well under slowLLMSleep (40s). Use a generous 25s budget (room for
	// loaded-CI latency) that's still strictly less than 40s.
	deadline, deadlineCancel := context.WithTimeout(ctx, 25*time.Second)
	defer deadlineCancel()
	status, err := client.WaitForRunComplete(deadline, runID)
	require.NoError(t, err, "run must complete before slowLLMSleep elapses (cancellation must have killed slowLLMStep)")
	assert.Equal(t, dex.RunStatusCompleted, status)
}

// TestSDKE2E_MultiAgent_StuckSubAgentTimerReArms forces the AnyOf
// timer branch by registering a stub saHangFlow that DeadEnds
// immediately without ever publishing to maSubAgentResponseCh. The
// parent's maWaitStep must:
//
//   - Re-arm via GoTo(self) on the first timer fire (observable via
//     maObservedStuckDetectorFires counter).
//   - Stay in the same step (NOT advance to maStartSubAgentStep, NOT
//     DeadEnd) so the run survives the timer fires.
func TestSDKE2E_MultiAgent_StuckSubAgentTimerReArms(t *testing.T) {
	resetMaTestState(maTestParams{
		// Short stuck-detector so the test can observe ≥2 re-arms in
		// a few seconds rather than waiting 30s × N.
		stuckDetectorTimeout: 500 * time.Millisecond,
		subAgentSleep:        50 * time.Millisecond,
		subAgentMaxIters:     3,
		fastLLMSleep:         10 * time.Millisecond,
		slowLLMSleep:         10 * time.Millisecond,
	})

	registry := dex.NewRegistry()
	registry.Register(&maFlow{})
	// Register the HANG variant of subAgentFlow under the same flow
	// type that startSubAgentStep would launch — saHangFlow's
	// reflected name (sdke2e.saHangFlow) is different from saFlow's,
	// so we have to swap startSubAgentStep's StartRun target. To
	// keep this test ergonomic we register saHangFlow alongside
	// saFlow and override the parent's behavior via a saHangFlow
	// type substitution: maStartSubAgentStep launches saFlow, so we
	// instead register a saFlow with steps that DeadEnd immediately.
	//
	// Rather than two registrations, swap the saFlow's GetSteps for
	// the duration of this test: define a sentinel "hang"-mode flag.
	// Cleanest: register saHangFlow as a sibling and adjust the parent
	// startSubAgentStep to dispatch it. We can't easily do that
	// mid-flight, so instead we register the hang flow under saFlow's
	// reflected name by exploiting the fact that registry.Register
	// uses GetFinalFlowType, which is `flowTypeFromReflect`. Both
	// saFlow and saHangFlow have different reflected names — so we
	// can't override.
	//
	// Pragmatic alternative: register saFlow normally, but make
	// subAgentSleep impossibly long (10 minutes) and rely on the
	// 500ms stuck timer firing repeatedly. The test then asserts
	// re-arm count rather than terminal state.
	maParams.subAgentSleep = 10 * time.Minute
	registry.Register(&saFlow{})

	client, cleanup := startMaWorker(t, registry)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	runID := "ma-stuck-" + uuid.NewString()
	require.NoError(t, client.StartRunWithOptions(ctx, runID, &maFlow{},
		&dex.RunOptions{TaskListName: dex.DefaultTaskListName},
		maInitInput{MaxConcurrentSubAgents: 1},
	))
	requireRunStatusReached(t, client, runID, dex.RunStatusAllStepsWaitingForConditions, 10*time.Second)

	// Ask for 1 subagent. The subagent will start (saInitStep moves
	// to saLoopStep), saLoopStep enters its 10-minute sleep, never
	// publishes — so the parent's 500ms timer is the ONLY thing that
	// fires for the whole run.
	require.NoError(t, client.PublishToChannel(ctx, runID, maMainAgentMessageCh.Name,
		maMessage{Kind: maKindHumanStartSubAgents, NumSubAgents: 1}))

	// Wait for at least 2 timer re-arms. With timeout=500ms each, two
	// re-arms take ~1s; allow 8s of headroom for CI scheduling.
	require.Eventually(t, func() bool {
		return maObservedStuckDetectorFires.Load() >= 2
	}, 8*time.Second, 100*time.Millisecond,
		"timer-fired re-arm must trigger ≥2 times (stuck-detector contract)")

	// Sanity: response count is still 0 (no subagent ever published).
	subAgentResponseCount, err := maKeySubAgentResponseCount.GetRunValue(client, ctx, runID)
	require.NoError(t, err)
	assert.Equal(t, 0, subAgentResponseCount,
		"parent must not have any subagent responses (subagent is hung)")

	// Tear down: stop the run so the subagent's 10-minute sleep
	// doesn't keep a goroutine pinned past the test.
	require.NoError(t, client.StopRun(ctx, runID, dex.StopRunComplete, ""))
}
