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
