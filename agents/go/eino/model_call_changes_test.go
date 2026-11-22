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
	"encoding/json"
	"fmt"
	"slices"
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

// Streamed parts with their own stream index, as Eino's Ark Responses client sends, are stored apart and run.
func TestStreamedMessageWithIndexedPartsIsStoredAndRun(t *testing.T) {
	text := func(index *int, text string) schema.MessageOutputPart {
		return withStreamIndex(schema.MessageOutputPart{Type: schema.ChatMessagePartTypeText, Text: text}, index)
	}
	reasoning := func(index *int, text string) schema.MessageOutputPart {
		return withStreamIndex(schema.MessageOutputPart{Type: schema.ChatMessagePartTypeReasoning,
			Reasoning: &schema.MessageOutputReasoning{Text: text}}, index)
	}
	first, second := ptr(0), ptr(1)
	for name, testCase := range map[string]struct {
		chunks   []schema.MessageOutputPart
		expected []string
	}{
		"two text parts": {[]schema.MessageOutputPart{text(first, "Order 1 "), text(first, "shipped."), text(second, "It arrives Monday.")},
			[]string{"text:Order 1 shipped.", "text:It arrives Monday."}},
		"two reasoning items": {[]schema.MessageOutputPart{reasoning(first, "Look it up. "), reasoning(second, "Found it.")},
			[]string{"reasoning:Look it up. ", "reasoning:Found it."}},
		"reasoning, text, reasoning": {[]schema.MessageOutputPart{reasoning(first, "Plan."), text(second, "Checking."), reasoning(ptr(2), "Done.")},
			[]string{"reasoning:Plan.", "text:Checking.", "reasoning:Done."}},
		"parts without an index": {[]schema.MessageOutputPart{text(nil, "Order 1 "), text(nil, "shipped.")},
			[]string{"text:Order 1 shipped."}},
	} {
		for _, isToolCalled := range []bool{false, true} {
			for _, isStreaming := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s tool call=%v streaming=%v", name, isToolCalled, isStreaming), func(t *testing.T) {
					modelChunks := func() []*schema.Message {
						var chunks []*schema.Message
						for _, part := range testCase.chunks {
							chunks = append(chunks, &schema.Message{Role: schema.Assistant,
								AssistantGenMultiContent: []schema.MessageOutputPart{part}})
						}
						if isToolCalled {
							chunks = append(chunks, toolCallMessage(call("call_1", "lookup_order", `{"id":"1"}`)))
						}
						return chunks
					}
					scripted := &scriptedChatModel{respond: func(input []*schema.Message) (*schema.Message, error) {
						if input[len(input)-1].Role == schema.Tool {
							return schema.AssistantMessage("Order 1 shipped.", nil), nil
						}
						return schema.ConcatMessages(modelChunks())
					}}
					scripted.chunks = func(message *schema.Message) []*schema.Message {
						if len(message.AssistantGenMultiContent) == 0 {
							return []*schema.Message{message}
						}
						return modelChunks()
					}
					runner := readRunner(t, scripted, dexeino.WithStreaming(isStreaming))
					state := newTurnState(t, runner, "where is order 1")
					outcome := state.runModel(runner)
					if completed, isCompleted := outcome.(*dexagents.TurnCompleted); isCompleted {
						require.Nil(t, completed.TurnError)
					}
					require.Len(t, state.messages, 2)
					require.Equal(t, testCase.expected, storedParts(t, state.messages[1].FrameworkItem))
					if !isToolCalled {
						require.IsType(t, &dexagents.TurnCompleted{}, outcome)
						return
					}
					requested := outcome.(*dexagents.ToolCallsRequested)
					require.Equal(t, `{"id":"1"}`, requested.ToolCalls[0].Arguments)
					require.Empty(t, state.runTools(runner))
					completed := state.runModel(runner).(*dexagents.TurnCompleted)
					require.Nil(t, completed.TurnError)
					replayed := scripted.lastInput()
					require.Equal(t, schema.Assistant, replayed[len(replayed)-2].Role)
					require.Equal(t, testCase.expected, partTexts(replayed[len(replayed)-2].AssistantGenMultiContent),
						"the next model call replays every part")
				})
			}
		}
	}
}

