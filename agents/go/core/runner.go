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
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/superdurable/dex/sdk-go/dex"
)

// OutcomeUnknownToolResult is the model-visible text for a call whose outcome is unknown.
const OutcomeUnknownToolResult = "The tool call's outcome is unknown: it stopped without returning a result, " +
	"so it may or may not have taken effect. It will not be retried automatically."

// ToolCallNotRunErrorPrefix starts the error of a failed record whose tool Step certainly never ran
// the tool. The record's error is this prefix, ": ", and the recovery detail, so the model sees a
// failed call that it may make again.
const ToolCallNotRunErrorPrefix = "the tool did not run, so it is safe to call again"

// ToolCallSkippedAfterWriteDetail follows ToolCallNotRunErrorPrefix and ": " in the error of a
// record that write ordering skipped.
//
// A call whose DeferredToolCall.Ordered is set waits for the earlier ordered
// writes of its turn. When one of them does not succeed, the session records
// each later ordered write failed with this error and AgentToolCall.Skipped
// set, and never runs it. The text is the same in every core.
//
// Since 0.6.16.
const ToolCallSkippedAfterWriteDetail = "an earlier write of this turn did not succeed"

// ConnectorMaxSendsCeiling is the largest ToolPolicy.MaxSends: one connector tool call sends its
// write at most this many times.
//
// Since 0.6.16.
const ConnectorMaxSendsCeiling int32 = 10

// TurnFailureNoteHeader is the first line of the note RecordTurnFailure stores after a failed
// turn ran tools.
const TurnFailureNoteHeader = "[Dex] The previous turn failed after these tool calls ran:"

// TurnFailureNoteFooter is the last line of that note.
const TurnFailureNoteFooter = "Do not repeat these tool calls unless the user asks again."

// ToolCallNoteMaxOutputBytes is the most UTF-8 bytes of one tool call's outcome that a tool call
// note keeps.
//
// BuildToolCallNote and BuildTurnFailureNote cut a longer outcome at the last whole rune within
// this budget and append ToolCallNoteTruncationMarker. The stored tool call record keeps the full
// output. Every later turn resends the note, so the budget bounds that cost.
// The note uses a copy of each outcome with each invalid UTF-8 sequence replaced by U+FFFD, and
// counts bytes on that copy.
//
// Since 0.6.12.
const ToolCallNoteMaxOutputBytes = 2000

// ToolCallNoteTruncationMarker is the marker a tool call note appends to a cut outcome.
//
// {omitted_bytes} is the UTF-8 size of the cut part and {total_bytes} the size of the whole
// outcome, both in plain decimal digits.
//
// Since 0.6.12.
const ToolCallNoteTruncationMarker = "\u2026 [output truncated: {omitted_bytes} of {total_bytes} bytes omitted]"

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
//		"slack_post_message": {Effect: dexagents.ToolEffectNonIdempotentWrite, MaxSends: ptr.Any(int32(5))},
//	}}
type ToolPolicy struct {
	// Effect is what the tool does outside the session. A connector tool's effect always comes
	// from its connector metadata, so this field does not change it.
	Effect ToolEffect
	// RequiresApproval reports whether a person approves each call. Nil uses the effect's
	// default: false for read and idempotent_write, true for non_idempotent_write and
	// unclassified.
	RequiresApproval *bool
	// MaxSends is the most times one call of a connector tool may send its write when the
	// outcome of a send is unknown, from 1 to ConnectorMaxSendsCeiling, or nil to keep the
	// connector's own cap.
	//
	// It replaces the cap of a connector Mutation whose RepeatOnUnknown is limited; 1 turns
	// resending off. Every other tool ignores it. A call keeps the value it was deferred with on
	// every attempt, so a later deploy that changes it affects only new calls. A raised value
	// also raises the call's attempt limit by the difference, so the connector can reach it.
	// NewAgentSessionFlow returns an error for a value outside 1 to ConnectorMaxSendsCeiling.
	//
	// Since 0.6.16.
	MaxSends *int32
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
// nothing. The returned policy always has RequiresApproval set, and keeps an
// explicit policy's MaxSends.
//
// Changed in 0.6.16: the returned policy keeps MaxSends.
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
	return ToolPolicy{Effect: policy.Effect, RequiresApproval: &isApprovalRequired, MaxSends: policy.MaxSends}
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
// An outcome longer than ToolCallNoteMaxOutputBytes keeps its longest prefix of whole runes within
// that budget, followed by ToolCallNoteTruncationMarker. The records themselves are unchanged.
//
// Changed in 0.6.12: long outcomes are cut to ToolCallNoteMaxOutputBytes.
//
// It returns an error when a record is not final.
func BuildTurnFailureNote(toolCalls []AgentToolCall) (string, error) {
	return BuildToolCallNote(TurnFailureNoteHeader, toolCalls)
}

// BuildToolCallNote returns a note in the turn-failure note's line format with another first line.
//
// It returns header, one "- <tool_name> <arguments> -> <status>: <result>"
// line per record in DeferralOrder, with long results cut as in
// BuildTurnFailureNote, and TurnFailureNoteFooter, joined with newlines, or an
// error when a record is not final.
//
// Changed in 0.6.12: long outcomes are cut to ToolCallNoteMaxOutputBytes.
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
		lines = append(lines, fmt.Sprintf("- %s %s -> %s: %s", toolCall.ToolName, toolCall.Arguments, toolCall.Status, truncateNoteOutcome(outcome)))
	}
	lines = append(lines, TurnFailureNoteFooter)
	return strings.Join(lines, "\n"), nil
}

