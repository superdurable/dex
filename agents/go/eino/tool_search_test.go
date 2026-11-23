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
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/dynamictool/toolsearch"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"

	dexagents "github.com/superdurable/dex/agents/go/core"
	dexeino "github.com/superdurable/dex/agents/go/eino"
)

// deferredToolsModel is a scriptedChatModel that also records each call's deferred tools and tool search tool.
type deferredToolsModel struct {
	*scriptedChatModel

	mutex      sync.Mutex
	deferred   [][]string
	toolSearch []string
}

func (recording *deferredToolsModel) note(options []model.Option) {
	recording.mutex.Lock()
	defer recording.mutex.Unlock()
	common := model.GetCommonOptions(&model.Options{}, options...)
	var names []string
	for _, info := range common.DeferredTools {
		names = append(names, info.Name)
	}
	recording.deferred = append(recording.deferred, names)
	toolSearch := ""
	if common.ToolSearchTool != nil {
		toolSearch = common.ToolSearchTool.Name
	}
	recording.toolSearch = append(recording.toolSearch, toolSearch)
}

func (recording *deferredToolsModel) Generate(ctx context.Context, input []*schema.Message, options ...model.Option) (*schema.Message, error) {
	recording.note(options)
	return recording.scriptedChatModel.Generate(ctx, input, options...)
}

func (recording *deferredToolsModel) Stream(ctx context.Context, input []*schema.Message, options ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	recording.note(options)
	return recording.scriptedChatModel.Stream(ctx, input, options...)
}

func (recording *deferredToolsModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return recording, nil
}

// With the tool search middleware in native mode, the model is offered the dynamic tools through
// model.WithDeferredTools and the tool search tool through model.WithToolSearchTool. A call of
// either gets the effect the options give it, as for any Handler-added tool, and runs in its
// tool Step, with and without tool sources.
func TestNativeToolSearchToolsGetTheirEffectAndRun(t *testing.T) {
	for name, withSources := range map[string]bool{"without tool sources": false, "with tool sources": true} {
		t.Run(name, func(t *testing.T) {
			var cancelRuns atomic.Int64
			cancelOrder := funcTool{name: "cancel_order", run: func(context.Context, string) (string, error) {
				cancelRuns.Add(1)
				return "cancelled", nil
			}}
			search, err := toolsearch.New(context.Background(), &toolsearch.Config{
				DynamicTools: []tool.BaseTool{cancelOrder}, UseModelToolSearch: true})
			require.NoError(t, err)
			scripted := &deferredToolsModel{scriptedChatModel: &scriptedChatModel{respond: func(input []*schema.Message) (*schema.Message, error) {
				if last := input[len(input)-1]; last.Role == schema.Tool {
					return schema.AssistantMessage(last.Content, nil), nil
				}
				return toolCallMessage(call("call_search", "tool_search", `{"query":"select:cancel_order"}`),
					call("call_cancel", "cancel_order", `{"id":"7"}`)), nil
			}}}
			config := &adk.ChatModelAgentConfig{Name: "agent", Description: "Test agent.", Model: scripted,
				Handlers: []adk.ChatModelAgentMiddleware{search}}
			options := []dexeino.Option{dexeino.WithToolEffects(map[string]dexagents.ToolEffect{
				"cancel_order": dexagents.ToolEffectNonIdempotentWrite, "tool_search": dexagents.ToolEffectRead})}
			if withSources {
				options = append(options, dexeino.WithToolSources(noteSource()))
			}
			runner, err := dexeino.NewRunner(config, options...)
			require.NoError(t, err)
			state := newTurnState(t, runner, "cancel order 7")
			requested := state.runModel(runner).(*dexagents.ToolCallsRequested)

			require.Equal(t, []string{"cancel_order"}, scripted.deferred[0], "the dynamic tool is offered as deferred")
			require.Equal(t, "tool_search", scripted.toolSearch[0])
			require.NotContains(t, scripted.tools[0], "cancel_order")
			require.NotContains(t, scripted.tools[0], "tool_search")
			policies := map[string]dexagents.ToolPolicy{}
			for _, deferred := range requested.ToolCalls {
				policies[deferred.ToolName] = deferred.Policy
			}
			require.Equal(t, dexagents.ToolEffectNonIdempotentWrite, policies["cancel_order"].Effect)
			require.True(t, policies["cancel_order"].IsApprovalRequired())
			require.Equal(t, dexagents.ToolEffectRead, policies["tool_search"].Effect)
			require.False(t, policies["tool_search"].IsApprovalRequired())

			require.Empty(t, state.runTools(runner))
			require.Equal(t, int64(1), cancelRuns.Load())
			require.Equal(t, "cancelled", *state.records["call_cancel"].Output)
			require.Equal(t, "[tool_search_result output omitted: Dex stores text tool results]",
				*state.records["call_search"].Output, "the README names this limit")
		})
	}
}

