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
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/agents/go/core/agentstest"
	"github.com/superdurable/dex/sdk-go/dex"
)

// A write that needs approval waits in parallel with a read, survives a Worker restart, runs
// once after approval, and its result reaches the model. A message sent meanwhile waits its turn.
func TestApprovedWriteRunsOnceAfterAWorkerRestart(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	worker := stack.startWorker()
	sessions := stack.sessions(scriptedAgentFlowType)
	ctx := context.Background()

	_, err := sessions.StartSession(ctx, "approval", ptr("lookup and refund"), dex.StartFlowOptions{})
	require.NoError(t, err)
	snapshot := stack.waitForSnapshot(scriptedAgentFlowType, "approval", "a pending approval and a finished read",
		func(snapshot dexagents.AgentSnapshot) bool {
			return len(snapshot.PendingInterrupts) == 1 && len(snapshot.ToolCalls) == 2 &&
				snapshot.ToolCalls[0].Status == dexagents.ToolCallStatusSucceeded
		})
	interrupt := snapshot.PendingInterrupts[0]
	require.Equal(t, dexagents.InterruptKindToolApproval, interrupt.Kind)
	require.Equal(t, "refund_order", interrupt.ToolName)
	require.Equal(t, interrupt.ToolCallID, interrupt.InterruptID)
	require.Nil(t, interrupt.Deadline)
	require.Equal(t, dexagents.ToolCallStatusAwaitingApproval, snapshot.ToolCalls[1].Status)
	require.Equal(t, dexagents.ToolEffectNonIdempotentWrite, snapshot.ToolCalls[1].Effect)
	require.Equal(t, dexagents.AgentSessionStatusRunningTools, snapshot.State.Status)

	isQueued, err := sessions.SendUserMessage(ctx, "approval", "after the refund")
	require.NoError(t, err)
	require.True(t, isQueued)
	worker.Kill()
	stack.startWorker()
	snapshot, err = sessions.GetAgentSnapshot(ctx, "approval")
	require.NoError(t, err)
	require.Equal(t, int64(1), snapshot.QueuedMessageCount, "a message sent during a turn is queued")
	require.Equal(t, "after the refund", snapshot.QueuedMessages[0].Message.Text)

	isResolved, err := sessions.ResolveInterrupt(ctx, "approval", interrupt.InterruptID, true, ptr("dropped on approval"))
	require.NoError(t, err)
	require.True(t, isResolved)
	isResolved, err = sessions.ResolveInterrupt(ctx, "approval", interrupt.InterruptID, true, nil)
	require.NoError(t, err)
	require.False(t, isResolved, "an interrupt resolves once")

	snapshot = stack.waitForTurnCount(scriptedAgentFlowType, "approval", 2)
	require.Equal(t, 1, stack.callCount("refund_order"))
	require.Equal(t, 1, stack.callCount("lookup_order"))
	require.Equal(t, "Done: order 1 is shipped; refunded 1 with key approval/turn-1/"+interrupt.ToolCallID,
		*snapshot.Messages[5].Text)
	require.Equal(t, "You said: after the refund", *snapshot.Messages[7].Text)
	executed := stack.executedStepTypes("approval")
	require.Contains(t, executed, "AwaitToolApproval")
	require.Contains(t, executed, "CallSingleAttemptTool")
	require.Contains(t, executed, "CallTool")
}

// A rejected write never runs, and the model sees the rejection message.
func TestRejectedWriteNeverRuns(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	stack.startWorker()
	sessions := stack.sessions(scriptedAgentFlowType)
	ctx := context.Background()

	_, err := sessions.StartSession(ctx, "rejection", ptr("refund 9"), dex.StartFlowOptions{})
	require.NoError(t, err)
	snapshot := stack.waitForPendingInterrupts(scriptedAgentFlowType, "rejection", 1)
	isResolved, err := sessions.ResolveInterrupt(ctx, "rejection", snapshot.PendingInterrupts[0].InterruptID, false, ptr("refunds need a manager"))
	require.NoError(t, err)
	require.True(t, isResolved)

	snapshot = stack.waitForTurnCount(scriptedAgentFlowType, "rejection", 1)
	require.Zero(t, stack.callCount("refund_order"))
	require.Equal(t, "refunds need a manager", *snapshot.Messages[2].ToolOutput)
	require.Equal(t, "Refund result: refunds need a manager", *snapshot.Messages[3].Text)
	require.NotContains(t, stack.executedStepTypes("rejection"), "CallSingleAttemptTool")
}

