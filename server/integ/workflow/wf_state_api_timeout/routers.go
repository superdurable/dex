// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package wf_state_api_timeout

import (
	"context"
	"fmt"
	"github.com/superdurable/dex/integ/workflow/common"
	"sync"
	"time"

	"github.com/superdurable/dex/gen/dexpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

/**
 * This test flow has one step, using WorkerServiceServer to implement the flow directly.
 *
 * Step1:
 *		- Timeout is set for 10s
 *      - Waits for 30s to invoke a timeout exception
 */
const (
	FlowType = "wf_state_api_timeout"
	Step1    = "S1"
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
	request *dexpb.InvokeWaitForMethodRequest,
) (*dexpb.InvokeWaitForMethodResponse, error) {
	common.LogRequest("received waitFor request, ", request)

	if request.GetFlowType() == FlowType {
		if value, ok := h.invokeHistory.Load(request.GetStepType() + "_waitFor"); ok {
			h.invokeHistory.Store(request.GetStepType()+"_waitFor", value.(int64)+1)
		} else {
			h.invokeHistory.Store(request.GetStepType()+"_waitFor", int64(1))
		}

		if request.GetStepType() == Step1 {
			time.Sleep(30 * time.Second)
			return nil, status.Error(codes.InvalidArgument, "waitFor method timeout")
		}
	}

	fmt.Printf("FlowType: %v", request.GetFlowType())
	panic("should not get here")
}

func (h *handler) InvokeExecuteMethod(
	_ context.Context,
	_ *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	panic("should not get here")
}

func (h *handler) GetTestResult() common.TestResult {
	invokeHistory := make(map[string]int64)
	h.invokeHistory.Range(func(key, value interface{}) bool {
		invokeHistory[key.(string)] = value.(int64)
		return true
	})
	return common.TestResult{InvokeHistory: invokeHistory}
}
