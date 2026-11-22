// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package builtinloop_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/agents/go/core/agentstest"
	"github.com/superdurable/dex/agents/go/core/builtinloop"
)

// ledgerSource lists one write tool and records the calls it runs.
type ledgerSource struct {
	executed []dexagents.DeferredToolExecution
}

func (*ledgerSource) SourceName() string { return "ledger" }

func (*ledgerSource) ListTools(context.Context) ([]dexagents.SourceToolDefinition, error) {
	return []dexagents.SourceToolDefinition{{
		Name: "send_message", Description: "Send a message.", InputSchema: json.RawMessage(`{"type":"object"}`),
	}}, nil
}

func (*ledgerSource) DeclaredToolNames() []string { return []string{"send_message"} }

func (*ledgerSource) DeferredToolCall(_ dexagents.AgentTurnContext, call dexagents.SourceToolCall) (dexagents.DeferredToolCall, error) {
	reference := `{"tool": "send_message"}`
	isApprovalRequired := false
	return dexagents.DeferredToolCall{
		ToolCallID: call.ToolCallID, ToolName: call.ToolName, Arguments: call.Arguments, ToolSourceReference: &reference,
		Policy:  dexagents.ToolPolicy{Effect: dexagents.ToolEffectNonIdempotentWrite, RequiresApproval: &isApprovalRequired},
		Ordered: true,
	}, nil
}

func (source *ledgerSource) ExecuteDeferredToolCall(_ context.Context, call dexagents.DeferredToolExecution) (dexagents.DeferredToolOutcome, error) {
	source.executed = append(source.executed, call)
	return &dexagents.ToolExecutionResult{Output: "sent"}, nil
}

func (*ledgerSource) ToolCallStepOptions(dexagents.AgentToolCall) (*dexagents.ToolCallStepOptions, error) {
	return &dexagents.ToolCallStepOptions{ExecuteMethodTimeout: 30 * time.Second}, nil
}

