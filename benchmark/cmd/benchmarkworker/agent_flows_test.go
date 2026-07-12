package main

import (
	"context"
	"net/http"
	"testing"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mainAgentTestCtx(timerFired bool, channelMessages map[string][]any) dex.Context {
	return dex.NewTestContext(context.Background(), (&mainAgentFlow{}).GetPersistenceSchema(), nil, timerFired, channelMessages)
}

func subAgentTestCtx(stateMap map[string]*pb.Value) dex.Context {
	return dex.NewTestContext(context.Background(), (&subAgentFlow{}).GetPersistenceSchema(), stateMap, false, nil)
}

func TestClassifyMainAgentMessages_PriorityOrder(t *testing.T) {
	cases := []struct {
		name   string
		msgs   []mainAgentMessage
		expect mainLoopAction
	}{
		{
			name:   "empty input yields empty action",
			msgs:   nil,
			expect: mainLoopAction{},
		},
		{
			name: "complete dominates over everything else",
			msgs: []mainAgentMessage{
				{Kind: msgKindHumanStartSubAgents, NumSubAgents: 5},
				{Kind: msgKindHumanStartLLMLoop},
				{Kind: msgKindHumanComplete},
				{Kind: msgKindSubAgentResponse},
			},
			expect: mainLoopAction{
				complete:          true,
				startLLMLoop:      true,
				subagentRequests:  5,
				subagentResponses: 1,
			},
		},
		{
			name: "start_llm_loop dominates over fold-only kinds",
			msgs: []mainAgentMessage{
				{Kind: msgKindHumanStartSubAgents, NumSubAgents: 2},
				{Kind: msgKindHumanStartLLMLoop},
				{Kind: msgKindSubAgentResponse},
				{Kind: msgKindSubAgentResponse},
			},
			expect: mainLoopAction{
				startLLMLoop:      true,
				subagentRequests:  2,
				subagentResponses: 2,
			},
		},
		{
			name: "fold-only: requests + responses accumulate",
			msgs: []mainAgentMessage{
				{Kind: msgKindHumanStartSubAgents, NumSubAgents: 3},
				{Kind: msgKindHumanStartSubAgents, NumSubAgents: 1},
				{Kind: msgKindSubAgentResponse},
				{Kind: msgKindSubAgentResponse},
				{Kind: msgKindSubAgentResponse},
			},
			expect: mainLoopAction{
				subagentRequests:  4,
				subagentResponses: 3,
			},
		},
		{
			name: "start_subagents with 0/negative num defaults to 1",
			msgs: []mainAgentMessage{
				{Kind: msgKindHumanStartSubAgents, NumSubAgents: 0},
				{Kind: msgKindHumanStartSubAgents, NumSubAgents: -5},
			},
			expect: mainLoopAction{subagentRequests: 2},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := classifyMainAgentMessages(testCase.msgs)
			require.Equal(t, testCase.expect, got)
		})
	}
}

func TestBuildSubAgentRunID_DeterministicAndStable(t *testing.T) {
	require.Equal(t, "subagent-parent-run-1-step-exe-123",
		buildSubAgentRunID("parent-run-1", "step-exe-123"))
	require.Equal(t, "subagent--",
		buildSubAgentRunID("", ""),
		"both-empty is degenerate but must not panic")
	require.Equal(t,
		buildSubAgentRunID("parent-1", "step-1"),
		buildSubAgentRunID("parent-1", "step-1"))
}

func TestBuildSubAgentRunID_DistinctParentsDoNotCollide(t *testing.T) {
	first := buildSubAgentRunID("parentA", "main.startSubAgentStep-1")
	second := buildSubAgentRunID("parentB", "main.startSubAgentStep-1")
	require.NotEqual(t, first, second,
		"two parent runs with identical step exe ids must NOT produce the same subagent runID")
}

func TestBuildSubAgentRunID_DistinctSlotsWithinParentDoNotCollide(t *testing.T) {
	first := buildSubAgentRunID("parent-1", "main.startSubAgentStep-1")
	second := buildSubAgentRunID("parent-1", "main.startSubAgentStep-2")
	require.NotEqual(t, first, second,
		"two slots within the same parent must NOT produce the same subagent runID")
}

