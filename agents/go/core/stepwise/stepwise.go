// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Package stepwise is the old name of package builtinloop, the built-in agent loop.
//
// Every identifier forwards to the same identifier in builtinloop: types are
// aliases, and functions call through. Five names changed: StepwiseAgentRunner
// is builtinloop.BuiltinLoopAgentRunner, NewStepwiseAgentRunner is
// builtinloop.NewBuiltinLoopAgentRunner, NewStepwiseAgentSessionFlow is
// builtinloop.NewBuiltinAgentSessionFlow, and EncodeStepwiseMessage and
// DecodeStepwiseMessage are builtinloop.EncodeBuiltinLoopMessage and
// builtinloop.DecodeBuiltinLoopMessage.
//
//	// Before
//	flow, err := stepwise.NewStepwiseAgentSessionFlow(definition, "SupportAgent")
//	// After
//	flow, err := builtinloop.NewBuiltinAgentSessionFlow(definition, "SupportAgent")
//
// Deprecated: Since 0.5.0. Import github.com/superdurable/dex/agents/go/core/builtinloop instead.
package stepwise

import (
	"context"
	"encoding/json"
	"time"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/agents/go/core/builtinloop"
)

// DefaultMaxModelCallsPerTurn is builtinloop.DefaultMaxModelCallsPerTurn.
//
// Deprecated: Use builtinloop.DefaultMaxModelCallsPerTurn.
const DefaultMaxModelCallsPerTurn = builtinloop.DefaultMaxModelCallsPerTurn

// ToolCallLimitReachedResultFormat is builtinloop.ToolCallLimitReachedResultFormat.
//
// Deprecated: Use builtinloop.ToolCallLimitReachedResultFormat.
const ToolCallLimitReachedResultFormat = builtinloop.ToolCallLimitReachedResultFormat

const (
	// UserTextItemType is builtinloop.UserTextItemType.
	//
	// Deprecated: Use builtinloop.UserTextItemType.
	UserTextItemType = builtinloop.UserTextItemType
	// ModelOutputItemType is builtinloop.ModelOutputItemType.
	//
	// Deprecated: Use builtinloop.ModelOutputItemType.
	ModelOutputItemType = builtinloop.ModelOutputItemType
	// ToolResultItemType is builtinloop.ToolResultItemType.
	//
	// Deprecated: Use builtinloop.ToolResultItemType.
	ToolResultItemType = builtinloop.ToolResultItemType
)

const (
	// ToolChoiceModeAuto is builtinloop.ToolChoiceModeAuto.
	//
	// Deprecated: Use builtinloop.ToolChoiceModeAuto.
	ToolChoiceModeAuto = builtinloop.ToolChoiceModeAuto
	// ToolChoiceModeNone is builtinloop.ToolChoiceModeNone.
	//
	// Deprecated: Use builtinloop.ToolChoiceModeNone.
	ToolChoiceModeNone = builtinloop.ToolChoiceModeNone
	// ToolChoiceModeRequired is builtinloop.ToolChoiceModeRequired.
	//
	// Deprecated: Use builtinloop.ToolChoiceModeRequired.
	ToolChoiceModeRequired = builtinloop.ToolChoiceModeRequired
	// ToolChoiceModeNamed is builtinloop.ToolChoiceModeNamed.
	//
	// Deprecated: Use builtinloop.ToolChoiceModeNamed.
	ToolChoiceModeNamed = builtinloop.ToolChoiceModeNamed
)

// AgentDefinition is builtinloop.AgentDefinition.
//
// Deprecated: Use builtinloop.AgentDefinition.
type AgentDefinition = builtinloop.AgentDefinition

// AgentDefinitionOption is builtinloop.AgentDefinitionOption.
//
// Deprecated: Use builtinloop.AgentDefinitionOption.
type AgentDefinitionOption = builtinloop.AgentDefinitionOption

// StepwiseAgentRunner is builtinloop.BuiltinLoopAgentRunner.
//
// Deprecated: Use builtinloop.BuiltinLoopAgentRunner.
type StepwiseAgentRunner = builtinloop.BuiltinLoopAgentRunner

// FunctionTool is builtinloop.FunctionTool.
//
// Deprecated: Use builtinloop.FunctionTool.
type FunctionTool = builtinloop.FunctionTool

// FunctionToolExecute is builtinloop.FunctionToolExecute.
//
// Deprecated: Use builtinloop.FunctionToolExecute.
type FunctionToolExecute = builtinloop.FunctionToolExecute

// FunctionToolOption is builtinloop.FunctionToolOption.
//
// Deprecated: Use builtinloop.FunctionToolOption.
type FunctionToolOption = builtinloop.FunctionToolOption

// InstructionContext is builtinloop.InstructionContext.
//
// Deprecated: Use builtinloop.InstructionContext.
type InstructionContext = builtinloop.InstructionContext

// InstructionsFunction is builtinloop.InstructionsFunction.
//
// Deprecated: Use builtinloop.InstructionsFunction.
type InstructionsFunction = builtinloop.InstructionsFunction

