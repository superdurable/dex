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

// toolNodeConnectorAgentFlowType is the connector agent behind toolNodeMiddlewareRunner.
const toolNodeConnectorAgentFlowType = "ToolNodeConnectorAgent"

// newConnectorAgentFlow builds the connector agent around the provider files in coordinationDir.
func newConnectorAgentFlow(coordinationDir string) (dex.Flow, error) {
	return newLedgerAgentFlow(coordinationDir, connectorAgentFlowType,
		func(runner *builtinloop.BuiltinLoopAgentRunner) dexagents.AgentFrameworkRunner {
			return derivedContextRunner{runner}
		})
}

// newToolNodeConnectorAgentFlow builds the connector agent whose runner runs source calls inside a tool node.
func newToolNodeConnectorAgentFlow(coordinationDir string) (dex.Flow, error) {
	return newLedgerAgentFlow(coordinationDir, toolNodeConnectorAgentFlowType,
		func(runner *builtinloop.BuiltinLoopAgentRunner) dexagents.AgentFrameworkRunner {
			return toolNodeMiddlewareRunner{runner}
		})
}

// newLedgerAgentFlow builds a fake-ledger connector agent of flowType behind the runner that wrap returns.
func newLedgerAgentFlow(
	coordinationDir string,
	flowType string,
	wrap func(*builtinloop.BuiltinLoopAgentRunner) dexagents.AgentFrameworkRunner,
) (dex.Flow, error) {
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
	getBalanceOnce, err := sdkgo.NewQueryAgentTool[fakeledger.GetBalanceInput, fakeledger.Balance](
		fakeledger.GetBalance{Provider: provider, MaximumAttempts: 1}, fakeledger.Connection, fakeledger.GetBalanceMetadataWithAttempts(1))
	if err != nil {
		return nil, err
	}
	type configuredTool struct {
		agentTool sdkgo.AgentTool
		options   []connectortools.ConnectorToolOption
	}
	tools := []*connectortools.ConnectorTool{}
	for _, configured := range []configuredTool{
		{agentTool: getBalance, options: []connectortools.ConnectorToolOption{connectortools.WithMaxResultBytes(ledgerResultMaxBytes)}},
		{agentTool: createPayout},
		{agentTool: postNote},
		{agentTool: sendMessage},
		{agentTool: getBalanceOnce, options: []connectortools.ConnectorToolOption{connectortools.WithToolName("fake-ledger_get_balance_once")}},
		{agentTool: sendMessage, options: []connectortools.ConnectorToolOption{
			connectortools.WithToolName("fake-ledger_send_approved_message"), connectortools.WithRequiresApproval(true),
		}},
	} {
		tool, err := connectortools.NewConnectorTool(configured.agentTool, configured.options...)
		if err != nil {
			return nil, err
		}
		tools = append(tools, tool)
	}
	source, err := connectortools.NewConnectorToolSource(tools)
	if err != nil {
		return nil, err
	}
	agent := ledgerAgent{coordinationDir: coordinationDir}
	definition, err := builtinloop.NewAgentDefinition("ledger", "You manage ledger accounts.",
		agentstest.NewScriptedModelClient(agent.respond), builtinloop.WithToolSources(source))
	if err != nil {
		return nil, err
	}
	return dexagents.NewAgentSessionFlow(wrap(builtinloop.NewBuiltinLoopAgentRunner(definition)), flowType)
}

// ledgerResultMaxBytes bounds getBalance's results, so a padded balance reaches the model truncated.
const ledgerResultMaxBytes = 256

type derivedContextKey struct{}

// derivedContextRunner passes tools a context derived from the Step's, as a tracing span does.
type derivedContextRunner struct {
	*builtinloop.BuiltinLoopAgentRunner
}

func (runner derivedContextRunner) ExecuteDeferredToolCall(
	ctx context.Context,
	call dexagents.DeferredToolExecution,
) (dexagents.DeferredToolOutcome, error) {
	derived := context.WithValue(ctx, derivedContextKey{}, call.ToolCall.ToolCallID)
	return runner.BuiltinLoopAgentRunner.ExecuteDeferredToolCall(derived, call)
}

