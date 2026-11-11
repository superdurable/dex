// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Package anthropicmodel connects built-in agent loop agents to Claude models through the official
// anthropic-sdk-go SDK.
//
// MessagesModelClient makes one streaming Messages API request per RunModel
// attempt. It stores each response as one assistant message in the shape the
// Python core's LiteLLM client stores an Anthropic response, provider
// "anthropic" and API "chat_completions", with thinking blocks and their
// signatures exact, so either core replays the other's items without losing
// thinking. It also replays the TypeScript core's AI SDK Anthropic items with
// their reasoning signatures.
package anthropicmodel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/agents/go/core/builtinloop"
	"github.com/superdurable/dex/agents/go/core/internal/pythonjson"
)

// ChatCompletionsAPI is the ModelOutputItem.API of the items this client returns.
//
// It is the API name the Python core's LiteLLM client records for Anthropic
// models, whose stored item has the same shape.
const ChatCompletionsAPI = "chat_completions"

// aiSDKAPI is the API name the TypeScript core's AI SDK client records.
const aiSDKAPI = "ai-sdk"

// ProviderName is the ModelOutputItem.Provider of the items this client returns.
const ProviderName = "anthropic"

// DefaultMaxOutputTokens is max_tokens when ModelSettings.MaxOutputTokens is nil; the API requires one.
const DefaultMaxOutputTokens int64 = 4096

// MessagesModelClientOption configures one MessagesModelClient.
type MessagesModelClientOption interface {
	applyMessagesModelClient(client *MessagesModelClient)
}

type messagesModelClientOptionFunc func(client *MessagesModelClient)

func (option messagesModelClientOptionFunc) applyMessagesModelClient(client *MessagesModelClient) {
	option(client)
}

// WithRequestOptions adds anthropic-sdk-go request options to every request, such as option.WithHeader.
//
// The client always adds option.WithMaxRetries(0) last, so Dex owns retries.
func WithRequestOptions(requestOptions ...option.RequestOption) MessagesModelClientOption {
	return messagesModelClientOptionFunc(func(client *MessagesModelClient) {
		client.requestOptions = append(client.requestOptions, requestOptions...)
	})
}

// MessagesModelClient makes one Anthropic Messages API request per RunModel attempt.
//
// It sends the whole conversation every time and makes no retries of its own.
// Text deltas become text_delta events and thinking deltas reasoning_delta
// events. An item from another provider or API replays as its text and tool
// calls; its reasoning is dropped. Credentials come from the
// anthropic.Client, which reads ANTHROPIC_API_KEY by default.
//
// ModelSettings map to temperature and max_tokens (DefaultMaxOutputTokens
// when unset). ReasoningEffort is not mapped: set extended thinking through
// ProviderOptions, such as {"thinking": {"type": "enabled", "budget_tokens":
// 2048}}. ProviderOptions set top-level request fields last.
//
//	model := anthropicmodel.NewMessagesModelClient(anthropic.NewClient(), "claude-sonnet-4-5")
//	definition, err := builtinloop.NewAgentDefinition("support", "You help customers.", model)
type MessagesModelClient struct {
	client         anthropic.Client
	model          string
	requestOptions []option.RequestOption
}

// NewMessagesModelClient creates a client for one model, such as "claude-sonnet-4-5".
func NewMessagesModelClient(client anthropic.Client, model string, options ...MessagesModelClientOption) *MessagesModelClient {
	modelClient := &MessagesModelClient{client: client, model: model}
	for _, option := range options {
		option.applyMessagesModelClient(modelClient)
	}
	return modelClient
}

type messagesStreamEvent struct {
	Type         string          `json:"type"`
	Index        int             `json:"index"`
	ContentBlock json.RawMessage `json:"content_block"`
	Delta        struct {
		Type        string  `json:"type"`
		Text        string  `json:"text"`
		Thinking    string  `json:"thinking"`
		Signature   string  `json:"signature"`
		PartialJSON string  `json:"partial_json"`
		StopReason  *string `json:"stop_reason"`
	} `json:"delta"`
	Message *struct {
		Model string        `json:"model"`
		Usage messagesUsage `json:"usage"`
	} `json:"message"`
	Usage *messagesUsage `json:"usage"`
}

type messagesUsage struct {
	InputTokens              *int64 `json:"input_tokens"`
	OutputTokens             *int64 `json:"output_tokens"`
	CacheReadInputTokens     *int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens *int64 `json:"cache_creation_input_tokens"`
	ThinkingTokens           *int64 `json:"thinking_tokens"`
}

