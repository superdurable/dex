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

type StepMovement struct {
	step    any
	input   any
	options *StepOptions
}

func GoTo[IN any](
	step Step[IN],
	input IN,
	options ...StepMoveOption,
) StepDecision {
	return GoToMulti(MovementOf(step, input, options...))
}

func MovementOf[IN any](
	step Step[IN],
	input IN,
	options ...StepMoveOption,
) StepMovement {
	movement := StepMovement{step: step, input: input}
	for _, option := range options {
		option.applyStepMovement(&movement)
	}
	return movement
}

func GoToMulti(movements ...StepMovement) StepDecision {
	return StepDecision{
		kind:      decisionNext,
		movements: movements,
	}
}

func GracefulComplete(output any) StepDecision {
	return StepDecision{
		kind: decisionClose,
		close: CloseDecision{
			kind:   closeGracefulComplete,
			output: output,
		},
	}
}

func ForceComplete(output any) StepDecision {
	return StepDecision{
		kind: decisionClose,
		close: CloseDecision{
			kind:   closeForceComplete,
			output: output,
		},
	}
}

func ForceFail(reason string) StepDecision {
	return StepDecision{
		kind: decisionClose,
		close: CloseDecision{
			kind:   closeForceFail,
			reason: reason,
		},
	}
}

func DeadEnd() StepDecision {
	return StepDecision{
		kind:  decisionClose,
		close: CloseDecision{kind: closeDeadEnd},
	}
}

func ForceCompleteOnChannelsEmpty(
	output any,
	channels []ChannelDef,
	otherwise ...StepMovement,
) StepDecision {
	return StepDecision{
		kind:      decisionConditionalClose,
		movements: otherwise,
		close: CloseDecision{
			kind:     closeConditionalForceComplete,
			output:   output,
			channels: channels,
		},
	}
}

type StepDecision struct {
	kind      decisionKind
	movements []StepMovement
	close     CloseDecision
}

type CloseDecision struct {
	kind     closeKind
	output   any
	reason   string
	channels []ChannelDef
}

type StepMoveOption interface {
	applyStepMovement(*StepMovement)
}

func WithStepOptions(options *StepOptions) StepMoveOption {
	return stepOptionsMoveOption{options: options}
}

type stepOptionsMoveOption struct {
	options *StepOptions
}

func (option stepOptionsMoveOption) applyStepMovement(movement *StepMovement) {
	movement.options = option.options
}

type decisionKind uint8

const (
	decisionNext decisionKind = iota + 1
	decisionClose
	decisionConditionalClose
)

type closeKind uint8

const (
	closeGracefulComplete closeKind = iota + 1
	closeForceComplete
	closeForceFail
	closeDeadEnd
	closeConditionalForceComplete
)
