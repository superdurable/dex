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
	"sync"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"

	dexagents "github.com/superdurable/dex/agents/go/core"
	dexeino "github.com/superdurable/dex/agents/go/eino"
)

// refundAgent is an agent whose one tool is a write that records the arguments it ran with.
type refundAgent struct {
	mutex   sync.Mutex
	ranWith []string
}

func (agent *refundAgent) tool() funcTool {
	return funcTool{name: "refund_order", run: func(_ context.Context, arguments string) (string, error) {
		agent.mutex.Lock()
		defer agent.mutex.Unlock()
		agent.ranWith = append(agent.ranWith, arguments)
		return "refunded", nil
	}}
}

func (agent *refundAgent) runs() []string {
	agent.mutex.Lock()
	defer agent.mutex.Unlock()
	return append([]string{}, agent.ranWith...)
}

func refundPolicies() dexeino.Option {
	return dexeino.WithToolPolicies(map[string]dexagents.ToolPolicy{
		"refund_order": {Effect: dexagents.ToolEffectNonIdempotentWrite, RequiresApproval: ptr(false)},
	})
}

// A Handler's AfterModelRewriteState or an AgentMiddleware's AfterChatModel that changes the model's
// message, keeping its tool call IDs, ends the turn as failed: Dex stores and runs the message the
// model returned, so the changed arguments, name, or text would otherwise be dropped.
func TestChangesAfterTheModelCallEndTheTurnAsFailed(t *testing.T) {
	clampArguments := func(message *schema.Message) {
		message.ToolCalls = []schema.ToolCall{call("call_1", "refund_order", `{"id":"1"}`)}
	}
	redactText := func(message *schema.Message) { message.Content = "card [redacted]" }
	for name, testCase := range map[string]struct {
		response   func() *schema.Message
		handler    func(message *schema.Message)
		middleware func(message *schema.Message)
		expected   string
	}{
		"a Handler clamps the arguments": {func() *schema.Message {
			return toolCallMessage(call("call_1", "refund_order", `{"id":"999999"}`))
		}, clampArguments, nil, "tool calls changed after the model call"},
		"a Handler repairs a tool name": {func() *schema.Message {
			return toolCallMessage(call("call_1", "refundOrder", `{"id":"1"}`))
		}, clampArguments, nil, "tool calls changed after the model call"},
		"a Handler redacts the text": {func() *schema.Message {
			return schema.AssistantMessage("card 4111-1111-1111-1111", nil)
		}, redactText, nil, "model message changed after the model call"},
		"a Handler redacts the text beside tool calls": {func() *schema.Message {
			return schema.AssistantMessage("card 4111-1111-1111-1111", []schema.ToolCall{call("call_1", "refund_order", `{"id":"1"}`)})
		}, redactText, nil, "model message changed after the model call"},
		"an AgentMiddleware clamps the arguments": {func() *schema.Message {
			return toolCallMessage(call("call_1", "refund_order", `{"id":"999999"}`))
		}, nil, clampArguments, "tool calls changed after the model call"},
		"an AgentMiddleware redacts the text": {func() *schema.Message {
			return schema.AssistantMessage("card 4111-1111-1111-1111", nil)
		}, nil, redactText, "model message changed after the model call"},
	} {
		for _, isStreaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s streaming=%v", name, isStreaming), func(t *testing.T) {
				refunds := &refundAgent{}
				scripted := &scriptedChatModel{respond: func([]*schema.Message) (*schema.Message, error) {
					return testCase.response(), nil
				}}
				config := &adk.ChatModelAgentConfig{Name: "agent", Description: "Test agent.", Model: scripted,
					ToolsConfig: toolsConfig(refunds.tool())}
				if testCase.handler != nil {
					config.Handlers = []adk.ChatModelAgentMiddleware{rewritingHandler{&adk.BaseChatModelAgentMiddleware{}, testCase.handler}}
				}
				if testCase.middleware != nil {
					rewrite := testCase.middleware
					config.Middlewares = []adk.AgentMiddleware{{AfterChatModel: func(_ context.Context, state *adk.ChatModelAgentState) error {
						last := *state.Messages[len(state.Messages)-1]
						rewrite(&last)
						state.Messages[len(state.Messages)-1] = &last
						return nil
					}}}
				}
				runner, err := dexeino.NewRunner(config, refundPolicies(), dexeino.WithStreaming(isStreaming))
				require.NoError(t, err)
				state := newTurnState(t, runner, "refund")
				outcome, err := runner.RunModelStep(context.Background(), state.context())
				completed := requireTurnError(t, outcome, err)
				require.Contains(t, *completed.TurnError, testCase.expected)
				require.Contains(t, *completed.TurnError, "WrapModel")
				require.Equal(t, int64(1), completed.Usage.ModelCalls, "the call that ran is billed")
				require.Empty(t, completed.NewMessages, "neither message is stored")
				require.Equal(t, 1, scripted.calls())
				require.Empty(t, refunds.runs())
			})
		}
	}
}

// clampingHandler rewrites the refund arguments in WrapModel, where Dex captures the message.
type clampingHandler struct {
	*adk.BaseChatModelAgentMiddleware
}

type clampingModel struct {
	inner model.BaseModel[*schema.Message]
}

func (clampingHandler) WrapModel(_ context.Context, inner model.BaseModel[*schema.Message], _ *adk.ModelContext) (model.BaseModel[*schema.Message], error) {
	return clampingModel{inner: inner}, nil
}

