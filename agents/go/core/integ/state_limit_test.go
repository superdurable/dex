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
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/agents/go/core/agentstest"
	"github.com/superdurable/dex/agents/go/core/builtinloop"
	"github.com/superdurable/dex/sdk-go/dex"
)

const (
	stateLimitAgentFlowType = "StateLimitAgent"
	stateLimitBytes         = 131072
	// A stored answer costs about three times its characters: one such answer leaves room for a large write.
	stateLimitFillText = "pad 20000"
	stateLimitWaitTime = 2 * time.Minute
)

var stateLimitReason = regexp.MustCompile(fmt.Sprintf(`^(active )?run state of \d+ bytes exceeds the maximum of %d bytes$`, stateLimitBytes))

// stateLimitCounts counts the model calls and the runs of each tool of the state-limit agent.
type stateLimitCounts struct {
	mutex      sync.Mutex
	modelCalls int
	toolRuns   map[string]int
}

func (counts *stateLimitCounts) countModelCall() {
	counts.mutex.Lock()
	defer counts.mutex.Unlock()
	counts.modelCalls++
}

func (counts *stateLimitCounts) countToolRun(toolName string) {
	counts.mutex.Lock()
	defer counts.mutex.Unlock()
	counts.toolRuns[toolName]++
}

func (counts *stateLimitCounts) modelCallCount() int {
	counts.mutex.Lock()
	defer counts.mutex.Unlock()
	return counts.modelCalls
}

func (counts *stateLimitCounts) toolRunCount(toolName string) int {
	counts.mutex.Lock()
	defer counts.mutex.Unlock()
	return counts.toolRuns[toolName]
}

// newStateLimitAgentStack serves an agent whose "pad <n>" answer or "call <tool> <n> ..." tool results have n characters.
func newStateLimitAgentStack(t *testing.T) (*agentTestStack, *stateLimitCounts) {
	t.Helper()
	stack := newAgentTestStackWithServerZDConfig(t, fmt.Sprintf(
		"values:\n  maxValueBytes: %d\nlimits:\n  maxFlowStateBytes: %d\n", stateLimitBytes, stateLimitBytes))
	counts := &stateLimitCounts{toolRuns: map[string]int{}}
	respond := func(request builtinloop.ModelRequest) (agentstest.ScriptedModelReply, error) {
		counts.countModelCall()
		userTexts := agentstest.UserTexts(request)
		lastText := userTexts[len(userTexts)-1]
		if len(agentstest.ToolResults(request)) > 0 {
			return agentstest.ScriptedModelReply{Text: "done"}, nil
		}
		fields := strings.Fields(lastText)
		switch {
		case len(fields) == 2 && fields[0] == "pad":
			characters, err := strconv.Atoi(fields[1])
			if err != nil {
				return agentstest.ScriptedModelReply{}, err
			}
			return agentstest.ScriptedModelReply{Text: strings.Repeat("x", characters)}, nil
		case len(fields) >= 3 && len(fields)%2 == 1 && fields[0] == "call":
			calls := []agentstest.ScriptedToolCall{}
			for position := 1; position < len(fields); position += 2 {
				calls = append(calls, agentstest.ScriptedToolCall{
					ToolName: fields[position], Arguments: fmt.Sprintf(`{"characters": %s}`, fields[position+1]),
				})
			}
			return toolCalls(calls...), nil
		default:
			return agentstest.ScriptedModelReply{Text: "You said: " + lastText}, nil
		}
	}
	newBulkTool := func(name string, effect dexagents.ToolEffect, isApprovalRequired bool) *builtinloop.FunctionTool {
		tool, err := builtinloop.NewFunctionTool(name, "Return many characters.",
			json.RawMessage(`{"type":"object","properties":{"characters":{"type":"integer"}},"required":["characters"]}`),
			effect, func(_ context.Context, arguments json.RawMessage, _ builtinloop.ToolCallContext) (string, error) {
				var input struct {
					Characters int `json:"characters"`
				}
				if err := json.Unmarshal(arguments, &input); err != nil {
					return "", err
				}
				counts.countToolRun(name)
				return strings.Repeat("y", input.Characters), nil
			}, builtinloop.WithRequiresApproval(isApprovalRequired))
		require.NoError(t, err)
		return tool
	}
	newFlow := func() *dexagents.AgentSessionFlow {
		definition, err := builtinloop.NewAgentDefinition("state-limit", "You answer.",
			agentstest.NewScriptedModelClient(respond), builtinloop.WithFunctionTools(
				newBulkTool("bulk", dexagents.ToolEffectRead, false),
				newBulkTool("bulk_write", dexagents.ToolEffectNonIdempotentWrite, false),
				newBulkTool("approved_write", dexagents.ToolEffectNonIdempotentWrite, true)))
		require.NoError(t, err)
		flow, err := builtinloop.NewBuiltinAgentSessionFlow(definition, stateLimitAgentFlowType)
		require.NoError(t, err)
		return flow
	}
	stack.startInProcessWorker([]dex.Flow{newFlow()})
	stack.addClientFlow(newFlow())
	return stack, counts
}

