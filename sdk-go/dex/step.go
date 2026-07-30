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

type Step[IN any] interface {
	GetStepType() string
	GetStepOptions() *StepOptions
	WaitFor(ctx Context, input IN) (Wait, error)
	Execute(ctx Context, input IN) (StepDecision, error)
}

type StepDef struct {
	step     any
	starting bool
}

func DefineStep[IN any](step Step[IN]) StepDef {
	return StepDef{step: step}
}

func DefineStepAsStart[IN any](step Step[IN]) StepDef {
	return StepDef{step: step, starting: true}
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
