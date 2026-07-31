// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package dex

import (
	"fmt"
	"time"
)

type Wait struct {
	kind         waitKind
	conditions   []Condition
	combinations []ConditionCombination
}

func ExecuteImmediately() Wait {
	return Wait{kind: waitCompletedImmediately}
}

func AllOf(conditions ...Condition) Wait {
	return Wait{kind: waitAllOf, conditions: conditions}
}

func AnyOf(conditions ...Condition) Wait {
	return Wait{kind: waitAnyOf, conditions: conditions}
}

func AnyComboOf(combinations ...ConditionCombination) Wait {
	return Wait{kind: waitAnyComboOf, combinations: combinations}
}

type Condition interface {
	condition()
}

func Timer(duration time.Duration, options ...ConditionOption) Condition {
	condition := &conditionValue{
		kind:     conditionTimer,
		duration: duration,
	}
	applyConditionOptions(condition, options)
	return condition
}

type ConditionOption interface {
	conditionOption()
}

func WithConditionID(conditionID string) ConditionOption {
	return conditionIDOption{conditionID: conditionID}
}

type ConditionCombination struct {
	conditions []Condition
}

func Combo(conditions ...Condition) ConditionCombination {
	return ConditionCombination{conditions: conditions}
}

type waitKind uint8

const (
	waitCompletedImmediately waitKind = iota + 1
	waitAllOf
	waitAnyOf
	waitAnyComboOf
)

type conditionKind uint8

const (
	conditionChannel conditionKind = iota + 1
	conditionTimer
)

type conditionValue struct {
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
	condition := &conditionValue{
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
	condition *conditionValue,
	options []ConditionOption,
) {
	for _, option := range options {
		switch value := option.(type) {
		case conditionIDOption:
			condition.conditionID = value.conditionID
			condition.idSet = true
		}
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

func (*conditionValue) condition() {}

type conditionIDOption struct {
	conditionID string
}

func (conditionIDOption) conditionOption() {}