// waitForFailedSession waits for the session's Flow to close failed and returns its reason.
func (stack *agentTestStack) waitForFailedSession(sessionID string) string {
	stack.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), stateLimitWaitTime)
	defer cancel()
	for {
		result, err := stack.client.WaitForFlow(ctx, sessionID, dex.WaitForFlowOptions{})
		var longPoll *dex.LongPollTimeoutError
		// The engine ends one long poll at its cap; only this test's deadline ends the wait.
		if errors.As(err, &longPoll) || (errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil) {
			continue
		}
		require.NoError(stack.t, err, "the session never closed")
		require.Equal(stack.t, dex.FlowFailed, result.Status)
		require.Regexp(stack.t, stateLimitReason, result.ErrorMessage)
		return result.ErrorMessage
	}
}

func queuedTexts(snapshot dexagents.AgentSnapshot) []string {
	texts := []string{}
	for _, pending := range snapshot.QueuedMessages {
		texts = append(texts, pending.Message.Text)
	}
	return texts
}

// storedSnapshot returns what the session stored, from the get_agent_snapshot RPC alone.
func (stack *agentTestStack) storedSnapshot(sessionID string) dexagents.AgentSnapshot {
	stack.t.Helper()
	var snapshot dexagents.AgentSnapshot
	require.NoError(stack.t, stack.client.InvokeRPC(context.Background(), sessionID,
		stack.flows[stateLimitAgentFlowType].GetAgentSnapshot, nil, &snapshot))
	require.NotNil(stack.t, snapshot.State)
	return snapshot
}

// requireNewMessagesRejected checks that a message to the failed session returns AgentSessionFailedError.
func (stack *agentTestStack) requireNewMessagesRejected(sessionID string, reason string) {
	stack.t.Helper()
	_, err := stack.sessions(stateLimitAgentFlowType).SendUserMessage(context.Background(), sessionID, "say hello")
	var failed *dexagents.AgentSessionFailedError
	require.ErrorAs(stack.t, err, &failed)
	require.Equal(stack.t, sessionID, failed.SessionID)
	require.Equal(stack.t, reason, failed.Reason)
	require.Contains(stack.t, err.Error(), reason)
	var closed *dex.FlowNotActiveOrNotFoundError
	require.ErrorAs(stack.t, err, &closed, "it stays the Dex error for a closed Flow")
}