// contentBlockBuilder accumulates one streamed content block.
type contentBlockBuilder struct {
	start     pythonjson.Object
	blockType string
	text      string
	thinking  string
	signature *string
	data      string
	inputJSON string
}

// CallModel makes one streaming Messages API request.
//
// It returns an error when the request fails, the stream reports an error
// event, or the stream ends without message_stop.
func (client *MessagesModelClient) CallModel(
	ctx context.Context,
	request builtinloop.ModelRequest,
	output builtinloop.ModelOutputWriter,
) (builtinloop.ModelResponse, error) {
	requestOptions, maxTokens, err := client.requestBody(request)
	if err != nil {
		return builtinloop.ModelResponse{}, err
	}
	stream := client.client.Messages.NewStreaming(ctx, anthropic.MessageNewParams{
		Model: anthropic.Model(client.model), MaxTokens: maxTokens,
	}, requestOptions...)
	blocks := map[int]*contentBlockBuilder{}
	blockOrder := []int{}
	response := builtinloop.ModelResponse{ToolCalls: []builtinloop.ModelToolCall{}, Usage: dexagents.AgentUsage{ModelCalls: 1}}
	isStopped := false
	for stream.Next() {
		var event messagesStreamEvent
		if err := json.Unmarshal([]byte(stream.Current().RawJSON()), &event); err != nil {
			return builtinloop.ModelResponse{}, errors.Join(fmt.Errorf("decode a Messages API event: %w", err), stream.Close())
		}
		err = nil
		switch event.Type {
		case "message_start":
			if event.Message != nil {
				response.ResponseModel = event.Message.Model
				addUsage(&response.Usage, event.Message.Usage)
			}
		case "content_block_start":
			var builder *contentBlockBuilder
			builder, err = newContentBlockBuilder(event.ContentBlock)
			if err == nil {
				blocks[event.Index] = builder
				blockOrder = append(blockOrder, event.Index)
			}
		case "content_block_delta":
			err = applyDelta(blocks[event.Index], event, output)
		case "message_delta":
			if event.Delta.StopReason != nil {
				response.FinishReason = *event.Delta.StopReason
			}
			if event.Usage != nil {
				addUsage(&response.Usage, *event.Usage)
			}
		case "message_stop":
			isStopped = true
		}
		if err != nil {
			return builtinloop.ModelResponse{}, errors.Join(err, stream.Close())
		}
	}
	if err := errors.Join(stream.Err(), stream.Close()); err != nil {
		return builtinloop.ModelResponse{}, err
	}
	if !isStopped {
		return builtinloop.ModelResponse{}, errors.New("the Messages API stream ended without message_stop")
	}
	return finishResponse(response, blocks, blockOrder)
}

func newContentBlockBuilder(raw json.RawMessage) (*contentBlockBuilder, error) {
	parsed, err := pythonjson.ParseOrdered(raw)
	if err != nil {
		return nil, fmt.Errorf("decode a content block: %w", err)
	}
	start, isObject := parsed.(pythonjson.Object)
	if !isObject {
		return nil, errors.New("a content block is not an object")
	}
	builder := &contentBlockBuilder{start: start}
	for _, member := range start {
		switch member.Key {
		case "type":
			builder.blockType, _ = member.Value.(string)
		case "text":
			builder.text, _ = member.Value.(string)
		case "thinking":
			builder.thinking, _ = member.Value.(string)
		case "signature":
			if signature, isText := member.Value.(string); isText {
				builder.signature = &signature
			}
		case "data":
			builder.data, _ = member.Value.(string)
		}
	}
	return builder, nil
}

func applyDelta(builder *contentBlockBuilder, event messagesStreamEvent, output builtinloop.ModelOutputWriter) error {
	if builder == nil {
		return fmt.Errorf("a %s arrived for unknown content block %d", event.Delta.Type, event.Index)
	}
	switch event.Delta.Type {
	case "text_delta":
		builder.text += event.Delta.Text
		return output.WriteTextDelta(event.Delta.Text)
	case "thinking_delta":
		builder.thinking += event.Delta.Thinking
		return output.WriteReasoningDelta(event.Delta.Thinking)
	case "signature_delta":
		signature := stringOrEmpty(builder.signature) + event.Delta.Signature
		builder.signature = &signature
	case "input_json_delta":
		builder.inputJSON += event.Delta.PartialJSON
	}
	return nil
}

