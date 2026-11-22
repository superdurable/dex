// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package integ

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/sdk-go/dex"
)

const (
	outcomeAgentFlowType = "OutcomeAgent"
	deliberateStopText   = "the runner's limit of one model call per turn was reached"
)

// outcomeRunnerUsage is the usage outcomeRunner reports for each model call.
var outcomeRunnerUsage = dexagents.AgentUsage{ModelCalls: 1, InputTokens: 10, OutputTokens: 5}

// outcomeRunner returns outcomes that the built-in loop never returns, chosen by the last user text.
type outcomeRunner struct {
	mutex      sync.Mutex
	modelCalls map[string]int
	toolRuns   int
}

func newOutcomeRunner() *outcomeRunner {
	return &outcomeRunner{modelCalls: map[string]int{}}
}

func outcomeMessage(role dexagents.MessageRole, text string) dexagents.FrameworkMessage {
	item, err := json.Marshal(map[string]string{"role": string(role), "text": text})
	if err != nil {
		panic(err)
	}
	return dexagents.FrameworkMessage{Role: role, Text: &text, FrameworkItem: string(item)}
}

func (runner *outcomeRunner) BuildUserMessage(text string) (dexagents.FrameworkMessage, error) {
	return outcomeMessage(dexagents.MessageRoleUser, text), nil
}

func (runner *outcomeRunner) RunModelStep(_ context.Context, turn dexagents.AgentTurnContext) (dexagents.AgentTurnOutcome, error) {
	text := ""
	for _, message := range turn.Messages {
		if message.Role == dexagents.MessageRoleUser && message.Text != nil {
			text = *message.Text
		}
	}
	runner.mutex.Lock()
	runner.modelCalls[text]++
	runner.mutex.Unlock()
	if turn.IsResuming() {
		return &dexagents.TurnCompleted{
			NewMessages: []dexagents.FrameworkMessage{outcomeMessage(dexagents.MessageRoleAssistant, "done after the tool")},
			Usage:       outcomeRunnerUsage,
		}, nil
	}
	switch text {
	case "stop here":
		return &dexagents.TurnCompleted{
			NewMessages: []dexagents.FrameworkMessage{outcomeMessage(dexagents.MessageRoleAssistant, "I stopped on purpose")},
			Usage:       outcomeRunnerUsage,
			TurnError:   ptr(deliberateStopText),
		}, nil
	case "stop with a blank reason":
		return &dexagents.TurnCompleted{
			NewMessages: []dexagents.FrameworkMessage{outcomeMessage(dexagents.MessageRoleAssistant, "I stopped")},
			Usage:       outcomeRunnerUsage,
			TurnError:   ptr(" \t"),
		}, nil
	case "answer without a role":
		return &dexagents.TurnCompleted{
			NewMessages: []dexagents.FrameworkMessage{outcomeMessage("", "an answer without a role")},
			Usage:       outcomeRunnerUsage,
		}, nil
	case "call a tool with a bad role":
		readEffect := dexagents.ToolEffectRead
		return &dexagents.ToolCallsRequested{
			FrameworkRunState: `{"model_calls":1}`,
			ToolCalls: []dexagents.DeferredToolCall{{
				ToolCallID: "call-1", ToolName: "lookup", Arguments: "{}",
				Policy: turn.ResolveToolPolicy("lookup", &readEffect),
			}},
			Usage:       outcomeRunnerUsage,
			NewMessages: []dexagents.FrameworkMessage{outcomeMessage("bot", "I call lookup")},
		}, nil
	default:
		return &dexagents.TurnCompleted{
			NewMessages: []dexagents.FrameworkMessage{outcomeMessage(dexagents.MessageRoleAssistant, "You said: "+text)},
			Usage:       outcomeRunnerUsage,
		}, nil
	}
}