// requireFailedSession checks what the failure's commit stored and that the session takes no more messages.
func (stack *agentTestStack) requireFailedSession(sessionID string, reason string) dexagents.AgentSnapshot {
	stack.t.Helper()
	snapshot := stack.storedSnapshot(sessionID)
	require.Equal(stack.t, dexagents.AgentSessionStatusFailed, snapshot.State.Status)
	require.NotNil(stack.t, snapshot.State.LastTurnError)
	require.Equal(stack.t, reason, *snapshot.State.LastTurnError)
	require.Nil(stack.t, snapshot.State.ActiveTurnID)
	require.Empty(stack.t, snapshot.PendingInterrupts)
	var frameworkRunState string
	result, err := stack.client.GetAttributes(context.Background(), sessionID,
		dex.ReadAttribute(dexagents.FrameworkRunStateAttribute, &frameworkRunState))
	require.NoError(stack.t, err)
	require.False(stack.t, result.IsSet(dexagents.FrameworkRunStateAttribute), "the failure deletes FrameworkRunState")
	read, err := stack.sessions(stateLimitAgentFlowType).GetAgentSnapshot(context.Background(), sessionID)
	require.NoError(stack.t, err)
	require.Equal(stack.t, snapshot, read, "the Flow's close adds nothing to a stored failure")
	stack.requireNewMessagesRejected(sessionID, reason)
	events := stack.eventsUntil(stateLimitAgentFlowType, sessionID, func(event dexagents.AgentEvent) bool {
		return event.Type == string(dexagents.AgentEventTypeTurnFailed)
	})
	payload := map[string]any{}
	require.NoError(stack.t, json.Unmarshal([]byte(events[len(events)-1].Payload), &payload))
	require.Equal(stack.t, map[string]any{"error": reason}, payload)
	return snapshot
}

// A model answer over limits.maxFlowStateBytes ends the session in RecordTurnFailure; queued messages stay queued.
func TestAModelAnswerOverTheStateLimitFailsTheSession(t *testing.T) {
	t.Parallel()
	stack, counts := newStateLimitAgentStack(t)
	sessions := stack.sessions(stateLimitAgentFlowType)
	_, err := sessions.StartSession(context.Background(), "answer", nil, dex.StartFlowOptions{})
	require.NoError(t, err)
	queued := []string{"pad 30000", "pad 30001", "pad 30002", "pad 30003", "pad 30004"}
	for _, text := range queued {
		isQueued, err := sessions.SendUserMessage(context.Background(), "answer", text)
		require.NoError(t, err)
		require.True(t, isQueued)
	}

	reason := stack.waitForFailedSession("answer")

	snapshot := stack.requireFailedSession("answer", reason)
	require.Contains(t, stack.executedStepTypes("answer"), "RecordTurnFailure")
	turnCount := int(snapshot.State.TurnCount)
	require.GreaterOrEqual(t, turnCount, 2, "the first answer fits")
	// Only the model call over the limit was lost, and no queued message ran.
	require.Equal(t, turnCount, counts.modelCallCount())
	require.Equal(t, queued[turnCount:], queuedTexts(snapshot))
	require.Equal(t, int64(len(queued)-turnCount), snapshot.QueuedMessageCount)
}

// A tool result over the limit ends the session in the tool Step's failure route.
func TestAToolResultOverTheStateLimitFailsTheSession(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct{ toolName, recoveryStep string }{
		{"bulk", "RecordToolFailure"},
		{"bulk_write", "RecordToolOutcomeUnknown"},
	} {
		t.Run(testCase.recoveryStep, func(t *testing.T) {
			t.Parallel()
			stack, counts := newStateLimitAgentStack(t)
			sessions := stack.sessions(stateLimitAgentFlowType)
			_, err := sessions.StartSession(context.Background(), "tool", ptr(stateLimitFillText), dex.StartFlowOptions{})
			require.NoError(t, err)
			stack.waitForTurnCount(stateLimitAgentFlowType, "tool", 1)
			modelCallsBefore := counts.modelCallCount()
			for _, text := range []string{"call " + testCase.toolName + " 70000", "say after the tool"} {
				isQueued, err := sessions.SendUserMessage(context.Background(), "tool", text)
				require.NoError(t, err)
				require.True(t, isQueued)
			}

			reason := stack.waitForFailedSession("tool")

			snapshot := stack.requireFailedSession("tool", reason)
			require.True(t, slices.ContainsFunc(stack.stepHistory("tool"), func(entry agentstest.StepHistoryEntry) bool {
				return entry.EventType == agentstest.ExecuteCompletedEvent && entry.StepType == testCase.recoveryStep
			}), "the failure route ends the session")
			require.Equal(t, 1, counts.toolRunCount(testCase.toolName))
			require.Equal(t, modelCallsBefore+1, counts.modelCallCount(), "the model never sees a failed tool call")
			require.Equal(t, int64(2), snapshot.State.TurnCount, "the failed turn counts")
			require.Equal(t, []string{"say after the tool"}, queuedTexts(snapshot))
		})
	}
}

