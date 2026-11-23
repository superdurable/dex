// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dexeino_test

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"

	dexagents "github.com/superdurable/dex/agents/go/core"
	dexeino "github.com/superdurable/dex/agents/go/eino"
)

// lookupAgent returns an agent whose model looks an order up once, then answers with the result.
func lookupAgent(t *testing.T, toolRuns *atomic.Int64) (*adk.ChatModelAgentConfig, *scriptedChatModel) {
	t.Helper()
	scripted := &scriptedChatModel{respond: func(input []*schema.Message) (*schema.Message, error) {
		last := input[len(input)-1]
		if last.Role == schema.Tool {
			return schema.AssistantMessage("Order status: "+last.Content, nil), nil
		}
		return toolCallMessage(call("", "lookup_order", `{"id":"7"}`)), nil
	}}
	lookup := funcTool{name: "lookup_order", run: func(_ context.Context, arguments string) (string, error) {
		toolRuns.Add(1)
		return "order " + decodeArguments(t, arguments)["id"].(string) + " is shipped", nil
	}}
	return &adk.ChatModelAgentConfig{
		Name: "support", Description: "Support agent.", Instruction: "You help customers.", Model: scripted,
		ToolsConfig: adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: []tool.BaseTool{lookup}}},
	}, scripted
}

// A turn makes one model call per RunModelStep, defers the tool call, and runs the tool only in its tool Step.
func TestRunModelStepDefersToolCallsAndReplaysTheirResults(t *testing.T) {
	var toolRuns atomic.Int64
	config, scripted := lookupAgent(t, &toolRuns)
	runner, err := dexeino.NewRunner(config, dexeino.WithToolEffects(map[string]dexagents.ToolEffect{
		"lookup_order": dexagents.ToolEffectRead,
	}))
	require.NoError(t, err)
	state := newTurnState(t, runner, "lookup 7")

	requested, isRequested := state.runModel(runner).(*dexagents.ToolCallsRequested)
	require.True(t, isRequested)
	require.Equal(t, 1, scripted.calls())
	require.Zero(t, toolRuns.Load(), "no tool runs inside RunModel")
	require.Len(t, requested.ToolCalls, 1)
	require.Equal(t, "RunModel-1-0", requested.ToolCalls[0].ToolCallID, "an empty ID gets a synthesized one")
	require.Equal(t, dexagents.ToolEffectRead, requested.ToolCalls[0].Policy.Effect)
	require.Equal(t, int64(1), requested.Usage.ModelCalls)
	require.Len(t, requested.NewMessages, 1)
	require.Equal(t, dexagents.MessageRoleAssistant, requested.NewMessages[0].Role)
	require.NotContains(t, requested.NewMessages[0].FrameworkItem, "_eino_msg_id")

	require.Empty(t, state.runTools(runner))
	require.Equal(t, int64(1), toolRuns.Load())

	completed, isCompleted := state.runModel(runner).(*dexagents.TurnCompleted)
	require.True(t, isCompleted)
	require.Equal(t, 2, scripted.calls())
	require.Len(t, completed.NewMessages, 2)
	require.Equal(t, dexagents.MessageRoleTool, completed.NewMessages[0].Role)
	require.Equal(t, "Order status: order 7 is shipped", *completed.NewMessages[1].Text)
	input := scripted.lastInput()
	require.Equal(t, schema.System, input[0].Role)
	require.Equal(t, "You help customers.", input[0].Content)
	require.Equal(t, []schema.RoleType{schema.System, schema.User, schema.Assistant, schema.Tool}, roles(input))
	require.True(t, strings.HasPrefix(input[3].ToolCallID, "RunModel-1"))
}

func roles(messages []*schema.Message) []schema.RoleType {
	result := make([]schema.RoleType, 0, len(messages))
	for _, message := range messages {
		result = append(result, message.Role)
	}
	return result
}

// Two tool rounds store one result per call; later model calls add only results not stored yet.
func TestTwoToolRoundsStoreOneResultPerCall(t *testing.T) {
	var toolRuns atomic.Int64
	lookup := funcTool{name: "lookup_order", run: func(_ context.Context, arguments string) (string, error) {
		toolRuns.Add(1)
		return "looked up " + arguments, nil
	}}
	scripted := &scriptedChatModel{respond: func(input []*schema.Message) (*schema.Message, error) {
		results := 0
		for _, message := range input {
			if message.Role == schema.Tool {
				results++
			}
		}
		switch results {
		case 0:
			return toolCallMessage(call("call_a", "lookup_order", `{"id":"1"}`)), nil
		case 1:
			return toolCallMessage(call("call_b", "lookup_order", `{"id":"2"}`)), nil
		default:
			return schema.AssistantMessage(fmt.Sprintf("done after %d results", results), nil), nil
		}
	}}
	config := &adk.ChatModelAgentConfig{Name: "agent", Description: "Test agent.", Model: scripted,
		ToolsConfig: adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: []tool.BaseTool{lookup}}}}
	runner, err := dexeino.NewRunner(config, dexeino.WithToolEffects(map[string]dexagents.ToolEffect{
		"lookup_order": dexagents.ToolEffectRead,
	}))
	require.NoError(t, err)
	state := newTurnState(t, runner, "two rounds")
	state.runModel(runner)
	require.Empty(t, state.runTools(runner))
	second := state.runModel(runner).(*dexagents.ToolCallsRequested)
	require.Equal(t, []dexagents.MessageRole{dexagents.MessageRoleTool, dexagents.MessageRoleAssistant}, frameworkRoles(second.NewMessages))
	require.Equal(t, "call_a", *second.NewMessages[0].ToolCallID)
	require.Empty(t, state.runTools(runner))

	unfinished, err := runner.BuildUnfinishedTurnMessages(context.Background(), state.context())
	require.NoError(t, err)
	require.Len(t, unfinished, 1, "only the second round's result is not stored yet")
	require.Equal(t, "call_b", *unfinished[0].ToolCallID)

	completed := state.runModel(runner).(*dexagents.TurnCompleted)
	require.Nil(t, completed.TurnError)
	require.Equal(t, "done after 2 results", *completed.NewMessages[len(completed.NewMessages)-1].Text)
	storedResults := map[string]int{}
	var storedRoles []dexagents.MessageRole
	for _, message := range state.messages {
		storedRoles = append(storedRoles, message.Role)
		if message.Role == dexagents.MessageRoleTool {
			storedResults[*message.ToolCallID]++
		}
	}
	require.Equal(t, map[string]int{"call_a": 1, "call_b": 1}, storedResults)
	require.Equal(t, []dexagents.MessageRole{dexagents.MessageRoleUser, dexagents.MessageRoleAssistant, dexagents.MessageRoleTool,
		dexagents.MessageRoleAssistant, dexagents.MessageRoleTool, dexagents.MessageRoleAssistant}, storedRoles)
	require.Equal(t, int64(2), toolRuns.Load())
}

func frameworkRoles(messages []dexagents.FrameworkMessage) []dexagents.MessageRole {
	result := make([]dexagents.MessageRole, 0, len(messages))
	for _, message := range messages {
		result = append(result, message.Role)
	}
	return result
}
