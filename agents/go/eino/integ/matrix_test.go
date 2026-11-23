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
	dexeino "github.com/superdurable/dex/agents/go/eino"
	"github.com/superdurable/dex/sdk-go/dex"
)

// Matrix 1 and 11: a turn with two model calls and a read runs RunModel, CallTool, ResumeTurn,
// RunModel; the history replays on the next turn; snapshots and message pages decode.
func TestReadToolTurnRunsOneStepPerCallAndReplaysTheHistory(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	stack.startWorker()
	sessions := stack.sessions(einoAgentFlowType)
	ctx := context.Background()

	_, err := sessions.StartSession(ctx, "read", ptr("lookup 7"), dex.StartFlowOptions{})
	require.NoError(t, err)
	snapshot := stack.waitForTurnCount(einoAgentFlowType, "read", 1)
	require.Equal(t, []string{"StartAgentSession", "RunModel", "CallTool", "ResumeTurn", "RunModel"}, stack.executedStepTypes("read"))
	require.Equal(t, []dexagents.MessageRole{dexagents.MessageRoleUser, dexagents.MessageRoleAssistant,
		dexagents.MessageRoleTool, dexagents.MessageRoleAssistant}, messageRoles(snapshot.Messages))
	require.Equal(t, "Order status: order 7 is shipped", *snapshot.Messages[3].Text)
	require.True(t, strings.HasPrefix(*snapshot.Messages[1].ToolCallID, "RunModel"), "an empty Eino ID gets a synthesized one")
	require.Equal(t, int64(2), snapshot.Usage.ModelCalls)
	require.Equal(t, int64(20), snapshot.Usage.InputTokens)
	require.Equal(t, 1, stack.callCount("lookup_order"))
	require.Equal(t, 2, stack.callCount("model"))

	isQueued, err := sessions.SendUserMessage(ctx, "read", "hello again")
	require.NoError(t, err)
	require.True(t, isQueued)
	snapshot = stack.waitForTurnCount(einoAgentFlowType, "read", 2)
	require.Len(t, snapshot.Messages, 6)
	require.Equal(t, "You said: hello again", *snapshot.Messages[5].Text)

	page, err := sessions.ListAgentMessages(ctx, "read", nil, 50)
	require.NoError(t, err)
	require.Len(t, page.Messages, 6)
	for _, message := range page.Messages {
		decoded, err := dexeino.DecodeMessage(message)
		require.NoError(t, err, "message %d", message.Sequence)
		require.Contains(t, []dexagents.MessageRole{dexagents.MessageRoleUser, dexagents.MessageRoleAssistant, dexagents.MessageRoleTool},
			message.Role)
		require.Equal(t, string(message.Role), string(decoded.Role))
	}
}

// Matrix 2: the tool calls of one model response run as parallel Steps.
func TestParallelToolCallsRunAsParallelSteps(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	stack.startWorker()
	sessions := stack.sessions(einoAgentFlowType)

	_, err := sessions.StartSession(context.Background(), "parallel", ptr("parallel reads"), dex.StartFlowOptions{})
	require.NoError(t, err)
	// Both reads start before either is released, so their Steps overlap.
	stack.waitForMarker("read-a.started")
	stack.waitForMarker("read-b.started")
	stack.releaseMarker("read-a")
	stack.releaseMarker("read-b")
	snapshot := stack.waitForTurnCount(einoAgentFlowType, "parallel", 1)
	require.Equal(t, "Parallel: read read-a; read read-b", *snapshot.Messages[len(snapshot.Messages)-1].Text)
	require.Equal(t, 2, stack.callCount("slow_read"))
}

