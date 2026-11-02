// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package stepwise

import (
	"context"
	"encoding/json"

	dexagents "github.com/superdurable/dex/agents/go/core"
)

// ModelSettings tunes every model call of one Stepwise agent.
//
// A nil field leaves the setting to the provider's default. ProviderOptions
// pass through to the provider request unchanged, so a client can expose a
// provider feature that Dex does not model.
//
//	temperature := 0.2
//	maxOutputTokens := int64(4000)
//	settings := stepwise.ModelSettings{Temperature: &temperature, MaxOutputTokens: &maxOutputTokens}
type ModelSettings struct {
	// Temperature is the sampling temperature, or nil.
	Temperature *float64
	// MaxOutputTokens is the upper bound of output tokens per call, or nil.
	MaxOutputTokens *int64
	// ReasoningEffort is the provider reasoning effort, such as "low", or nil.
	ReasoningEffort *string
	// ProviderOptions are extra request fields, passed through unchanged. Each model client
	// documents how it applies them.
	ProviderOptions map[string]any
}

// ModelToolCall describes one tool call a model response asked for.
type ModelToolCall struct {
	// ToolCallID is the provider tool call ID. It is non-empty and has no "/".
	ToolCallID string
	// ToolName is the tool name the model called.
	ToolName string
	// Arguments is the tool arguments as JSON text, exactly as the model wrote them.
	Arguments string
}

// ModelInputItem is one item of a model request: UserTextItem, ModelOutputItem, or ToolResultItem.
type ModelInputItem interface {
	modelInputItem()
}

// UserTextItem carries user text, or a note Dex adds as user text, into a model request.
type UserTextItem struct {
	// Text is the text.
	Text string
}

func (UserTextItem) modelInputItem() {}

// ModelOutputItem stores one output item exactly as a provider returned it.
//
// Item is the provider's JSON text, opaque to Dex. Its opaque fields, such as
// thinking signatures and encrypted reasoning content, are kept as the exact
// strings the provider sent. A client replays Item unchanged when Provider and
// API match its own. Otherwise it replays Text and ToolCalls in a neutral form
// and drops the rest, such as reasoning.
type ModelOutputItem struct {
	// Provider is the provider that produced the item, such as "openai".
	Provider string
	// API is the provider API that produced it, such as "responses".
	API string
	// Item is the provider JSON text of the item.
	Item string
	// Text is the visible text of the item, or nil when it has none.
	Text *string
	// ToolCalls are the tool calls the item asks for, in order.
	ToolCalls []ModelToolCall
}

func (ModelOutputItem) modelInputItem() {}

// ToolResultItem carries one tool result into a model request.
type ToolResultItem struct {
	// ToolCallID is the tool call this result answers.
	ToolCallID string
	// ToolName is the tool name of that call.
	ToolName string
	// Output is the result text the model sees.
	Output string
}

func (ToolResultItem) modelInputItem() {}

// ModelToolDefinition describes one tool to the model.
type ModelToolDefinition struct {
	// Name is the tool name the model calls.
	Name string
	// Description is the model-visible description.
	Description string
	// Parameters is the JSON Schema object of the tool arguments.
	Parameters json.RawMessage
}

// ToolChoiceMode names how the model may use the request's tools.
type ToolChoiceMode string

const (
	// ToolChoiceModeAuto lets the model call tools or answer.
	ToolChoiceModeAuto ToolChoiceMode = "auto"
	// ToolChoiceModeNone forbids tool calls.
	ToolChoiceModeNone ToolChoiceMode = "none"
	// ToolChoiceModeRequired requires at least one tool call.
	ToolChoiceModeRequired ToolChoiceMode = "required"
	// ToolChoiceModeNamed requires a call to ToolChoice.ToolName.
	ToolChoiceModeNamed ToolChoiceMode = "named"
)

// ToolChoice says how the model may use the request's tools.
type ToolChoice struct {
	// Mode is the choice.
	Mode ToolChoiceMode
	// ToolName names the forced tool when Mode is ToolChoiceModeNamed, and is empty otherwise.
	ToolName string
}

// ModelRequestMetadata identifies the RunModel attempt that makes one model request.
type ModelRequestMetadata struct {
	// FlowID is the session ID, which is the Flow ID.
	FlowID string
	// TurnID is "turn-<n>" of the running turn.
	TurnID string
	// StepExecutionID is the Dex Step execution ID of the RunModel Step.
	StepExecutionID string
	// Attempt is the one-based RunModel attempt.
	Attempt int64
}

// ModelRequest describes one model call: the whole input, the tools, and the settings.
//
// The runner builds it from committed session state only, so a retried
// RunModel attempt sends the same request. A client sends the whole input
// every time and uses no provider-side conversation state.
type ModelRequest struct {
	// Instructions are the system instructions.
	Instructions string
	// InputItems are the conversation in order. Every model output item with tool calls is
	// followed by a result for each call.
	InputItems []ModelInputItem
	// Tools are the tools the model may call. Empty means no tools.
	Tools []ModelToolDefinition
	// ToolChoice says how the model may use Tools.
	ToolChoice ToolChoice
	// Settings are the model settings of the agent.
	Settings ModelSettings
	// Metadata identifies the RunModel attempt that makes the call.
	Metadata ModelRequestMetadata
}

// ModelResponse returns the result of one model call.
type ModelResponse struct {
	// OutputItems are every output item of the response, provider-exact and in order. The
	// session stores each one.
	OutputItems []ModelOutputItem
	// Text is the visible text of the response, for display.
	Text string
	// ToolCalls are every tool call of the response, in order.
	ToolCalls []ModelToolCall
	// Usage is the usage of this one request. The runner sets ModelCalls to 1.
	Usage dexagents.AgentUsage
	// ResponseModel is the model name the provider reported, or "".
	ResponseModel string
	// FinishReason is the provider finish reason, such as "stop", or "".
	FinishReason string
}

// ModelOutputWriter receives the live output of one model call.
//
// The runner passes the turn's *dexagents.AgentEventWriter, so each chunk
// becomes a text_delta or reasoning_delta event on AgentEvents.
type ModelOutputWriter interface {
	// WriteTextDelta writes one chunk of visible model text.
	WriteTextDelta(text string) error
	// WriteReasoningDelta writes one chunk of a provider-authored reasoning summary.
	WriteReasoningDelta(text string) error
}

// ModelClient makes exactly one model request for one RunModel attempt.
//
// Implement it to connect Stepwise mode to a model provider, or use
// openaimodel or anthropicmodel. Every client follows four rules:
//
//   - One request per call, with no retries of its own. Retries are RunModel
//     attempts, so each one is visible in the Flow history.
//   - Dex owns the conversation. Send the whole input every time and use no
//     provider-side conversation state.
//   - Provider-exact transcript. Return each output item as the provider sent
//     it, and replay a stored item unchanged when it comes from the same
//     provider and API.
//   - Read provider credentials from the Worker's environment or a credential
//     provider, never from Flow input or Attributes.
//
// CallModel may be called concurrently for different sessions.
type ModelClient interface {
	// CallModel makes one model request and streams its output to output. Any error fails the
	// RunModel attempt, which Dex retries under the Step's retry policy.
	CallModel(ctx context.Context, request ModelRequest, output ModelOutputWriter) (ModelResponse, error)
	// EstimateInputTokens estimates the input size of a request without making it, including
	// opaque stored items. Context compaction, a later contract addition, uses it.
	EstimateInputTokens(request ModelRequest) int64
}
