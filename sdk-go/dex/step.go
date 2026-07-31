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
	"reflect"
)

type Step[IN any] interface {
	GetStepType() string
	GetStepOptions() *StepOptions
	WaitFor(ctx Context, input IN) (Wait, error)
	Execute(ctx Context, input IN) (StepDecision, error)
}

func DefineStep[IN any](step Step[IN]) StepDef {
	return newStepDef(step, false)
}

func DefineStepAsStart[IN any](step Step[IN]) StepDef {
	return newStepDef(step, true)
}

func newStepDef[IN any](step Step[IN], starting bool) StepDef {
	return typedStepDef[IN]{step: step, starting: starting}
}

// StepDef is the representation of Step, without Go's generic
// So that internal sdk can use it to workaround Go's generic limitations
type StepDef interface {
	stepType() string
	stepInputType() reflect.Type
	stepOptions() *StepOptions
	stepValue() any
	isStarting() bool
	skipWaitFor() bool
	waitFor(Context, any) (Wait, error)
	execute(Context, any) (StepDecision, error)
}

type NoWaitFor[IN any] struct{}

type DefaultStepOptions struct{}

type StepDefaults[IN any] struct {
	DefaultStepOptions
	NoWaitFor[IN]
}

func (NoWaitFor[IN]) WaitFor(Context, IN) (Wait, error) {
	panic("NoWaitFor: framework must skip WaitFor")
}

func (NoWaitFor[IN]) noWaitFor() {}

func (DefaultStepOptions) GetStepOptions() *StepOptions {
	return nil
}

// typedStepDef is the only concrete StepDef. Each application step is a
// Step[IN] with its own input type, but GetSteps, registration, movements,
// and execute-failure options need one non-generic type so differently typed
// steps can live in the same slice and maps. Go cannot express that as
// []Step[IN] with mixed IN, so DefineStep / DefineStepAsStart / MovementOf
// wrap each Step[IN] in typedStepDef[IN]. The wrapper implements StepDef with
// any-typed waitFor/execute, then casts the input back to IN before calling
// the real Step methods—keeping compile-time typing at the authoring API
// while letting the internal SDK treat all steps uniformly.
type typedStepDef[IN any] struct {
	step     Step[IN]
	starting bool
}

func (def typedStepDef[IN]) stepType() string {
	return def.step.GetStepType()
}

func (typedStepDef[IN]) stepInputType() reflect.Type {
	return reflect.TypeFor[IN]()
}

func (def typedStepDef[IN]) stepOptions() *StepOptions {
	return def.step.GetStepOptions()
}

func (def typedStepDef[IN]) stepValue() any {
	return def.step
}

func (def typedStepDef[IN]) isStarting() bool {
	return def.starting
}

func (def typedStepDef[IN]) skipWaitFor() bool {
	_, skip := any(def.step).(noWaitForStep)
	return skip
}

func (def typedStepDef[IN]) waitFor(
	ctx Context,
	input any,
) (Wait, error) {
	typedInput, err := stepInput[IN](input)
	if err != nil {
		return Wait{}, err
	}
	return def.step.WaitFor(ctx, typedInput)
}

func (def typedStepDef[IN]) execute(
	ctx Context,
	input any,
) (StepDecision, error) {
	typedInput, err := stepInput[IN](input)
	if err != nil {
		return StepDecision{}, err
	}
	return def.step.Execute(ctx, typedInput)
}

type noWaitForStep interface {
	noWaitFor()
}

func stepInput[IN any](input any) (IN, error) {
	var zero IN
	if input == nil {
		inputType := reflect.TypeFor[IN]()
		if isNilableType(inputType) {
			return zero, nil
		}
		return zero, fmt.Errorf(
			"dex: nil input is not assignable to step input %s",
			inputType,
		)
	}
	typedInput, ok := input.(IN)
	if !ok {
		return zero, fmt.Errorf(
			"dex: input type %T is not assignable to step input %s",
			input,
			reflect.TypeFor[IN](),
		)
	}
	return typedInput, nil
}

func validStepDef(definition StepDef) bool {
	if definition == nil {
		return false
	}
	value := reflect.ValueOf(definition.stepValue())
	return value.IsValid() &&
		!isNilValue(value) &&
		definition.stepType() != ""
}

func isNilableType(valueType reflect.Type) bool {
	if valueType == nil {
		return true
	}
	switch valueType.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map,
		reflect.Pointer, reflect.Slice:
		return true
	default:
		return false
	}
}
