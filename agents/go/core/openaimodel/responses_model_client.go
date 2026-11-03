// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Package openaimodel connects Stepwise agents to OpenAI models through the official openai-go SDK.
//
// ResponsesModelClient makes one streaming Responses API request per RunModel
// attempt, with store false and encrypted reasoning content included, so Dex
// owns the conversation and reasoning items replay exactly. Its items are
// those of the Python core's LiteLLM client for OpenAI models, provider
// "openai" and API "responses", so a session can move between Workers of
// either core.
package openaimodel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/agents/go/core/internal/pythonjson"
	"github.com/superdurable/dex/agents/go/core/stepwise"
)

// ResponsesAPI is the ModelOutputItem.API of the items this client returns.
const ResponsesAPI = "responses"

// DefaultProviderName is the default ModelOutputItem.Provider of the items this client returns.
const DefaultProviderName = "openai"

// responsesReplayFields are the item fields stored and replayed, in the Python core's order.
var responsesReplayFields = map[string][]string{
	"reasoning":     {"type", "id", "summary", "content", "encrypted_content", "status"},
	"message":       {"type", "id", "role", "content", "status"},
	"function_call": {"type", "id", "call_id", "name", "arguments", "status"},
}

// ResponsesModelClientOption configures one ResponsesModelClient.
type ResponsesModelClientOption interface {
	applyResponsesModelClient(client *ResponsesModelClient)
}

type responsesModelClientOptionFunc func(client *ResponsesModelClient)

func (option responsesModelClientOptionFunc) applyResponsesModelClient(client *ResponsesModelClient) {
	option(client)
}

// WithProviderName sets the provider stored on every item, such as "azure".
//
// Items replay exactly only to a client with the same provider name; the
// default is DefaultProviderName.
func WithProviderName(providerName string) ResponsesModelClientOption {
	return responsesModelClientOptionFunc(func(client *ResponsesModelClient) {
		client.providerName = providerName
	})
}

// WithRequestOptions adds openai-go request options to every request, such as option.WithHeader.
//
// The client always adds option.WithMaxRetries(0) last, so Dex owns retries.
func WithRequestOptions(requestOptions ...option.RequestOption) ResponsesModelClientOption {
	return responsesModelClientOptionFunc(func(client *ResponsesModelClient) {
		client.requestOptions = append(client.requestOptions, requestOptions...)
	})
}

// ResponsesModelClient makes one OpenAI Responses API request per RunModel attempt.
//
// It sends the whole input every time with store false, includes
// reasoning.encrypted_content, asks for automatic reasoning summaries, and
// makes no retries of its own. It keeps each output item's replay fields as
// the provider sent them and replays an item unchanged to the same provider
// and API; an item from another provider or API replays as its text and tool
// calls. Credentials come from the openai.Client, which reads OPENAI_API_KEY
// by default, never from Flow input.
//
// ModelSettings map to temperature, max_output_tokens, and reasoning.effort.
// ProviderOptions set top-level request fields last, so they override the
// client's own fields.
//
//	model := openaimodel.NewResponsesModelClient(openai.NewClient(), "gpt-5-mini")
//	definition, err := stepwise.NewAgentDefinition("support", "You help customers.", model)
type ResponsesModelClient struct {
	client         openai.Client
	model          string
	providerName   string
	requestOptions []option.RequestOption
}

// NewResponsesModelClient creates a client for one model, such as "gpt-5-mini".
func NewResponsesModelClient(client openai.Client, model string, options ...ResponsesModelClientOption) *ResponsesModelClient {
	modelClient := &ResponsesModelClient{client: client, model: model, providerName: DefaultProviderName}
	for _, option := range options {
		option.applyResponsesModelClient(modelClient)
	}
	return modelClient
}

type responsesStreamEvent struct {
	Type        string          `json:"type"`
	Delta       string          `json:"delta"`
	OutputIndex int64           `json:"output_index"`
	Item        json.RawMessage `json:"item"`
	Response    json.RawMessage `json:"response"`
}

type responsesCompleted struct {
	Model  string `json:"model"`
	Status string `json:"status"`
	Usage  struct {
		InputTokens        int64 `json:"input_tokens"`
		OutputTokens       int64 `json:"output_tokens"`
		InputTokensDetails struct {
			CachedTokens int64 `json:"cached_tokens"`
		} `json:"input_tokens_details"`
		OutputTokensDetails struct {
			ReasoningTokens int64 `json:"reasoning_tokens"`
		} `json:"output_tokens_details"`
	} `json:"usage"`
}