// In client-side mode, the tool search result is stored as text, so the next model call offers the
// tools it found, and a call of one gets its effect.
func TestClientSideToolSearchOffersTheFoundToolsInTheNextModelCall(t *testing.T) {
	var cancelRuns atomic.Int64
	cancelOrder := funcTool{name: "cancel_order", run: func(context.Context, string) (string, error) {
		cancelRuns.Add(1)
		return "cancelled", nil
	}}
	search, err := toolsearch.New(context.Background(), &toolsearch.Config{DynamicTools: []tool.BaseTool{cancelOrder}})
	require.NoError(t, err)
	scripted := &scriptedChatModel{respond: func(input []*schema.Message) (*schema.Message, error) {
		switch last := input[len(input)-1]; {
		case last.Role == schema.Tool && last.ToolName == "tool_search":
			return toolCallMessage(call("call_cancel", "cancel_order", `{"id":"7"}`)), nil
		case last.Role == schema.Tool:
			return schema.AssistantMessage(last.Content, nil), nil
		}
		return toolCallMessage(call("call_search", "tool_search", `{"query":"select:cancel_order"}`)), nil
	}}
	runner, err := dexeino.NewRunner(&adk.ChatModelAgentConfig{Name: "agent", Description: "Test agent.", Model: scripted,
		Handlers: []adk.ChatModelAgentMiddleware{search}}, dexeino.WithToolEffects(map[string]dexagents.ToolEffect{
		"cancel_order": dexagents.ToolEffectIdempotentWrite, "tool_search": dexagents.ToolEffectRead}))
	require.NoError(t, err)
	state := newTurnState(t, runner, "cancel order 7")
	state.runModel(runner)
	require.Equal(t, []string{"tool_search"}, scripted.tools[0])
	require.Empty(t, state.runTools(runner))
	require.JSONEq(t, `{"matches":["cancel_order"]}`, *state.records["call_search"].Output)

	requested := state.runModel(runner).(*dexagents.ToolCallsRequested)
	require.Equal(t, []string{"tool_search", "cancel_order"}, scripted.tools[1])
	require.Equal(t, dexagents.ToolEffectIdempotentWrite, requested.ToolCalls[0].Policy.Effect)
	require.Empty(t, state.runTools(runner))
	require.Equal(t, int64(1), cancelRuns.Load())
}

// A deferred tool without an effect of its own gets the default effect, with its approval, in
// RunModel and in its tool Step, so it runs once approved instead of being refused.
func TestDeferredToolWithoutAnEffectGetsTheDefault(t *testing.T) {
	var runs atomic.Int64
	archive := funcTool{name: "archive_order", run: func(context.Context, string) (string, error) { runs.Add(1); return "archived", nil }}
	search, err := toolsearch.New(context.Background(), &toolsearch.Config{
		DynamicTools: []tool.BaseTool{archive}, UseModelToolSearch: true})
	require.NoError(t, err)
	scripted := &deferredToolsModel{scriptedChatModel: &scriptedChatModel{respond: func([]*schema.Message) (*schema.Message, error) {
		return toolCallMessage(call("call_archive", "archive_order", `{"id":"7"}`)), nil
	}}}
	runner, err := dexeino.NewRunner(&adk.ChatModelAgentConfig{Name: "agent", Description: "Test agent.", Model: scripted,
		Handlers: []adk.ChatModelAgentMiddleware{search}})
	require.NoError(t, err)
	state := newTurnState(t, runner, "archive order 7")
	requested := state.runModel(runner).(*dexagents.ToolCallsRequested)
	require.Equal(t, dexagents.ToolEffectUnclassified, requested.ToolCalls[0].Policy.Effect)
	require.True(t, requested.ToolCalls[0].Policy.IsApprovalRequired())
	require.Empty(t, state.runTools(runner))
	require.Equal(t, int64(1), runs.Load())
}