// truncateNoteOutcome returns the valid UTF-8 outcome, cut to the note budget with the marker when
// longer.
func truncateNoteOutcome(outcome string) string {
	// Every core cuts the same text; JSON decoding already turned lone surrogates into U+FFFD.
	text := strings.ToValidUTF8(outcome, string(utf8.RuneError))
	totalBytes := len(text)
	if totalBytes <= ToolCallNoteMaxOutputBytes {
		return text
	}
	keptBytes := ToolCallNoteMaxOutputBytes
	for !utf8.RuneStart(text[keptBytes]) {
		keptBytes--
	}
	marker := strings.NewReplacer(
		"{omitted_bytes}", strconv.Itoa(totalBytes-keptBytes),
		"{total_bytes}", strconv.Itoa(totalBytes),
	).Replace(ToolCallNoteTruncationMarker)
	return text[:keptBytes] + marker
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
	// Role is the author of the item. It must be user, assistant, or tool; MessageRole.IsValid
	// checks it. The session never stores any other value, the empty string included, because
	// every reader of a stored message rejects it. Such a message in TurnCompleted.NewMessages or
	// ToolCallsRequested.NewMessages fails the turn at once (see AgentFrameworkRunner.RunModelStep).
	// When UnfinishedTurnMessageBuilder returns one, the session logs a warning and stores none of
	// the messages it returned, only the turn-failure note. One from BuildUserMessage fails the Step
	// attempt.
	//
	// Changed in 0.6.16: an invalid Role in NewMessages fails the turn instead of being stored.
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
// and calls RunModelStep again once every call has a final record. Ordered
// calls are the exception: they run one at a time, as Ordered describes.
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
	// Ordered reports whether the call is a write that runs in the model's order after the turn's
	// earlier ordered calls.
	//
	// The session starts one ordered call at a time, raises its approval only when its turn
	// comes, and records the later ordered calls failed with AgentToolCall.Skipped and
	// ToolCallSkippedAfterWriteDetail once one does not succeed. A read call ignores it. False,
	// the default, runs the call in parallel with the turn's other calls. A connector tool
	// source sets it for connector writes.
	//
	// Since 0.6.16.
	Ordered bool
}

// AgentTurnOutcome is the result of one RunModelStep call: *TurnCompleted or *ToolCallsRequested.
type AgentTurnOutcome interface {
	agentTurnOutcome()
}