func TestMainInitStep_FansOutNPlusOne(t *testing.T) {
	step := &mainInitStep{}
	decision, err := step.Execute(mainAgentTestCtx(false, nil), mainAgentInitInput{MaxConcurrentSubAgents: 3})
	require.NoError(t, err)
	require.NotNil(t, decision)
	require.Len(t, stepMovements(t, decision), 4, "3 startSubAgentStep + 1 mainLoopStep = 4 movements")

	startStepID := dex.GetFinalStepId(&startSubAgentStep{})
	loopStepID := dex.GetFinalStepId(&mainLoopStep{})
	movements := stepMovements(t, decision)
	for index := 0; index < 3; index++ {
		require.Equal(t, startStepID, movements[index].StepID,
			"movement[%d] must target startSubAgentStep", index)
	}
	require.Equal(t, loopStepID, movements[3].StepID,
		"final movement must target mainLoopStep")
}

func TestMainInitStep_DefaultsZeroOrNegativeToOne(t *testing.T) {
	step := &mainInitStep{}
	for _, maxConcurrent := range []int{0, -1, -100} {
		decision, err := step.Execute(mainAgentTestCtx(false, nil), mainAgentInitInput{MaxConcurrentSubAgents: maxConcurrent})
		require.NoError(t, err)
		require.Len(t, stepMovements(t, decision), 2, "n=%d should default to 1 + 1 = 2 movements", maxConcurrent)
	}
}

func TestWaitForSubAgentStep_TimerFiredReArmsToSelf(t *testing.T) {
	step := &waitForSubAgentStep{}
	input := waitForSubAgentInput{SubAgentRunID: "subagent-test-stuck"}

	decision, err := step.Execute(mainAgentTestCtx(false, nil), input)
	require.NoError(t, err)
	require.NotNil(t, decision)

	movements := stepMovements(t, decision)
	require.Len(t, movements, 1)
	want := dex.GetFinalStepId(&waitForSubAgentStep{})
	require.Equal(t, want, movements[0].StepID,
		"timer-fired must re-arm by GoTo(self), not advance to startSubAgentStep")
	gotInput, ok := movements[0].Input.(waitForSubAgentInput)
	require.True(t, ok)
	require.Equal(t, input, gotInput, "re-arm must carry the same waitForSubAgentInput")
}

func TestWaitForSubAgentStep_LastResponseAdvancesToStartSubAgent(t *testing.T) {
	step := &waitForSubAgentStep{}
	input := waitForSubAgentInput{SubAgentRunID: "subagent-test-final"}
	response := subAgentResponse{
		SubAgentRunID: input.SubAgentRunID, Counter: 3, Message: "final", IsLast: true,
	}
	channelName := SubAgentResponseCh.Prefix + input.SubAgentRunID
	ctx := mainAgentTestCtx(false, map[string][]any{channelName: {response}})

	decision, err := step.Execute(ctx, input)
	require.NoError(t, err)
	movements := stepMovements(t, decision)
	require.Len(t, movements, 1)

	wantStart := dex.GetFinalStepId(&startSubAgentStep{})
	require.Equal(t, wantStart, movements[0].StepID,
		"final response must advance to startSubAgentStep so the slot can be reused")
}

func TestWaitForSubAgentStep_IntermediateResponseStaysOnWait(t *testing.T) {
	step := &waitForSubAgentStep{}
	input := waitForSubAgentInput{SubAgentRunID: "subagent-test-mid"}
	response := subAgentResponse{
		SubAgentRunID: input.SubAgentRunID, Counter: 1, Message: "mid", IsLast: false,
	}
	channelName := SubAgentResponseCh.Prefix + input.SubAgentRunID
	ctx := mainAgentTestCtx(false, map[string][]any{channelName: {response}})

	decision, err := step.Execute(ctx, input)
	require.NoError(t, err)
	movements := stepMovements(t, decision)
	require.Len(t, movements, 1)

	wantWait := dex.GetFinalStepId(&waitForSubAgentStep{})
	require.Equal(t, wantWait, movements[0].StepID)
}