// A read over the limit while a write waits for approval ends the session with no pending interrupt.
func TestAToolResultOverTheStateLimitClearsPendingApprovals(t *testing.T) {
	t.Parallel()
	stack, counts := newStateLimitAgentStack(t)
	sessions := stack.sessions(stateLimitAgentFlowType)
	_, err := sessions.StartSession(context.Background(), "approval", ptr(stateLimitFillText), dex.StartFlowOptions{})
	require.NoError(t, err)
	stack.waitForTurnCount(stateLimitAgentFlowType, "approval", 1)
	isQueued, err := sessions.SendUserMessage(context.Background(), "approval", "call approved_write 1 bulk 70000")
	require.NoError(t, err)
	require.True(t, isQueued)

	reason := stack.waitForFailedSession("approval")

	stack.requireFailedSession("approval", reason)
	events := stack.eventsUntil(stateLimitAgentFlowType, "approval", func(event dexagents.AgentEvent) bool {
		return event.Type == string(dexagents.AgentEventTypeTurnFailed)
	})
	interruptID := ""
	for _, event := range events {
		if event.Type == string(dexagents.AgentEventTypeInterruptRaised) {
			payload := map[string]any{}
			require.NoError(t, json.Unmarshal([]byte(event.Payload), &payload))
			interruptID, _ = payload["interrupt_id"].(string)
		}
	}
	require.NotEmpty(t, interruptID, "the write waited for approval when the session failed")
	_, err = sessions.ResolveInterrupt(context.Background(), "approval", interruptID, true, nil)
	require.Error(t, err, "a failed session resolves no interrupt")
	require.Zero(t, counts.toolRunCount("approved_write"))
	require.Equal(t, 1, counts.toolRunCount("bulk"))
}

// AwaitUserMessage has no failure route: storing a queued message over the limit closes the Flow, AgentState unchanged.
func TestAUserMessageStoredOverTheStateLimitFailsTheSession(t *testing.T) {
	t.Parallel()
	stack, counts := newStateLimitAgentStack(t)
	sessions := stack.sessions(stateLimitAgentFlowType)
	_, err := sessions.StartSession(context.Background(), "message", ptr(stateLimitFillText), dex.StartFlowOptions{})
	require.NoError(t, err)
	stack.waitForTurnCount(stateLimitAgentFlowType, "message", 1)
	stack.sendUserMessage(stateLimitAgentFlowType, "message", "pad 12000")
	stack.waitForTurnCount(stateLimitAgentFlowType, "message", 2)
	modelCallsBefore := counts.modelCallCount()
	// Queueing the message fits; AwaitUserMessage storing it as well does not.
	isQueued, err := sessions.SendUserMessage(context.Background(), "message", "say "+strings.Repeat("u", 20000))
	require.NoError(t, err)
	require.True(t, isQueued)

	reason := stack.waitForFailedSession("message")

	stored := stack.storedSnapshot("message")
	require.Equal(t, dexagents.AgentSessionStatusWaitingForUserMessage, stored.State.Status, "the failing Step stored nothing")
	require.Nil(t, stored.State.LastTurnError)
	require.NotContains(t, stack.executedStepTypes("message"), "RecordTurnFailure")
	snapshot, err := sessions.GetAgentSnapshot(context.Background(), "message")
	require.NoError(t, err)
	require.Equal(t, dexagents.AgentSessionStatusFailed, snapshot.State.Status)
	require.NotNil(t, snapshot.State.LastTurnError)
	require.Equal(t, reason, *snapshot.State.LastTurnError)
	require.Nil(t, snapshot.State.ActiveTurnID)
	require.Equal(t, int64(2), snapshot.State.TurnCount)
	require.Empty(t, snapshot.PendingInterrupts)
	require.Empty(t, snapshot.ToolCalls)
	require.Equal(t, stored.Messages, snapshot.Messages)
	require.Equal(t, modelCallsBefore, counts.modelCallCount(), "no turn started")
	stack.requireNewMessagesRejected("message", reason)

	// A session that does not exist keeps the Dex error.
	_, err = sessions.SendUserMessage(context.Background(), "no-such-session", "say hello")
	var closed *dex.FlowNotActiveOrNotFoundError
	require.ErrorAs(t, err, &closed)
	var failed *dexagents.AgentSessionFailedError
	require.False(t, errors.As(err, &failed))
}

