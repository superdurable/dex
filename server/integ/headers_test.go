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
	"github.com/superdurable/dex/integ/workflow/headers"
	"github.com/superdurable/dex/service"
	"github.com/superdurable/dex/service/common/ptr"
)

func TestHeadersFlowTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}

	for i := 0; i < *repeatIntegTest; i++ {
		doTestFlowWithHeaders(t, service.BackendTypeTemporal, nil)
		smallWaitForFastTest()

		doTestFlowWithHeaders(
			t,
			service.BackendTypeTemporal,
			minimumContinueAsNewConfig(dexpb.StepDurability_STEP_DURABILITY_ASYNC),
		)
		smallWaitForFastTest()

		doTestFlowWithHeaders(t, service.BackendTypeTemporal, &dexpb.FlowConfig{
			ActiveStepSearchMode: ptr.Any(
				dexpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_DISABLED,
			),
		})
		smallWaitForFastTest()
	}
}

func TestHeadersFlowCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestFlowWithHeaders(t, service.BackendTypeCadence, nil)
		smallWaitForFastTest()
		doTestFlowWithHeaders(
			t,
			service.BackendTypeCadence,
			minimumContinueAsNewConfig(dexpb.StepDurability_STEP_DURABILITY_SYNC),
		)
		smallWaitForFastTest()
	}
}

func doTestFlowWithHeaders(
	t *testing.T,
	backendType service.BackendType,
	flowConfig *dexpb.FlowConfig,
) {
	workerHandler := headers.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, DexServiceTestConfig{
		BackendType: backendType,
		DefaultHeaders: map[string]string{
			headers.TestHeaderKey: headers.TestHeaderValue,
		},
	})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowId := headers.WorkflowType + "-" + uuid.NewString()
	stepInput := encodedObjectValue("json", []byte("test data"))
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		FlowId:             flowId,
		FlowType:           headers.WorkflowType,
		FlowTimeoutSeconds: 100,
		WorkerTarget:       workerTarget,
		StartStepType:      headers.State1,
		StepInput:          stepInput,
		FlowStartOptions: &dexpb.FlowStartOptions{
			FlowConfigOverride: flowConfig,
		},
	})
	require.NoError(t, err)

	_, err = flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId:          flowId,
		WaitTimeSeconds: 20,
	})
	require.NoError(t, err)

	if flowConfig != nil && flowConfig.ContinueAsNewThreshold != nil {
		require.Eventually(t, func() bool {
			return len(runtime.internalDumpCapture.snapshot()) > 0
		}, 30*time.Second, 100*time.Millisecond, "expected DumpFlowForContinueAsNew call")
		runtime.requireInternalDumpHeaders(
			t,
			headers.TestHeaderKey,
			headers.TestHeaderValue,
		)
	}

	expectedHistory := map[string]int64{
		"S1_waitFor": 1,
		"S1_execute": 1,
		"S2_waitFor": 1,
		"S2_execute": 1,
	}
	history := workerHandler.GetTestResult().InvokeHistory
	require.Equalf(t, expectedHistory, history, "headers test fail, %v", history)
}
