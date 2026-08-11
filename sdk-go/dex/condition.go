// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
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
//		approvals.ForOne(dex.WithConditionID("approved")),
//		dex.Timer(5*time.Minute, dex.WithConditionID("timeout")),
//	), nil
type Wait struct {
	kind         waitKind
	conditions   []Condition
	combinations []ConditionCombination
	transient    *StepMovement
}

// SkipWaitImmediately asks Dex to invoke Execute without evaluating WaitFor conditions.
func SkipWaitImmediately() *Wait {
	return &Wait{kind: skipWaitImmediately}
}

// Until waits until one condition is satisfied.
func Until(condition Condition) *Wait {
	return AllOf(condition)
}

// AllOf waits until every condition is satisfied.
func AllOf(conditions ...Condition) *Wait {
	return &Wait{kind: waitAllOf, conditions: conditions}
}

// AnyOf waits until at least one condition is satisfied.
func AnyOf(conditions ...Condition) *Wait {
	return &Wait{kind: waitAnyOf, conditions: conditions}
}

// AnyComboOf waits until every Condition in at least one combination is satisfied.
// Every Condition requires a non-empty user-provided ID.
func AnyComboOf(combinations ...ConditionCombination) *Wait {
	return &Wait{kind: waitAnyComboOf, combinations: combinations}
}

func withTransientMovement(wait *Wait, movement StepMovement) *Wait {
	wait.transient = &movement
	return wait
}

// Condition represents one durable Timer or Channel predicate.
// Create values through Timer, Channel, or ChannelMap; custom implementations are not supported.
type Condition interface {
	condition()
}

// Timer returns a Condition satisfied after duration of durable workflow time.
// The timer survives Worker restarts.
func Timer(duration time.Duration, options ...ConditionOption) Condition {
	condition := &conditionImpl{
		kind:     conditionTimer,
		duration: duration,
	}
	applyConditionOptions(condition, options)
	return condition
}

// ConditionOption configures a Condition. Use WithConditionID to create an option.
type ConditionOption interface {
	applyCondition(*conditionImpl)
}

// WithConditionID assigns a stable timer or Channel Condition ID for later targeting.
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
)

type conditionImpl struct {
	kind        conditionKind
	conditionID string
	idSet       bool
	channelName string
	instance    string
	isMap       bool
	atLeast     *int
	atMost      *int
	duration    time.Duration
	err         error
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