// An approval deadline rejects the call with "approval deadline passed", and a late answer is refused.
func TestApprovalDeadlineRejectsTheCall(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	stack.startWorker()
	sessions := stack.sessions(deadlineAgentFlowType)
	ctx := context.Background()

	_, err := sessions.StartSession(ctx, "deadline", ptr("refund 3"), dex.StartFlowOptions{})
	require.NoError(t, err)
	snapshot := stack.waitForPendingInterrupts(deadlineAgentFlowType, "deadline", 1)
	require.NotNil(t, snapshot.PendingInterrupts[0].Deadline)
	interruptID := snapshot.PendingInterrupts[0].InterruptID

	snapshot = stack.waitForTurnCount(deadlineAgentFlowType, "deadline", 1)
	require.Zero(t, stack.callCount("refund_order"))
	require.Equal(t, dexagents.ApprovalDeadlinePassed, *snapshot.Messages[2].ToolOutput)
	isResolved, err := sessions.ResolveInterrupt(ctx, "deadline", interruptID, true, nil)
	require.NoError(t, err)
	require.False(t, isResolved)
}

// A Worker killed during CallSingleAttemptTool records outcome_unknown; the tool never runs again,
// and the model sees the unknown outcome.
func TestWorkerKillDuringSingleAttemptToolRecordsOutcomeUnknown(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	worker := stack.startWorker()
	sessions := stack.sessions(scriptedAgentFlowType)
	ctx := context.Background()

	_, err := sessions.StartSession(ctx, "kill-tool", ptr("charge slowly"), dex.StartFlowOptions{})
	require.NoError(t, err)
	stack.waitForMarker("charge_card.started")
	worker.Kill()
	stack.startWorker()

	snapshot := stack.waitForTurnCount(scriptedAgentFlowType, "kill-tool", 1)
	require.Equal(t, 1, stack.callCount("charge_card"), "a single-attempt call never runs twice")
	require.Equal(t, dexagents.OutcomeUnknownToolResult, *snapshot.Messages[2].ToolOutput)
	require.Equal(t, "Charge result: "+dexagents.OutcomeUnknownToolResult, *snapshot.Messages[3].Text)
	executed := stack.executedStepTypes("kill-tool")
	require.Contains(t, executed, "RecordToolOutcomeUnknown")
	require.NotContains(t, executed, "CallSingleAttemptTool", "the killed attempt never completed")
}

// A Worker killed during RunModel reruns only that one model call.
func TestWorkerKillDuringRunModelRerunsOnlyThatCall(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	worker := stack.startWorker()
	sessions := stack.sessions(scriptedAgentFlowType)
	ctx := context.Background()

	_, err := sessions.StartSession(ctx, "kill-model", ptr("think slowly"), dex.StartFlowOptions{})
	require.NoError(t, err)
	stack.waitForMarker("model-attempt-1")
	worker.Kill()
	stack.startWorker()

	snapshot := stack.waitForTurnCount(scriptedAgentFlowType, "kill-model", 1)
	require.Equal(t, "answered on attempt 2", *snapshot.Messages[1].Text)
	require.Nil(t, snapshot.State.LastTurnError)
	history := stack.stepHistory("kill-model")
	runModelIndex := slices.IndexFunc(history, func(entry agentstest.StepHistoryEntry) bool {
		return entry.EventType == "StepExecuteCompleted" && entry.StepType == "RunModel"
	})
	require.GreaterOrEqual(t, runModelIndex, 0)
	require.Equal(t, 2, history[runModelIndex].FinalAttempt)
	require.Equal(t, []string{"StartAgentSession", "RunModel"}, stack.executedStepTypes("kill-model"))
	require.Equal(t, int64(1), snapshot.Usage.ModelCalls, "only the completed attempt's usage is recorded")
}

