// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package wf_non_retryable_failure

import (
	"context"
	"fmt"
	"sync"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/server-zd/integ/workflow/common"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

/**
 * This test flow reports Worker failures with or without the non-retryable flag.
 *
 * Step1:
 *		- WaitFor fails when the input is WaitForNonRetryableInput
 *		- Execute fails when the input is ExecuteNonRetryableInput or ExecuteRetryableInput
 * StepRecover:
 *		- Execute checks the recovery error and gracefully completes the flow
 */
const (
	FlowType                 = "wf_non_retryable_failure"
	Step1                    = "S1"
	StepRecover              = "Recover"
	WaitForNonRetryableInput = "wait-for-non-retryable"
	ExecuteNonRetryableInput = "execute-non-retryable"
	ExecuteRetryableInput    = "execute-retryable"
	errorDetail              = "permanent failure"
	errorType                = "PermanentFailure"
)

type handler struct {
	dexpb.UnimplementedWorkerServiceServer
	invokeHistory sync.Map
}

func NewHandler() *handler {
	return &handler{}
}

func (h *handler) InvokeWaitForMethod(
	_ context.Context,
	request *dexpb.InvokeWaitForMethodRequest,
) (*dexpb.InvokeWaitForMethodResponse, error) {
	common.LogRequest("received waitFor request, ", request)
	if request.GetFlowType() != FlowType || request.GetStepType() != Step1 {
		panic("should not get here")
	}
	h.recordInvocation(request.GetStepType() + "_waitFor")
	if request.GetStepInput().GetStringValue() == WaitForNonRetryableInput {
		return nil, workerFailure(true)
	}
	return &dexpb.InvokeWaitForMethodResponse{}, nil
}

func (h *handler) InvokeExecuteMethod(
	_ context.Context,
	request *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	common.LogRequest("received execute request, ", request)
	if request.GetFlowType() != FlowType {
		panic("should not get here")
	}
	h.recordInvocation(request.GetStepType() + "_execute")
	input := request.GetStepInput().GetStringValue()
	switch {
	case request.GetStepType() == Step1 && input == ExecuteNonRetryableInput:
		return nil, workerFailure(true)
	case request.GetStepType() == Step1 && input == ExecuteRetryableInput:
		return nil, workerFailure(false)
	case request.GetStepType() == Step1 && input == WaitForNonRetryableInput:
		if !request.GetConditionResults().GetWaitForFailed() {
			panic("wait_for_failed should be true")
		}
		validateRecoveryError(request.GetContext().GetRecoveryError())
	case request.GetStepType() == StepRecover:
		validateRecoveryError(request.GetContext().GetRecoveryError())
	default:
		panic("should not get here")
	}
	return &dexpb.InvokeExecuteMethodResponse{
		StepDecision: &dexpb.StepDecision{
			CloseDecision: common.GracefulCompleteDecision(nil),
		},
	}, nil
}

func (h *handler) recordInvocation(key string) {
	if value, ok := h.invokeHistory.Load(key); ok {
		h.invokeHistory.Store(key, value.(int64)+1)
		return
	}
	h.invokeHistory.Store(key, int64(1))
}

func workerFailure(isNonRetryable bool) error {
	workerStatus, err := status.New(codes.Unknown, errorDetail).WithDetails(
		&dexpb.WorkerErrorResponse{
			Detail:       errorDetail,
			ErrorType:    errorType,
			NonRetryable: isNonRetryable,
		},
	)
	if err != nil {
		panic(err)
	}
	return workerStatus.Err()
}

func validateRecoveryError(recoveryError *dexpb.RecoveryErrorInfo) {
	if recoveryError.GetDetail() != errorDetail ||
		recoveryError.GetErrorType() != errorType {
		panic(fmt.Sprintf("recovery error is not correct: %v", recoveryError))
	}
}

func (h *handler) GetTestResult() common.TestResult {
	invokeHistory := make(map[string]int64)
	h.invokeHistory.Range(func(key, value interface{}) bool {
		invokeHistory[key.(string)] = value.(int64)
		return true
	})
	return common.TestResult{InvokeHistory: invokeHistory}
}
