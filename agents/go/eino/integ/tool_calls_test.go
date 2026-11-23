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

// A tool the model invents fails its own tool Step as an unknown tool; the turn continues.
func TestHallucinatedToolIsAnsweredAsUnknown(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	stack.startWorker()
	sessions := stack.sessions(einoAgentFlowType)

	_, err := sessions.StartSession(context.Background(), "ghost", ptr("hallucinate"), dex.StartFlowOptions{})
	require.NoError(t, err)
	snapshot := stack.waitForTurnCount(einoAgentFlowType, "ghost", 1)
	require.Nil(t, snapshot.State.LastTurnError)
	require.Equal(t, "Ghost: The tool call failed: unknown tool ghost_tool", *snapshot.Messages[3].Text)
	require.Equal(t, []string{"StartAgentSession", "RunModel", "CallTool", "ResumeTurn", "RunModel"}, stack.executedStepTypes("ghost"))
	require.Equal(t, int64(2), snapshot.Usage.ModelCalls, "no paid retry")
}

// A first attempt whose arguments break the tool's schema is a definite failure the model reads; no tool code runs.
func TestArgumentsTheSchemaRejectsAreAFailedCallTheModelSees(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	stack.startWorker()
	sessions := stack.sessions(einoAgentFlowType)

	_, err := sessions.StartSession(context.Background(), "numeric-id", ptr("numeric order id"), dex.StartFlowOptions{})
	require.NoError(t, err)
	snapshot := stack.waitForTurnCount(einoAgentFlowType, "numeric-id", 1)
	require.Nil(t, snapshot.State.LastTurnError)
	// "The tool call failed: " is the result text of a failed call, unlike outcome_unknown's.
	require.Equal(t, "Lookup: The tool call failed: invalid arguments for lookup_order: arguments.order_id must be string",
		*snapshot.Messages[3].Text)
	require.Zero(t, stack.callCount("lookup_order")+stack.callCount("middleware")+stack.callCount("handler"))
	require.Equal(t, 1, stack.finalAttempt("numeric-id", "CallTool"), "a refusal is not retried")
}

// Empty, "/"-containing, and duplicate tool call IDs get valid unique IDs before they are stored.
func TestInvalidToolCallIDsAreReplacedBeforeTheyAreStored(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	stack.startWorker()
	sessions := stack.sessions(einoAgentFlowType)

	_, err := sessions.StartSession(context.Background(), "ids", ptr("bad ids"), dex.StartFlowOptions{})
	require.NoError(t, err)
	snapshot := stack.waitForTurnCount(einoAgentFlowType, "ids", 1)
	require.Nil(t, snapshot.State.LastTurnError)
	require.Equal(t, int64(2), snapshot.Usage.ModelCalls, "the IDs never cost a retried model call")
	final := *snapshot.Messages[len(snapshot.Messages)-1].Text
	require.True(t, strings.HasPrefix(final, "IDs: "), final)
	seen := map[string]bool{}
	for _, pair := range strings.Split(strings.TrimPrefix(final, "IDs: "), "; ") {
		id, output, _ := strings.Cut(pair, "=")
		require.NotEmpty(t, id)
		require.NotContains(t, id, "/")
		require.False(t, seen[id], "duplicate ID %s", id)
		seen[id] = true
		require.Contains(t, output, "is shipped")
	}
	require.Len(t, seen, 4)
	require.True(t, seen["dup"], "the first of two equal IDs keeps it")
	require.Equal(t, 4, stack.callCount("lookup_order"))
}

// A tool reads its call ID with compose.GetToolCallID and the Dex call inside its tool Step, and the
// tool middlewares and Handler tool wrappers run once, there.
func TestToolStepGivesTheToolItsCallAndRunsMiddlewaresOnce(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	stack.startWorker()
	sessions := stack.sessions(einoAgentFlowType)

	_, err := sessions.StartSession(context.Background(), "echo", ptr("echo"), dex.StartFlowOptions{})
	require.NoError(t, err)
	snapshot := stack.waitForTurnCount(einoAgentFlowType, "echo", 1)
	require.Equal(t, "Echo: id=call_echo key=echo/turn-1/call_echo", *snapshot.Messages[3].Text)
	require.Equal(t, 1, stack.callCount("middleware"))
	require.Equal(t, 1, stack.callCount("handler"))
}

// A tool's *dexagents.ToolExecutionError, wrapped in its own error, reaches the model as a failure.
func TestTypedToolExecutionErrorIsADefiniteFailure(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	stack.startWorker()
	sessions := stack.sessions(einoAgentFlowType)

	_, err := sessions.StartSession(context.Background(), "typed", ptr("typed failure"), dex.StartFlowOptions{})
	require.NoError(t, err)
	snapshot := stack.waitForTurnCount(einoAgentFlowType, "typed", 1)
	require.Equal(t, "Typed: The tool call failed: order 9 is already shipped", *snapshot.Messages[3].Text)
	require.Equal(t, 1, stack.callCount("typed_failure"), "a definite failure is not retried")
}

// A plain error from a non_idempotent_write fails its only attempt, so the call's outcome is unknown.
func TestPlainErrorOfASingleAttemptWriteRecordsOutcomeUnknown(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	stack.startWorker()
	sessions := stack.sessions(einoAgentFlowType)

	_, err := sessions.StartSession(context.Background(), "plain", ptr("plain failure"), dex.StartFlowOptions{})
	require.NoError(t, err)
	snapshot := stack.waitForTurnCount(einoAgentFlowType, "plain", 1)
	require.Equal(t, dexagents.OutcomeUnknownToolResult, *snapshot.Messages[2].ToolOutput)
	require.Equal(t, 1, stack.callCount("plain_failure"))
	require.Contains(t, stack.executedStepTypes("plain"), "RecordToolOutcomeUnknown")
}

// The builder chooses each model call's model from the turn's session settings.
func TestSessionSettingsChooseTheModel(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	stack.startWorker()
	sessions := stack.sessions(einoSettingsAgentFlowType)
	ctx := context.Background()

	_, err := sessions.StartSessionWithSettings(ctx, "settings", ptr("which model"), map[string]any{"model": "beta"}, dex.StartFlowOptions{})
	require.NoError(t, err)
	snapshot := stack.waitForTurnCount(einoSettingsAgentFlowType, "settings", 1)
	require.Equal(t, "model beta: You are the settings agent.", *snapshot.Messages[1].Text)

	isUpdated, err := sessions.UpdateSessionSettings(ctx, "settings", map[string]any{"model": "alpha"})
	require.NoError(t, err)
	require.True(t, isUpdated)
	isQueued, err := sessions.SendUserMessage(ctx, "settings", "which model now")
	require.NoError(t, err)
	require.True(t, isQueued)
	snapshot = stack.waitForTurnCount(einoSettingsAgentFlowType, "settings", 2)
	require.Equal(t, "model alpha: You are the settings agent.", *snapshot.Messages[3].Text)
}
