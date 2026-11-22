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
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex-connectors-library/sdkgo"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/agents/go/core/agentstest"
	"github.com/superdurable/dex/agents/go/core/builtinloop"
	"github.com/superdurable/dex/agents/go/core/connectortools"
	"github.com/superdurable/dex/agents/go/core/internal/fakeledger"
	"github.com/superdurable/dex/sdk-go/dex"
)

// connectorAgentFlowType is a built-in loop agent whose tools are fake-ledger Connector operations.
const connectorAgentFlowType = "ConnectorAgent"

// newConnectorAgentFlow builds the connector agent around the provider files in coordinationDir.
func newConnectorAgentFlow(coordinationDir string) (dex.Flow, error) {
	provider := fakeledger.NewProvider(coordinationDir)
	getBalance, err := sdkgo.NewQueryAgentTool[fakeledger.GetBalanceInput, fakeledger.Balance](
		fakeledger.GetBalance{Provider: provider}, fakeledger.Connection, fakeledger.GetBalanceMetadata())
	if err != nil {
		return nil, err
	}
	createPayout, err := sdkgo.NewMutationAgentTool[fakeledger.CreatePayoutInput, fakeledger.Payout](
		fakeledger.CreatePayout{Provider: provider}, fakeledger.Connection, fakeledger.CreatePayoutMetadata())
	if err != nil {
		return nil, err
	}
	postNote, err := sdkgo.NewMutationAgentTool[fakeledger.TextInput, fakeledger.Stored](
		fakeledger.PostNote{Provider: provider}, fakeledger.Connection, fakeledger.PostNoteMetadata())
	if err != nil {
		return nil, err
	}
	sendMessage, err := sdkgo.NewMutationAgentTool[fakeledger.TextInput, fakeledger.Stored](
		fakeledger.SendMessage{Provider: provider}, fakeledger.Connection, fakeledger.SendMessageMetadata(0))
	if err != nil {
		return nil, err
	}
	tools := []*connectortools.ConnectorTool{}
	for _, agentTool := range []sdkgo.AgentTool{getBalance, createPayout, postNote, sendMessage} {
		tool, err := connectortools.NewConnectorTool(agentTool)
		if err != nil {
			return nil, err
		}
		tools = append(tools, tool)
	}
	source, err := connectortools.NewConnectorToolSource(tools)
	if err != nil {
		return nil, err
	}
	definition, err := builtinloop.NewAgentDefinition("ledger", "You manage ledger accounts.",
		agentstest.NewScriptedModelClient(respondAsLedgerAgent), builtinloop.WithToolSources(source))
	if err != nil {
		return nil, err
	}
	return builtinloop.NewBuiltinAgentSessionFlow(definition, connectorAgentFlowType)
}

