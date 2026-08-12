// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package interfaces

import "github.com/superdurable/dex/gen/dexpb"

func IsStepActivityInput(input interface{}) bool {
	switch input.(type) {
	case *dexpb.InvokeWaitForMethodActivityInput:
		return true
	case *dexpb.InvokeExecuteMethodActivityInput:
		return true
	default:
		return false
	}
}

func StepActivityInputWithAttemptContext(
	input interface{},
	previousAttempts int32,
	firstAttemptTimestamp int64,
) interface{} {
	switch activityInput := input.(type) {
	case *dexpb.InvokeWaitForMethodActivityInput:
		return waitForActivityInputWithAttemptContext(
			activityInput,
			previousAttempts,
			firstAttemptTimestamp,
		)
	case *dexpb.InvokeExecuteMethodActivityInput:
		return executeActivityInputWithAttemptContext(
			activityInput,
			previousAttempts,
			firstAttemptTimestamp,
		)
	default:
		panic("step activity input required")
	}
}

func waitForActivityInputWithAttemptContext(
	input *dexpb.InvokeWaitForMethodActivityInput,
	previousAttempts int32,
	firstAttemptTimestamp int64,
) *dexpb.InvokeWaitForMethodActivityInput {
	request := input.GetRequest()
	if request == nil {
		panic("step activity request required")
	}
	return &dexpb.InvokeWaitForMethodActivityInput{
		WorkerTarget: input.GetWorkerTarget(),
		Request: &dexpb.InvokeWaitForMethodRequest{
			Context:    stepActivityContextForFallback(request.GetContext(), previousAttempts, firstAttemptTimestamp),
			FlowType:   request.GetFlowType(),
			StepType:   request.GetStepType(),
			StepInput:  request.GetStepInput(),
			Attributes: request.GetAttributes(),
		},
	}
}

func executeActivityInputWithAttemptContext(
	input *dexpb.InvokeExecuteMethodActivityInput,
	previousAttempts int32,
	firstAttemptTimestamp int64,
) *dexpb.InvokeExecuteMethodActivityInput {
	request := input.GetRequest()
	if request == nil {
		panic("step activity request required")
	}
	return &dexpb.InvokeExecuteMethodActivityInput{
		WorkerTarget: input.GetWorkerTarget(),
		Request: &dexpb.InvokeExecuteMethodRequest{
			Context:          stepActivityContextForFallback(request.GetContext(), previousAttempts, firstAttemptTimestamp),
			FlowType:         request.GetFlowType(),
			StepType:         request.GetStepType(),
			StepInput:        request.GetStepInput(),
			Attributes:       request.GetAttributes(),
			StepExeLocals:    request.GetStepExeLocals(),
			ConditionResults: request.GetConditionResults(),
		},
	}
}

func stepActivityContextForFallback(
	workerContext *dexpb.Context,
	previousAttempts int32,
	firstAttemptTimestamp int64,
) *dexpb.Context {
	if workerContext == nil {
		panic("step activity Context required")
	}
	return &dexpb.Context{
		FlowId:                workerContext.GetFlowId(),
		RunId:                 workerContext.GetRunId(),
		FlowStartedTimestamp:  workerContext.GetFlowStartedTimestamp(),
		StepExecutionId:       workerContext.GetStepExecutionId(),
		FirstAttemptTimestamp: firstAttemptTimestamp,
		Attempt:               previousAttempts,
		FromStepExecutionId:   workerContext.GetFromStepExecutionId(),
		RecoveryError:         workerContext.GetRecoveryError(),
	}
}