func (runner *outcomeRunner) ExecuteDeferredToolCall(context.Context, dexagents.DeferredToolExecution) (dexagents.DeferredToolOutcome, error) {
	runner.mutex.Lock()
	runner.toolRuns++
	runner.mutex.Unlock()
	return &dexagents.ToolExecutionResult{Output: "looked up"}, nil
}

// BuildUnfinishedTurnMessages stores nothing more; it lets RunModelStep set ToolCallsRequested.NewMessages.
func (runner *outcomeRunner) BuildUnfinishedTurnMessages(context.Context, dexagents.AgentTurnContext) ([]dexagents.FrameworkMessage, error) {
	return nil, nil
}

func (runner *outcomeRunner) modelCallCount(text string) int {
	runner.mutex.Lock()
	defer runner.mutex.Unlock()
	return runner.modelCalls[text]
}

func (runner *outcomeRunner) toolRunCount() int {
	runner.mutex.Lock()
	defer runner.mutex.Unlock()
	return runner.toolRuns
}

// newOutcomeAgentStack serves outcomeRunner's Flow from this process.
func newOutcomeAgentStack(t *testing.T) (*agentTestStack, *outcomeRunner) {
	t.Helper()
	stack := newAgentTestStack(t)
	runner := newOutcomeRunner()
	newFlow := func() *dexagents.AgentSessionFlow {
		flow, err := dexagents.NewAgentSessionFlow(runner, outcomeAgentFlowType)
		require.NoError(t, err)
		return flow
	}
	stack.startInProcessWorker([]dex.Flow{newFlow()})
	stack.addClientFlow(newFlow())
	return stack, runner
}

// eventsUntil reads the session's AgentEvents from the start until isLast accepts one, and returns them all.
func (stack *agentTestStack) eventsUntil(flowType string, sessionID string, isLast func(dexagents.AgentEvent) bool) []dexagents.AgentEvent {
	stack.t.Helper()
	sessions := stack.sessions(flowType)
	events := []dexagents.AgentEvent{}
	resumeToken := ""
	for {
		readContext, cancel := context.WithTimeout(context.Background(), sessionConvergenceTimeout)
		event, message, err := sessions.ReadAgentEvent(readContext, sessionID, resumeToken)
		cancel()
		require.NoError(stack.t, err)
		resumeToken = message.ResumeToken
		events = append(events, event)
		if isLast(event) {
			return events
		}
	}
}

func eventTypesOfTurn(events []dexagents.AgentEvent, turnID string) []string {
	types := []string{}
	for _, event := range events {
		if event.TurnID != nil && *event.TurnID == turnID {
			types = append(types, event.Type)
		}
	}
	return types
}

