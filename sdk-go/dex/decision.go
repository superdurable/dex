// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package dex

type StepMovement struct {
	step    StepDef
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
	movement := StepMovement{
		step:  typedStepDef[IN]{step: step},
		input: input,
	}
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