func withStreamIndex(part schema.MessageOutputPart, index *int) schema.MessageOutputPart {
	if index != nil {
		part.StreamingMeta = &schema.MessageStreamingMeta{Index: *index}
	}
	return part
}

// storedParts returns the type and text of each AssistantGenMultiContent part of a stored Eino message.
func storedParts(t *testing.T, frameworkItem string) []string {
	t.Helper()
	var item struct {
		Message *schema.Message `json:"message"`
	}
	require.NoError(t, json.Unmarshal([]byte(frameworkItem), &item))
	return partTexts(item.Message.AssistantGenMultiContent)
}

func partTexts(parts []schema.MessageOutputPart) []string {
	var texts []string
	for _, part := range parts {
		switch part.Type {
		case schema.ChatMessagePartTypeReasoning:
			texts = append(texts, "reasoning:"+part.Reasoning.Text)
		default:
			texts = append(texts, string(part.Type)+":"+part.Text)
		}
	}
	return texts
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

// fallbackHandler calls the model it wraps and answers from a backup model when that call fails.
type fallbackHandler struct {
	*adk.BaseChatModelAgentMiddleware
	backup model.BaseModel[*schema.Message]
}

type fallbackModel struct {
	inner, backup model.BaseModel[*schema.Message]
}

func (handler fallbackHandler) WrapModel(_ context.Context, inner model.BaseModel[*schema.Message], _ *adk.ModelContext) (model.BaseModel[*schema.Message], error) {
	return fallbackModel{inner: inner, backup: handler.backup}, nil
}

func (fallback fallbackModel) Generate(ctx context.Context, input []*schema.Message, options ...model.Option) (*schema.Message, error) {
	message, err := fallback.inner.Generate(ctx, input, options...)
	if err != nil {
		return fallback.backup.Generate(ctx, input, options...)
	}
	return message, nil
}

func (fallback fallbackModel) Stream(ctx context.Context, input []*schema.Message, options ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	stream, err := fallback.inner.Stream(ctx, input, options...)
	if err != nil {
		return fallback.backup.Stream(ctx, input, options...)
	}
	message, err := schema.ConcatMessageStream(stream)
	if err != nil {
		return fallback.backup.Stream(ctx, input, options...)
	}
	return schema.StreamReaderFromArray([]*schema.Message{message}), nil
}

// A Handler whose WrapModel answers from another model ends the turn as failed: Dex cannot count
// that model's calls or see the tools it was offered. The usage the message reports is billed.
// A fallback after a failed call ends the turn the same way: two model calls ran.
func TestHandlerThatAnswersFromAnotherModelEndsTheTurnAsFailed(t *testing.T) {
	unavailable := &statusCodeError{StatusCode: 503}
	for name, testCase := range map[string]struct {
		base      func() model.ToolCallingChatModel
		handler   func(answering model.BaseModel[*schema.Message]) adk.ChatModelAgentMiddleware
		streaming []bool
		expected  string
		baseCalls int
	}{
		"routes to another model": {
			func() model.ToolCallingChatModel { return echoModel() },
			func(answering model.BaseModel[*schema.Message]) adk.ChatModelAgentMiddleware {
				return routingHandler{&adk.BaseChatModelAgentMiddleware{}, answering, false}
			}, []bool{false, true}, "without calling the model it wraps", 0},
		"routes to another model twice": {
			func() model.ToolCallingChatModel { return echoModel() },
			func(answering model.BaseModel[*schema.Message]) adk.ChatModelAgentMiddleware {
				return routingHandler{&adk.BaseChatModelAgentMiddleware{}, answering, true}
			}, []bool{false, true}, "without calling the model it wraps", 0},
		"falls back after a failed call": {
			func() model.ToolCallingChatModel {
				return &scriptedChatModel{respond: func([]*schema.Message) (*schema.Message, error) { return nil, unavailable }}
			},
			func(answering model.BaseModel[*schema.Message]) adk.ChatModelAgentMiddleware {
				return fallbackHandler{&adk.BaseChatModelAgentMiddleware{}, answering}
			}, []bool{false, true}, "after the call of the model it wraps failed", 1},
		"falls back after a failed stream": {
			func() model.ToolCallingChatModel { return &midStreamModel{failure: unavailable} },
			func(answering model.BaseModel[*schema.Message]) adk.ChatModelAgentMiddleware {
				return fallbackHandler{&adk.BaseChatModelAgentMiddleware{}, answering}
			}, []bool{true}, "after the call of the model it wraps failed", -1},
	} {
		for _, isStreaming := range testCase.streaming {
			t.Run(fmt.Sprintf("%s streaming=%v", name, isStreaming), func(t *testing.T) {
				refunds := &refundAgent{}
				base := testCase.base()
				answering := &scriptedChatModel{respond: func([]*schema.Message) (*schema.Message, error) {
					message := toolCallMessage(call("call_1", "refund_order", `{"id":"1"}`))
					message.ResponseMeta = &schema.ResponseMeta{Usage: &schema.TokenUsage{PromptTokens: 7, CompletionTokens: 3}}
					return message, nil
				}}
				config := &adk.ChatModelAgentConfig{Name: "agent", Description: "Test agent.", Model: base,
					ToolsConfig: toolsConfig(refunds.tool()),
					Handlers:    []adk.ChatModelAgentMiddleware{testCase.handler(answering)}}
				runner, err := dexeino.NewRunner(config, refundPolicies(), dexeino.WithStreaming(isStreaming))
				require.NoError(t, err)
				outcome, err := runner.RunModelStep(context.Background(), newTurnState(t, runner, "refund").context())
				completed := requireTurnError(t, outcome, err)
				require.Contains(t, *completed.TurnError, testCase.expected)
				require.Contains(t, *completed.TurnError, "AgentConfigBuilder")
				require.Equal(t, dexagents.AgentUsage{ModelCalls: 1, InputTokens: 7, OutputTokens: 3}, completed.Usage,
					"the answering model's call is billed and the failed call is not")
				require.Empty(t, completed.NewMessages)
				if scripted, isScripted := base.(*scriptedChatModel); isScripted {
					require.Equal(t, testCase.baseCalls, scripted.calls())
				}
				require.GreaterOrEqual(t, answering.calls(), 1)
				require.Empty(t, refunds.runs())
			})
		}
	}
}

// stateEditingHandler changes the agent state after the model call, leaving the model's message last.
type stateEditingHandler struct {
	*adk.BaseChatModelAgentMiddleware
	edit func(state *adk.ChatModelAgentState)
}

func (handler stateEditingHandler) AfterModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, _ *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	handler.edit(state)
	return ctx, state, nil
}

