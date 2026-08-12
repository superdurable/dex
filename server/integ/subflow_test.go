// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package integ

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/integ/workflow/common"
	"github.com/superdurable/dex/service"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const (
	subFlowParentType = "sub-flow-parent"
	subFlowChildType  = "sub-flow-child"
	subFlowParentStep = "Parent"
	subFlowChildStep  = "Child"
)

type subFlowObservation struct {
	flowID string
	runID  string
	status dexpb.FlowStatus
}

type subFlowHandler struct {
	dexpb.UnimplementedWorkerServiceServer
	mu           sync.Mutex
	observations []subFlowObservation
}

func TestSubFlowConditionTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		t.Run(fmt.Sprintf("terminal-attach-after-reset-%d", i), func(t *testing.T) {
			doTestSubFlowCondition(t, service.BackendTypeTemporal)
		})
	}
}

func TestSubFlowConditionCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		t.Run(fmt.Sprintf("terminal-attach-after-reset-%d", i), func(t *testing.T) {
			doTestSubFlowCondition(t, service.BackendTypeCadence)
		})
	}
}

func doTestSubFlowCondition(t *testing.T, backendType service.BackendType) {
	handler := &subFlowHandler{}
	workerTarget := startWorker(t, handler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})
	flowClient := runtime.FlowClient
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	parentFlowID := subFlowParentType + "-" + uuid.NewString()
	childFlowID := "SubFlow-" + parentFlowID + "-" + subFlowParentStep + "-1-0"
	input := stringValue("child-output")
	startResponse, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             parentFlowID,
		FlowType:           subFlowParentType,
		FlowTimeoutSeconds: 30,
		StartStepType:      subFlowParentStep,
		StepInput:          input,
		FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{
			IdReusePolicy: dexpb.IdReusePolicy_ID_REUSE_POLICY_DISALLOW_REUSE,
		}, workerTarget),
	})
	require.NoError(t, err)

	firstResult, err := flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId:          parentFlowID,
		NeedsResults:    true,
		WaitTimeSeconds: 30,
	})
	require.NoError(t, err)
	require.Equal(t, parentFlowID, firstResult.GetFlowId())
	require.NotEmpty(t, firstResult.GetRunId())
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, firstResult.GetFlowStatus())
	require.Len(t, firstResult.GetResults(), 1)
	require.True(t, proto.Equal(input, firstResult.GetResults()[0].GetCompletedStepOutput()))

	firstChild, err := flowClient.GetFlowSummary(ctx, &dexpb.GetFlowSummaryRequest{
		FlowId: childFlowID,
	})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, firstChild.GetFlowStatus())
	require.Equal(t, startResponse.GetRunId()+subFlowParentStep+"-1", firstChild.GetRequestId())

	_, err = flowClient.ResetFlow(ctx, &dexpb.ResetFlowRequest{
		FlowId:    parentFlowID,
		RunId:     startResponse.GetRunId(),
		ResetType: dexpb.FlowResetType_FLOW_RESET_TYPE_BEGINNING,
	})
	require.NoError(t, err)
	secondResult, err := flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId:          parentFlowID,
		NeedsResults:    true,
		WaitTimeSeconds: 30,
	})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, secondResult.GetFlowStatus())

	secondChild, err := flowClient.GetFlowSummary(ctx, &dexpb.GetFlowSummaryRequest{
		FlowId: childFlowID,
	})
	require.NoError(t, err)
	require.Equal(t, firstChild.GetFlowExecutionId().GetRunId(), secondChild.GetFlowExecutionId().GetRunId())
	require.Equal(t, firstChild.GetRequestId(), secondChild.GetRequestId())
	require.Equal(t, []subFlowObservation{
		{flowID: childFlowID, runID: firstChild.GetFlowExecutionId().GetRunId(), status: dexpb.FlowStatus_FLOW_STATUS_COMPLETED},
		{flowID: childFlowID, runID: firstChild.GetFlowExecutionId().GetRunId(), status: dexpb.FlowStatus_FLOW_STATUS_COMPLETED},
	}, handler.results())
}

func (h *subFlowHandler) InvokeWaitForMethod(
	_ context.Context,
	request *dexpb.InvokeWaitForMethodRequest,
) (*dexpb.InvokeWaitForMethodResponse, error) {
	common.LogRequest("received SubFlow waitFor request, ", request)
	if request.GetFlowType() != subFlowParentType || request.GetStepType() != subFlowParentStep {
		return nil, status.Error(codes.InvalidArgument, "unexpected waitFor invocation")
	}
	return &dexpb.InvokeWaitForMethodResponse{
		WaitingCondition: &dexpb.WaitingCondition{
			WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
			SubFlowConditions: []*dexpb.SubFlowCondition{{
				FlowType:      subFlowChildType,
				StartStepType: subFlowChildStep,
				StepInput:     request.GetStepInput(),
				StepOptions:   &dexpb.StepOptions{SkipWaitFor: true},
				Options: &dexpb.SubFlowOptions{
					ReusePolicy: dexpb.SubFlowReusePolicy_SUB_FLOW_REUSE_POLICY_RESTART_IF_PREVIOUS_EXITS_ABNORMALLY,
				},
			}},
		},
	}, nil
}

func (h *subFlowHandler) InvokeExecuteMethod(
	_ context.Context,
	request *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	common.LogRequest("received SubFlow execute request, ", request)
	switch request.GetFlowType() {
	case subFlowChildType:
		if request.GetStepType() != subFlowChildStep {
			return nil, status.Error(codes.InvalidArgument, "unexpected child Step")
		}
		return &dexpb.InvokeExecuteMethodResponse{
			StepDecision: &dexpb.StepDecision{
				CloseDecision: common.GracefulCompleteDecision(request.GetStepInput()),
			},
		}, nil
	case subFlowParentType:
		if request.GetStepType() != subFlowParentStep ||
			request.GetConditionResults().GetSubFlowResults() == nil {
			return nil, status.Error(codes.InvalidArgument, "unexpected parent execute invocation")
		}
		results := request.GetConditionResults().GetSubFlowResults()
		if len(results) != 1 || results[0].GetFlowStatus() != dexpb.FlowStatus_FLOW_STATUS_COMPLETED ||
			len(results[0].GetResults()) != 1 {
			return nil, status.Error(codes.InvalidArgument, "invalid SubFlow result")
		}
		h.mu.Lock()
		h.observations = append(h.observations, subFlowObservation{
			flowID: results[0].GetFlowId(),
			runID:  results[0].GetRunId(),
			status: results[0].GetFlowStatus(),
		})
		h.mu.Unlock()
		return &dexpb.InvokeExecuteMethodResponse{
			StepDecision: &dexpb.StepDecision{
				CloseDecision: common.GracefulCompleteDecision(
					results[0].GetResults()[0].GetCompletedStepOutput(),
				),
			},
		}, nil
	default:
		return nil, status.Error(codes.InvalidArgument, "unexpected Flow type")
	}
}

func (h *subFlowHandler) results() []subFlowObservation {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]subFlowObservation(nil), h.observations...)
}