// respondAsLedgerAgent calls the tools the last user text names, then reports every result:
// "balance <account>", "send <text>", "payout <amount>", "note <text> | send <text>", which asks
// for a read and two writes in one response, and "notepay <text>", a note and then a payout.
func respondAsLedgerAgent(request builtinloop.ModelRequest) (agentstest.ScriptedModelReply, error) {
	userTexts := agentstest.UserTexts(request)
	lastText := userTexts[len(userTexts)-1]
	results := agentstest.ToolResults(request)
	if len(results) > 0 {
		outputs := make([]string, 0, len(results))
		for _, result := range results {
			outputs = append(outputs, result.ToolName+": "+result.Output)
		}
		return agentstest.ScriptedModelReply{Text: strings.Join(outputs, "\n")}, nil
	}
	call := func(toolName string, arguments map[string]any) agentstest.ScriptedToolCall {
		encoded, err := agentstest.JSONArguments(arguments)
		if err != nil {
			panic(err)
		}
		return agentstest.ScriptedToolCall{ToolName: toolName, Arguments: encoded}
	}
	switch command, argument, _ := strings.Cut(lastText, " "); command {
	case "balance":
		return toolCalls(call("fake-ledger_get_balance", map[string]any{"accountId": argument})), nil
	case "send":
		return toolCalls(call("fake-ledger_send_message", map[string]any{"accountId": "a-1", "text": argument})), nil
	case "payout":
		var amount int
		if _, err := fmt.Sscan(argument, &amount); err != nil {
			return agentstest.ScriptedModelReply{}, err
		}
		return toolCalls(call("fake-ledger_create_payout", map[string]any{"accountId": "a-1", "amount": amount})), nil
	case "notepay":
		return toolCalls(
			call("fake-ledger_post_note", map[string]any{"accountId": "a-1", "text": argument}),
			call("fake-ledger_create_payout", map[string]any{"accountId": "a-1", "amount": 9}),
		), nil
	case "note":
		noteText, sendText, _ := strings.Cut(argument, " | send ")
		return toolCalls(
			call("fake-ledger_post_note", map[string]any{"accountId": "a-1", "text": noteText}),
			call("fake-ledger_get_balance", map[string]any{"accountId": "a-1"}),
			call("fake-ledger_send_message", map[string]any{"accountId": "a-1", "text": sendText}),
		), nil
	default:
		return agentstest.ScriptedModelReply{Text: "I can read balances, send messages, post notes, and pay out."}, nil
	}
}

func (stack *agentTestStack) ledger() *fakeledger.Provider {
	return fakeledger.NewProvider(stack.coordinationDir)
}

// finalAttempt returns the attempt that completed the session's only execution of a Step type.
func (stack *agentTestStack) finalAttempt(sessionID string, stepType string) int {
	stack.t.Helper()
	history := stack.stepHistory(sessionID)
	index := slices.IndexFunc(history, func(entry agentstest.StepHistoryEntry) bool {
		return entry.EventType == "StepExecuteCompleted" && entry.StepType == stepType
	})
	require.GreaterOrEqual(stack.t, index, 0, "no completed %s", stepType)
	return history[index].FinalAttempt
}

// A limited Connector write whose Worker is SIGKILLed after the send retries in the same tool Step,
// reads the write back under the same Call ID, and succeeds without a second send.
func TestAConnectorWriteKilledAfterItsSendReadsBackOnTheRetry(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	worker := stack.startWorker()
	sessions := stack.sessions(connectorAgentFlowType)
	ctx := context.Background()

	_, err := sessions.StartSession(ctx, "kill-send", ptr("send hello gate-after-send:kill-send"), dex.StartFlowOptions{})
	require.NoError(t, err)
	stack.waitForMarker("kill-send.reached")
	worker.Kill()
	stack.startWorker()

	snapshot := stack.waitForTurnCount(connectorAgentFlowType, "kill-send", 1)
	require.Nil(t, snapshot.State.LastTurnError)
	require.Equal(t, `{"id":"sendMessage-1"}`, *snapshot.Messages[2].ToolOutput)
	require.Equal(t, `fake-ledger_send_message: {"id":"sendMessage-1"}`, *snapshot.Messages[3].Text)
	require.Equal(t, []string{"sendMessage-1"}, stack.ledger().Writes("sendMessage"))
	require.Equal(t, 1, stack.ledger().Sends("sendMessage"), "the retry read the write back instead of sending again")
	require.Equal(t, 2, stack.ledger().Calls("sendMessage"))
	require.Equal(t, 2, stack.finalAttempt("kill-send", "CallSingleAttemptTool"))
	require.Equal(t, []string{"StartAgentSession", "RunModel", "CallSingleAttemptTool", "ResumeTurn", "RunModel"},
		stack.executedStepTypes("kill-send"))
}