// CallModel makes one streaming Responses API request.
//
// Text and refusal deltas become text deltas; reasoning summary deltas become
// reasoning deltas. It returns an error when the request fails, the stream
// reports response.failed or error, or the stream ends without
// response.completed or response.incomplete.
func (client *ResponsesModelClient) CallModel(
	ctx context.Context,
	request stepwise.ModelRequest,
	output stepwise.ModelOutputWriter,
) (stepwise.ModelResponse, error) {
	requestOptions, err := client.requestBody(request)
	if err != nil {
		return stepwise.ModelResponse{}, err
	}
	stream := client.client.Responses.NewStreaming(ctx, responses.ResponseNewParams{Model: client.model}, requestOptions...)
	itemsByIndex := map[int64]json.RawMessage{}
	var completed *responsesCompleted
	for stream.Next() {
		var event responsesStreamEvent
		if err := json.Unmarshal([]byte(stream.Current().RawJSON()), &event); err != nil {
			return stepwise.ModelResponse{}, errors.Join(fmt.Errorf("decode a Responses API event: %w", err), stream.Close())
		}
		switch event.Type {
		case "response.output_text.delta", "response.refusal.delta":
			err = output.WriteTextDelta(event.Delta)
		case "response.reasoning_summary_text.delta":
			err = output.WriteReasoningDelta(event.Delta)
		case "response.output_item.done":
			itemsByIndex[event.OutputIndex] = event.Item
		case "response.completed", "response.incomplete":
			completed = &responsesCompleted{}
			err = json.Unmarshal(event.Response, completed)
		case "response.failed", "error":
			err = fmt.Errorf("the Responses API stream failed: %s", stream.Current().RawJSON())
		}
		if err != nil {
			return stepwise.ModelResponse{}, errors.Join(err, stream.Close())
		}
	}
	if err := errors.Join(stream.Err(), stream.Close()); err != nil {
		return stepwise.ModelResponse{}, err
	}
	if completed == nil {
		return stepwise.ModelResponse{}, errors.New("the Responses API stream ended without response.completed")
	}
	return client.response(itemsByIndex, completed)
}

func (client *ResponsesModelClient) requestBody(request stepwise.ModelRequest) ([]option.RequestOption, error) {
	input, err := responsesInput(request.InputItems, client.providerName)
	if err != nil {
		return nil, err
	}
	reasoning := map[string]any{"summary": "auto"}
	if request.Settings.ReasoningEffort != nil {
		reasoning["effort"] = *request.Settings.ReasoningEffort
	}
	fields := map[string]any{
		"input":        input,
		"instructions": request.Instructions,
		"include":      []string{"reasoning.encrypted_content"},
		"store":        false,
		"reasoning":    reasoning,
	}
	if request.Settings.Temperature != nil {
		fields["temperature"] = *request.Settings.Temperature
	}
	if len(request.Tools) > 0 {
		tools := make([]map[string]any, 0, len(request.Tools))
		for _, tool := range request.Tools {
			tools = append(tools, map[string]any{
				"type": "function", "name": tool.Name, "description": tool.Description,
				"parameters": tool.Parameters, "strict": false,
			})
		}
		fields["tools"] = tools
		fields["tool_choice"] = responsesToolChoice(request.ToolChoice)
	}
	if request.Settings.MaxOutputTokens != nil {
		fields["max_output_tokens"] = *request.Settings.MaxOutputTokens
	}
	maps.Copy(fields, request.Settings.ProviderOptions)
	requestOptions := append([]option.RequestOption{}, client.requestOptions...)
	for key, value := range fields {
		requestOptions = append(requestOptions, option.WithJSONSet(key, value))
	}
	return append(requestOptions, option.WithMaxRetries(0)), nil
}

func (client *ResponsesModelClient) response(
	itemsByIndex map[int64]json.RawMessage,
	completed *responsesCompleted,
) (stepwise.ModelResponse, error) {
	indexes := make([]int64, 0, len(itemsByIndex))
	for index := range itemsByIndex {
		indexes = append(indexes, index)
	}
	slices.Sort(indexes)
	response := stepwise.ModelResponse{
		OutputItems:   []stepwise.ModelOutputItem{},
		ToolCalls:     []stepwise.ModelToolCall{},
		ResponseModel: completed.Model,
		FinishReason:  completed.Status,
		Usage: dexagents.AgentUsage{
			ModelCalls:        1,
			InputTokens:       completed.Usage.InputTokens,
			OutputTokens:      completed.Usage.OutputTokens,
			CachedInputTokens: completed.Usage.InputTokensDetails.CachedTokens,
			ReasoningTokens:   completed.Usage.OutputTokensDetails.ReasoningTokens,
		},
	}
	for _, index := range indexes {
		item, err := client.outputItem(itemsByIndex[index])
		if err != nil {
			return stepwise.ModelResponse{}, err
		}
		response.OutputItems = append(response.OutputItems, item)
		response.ToolCalls = append(response.ToolCalls, item.ToolCalls...)
		if item.Text != nil {
			response.Text += *item.Text
		}
	}
	return response, nil
}

type responsesItemProjection struct {
	Type      string  `json:"type"`
	CallID    *string `json:"call_id"`
	Name      *string `json:"name"`
	Arguments *string `json:"arguments"`
	Content   []struct {
		Text    *string `json:"text"`
		Refusal *string `json:"refusal"`
	} `json:"content"`
}

