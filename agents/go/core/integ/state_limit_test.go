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

// newStateLimitAgentStack serves a built-in loop agent on an engine whose limits.maxFlowStateBytes is
// stateLimitBytes. "pad <n>" answers n characters, and "call <tool> <n>" calls the read tool bulk or
// the single-attempt write bulk_write, which returns n characters.
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
		case len(fields) == 3 && fields[0] == "call":
			return toolCalls(agentstest.ScriptedToolCall{
				ToolName: fields[1], Arguments: fmt.Sprintf(`{"characters": %s}`, fields[2]),
			}), nil
		default:
			return agentstest.ScriptedModelReply{Text: "You said: " + lastText}, nil
		}
	}
	newBulkTool := func(name string, effect dexagents.ToolEffect) *builtinloop.FunctionTool {
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
			}, builtinloop.WithRequiresApproval(false))
		require.NoError(t, err)
		return tool
	}
	newFlow := func() *dexagents.AgentSessionFlow {
		definition, err := builtinloop.NewAgentDefinition("state-limit", "You answer.",
			agentstest.NewScriptedModelClient(respond), builtinloop.WithFunctionTools(
				newBulkTool("bulk", dexagents.ToolEffectRead),
				newBulkTool("bulk_write", dexagents.ToolEffectNonIdempotentWrite)))
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

// requireFailedSession checks what the failure's commit stored and that the session takes no more messages.
func (stack *agentTestStack) requireFailedSession(sessionID string, reason string) dexagents.AgentSnapshot {
	stack.t.Helper()
	snapshot, err := stack.sessions(stateLimitAgentFlowType).GetAgentSnapshot(context.Background(), sessionID)
	require.NoError(stack.t, err)
	require.Equal(stack.t, dexagents.AgentSessionStatusFailed, snapshot.State.Status)
	require.NotNil(stack.t, snapshot.State.LastTurnError)
	require.Equal(stack.t, reason, *snapshot.State.LastTurnError)
	require.Nil(stack.t, snapshot.State.ActiveTurnID)
	require.Empty(stack.t, snapshot.PendingInterrupts)
	_, err = stack.sessions(stateLimitAgentFlowType).SendUserMessage(context.Background(), sessionID, "say hello")
	require.Error(stack.t, err, "a failed session takes no more messages")
	events := stack.eventsUntil(stateLimitAgentFlowType, sessionID, func(event dexagents.AgentEvent) bool {
		return event.Type == string(dexagents.AgentEventTypeTurnFailed)
	})
	payload := map[string]any{}
	require.NoError(stack.t, json.Unmarshal([]byte(events[len(events)-1].Payload), &payload))
	require.Equal(stack.t, map[string]any{"error": reason}, payload)
	return snapshot
}

// A model answer that would carry the session past limits.maxFlowStateBytes ends the session in
// RecordTurnFailure with the reason, instead of failing every later turn. Queued messages stay queued.
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
	// Only the one paid model call over the limit was lost; the session did not go on to the queued messages.
	require.Equal(t, turnCount, counts.modelCallCount())
	require.Equal(t, queued[turnCount:], queuedTexts(snapshot))
	require.Equal(t, int64(len(queued)-turnCount), snapshot.QueuedMessageCount)
}

// A tool result that would carry the session past the limit ends the session in the tool Step's
// failure route; the model never sees a failed call it could make again.
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