// TurnCompleted.TurnError ends the turn as failed in RunModel: the answer and usage are stored, no
// retry or RecordTurnFailure runs, turn_failed replaces turn_completed, and the next turn clears it.
func TestTurnErrorEndsTheTurnAsFailedWithoutARetry(t *testing.T) {
	t.Parallel()
	stack, runner := newOutcomeAgentStack(t)
	sessions := stack.sessions(outcomeAgentFlowType)
	ctx := context.Background()

	_, err := sessions.StartSession(ctx, "turn-error", ptr("stop here"), dex.StartFlowOptions{})
	require.NoError(t, err)
	snapshot := stack.waitForTurnCount(outcomeAgentFlowType, "turn-error", 1)

	require.NotNil(t, snapshot.State.LastTurnError)
	require.Equal(t, deliberateStopText, *snapshot.State.LastTurnError)
	require.Equal(t, dexagents.AgentSessionStatusWaitingForUserMessage, snapshot.State.Status)
	require.Equal(t, []dexagents.MessageRole{dexagents.MessageRoleUser, dexagents.MessageRoleAssistant},
		messageRoles(snapshot.Messages))
	require.Equal(t, "I stopped on purpose", *snapshot.Messages[1].Text)
	require.Equal(t, outcomeRunnerUsage, snapshot.Usage)
	require.Equal(t, 1, runner.modelCallCount("stop here"))
	require.Equal(t, []string{"StartAgentSession", "RunModel"}, stack.executedStepTypes("turn-error"))

	isQueued, err := sessions.SendUserMessage(ctx, "turn-error", "hello")
	require.NoError(t, err)
	require.True(t, isQueued)
	snapshot = stack.waitForTurnCount(outcomeAgentFlowType, "turn-error", 2)
	require.Nil(t, snapshot.State.LastTurnError)
	require.Equal(t, "You said: hello", *snapshot.Messages[3].Text)

	events := stack.eventsUntil(outcomeAgentFlowType, "turn-error", func(event dexagents.AgentEvent) bool {
		return event.Type == string(dexagents.AgentEventTypeTurnCompleted)
	})
	require.Equal(t, []string{"turn_started", "usage_recorded", "turn_failed"}, eventTypesOfTurn(events, "turn-1"))
	require.Equal(t, []string{"turn_started", "usage_recorded", "turn_completed"}, eventTypesOfTurn(events, "turn-2"))
	for _, event := range events {
		if event.Type == string(dexagents.AgentEventTypeTurnFailed) {
			require.JSONEq(t, `{"error": "`+deliberateStopText+`"}`, event.Payload)
		}
	}
}

// An outcome that the session must not store fails its turn in RunModel, with no paid retry and
// nothing of it stored but its usage, and the session keeps working.
func TestOutcomeTheSessionMustNotStoreFailsTheTurnAtOnce(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name          string
		userText      string
		expectedError string
	}{
		{"TurnCompletedWithoutARole", "answer without a role",
			`TurnCompleted.NewMessages[0] has role "", which is not user, assistant, or tool`},
		{"ToolCallsRequestedWithAnUnknownRole", "call a tool with a bad role",
			`ToolCallsRequested.NewMessages[0] has role "bot", which is not user, assistant, or tool`},
		{"BlankTurnError", "stop with a blank reason", "TurnCompleted.TurnError for turn-1 must be a non-blank string"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			stack, runner := newOutcomeAgentStack(t)
			sessions := stack.sessions(outcomeAgentFlowType)
			ctx := context.Background()
			sessionID := testCase.name
			_, err := sessions.StartSession(ctx, sessionID, ptr(testCase.userText), dex.StartFlowOptions{})
			require.NoError(t, err)
			snapshot := stack.waitForTurnCount(outcomeAgentFlowType, sessionID, 1)

			require.NotNil(t, snapshot.State.LastTurnError)
			require.Equal(t, testCase.expectedError, *snapshot.State.LastTurnError)
			require.Equal(t, []dexagents.MessageRole{dexagents.MessageRoleUser}, messageRoles(snapshot.Messages))
			require.Empty(t, snapshot.ToolCalls)
			require.Equal(t, outcomeRunnerUsage, snapshot.Usage, "the paid model call is recorded")
			require.Equal(t, 1, runner.modelCallCount(testCase.userText), "a retry would repeat the paid model call")
			require.Equal(t, []string{"StartAgentSession", "RunModel"}, stack.executedStepTypes(sessionID))
			page, err := sessions.ListAgentMessages(ctx, sessionID, nil, 50)
			require.NoError(t, err)
			require.Len(t, page.Messages, 1)

			isQueued, err := sessions.SendUserMessage(ctx, sessionID, "hello")
			require.NoError(t, err)
			require.True(t, isQueued)
			snapshot = stack.waitForTurnCount(outcomeAgentFlowType, sessionID, 2)
			require.Nil(t, snapshot.State.LastTurnError)
			require.Equal(t, "You said: hello", *snapshot.Messages[len(snapshot.Messages)-1].Text)
			require.Zero(t, runner.toolRunCount(), "a rejected ToolCallsRequested starts no tool Step")
		})
	}
}