// Matrix 3, 7, and 10: a write that needs approval waits beside a read, survives a Worker restart,
// runs once after approval, and its result reaches the model; a second resolve returns false; a
// message sent meanwhile is queued and handled after the turn.
func TestApprovedWriteRunsOnceAfterAWorkerRestart(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	worker := stack.startWorker()
	sessions := stack.sessions(einoAgentFlowType)
	ctx := context.Background()

	_, err := sessions.StartSession(ctx, "approval", ptr("lookup and refund"), dex.StartFlowOptions{})
	require.NoError(t, err)
	snapshot := stack.waitForSnapshot(einoAgentFlowType, "approval", "a pending approval and a finished read",
		func(snapshot dexagents.AgentSnapshot) bool {
			return len(snapshot.PendingInterrupts) == 1 && len(snapshot.ToolCalls) == 2 &&
				snapshot.ToolCalls[0].Status == dexagents.ToolCallStatusSucceeded
		})
	interrupt := snapshot.PendingInterrupts[0]
	require.Equal(t, "refund_order", interrupt.ToolName)
	require.Equal(t, dexagents.ToolEffectNonIdempotentWrite, snapshot.ToolCalls[1].Effect)
	require.Equal(t, dexagents.ToolCallStatusAwaitingApproval, snapshot.ToolCalls[1].Status)

	isQueued, err := sessions.SendUserMessage(ctx, "approval", "after the refund")
	require.NoError(t, err)
	require.True(t, isQueued)
	worker.Kill()
	stack.startWorker()
	snapshot, err = sessions.GetAgentSnapshot(ctx, "approval")
	require.NoError(t, err)
	require.Equal(t, int64(1), snapshot.QueuedMessageCount)

	isResolved, err := sessions.ResolveInterrupt(ctx, "approval", interrupt.InterruptID, true, nil)
	require.NoError(t, err)
	require.True(t, isResolved)
	isResolved, err = sessions.ResolveInterrupt(ctx, "approval", interrupt.InterruptID, true, nil)
	require.NoError(t, err)
	require.False(t, isResolved, "an interrupt resolves once")

	snapshot = stack.waitForTurnCount(einoAgentFlowType, "approval", 2)
	require.Equal(t, 1, stack.callCount("refund_order"))
	require.Equal(t, 1, stack.callCount("lookup_order"))
	require.Equal(t, "Done: order 1 is shipped; refunded 1 with key approval/turn-1/call_refund", *snapshot.Messages[4].Text)
	require.Equal(t, "You said: after the refund", *snapshot.Messages[6].Text)
	executed := stack.executedStepTypes("approval")
	require.Contains(t, executed, "AwaitToolApproval")
	require.Contains(t, executed, "CallSingleAttemptTool")
	require.Contains(t, executed, "CallTool")
}

// Matrix 4: a rejected write never runs, and the model sees the rejection message.
func TestRejectedWriteNeverRuns(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	stack.startWorker()
	sessions := stack.sessions(einoAgentFlowType)
	ctx := context.Background()

	_, err := sessions.StartSession(ctx, "rejection", ptr("refund 9"), dex.StartFlowOptions{})
	require.NoError(t, err)
	snapshot := stack.waitForPendingInterrupts(einoAgentFlowType, "rejection", 1)
	isResolved, err := sessions.ResolveInterrupt(ctx, "rejection", snapshot.PendingInterrupts[0].InterruptID, false, ptr("refunds need a manager"))
	require.NoError(t, err)
	require.True(t, isResolved)

	snapshot = stack.waitForTurnCount(einoAgentFlowType, "rejection", 1)
	require.Zero(t, stack.callCount("refund_order"))
	require.Equal(t, "refunds need a manager", *snapshot.Messages[2].ToolOutput)
	require.Equal(t, "Refund result: refunds need a manager", *snapshot.Messages[3].Text)
	require.NotContains(t, stack.executedStepTypes("rejection"), "CallSingleAttemptTool")
}

// Matrix 5: a Worker killed during CallSingleAttemptTool records outcome_unknown, and the tool never runs again.
func TestWorkerKillDuringSingleAttemptToolRecordsOutcomeUnknown(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	worker := stack.startWorker()
	sessions := stack.sessions(einoAgentFlowType)

	_, err := sessions.StartSession(context.Background(), "kill-tool", ptr("charge slowly"), dex.StartFlowOptions{})
	require.NoError(t, err)
	stack.waitForMarker("charge_card.started")
	worker.Kill()
	stack.startWorker()

	snapshot := stack.waitForTurnCount(einoAgentFlowType, "kill-tool", 1)
	require.Equal(t, 1, stack.callCount("charge_card"), "a single-attempt call never runs twice")
	require.Equal(t, dexagents.OutcomeUnknownToolResult, *snapshot.Messages[2].ToolOutput)
	require.Equal(t, "Charge result: "+dexagents.OutcomeUnknownToolResult, *snapshot.Messages[3].Text)
	executed := stack.executedStepTypes("kill-tool")
	require.Contains(t, executed, "RecordToolOutcomeUnknown")
	require.NotContains(t, executed, "CallSingleAttemptTool")
}

