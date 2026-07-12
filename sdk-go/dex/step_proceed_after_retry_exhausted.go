package dex

import (
	pb "github.com/superdurable/dex/protocol-grpc/gen/dexpb"
)

// buildProceedCompletion builds a retry-exhaust proceed completion for either method kind.
// Execute: decision only (handleExecuteCompletion). WaitFor: same decision + waitForMethodFailed.
func buildProceedCompletion(
	retryKind stepTaskMethodKind,
	task *stepInvocationTask,
	handler stepCommon,
	failingInput *pb.Value,
	methodReport *pb.StepMethodReport,
	codec ObjectCodec,
) (*stepTaskCompletion, error) {
	decodedInput, err := decodeHandlerInput(codec, handler, failingInput)
	if err != nil {
		return nil, err
	}
	completion := stepTaskCompletion{
		task: task,
		decision: &stepDecisionImpl{
			Movements: []StepMovement{{
				StepID: stepIDFromCommon(handler),
				Input:  decodedInput,
			}},
		},
		methodReport: methodReport,
	}
	if retryKind == stepTaskMethodKindWaitFor {
		completion.waitForMethodFailed = true
	}
	return &completion, nil
}

func proceedHandlerForMethod(opts *StepOptions, kind stepTaskMethodKind) stepCommon {
	if opts == nil {
		return nil
	}
	switch kind {
	case stepTaskMethodKindWaitFor:
		return opts.WaitForMethodProceedToAfterRetryExhausted
	case stepTaskMethodKindExecute:
		return opts.ExecuteMethodProceedToAfterRetryExhausted
	default:
		return nil
	}
}

func decodeHandlerInput(codec ObjectCodec, handler stepCommon, inputVal *pb.Value) (any, error) {
	if inputVal == nil {
		return nil, nil
	}
	inputType, err := stepInputReflectType(handler)
	if err != nil {
		return nil, err
	}
	decoded, err := decodeToType(codec, inputVal, inputType)
	if err != nil {
		return nil, err
	}
	return decoded.Interface(), nil
}
