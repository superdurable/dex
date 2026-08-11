// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package dex

// StepMovement targets a Step with typed input and optional per-movement StepOptions.
// Create movements with MovementOf when building multi-target or conditional decisions.
type StepMovement struct {
	step    StepDef
	input   any
	options *StepOptions
}

// GoTo returns a decision that moves to one typed target Step.
func GoTo[IN any](
	step Step[IN],
	input IN,
	options ...StepMoveOption,
) *StepDecision {
	return GoToMulti(MovementOf(step, input, options...))
}

// MovementOf creates a typed Step movement for GoToMulti, RPCResult, or conditional completion.
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

// GoToMulti moves to every supplied target, enabling concurrent active Steps.
func GoToMulti(movements ...StepMovement) *StepDecision {
	return &StepDecision{
		kind:      decisionNext,
		movements: movements,
	}
}

// GracefulComplete completes after all other active Steps finish and returns output.
func GracefulComplete(output any) *StepDecision {
	return &StepDecision{
		kind: decisionClose,
		close: CloseDecision{
			kind:   closeGracefulComplete,
			output: output,
		},
	}
}

// ForceComplete completes immediately, abandons other active Steps, and returns output.
func ForceComplete(output any) *StepDecision {
	return &StepDecision{
		kind: decisionClose,
		close: CloseDecision{
			kind:   closeForceComplete,
			output: output,
		},
	}
}

// ForceFail ends the Flow immediately with an application-provided reason.
func ForceFail(reason string) *StepDecision {
	return &StepDecision{
		kind: decisionClose,
		close: CloseDecision{
			kind:   closeForceFail,
			reason: reason,
		},
	}
}

// DeadEnd leaves the Flow running with no next Step.
// Use it only when an RPC or external operation will resume the Flow later.
func DeadEnd() *StepDecision {
	return &StepDecision{
		kind:  decisionClose,
		close: CloseDecision{kind: closeDeadEnd},
	}
}

// ForceCompleteOnChannelsEmpty completes when every guarded Channel is empty.
// Otherwise Dex follows the optional movements supplied through otherwise.
func ForceCompleteOnChannelsEmpty(
	output any,
	channels []ChannelDef,
	otherwise ...StepMovement,
) *StepDecision {
	return &StepDecision{
		kind:      decisionConditionalClose,
		movements: otherwise,
		close: CloseDecision{
			kind:     closeConditionalForceComplete,
			output:   output,
			channels: channels,
		},
	}
}

// StepDecision describes the durable outcome of one successful Step Execute call.
type StepDecision struct {
	kind      decisionKind
	movements []StepMovement
	close     CloseDecision
}

// CloseDecision stores the terminal output, failure reason, and guarded Channels.
// Applications create it through decision factories instead of constructing it directly.
type CloseDecision struct {
	kind     closeKind
	output   any
	reason   string
	channels []ChannelDef
}

// StepMoveOption configures one Step movement. The interface is sealed to SDK implementations.
type StepMoveOption interface {
	applyStepMovement(*StepMovement)
}

// WithStepOptions overrides the target Step's registered options for one movement.
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
