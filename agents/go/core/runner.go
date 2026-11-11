// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dexagents

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// OutcomeUnknownToolResult is the model-visible text for a call whose outcome is unknown.
const OutcomeUnknownToolResult = "The tool call's outcome is unknown: it stopped without returning a result, " +
	"so it may or may not have taken effect. It will not be retried automatically."

// ToolCallNotRunErrorPrefix starts the error of a failed record whose tool Step certainly never ran
// the tool. The record's error is this prefix, ": ", and the recovery detail, so the model sees a
// failed call that it may make again.
const ToolCallNotRunErrorPrefix = "the tool did not run, so it is safe to call again"

// TurnFailureNoteHeader is the first line of the note RecordTurnFailure stores after a failed
// turn ran tools.
const TurnFailureNoteHeader = "[Dex] The previous turn failed after these tool calls ran:"

// TurnFailureNoteFooter is the last line of that note.
const TurnFailureNoteFooter = "Do not repeat these tool calls unless the user asks again."

// DefaultRejectionToolResult is the model-visible text for a rejection without a message.
const DefaultRejectionToolResult = "The user rejected this tool call."

// ApprovalDeadlinePassed is the rejection message recorded when an approval deadline passes.
const ApprovalDeadlinePassed = "approval deadline passed"

// MissingUnfinishedTurnMessageBuilder is the last_turn_error of a turn whose runner set
// ToolCallsRequested.NewMessages without implementing UnfinishedTurnMessageBuilder.
const MissingUnfinishedTurnMessageBuilder = "a runner that sets ToolCallsRequested.new_messages " +
	"must implement build_unfinished_turn_messages"

// ToolPolicy declares how the session treats calls to one tool.
//
// Pass policies by tool name in AgentSessionOptions; a policy overrides
// framework metadata and the session's default tool effect. Every tool call
// runs in its own Step; the policy picks that Step and whether it waits for
// approval.
//
//	options := dexagents.AgentSessionOptions{ToolPolicies: map[string]dexagents.ToolPolicy{
//		"search_tickets": {Effect: dexagents.ToolEffectRead},
//		"create_refund":  {Effect: dexagents.ToolEffectIdempotentWrite, RequiresApproval: ptr.Any(true)},
//		"send_email":     {Effect: dexagents.ToolEffectNonIdempotentWrite},
//	}}
type ToolPolicy struct {
	// Effect is what the tool does outside the session.
	Effect ToolEffect
	// RequiresApproval reports whether a person approves each call. Nil uses the effect's
	// default: false for read and idempotent_write, true for non_idempotent_write and
	// unclassified.
	RequiresApproval *bool
}

// IsApprovalRequired reports whether each call waits for the resolve_interrupt RPC.
//
// It returns RequiresApproval when set, otherwise the effect's default.
func (policy ToolPolicy) IsApprovalRequired() bool {
	if policy.RequiresApproval != nil {
		return *policy.RequiresApproval
	}
	return policy.Effect == ToolEffectNonIdempotentWrite || policy.Effect == ToolEffectUnclassified
}

// ResolveToolPolicy returns the policy that applies to one tool.
//
// Runners normally call AgentTurnContext.ResolveToolPolicy instead. An
// explicit policy in toolPolicies wins over frameworkEffect, which wins over
// defaultToolEffect; pass nil for frameworkEffect when the framework says
// nothing. The returned policy always has RequiresApproval set.
func ResolveToolPolicy(
	toolName string,
	toolPolicies map[string]ToolPolicy,
	defaultToolEffect ToolEffect,
	frameworkEffect *ToolEffect,
) ToolPolicy {
	policy, isConfigured := toolPolicies[toolName]
	if !isConfigured {
		effect := defaultToolEffect
		if frameworkEffect != nil {
			effect = *frameworkEffect
		}
		policy = ToolPolicy{Effect: effect}
	}
	isApprovalRequired := policy.IsApprovalRequired()
	return ToolPolicy{Effect: policy.Effect, RequiresApproval: &isApprovalRequired}
}

