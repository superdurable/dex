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
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/sdk-go/dex"
)

// A turn with two model calls and a read tool runs RunModel, CallTool, ResumeTurn, RunModel, and
// the next turn replays the stored history.
func TestTurnWithReadToolRunsOneStepPerCall(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	stack.startWorker()
	sessions := stack.sessions(scriptedAgentFlowType)
	ctx := context.Background()

	_, err := sessions.StartSession(ctx, "read-tool", ptr("lookup 7"), dex.StartFlowOptions{})
	require.NoError(t, err)
	snapshot := stack.waitForTurnCount(scriptedAgentFlowType, "read-tool", 1)

	require.Equal(t, []string{"StartAgentSession", "RunModel", "CallTool", "ResumeTurn", "RunModel"},
		stack.executedStepTypes("read-tool"))
	require.Equal(t, []dexagents.MessageRole{
		dexagents.MessageRoleUser, dexagents.MessageRoleAssistant, dexagents.MessageRoleTool, dexagents.MessageRoleAssistant,
	}, messageRoles(snapshot.Messages))
	require.Equal(t, "order 7 is shipped", *snapshot.Messages[2].ToolOutput)
	require.Equal(t, "Order status: order 7 is shipped", *snapshot.Messages[3].Text)
	require.Empty(t, snapshot.ToolCalls, "a finished turn deletes its records")
	require.Equal(t, dexagents.AgentUsage{ModelCalls: 2, InputTokens: 20, OutputTokens: 10}, snapshot.Usage)
	require.Equal(t, 1, stack.callCount("lookup_order"))

	isQueued, err := sessions.SendUserMessage(ctx, "read-tool", "hello again")
	require.NoError(t, err)
	require.True(t, isQueued)
	isQueued, err = sessions.SendUserMessage(ctx, "read-tool", " \t\x1f")
	require.NoError(t, err)
	require.False(t, isQueued, "whitespace-only text, by Python's definition, is refused")
	snapshot = stack.waitForTurnCount(scriptedAgentFlowType, "read-tool", 2)
	require.Len(t, snapshot.Messages, 6)
	require.Equal(t, "You said: hello again", *snapshot.Messages[5].Text)
	require.Equal(t, int64(7), snapshot.State.NextMessageSequence)
	require.Nil(t, snapshot.State.LastTurnError)
	require.False(t, snapshot.State.AcceptsTurnControl)
}

// get_agent_snapshot and list_agent_messages return committed messages; pages walk back to sequence 1.
func TestSnapshotAndMessagePaging(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	stack.startWorker()
	sessions := stack.sessions(scriptedAgentFlowType)
	ctx := context.Background()

	empty, err := sessions.ListAgentMessages(ctx, "paging", nil, 50)
	require.Error(t, err, "a session that was never started has no Flow")
	require.Empty(t, empty.Messages)

	_, err = sessions.StartSession(ctx, "paging", nil, dex.StartFlowOptions{})
	require.NoError(t, err)
	snapshot := stack.waitForSnapshot(scriptedAgentFlowType, "paging", "an initialized session",
		func(snapshot dexagents.AgentSnapshot) bool { return snapshot.State != nil })
	require.Equal(t, dexagents.AgentSessionStatusWaitingForUserMessage, snapshot.State.Status)
	require.Empty(t, snapshot.Messages)
	for turn, text := range []string{"one", "two", "three"} {
		isQueued, err := sessions.SendUserMessage(ctx, "paging", text)
		require.NoError(t, err)
		require.True(t, isQueued)
		stack.waitForTurnCount(scriptedAgentFlowType, "paging", int64(turn+1))
	}

	newest, err := sessions.ListAgentMessages(ctx, "paging", nil, 4)
	require.NoError(t, err)
	require.Equal(t, []int64{3, 4, 5, 6}, messageSequences(newest.Messages))
	require.Equal(t, int64(3), *newest.NextBeforeSequence)
	older, err := sessions.ListAgentMessages(ctx, "paging", newest.NextBeforeSequence, 4)
	require.NoError(t, err)
	require.Equal(t, []int64{1, 2}, messageSequences(older.Messages))
	require.Nil(t, older.NextBeforeSequence)
	clamped, err := sessions.ListAgentMessages(ctx, "paging", nil, 0)
	require.NoError(t, err)
	require.Equal(t, []int64{6}, messageSequences(clamped.Messages), "a limit below 1 is clamped to 1")

	snapshot, err = sessions.GetAgentSnapshot(ctx, "paging")
	require.NoError(t, err)
	require.Equal(t, int64(3), snapshot.State.TurnCount)
	require.Equal(t, []int64{1, 2, 3, 4, 5, 6}, messageSequences(snapshot.Messages))
	require.Equal(t, "turn-3", snapshot.Messages[5].TurnID)
	require.True(t, strings.HasSuffix(snapshot.Messages[0].CreatedAt, "Z"))
	require.Len(t, snapshot.Messages[0].CreatedAt, len("2026-11-01T12:00:00.123Z"))
	require.Zero(t, snapshot.QueuedMessageCount)
	require.Empty(t, snapshot.QueuedMessages)
	require.Nil(t, snapshot.Plan)
}

func messageRoles(messages []dexagents.AgentMessage) []dexagents.MessageRole {
	roles := []dexagents.MessageRole{}
	for _, message := range messages {
		roles = append(roles, message.Role)
	}
	return roles
}

func messageSequences(messages []dexagents.AgentMessage) []int64 {
	sequences := []int64{}
	for _, message := range messages {
		sequences = append(sequences, message.Sequence)
	}
	return sequences
}
