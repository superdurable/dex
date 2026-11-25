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
	"github.com/superdurable/dex/server-zd/integ/workflow/wf_non_retryable_failure"
)

// hourlyRetryPolicy keeps real business retry timing: unlimited attempts one hour apart.
func hourlyRetryPolicy() *dexpb.RetryPolicy {
	return &dexpb.RetryPolicy{
		InitialIntervalSeconds: 3600,
		MaximumIntervalSeconds: 3600,
		BackoffCoefficient:     1,
	}
}

func TestNonRetryableExecuteFailureProceedsToConfiguredStep(t *testing.T) {
	workerHandler := wf_non_retryable_failure.NewHandler()
	response := runNonRetryableFailureFlow(t, workerHandler, wf_non_retryable_failure.ExecuteNonRetryableInput, &dexpb.StepOptions{
		SkipWaitFor:                      true,
		ExecuteRetryPolicy:               hourlyRetryPolicy(),
		ExecuteFailurePolicy:             dexpb.ExecuteMethodFailurePolicy_EXECUTE_METHOD_FAILURE_POLICY_PROCEED_TO_CONFIGURED_STEP,
		ExecuteFailureProceedStepType:    wf_non_retryable_failure.StepRecover,
		ExecuteFailureProceedStepOptions: &dexpb.StepOptions{SkipWaitFor: true},
	})
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, response.GetFlowStatus())
	require.Equal(t, map[string]int64{
		"S1_execute":      1,
		"Recover_execute": 1,
	}, workerHandler.GetTestResult().InvokeHistory)
}

func TestNonRetryableExecuteFailureFailsFlowByDefault(t *testing.T) {
	workerHandler := wf_non_retryable_failure.NewHandler()
	response := runNonRetryableFailureFlow(t, workerHandler, wf_non_retryable_failure.ExecuteNonRetryableInput, &dexpb.StepOptions{
		SkipWaitFor:        true,
		ExecuteRetryPolicy: hourlyRetryPolicy(),
	})
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_FAILED, response.GetFlowStatus())
	require.Equal(t, map[string]int64{"S1_execute": 1}, workerHandler.GetTestResult().InvokeHistory)
}

func TestNonRetryableWaitForFailureProceedsToExecute(t *testing.T) {
	workerHandler := wf_non_retryable_failure.NewHandler()
	response := runNonRetryableFailureFlow(t, workerHandler, wf_non_retryable_failure.WaitForNonRetryableInput, &dexpb.StepOptions{
		WaitForRetryPolicy:   hourlyRetryPolicy(),
		WaitForFailurePolicy: dexpb.WaitForMethodFailurePolicy_WAIT_FOR_METHOD_FAILURE_POLICY_PROCEED_ON_FAILURE,
	})
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, response.GetFlowStatus())
	require.Equal(t, map[string]int64{
		"S1_waitFor": 1,
		"S1_execute": 1,
	}, workerHandler.GetTestResult().InvokeHistory)
}

// The control case: the same failure without the flag uses every attempt of its policy.
func TestRetryableExecuteFailureRetriesBeforeConfiguredStep(t *testing.T) {
	workerHandler := wf_non_retryable_failure.NewHandler()
	response := runNonRetryableFailureFlow(t, workerHandler, wf_non_retryable_failure.ExecuteRetryableInput, &dexpb.StepOptions{
		SkipWaitFor: true,
		ExecuteRetryPolicy: &dexpb.RetryPolicy{
			InitialIntervalSeconds: 1,
			MaximumIntervalSeconds: 1,
			BackoffCoefficient:     1,
			MaximumAttempts:        3,
		},
		ExecuteFailurePolicy:             dexpb.ExecuteMethodFailurePolicy_EXECUTE_METHOD_FAILURE_POLICY_PROCEED_TO_CONFIGURED_STEP,
		ExecuteFailureProceedStepType:    wf_non_retryable_failure.StepRecover,
		ExecuteFailureProceedStepOptions: &dexpb.StepOptions{SkipWaitFor: true},
	})
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, response.GetFlowStatus())
	require.Equal(t, map[string]int64{
		"S1_execute":      3,
		"Recover_execute": 1,
	}, workerHandler.GetTestResult().InvokeHistory)
}

// runNonRetryableFailureFlow returns the WaitForFlow response; the 30-second limit is far
// below any hourly retry, so completing proves the failure skipped its retries.
func runNonRetryableFailureFlow(
	t *testing.T,
	workerHandler any,
	input string,
	stepOptions *dexpb.StepOptions,
) *dexpb.FlowResult {
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, serverZDTestConfig{})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	flowId := wf_non_retryable_failure.FlowType + uuid.NewString()
	_, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           wf_non_retryable_failure.FlowType,
		FlowTimeoutSeconds: 30,
		StartStepType:      wf_non_retryable_failure.Step1,
		StepInput:          &dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: input}},
		StepOptions:        stepOptions,
		FlowStartOptions:   withWorkerTarget(nil, workerTarget),
	})
	require.NoError(t, err)
	response, err := runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowId})
	require.NoError(t, err)
	return response
}