// An after-model change to earlier messages or tools ends the turn: Dex replays both at every model call.
func TestStateChangesAfterTheModelCallEndTheTurnAsFailed(t *testing.T) {
	trimHistory := func(state *adk.ChatModelAgentState) {
		state.Messages = state.Messages[len(state.Messages)-1:]
	}
	redactCopy := func(state *adk.ChatModelAgentState) {
		redacted := *state.Messages[0]
		redacted.Content = "[redacted]"
		state.Messages[0] = &redacted
	}
	redactInPlace := func(state *adk.ChatModelAgentState) { state.Messages[0].Content = "[redacted]" }
	addTool := func(state *adk.ChatModelAgentState) {
		state.ToolInfos = append(slices.Clone(state.ToolInfos), &schema.ToolInfo{Name: "added_later", Desc: "Added after the model call."})
	}
	for name, testCase := range map[string]struct {
		handler    func(state *adk.ChatModelAgentState)
		middleware func(state *adk.ChatModelAgentState)
		expected   string
	}{
		"a Handler trims the history":          {trimHistory, nil, "earlier messages changed after the model call"},
		"a Handler redacts a copy":             {redactCopy, nil, "earlier messages changed after the model call"},
		"a Handler redacts in place":           {redactInPlace, nil, "earlier messages changed after the model call"},
		"a Handler adds a tool":                {addTool, nil, "tools changed after the model call"},
		"an AgentMiddleware trims the history": {nil, trimHistory, "earlier messages changed after the model call"},
		"an AgentMiddleware redacts in place":  {nil, redactInPlace, "earlier messages changed after the model call"},
		"an AgentMiddleware adds a tool":       {nil, addTool, "tools changed after the model call"},
	} {
		for _, isStreaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s streaming=%v", name, isStreaming), func(t *testing.T) {
				refunds := &refundAgent{}
				scripted := &scriptedChatModel{respond: func([]*schema.Message) (*schema.Message, error) {
					return toolCallMessage(call("call_1", "refund_order", `{"id":"1"}`)), nil
				}}
				config := &adk.ChatModelAgentConfig{Name: "agent", Description: "Test agent.", Model: scripted,
					ToolsConfig: toolsConfig(refunds.tool())}
				if testCase.handler != nil {
					config.Handlers = []adk.ChatModelAgentMiddleware{stateEditingHandler{&adk.BaseChatModelAgentMiddleware{}, testCase.handler}}
				}
				if testCase.middleware != nil {
					edit := testCase.middleware
					config.Middlewares = []adk.AgentMiddleware{{AfterChatModel: func(_ context.Context, state *adk.ChatModelAgentState) error {
						edit(state)
						return nil
					}}}
				}
				runner, err := dexeino.NewRunner(config, refundPolicies(), dexeino.WithStreaming(isStreaming))
				require.NoError(t, err)
				outcome, err := runner.RunModelStep(context.Background(), newTurnState(t, runner, "refund card 4111").context())
				completed := requireTurnError(t, outcome, err)
				require.Contains(t, *completed.TurnError, testCase.expected)
				require.Contains(t, *completed.TurnError, "BeforeModelRewriteState")
				require.Equal(t, int64(1), completed.Usage.ModelCalls, "the call that ran is billed")
				require.Empty(t, completed.NewMessages)
				require.Equal(t, 1, scripted.calls())
				require.Empty(t, refunds.runs())
			})
		}
	}
}

