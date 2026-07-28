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
	"github.com/superdurable/dex/integ/workflow/wf_state_options_data_attributes_loading"
	"github.com/superdurable/dex/service"
)

func TestWfStateOptionsDataAttributesLoading(t *testing.T) {
	for _, backendType := range getBackendTypes() {
		for i := 0; i < *repeatIntegTest; i++ {
			doTestWfStateOptionsDataAttributesLoading(t, backendType)
			smallWaitForFastTest()
		}
	}
}

func doTestWfStateOptionsDataAttributesLoading(t *testing.T, backendType service.BackendType) {
	workerHandler := wf_state_options_data_attributes_loading.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowId := wf_state_options_data_attributes_loading.WorkflowType + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		FlowId:             flowId,
		FlowType:           wf_state_options_data_attributes_loading.WorkflowType,
		FlowTimeoutSeconds: 10,
		WorkerTarget:       workerTarget,
		StartStepType:      wf_state_options_data_attributes_loading.State1,
		StepInput:          objJSONValue(`"PARTIAL_WITHOUT_LOCKING"`),
	})
	require.NoError(t, err)

	_, err = flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowId})
	require.NoError(t, err)

	history := workerHandler.GetTestResult().InvokeHistory
	require.Equal(t, map[string]int64{
		"S1_waitFor": 1,
		"S1_execute": 1,
		"S2_waitFor": 1,
		"S2_execute": 1,
		"S3_waitFor": 1,
		"S3_execute": 1,
		"S4_waitFor": 1,
		"S4_execute": 1,
		"S5_waitFor": 1,
		"S5_execute": 1,
	}, history)
}