// DescribeToolCallOutcome returns the tool result text the model should see for a final record.
//
// Use it when resuming a turn: give the framework this text as the tool's
// result instead of running the tool again. It returns the output for
// succeeded, "The tool call failed: <error>" for failed, the rejection
// message or DefaultRejectionToolResult for rejected, and
// OutcomeUnknownToolResult for outcome_unknown. It returns an error for a
// record that is not final.
func DescribeToolCallOutcome(toolCall AgentToolCall) (string, error) {
	switch toolCall.Status {
	case ToolCallStatusSucceeded:
		return stringOrEmpty(toolCall.Output), nil
	case ToolCallStatusFailed:
		errorText := "unknown error"
		if toolCall.Error != nil && *toolCall.Error != "" {
			errorText = *toolCall.Error
		}
		return "The tool call failed: " + errorText, nil
	case ToolCallStatusRejected:
		if toolCall.RejectionMessage != nil && *toolCall.RejectionMessage != "" {
			return *toolCall.RejectionMessage, nil
		}
		return DefaultRejectionToolResult, nil
	case ToolCallStatusOutcomeUnknown:
		return OutcomeUnknownToolResult, nil
	default:
		return "", fmt.Errorf("tool call %s has no final outcome yet", toolCall.ToolCallID)
	}
}

// BuildTurnFailureNote returns the note that tells the model what a failed turn already did.
//
// RecordTurnFailure stores this note through BuildUserMessage when a failed
// turn has final tool call records, so the next turn's model does not repeat
// a write. The note has one line per record, in DeferralOrder:
//
//	[Dex] The previous turn failed after these tool calls ran:
//	- charge {"amount": 5} -> succeeded: charged 5
//	Do not repeat these tool calls unless the user asks again.
//
// It returns an error when a record is not final.
func BuildTurnFailureNote(toolCalls []AgentToolCall) (string, error) {
	return BuildToolCallNote(TurnFailureNoteHeader, toolCalls)
}

// BuildToolCallNote returns a note in the turn-failure note's line format with another first line.
//
// It returns header, one "- <tool_name> <arguments> -> <status>: <result>"
// line per record in DeferralOrder, and TurnFailureNoteFooter, joined with
// newlines, or an error when a record is not final.
func BuildToolCallNote(header string, toolCalls []AgentToolCall) (string, error) {
	ordered := slices.Clone(toolCalls)
	sort.SliceStable(ordered, func(left, right int) bool {
		return ordered[left].DeferralOrder < ordered[right].DeferralOrder
	})
	lines := []string{header}
	for _, toolCall := range ordered {
		outcome, err := DescribeToolCallOutcome(toolCall)
		if err != nil {
			return "", err
		}
		lines = append(lines, fmt.Sprintf("- %s %s -> %s: %s", toolCall.ToolName, toolCall.Arguments, toolCall.Status, outcome))
	}
	lines = append(lines, TurnFailureNoteFooter)
	return strings.Join(lines, "\n"), nil
}

// FrameworkMessage describes one framework item that the session should store.
//
// A runner returns these from BuildUserMessage, in TurnCompleted.NewMessages,
// and in ToolCallsRequested.NewMessages. The session adds the sequence, turn
// ID, and creation time, and stores each as an AgentMessage. On later turns
// the runner receives them back in AgentTurnContext.Messages and rebuilds
// framework input from FrameworkItem alone. Set the Tool fields only when the
// item carries exactly one tool call or result.
type FrameworkMessage struct {
	// Role is the author of the item. It must be user, assistant, or tool.
	Role MessageRole
	// FrameworkItem is the framework-native JSON text, replayed unchanged.
	FrameworkItem string
	// Text is the display text, or nil.
	Text *string
	// ToolCallID is the tool call ID of a tool request or result item, or nil.
	ToolCallID *string
	// ToolName is the tool name of a tool request or result item, or nil.
	ToolName *string
	// ToolArguments is the tool arguments as JSON text, or nil.
	ToolArguments *string
	// ToolOutput is the tool result text, or nil.
	ToolOutput *string
}

