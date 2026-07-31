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
	return StepDef{
		handler:  typedStepHandler[IN]{step: step},
		starting: starting,
	}
}

// StepDef is the representation of Step, without Go's generic
// So that internal sdk can use it to workaround Go's generic limitations
type StepDef struct {
	handler  stepHandler
	starting bool
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

type stepReference interface {
	stepType() string
	stepInputType() reflect.Type
	stepOptions() *StepOptions
	stepValue() any
}

type stepHandler interface {
	stepReference
	skipWaitFor() bool
	waitFor(Context, any) (Wait, error)
	execute(Context, any) (StepDecision, error)
}

type typedStepHandler[IN any] struct {
	step Step[IN]
}

func (handler typedStepHandler[IN]) stepType() string {
	return handler.step.GetStepType()
}

func (typedStepHandler[IN]) stepInputType() reflect.Type {
	return reflect.TypeFor[IN]()
}

func (handler typedStepHandler[IN]) stepOptions() *StepOptions {
	return handler.step.GetStepOptions()
}

func (handler typedStepHandler[IN]) stepValue() any {
	return handler.step
}

func (handler typedStepHandler[IN]) skipWaitFor() bool {
	_, skip := any(handler.step).(noWaitForStep)
	return skip
}

func (handler typedStepHandler[IN]) waitFor(
	ctx Context,
	input any,
) (Wait, error) {
	typedInput, err := stepInput[IN](input)
	if err != nil {
		return Wait{}, err
	}
	return handler.step.WaitFor(ctx, typedInput)
}

func (handler typedStepHandler[IN]) execute(
	ctx Context,
	input any,
) (StepDecision, error) {
	typedInput, err := stepInput[IN](input)
	if err != nil {
		return StepDecision{}, err
	}
	return handler.step.Execute(ctx, typedInput)
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

func validStepReference(reference stepReference) bool {
	if reference == nil {
		return false
	}
	value := reflect.ValueOf(reference.stepValue())
	return value.IsValid() &&
		!isNilValue(value) &&
		reference.stepType() != ""
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
