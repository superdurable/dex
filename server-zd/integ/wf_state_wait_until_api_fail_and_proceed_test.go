// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package integ

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/server-zd/integ/workflow/wf_state_api_fail_and_proceed"
)

func TestStateApiFailAndProceedTemporal(t *testing.T) {
	for i := 0; i < *repeatIntegTest; i++ {
		doTestStateApiFailAndProceed(t, nil)
		smallWaitForFastTest()
		doTestStateApiFailAndProceed(t, minimumContinueAsNewSyncDurabilityConfig())
		smallWaitForFastTest()
		doTestStateApiFailAndProceed(t, asyncDurabilityConfig())
		smallWaitForFastTest()
	}
}

func doTestStateApiFailAndProceed(
	t *testing.T,
	flowConfig *dexpb.FlowConfig,
) {
	workerHandler := wf_state_api_fail_and_proceed.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, serverZDTestConfig{})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowId := wf_state_api_fail_and_proceed.FlowType + uuid.NewString()
	stepOptions := &dexpb.StepOptions{
		WaitForRetryPolicy: &dexpb.RetryPolicy{
			MaximumAttempts: 1,
		},
		WaitForFailurePolicy: dexpb.WaitForMethodFailurePolicy_WAIT_FOR_METHOD_FAILURE_POLICY_PROCEED_ON_FAILURE,
	}

	startRequest := &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           wf_state_api_fail_and_proceed.FlowType,
		FlowTimeoutSeconds: 10,

		StartStepType:    wf_state_api_fail_and_proceed.Step1,
		StepOptions:      stepOptions,
		FlowStartOptions: withWorkerTarget(nil, workerTarget),
	}
	if flowConfig != nil {
		startRequest.FlowStartOptions = withWorkerTarget(&dexpb.FlowStartOptions{
			FlowConfigOverride: flowConfig,
		}, workerTarget)
	}
	_, err := flowClient.StartFlow(ctx, startRequest)
	require.NoError(t, err)

	resp, err := flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId: flowId,
	})
	require.NoError(t, err)

	history := workerHandler.GetTestResult().InvokeHistory
	require.Equal(t, map[string]int64{
		"S1_waitFor": 1,
		"S1_execute": 1,
	}, history)

	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, resp.GetFlowStatus())
	require.Empty(t, resp.GetResults())
}

func TestStateApiRetryAfterTemporal(t *testing.T) {
	workerHandler := wf_state_api_fail_and_proceed.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, serverZDTestConfig{})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	flowId := wf_state_api_fail_and_proceed.FlowType + uuid.NewString()
	stepOptions := &dexpb.StepOptions{
		WaitForRetryPolicy: &dexpb.RetryPolicy{
			InitialIntervalSeconds: 10,
			MaximumAttempts:        2,
		},
		WaitForFailurePolicy: dexpb.WaitForMethodFailurePolicy_WAIT_FOR_METHOD_FAILURE_POLICY_PROCEED_ON_FAILURE,
	}
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           wf_state_api_fail_and_proceed.FlowType,
		FlowTimeoutSeconds: 20,
		StartStepType:      wf_state_api_fail_and_proceed.Step1,
		StepInput: &dexpb.Value{
			Kind: &dexpb.Value_StringValue{StringValue: wf_state_api_fail_and_proceed.RetryOnceInput},
		},
		StepOptions:      stepOptions,
		FlowStartOptions: withWorkerTarget(nil, workerTarget),
	})
	require.NoError(t, err)
	_, err = flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowId})
	require.NoError(t, err)

	require.Equal(t, map[string]int64{"S1_waitFor": 2, "S1_execute": 1}, workerHandler.GetTestResult().InvokeHistory)
	require.GreaterOrEqual(t, workerHandler.GetRetryAttemptGap(), 500*time.Millisecond)
	require.Less(t, workerHandler.GetRetryAttemptGap(), 5*time.Second)
}