// TurnCompleted reports that the model call produced the turn's final answer, or ended the turn on
// purpose.
//
// The session stores NewMessages, records Usage, deletes the turn's tool call records and
// FrameworkRunState, and waits for the next user message, all in one commit. Set TurnError to end
// the turn as failed instead of completed:
//
//	return &dexagents.TurnCompleted{
//		NewMessages: messages,
//		Usage:       usage,
//		TurnError:   ptr.Any("the turn used more than 20 model calls"),
//	}, nil
type TurnCompleted struct {
	// NewMessages are the turn's items that the session has not stored yet, in order. For a
	// runner that never sets ToolCallsRequested.NewMessages, that is every item the turn produced
	// after its user message. The session appends them in one commit. Each Role must be valid; see
	// FrameworkMessage.Role.
	NewMessages []FrameworkMessage
	// Usage is the usage of this model call only.
	Usage AgentUsage
	// TurnError is error text that ends the turn as failed instead of completed, or nil to
	// complete the turn.
	//
	// The session still stores NewMessages and records Usage in the same commit. It then sets
	// AgentState.LastTurnError to this text and writes a turn_failed event instead of
	// turn_completed. It stores no turn-failure note, because NewMessages already say what the
	// turn did. No retry follows, so use it only for a deliberate stop that a retry would repeat,
	// such as a limit the runner enforces; return an error instead for a failure that a retry may
	// fix. The next turn that completes clears LastTurnError. Snapshots show the text, so keep
	// secrets out of it.
	//
	// A blank text is a runner defect: the turn fails at once without storing NewMessages, as for
	// an invalid Role; see AgentFrameworkRunner.RunModelStep. This is the Python core's turn_error and the TypeScript
	// core's turnError.
	//
	// Since 0.6.16.
	TurnError *string
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
	// Each Role must be valid; see FrameworkMessage.Role.
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
	// TruncatedOutputBytes is the size in bytes of the full output when Output is a truncated
	// copy, or nil when Output is whole. The session stores it as
	// AgentToolCall.TruncatedOutputBytes.
	//
	// Since 0.6.16.
	TruncatedOutputBytes *int64
	// RunnerToolState is opaque text the runner keeps with the call's result for its own later model
	// calls, such as the framework state that the tool changed, or nil to store nothing.
	//
	// The session stores it as AgentToolCall.RunnerToolState of the succeeded record, and RunModelStep
	// reads it from AgentTurnContext.ToolCalls. The session never interprets it and never shows it to
	// the model or in trace attributes. Only the attempt that commits the result stores it, so a retried
	// attempt never adds a second copy.
	//
	// Since 0.5.0.
	RunnerToolState *string
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
// On a later attempt of a call that is not a read, this error records
// outcome_unknown instead, with the same message. That attempt follows an
// earlier one that reached a Worker and ended without a result, such as a
// Worker that stopped while the tool ran, so the earlier attempt may have taken
// effect. This covers a refusal before the tool runs, such as an unknown tool
// after a deploy renamed it or arguments that a changed schema rejects. A call
// that a tool source built keeps failed, because the source classifies its own
// errors.
//
// Changed in 0.6.16: a later attempt of a call that is not a read records
// outcome_unknown, not failed.
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

// ToolOutcomeUnknownError reports that a tool call may or may not have taken effect, so it must not
// run again.
//
// Return it from ExecuteDeferredToolCall, directly or wrapped, when the tool
// sent a write and cannot tell whether it took effect, such as a connector
// operation's uncertain branch. The session records outcome_unknown with
// Message as the record's error and does not retry, and the model sees
// OutcomeUnknownToolResult. A later ordered write of the turn is skipped.
//
//	return nil, &dexagents.ToolOutcomeUnknownError{Message: "the provider timed out after receiving the refund"}
//
// Since 0.6.16.
type ToolOutcomeUnknownError struct {
	// Message is the record's error. The model sees OutcomeUnknownToolResult instead.
	Message string
}

// Error returns Message.
func (unknownError *ToolOutcomeUnknownError) Error() string {
	return unknownError.Message
}

// ToolCallStepOptions overrides the timeout and retry policy of one tool call's Step.
//
// A runner returns it from ToolCallStepOptionsProvider.ToolCallStepOptions.
// The session applies it to the movement that starts the call's tool Step,
// and to no other Step. A CallSingleAttemptTool call takes
// ExecuteMethodTimeout, and it runs one attempt unless
// BoundedResendExecuteRetry is set.
//
//	options := &dexagents.ToolCallStepOptions{
//		ExecuteMethodTimeout: 30 * time.Second,
//		ExecuteRetry:         &dex.RetryPolicy{MaximumAttempts: 3, TotalDuration: time.Hour},
//	}
//
// Since 0.6.16.
type ToolCallStepOptions struct {
	// ExecuteMethodTimeout is the timeout of each attempt, or zero for the tool Step's default.
	ExecuteMethodTimeout time.Duration
	// ExecuteRetry is the retry policy of a CallTool call, or nil for the default of three
	// attempts.
	ExecuteRetry *dex.RetryPolicy
	// BoundedResendExecuteRetry is the retry policy of a non_idempotent_write call whose tool
	// itself bounds how many times a repeat can send the write, such as a connector Mutation
	// whose RepeatOnUnknown is limited, or nil for one attempt.
	//
	// The call still runs in CallSingleAttemptTool, with these attempts instead of one. When
	// they run out, the call records outcome_unknown, as a single attempt does. Set it only for
	// a tool that enforces its own send cap on every attempt. The record's effect stays
	// non_idempotent_write, so a Worker without this option runs a later attempt of the call
	// once.
	BoundedResendExecuteRetry *dex.RetryPolicy
}

// ToolCallStepOptionsProvider gives a tool call's Step its own timeout and retry policy.
//
// A runner implements it when some tools need options other than the tool
// Steps' defaults, as builtinloop.BuiltinLoopAgentRunner does for a tool
// source that implements it. The session calls it whenever it starts a
// call's tool Step, including after an approval, so it must answer from the
// record alone. An error leaves the defaults and is logged.
//
// Since 0.6.16.
type ToolCallStepOptionsProvider interface {
	// ToolCallStepOptions returns the options of one call's tool Step, or nil for the defaults.
	ToolCallStepOptions(toolCall AgentToolCall) (*ToolCallStepOptions, error)
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
	// attempts; other calls get one. ToolCallStepOptions can change both.
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
	// StepContext is the tool Step's dex.Context, or nil when the caller has none, such as a unit
	// test.
	//
	// It gives the Step execution ID, the attempt, and the attempt deadline,
	// whatever context the runner passes to ExecuteDeferredToolCall, so a
	// runner may pass a derived context, such as one that a framework's tool
	// node or a tracing span adds values to. A ToolSource reads the Step from
	// here, never from the identity of its ctx: connectortools derives the
	// connector Call ID from its Step execution ID. Use it only during this
	// call, never store it, and record heartbeats only through Heartbeat and
	// SaveHeartbeatCheckpoint, because a bare heartbeat would drop the
	// checkpoint.
	//
	// Since 0.6.16.
	StepContext dex.Context
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
	//
	// An outcome that the session must not store is a runner defect, and the turn fails at once
	// in that RunModel attempt, because a retry would only repeat the paid model call. The
	// session records the outcome's Usage, stores nothing else from it, and ends the turn as
	// RecordTurnFailure does, with a LastTurnError that names the defect. The defects are
	// ToolCallsRequested.NewMessages from a runner that does not implement
	// UnfinishedTurnMessageBuilder (MissingUnfinishedTurnMessageBuilder), a message in
	// TurnCompleted.NewMessages or ToolCallsRequested.NewMessages whose Role is not user,
	// assistant, or tool, such as `TurnCompleted.NewMessages[0] has role "", which is not user,
	// assistant, or tool`, and a blank TurnCompleted.TurnError.
	//
	// Changed in 0.6.16: an invalid Role in NewMessages and a blank TurnCompleted.TurnError fail
	// the turn. Before, a message with an invalid Role was stored, and every later
	// get_agent_snapshot and list_agent_messages call failed to decode it.
	RunModelStep(ctx context.Context, turn AgentTurnContext) (AgentTurnOutcome, error)
	// ExecuteDeferredToolCall runs the real tool once. It returns *ToolExecutionResult, a
	// *ToolExecutionError for a failure the model sees, a *ToolOutcomeUnknownError for a write
	// that may have taken effect, or another error that fails the attempt. A *ToolExecutionError
	// records failed, or outcome_unknown on a later attempt of a call that is not a read; see
	// ToolExecutionError. ctx is the tool Step's dex.Context, and call.StepContext holds the same
	// Step, so the runner may pass a tool or ToolSource a context derived from ctx.
	//
	// Changed in 0.6.16: a *ToolOutcomeUnknownError records outcome_unknown, and
	// call.StepContext holds the tool Step's dex.Context.
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
