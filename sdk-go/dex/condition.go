// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Sustainable Use License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package dex

import (
	"fmt"
	"time"
)

// Wait describes when Dex may invoke a Step's Execute method.
//
// Build waits from durable Timer and Channel Conditions. Condition order determines timer indexes
// used by TimerID and HasTimerFiredByIndex.
//
// Example:
//
//	return dex.AnyOf(
//		approvals.ForOne(),
//		dex.Timer(5*time.Minute),
//	), nil
type Wait struct {
	kind         waitKind
	conditions   []Condition
	combinations []ConditionCombination
}

// SkipWaitImmediately asks Dex to invoke Execute without evaluating WaitFor conditions.
func SkipWaitImmediately() *Wait {
	return &Wait{kind: skipWaitImmediately}
}

// Until waits until one condition is satisfied. The Condition does not need an ID.
func Until(condition Condition) *Wait {
	return AllOf(condition)
}

// AllOf waits until every condition is satisfied. Conditions do not need IDs.
func AllOf(conditions ...Condition) *Wait {
	return &Wait{kind: waitAllOf, conditions: conditions}
}

// AnyOf waits until at least one condition is satisfied.
// Conditions do not need IDs.
// Among ready conditions, Dex selects the first in canonical order: Timers, Channels, SubFlows,
// then the graceful completion condition, preserving declaration order within each kind.
// An unready condition does not block
// a later ready condition. Return only the higher-priority condition to wait for strict priority.
// It consumes messages only from the selected Channel condition, not other ready alternatives.
func AnyOf(conditions ...Condition) *Wait {
	return &Wait{kind: waitAnyOf, conditions: conditions}
}

// AnyComboOf waits until every Condition in at least one combination is satisfied.
// It consumes messages only from Channel conditions in the selected combination.
// Every Condition requires a non-empty user-provided ID.
func AnyComboOf(combinations ...ConditionCombination) *Wait {
	return &Wait{kind: waitAnyComboOf, combinations: combinations}
}

// Condition represents one durable Timer, Channel, SubFlow, or graceful completion predicate.
// Create values through Timer, SubFlow, Channel, ChannelMap, or GracefulCompletionRequested;
// custom implementations are not supported.
type Condition interface {
	condition()
}

// Timer returns a Condition satisfied after duration of durable workflow time.
// The timer survives Worker restarts. Waiting does not occupy a Worker task slot.
// duration must be nonnegative; positive fractions round up to whole seconds.
// Invalid durations fail when Dex serializes the returned Wait, not when Timer constructs the Condition.
// WithConditionID supplies a stable ID for Client.SkipTimer; otherwise use the Timer's zero-based index.
// Execute can inspect Context.HasTimerFired or HasTimerFiredByIndex, including after a skipped Timer.
//
//	return dex.Until(dex.Timer(5*time.Minute, dex.WithConditionID("approval-deadline"))), nil
func Timer(duration time.Duration, options ...ConditionOption) Condition {
	condition := &conditionImpl{
		kind:     conditionTimer,
		duration: duration,
	}
	applyConditionOptions(condition, options)
	return condition
}

// SubFlow returns a Condition satisfied when target reaches a terminal state.
//
// target must be registered by the Worker and define a starting Step whose input accepts input.
// Dex generates the SubFlow Flow ID. Omit options to use the default abnormal-restart policy, or
// pass exactly one value to configure reuse, timing, Attributes, Flow configuration, and ID.
// WaitFor's successful result starts or attaches to the child; Execute reads SubFlowResult.
// AnyOf does not stop losing children. Use SubFlowID with Client.StopFlow to stop a still-running child.
// Invalid options or unencodable input fail serialization of the returned Wait.
// Child failure is represented by FlowResult.Status; inspect it before decoding outputs.
//
// Only a Step whose Step type contains no "-" can start SubFlows. The child Flow ID joins the
// parent Flow ID, Step execution ID, and SubFlow index with "-", so a dash makes it ambiguous.
// Default Step types are Go type names and never contain "-"; only a GetStepType override can.
// When such a Step returns a Wait with a SubFlow Condition, the Worker logs a warning and sends the
// WaitFor result unchanged. The server rejects it as an invalid Worker response and fails the
// attempt without retry, so the Flow fails under the default policy. Rename the Step type to fix it.
//
// Changed in 1.45.0: a Step whose Step type contains "-" can no longer start SubFlows.
//
//	return dex.Until(dex.SubFlow(ChargeFlow{}, chargeInput)), nil
func SubFlow(target Flow, input any, options ...SubFlowOptions) Condition {
	condition := &conditionImpl{
		kind:           conditionSubFlow,
		subFlow:        target,
		subFlowInput:   input,
		subFlowOptions: defaultSubFlowOptions(),
	}
	if len(options) > 1 {
		condition.err = fmt.Errorf("dex: SubFlow accepts at most one options value")
		return condition
	}
	if len(options) == 1 {
		condition.subFlowOptions = options[0]
	}
	condition.conditionID = condition.subFlowOptions.ConditionID
	condition.idSet = condition.conditionID != ""
	return condition
}