// Matrix 6: a Worker killed while the model streams reruns only that one model call.
func TestWorkerKillDuringRunModelRerunsOnlyThatCall(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	worker := stack.startWorker()
	sessions := stack.sessions(einoStreamingAgentFlowType)

	_, err := sessions.StartSession(context.Background(), "kill-model", ptr("think slowly"), dex.StartFlowOptions{})
	require.NoError(t, err)
	stack.waitForMarker("model-attempt-1")
	worker.Kill()
	stack.startWorker()

	snapshot := stack.waitForTurnCount(einoStreamingAgentFlowType, "kill-model", 1)
	require.Equal(t, "answered after the restart", *snapshot.Messages[1].Text)
	require.Nil(t, snapshot.State.LastTurnError)
	require.Equal(t, 2, stack.finalAttempt("kill-model", "RunModel"))
	require.Equal(t, []string{"StartAgentSession", "RunModel"}, stack.executedStepTypes("kill-model"))
	require.Equal(t, int64(1), snapshot.Usage.ModelCalls, "only the completed attempt's usage is recorded")
	require.Equal(t, 2, stack.callCount("model"))
}

// Matrix 7: an approval deadline rejects the call, and a late answer is refused.
func TestApprovalDeadlineRejectsTheCall(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	stack.startWorker()
	sessions := stack.sessions(einoDeadlineAgentFlowType)
	ctx := context.Background()

	_, err := sessions.StartSession(ctx, "deadline", ptr("refund 3"), dex.StartFlowOptions{})
	require.NoError(t, err)
	snapshot := stack.waitForPendingInterrupts(einoDeadlineAgentFlowType, "deadline", 1)
	require.NotNil(t, snapshot.PendingInterrupts[0].Deadline)
	interruptID := snapshot.PendingInterrupts[0].InterruptID

	snapshot = stack.waitForTurnCount(einoDeadlineAgentFlowType, "deadline", 1)
	require.Zero(t, stack.callCount("refund_order"))
	require.Equal(t, dexagents.ApprovalDeadlinePassed, *snapshot.Messages[2].ToolOutput)
	isResolved, err := sessions.ResolveInterrupt(ctx, "deadline", interruptID, true, nil)
	require.NoError(t, err)
	require.False(t, isResolved)
}

// Matrix 8: a turn that fails after a write ran stores the tool result and the turn-failure note,
// and the write is not repeated on the next turn.
func TestTurnFailureAfterAWriteLeavesTheNote(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	stack.startWorker()
	sessions := stack.sessions(einoAgentFlowType)
	ctx := context.Background()

	_, err := sessions.StartSession(ctx, "failure", ptr("charge and fail"), dex.StartFlowOptions{})
	require.NoError(t, err)
	snapshot := stack.waitForTurnCount(einoAgentFlowType, "failure", 1)
	require.NotNil(t, snapshot.State.LastTurnError)
	require.Contains(t, *snapshot.State.LastTurnError, "the scripted model is down")
	require.Equal(t, 1, stack.callCount("charge_card"))
	require.Equal(t, []dexagents.MessageRole{dexagents.MessageRoleUser, dexagents.MessageRoleAssistant,
		dexagents.MessageRoleTool, dexagents.MessageRoleUser}, messageRoles(snapshot.Messages))
	require.Equal(t, "charged 5", *snapshot.Messages[2].ToolOutput)
	require.Equal(t, strings.Join([]string{
		dexagents.TurnFailureNoteHeader,
		`- charge_card {"amount":5} -> succeeded: charged 5`,
		dexagents.TurnFailureNoteFooter,
	}, "\n"), *snapshot.Messages[3].Text)
	require.Equal(t, []string{"StartAgentSession", "RunModel", "CallSingleAttemptTool", "ResumeTurn", "RunModel", "RecordTurnFailure"},
		stack.executedStepTypes("failure"))

	isQueued, err := sessions.SendUserMessage(ctx, "failure", "what happened?")
	require.NoError(t, err)
	require.True(t, isQueued)
	snapshot = stack.waitForTurnCount(einoAgentFlowType, "failure", 2)
	require.Equal(t, "I see the failure note", *snapshot.Messages[len(snapshot.Messages)-1].Text)
	require.Nil(t, snapshot.State.LastTurnError)
	require.Equal(t, 1, stack.callCount("charge_card"))
}

// Matrix 9: an ASYNC Step durability default never reruns a single-attempt write.
func TestAsyncDurabilityNeverRerunsASingleAttemptWrite(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	worker := stack.startWorker()
	sessions := stack.sessions(einoAgentFlowType)
	async := dex.StepDurabilityAsync

	_, err := sessions.StartSession(context.Background(), "async", ptr("charge slowly"),
		dex.StartFlowOptions{ConfigOverride: &dex.FlowConfig{StepDurability: &async}})
	require.NoError(t, err)
	stack.waitForMarker("charge_card.started")
	worker.Kill()
	stack.startWorker()
	snapshot := stack.waitForTurnCount(einoAgentFlowType, "async", 1)
	require.Equal(t, 1, stack.callCount("charge_card"))
	require.Equal(t, dexagents.OutcomeUnknownToolResult, *snapshot.Messages[2].ToolOutput)
}
