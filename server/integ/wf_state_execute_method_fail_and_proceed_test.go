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
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/integ/workflow/wf_execute_method_fail_and_proceed"
	"github.com/superdurable/dex/service"
)

func TestStateExecuteMethodFailAndProceedTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestStateExecuteMethodFailAndProceed(
			t,
			service.BackendTypeTemporal,
			nil,
			dexpb.StepDurability_STEP_DURABILITY_UNSPECIFIED,
		)
		smallWaitForFastTest()
		doTestStateExecuteMethodFailAndProceed(
			t,
			service.BackendTypeTemporal,
			minimumContinueAsNewSyncDurabilityConfig(),
			dexpb.StepDurability_STEP_DURABILITY_UNSPECIFIED,
		)
		smallWaitForFastTest()
		doTestStateExecuteMethodFailAndProceed(
			t,
			service.BackendTypeTemporal,
			syncDurabilityConfig(),
			dexpb.StepDurability_STEP_DURABILITY_ASYNC,
		)
		smallWaitForFastTest()
	}
}

func TestStateExecuteMethodFailAndProceedCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestStateExecuteMethodFailAndProceed(
			t,
			service.BackendTypeCadence,
			nil,
			dexpb.StepDurability_STEP_DURABILITY_UNSPECIFIED,
		)
		smallWaitForFastTest()
		doTestStateExecuteMethodFailAndProceed(
			t,
			service.BackendTypeCadence,
			minimumContinueAsNewSyncDurabilityConfig(),
			dexpb.StepDurability_STEP_DURABILITY_UNSPECIFIED,
		)
		smallWaitForFastTest()
	}
}

func doTestStateExecuteMethodFailAndProceed(
	t *testing.T,
	backendType service.BackendType,
	flowConfig *dexpb.FlowConfig,
	executeDurabilityOverride dexpb.StepDurability,
) {
	workerHandler := wf_execute_method_fail_and_proceed.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, DexServiceTestConfig{
		BackendType: backendType,
	})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowId := wf_execute_method_fail_and_proceed.FlowType + uuid.NewString()
	startRequest := &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           wf_execute_method_fail_and_proceed.FlowType,
		FlowTimeoutSeconds: 10,

		StartStepType: wf_execute_method_fail_and_proceed.Step1,
		StepInput: &dexpb.Value{
			Kind: &dexpb.Value_ObjValue{
				ObjValue: &dexpb.EncodedObject{
					Encoding: wf_execute_method_fail_and_proceed.InputDataEncoding,
					Payload:  []byte(wf_execute_method_fail_and_proceed.InputData),
				},
			},
		},
		StepOptions: &dexpb.StepOptions{
			SkipWaitFor:               true,
			ExecuteDurabilityOverride: executeDurabilityOverride,
			ExecuteRetryPolicy: &dexpb.RetryPolicy{
				MaximumAttempts: 1,
			},
			ExecuteFailurePolicy:          dexpb.ExecuteMethodFailurePolicy_EXECUTE_METHOD_FAILURE_POLICY_PROCEED_TO_CONFIGURED_STEP,
			ExecuteFailureProceedStepType: wf_execute_method_fail_and_proceed.StepRecover,
			ExecuteFailureProceedStepOptions: &dexpb.StepOptions{
				SkipWaitFor: true,
			},
		},
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
	expectedExecute := int64(1)
	if executeDurabilityOverride == dexpb.StepDurability_STEP_DURABILITY_ASYNC {
		expectedExecute = 2
	}
	require.Equal(t, map[string]int64{
		"S1_execute":      expectedExecute,
		"Recover_execute": 1,
	}, history)

	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, resp.GetFlowStatus())
	require.Empty(t, resp.GetResults())
}
