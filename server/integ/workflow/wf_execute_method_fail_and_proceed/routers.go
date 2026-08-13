// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package wf_execute_method_fail_and_proceed

import (
	"context"
	"fmt"
	"strings"
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
 *		- WaitFor method is skipped
 *      - Execute method will intentionally fail
 * StepRecover:
 *		- Execute method will gracefully complete flow
 */
const (
	FlowType          = "wf_execute_method_fail_and_proceed"
	Step1             = "S1"
	StepRecover       = "Recover"
	InputData         = "test-data"
	TimeoutInputData  = "timeout-data"
	InputDataEncoding = "test-encoding"
	errorDetail       = "test-error"
	errorType         = "ExecuteFailure"
	errorStack        = "execute stack"
)

type handler struct {
	dexpb.UnimplementedWorkerServiceServer
	invokeHistory sync.Map
}

func NewHandler() *handler {
	return &handler{
		invokeHistory: sync.Map{},
	}
}

func (h *handler) InvokeWaitForMethod(
	_ context.Context,
	_ *dexpb.InvokeWaitForMethodRequest,
) (*dexpb.InvokeWaitForMethodResponse, error) {
	panic("should not get here")
}

func (h *handler) InvokeExecuteMethod(
	ctx context.Context,
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

	stepInput := request.GetStepInput()
	encoding, data := objValueParts(stepInput)
	if (data != InputData && data != TimeoutInputData) || encoding != InputDataEncoding {
		panic("input is not correct: " + data + ", " + encoding)
	}

	if request.GetStepType() == Step1 {
		if data == TimeoutInputData {
			time.Sleep(2 * time.Second)
			return nil, ctx.Err()
		}
		return nil, workerFailure()
	}
	if request.GetStepType() == StepRecover {
		if data == TimeoutInputData {
			validateTimeoutRecoveryError(request.GetContext().GetRecoveryError())
		} else {
			validateRecoveryError(request.GetContext().GetRecoveryError())
		}
		return &dexpb.InvokeExecuteMethodResponse{
			StepDecision: &dexpb.StepDecision{
				CloseDecision: common.GracefulCompleteDecision(nil),
			},
		}, nil
	}

	panic("should not get here")
}

func validateTimeoutRecoveryError(recoveryError *dexpb.RecoveryErrorInfo) {
	if recoveryError.GetErrorType() == dexpb.FlowErrorType_FLOW_ERROR_TYPE_WORKER_API_FAIL.String() {
		return
	}

	normalizedType := strings.ReplaceAll(strings.ToUpper(recoveryError.GetErrorType()), "_", "")
	if recoveryError.GetDetail() == "" ||
		!strings.Contains(normalizedType, "STARTTOCLOSE") {
		panic(fmt.Sprintf("execute timeout recovery error is not correct: %v", recoveryError))
	}
}

func workerFailure() error {
	workerStatus, err := status.New(codes.InvalidArgument, errorDetail).WithDetails(
		&dexpb.WorkerErrorResponse{
			Detail:     errorDetail,
			ErrorType:  errorType,
			StackTrace: errorStack,
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
		panic(fmt.Sprintf("execute recovery error is not correct: %v", recoveryError))
	}
}

func objValueParts(value *dexpb.Value) (encoding string, data string) {
	obj := value.GetObjValue()
	if obj == nil {
		return "", ""
	}
	return obj.GetEncoding(), string(obj.GetPayload())
}

func (h *handler) GetTestResult() common.TestResult {
	invokeHistory := make(map[string]int64)
	h.invokeHistory.Range(func(key, value interface{}) bool {
		invokeHistory[key.(string)] = value.(int64)
		return true
	})
	return common.TestResult{InvokeHistory: invokeHistory}
}