// finishResponse builds the one stored assistant message, in the LiteLLM chat message shape.
//
// The keys and their order are those the Python core writes: role, content,
// then tool_calls and thinking_blocks when present. A response with no text,
// tool call, or signed thinking stores nothing: the API rejects an empty
// assistant message on every later request.
func finishResponse(
	response builtinloop.ModelResponse,
	blocks map[int]*contentBlockBuilder,
	blockOrder []int,
) (builtinloop.ModelResponse, error) {
	toolCalls := []any{}
	thinkingBlocks := []any{}
	for _, index := range blockOrder {
		builder := blocks[index]
		switch builder.blockType {
		case "text":
			response.Text += builder.text
		case "thinking":
			// An unsigned thinking block cannot be replayed, as in the Python core.
			if builder.signature != nil && *builder.signature != "" {
				thinkingBlocks = append(thinkingBlocks, pythonjson.Object{
					{Key: "type", Value: "thinking"}, {Key: "thinking", Value: builder.thinking},
					{Key: "signature", Value: *builder.signature},
				})
			}
		case "redacted_thinking":
			thinkingBlocks = append(thinkingBlocks, pythonjson.Object{
				{Key: "type", Value: "redacted_thinking"}, {Key: "data", Value: builder.data},
			})
		case "tool_use":
			toolCall, storedArguments, err := builder.toolCall()
			if err != nil {
				return builtinloop.ModelResponse{}, err
			}
			response.ToolCalls = append(response.ToolCalls, toolCall)
			toolCalls = append(toolCalls, pythonjson.Object{
				{Key: "id", Value: toolCall.ToolCallID},
				{Key: "type", Value: "function"},
				{Key: "function", Value: pythonjson.Object{
					{Key: "name", Value: toolCall.ToolName}, {Key: "arguments", Value: storedArguments},
				}},
			})
		}
	}
	response.OutputItems = []builtinloop.ModelOutputItem{}
	if response.Text == "" && len(toolCalls) == 0 && len(thinkingBlocks) == 0 {
		return response, nil
	}
	message := pythonjson.Object{{Key: "role", Value: "assistant"}, {Key: "content", Value: nil}}
	if response.Text != "" {
		message[1].Value = response.Text
	}
	if len(toolCalls) > 0 {
		message = append(message, pythonjson.Member{Key: "tool_calls", Value: toolCalls})
	}
	if len(thinkingBlocks) > 0 {
		message = append(message, pythonjson.Member{Key: "thinking_blocks", Value: thinkingBlocks})
	}
	item, err := pythonjson.Marshal(message, pythonjson.Options{IsCompact: true})
	if err != nil {
		return builtinloop.ModelResponse{}, err
	}
	outputItem := builtinloop.ModelOutputItem{Provider: ProviderName, API: ChatCompletionsAPI, Item: item, ToolCalls: response.ToolCalls}
	if response.Text != "" {
		text := response.Text
		outputItem.Text = &text
	}
	response.OutputItems = []builtinloop.ModelOutputItem{outputItem}
	return response, nil
}

// toolCall returns the call and the arguments to store, which are "" for a tool with no input.
//
// The stored text is what was streamed, as the Python core stores it; the
// call's arguments are "{}" then. Input that arrived whole in
// content_block_start counts when no delta came.
func (builder *contentBlockBuilder) toolCall() (builtinloop.ModelToolCall, string, error) {
	var identity struct {
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	}
	raw, err := pythonjson.Marshal(builder.start, pythonjson.Options{IsCompact: true})
	if err != nil {
		return builtinloop.ModelToolCall{}, "", err
	}
	if err := json.Unmarshal([]byte(raw), &identity); err != nil {
		return builtinloop.ModelToolCall{}, "", err
	}
	storedArguments := builder.inputJSON
	if storedArguments == "" && len(identity.Input) > 0 && string(identity.Input) != "{}" {
		storedArguments = string(identity.Input)
	}
	arguments := storedArguments
	if arguments == "" {
		arguments = "{}"
	}
	if !json.Valid([]byte(arguments)) {
		return builtinloop.ModelToolCall{}, "", fmt.Errorf("tool_use %s input is not JSON", identity.ID)
	}
	return builtinloop.ModelToolCall{ToolCallID: identity.ID, ToolName: identity.Name, Arguments: arguments}, storedArguments, nil
}

