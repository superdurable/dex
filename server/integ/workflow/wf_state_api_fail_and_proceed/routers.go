// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package wf_state_api_fail_and_proceed

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/integ/workflow/common"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

/**
 * This test flow has one step, using WorkerServiceServer to implement the flow directly.
 *
 * Step1:
 *		- The step will fail and proceed to StepRecover which will gracefully complete flow
 */
const (
	FlowType          = "wf_state_api_fail_and_proceed"
	Step1             = "S1"
	StepRecover       = "Recover"
	RetryOnceInput    = "retry-once"
	RetryFailureInput = "retry-failure"
	errorDetail       = "waitFor method failure"
	errorType         = "WaitForFailure"
	errorStack        = "waitFor stack"
	retryAfter        = int32(1)
)

type handler struct {
	dexpb.UnimplementedWorkerServiceServer
	invokeHistory     sync.Map
	attemptTimesMutex sync.Mutex
	attemptTimes      []time.Time
}

func NewHandler() *handler {
	return &handler{
		invokeHistory: sync.Map{},
	}
}

func (h *handler) InvokeWaitForMethod(
	_ context.Context,
	request *dexpb.InvokeWaitForMethodRequest,
) (*dexpb.InvokeWaitForMethodResponse, error) {
	common.LogRequest("received waitFor request, ", request)

	if request.GetFlowType() != FlowType {
		panic("should not get here")
	}

	if value, ok := h.invokeHistory.Load(request.GetStepType() + "_waitFor"); ok {
		h.invokeHistory.Store(request.GetStepType()+"_waitFor", value.(int64)+1)
	} else {
		h.invokeHistory.Store(request.GetStepType()+"_waitFor", int64(1))
	}

	if request.GetStepType() == Step1 {
		stepInput := request.GetStepInput().GetStringValue()
		if stepInput == RetryOnceInput {
			h.attemptTimesMutex.Lock()
			h.attemptTimes = append(h.attemptTimes, time.Now())
			h.attemptTimesMutex.Unlock()
			if request.GetContext().GetAttempt() > 1 {
				return &dexpb.InvokeWaitForMethodResponse{}, nil
			}
		}
		if stepInput == RetryOnceInput || stepInput == RetryFailureInput {
			return nil, workerFailure(retryAfter)
		}
		return nil, workerFailure(0)
	}

	panic("should not get here")
}

func (h *handler) InvokeExecuteMethod(
	_ context.Context,
	request *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	common.LogRequest("received execute request, ", request)

	if request.GetFlowType() != FlowType {
		panic("should not get here")
	}

	if value, ok := h.invokeHistory.Load(request.GetStepType() + "_execute"); ok {
		h.invokeHistory.Store(request.GetStepType()+"_execute", value.(int64)+1)
	} else {
		h.invokeHistory.Store(request.GetStepType()+"_execute", int64(1))
	}

	conditionResults := request.GetConditionResults()
	if request.GetStepInput().GetStringValue() == RetryOnceInput {
		if conditionResults.GetWaitForFailed() || request.GetContext().GetRecoveryError() != nil {
			panic("successful waitFor retry must not set a recovery error")
		}
	} else if conditionResults == nil || !conditionResults.GetWaitForFailed() {
		panic("wait_for_failed should be true")
	} else {
		validateRecoveryError(request.GetContext().GetRecoveryError())
	}

	return &dexpb.InvokeExecuteMethodResponse{
		StepDecision: &dexpb.StepDecision{
			CloseDecision: common.GracefulCompleteDecision(nil),
		},
	}, nil
}

func (h *handler) GetRetryAttemptGap() time.Duration {
	h.attemptTimesMutex.Lock()
	defer h.attemptTimesMutex.Unlock()
	if len(h.attemptTimes) != 2 {
		return 0
	}
	return h.attemptTimes[1].Sub(h.attemptTimes[0])
}

func workerFailure(retryAfterSeconds int32) error {
	workerStatus, err := status.New(codes.InvalidArgument, errorDetail).WithDetails(
		&dexpb.WorkerErrorResponse{
			Detail:            errorDetail,
			ErrorType:         errorType,
			StackTrace:        errorStack,
			RetryAfterSeconds: retryAfterSeconds,
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
		panic(fmt.Sprintf("waitFor recovery error is not correct: %v", recoveryError))
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
