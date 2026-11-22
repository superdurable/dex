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
	"testing"

	"github.com/stretchr/testify/require"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/agents/go/core/agentstest"
	"github.com/superdurable/dex/sdk-go/dex"
)

// runModelAttempts returns the final attempt of every RunModel Step of a session, in order.
func (stack *agentTestStack) runModelAttempts(sessionID string) []int {
	stack.t.Helper()
	attempts := []int{}
	for _, entry := range stack.stepHistory(sessionID) {
		if entry.EventType == agentstest.ExecuteCompletedEvent && entry.StepType == "RunModel" {
			attempts = append(attempts, entry.FinalAttempt)
		}
	}
	return attempts
}

// A Handler that calls the model twice ends the turn as failed in its one RunModel attempt, with one
// model call billed; the next turn completes and clears the error.
func TestSecondModelCallEndsTheTurnAsFailedWithOneModelCallBilled(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	stack.startWorker()
	sessions := stack.sessions(einoGuardedAgentFlowType)
	ctx := context.Background()

	_, err := sessions.StartSession(ctx, "twice", ptr("call twice"), dex.StartFlowOptions{})
	require.NoError(t, err)
	snapshot := stack.waitForTurnCount(einoGuardedAgentFlowType, "twice", 1)
	require.NotNil(t, snapshot.State.LastTurnError)
	require.Contains(t, *snapshot.State.LastTurnError, "the Eino agent tried a second model call in one RunModel Step")
	require.Equal(t, int64(1), snapshot.Usage.ModelCalls, "the one model call is billed")
	require.Equal(t, 1, stack.callCount("model"), "the second call never reaches the model, and nothing retries")
	require.Equal(t, []string{"StartAgentSession", "RunModel"}, stack.executedStepTypes("twice"))
	require.Equal(t, []int{1}, stack.runModelAttempts("twice"))
	require.Equal(t, []dexagents.MessageRole{dexagents.MessageRoleUser}, messageRoles(snapshot.Messages))

	isQueued, err := sessions.SendUserMessage(ctx, "twice", "hello")
	require.NoError(t, err)
	require.True(t, isQueued)
	snapshot = stack.waitForTurnCount(einoGuardedAgentFlowType, "twice", 2)
	require.Nil(t, snapshot.State.LastTurnError, "a completed turn clears the error")
	require.Equal(t, "You said: hello", *snapshot.Messages[len(snapshot.Messages)-1].Text)
}

// A turn at the agent's MaxIterations ends as failed without a model call or a retry, and stores the
// tool result, so the stored call keeps its result.
func TestMaxIterationsEndsTheTurnAsFailedAndStoresTheToolResult(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	stack.startWorker()
	sessions := stack.sessions(einoGuardedAgentFlowType)

	_, err := sessions.StartSession(context.Background(), "limit", ptr("lookup 5"), dex.StartFlowOptions{})
	require.NoError(t, err)
	snapshot := stack.waitForTurnCount(einoGuardedAgentFlowType, "limit", 1)
	require.NotNil(t, snapshot.State.LastTurnError)
	require.Equal(t, "the turn reached the Eino agent's MaxIterations of 1 model calls", *snapshot.State.LastTurnError)
	require.Equal(t, []string{"StartAgentSession", "RunModel", "CallTool", "ResumeTurn", "RunModel"}, stack.executedStepTypes("limit"))
	require.Equal(t, []int{1, 1}, stack.runModelAttempts("limit"), "no RunModel attempt is retried")
	require.Equal(t, []dexagents.MessageRole{dexagents.MessageRoleUser, dexagents.MessageRoleAssistant, dexagents.MessageRoleTool},
		messageRoles(snapshot.Messages))
	require.Equal(t, "order 5 is shipped", *snapshot.Messages[2].ToolOutput)
	require.Equal(t, int64(1), snapshot.Usage.ModelCalls)
	require.Equal(t, 1, stack.callCount("model"))
	require.Equal(t, 1, stack.callCount("lookup_order"))
}
