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
	"reflect"
)

type none struct{}

// None is the nil-only SDK payload, rejecting arbitrary values unlike any and clarifying struct{} intent.
type None = *none

// Step is one node in a Flow's durable state machine. IN is the typed input
// passed into WaitFor and Execute for this step.
//
// A waiting step embeds StepDefaults and implements WaitFor.
// An execute-only step embeds StepDefaultsNoWaitFor[IN] (or NoWaitFor[IN]) so
// registration sets skip_wait_for and never invokes WaitFor.
//
// Example (waiting step):
//
//	type ApproveOrderStep struct {
//		dex.StepDefaults
//	}
//
//	func (ApproveOrderStep) WaitFor(
//		ctx dex.Context,
//		input ApproveOrderInput,
//	) (dex.Wait, error) {
//		return dex.AnyOf(
//			ApprovalChannel.ForOne(),
//			dex.Timer(input.Timeout, dex.WithConditionID("approval-timeout")),
//		), nil
//	}
//
//	func (ApproveOrderStep) Execute(
//		ctx dex.Context,
//		input ApproveOrderInput,
//	) (dex.StepDecision, error) {
//		if ctx.HasTimerFired() {
//			return dex.ForceFail("approval timed out"), nil
//		}
//		return dex.GoTo(ShipOrder, ShipOrderInput{OrderID: input.OrderID}), nil
//	}
//
//	var ApproveOrder = ApproveOrderStep{}
//
// Example (execute-only step):
//
//	type ShipOrderStep struct {
//		dex.StepDefaultsNoWaitFor[ShipOrderInput]
//	}
//
//	func (ShipOrderStep) Execute(
//		ctx dex.Context,
//		input ShipOrderInput,
//	) (dex.StepDecision, error) {
//		return dex.DeadEnd(), nil
//	}
//
//	var ShipOrder = ShipOrderStep{}
type Step[IN any] interface {
	// GetStepType overrides the default package-qualified Go type name.
	// Embed DefaultStepType to use the default.
	GetStepType() string

	// GetStepOptions returns immutable defaults applied whenever this step is
	// scheduled. Return nil (e.g. by embedding StepDefaults) to use server
	// defaults. A movement may override individual fields when transitioning.
	GetStepOptions() *StepOptions

	// WaitFor sets up the conditions this step waits on before Execute runs.
	//
	//	ctx    invocation context (flow id, run id, step execution id, etc.)
	//	input  typed step input for this execution
	//
	// Return a Wait built from Channel.ForOne / ForAll, Timer, AnyOf / AllOf, or
	// SkipWaitImmediately. Attribute Set, RecordEvent, Publish, and
	// SetStepExecutionLocal are allowed here; writes are accepted with the
	// WaitFor response.
	//
	// Optional for execute-only steps: embed NoWaitFor[IN] or
	// StepDefaultsNoWaitFor[IN] instead of implementing a real WaitFor.
	// Registration detects the marker and skips this method; the panic body must
	// never run.
	WaitFor(ctx Context, input IN) (Wait, error)

	// Execute decides what happens next after WaitFor conditions complete, or
	// immediately when the step is execute-only (NoWaitFor / StepDefaultsNoWaitFor).
	//
	//	ctx    invocation context; read condition results, timers, and
	//	       step-execution locals set in WaitFor
	//	input  the same typed step input passed to WaitFor
	//
	// Return a non-empty StepDecision (GoTo, GoToMulti, DeadEnd, GracefulComplete,
	// ForceComplete, ForceFail, or a conditional close). Attribute / channel /
	// event writes are allowed; they are accepted with the Execute response.
	Execute(ctx Context, input IN) (StepDecision, error)
}

// DefineStep wraps a non-starting Step for Flow.GetSteps.
func DefineStep[IN any](step Step[IN]) StepDef {
	return newStepDef(step, false)
}

// DefineStartStep wraps the Flow's starting Step for Flow.GetSteps.
// A Flow may declare at most one starting step. With none, the run starts with
// no step execution (RPCs can still start steps later).
func DefineStartStep[IN any](step Step[IN]) StepDef {
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

// NoWaitFor marks a Step as execute-only. Embed it (or StepDefaultsNoWaitFor) so
// registration sets skip_wait_for and never calls WaitFor. The WaitFor method
// exists only to satisfy Step[IN] and panics if invoked.
//
// Example:
//
//	type ShipOrderStep struct {
//		dex.NoWaitFor[ShipOrderInput]
//		dex.DefaultStepOptions
//	}
type NoWaitFor[IN any] struct{}

// DefaultStepType uses the package-qualified Go type as the durable step type.
type DefaultStepType struct{}

// DefaultStepOptions embeds GetStepOptions that returns nil (server defaults).
//
// Example:
//
//	type ApproveOrderStep struct {
//		dex.DefaultStepOptions
//	}
type DefaultStepOptions struct {
	DefaultStepType
}

// StepDefaults supplies default type and options while requiring WaitFor.
//
// Example:
//
//	type ApproveOrderStep struct {
//		dex.StepDefaults
//	}
type StepDefaults struct {
	DefaultStepOptions
}

// StepDefaultsNoWaitFor supplies defaults for execute-only steps.
//
// Example:
//
//	type ShipOrderStep struct {
//		dex.StepDefaultsNoWaitFor[ShipOrderInput]
//	}
type StepDefaultsNoWaitFor[IN any] struct {
	StepDefaults
	NoWaitFor[IN]
}

func (NoWaitFor[IN]) WaitFor(Context, IN) (Wait, error) {
	panic("NoWaitFor: framework must skip WaitFor")
}

func (NoWaitFor[IN]) noWaitFor() {}

func (DefaultStepOptions) GetStepOptions() *StepOptions {
	return nil
}

func (DefaultStepType) GetStepType() string {
	return ""
}

// typedStepDef is the only concrete StepDef. Each application step is a
// Step[IN] with its own input type, but GetSteps, registration, movements,
// and execute-failure options need one non-generic type so differently typed
// steps can live in the same slice and maps. Go cannot express that as
// []Step[IN] with mixed IN, so DefineStep / DefineStartStep / MovementOf
// wrap each Step[IN] in typedStepDef[IN]. The wrapper implements StepDef with
// any-typed waitFor/execute, then casts the input back to IN before calling
// the real Step methods—keeping compile-time typing at the authoring API
// while letting the internal SDK treat all steps uniformly.
type typedStepDef[IN any] struct {
	step     Step[IN]
	starting bool
}

func (def typedStepDef[IN]) stepType() string {
	return GetFinalStepType(def.step)
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
