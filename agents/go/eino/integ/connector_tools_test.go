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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/agents/go/core/agentstest"
	dexeino "github.com/superdurable/dex/agents/go/eino"
	"github.com/superdurable/dex/sdk-go/dex"
)

// Connector operations are Eino tools on the model side and run through their source in Dex tool
// Steps: a Query in CallTool, a single-attempt Mutation in CallSingleAttemptTool, beside an Eino tool.
func TestConnectorToolsRunThroughTheirSourceInToolSteps(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	stack.startWorker()
	sessions := stack.sessions(einoConnectorAgentFlowType)
	ctx := context.Background()

	_, err := sessions.StartSession(ctx, "ledger", ptr("ledger turn"), dex.StartFlowOptions{})
	require.NoError(t, err)
	stack.waitForMarker("ledger-final.reached")
	snapshot, err := sessions.GetAgentSnapshot(ctx, "ledger")
	require.NoError(t, err)
	records := map[string]dexagents.AgentToolCall{}
	for _, record := range snapshot.ToolCalls {
		records[record.ToolCallID] = record
	}
	require.Len(t, records, 3)
	require.Nil(t, records["call_lookup"].ToolSourceReference, "an Eino tool's record carries no source reference")
	require.Equal(t, dexagents.ToolEffectRead, records["call_lookup"].Effect)
	require.Equal(t, `{"dex_tool_source": "connectors", "reference": "{\"tool\": \"fake-ledger_get_balance\"}"}`,
		*records["call_balance"].ToolSourceReference)
	require.Equal(t, dexagents.ToolEffectRead, records["call_balance"].Effect)
	require.Equal(t, `{"dex_tool_source": "connectors", "reference": "{\"tool\": \"fake-ledger_post_note\"}"}`,
		*records["call_note"].ToolSourceReference)
	require.Equal(t, dexagents.ToolEffectNonIdempotentWrite, records["call_note"].Effect)
	require.True(t, records["call_note"].OrderedWrite)
	for id, record := range records {
		require.Equal(t, dexagents.ToolCallStatusSucceeded, record.Status, id)
	}
	stack.releaseMarker("ledger-final")

	snapshot = stack.waitForTurnCount(einoConnectorAgentFlowType, "ledger", 1)
	require.Nil(t, snapshot.State.LastTurnError)
	require.Equal(t, `Ledger: order 1 is shipped; {"accountId":"acct-1","amount":1250}; {"id":"note-1"}`,
		*snapshot.Messages[len(snapshot.Messages)-1].Text)
	steps := stack.executedStepTypes("ledger")
	require.Equal(t, []string{"StartAgentSession", "RunModel"}, steps[:2])
	require.ElementsMatch(t, []string{"CallTool", "CallTool", "CallSingleAttemptTool"}, steps[2:5])
	require.Equal(t, []string{"ResumeTurn", "RunModel"}, steps[5:])
	require.Equal(t, 1, stack.callCount("getBalance"))
	require.Equal(t, 1, stack.callCount("postNote"))
	require.Equal(t, 1, stack.callCount("lookup_order"))
	require.Equal(t, 1, stack.callCount("middleware"), "only the Eino tool runs through the tool middleware")
	require.Equal(t, 1, stack.callCount("handler"), "only the Eino tool runs through the Handler")
	offered, err := os.ReadFile(filepath.Join(stack.coordination.dir, "offered-tools"))
	require.NoError(t, err)
	require.Contains(t, string(offered), "lookup_order,")
	require.Contains(t, string(offered), "fake-ledger_get_balance,fake-ledger_post_note,fake-ledger_create_payout",
		"the source's tools follow the agent's own")
	require.Equal(t, int64(2), snapshot.Usage.ModelCalls)

	page, err := sessions.ListAgentMessages(ctx, "ledger", nil, 50)
	require.NoError(t, err)
	for _, message := range page.Messages {
		_, err := dexeino.DecodeMessage(message)
		require.NoError(t, err, "message %d", message.Sequence)
	}
}

// A connector's definite refusal reaches the model as a failed call; it runs once, outside the
// Eino tool middleware and Handler.
func TestRejectedConnectorWriteIsAFailedCallThatRunsOnce(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	stack.startWorker()
	sessions := stack.sessions(einoConnectorAgentFlowType)

	_, err := sessions.StartSession(context.Background(), "rejected", ptr("rejected note"), dex.StartFlowOptions{})
	require.NoError(t, err)
	snapshot := stack.waitForTurnCount(einoConnectorAgentFlowType, "rejected", 1)
	require.Nil(t, snapshot.State.LastTurnError)
	require.Equal(t, "Note: The tool call failed: fake-ledger postNote returned providerRejected: PROVIDER_REJECTION: account is closed",
		*snapshot.Messages[len(snapshot.Messages)-1].Text)
	require.Equal(t, 1, stack.callCount("postNote"))
	require.Equal(t, 1, stack.finalAttempt("rejected", "CallSingleAttemptTool"))
	require.Zero(t, stack.callCount("middleware"))
	require.Zero(t, stack.callCount("handler"))
}

// A connector write whose metadata recommends approval waits in AwaitToolApproval and runs once after it.
func TestConnectorWriteWaitsForItsApprovalAndRunsOnce(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	stack.startWorker()
	sessions := stack.sessions(einoConnectorAgentFlowType)
	ctx := context.Background()

	_, err := sessions.StartSession(ctx, "payout", ptr("payout"), dex.StartFlowOptions{})
	require.NoError(t, err)
	snapshot := stack.waitForPendingInterrupts(einoConnectorAgentFlowType, "payout", 1)
	interrupt := snapshot.PendingInterrupts[0]
	require.Equal(t, "fake-ledger_create_payout", interrupt.ToolName)
	require.Zero(t, stack.callCount("createPayout"), "nothing runs before the approval")

	isResolved, err := sessions.ResolveInterrupt(ctx, "payout", interrupt.InterruptID, true, nil)
	require.NoError(t, err)
	require.True(t, isResolved)
	snapshot = stack.waitForTurnCount(einoConnectorAgentFlowType, "payout", 1)
	require.Equal(t, `Payout: {"id":"payout-acct-2"}`, *snapshot.Messages[len(snapshot.Messages)-1].Text)
	require.Equal(t, 1, stack.callCount("createPayout"))
	require.Equal(t, []string{"StartAgentSession", "RunModel", "AwaitToolApproval", "CallTool", "ResumeTurn", "RunModel"},
		agentstest.ExecutedStepTypes(stack.stepHistory("payout")))
}