// historyTrimmingHandler keeps only the last message in the model input, before every model call.
type historyTrimmingHandler struct {
	*adk.BaseChatModelAgentMiddleware
}

func (historyTrimmingHandler) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, _ *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	state.Messages = state.Messages[len(state.Messages)-1:]
	state.ToolInfos = slices.Clone(state.ToolInfos)
	return ctx, state, nil
}

// Before-model state changes apply at every model call and are not refused, nor are unchanged after-model copies.
func TestStateChangesBeforeTheModelCallApplyToEveryModelCall(t *testing.T) {
	for _, testCase := range []struct{ isStreaming, isToolInfosDropped bool }{{false, false}, {true, false}, {false, true}, {true, true}} {
		isStreaming := testCase.isStreaming
		t.Run(fmt.Sprintf("streaming=%v tools dropped=%v", isStreaming, testCase.isToolInfosDropped), func(t *testing.T) {
			refunds := &refundAgent{}
			scripted := &scriptedChatModel{respond: func(input []*schema.Message) (*schema.Message, error) {
				if input[len(input)-1].Role == schema.Tool {
					return schema.AssistantMessage("refunded", nil), nil
				}
				return toolCallMessage(call("call_1", "refund_order", `{"id":"1"}`)), nil
			}}
			config := &adk.ChatModelAgentConfig{Name: "agent", Description: "Test agent.", Model: scripted,
				ToolsConfig: toolsConfig(refunds.tool()),
				Handlers: []adk.ChatModelAgentMiddleware{
					historyTrimmingHandler{&adk.BaseChatModelAgentMiddleware{}},
					stateEditingHandler{&adk.BaseChatModelAgentMiddleware{}, func(state *adk.ChatModelAgentState) {
						state.Messages = slices.Clone(state.Messages)
						state.ToolInfos = slices.Clone(state.ToolInfos)
						if testCase.isToolInfosDropped {
							// Eino offers the agent's tools again when a hook leaves no ToolInfos.
							state.ToolInfos = nil
						}
					}},
				}}
			runner, err := dexeino.NewRunner(config, refundPolicies(), dexeino.WithStreaming(isStreaming))
			require.NoError(t, err)
			state := newTurnState(t, runner, "refund")
			state.runModel(runner)
			require.Empty(t, state.runTools(runner))
			completed := state.runModel(runner).(*dexagents.TurnCompleted)
			require.Nil(t, completed.TurnError)
			require.Equal(t, []string{`{"id":"1"}`}, refunds.runs())
			require.Equal(t, []schema.RoleType{schema.Tool}, roles(scripted.lastInput()), "the trim applied to the second model call")
		})
	}
}