func (client *ResponsesModelClient) outputItem(raw json.RawMessage) (stepwise.ModelOutputItem, error) {
	parsed, err := pythonjson.ParseOrdered(raw)
	if err != nil {
		return stepwise.ModelOutputItem{}, fmt.Errorf("decode a Responses API output item: %w", err)
	}
	object, isObject := parsed.(pythonjson.Object)
	if !isObject {
		return stepwise.ModelOutputItem{}, errors.New("a Responses API output item is not an object")
	}
	var projection responsesItemProjection
	if err := json.Unmarshal(raw, &projection); err != nil {
		return stepwise.ModelOutputItem{}, fmt.Errorf("decode a Responses API output item: %w", err)
	}
	stored := object
	if fields, hasReplayFields := responsesReplayFields[projection.Type]; hasReplayFields {
		stored = pythonjson.Object{}
		for _, field := range fields {
			for _, member := range object {
				if member.Key == field {
					stored = append(stored, member)
				}
			}
		}
	}
	itemText, err := pythonjson.Marshal(stored, pythonjson.Options{IsCompact: true})
	if err != nil {
		return stepwise.ModelOutputItem{}, err
	}
	item := stepwise.ModelOutputItem{Provider: client.providerName, API: ResponsesAPI, Item: itemText}
	switch projection.Type {
	case "message":
		text := ""
		for _, part := range projection.Content {
			if part.Text != nil && *part.Text != "" {
				text += *part.Text
			} else if part.Refusal != nil {
				text += *part.Refusal
			}
		}
		if text != "" {
			item.Text = &text
		}
	case "function_call":
		if projection.CallID == nil || projection.Name == nil {
			return stepwise.ModelOutputItem{}, errors.New("a Responses API function_call item has no call_id or name")
		}
		arguments := "{}"
		if projection.Arguments != nil && *projection.Arguments != "" {
			arguments = *projection.Arguments
		}
		item.ToolCalls = []stepwise.ModelToolCall{{ToolCallID: *projection.CallID, ToolName: *projection.Name, Arguments: arguments}}
	}
	return item, nil
}

// EstimateInputTokens estimates four characters per token over the request text and items.
//
// The Python core counts with LiteLLM's tokenizer instead; this estimate only
// orders compaction, a later contract addition.
func (client *ResponsesModelClient) EstimateInputTokens(request stepwise.ModelRequest) int64 {
	characters := len(request.Instructions)
	for _, item := range request.InputItems {
		switch typed := item.(type) {
		case stepwise.UserTextItem:
			characters += len(typed.Text)
		case stepwise.ToolResultItem:
			characters += len(typed.Output)
		case stepwise.ModelOutputItem:
			characters += len(typed.Item)
		}
	}
	for _, tool := range request.Tools {
		characters += len(tool.Name) + len(tool.Description) + len(tool.Parameters)
	}
	return int64(characters / 4)
}

// responsesInput converts the request items; a stored item of this provider replays unchanged.
func responsesInput(items []stepwise.ModelInputItem, providerName string) ([]json.RawMessage, error) {
	converted := []json.RawMessage{}
	appendJSON := func(value any) error {
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		converted = append(converted, encoded)
		return nil
	}
	for _, item := range items {
		var err error
		switch typed := item.(type) {
		case stepwise.UserTextItem:
			err = appendJSON(map[string]any{"type": "message", "role": "user", "content": typed.Text})
		case stepwise.ToolResultItem:
			err = appendJSON(map[string]any{"type": "function_call_output", "call_id": typed.ToolCallID, "output": typed.Output})
		case stepwise.ModelOutputItem:
			if typed.Provider == providerName && typed.API == ResponsesAPI {
				if !json.Valid([]byte(typed.Item)) {
					return nil, fmt.Errorf("stored %s item is not JSON", ResponsesAPI)
				}
				converted = append(converted, json.RawMessage(typed.Item))
				continue
			}
			if typed.Text != nil && *typed.Text != "" {
				err = appendJSON(map[string]any{"type": "message", "role": "assistant", "content": *typed.Text})
			}
			for _, call := range typed.ToolCalls {
				if err != nil {
					break
				}
				err = appendJSON(map[string]any{
					"type": "function_call", "call_id": call.ToolCallID, "name": call.ToolName, "arguments": call.Arguments,
				})
			}
		}
		if err != nil {
			return nil, err
		}
	}
	return converted, nil
}

func responsesToolChoice(choice stepwise.ToolChoice) any {
	if choice.Mode == stepwise.ToolChoiceModeNamed {
		return map[string]any{"type": "function", "name": choice.ToolName}
	}
	if choice.Mode == "" {
		return string(stepwise.ToolChoiceModeAuto)
	}
	return string(choice.Mode)
}