// DeferredToolCall asks the session to run one tool call in its own Dex Step.
//
// Return one in ToolCallsRequested.ToolCalls for every tool call of the model
// response. The session records each call, starts one Step per call in
// parallel, raises an approval interrupt first when the policy requires it,
// and calls RunModelStep again once every call has a final record.
type DeferredToolCall struct {
	// ToolCallID is the framework tool call ID. It must be non-empty, contain no "/", and be
	// unique within one turn. IDs may repeat across turns.
	ToolCallID string
	// ToolName is the tool name the model called.
	ToolName string
	// Arguments is the tool arguments as JSON text.
	Arguments string
	// Policy is the policy from AgentTurnContext.ResolveToolPolicy.
	Policy ToolPolicy
	// ToolSourceReference is opaque text stored on the record and returned to the tool Step, or
	// nil.
	ToolSourceReference *string
}

// AgentTurnOutcome is the result of one RunModelStep call: *TurnCompleted or *ToolCallsRequested.
type AgentTurnOutcome interface {
	agentTurnOutcome()
}

// TurnCompleted reports that the model call produced the turn's final answer.
type TurnCompleted struct {
	// NewMessages are the turn's items that the session has not stored yet, in order. For a
	// runner that never sets ToolCallsRequested.NewMessages, that is every item the turn produced
	// after its user message. The session appends them in one commit.
	NewMessages []FrameworkMessage
	// Usage is the usage of this model call only.
	Usage AgentUsage
}

func (*TurnCompleted) agentTurnOutcome() {}

// ToolCallsRequested reports that the model call asked for tools, which the session runs as Steps.
//
// The session commits the run state, NewMessages, and one record per tool call
// in one Step result, then starts the tool call Steps in parallel. A runner
// either keeps the turn's items in FrameworkRunState until the turn completes,
// or stores them as they appear through NewMessages. A runner that sets
// NewMessages must implement UnfinishedTurnMessageBuilder; otherwise the turn
// fails with MissingUnfinishedTurnMessageBuilder.
type ToolCallsRequested struct {
	// FrameworkRunState is framework-serialized state that continues the turn after the tools
	// return. It must be non-empty.
	FrameworkRunState string
	// ToolCalls holds at least one tool call, in the order the model made them.
	ToolCalls []DeferredToolCall
	// Usage is the usage of this model call only.
	Usage AgentUsage
	// NewMessages are items the session appends in the same commit that records the tool calls.
	NewMessages []FrameworkMessage
}

func (*ToolCallsRequested) agentTurnOutcome() {}

// DeferredToolOutcome is the result of one ExecuteDeferredToolCall call: *ToolExecutionResult.
//
// The interface is sealed so later contract additions, such as tool input
// requests, can add results without changing the runner interface.
type DeferredToolOutcome interface {
	deferredToolOutcome()
}

// ToolExecutionResult returns the output of one deferred tool call.
type ToolExecutionResult struct {
	// Output is the text the model sees.
	Output string
}

func (*ToolExecutionResult) deferredToolOutcome() {}

// ToolExecutionError reports a tool failure that the model should see.
//
// Return it from ExecuteDeferredToolCall, directly or wrapped, when the tool
// ran and failed in a way the model can react to, such as invalid arguments.
// The session records failed with Message and does not retry. Any other error
// fails the Step attempt: a read or idempotent_write call retries up to three
// attempts, and when the attempts run out the session records failed for a
// read call and outcome_unknown for a write.
//
//	return nil, &dexagents.ToolExecutionError{Message: "order 42 is already shipped"}
type ToolExecutionError struct {
	// Message is the text the model sees after "The tool call failed: ".
	Message string
}