// A definite refusal after the send records failed and is never sent again, although the limited
// write's tool Step has attempts left.
func TestAConnectorWriteTheProviderRefusedIsNeverResent(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	stack.startWorker()
	sessions := stack.sessions(connectorAgentFlowType)

	_, err := sessions.StartSession(context.Background(), "refused", ptr("send reject-on-send"), dex.StartFlowOptions{})
	require.NoError(t, err)
	snapshot := stack.waitForTurnCount(connectorAgentFlowType, "refused", 1)
	require.Equal(t, "The tool call failed: fake-ledger sendMessage returned providerRejected: PROVIDER_REJECTION: message refused",
		*snapshot.Messages[2].ToolOutput)
	require.Equal(t, 1, stack.ledger().Sends("sendMessage"))
	require.Empty(t, stack.ledger().Writes("sendMessage"))
	require.Equal(t, 1, stack.finalAttempt("refused", "CallSingleAttemptTool"))
}

// A Connector Query that asks to retry runs again in its CallTool Step and returns the later result.
func TestAConnectorQueryThatAsksToRetryIsRetried(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	stack.startWorker()
	sessions := stack.sessions(connectorAgentFlowType)

	_, err := sessions.StartSession(context.Background(), "retried-read", ptr("balance retry-once"), dex.StartFlowOptions{})
	require.NoError(t, err)
	snapshot := stack.waitForTurnCount(connectorAgentFlowType, "retried-read", 1)
	require.Equal(t, `{"accountId":"retry-once","amount":1250}`, *snapshot.Messages[2].ToolOutput)
	require.Equal(t, 2, stack.ledger().Calls("getBalance"))
	require.Equal(t, 2, stack.finalAttempt("retried-read", "CallTool"))
}

// A Connector write whose metadata recommends approval waits in AwaitToolApproval, then runs once.
func TestAConnectorWriteThatRecommendsApprovalWaitsForIt(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	stack.startWorker()
	sessions := stack.sessions(connectorAgentFlowType)
	ctx := context.Background()

	_, err := sessions.StartSession(ctx, "payout", ptr("payout 7"), dex.StartFlowOptions{})
	require.NoError(t, err)
	snapshot := stack.waitForPendingInterrupts(connectorAgentFlowType, "payout", 1)
	interrupt := snapshot.PendingInterrupts[0]
	require.Equal(t, "fake-ledger_create_payout", interrupt.ToolName)
	require.Equal(t, dexagents.ToolCallStatusAwaitingApproval, snapshot.ToolCalls[0].Status)
	require.Equal(t, dexagents.ToolEffectIdempotentWrite, snapshot.ToolCalls[0].Effect)
	require.Equal(t, `{"stepwise_tool_source": "connectors", "reference": "{\"tool\": \"fake-ledger_create_payout\"}"}`,
		*snapshot.ToolCalls[0].ToolSourceReference)
	require.Zero(t, stack.ledger().Calls("createPayout"), "nothing runs before the approval")

	isResolved, err := sessions.ResolveInterrupt(ctx, "payout", interrupt.InterruptID, true, nil)
	require.NoError(t, err)
	require.True(t, isResolved)
	snapshot = stack.waitForTurnCount(connectorAgentFlowType, "payout", 1)
	require.Equal(t, `{"amount":7,"payoutId":"createPayout-1"}`, *snapshot.Messages[2].ToolOutput)
	require.Equal(t, []string{"createPayout-1"}, stack.ledger().Writes("createPayout"))
	executed := stack.executedStepTypes("payout")
	require.Contains(t, executed, "AwaitToolApproval")
	require.Contains(t, executed, "CallTool")
}