// ModelClient is builtinloop.ModelClient.
//
// Deprecated: Use builtinloop.ModelClient.
type ModelClient = builtinloop.ModelClient

// ModelInputItem is builtinloop.ModelInputItem.
//
// Deprecated: Use builtinloop.ModelInputItem.
type ModelInputItem = builtinloop.ModelInputItem

// ModelOutputItem is builtinloop.ModelOutputItem.
//
// Deprecated: Use builtinloop.ModelOutputItem.
type ModelOutputItem = builtinloop.ModelOutputItem

// ModelOutputWriter is builtinloop.ModelOutputWriter.
//
// Deprecated: Use builtinloop.ModelOutputWriter.
type ModelOutputWriter = builtinloop.ModelOutputWriter

// ModelRequest is builtinloop.ModelRequest.
//
// Deprecated: Use builtinloop.ModelRequest.
type ModelRequest = builtinloop.ModelRequest

// ModelRequestMetadata is builtinloop.ModelRequestMetadata.
//
// Deprecated: Use builtinloop.ModelRequestMetadata.
type ModelRequestMetadata = builtinloop.ModelRequestMetadata

// ModelResolver is builtinloop.ModelResolver.
//
// Deprecated: Use builtinloop.ModelResolver.
type ModelResolver = builtinloop.ModelResolver

// ModelResponse is builtinloop.ModelResponse.
//
// Deprecated: Use builtinloop.ModelResponse.
type ModelResponse = builtinloop.ModelResponse

// ModelSettings is builtinloop.ModelSettings.
//
// Deprecated: Use builtinloop.ModelSettings.
type ModelSettings = builtinloop.ModelSettings

// ModelToolCall is builtinloop.ModelToolCall.
//
// Deprecated: Use builtinloop.ModelToolCall.
type ModelToolCall = builtinloop.ModelToolCall

// ModelToolDefinition is builtinloop.ModelToolDefinition.
//
// Deprecated: Use builtinloop.ModelToolDefinition.
type ModelToolDefinition = builtinloop.ModelToolDefinition

// ToolCallContext is builtinloop.ToolCallContext.
//
// Deprecated: Use builtinloop.ToolCallContext.
type ToolCallContext = builtinloop.ToolCallContext

// ToolChoice is builtinloop.ToolChoice.
//
// Deprecated: Use builtinloop.ToolChoice.
type ToolChoice = builtinloop.ToolChoice

// ToolChoiceMode is builtinloop.ToolChoiceMode.
//
// Deprecated: Use builtinloop.ToolChoiceMode.
type ToolChoiceMode = builtinloop.ToolChoiceMode

// ToolResultItem is builtinloop.ToolResultItem.
//
// Deprecated: Use builtinloop.ToolResultItem.
type ToolResultItem = builtinloop.ToolResultItem

// UserTextItem is builtinloop.UserTextItem.
//
// Deprecated: Use builtinloop.UserTextItem.
type UserTextItem = builtinloop.UserTextItem

// NewAgentDefinition calls builtinloop.NewAgentDefinition.
//
// It returns the definition and the errors of builtinloop.NewAgentDefinition.
//
// Deprecated: Use builtinloop.NewAgentDefinition.
func NewAgentDefinition(name string, instructions string, model ModelClient, options ...AgentDefinitionOption) (*AgentDefinition, error) {
	return builtinloop.NewAgentDefinition(name, instructions, model, options...)
}

// NewStepwiseAgentRunner calls builtinloop.NewBuiltinLoopAgentRunner.
//
// It returns the runner for definition, which must not be nil.
//
// Deprecated: Use builtinloop.NewBuiltinLoopAgentRunner.
func NewStepwiseAgentRunner(definition *AgentDefinition) *StepwiseAgentRunner {
	return builtinloop.NewBuiltinLoopAgentRunner(definition)
}

// NewStepwiseAgentSessionFlow calls builtinloop.NewBuiltinAgentSessionFlow.
//
// It returns the session Flow and the errors of builtinloop.NewBuiltinAgentSessionFlow.
//
// Deprecated: Use builtinloop.NewBuiltinAgentSessionFlow.
func NewStepwiseAgentSessionFlow(
	definition *AgentDefinition,
	flowType string,
	sessionOptions ...dexagents.AgentSessionOption,
) (*dexagents.AgentSessionFlow, error) {
	return builtinloop.NewBuiltinAgentSessionFlow(definition, flowType, sessionOptions...)
}

// NewFunctionTool calls builtinloop.NewFunctionTool.
//
// It returns the tool and the errors of builtinloop.NewFunctionTool.
//
// Deprecated: Use builtinloop.NewFunctionTool.
func NewFunctionTool(
	name string,
	description string,
	parameters json.RawMessage,
	effect dexagents.ToolEffect,
	execute FunctionToolExecute,
	options ...FunctionToolOption,
) (*FunctionTool, error) {
	return builtinloop.NewFunctionTool(name, description, parameters, effect, execute, options...)
}