// Error returns Message.
func (toolError *ToolExecutionError) Error() string {
	return toolError.Message
}

// AgentTurnContext gives RunModelStep everything it needs for one model call of a turn.
//
// The session builds a new context for every RunModel attempt from committed
// state. A retry after a crash receives the same messages, run state, and
// records as the failed attempt, with a larger Attempt, so only that one model
// call runs again.
type AgentTurnContext struct {
	// FlowID is the session ID, which is the Flow ID.
	FlowID string
	// TurnID is "turn-<n>" of the running turn.
	TurnID string
	// Attempt is the one-based RunModel attempt.
	Attempt int64
	// Messages are every stored message in sequence order. The last user message belongs to this
	// turn.
	Messages []AgentMessage
	// FrameworkRunState is the value from this turn's last ToolCallsRequested, or nil for the
	// turn's first model call.
	FrameworkRunState *string
	// ToolCalls are every tool call record of this turn by tool call ID. When RunModel runs, every
	// record is final.
	ToolCalls map[string]AgentToolCall
	// ToolPolicies are the explicit policies from AgentSessionOptions.
	ToolPolicies map[string]ToolPolicy
	// DefaultToolEffect is the effect for a tool without policy or metadata.
	DefaultToolEffect ToolEffect
	// Events writes live events, bound to this turn and attempt.
	Events *AgentEventWriter
	// Heartbeat records Step liveness. The session already heartbeats in the background; call it
	// only from a loop that could otherwise run longer than the heartbeat timeout unnoticed.
	Heartbeat func() error
	// InteractionMode is the turn's AgentState.InteractionMode. This Go core runs chat turns.
	InteractionMode InteractionMode
	// SessionSettings are the settings this turn started with, such as {"model": "gpt-5-mini"}.
	// Use them to choose the model or the instructions of each model call.
	// update_session_settings changes them from the next turn on, so every model call of one
	// turn sees the same value, retries included. The map is empty, never nil, when the session
	// has no settings. Treat it as read-only. JSON numbers decode as float64.
	SessionSettings map[string]any
}

// IsResuming reports whether this model call continues the turn after tool calls.
//
// It returns true when FrameworkRunState is set.
func (turn AgentTurnContext) IsResuming() bool {
	return turn.FrameworkRunState != nil
}

// ResolveToolPolicy returns the policy for one tool call.
//
// An explicit policy wins over frameworkEffect, which wins over
// DefaultToolEffect; pass nil for frameworkEffect when the framework says
// nothing. Put the returned policy in the DeferredToolCall for that call.
func (turn AgentTurnContext) ResolveToolPolicy(toolName string, frameworkEffect *ToolEffect) ToolPolicy {
	return ResolveToolPolicy(toolName, turn.ToolPolicies, turn.DefaultToolEffect, frameworkEffect)
}

// RecordedToolCall returns this turn's record for a tool call ID.
//
// It returns false when the turn never deferred that call.
func (turn AgentTurnContext) RecordedToolCall(toolCallID string) (AgentToolCall, bool) {
	toolCall, isRecorded := turn.ToolCalls[toolCallID]
	return toolCall, isRecorded
}

// DeferredToolExecution gives ExecuteDeferredToolCall one approved tool call to run.
type DeferredToolExecution struct {
	// FlowID is the session ID, which is the Flow ID.
	FlowID string
	// ToolCall is the approved record.
	ToolCall AgentToolCall
	// IdempotencyKey is "<flowID>/<turnID>/<toolCallID>", the same on every attempt. Pass it to
	// the external system when it supports idempotency.
	IdempotencyKey string
	// Attempt is the one-based tool Step attempt. Read and idempotent_write calls get up to three
	// attempts; other calls get one.
	Attempt int64
	// Events writes live events, bound to the tool call Step attempt.
	Events *AgentEventWriter
	// Heartbeat records Step liveness. The session already heartbeats in the background. Every
	// heartbeat repeats the latest checkpoint.
	Heartbeat func() error
	// HeartbeatCheckpoint is the checkpoint an earlier attempt of this tool Step saved, possibly
	// on a Worker of another core, or nil. Use it to resume remote work that attempt started.
	HeartbeatCheckpoint *string
	// SaveHeartbeatCheckpoint records a heartbeat that carries checkpoint, so the next attempt reads
	// it as HeartbeatCheckpoint. Dex may drop a checkpoint, so pass IdempotencyKey to remote work
	// as well. It returns the heartbeat's error.
	SaveHeartbeatCheckpoint func(checkpoint string) error
}