// GracefulCompletionRequested returns a Condition satisfied once the Flow has a graceful
// completion request.
//
// A graceful completion request comes from Client.StopFlow with GracefulCompleteFlow, or from
// a Step that returns GracefulComplete. The request stays in force, so a Wait registered after
// it is satisfied at once. When the request arrives, Dex re-checks only the Waits that include
// this Condition; other waiting Steps keep waiting and keep the Flow running.
//
// Use it to let a long-waiting Step end when someone asks the Flow to complete. A typical Step
// waits for the next message or this Condition with AnyOf. Its Execute first handles a message
// that the Wait consumed. When there is none and IsGracefulCompletionConditionCompleted is true,
// it returns DeadEnd. The Flow then completes once no other Step is active.
//
// In AnyOf, this Condition comes after Timers, Channels, and SubFlows, so a ready message still
// wins over the request. This Condition still reports completed when a message won and a request
// exists, so check the message first or it is lost. It also works in AllOf and in AnyComboOf, where it needs
// WithConditionID. A Wait may hold at most one graceful completion condition; a second one fails
// serialization of the returned Wait.
//
// It requires Server protocol version 7 or later. With an older Server, the Step's WaitFor fails
// with GracefulCompletionConditionUnsupportedError.
//
// Example:
//
//	func (waitForMessage) WaitFor(_ dex.Context, _ struct{}) (*dex.Wait, error) {
//		return dex.AnyOf(
//			UserMessages.ForOne(),
//			dex.GracefulCompletionRequested(),
//		), nil
//	}
//
//	func (waitForMessage) Execute(ctx dex.Context, _ struct{}) (*dex.StepDecision, error) {
//		messages, err := UserMessages.GetConditionResults(ctx)
//		if err != nil {
//			return nil, err
//		}
//		if len(messages) > 0 {
//			return dex.GoTo(handleMessage{}, messages[0]), nil
//		}
//		if dex.IsGracefulCompletionConditionCompleted(ctx) {
//			return dex.DeadEnd(), nil
//		}
//		return dex.GoTo(waitForMessage{}, struct{}{}), nil
//	}
//
// Since 1.45.6.
func GracefulCompletionRequested(options ...ConditionOption) Condition {
	condition := &conditionImpl{kind: conditionGracefulCompletion}
	applyConditionOptions(condition, options)
	return condition
}

// IsGracefulCompletionConditionCompleted reports whether the graceful completion condition of
// the current Execute invocation's Wait completed.
//
// It returns true when the Flow had a graceful completion request as the Wait completed, even
// when another AnyOf condition won, as with a Timer that fired. Read consumed Channel messages
// first, so a message that won is not dropped. It returns false when the Wait
// has no such condition, outside Execute, after SkipWaitImmediately, and for a Context that Dex
// did not create. See GracefulCompletionRequested for an example.
//
// Since 1.45.6.
func IsGracefulCompletionConditionCompleted(ctx Context) bool {
	invocation, ok := ctx.(*invocationContext)
	if !ok || invocation == nil {
		return false
	}
	return invocation.isGracefulCompletionConditionCompleted()
}

// SupportsGracefulCompletionCondition reports whether the connected Dex Server supports the
// GracefulCompletionRequested condition.
//
// Use it in WaitFor to add the condition only when the Server supports it. With an older Server,
// a Wait that contains the condition fails with GracefulCompletionConditionUnsupportedError after
// WaitFor returns, and Step code cannot catch that error. Check this function first and keep the
// Wait without the condition instead.
//
// It reads the Server protocol version that the Worker negotiated with GetServerInfo when it
// started. It never sends a request, so it is cheap to call on every invocation. It returns true
// when that version is 7 or later. It works in WaitFor, Execute, and RPC handlers alike. The Worker
// negotiates before it receives any task, so a Context that Dex created always sees the
// negotiated version. It returns false for a nil Context and for a Context that Dex did not
// create, such as a test fake.
//
// Example:
//
//	func (waitForMessage) WaitFor(ctx dex.Context, _ struct{}) (*dex.Wait, error) {
//		if dex.SupportsGracefulCompletionCondition(ctx) {
//			return dex.AnyOf(
//				UserMessages.ForOne(),
//				dex.GracefulCompletionRequested(),
//			), nil
//		}
//		return dex.AnyOf(UserMessages.ForOne()), nil
//	}
//
// It returns true when the Worker's negotiated Server protocol supports the graceful completion
// condition, and false otherwise.
//
// Since 1.45.6.
func SupportsGracefulCompletionCondition(ctx Context) bool {
	invocation, ok := ctx.(*invocationContext)
	if !ok || invocation == nil {
		return false
	}
	return invocation.serverProtocol.supportsGracefulCompletionCondition()
}