// NewTypedFunctionTool calls builtinloop.NewTypedFunctionTool.
//
// It returns the tool and the errors of builtinloop.NewTypedFunctionTool.
//
// Deprecated: Use builtinloop.NewTypedFunctionTool.
func NewTypedFunctionTool[Arguments any, Result any](
	name string,
	description string,
	parameters json.RawMessage,
	effect dexagents.ToolEffect,
	execute func(ctx context.Context, arguments Arguments, call ToolCallContext) (Result, error),
	options ...FunctionToolOption,
) (*FunctionTool, error) {
	return builtinloop.NewTypedFunctionTool(name, description, parameters, effect, execute, options...)
}

// WithRequiresApproval calls builtinloop.WithRequiresApproval.
//
// Deprecated: Use builtinloop.WithRequiresApproval.
func WithRequiresApproval(isApprovalRequired bool) FunctionToolOption {
	return builtinloop.WithRequiresApproval(isApprovalRequired)
}

// WithInstructionsFunction calls builtinloop.WithInstructionsFunction.
//
// Deprecated: Use builtinloop.WithInstructionsFunction.
func WithInstructionsFunction(instructions InstructionsFunction) AgentDefinitionOption {
	return builtinloop.WithInstructionsFunction(instructions)
}

// WithModelResolver calls builtinloop.WithModelResolver.
//
// Deprecated: Use builtinloop.WithModelResolver.
func WithModelResolver(resolveModel ModelResolver) AgentDefinitionOption {
	return builtinloop.WithModelResolver(resolveModel)
}

// WithModelSettings calls builtinloop.WithModelSettings.
//
// Deprecated: Use builtinloop.WithModelSettings.
func WithModelSettings(settings ModelSettings) AgentDefinitionOption {
	return builtinloop.WithModelSettings(settings)
}

// WithFunctionTools calls builtinloop.WithFunctionTools.
//
// Deprecated: Use builtinloop.WithFunctionTools.
func WithFunctionTools(tools ...*FunctionTool) AgentDefinitionOption {
	return builtinloop.WithFunctionTools(tools...)
}

// WithToolPolicies calls builtinloop.WithToolPolicies.
//
// Deprecated: Use builtinloop.WithToolPolicies.
func WithToolPolicies(toolPolicies map[string]dexagents.ToolPolicy) AgentDefinitionOption {
	return builtinloop.WithToolPolicies(toolPolicies)
}

// WithDefaultToolEffect calls builtinloop.WithDefaultToolEffect.
//
// Deprecated: Use builtinloop.WithDefaultToolEffect.
func WithDefaultToolEffect(effect dexagents.ToolEffect) AgentDefinitionOption {
	return builtinloop.WithDefaultToolEffect(effect)
}

// WithMaxModelCallsPerTurn calls builtinloop.WithMaxModelCallsPerTurn.
//
// Deprecated: Use builtinloop.WithMaxModelCallsPerTurn.
func WithMaxModelCallsPerTurn(maxModelCallsPerTurn int) AgentDefinitionOption {
	return builtinloop.WithMaxModelCallsPerTurn(maxModelCallsPerTurn)
}

// WithInterruptDeadline calls builtinloop.WithInterruptDeadline.
//
// Deprecated: Use builtinloop.WithInterruptDeadline.
func WithInterruptDeadline(deadline time.Duration) AgentDefinitionOption {
	return builtinloop.WithInterruptDeadline(deadline)
}

// WithEventStreamCapacityBytes calls builtinloop.WithEventStreamCapacityBytes.
//
// Deprecated: Use builtinloop.WithEventStreamCapacityBytes.
func WithEventStreamCapacityBytes(capacityBytes int64) AgentDefinitionOption {
	return builtinloop.WithEventStreamCapacityBytes(capacityBytes)
}

// EncodeStepwiseMessage calls builtinloop.EncodeBuiltinLoopMessage.
//
// It returns the session message that stores item, or an error for an unknown item type.
//
// Deprecated: Use builtinloop.EncodeBuiltinLoopMessage.
func EncodeStepwiseMessage(item ModelInputItem) (dexagents.FrameworkMessage, error) {
	return builtinloop.EncodeBuiltinLoopMessage(item)
}

// DecodeStepwiseMessage calls builtinloop.DecodeBuiltinLoopMessage.
//
// It returns the model input item that message holds, or an error when the
// built-in agent loop did not store it.
//
// Deprecated: Use builtinloop.DecodeBuiltinLoopMessage.
func DecodeStepwiseMessage(message dexagents.AgentMessage) (ModelInputItem, error) {
	return builtinloop.DecodeBuiltinLoopMessage(message)
}

// PairToolCallsWithResults calls builtinloop.PairToolCallsWithResults.
//
// It returns the items with exactly one result after every tool call.
//
// Deprecated: Use builtinloop.PairToolCallsWithResults.
func PairToolCallsWithResults(items []ModelInputItem) []ModelInputItem {
	return builtinloop.PairToolCallsWithResults(items)
}