// AgentFrameworkRunner runs one agent framework, or the built-in agent loop, inside the session Flow.
//
// Implement it once per framework and pass the runner to NewAgentSessionFlow.
// The session calls it from Steps, so every method may run again after a
// crash or retry: keep the runner stateless between calls and derive
// everything from the arguments. Tool calls run in parallel Steps, so
// ExecuteDeferredToolCall may run concurrently with itself. The context
// passed to each method ends with the Step attempt.
//
//	type EchoRunner struct{}
//
//	func (EchoRunner) BuildUserMessage(text string) (dexagents.FrameworkMessage, error) {
//		item, err := json.Marshal(map[string]string{"user": text})
//		if err != nil {
//			return dexagents.FrameworkMessage{}, err
//		}
//		return dexagents.FrameworkMessage{Role: dexagents.MessageRoleUser, Text: &text, FrameworkItem: string(item)}, nil
//	}
//
//	func (EchoRunner) RunModelStep(ctx context.Context, turn dexagents.AgentTurnContext) (dexagents.AgentTurnOutcome, error) {
//		reply := "echo"
//		return &dexagents.TurnCompleted{NewMessages: []dexagents.FrameworkMessage{{
//			Role: dexagents.MessageRoleAssistant, Text: &reply, FrameworkItem: `{"assistant":"echo"}`,
//		}}}, nil
//	}
//
//	func (EchoRunner) ExecuteDeferredToolCall(ctx context.Context, call dexagents.DeferredToolExecution) (dexagents.DeferredToolOutcome, error) {
//		return nil, &dexagents.ToolExecutionError{Message: "echo has no tools"}
//	}
type AgentFrameworkRunner interface {
	// BuildUserMessage turns user text, or a Dex note such as the turn-failure note, into a stored
	// user item. It returns an error when the text cannot be encoded.
	BuildUserMessage(text string) (FrameworkMessage, error)
	// RunModelStep makes at most one model call and runs no tool it can defer. It returns
	// *TurnCompleted or *ToolCallsRequested; an error fails the RunModel attempt.
	RunModelStep(ctx context.Context, turn AgentTurnContext) (AgentTurnOutcome, error)
	// ExecuteDeferredToolCall runs the real tool once. It returns *ToolExecutionResult, a
	// *ToolExecutionError for a failure the model sees, or another error that fails the attempt.
	ExecuteDeferredToolCall(ctx context.Context, call DeferredToolExecution) (DeferredToolOutcome, error)
}

// UnfinishedTurnMessageBuilder is implemented by a runner that stores a turn's items incrementally.
//
// A runner that sets ToolCallsRequested.NewMessages must implement it. The
// session calls it when a turn ends without a final answer, in
// RecordTurnFailure before the turn-failure note, with every record final. It
// returns the items not stored yet, including a tool result for every stored
// tool call, and makes no model call. An error, or a message with an invalid
// role, is logged and the session stores only the note, so a runner defect
// never blocks the failure route.
type UnfinishedTurnMessageBuilder interface {
	// BuildUnfinishedTurnMessages returns the turn's unstored items.
	BuildUnfinishedTurnMessages(ctx context.Context, turn AgentTurnContext) ([]FrameworkMessage, error)
}

func stringOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