// A turn that fails after a write ran stores the turn-failure note, and the write is not repeated.
func TestTurnFailureAfterAWriteLeavesTheNote(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	stack.startWorker()
	sessions := stack.sessions(scriptedAgentFlowType)
	ctx := context.Background()

	_, err := sessions.StartSession(ctx, "failure", ptr("charge and fail"), dex.StartFlowOptions{})
	require.NoError(t, err)
	snapshot := stack.waitForTurnCount(scriptedAgentFlowType, "failure", 1)
	require.NotNil(t, snapshot.State.LastTurnError)
	require.Contains(t, *snapshot.State.LastTurnError, "the scripted model is down")
	require.Equal(t, 1, stack.callCount("charge_card"))
	require.Empty(t, snapshot.PendingInterrupts)
	require.Empty(t, snapshot.ToolCalls)

	// The incremental runner stores the unstored tool result before the note.
	require.Equal(t, []dexagents.MessageRole{
		dexagents.MessageRoleUser, dexagents.MessageRoleAssistant, dexagents.MessageRoleTool, dexagents.MessageRoleUser,
	}, messageRoles(snapshot.Messages))
	require.Equal(t, "charged 5", *snapshot.Messages[2].ToolOutput)
	require.Equal(t, strings.Join([]string{
		dexagents.TurnFailureNoteHeader,
		`- charge_card {"amount": 5} -> succeeded: charged 5`,
		dexagents.TurnFailureNoteFooter,
	}, "\n"), *snapshot.Messages[3].Text)
	require.Equal(t, []string{"StartAgentSession", "RunModel", "CallSingleAttemptTool", "ResumeTurn", "RecordTurnFailure"},
		stack.executedStepTypes("failure"))

	isQueued, err := sessions.SendUserMessage(ctx, "failure", "what happened?")
	require.NoError(t, err)
	require.True(t, isQueued)
	snapshot = stack.waitForTurnCount(scriptedAgentFlowType, "failure", 2)
	require.Equal(t, "I see the failure note", *snapshot.Messages[len(snapshot.Messages)-1].Text)
	require.Nil(t, snapshot.State.LastTurnError, "a completed turn clears the last turn error")
	require.Equal(t, 1, stack.callCount("charge_card"))
}

// A tool Step attempt carries the earlier attempt's heartbeat checkpoint forward, so a later
// attempt still reads it after an attempt that saved none.
func TestToolStepCarriesTheHeartbeatCheckpointForward(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	stack.startWorker()
	sessions := stack.sessions(checkpointAgentFlowType)

	_, err := sessions.StartSession(context.Background(), "checkpoint", ptr("run the job"), dex.StartFlowOptions{})
	require.NoError(t, err)
	snapshot := stack.waitForTurnCount(checkpointAgentFlowType, "checkpoint", 1)
	require.Equal(t, "Job: resumed job-1", *snapshot.Messages[len(snapshot.Messages)-1].Text)
	require.Equal(t, 2, stack.callCount("checkpoint-job-1"))
	require.Zero(t, stack.callCount("checkpoint-none"))
}

// Without tracing, tool call records carry no trace context, even when another Worker left one.
func TestToolCallRecordsCarryNoStaleTraceContext(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	stack.startWorker()
	sessions := stack.sessions(scriptedAgentFlowType)
	staleRoot, err := dex.InitialAttribute(dexagents.AgentTraceContextAttribute,
		"00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01")
	require.NoError(t, err)

	_, err = sessions.StartSession(context.Background(), "trace", ptr("lookup and refund"),
		dex.StartFlowOptions{Attributes: []dex.InitialAttributeDef{staleRoot}})
	require.NoError(t, err)
	snapshot := stack.waitForPendingInterrupts(scriptedAgentFlowType, "trace", 1)
	require.Len(t, snapshot.ToolCalls, 2)
	for _, toolCall := range snapshot.ToolCalls {
		require.Nil(t, toolCall.TraceContext, toolCall.ToolName)
	}
}