func TestABuiltinLoopOffersSourceToolsAfterFunctionToolsAndRunsTheirCallsThroughTheSource(t *testing.T) {
	lookup, err := builtinloop.NewFunctionTool("lookup_order", "Look up an order.", json.RawMessage(`{"type":"object"}`),
		dexagents.ToolEffectRead, func(context.Context, json.RawMessage, builtinloop.ToolCallContext) (string, error) {
			return "shipped", nil
		})
	require.NoError(t, err)
	source := &ledgerSource{}
	model := agentstest.NewScriptedModelClient(func(builtinloop.ModelRequest) (agentstest.ScriptedModelReply, error) {
		return agentstest.ScriptedModelReply{ToolCalls: []agentstest.ScriptedToolCall{
			{ToolName: "send_message", Arguments: `{"text": "hi"}`},
			{ToolName: "lookup_order", Arguments: `{}`},
			{ToolName: "ghost_tool", Arguments: `{}`},
		}}, nil
	})
	definition, err := builtinloop.NewAgentDefinition("support", "Help.", model,
		builtinloop.WithFunctionTools(lookup), builtinloop.WithToolSources(source))
	require.NoError(t, err)
	runner := builtinloop.NewBuiltinLoopAgentRunner(definition)
	userMessage, err := runner.BuildUserMessage("send it")
	require.NoError(t, err)
	turnID := "turn-1"
	turn := dexagents.AgentTurnContext{
		FlowID: "session-1", TurnID: turnID, Attempt: 1,
		Messages: []dexagents.AgentMessage{{
			Sequence: 1, TurnID: turnID, Role: dexagents.MessageRoleUser, Text: userMessage.Text, FrameworkItem: userMessage.FrameworkItem,
		}},
		ToolCalls: map[string]dexagents.AgentToolCall{}, ToolPolicies: map[string]dexagents.ToolPolicy{},
		DefaultToolEffect: dexagents.ToolEffectUnclassified, SessionSettings: map[string]any{},
		Events: dexagents.NewAgentEventWriter(func(dexagents.AgentEvent) error { return nil }, &turnID, 1, "RunModel-1"),
	}

	outcome, err := runner.RunModelStep(context.Background(), turn)
	require.NoError(t, err)
	toolNames := []string{}
	for _, tool := range model.Requests()[0].Tools {
		toolNames = append(toolNames, tool.Name)
	}
	require.Equal(t, []string{"lookup_order", "send_message"}, toolNames)
	requested := outcome.(*dexagents.ToolCallsRequested)
	require.Len(t, requested.ToolCalls, 3)
	send, lookupCall, ghost := requested.ToolCalls[0], requested.ToolCalls[1], requested.ToolCalls[2]
	require.Equal(t, `{"stepwise_tool_source": "ledger", "reference": "{\"tool\": \"send_message\"}"}`, *send.ToolSourceReference)
	require.True(t, send.Ordered)
	require.Equal(t, dexagents.ToolEffectNonIdempotentWrite, send.Policy.Effect)
	require.Nil(t, lookupCall.ToolSourceReference)
	require.Equal(t, dexagents.ToolEffectRead, lookupCall.Policy.Effect)
	require.Equal(t, dexagents.ToolEffectUnclassified, ghost.Policy.Effect, "an unlisted name runs once")
	require.False(t, *ghost.Policy.RequiresApproval, "and never waits for approval")

	sendRecord := dexagents.AgentToolCall{ToolCallID: send.ToolCallID, TurnID: turnID, ToolName: send.ToolName,
		Arguments: send.Arguments, ToolSourceReference: send.ToolSourceReference}
	result, err := runner.ExecuteDeferredToolCall(context.Background(), dexagents.DeferredToolExecution{ToolCall: sendRecord})
	require.NoError(t, err)
	require.Equal(t, "sent", result.(*dexagents.ToolExecutionResult).Output)
	require.Equal(t, `{"tool": "send_message"}`, *source.executed[0].ToolCall.ToolSourceReference)
	options, err := runner.ToolCallStepOptions(sendRecord)
	require.NoError(t, err)
	require.Equal(t, 30*time.Second, options.ExecuteMethodTimeout)
	options, err = runner.ToolCallStepOptions(dexagents.AgentToolCall{ToolName: "lookup_order"})
	require.NoError(t, err)
	require.Nil(t, options, "a function tool keeps the defaults")

	_, err = runner.ExecuteDeferredToolCall(context.Background(), dexagents.DeferredToolExecution{
		ToolCall: dexagents.AgentToolCall{ToolName: "ghost_tool", Arguments: "{}"},
	})
	var toolError *dexagents.ToolExecutionError
	require.ErrorAs(t, err, &toolError)
	require.Equal(t, "unknown tool ghost_tool", toolError.Message)
}

func TestABuiltinLoopRefusesASourceToolNamedLikeAFunctionTool(t *testing.T) {
	sendMessage, err := builtinloop.NewFunctionTool("send_message", "Send.", json.RawMessage(`{"type":"object"}`),
		dexagents.ToolEffectRead, func(context.Context, json.RawMessage, builtinloop.ToolCallContext) (string, error) {
			return "", nil
		})
	require.NoError(t, err)
	model := agentstest.NewScriptedModelClient(func(builtinloop.ModelRequest) (agentstest.ScriptedModelReply, error) {
		return agentstest.ScriptedModelReply{Text: "done"}, nil
	})
	_, err = builtinloop.NewAgentDefinition("support", "Help.", model,
		builtinloop.WithFunctionTools(sendMessage), builtinloop.WithToolSources(&ledgerSource{}))
	require.EqualError(t, err, "tool send_message of a tool source uses the name of a framework tool")
	_, err = builtinloop.NewAgentDefinition("support", "Help.", model,
		builtinloop.WithToolSources(&ledgerSource{}), builtinloop.WithToolSourceListingTimeout(-time.Second))
	require.ErrorContains(t, err, "listing timeout must be positive")
}