func (clamping clampingModel) Generate(ctx context.Context, input []*schema.Message, options ...model.Option) (*schema.Message, error) {
	message, err := clamping.inner.Generate(ctx, input, options...)
	if err != nil {
		return nil, err
	}
	return clampRefund(message), nil
}

func (clamping clampingModel) Stream(ctx context.Context, input []*schema.Message, options ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	stream, err := clamping.inner.Stream(ctx, input, options...)
	if err != nil {
		return nil, err
	}
	message, err := schema.ConcatMessageStream(stream)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{clampRefund(message)}), nil
}

func clampRefund(message *schema.Message) *schema.Message {
	if len(message.ToolCalls) == 0 {
		return message
	}
	clamped := *message
	clamped.ToolCalls = []schema.ToolCall{call(message.ToolCalls[0].ID, "refund_order", `{"id":"1"}`)}
	return &clamped
}

// A Handler that changes the model's message in WrapModel changes what Dex stores and runs, and an
// after-model hook that copies the message without changing it changes nothing.
func TestWrapModelChangesAreStoredAndRun(t *testing.T) {
	for _, isStreaming := range []bool{false, true} {
		t.Run(fmt.Sprintf("streaming=%v", isStreaming), func(t *testing.T) {
			refunds := &refundAgent{}
			scripted := &scriptedChatModel{respond: func(input []*schema.Message) (*schema.Message, error) {
				if input[len(input)-1].Role == schema.Tool {
					return schema.AssistantMessage("refunded order 1", nil), nil
				}
				return toolCallMessage(call("call_1", "refund_order", `{"id":"999999"}`)), nil
			}}
			config := &adk.ChatModelAgentConfig{Name: "agent", Description: "Test agent.", Model: scripted,
				ToolsConfig: toolsConfig(refunds.tool()),
				Handlers: []adk.ChatModelAgentMiddleware{
					clampingHandler{&adk.BaseChatModelAgentMiddleware{}},
					rewritingHandler{&adk.BaseChatModelAgentMiddleware{}, func(*schema.Message) {}},
				},
				Middlewares: []adk.AgentMiddleware{{AfterChatModel: func(_ context.Context, state *adk.ChatModelAgentState) error {
					last := *state.Messages[len(state.Messages)-1]
					state.Messages[len(state.Messages)-1] = &last
					return nil
				}}},
			}
			runner, err := dexeino.NewRunner(config, refundPolicies(), dexeino.WithStreaming(isStreaming))
			require.NoError(t, err)
			state := newTurnState(t, runner, "refund")
			requested := state.runModel(runner).(*dexagents.ToolCallsRequested)
			require.Equal(t, `{"id":"1"}`, requested.ToolCalls[0].Arguments)
			require.Contains(t, requested.NewMessages[0].FrameworkItem, `{\"id\":\"1\"}`)
			require.Empty(t, state.runTools(runner))
			require.Equal(t, []string{`{"id":"1"}`}, refunds.runs())
			completed := state.runModel(runner).(*dexagents.TurnCompleted)
			require.Nil(t, completed.TurnError)
			require.Equal(t, "refunded order 1", *completed.NewMessages[len(completed.NewMessages)-1].Text)
		})
	}
}

// routingHandler answers from a model it picked and never calls the model it wraps.
type routingHandler struct {
	*adk.BaseChatModelAgentMiddleware
	routed        model.BaseModel[*schema.Message]
	isCalledTwice bool
}

func (handler routingHandler) WrapModel(context.Context, model.BaseModel[*schema.Message], *adk.ModelContext) (model.BaseModel[*schema.Message], error) {
	if handler.isCalledTwice {
		return doubleCallingModel{inner: handler.routed}, nil
	}
	return handler.routed, nil
}

// A Handler whose WrapModel answers from another model ends the turn as failed: Dex cannot count
// that model's calls or see the tools it was offered. The usage the message reports is billed.
func TestHandlerThatAnswersFromAnotherModelEndsTheTurnAsFailed(t *testing.T) {
	for _, isCalledTwice := range []bool{false, true} {
		for _, isStreaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("twice=%v streaming=%v", isCalledTwice, isStreaming), func(t *testing.T) {
				refunds := &refundAgent{}
				base := echoModel()
				routed := &scriptedChatModel{respond: func([]*schema.Message) (*schema.Message, error) {
					message := toolCallMessage(call("call_1", "refund_order", `{"id":"1"}`))
					message.ResponseMeta = &schema.ResponseMeta{Usage: &schema.TokenUsage{PromptTokens: 7, CompletionTokens: 3}}
					return message, nil
				}}
				config := &adk.ChatModelAgentConfig{Name: "agent", Description: "Test agent.", Model: base,
					ToolsConfig: toolsConfig(refunds.tool()),
					Handlers:    []adk.ChatModelAgentMiddleware{routingHandler{&adk.BaseChatModelAgentMiddleware{}, routed, isCalledTwice}}}
				runner, err := dexeino.NewRunner(config, refundPolicies(), dexeino.WithStreaming(isStreaming))
				require.NoError(t, err)
				outcome, err := runner.RunModelStep(context.Background(), newTurnState(t, runner, "refund").context())
				completed := requireTurnError(t, outcome, err)
				require.Contains(t, *completed.TurnError, "without calling the model it wraps")
				require.Contains(t, *completed.TurnError, "AgentConfigBuilder")
				require.Equal(t, dexagents.AgentUsage{ModelCalls: 1, InputTokens: 7, OutputTokens: 3}, completed.Usage)
				require.Empty(t, completed.NewMessages)
				require.Zero(t, base.calls())
				require.Empty(t, refunds.runs())
			})
		}
	}
}