// startNoRoomSession queues two large answers and many small tool turns, all while there is room for them.
func startNoRoomSession(sessions *dexagents.AgentSessionClient, sessionID string, firstAnswerCharacters int) error {
	if _, err := sessions.StartSession(context.Background(), sessionID, nil, dex.StartFlowOptions{}); err != nil {
		return err
	}
	texts := []string{"pad 15000", fmt.Sprintf("pad %d", firstAnswerCharacters)}
	for range 40 {
		texts = append(texts, "call bulk 40")
	}
	for _, text := range texts {
		isQueued, err := sessions.SendUserMessage(context.Background(), sessionID, text)
		if err != nil {
			return err
		}
		if !isQueued {
			return fmt.Errorf("session %s did not queue %q", sessionID, text)
		}
	}
	return nil
}

// Port of the Python and TypeScript test: some sessions lack room for the failure's own commit.
func TestASessionWithNoRoomToStoreItsFailureStillReportsIt(t *testing.T) {
	t.Parallel()
	stack, _ := newStateLimitAgentStack(t)
	sessions := stack.sessions(stateLimitAgentFlowType)
	const sessionCount = 24
	startErrors := make([]error, sessionCount)
	var group sync.WaitGroup
	for offset := range sessionCount {
		group.Go(func() {
			startErrors[offset] = startNoRoomSession(sessions, fmt.Sprintf("no-room-%d", offset), 15000+37*offset)
		})
	}
	group.Wait()

	storedActiveTurns := 0
	for offset := range sessionCount {
		require.NoError(t, startErrors[offset])
		sessionID := fmt.Sprintf("no-room-%d", offset)
		reason := stack.waitForFailedSession(sessionID)
		snapshot, err := sessions.GetAgentSnapshot(context.Background(), sessionID)
		require.NoError(t, err)
		require.Equal(t, dexagents.AgentSessionStatusFailed, snapshot.State.Status)
		require.NotNil(t, snapshot.State.LastTurnError)
		require.Equal(t, reason, *snapshot.State.LastTurnError)
		require.Nil(t, snapshot.State.ActiveTurnID)
		require.Empty(t, snapshot.PendingInterrupts)
		require.Empty(t, snapshot.ToolCalls)
		stack.requireNewMessagesRejected(sessionID, reason)
		if stored := stack.storedSnapshot(sessionID); stored.State.ActiveTurnID != nil {
			storedActiveTurns++
			require.Equal(t, stored.State.TurnCount+1, snapshot.State.TurnCount, "the running turn counts")
		}
	}

	// A Step without a failure route, or a route whose commit did not fit, left its turn active.
	require.Positive(t, storedActiveTurns)
}