// toolNodeMiddlewareInvocations is how many times toolNodeMiddlewareRunner's retry middleware runs a failing call.
const toolNodeMiddlewareInvocations = 5

// toolNodeMiddlewareRunner runs every call, source calls too, the way a framework's tool node with
// middleware would: a retry middleware runs a failing call again within the attempt, and an error
// handler then returns the error as result text. A runner must route source calls before such code.
type toolNodeMiddlewareRunner struct {
	*builtinloop.BuiltinLoopAgentRunner
}

func (runner toolNodeMiddlewareRunner) ExecuteDeferredToolCall(
	ctx context.Context,
	call dexagents.DeferredToolExecution,
) (dexagents.DeferredToolOutcome, error) {
	var err error
	for invocation := 0; invocation < toolNodeMiddlewareInvocations; invocation++ {
		var outcome dexagents.DeferredToolOutcome
		outcome, err = runner.BuiltinLoopAgentRunner.ExecuteDeferredToolCall(ctx, call)
		if err == nil {
			return outcome, nil
		}
	}
	return &dexagents.ToolExecutionResult{Output: "tool error: " + err.Error()}, nil
}

// ledgerAgent is the connector agent's scripted model.
type ledgerAgent struct {
	coordinationDir string
}

// respond calls the tools the last user text names, then reports every result:
// "balance <account>", "send <text>", "payout <amount>", "note <text> | send <text>", which asks
// for a read and two writes in one response, and "notepay <text>", a note and then a payout. Also
// "bigbalance <padding>", "balanceonce <account>" with one attempt, "approvedsend <text>", which
// waits for approval, and "paynote <text>", a payout and then a note. A word "hold-final:<gate>"
// holds the model call that reports the results at that gate.
func (agent ledgerAgent) respond(request builtinloop.ModelRequest) (agentstest.ScriptedModelReply, error) {
	userTexts := agentstest.UserTexts(request)
	lastText, holdGate := cutHoldFinalGate(userTexts[len(userTexts)-1])
	results := agentstest.ToolResults(request)
	if len(results) > 0 {
		if holdGate != "" {
			writeMarker(agent.coordinationDir, holdGate+".reached")
			if err := waitForRelease(agent.coordinationDir, holdGate); err != nil {
				return agentstest.ScriptedModelReply{}, err
			}
		}
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
	case "bigbalance":
		var padding int
		if _, err := fmt.Sscan(argument, &padding); err != nil {
			return agentstest.ScriptedModelReply{}, err
		}
		return toolCalls(call("fake-ledger_get_balance", map[string]any{"accountId": "a-1", "padding": padding})), nil
	case "balanceonce":
		return toolCalls(call("fake-ledger_get_balance_once", map[string]any{"accountId": argument})), nil
	case "approvedsend":
		return toolCalls(call("fake-ledger_send_approved_message", map[string]any{"accountId": "a-1", "text": argument})), nil
	case "paynote":
		return toolCalls(
			call("fake-ledger_create_payout", map[string]any{"accountId": "a-1", "amount": 9}),
			call("fake-ledger_post_note", map[string]any{"accountId": "a-1", "text": argument}),
		), nil
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

// cutHoldFinalGate removes a "hold-final:<gate>" word from text and returns the gate, or "".
func cutHoldFinalGate(text string) (string, string) {
	words := []string{}
	gate := ""
	for _, word := range strings.Fields(text) {
		if name, isGate := strings.CutPrefix(word, "hold-final:"); isGate {
			gate = name
			continue
		}
		words = append(words, word)
	}
	return strings.Join(words, " "), gate
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

// heldTurnRecords reads the turn's final records at its held last model call; a finished turn deletes them.
func (stack *agentTestStack) heldTurnRecords(sessionID string, gate string) map[string]dexagents.AgentToolCall {
	stack.t.Helper()
	return stack.heldTurnRecordsOf(connectorAgentFlowType, sessionID, gate)
}

// heldTurnRecordsOf is heldTurnRecords for a session of flowType.
func (stack *agentTestStack) heldTurnRecordsOf(flowType string, sessionID string, gate string) map[string]dexagents.AgentToolCall {
	stack.t.Helper()
	stack.waitForMarker(gate + ".reached")
	snapshot, err := stack.sessions(flowType).GetAgentSnapshot(context.Background(), sessionID)
	require.NoError(stack.t, err)
	records := map[string]dexagents.AgentToolCall{}
	for _, record := range snapshot.ToolCalls {
		require.True(stack.t, record.Status.IsFinal(), "%s is %s", record.ToolName, record.Status)
		records[record.ToolName] = record
	}
	stack.releaseMarker(gate)
	return records
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

// A limited write whose responses are all lost records outcome_unknown at its send cap, with attempts left.
func TestALimitedConnectorWriteThatNeverConfirmsRecordsOutcomeUnknownAtItsCap(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	stack.startWorker()
	sessions := stack.sessions(connectorAgentFlowType)

	_, err := sessions.StartSession(context.Background(), "lost", ptr("send lose-always hold-final:lost"), dex.StartFlowOptions{})
	require.NoError(t, err)
	record := stack.heldTurnRecords("lost", "lost")["fake-ledger_send_message"]
	snapshot := stack.waitForTurnCount(connectorAgentFlowType, "lost", 1)
	require.Nil(t, snapshot.State.LastTurnError)
	require.Equal(t, dexagents.ToolCallStatusOutcomeUnknown, record.Status)
	require.Equal(t, "fake-ledger sendMessage returned uncertain: TRANSPORT: read-back found no message", *record.Error)
	require.Equal(t, dexagents.OutcomeUnknownToolResult, *snapshot.Messages[2].ToolOutput)
	require.Equal(t, 3, stack.ledger().Sends("sendMessage"), "the send cap")
	// Three lost sends, then one read-back at the cap; the operation allows five attempts.
	require.Equal(t, 4, stack.finalAttempt("lost", "CallSingleAttemptTool"))
	require.NotContains(t, stack.executedStepTypes("lost"), "RecordToolOutcomeUnknown")
}

// An approved limited write survives a restart while it waits, and reads back after a SIGKILL.
func TestAnApprovedConnectorWriteKilledAfterItsSendReadsBackOnTheRetry(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	worker := stack.startWorker()
	sessions := stack.sessions(connectorAgentFlowType)
	ctx := context.Background()

	_, err := sessions.StartSession(ctx, "approved-send",
		ptr("approvedsend hello gate-after-send:approved-send hold-final:approved-send-final"), dex.StartFlowOptions{})
	require.NoError(t, err)
	snapshot := stack.waitForPendingInterrupts(connectorAgentFlowType, "approved-send", 1)
	require.Equal(t, "fake-ledger_send_approved_message", snapshot.PendingInterrupts[0].ToolName)
	require.Equal(t, dexagents.ToolEffectNonIdempotentWrite, snapshot.ToolCalls[0].Effect)
	worker.Kill()
	worker = stack.startWorker()

	isResolved, err := sessions.ResolveInterrupt(ctx, "approved-send", snapshot.PendingInterrupts[0].InterruptID, true, nil)
	require.NoError(t, err)
	require.True(t, isResolved)
	stack.waitForMarker("approved-send.reached")
	worker.Kill()
	stack.startWorker()

	record := stack.heldTurnRecords("approved-send", "approved-send-final")["fake-ledger_send_approved_message"]
	require.Equal(t, dexagents.ToolCallStatusSucceeded, record.Status)
	snapshot = stack.waitForTurnCount(connectorAgentFlowType, "approved-send", 1)
	require.Equal(t, `{"id":"sendMessage-1"}`, *snapshot.Messages[2].ToolOutput)
	require.Equal(t, 1, stack.ledger().Sends("sendMessage"), "the retry read the write back instead of sending again")
	require.Equal(t, 2, stack.finalAttempt("approved-send", "CallSingleAttemptTool"))
	require.Equal(t, []string{"StartAgentSession", "RunModel", "AwaitToolApproval", "CallSingleAttemptTool", "ResumeTurn", "RunModel"},
		stack.executedStepTypes("approved-send"))
}

// A Connector result longer than its bound reaches the model truncated, and the record keeps the full size.
func TestATruncatedConnectorResultRecordsItsFullSize(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	stack.startWorker()
	sessions := stack.sessions(connectorAgentFlowType)

	_, err := sessions.StartSession(context.Background(), "big", ptr("bigbalance 400 hold-final:big"), dex.StartFlowOptions{})
	require.NoError(t, err)
	record := stack.heldTurnRecords("big", "big")["fake-ledger_get_balance"]
	full := `{"accountId":"a-1","amount":1250,"filler":"` + strings.Repeat("x", 400) + `"}`
	require.Equal(t, dexagents.ToolCallStatusSucceeded, record.Status)
	require.NotNil(t, record.TruncatedOutputBytes)
	require.Equal(t, int64(len(full)), *record.TruncatedOutputBytes)
	require.Equal(t, full[:ledgerResultMaxBytes]+"\n"+strings.ReplaceAll(connectortools.ConnectorResultTruncatedNote,
		"{total_bytes}", fmt.Sprint(len(full))), *record.Output)
	snapshot := stack.waitForTurnCount(connectorAgentFlowType, "big", 1)
	require.Equal(t, *record.Output, *snapshot.Messages[2].ToolOutput)
}

// A Connector Query keeps its operation's attempt limit in CallTool, here one attempt instead of three.
func TestAConnectorQueryKeepsItsOperationsAttemptLimit(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	stack.startWorker()
	sessions := stack.sessions(connectorAgentFlowType)

	_, err := sessions.StartSession(context.Background(), "read-once", ptr("balanceonce retry-once hold-final:read-once"),
		dex.StartFlowOptions{})
	require.NoError(t, err)
	record := stack.heldTurnRecords("read-once", "read-once")["fake-ledger_get_balance_once"]
	require.Equal(t, dexagents.ToolCallStatusFailed, record.Status)
	stack.waitForTurnCount(connectorAgentFlowType, "read-once", 1)
	require.Equal(t, 1, stack.ledger().Calls("getBalance"), "the retry the connector asked for had no attempt left")
	require.Contains(t, stack.executedStepTypes("read-once"), "RecordToolFailure")
}

// A rejected approval of the first Connector write skips the later write of the response without running it.
func TestARejectedConnectorWriteSkipsTheLaterWrites(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	stack.startWorker()
	sessions := stack.sessions(connectorAgentFlowType)
	ctx := context.Background()

	_, err := sessions.StartSession(ctx, "rejected", ptr("paynote after the payout hold-final:rejected"), dex.StartFlowOptions{})
	require.NoError(t, err)
	snapshot := stack.waitForPendingInterrupts(connectorAgentFlowType, "rejected", 1)
	require.Equal(t, "fake-ledger_create_payout", snapshot.PendingInterrupts[0].ToolName)
	require.True(t, snapshot.ToolCalls[1].WriteQueued)
	isResolved, err := sessions.ResolveInterrupt(ctx, "rejected", snapshot.PendingInterrupts[0].InterruptID, false, ptr("not today"))
	require.NoError(t, err)
	require.True(t, isResolved)

	records := stack.heldTurnRecords("rejected", "rejected")
	require.Equal(t, dexagents.ToolCallStatusRejected, records["fake-ledger_create_payout"].Status)
	skipped := records["fake-ledger_post_note"]
	require.Equal(t, dexagents.ToolCallStatusFailed, skipped.Status)
	require.True(t, skipped.Skipped)
	require.Equal(t, dexagents.ToolCallNotRunErrorPrefix+": "+dexagents.ToolCallSkippedAfterWriteDetail, *skipped.Error)
	require.Zero(t, stack.ledger().Calls("postNote"))
	require.Zero(t, stack.ledger().Calls("createPayout"))
	stack.waitForTurnCount(connectorAgentFlowType, "rejected", 1)
}

// A single-attempt write SIGKILLed mid-call records outcome_unknown and skips the later write; the read succeeds.
func TestASingleAttemptConnectorWriteKilledMidCallSkipsTheLaterWrites(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	worker := stack.startWorker()
	sessions := stack.sessions(connectorAgentFlowType)

	_, err := sessions.StartSession(context.Background(), "kill-note",
		ptr("note gate-after-send:kill-note | send after hold-final:kill-note-final"), dex.StartFlowOptions{})
	require.NoError(t, err)
	stack.waitForMarker("kill-note.reached")
	worker.Kill()
	stack.startWorker()

	records := stack.heldTurnRecords("kill-note", "kill-note-final")
	require.Equal(t, dexagents.ToolCallStatusOutcomeUnknown, records["fake-ledger_post_note"].Status)
	require.Equal(t, dexagents.ToolCallStatusSucceeded, records["fake-ledger_get_balance"].Status)
	require.True(t, records["fake-ledger_send_message"].Skipped)
	require.Equal(t, dexagents.ToolCallNotRunErrorPrefix+": "+dexagents.ToolCallSkippedAfterWriteDetail,
		*records["fake-ledger_send_message"].Error)
	require.Equal(t, []string{"postNote-1"}, stack.ledger().Writes("postNote"))
	require.Equal(t, 1, stack.ledger().Calls("postNote"), "the note never ran again")
	require.Zero(t, stack.ledger().Calls("sendMessage"))
	stack.waitForTurnCount(connectorAgentFlowType, "kill-note", 1)
	require.Contains(t, stack.executedStepTypes("kill-note"), "RecordToolOutcomeUnknown")
}

// A runner that runs a limited write inside a retry middleware within one attempt sends no more
// than the send cap allows across attempts, and an error handler around it cannot hide the outcome.
func TestASourceCallRunsOncePerAttemptWhateverMiddlewareTheRunnerRunsItIn(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	stack.startWorker()
	sessions := stack.sessions(toolNodeConnectorAgentFlowType)

	_, err := sessions.StartSession(context.Background(), "node-lost", ptr("send lose-always hold-final:node-lost"), dex.StartFlowOptions{})
	require.NoError(t, err)
	record := stack.heldTurnRecordsOf(toolNodeConnectorAgentFlowType, "node-lost", "node-lost")["fake-ledger_send_message"]
	snapshot := stack.waitForTurnCount(toolNodeConnectorAgentFlowType, "node-lost", 1)
	require.Nil(t, snapshot.State.LastTurnError)
	require.Equal(t, dexagents.ToolCallStatusOutcomeUnknown, record.Status)
	require.Equal(t, "fake-ledger sendMessage returned uncertain: TRANSPORT: read-back found no message", *record.Error)
	require.Equal(t, 3, stack.ledger().Sends("sendMessage"), "the send cap holds although the middleware ran the call again")
	require.Equal(t, 4, stack.finalAttempt("node-lost", "CallSingleAttemptTool"))
}

// A single-attempt write whose outcome is unknown skips the later writes, although an error handler
// in the runner turned its error into result text.
func TestAnUnknownSourceOutcomeSkipsTheLaterWritesWhateverTheRunnersErrorHandlerReturns(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	stack.startWorker()
	sessions := stack.sessions(toolNodeConnectorAgentFlowType)

	_, err := sessions.StartSession(context.Background(), "node-note",
		ptr("note retry | send after hold-final:node-note"), dex.StartFlowOptions{})
	require.NoError(t, err)
	records := stack.heldTurnRecordsOf(toolNodeConnectorAgentFlowType, "node-note", "node-note")
	require.Equal(t, dexagents.ToolCallStatusOutcomeUnknown, records["fake-ledger_post_note"].Status)
	require.True(t, records["fake-ledger_send_message"].Skipped)
	require.Equal(t, dexagents.ToolCallStatusSucceeded, records["fake-ledger_get_balance"].Status)
	require.Equal(t, 1, stack.ledger().Calls("postNote"), "the middleware's retries never reached the operation")
	require.Zero(t, stack.ledger().Calls("sendMessage"), "the write after an unknown outcome never ran")
	stack.waitForTurnCount(toolNodeConnectorAgentFlowType, "node-note", 1)
	require.Contains(t, stack.executedStepTypes("node-note"), "RecordToolOutcomeUnknown")
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