func addUsage(usage *dexagents.AgentUsage, reported messagesUsage) {
	// The API reports cumulative counts; the latest non-nil value wins.
	if reported.InputTokens != nil || reported.CacheReadInputTokens != nil || reported.CacheCreationInputTokens != nil {
		cached := valueOrZero(reported.CacheReadInputTokens)
		usage.InputTokens = valueOrZero(reported.InputTokens) + cached + valueOrZero(reported.CacheCreationInputTokens)
		usage.CachedInputTokens = cached
	}
	if reported.OutputTokens != nil {
		usage.OutputTokens = *reported.OutputTokens
	}
	if reported.ThinkingTokens != nil {
		usage.ReasoningTokens = *reported.ThinkingTokens
	}
}

func (client *MessagesModelClient) requestBody(request builtinloop.ModelRequest) ([]option.RequestOption, int64, error) {
	messages, err := anthropicMessages(request.InputItems)
	if err != nil {
		return nil, 0, err
	}
	maxTokens := DefaultMaxOutputTokens
	if request.Settings.MaxOutputTokens != nil {
		maxTokens = *request.Settings.MaxOutputTokens
	}
	fields := map[string]any{"messages": messages}
	if request.Instructions != "" {
		fields["system"] = request.Instructions
	}
	if request.Settings.Temperature != nil {
		fields["temperature"] = *request.Settings.Temperature
	}
	if len(request.Tools) > 0 {
		tools := make([]map[string]any, 0, len(request.Tools))
		for _, tool := range request.Tools {
			tools = append(tools, map[string]any{"name": tool.Name, "description": tool.Description, "input_schema": tool.Parameters})
		}
		fields["tools"] = tools
		fields["tool_choice"] = anthropicToolChoice(request.ToolChoice)
	}
	maps.Copy(fields, request.Settings.ProviderOptions)
	requestOptions := append([]option.RequestOption{}, client.requestOptions...)
	for key, value := range fields {
		requestOptions = append(requestOptions, option.WithJSONSet(key, value))
	}
	return append(requestOptions, option.WithMaxRetries(0)), maxTokens, nil
}

// EstimateInputTokens estimates four characters per token over the request text and items.
func (client *MessagesModelClient) EstimateInputTokens(request builtinloop.ModelRequest) int64 {
	characters := len(request.Instructions)
	for _, item := range request.InputItems {
		switch typed := item.(type) {
		case builtinloop.UserTextItem:
			characters += len(typed.Text)
		case builtinloop.ToolResultItem:
			characters += len(typed.Output)
		case builtinloop.ModelOutputItem:
			characters += len(typed.Item)
		}
	}
	for _, tool := range request.Tools {
		characters += len(tool.Name) + len(tool.Description) + len(tool.Parameters)
	}
	return int64(characters / 4)
}

// anthropicMessage is one message of the request; consecutive items of one role merge into it.
type anthropicMessage struct {
	Role    string `json:"role"`
	Content []any  `json:"content"`
}

func anthropicMessages(items []builtinloop.ModelInputItem) ([]anthropicMessage, error) {
	messages := []anthropicMessage{}
	appendBlocks := func(role string, blocks []any) {
		if len(blocks) == 0 {
			// An empty message is rejected by the API, so it is never sent.
			return
		}
		last := len(messages) - 1
		if last >= 0 && messages[last].Role == role {
			messages[last].Content = append(messages[last].Content, blocks...)
			return
		}
		messages = append(messages, anthropicMessage{Role: role, Content: blocks})
	}
	for _, item := range items {
		switch typed := item.(type) {
		case builtinloop.UserTextItem:
			appendBlocks("user", []any{map[string]any{"type": "text", "text": typed.Text}})
		case builtinloop.ToolResultItem:
			appendBlocks("user", []any{map[string]any{"type": "tool_result", "tool_use_id": typed.ToolCallID, "content": typed.Output}})
		case builtinloop.ModelOutputItem:
			blocks, err := assistantBlocks(typed)
			if err != nil {
				return nil, err
			}
			appendBlocks("assistant", blocks)
		}
	}
	return messages, nil
}

// assistantBlocks converts one stored item, keeping thinking signatures for every Anthropic format.
func assistantBlocks(item builtinloop.ModelOutputItem) ([]any, error) {
	switch {
	case item.Provider == ProviderName && item.API == ChatCompletionsAPI:
		return chatCompletionsBlocks(item.Item)
	case item.API == aiSDKAPI && strings.HasPrefix(item.Provider, ProviderName):
		return aiSDKBlocks(item.Item)
	default:
		// Items from another provider or API replay as text and tool calls; reasoning is dropped.
		blocks := []any{}
		if item.Text != nil && *item.Text != "" {
			blocks = append(blocks, map[string]any{"type": "text", "text": *item.Text})
		}
		for _, call := range item.ToolCalls {
			blocks = append(blocks, toolUseBlock(call.ToolCallID, call.ToolName, call.Arguments))
		}
		return blocks, nil
	}
}

