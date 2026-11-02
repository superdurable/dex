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
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/sdk-go/dex"
)

// A Python Client starts a session on a Go Worker, approves its write, queues a message, pages
// the messages, and reads the AgentEvents Stream; a Go Client then reads the same session.
func TestPythonClientRunsASessionOnAGoWorker(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	stack.startWorker()

	output := runPythonScript(t, "cross_language_client.py", stack.engine.Address(), stack.stateDir, "python-client")
	var seen struct {
		WaitingInterrupt struct {
			Kind     string `json:"kind"`
			ToolName string `json:"tool_name"`
		} `json:"waiting_interrupt"`
		WaitingToolStatuses    []string        `json:"waiting_tool_statuses"`
		IsQueued               bool            `json:"queued"`
		IsResolved             bool            `json:"resolved"`
		IsResolvedAgain        bool            `json:"resolved_again"`
		Snapshot               json.RawMessage `json:"snapshot"`
		PageSequences          []int64         `json:"page_sequences"`
		PageNextBeforeSequence *int64          `json:"page_next_before_sequence"`
		EventTypes             []string        `json:"event_types"`
	}
	require.NoError(t, json.Unmarshal([]byte(output), &seen))
	require.Equal(t, "tool_approval", seen.WaitingInterrupt.Kind)
	require.Equal(t, "refund_order", seen.WaitingInterrupt.ToolName)
	require.Contains(t, seen.WaitingToolStatuses, "awaiting_approval")
	require.True(t, seen.IsQueued)
	require.True(t, seen.IsResolved)
	require.False(t, seen.IsResolvedAgain)
	require.Equal(t, []int64{6, 7, 8}, seen.PageSequences)
	require.Equal(t, int64(6), *seen.PageNextBeforeSequence)
	for _, eventType := range []string{"turn_started", "tool_call_deferred", "interrupt_raised", "tool_call_started",
		"tool_call_finished", "interrupt_resolved", "usage_recorded", "text_delta", "turn_completed"} {
		require.Contains(t, seen.EventTypes, eventType)
	}
	require.Equal(t, 1, stack.callCount("refund_order"))

	var pythonSnapshot struct {
		State struct {
			TurnCount int64 `json:"turn_count"`
		} `json:"state"`
		Messages []struct {
			Text *string `json:"text"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(seen.Snapshot, &pythonSnapshot))
	require.Equal(t, int64(2), pythonSnapshot.State.TurnCount)
	require.Len(t, pythonSnapshot.Messages, 8)
	require.Contains(t, *pythonSnapshot.Messages[5].Text, "Done: order 1 is shipped; refunded 1")
	require.Equal(t, "You said: thanks", *pythonSnapshot.Messages[7].Text)

	goSnapshot, err := stack.sessions(scriptedAgentFlowType).GetAgentSnapshot(context.Background(), "python-client")
	require.NoError(t, err)
	require.Equal(t, *pythonSnapshot.Messages[5].Text, *goSnapshot.Messages[5].Text)
	require.Equal(t, dexagents.AgentUsage{ModelCalls: 3, InputTokens: 30, OutputTokens: 15}, goSnapshot.Usage)
}

// pythonStepwiseFlowType is the Flow type that the Python core's Stepwise test Worker serves.
const pythonStepwiseFlowType = "StepwiseScriptedAgent"

// A Go Client starts a session on a Python Worker, approves its write, and decodes every value
// the Python Worker stored.
func TestGoClientRunsASessionOnAPythonWorker(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	stack.startPythonWorker()
	flow, err := newTypingOnlyFlow(pythonStepwiseFlowType)
	require.NoError(t, err)
	stack.addClientFlow(flow)
	sessions := stack.sessions(pythonStepwiseFlowType)
	ctx := context.Background()

	_, err = sessions.StartSession(ctx, "go-client", ptr("publish hello from Go"), dex.StartFlowOptions{})
	require.NoError(t, err)
	snapshot := stack.waitForPendingInterrupts(pythonStepwiseFlowType, "go-client", 1)
	require.Equal(t, "publish_note", snapshot.PendingInterrupts[0].ToolName)
	require.Equal(t, dexagents.ToolCallStatusAwaitingApproval, snapshot.ToolCalls[0].Status)
	require.Equal(t, dexagents.InteractionModeChat, snapshot.State.InteractionMode)
	require.True(t, snapshot.State.AcceptsTurnControl, "a Python 0.2 Worker accepts turn control")
	isResolved, err := sessions.ResolveInterrupt(ctx, "go-client", snapshot.PendingInterrupts[0].InterruptID, true, nil)
	require.NoError(t, err)
	require.True(t, isResolved)

	snapshot = stack.waitForTurnCount(pythonStepwiseFlowType, "go-client", 1)
	last := snapshot.Messages[len(snapshot.Messages)-1]
	require.Equal(t, `publish_note: {"published": "hello from Go"}`, *last.Text)
	page, err := sessions.ListAgentMessages(ctx, "go-client", nil, 2)
	require.NoError(t, err)
	require.Len(t, page.Messages, 2)
	event, _, err := sessions.ReadAgentEvent(ctx, "go-client", "")
	require.NoError(t, err)
	require.Equal(t, "turn_started", event.Type)
}

// typescriptCoreDir is the TypeScript core, whose built package runs the TypeScript Client script.
const typescriptCoreDir = "../../../typescript/core"

// A TypeScript Client reads a Go-served session's snapshot mid-turn, approves its write, and reads
// the finished session.
func TestTypeScriptClientDrivesASessionOnAGoWorker(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	stack.startWorker()
	_, err := stack.sessions(scriptedAgentFlowType).StartSession(context.Background(), "typescript-client",
		ptr("lookup and refund"), dex.StartFlowOptions{})
	require.NoError(t, err)
	stack.waitForSnapshot(scriptedAgentFlowType, "typescript-client", "a pending approval and a finished read",
		func(snapshot dexagents.AgentSnapshot) bool {
			return len(snapshot.PendingInterrupts) == 1 && len(snapshot.ToolCalls) == 2 &&
				snapshot.ToolCalls[0].Status == dexagents.ToolCallStatusSucceeded
		})

	typescriptCore, err := filepath.Abs(typescriptCoreDir)
	require.NoError(t, err)
	scriptPath, err := filepath.Abs(filepath.Join("testdata", "typescript", "drive_go_session.mjs"))
	require.NoError(t, err)
	command := exec.CommandContext(context.Background(), "node", scriptPath, typescriptCore,
		stack.engine.Address(), "typescript-client")
	output, err := command.Output()
	if exitError, isExitError := err.(*exec.ExitError); isExitError {
		t.Fatalf("the TypeScript Client failed: %v\n%s", err, exitError.Stderr)
	}
	require.NoError(t, err)
	var seen struct {
		WaitingStatus      string           `json:"waitingStatus"`
		WaitingToolCalls   [][]string       `json:"waitingToolCalls"`
		Interrupt          []any            `json:"interrupt"`
		IsResolved         bool             `json:"isResolved"`
		TurnCount          int64            `json:"turnCount"`
		AcceptsTurnControl bool             `json:"acceptsTurnControl"`
		InteractionMode    string           `json:"interactionMode"`
		MessageTexts       []*string        `json:"messageTexts"`
		Usage              map[string]int64 `json:"usage"`
		PageSequences      []int64          `json:"pageSequences"`
	}
	require.NoError(t, json.Unmarshal(output, &seen), string(output))
	require.Equal(t, "running_tools", seen.WaitingStatus)
	require.Equal(t, [][]string{
		{"lookup_order", "succeeded", "read"},
		{"refund_order", "awaiting_approval", "non_idempotent_write"},
	}, seen.WaitingToolCalls)
	require.Equal(t, []any{"tool_approval", "refund_order", []any{}, true}, seen.Interrupt)
	require.True(t, seen.IsResolved)
	require.Equal(t, int64(1), seen.TurnCount)
	require.False(t, seen.AcceptsTurnControl)
	require.Equal(t, "chat", seen.InteractionMode)
	require.Len(t, seen.MessageTexts, 6)
	require.Contains(t, *seen.MessageTexts[5], "Done: order 1 is shipped; refunded 1")
	require.Equal(t, map[string]int64{"modelCalls": 2, "inputTokens": 20, "outputTokens": 10,
		"cachedInputTokens": 0, "reasoningTokens": 0}, seen.Usage)
	require.Equal(t, []int64{5, 6}, seen.PageSequences)
	require.Equal(t, 1, stack.callCount("refund_order"))
}
