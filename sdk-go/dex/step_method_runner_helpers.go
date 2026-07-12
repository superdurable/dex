package dex

import (
	"fmt"
	"reflect"

	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
)

func executeStepReflect(
	codec ObjectCodec,
	step stepCommon,
	parent Context,
	inputVal *pb.Value,
	stateMap map[string]*pb.Value,
	schema PersistenceSchema,
	results *conditionResults,
) (StepDecision, *persistenceImpl, error) {
	rv := reflect.ValueOf(step)
	method := rv.MethodByName("Execute")
	if !method.IsValid() {
		return nil, nil, newRunNonProcessableError("step %T has no Execute method", step)
	}

	methodType := method.Type()
	if methodType.NumIn() != 2 || methodType.NumOut() != 2 {
		return nil, nil, newRunNonProcessableError("step %T.Execute has wrong signature (want 2 in, 2 out)", step)
	}

	inputType := methodType.In(1)
	inputArg, err := decodeToType(codec, inputVal, inputType)
	if err != nil {
		return nil, nil, wrapRunNonProcessableError(err, "decode step input")
	}

	stepCtx := newStepContext(
		parent,
		parent.RunID(),
		parent.StepExecutionID(),
		parent.FromStepExecutionID(),
		parent.GetShutdownChannel(),
		stateMap,
		schema,
		results,
		codec,
	)
	callResults := method.Call([]reflect.Value{
		reflect.ValueOf(stepCtx),
		inputArg,
	})

	var retErr error
	if !callResults[1].IsNil() {
		retErr = callResults[1].Interface().(error)
	}
	if retErr != nil {
		return nil, stepCtx.persistence, retErr
	}
	if callResults[0].IsNil() {
		return nil, stepCtx.persistence, newRunNonProcessableError("step %T.Execute returned nil StepDecision", step)
	}

	decision, ok := callResults[0].Interface().(StepDecision)
	if !ok {
		return nil, stepCtx.persistence, newRunNonProcessableError("step %T.Execute returned non-StepDecision", step)
	}
	return decision, stepCtx.persistence, nil
}

func executeWaitForReflect(
	codec ObjectCodec,
	step stepCommon,
	parent Context,
	inputVal *pb.Value,
	stateMap map[string]*pb.Value,
	schema PersistenceSchema,
) (WaitForCondition, *persistenceImpl, error) {
	rv := reflect.ValueOf(step)
	method := rv.MethodByName("WaitFor")
	if !method.IsValid() {
		return nil, nil, newRunNonProcessableError("step %T has no WaitFor method", step)
	}

	methodType := method.Type()
	if methodType.NumIn() != 2 || methodType.NumOut() != 2 {
		return nil, nil, newRunNonProcessableError("step %T.WaitFor has wrong signature (want 2 in, 2 out)", step)
	}

	inputType := methodType.In(1)
	inputArg, err := decodeToType(codec, inputVal, inputType)
	if err != nil {
		return nil, nil, wrapRunNonProcessableError(err, "decode step input")
	}

	stepCtx := newStepContext(
		parent,
		parent.RunID(),
		parent.StepExecutionID(),
		parent.FromStepExecutionID(),
		parent.GetShutdownChannel(),
		stateMap,
		schema,
		&emptyConditionResults,
		codec,
	)
	callResults := method.Call([]reflect.Value{
		reflect.ValueOf(stepCtx),
		inputArg,
	})

	var retErr error
	if !callResults[1].IsNil() {
		retErr = callResults[1].Interface().(error)
	}
	if retErr != nil {
		return nil, stepCtx.persistence, retErr
	}
	if callResults[0].IsNil() {
		return nil, stepCtx.persistence, newRunNonProcessableError("step %T.WaitFor returned nil WaitForCondition", step)
	}

	condition, ok := callResults[0].Interface().(WaitForCondition)
	if !ok {
		return nil, stepCtx.persistence, newRunNonProcessableError("step %T.WaitFor returned non-WaitForCondition", step)
	}
	if err := stepCtx.persistence.validateWaitCondition(condition); err != nil {
		return nil, stepCtx.persistence, wrapRunNonProcessableError(err, "validate wait condition")
	}
	return condition, stepCtx.persistence, nil
}

func decodeToType(codec ObjectCodec, val *pb.Value, targetType reflect.Type) (reflect.Value, error) {
	if val == nil || val.Kind == nil {
		return reflect.Zero(targetType), nil
	}
	if _, ok := val.Kind.(*pb.Value_NullValue); ok {
		return reflect.Zero(targetType), nil
	}

	target := reflect.New(targetType)
	if err := codec.DecodeValue(val, target.Interface()); err != nil {
		return reflect.Value{}, err
	}
	return target.Elem(), nil
}

// stepInputType returns the IN type from step.Execute (or WaitFor as fallback).
func stepInputType(step stepCommon) (reflect.Type, error) {
	return stepInputReflectType(step)
}

// stepInputReflectType reads IN from Execute, else WaitFor.
func stepInputReflectType(step stepCommon) (reflect.Type, error) {
	method := reflect.ValueOf(step).MethodByName("Execute")
	if !method.IsValid() {
		method = reflect.ValueOf(step).MethodByName("WaitFor")
	}
	if !method.IsValid() {
		return nil, fmt.Errorf("step %T has no Execute or WaitFor", step)
	}
	methodType := method.Type()
	if methodType.NumIn() < 2 {
		return nil, fmt.Errorf("step %T method has wrong signature", step)
	}
	return methodType.In(1), nil
}

func validateStartingStepInput(stepID string, index int, input any, expected reflect.Type) error {
	if input == nil {
		return nil
	}
	got := reflect.TypeOf(input)
	if !got.AssignableTo(expected) {
		return newStartingStepInputMismatchError(stepID, index, expected, got)
	}
	return nil
}

func timerFiredFromConditionResults(results []*pb.ConditionResult) bool {
	for _, result := range results {
		if timer := result.GetTimer(); timer != nil && timer.Fired {
			return true
		}
	}
	return false
}

func conditionResultsForExecute(
	stepExe *pb.ActiveStepExecution,
	consumed map[string][]*pb.Value,
) *conditionResults {
	timerFired := false
	if stepExe != nil {
		timerFired = timerFiredFromConditionResults(stepExe.ConditionResults)
	}
	return newConditionResults(timerFired, consumed)
}