// chatCompletionsBlocks reads the LiteLLM chat message that this client and the Python core store.
func chatCompletionsBlocks(stored string) ([]any, error) {
	var message struct {
		Content   *string `json:"content"`
		ToolCalls []struct {
			ID       string `json:"id"`
			Function struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"function"`
		} `json:"tool_calls"`
		ThinkingBlocks []struct {
			Type      string `json:"type"`
			Thinking  string `json:"thinking"`
			Signature string `json:"signature"`
			Data      string `json:"data"`
		} `json:"thinking_blocks"`
	}
	if err := json.Unmarshal([]byte(stored), &message); err != nil {
		return nil, fmt.Errorf("stored %s item: %w", ChatCompletionsAPI, err)
	}
	blocks := []any{}
	for _, thinking := range message.ThinkingBlocks {
		if thinking.Type == "redacted_thinking" {
			blocks = append(blocks, map[string]any{"type": "redacted_thinking", "data": thinking.Data})
			continue
		}
		blocks = append(blocks, map[string]any{"type": "thinking", "thinking": thinking.Thinking, "signature": thinking.Signature})
	}
	if message.Content != nil && *message.Content != "" {
		blocks = append(blocks, map[string]any{"type": "text", "text": *message.Content})
	}
	for _, call := range message.ToolCalls {
		blocks = append(blocks, toolUseBlock(call.ID, call.Function.Name, call.Function.Arguments))
	}
	return blocks, nil
}

// aiSDKBlocks reads the AI SDK assistant message that the TypeScript core stores for Anthropic models.
func aiSDKBlocks(stored string) ([]any, error) {
	var message struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal([]byte(stored), &message); err != nil {
		return nil, fmt.Errorf("stored %s item: %w", aiSDKAPI, err)
	}
	var text string
	if err := json.Unmarshal(message.Content, &text); err == nil {
		if text == "" {
			return []any{}, nil
		}
		return []any{map[string]any{"type": "text", "text": text}}, nil
	}
	var parts []struct {
		Type            string          `json:"type"`
		Text            string          `json:"text"`
		ToolCallID      string          `json:"toolCallId"`
		ToolName        string          `json:"toolName"`
		Input           json.RawMessage `json:"input"`
		ProviderOptions struct {
			Anthropic struct {
				Signature    string `json:"signature"`
				RedactedData string `json:"redactedData"`
			} `json:"anthropic"`
		} `json:"providerOptions"`
	}
	if err := json.Unmarshal(message.Content, &parts); err != nil {
		return nil, fmt.Errorf("stored %s item content: %w", aiSDKAPI, err)
	}
	blocks := []any{}
	for _, part := range parts {
		anthropicOptions := part.ProviderOptions.Anthropic
		switch {
		case part.Type == "reasoning" && anthropicOptions.RedactedData != "":
			blocks = append(blocks, map[string]any{"type": "redacted_thinking", "data": anthropicOptions.RedactedData})
		case part.Type == "reasoning" && anthropicOptions.Signature != "":
			blocks = append(blocks, map[string]any{"type": "thinking", "thinking": part.Text, "signature": anthropicOptions.Signature})
		case part.Type == "text" && part.Text != "":
			blocks = append(blocks, map[string]any{"type": "text", "text": part.Text})
		case part.Type == "tool-call":
			blocks = append(blocks, toolUseBlock(part.ToolCallID, part.ToolName, string(part.Input)))
		}
	}
	return blocks, nil
}

func toolUseBlock(toolCallID string, toolName string, arguments string) map[string]any {
	input := json.RawMessage(arguments)
	if !json.Valid(input) {
		input = json.RawMessage("{}")
	}
	return map[string]any{"type": "tool_use", "id": toolCallID, "name": toolName, "input": input}
}

func anthropicToolChoice(choice builtinloop.ToolChoice) map[string]any {
	switch choice.Mode {
	case builtinloop.ToolChoiceModeNone:
		return map[string]any{"type": "none"}
	case builtinloop.ToolChoiceModeRequired:
		return map[string]any{"type": "any"}
	case builtinloop.ToolChoiceModeNamed:
		return map[string]any{"type": "tool", "name": choice.ToolName}
	default:
		return map[string]any{"type": "auto"}
	}
}

func valueOrZero(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func stringOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