// SubFlowResult returns one SubFlow result from the current Execute invocation.
//
// Omit index to read the first SubFlow Condition. Indexes are zero-based in the stable SubFlow
// order within the surrounding Wait. AnyOf losers return a nonterminal running snapshot.
// Invalid contexts, multiple or negative indexes, missing results, and undecodable results return errors.
// Inspect Status before calling DecodeSingleOutput; a failed child is a result, not a SubFlowResult error.
func SubFlowResult(ctx Context, index ...int) (FlowResult, error) {
	invocation, ok := ctx.(*invocationContext)
	if !ok || invocation == nil {
		return FlowResult{}, fmt.Errorf("dex: SubFlow results require a Dex invocation Context")
	}
	resolvedIndex, err := resolveSubFlowIndex(index)
	if err != nil {
		return FlowResult{}, err
	}
	return invocation.subFlowResult(resolvedIndex)
}

// SubFlowID returns one server-generated SubFlow Flow ID from the current Execute invocation.
//
// Omit index for the first SubFlow. The ID remains addressable after AnyOf completes and can be
// passed to Client.StopFlow to stop a still-running loser.
func SubFlowID(ctx Context, index ...int) (string, error) {
	invocation, ok := ctx.(*invocationContext)
	if !ok || invocation == nil {
		return "", fmt.Errorf("dex: SubFlow IDs require a Dex invocation Context")
	}
	resolvedIndex, err := resolveSubFlowIndex(index)
	if err != nil {
		return "", err
	}
	if _, err := invocation.subFlowResult(resolvedIndex); err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"SubFlow:%s-%s-%d",
		invocation.flowID,
		invocation.stepExecutionID,
		resolvedIndex,
	), nil
}

func resolveSubFlowIndex(indexes []int) (int, error) {
	if len(indexes) > 1 {
		return 0, fmt.Errorf("dex: SubFlow accepts at most one result index")
	}
	if len(indexes) == 0 {
		return 0, nil
	}
	if indexes[0] < 0 {
		return 0, fmt.Errorf("dex: SubFlow result index must not be negative")
	}
	return indexes[0], nil
}

// ConditionOption configures a Condition. Use WithConditionID to create an option.
type ConditionOption interface {
	applyCondition(*conditionImpl)
}

// WithConditionID assigns a stable timer, Channel, or graceful completion Condition ID for later targeting.
func WithConditionID(conditionID string) ConditionOption {
	return conditionIDOption{conditionID: conditionID}
}

// ConditionCombination groups Conditions that must all succeed as one AnyComboOf branch.
type ConditionCombination struct {
	conditions []Condition
}

// Combo creates one branch satisfied only when all conditions are satisfied.
func Combo(conditions ...Condition) ConditionCombination {
	return ConditionCombination{conditions: conditions}
}

type waitKind uint8

const (
	skipWaitImmediately waitKind = iota + 1
	waitAllOf
	waitAnyOf
	waitAnyComboOf
)

type conditionKind uint8

const (
	conditionChannel conditionKind = iota + 1
	conditionTimer
	conditionSubFlow
	conditionGracefulCompletion
)

type conditionImpl struct {
	kind           conditionKind
	conditionID    string
	idSet          bool
	channelName    string
	instance       string
	isMap          bool
	atLeast        *int
	atMost         *int
	duration       time.Duration
	subFlow        Flow
	subFlowInput   any
	subFlowOptions SubFlowOptions
	err            error
}

func newChannelCondition(
	name string,
	instance string,
	isMap bool,
	atLeast *int,
	atMost *int,
	options []ConditionOption,
) Condition {
	condition := &conditionImpl{
		kind:        conditionChannel,
		channelName: name,
		instance:    instance,
		isMap:       isMap,
		atLeast:     atLeast,
		atMost:      atMost,
	}
	condition.err = validateChannelBounds(atLeast, atMost)
	applyConditionOptions(condition, options)
	return condition
}

func applyConditionOptions(
	condition *conditionImpl,
	options []ConditionOption,
) {
	for _, option := range options {
		option.applyCondition(condition)
	}
}

func validateChannelBounds(atLeast *int, atMost *int) error {
	if atLeast != nil && *atLeast < 0 {
		return fmt.Errorf("dex: at_least must be non-negative")
	}
	if atMost != nil && *atMost < 0 {
		return fmt.Errorf("dex: at_most must be non-negative")
	}
	if atLeast != nil && atMost != nil && *atMost < *atLeast {
		return fmt.Errorf("dex: at_most must not be below at_least")
	}
	return nil
}

func (*conditionImpl) condition() {}

type conditionIDOption struct {
	conditionID string
}

func (option conditionIDOption) applyCondition(condition *conditionImpl) {
	condition.conditionID = option.conditionID
	condition.idSet = true
}
