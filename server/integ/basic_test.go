// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package integ

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/iwf/gen/iwfpb"
	"github.com/superdurable/iwf/integ/workflow/basic"
	"github.com/superdurable/iwf/service"
	"github.com/superdurable/iwf/service/common/ptr"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestBasicFlowTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		t.Run(fmt.Sprintf("default-%d", i), func(t *testing.T) {
			doTestBasicFlow(t, service.BackendTypeTemporal, nil)
		})
		t.Run(fmt.Sprintf("continue-as-new-%d", i), func(t *testing.T) {
			doTestBasicFlow(
				t,
				service.BackendTypeTemporal,
				minimumContinueAsNewConfig(
					iwfpb.StepDurability_STEP_DURABILITY_ASYNC,
				),
			)
		})
		t.Run(fmt.Sprintf("active-step-search-disabled-%d", i), func(t *testing.T) {
			doTestBasicFlow(t, service.BackendTypeTemporal, &iwfpb.FlowConfig{
				ActiveStepSearchMode: ptr.Any(
					iwfpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_DISABLED,
				),
			})
		})
	}
}

func TestBasicFlowCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		t.Run(fmt.Sprintf("default-%d", i), func(t *testing.T) {
			doTestBasicFlow(t, service.BackendTypeCadence, nil)
		})
		t.Run(fmt.Sprintf("continue-as-new-%d", i), func(t *testing.T) {
			doTestBasicFlow(
				t,
				service.BackendTypeCadence,
				minimumContinueAsNewConfig(
					iwfpb.StepDurability_STEP_DURABILITY_ASYNC,
				),
			)
		})
		t.Run(fmt.Sprintf("active-step-search-disabled-%d", i), func(t *testing.T) {
			doTestBasicFlow(t, service.BackendTypeCadence, &iwfpb.FlowConfig{
				ActiveStepSearchMode: ptr.Any(
					iwfpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_DISABLED,
				),
			})
		})
	}
}

func doTestBasicFlow(
	t *testing.T,
	backendType service.BackendType,
	flowConfig *iwfpb.FlowConfig,
) {
	workerHandler := basic.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startIwfService(t, IwfServiceTestConfig{
		BackendType: backendType,
	})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowId := basic.FlowType + "-" + uuid.NewString()
	flowInput := &iwfpb.Value{
		Kind: &iwfpb.Value_ObjValue{
			ObjValue: &iwfpb.EncodedObject{
				Encoding: "json",
				Payload:  []byte("test data"),
			},
		},
	}
	startRequest := &iwfpb.StartFlowRequest{
		FlowId:             flowId,
		FlowType:           basic.FlowType,
		FlowTimeoutSeconds: 100,
		WorkerTarget:       workerTarget,
		StartStepType:      basic.Step1,
		StepInput:          flowInput,
		FlowStartOptions: &iwfpb.FlowStartOptions{
			FlowConfigOverride: flowConfig,
			IdReusePolicy:      iwfpb.IdReusePolicy_ID_REUSE_POLICY_DISALLOW_REUSE,
			// TODO: need more work to write integ test for cron
			// manual testing for now by uncomment the following line
			// CronSchedule: "* * * * *",
			RetryPolicy: &iwfpb.FlowRetryPolicy{
				InitialIntervalSeconds: 11,
				BackoffCoefficient:     11,
				MaximumAttempts:        11,
				MaximumIntervalSeconds: 11,
			},
		},
		StepOptions: &iwfpb.StepOptions{
			WaitForTimeoutSeconds: 12,
			ExecuteTimeoutSeconds: 13,
			WaitForRetryPolicy: &iwfpb.RetryPolicy{
				InitialIntervalSeconds: 12,
				BackoffCoefficient:     12,
				MaximumAttempts:        12,
				MaximumIntervalSeconds: 12,
			},
			ExecuteRetryPolicy: &iwfpb.RetryPolicy{
				InitialIntervalSeconds: 13,
				BackoffCoefficient:     13,
				MaximumAttempts:        13,
				MaximumIntervalSeconds: 13,
			},
		},
	}
	startResponse, err := flowClient.StartFlow(ctx, startRequest)
	require.NoError(t, err)
	require.NotEmpty(t, startResponse.GetRunId())

	_, err = flowClient.StartFlow(ctx, startRequest)
	require.Equal(t, codes.AlreadyExists, status.Code(err))
	require.Equal(
		t,
		iwfpb.ErrorSubStatus_ERROR_SUB_STATUS_FLOW_ALREADY_STARTED,
		grpcErrorResponse(t, err).GetSubStatus(),
	)

	response, err := flowClient.WaitForFlow(ctx, &iwfpb.WaitForFlowRequest{
		FlowId:          flowId,
		NeedsResults:    true,
		WaitTimeSeconds: 20,
	})
	require.NoError(t, err)

	_, err = flowClient.WaitForFlow(ctx, &iwfpb.WaitForFlowRequest{
		FlowId:          "a-wrong-flow-id-" + uuid.NewString(),
		WaitTimeSeconds: 1,
	})
	require.Equal(t, codes.NotFound, status.Code(err))
	require.Equal(
		t,
		iwfpb.ErrorSubStatus_ERROR_SUB_STATUS_FLOW_NOT_EXISTS,
		grpcErrorResponse(t, err).GetSubStatus(),
	)

	history := workerHandler.GetTestResult().InvokeHistory
	require.Equal(t, map[string]int64{
		"S1_waitFor": 1,
		"S1_execute": 1,
		"S2_waitFor": 1,
		"S2_execute": 1,
	}, history)

	require.Equal(t, iwfpb.FlowStatus_FLOW_STATUS_COMPLETED, response.GetFlowStatus())
	require.Len(t, response.GetResults(), 1)
	result := response.GetResults()[0]
	require.Equal(t, basic.Step2, result.GetCompletedStepType())
	require.Equal(t, basic.Step2+"-1", result.GetCompletedStepExecutionId())
	require.True(t, proto.Equal(flowInput, result.GetCompletedStepOutput()))
}