func TestSubAgentInitStep_SetsCounterAndParent(t *testing.T) {
	step := &subAgentInitStep{}
	ctx := subAgentTestCtx(nil)
	decision, err := step.Execute(ctx, subAgentInitInput{ParentRunID: "main-run-42"})
	require.NoError(t, err)
	require.NotNil(t, decision)

	counter, err := keySubCounter.GetValue(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, counter, "initial counter must be 1 so isLast logic Counter>=3 lands on iter 3")
	parentRunID, err := keySubParentRunID.GetValue(ctx)
	require.NoError(t, err)
	require.Equal(t, "main-run-42", parentRunID)

	movements := stepMovements(t, decision)
	require.Len(t, movements, 1)
	wantLoop := dex.GetFinalStepId(&subAgentLoopStep{})
	require.Equal(t, wantLoop, movements[0].StepID)
}

func TestStartLLMStep_FansOutThreeBranches(t *testing.T) {
	step := &startLLMStep{}
	decision, err := step.Execute(mainAgentTestCtx(false, nil), nil)
	require.NoError(t, err)
	movements := stepMovements(t, decision)
	require.Len(t, movements, 3)

	want := map[string]bool{
		dex.GetFinalStepId(&fastLLMStep{}): false,
		dex.GetFinalStepId(&slowLLMStep{}): false,
		dex.GetFinalStepId(&llmLoopStep{}): false,
	}
	for _, movement := range movements {
		_, ok := want[movement.StepID]
		require.True(t, ok, "unexpected movement target %q", movement.StepID)
		want[movement.StepID] = true
	}
	for stepID, seen := range want {
		require.True(t, seen, "missing fan-out movement to %q", stepID)
	}
}

func TestLLMLoopStep_OnInterruptCancelsBothSiblings(t *testing.T) {
	step := &llmLoopStep{}
	decision, err := step.Execute(mainAgentTestCtx(false, nil), nil)
	require.NoError(t, err)
	require.NotNil(t, decision)

	wantLoop := dex.GetFinalStepId(&mainLoopStep{})
	movements := stepMovements(t, decision)
	require.Len(t, movements, 1)
	require.Equal(t, wantLoop, movements[0].StepID)

	wantFast := dex.GetFinalStepId(&fastLLMStep{})
	wantSlow := dex.GetFinalStepId(&slowLLMStep{})
	require.ElementsMatch(t, []string{wantFast, wantSlow}, stepCancelIDs(t, decision))
}

func TestBuildHumanMessage(t *testing.T) {
	t.Run("start_subagents default num=1", func(t *testing.T) {
		msg, num, err := buildHumanMessage("start_subagents", "")
		require.NoError(t, err)
		assert.Equal(t, msgKindHumanStartSubAgents, msg.Kind)
		assert.Equal(t, 1, msg.NumSubAgents)
		assert.Equal(t, 1, num)
	})
	t.Run("start_subagents explicit num", func(t *testing.T) {
		msg, num, err := buildHumanMessage("start_subagents", "7")
		require.NoError(t, err)
		assert.Equal(t, 7, msg.NumSubAgents)
		assert.Equal(t, 7, num)
	})
	t.Run("start_subagents rejects zero/negative/garbage", func(t *testing.T) {
		for _, raw := range []string{"0", "-1", "abc"} {
			_, _, err := buildHumanMessage("start_subagents", raw)
			require.Error(t, err, "num=%q must be rejected", raw)
		}
	})
	t.Run("start_llm_loop", func(t *testing.T) {
		msg, num, err := buildHumanMessage("start_llm_loop", "")
		require.NoError(t, err)
		assert.Equal(t, msgKindHumanStartLLMLoop, msg.Kind)
		assert.Equal(t, 0, num)
	})
	t.Run("complete", func(t *testing.T) {
		msg, _, err := buildHumanMessage("complete", "")
		require.NoError(t, err)
		assert.Equal(t, msgKindHumanComplete, msg.Kind)
	})
	t.Run("unknown kind rejected", func(t *testing.T) {
		_, _, err := buildHumanMessage("explode", "")
		require.Error(t, err)
	})
}

func TestIsAlreadyExists(t *testing.T) {
	require.False(t, isAlreadyExists(nil))
	require.False(t, isAlreadyExists(http.ErrServerClosed),
		"non-grpc errors must report false (codes.Unknown)")
	require.False(t, isAlreadyExists(context.Canceled))
}