// Connector writes of one response run one at a time in the model's order while the read runs
// beside the first, and a write that does not succeed skips the later ones without running them.
func TestConnectorWritesRunInTheModelsOrderAndSkipAfterAFailure(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	stack.startWorker()
	sessions := stack.sessions(connectorAgentFlowType)
	ctx := context.Background()

	_, err := sessions.StartSession(ctx, "ordered", ptr("note first | send second"), dex.StartFlowOptions{})
	require.NoError(t, err)
	snapshot := stack.waitForTurnCount(connectorAgentFlowType, "ordered", 1)
	require.Equal(t, strings.Join([]string{
		`fake-ledger_post_note: {"id":"postNote-1"}`,
		`fake-ledger_get_balance: {"accountId":"a-1","amount":1250}`,
		`fake-ledger_send_message: {"id":"sendMessage-2"}`,
	}, "\n"), *snapshot.Messages[len(snapshot.Messages)-1].Text)
	executed := stack.executedStepTypes("ordered")
	require.Equal(t, []string{"StartAgentSession", "RunModel"}, executed[:2])
	require.Equal(t, 2, countOf(executed, "CallSingleAttemptTool"))
	require.Equal(t, 2, countOf(executed, "ResumeTurn"), "the second write starts after the first one's result")
	require.Equal(t, 3, countOf(executed, "RunModel"))

	isQueued, err := sessions.SendUserMessage(ctx, "ordered", "note reject | send never")
	require.NoError(t, err)
	require.True(t, isQueued)
	snapshot = stack.waitForTurnCount(connectorAgentFlowType, "ordered", 2)
	require.Equal(t, strings.Join([]string{
		"fake-ledger_post_note: The tool call failed: fake-ledger postNote returned providerRejected: PROVIDER_REJECTION: account is closed",
		`fake-ledger_get_balance: {"accountId":"a-1","amount":1250}`,
		"fake-ledger_send_message: The tool call failed: " + dexagents.ToolCallNotRunErrorPrefix + ": " +
			dexagents.ToolCallSkippedAfterWriteDetail,
	}, "\n"), *snapshot.Messages[len(snapshot.Messages)-1].Text)
	require.Equal(t, 1, stack.ledger().Calls("sendMessage"), "the skipped write never ran")
	require.Nil(t, snapshot.State.LastTurnError)
}

// A queued write whose metadata recommends approval raises it only when the write before it succeeded.
func TestAQueuedConnectorWriteAsksForApprovalOnlyWhenItsTurnComes(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	stack.startWorker()
	sessions := stack.sessions(connectorAgentFlowType)
	ctx := context.Background()

	_, err := sessions.StartSession(ctx, "queued", ptr("notepay gate-after-send:note-gate"), dex.StartFlowOptions{})
	require.NoError(t, err)
	stack.waitForMarker("note-gate.reached")
	waiting, err := sessions.GetAgentSnapshot(ctx, "queued")
	require.NoError(t, err)
	require.Len(t, waiting.ToolCalls, 2)
	require.Equal(t, "fake-ledger_create_payout", waiting.ToolCalls[1].ToolName)
	require.True(t, waiting.ToolCalls[1].WriteQueued)
	require.True(t, waiting.ToolCalls[1].OrderedWrite)
	require.Equal(t, dexagents.ToolCallStatusApproved, waiting.ToolCalls[1].Status)
	require.Empty(t, waiting.PendingInterrupts, "the queued payout asks nobody yet")

	stack.releaseMarker("note-gate")
	snapshot := stack.waitForPendingInterrupts(connectorAgentFlowType, "queued", 1)
	require.Equal(t, "fake-ledger_create_payout", snapshot.PendingInterrupts[0].ToolName)
	require.Equal(t, dexagents.ToolCallStatusSucceeded, snapshot.ToolCalls[0].Status)
	require.False(t, snapshot.ToolCalls[1].WriteQueued)
	isResolved, err := sessions.ResolveInterrupt(ctx, "queued", snapshot.PendingInterrupts[0].InterruptID, true, nil)
	require.NoError(t, err)
	require.True(t, isResolved)
	snapshot = stack.waitForTurnCount(connectorAgentFlowType, "queued", 1)
	require.Equal(t, strings.Join([]string{
		`fake-ledger_post_note: {"id":"postNote-1"}`,
		`fake-ledger_create_payout: {"amount":9,"payoutId":"createPayout-2"}`,
	}, "\n"), *snapshot.Messages[len(snapshot.Messages)-1].Text)
}

func countOf(values []string, value string) int {
	count := 0
	for _, candidate := range values {
		if candidate == value {
			count++
		}
	}
	return count
}
