package dex

import (
	"errors"
	"fmt"
	"reflect"
)

// Schema validation — errors.Is(err, ErrUndeclaredStateKey) or ErrUndeclaredChannel.
var (
	ErrUndeclaredStateKey = errors.New("dex: undeclared state key")
	ErrUndeclaredChannel  = errors.New("dex: undeclared channel")
)

// UndeclaredStateKeyError means the key was not registered in GetPersistenceSchema.
type UndeclaredStateKeyError struct {
	Key string
}

func (err *UndeclaredStateKeyError) Error() string {
	return fmt.Sprintf("dex: undeclared state key %q (add to GetPersistenceSchema)", err.Key)
}

func (err *UndeclaredStateKeyError) Is(target error) bool {
	return target == ErrUndeclaredStateKey
}

// UndeclaredChannelError means the channel was not registered in GetPersistenceSchema.
type UndeclaredChannelError struct {
	ChannelName string
}

func (err *UndeclaredChannelError) Error() string {
	return fmt.Sprintf("dex: undeclared channel %q (add to GetPersistenceSchema)", err.ChannelName)
}

func (err *UndeclaredChannelError) Is(target error) bool {
	return target == ErrUndeclaredChannel
}

// Client / registry — errors.Is(err, ErrFlowNotRegistered), ErrRunNotFound, etc.
var (
	ErrFlowNotRegistered         = errors.New("dex: flow type not registered")
	ErrStepNotFound              = errors.New("dex: step not found in flow")
	ErrRunNotFound               = errors.New("dex: run not found")
	ErrNoStartingSteps           = errors.New("dex: flow has no starting steps")
	ErrInputCountMismatch        = errors.New("dex: StartRun input count mismatch")
	ErrStartingStepInputMismatch = errors.New("dex: starting step input type mismatch")
	ErrInvalidContext            = errors.New("dex: invalid Context implementation")
)

// FlowNotRegisteredError is returned when StartRun or the worker cannot resolve a flow type.
type FlowNotRegisteredError struct {
	FlowType string
}

func (err *FlowNotRegisteredError) Error() string {
	return fmt.Sprintf("dex: flow type %q not registered", err.FlowType)
}

func (err *FlowNotRegisteredError) Is(target error) bool {
	return target == ErrFlowNotRegistered
}

// StepNotFoundError is returned when a step ID is missing from a registered flow.
type StepNotFoundError struct {
	StepID   string
	FlowType string
}

func (err *StepNotFoundError) Error() string {
	return fmt.Sprintf("dex: step %q not found in flow %q", err.StepID, err.FlowType)
}

func (err *StepNotFoundError) Is(target error) bool {
	return target == ErrStepNotFound
}

// RunNotFoundError is returned when GetRun finds no run for the given ID.
type RunNotFoundError struct {
	RunID string
}

func (err *RunNotFoundError) Error() string {
	return fmt.Sprintf("dex: run %q not found", err.RunID)
}

func (err *RunNotFoundError) Is(target error) bool {
	return target == ErrRunNotFound
}

// InputCountMismatchError is returned when StartRun inputs length is not 0 or N.
type InputCountMismatchError struct {
	InputCount        int
	StartingStepCount int
}

func (err *InputCountMismatchError) Error() string {
	return fmt.Sprintf(
		"dex: inputs length %d does not match starting steps count %d (must be 0 for nil-per-step, or exactly %d for per-step inputs)",
		err.InputCount, err.StartingStepCount, err.StartingStepCount,
	)
}

func (err *InputCountMismatchError) Is(target error) bool {
	return target == ErrInputCountMismatch
}

// StartingStepInputMismatchError is returned when a per-step input type does not match.
type StartingStepInputMismatchError struct {
	StepID       string
	Index        int
	ExpectedType reflect.Type
	GotType      reflect.Type
}

func (err *StartingStepInputMismatchError) Error() string {
	return fmt.Sprintf(
		"dex: input type %s for starting step %q (index %d) does not match step input type %s",
		err.GotType, err.StepID, err.Index, err.ExpectedType,
	)
}

func (err *StartingStepInputMismatchError) Is(target error) bool {
	return target == ErrStartingStepInputMismatch
}

// InvalidContextError is returned when Context is not a dex step context.
type InvalidContextError struct {
	Got any
}

func (err *InvalidContextError) Error() string {
	return fmt.Sprintf("dex: invalid Context implementation %T", err.Got)
}

func (err *InvalidContextError) Is(target error) bool {
	return target == ErrInvalidContext
}

func newUndeclaredStateKeyError(key string) error {
	return &UndeclaredStateKeyError{Key: key}
}

func newUndeclaredChannelError(channelName string) error {
	return &UndeclaredChannelError{ChannelName: channelName}
}

func newFlowNotRegisteredError(flowType string) error {
	return &FlowNotRegisteredError{FlowType: flowType}
}

func newStepNotFoundError(stepID, flowType string) error {
	return &StepNotFoundError{StepID: stepID, FlowType: flowType}
}

func newRunNotFoundError(runID string) error {
	return &RunNotFoundError{RunID: runID}
}

func newInputCountMismatchError(inputCount, startingStepCount int) error {
	return &InputCountMismatchError{
		InputCount:        inputCount,
		StartingStepCount: startingStepCount,
	}
}

func newStartingStepInputMismatchError(stepID string, index int, expected, got reflect.Type) error {
	return &StartingStepInputMismatchError{
		StepID:       stepID,
		Index:        index,
		ExpectedType: expected,
		GotType:      got,
	}
}

func newInvalidContextError(got any) error {
	return &InvalidContextError{Got: got}
}

// runNonProcessableError marks a failure this worker cannot retry.
// The worker exits the run so another worker can pick up after heartbeat.
type runNonProcessableError struct {
	cause error
}

func newRunNonProcessableError(format string, args ...any) error {
	return runNonProcessableError{cause: fmt.Errorf(format, args...)}
}

func wrapRunNonProcessableError(err error, msg string) error {
	return runNonProcessableError{cause: fmt.Errorf("%s: %w", msg, err)}
}

func (err runNonProcessableError) Error() string { return err.cause.Error() }

func (err runNonProcessableError) Unwrap() error { return err.cause }

func isRunNonProcessableError(err error) bool {
	var nonProcessable runNonProcessableError
	return errors.As(err, &nonProcessable)
}
