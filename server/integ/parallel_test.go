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
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/integ/workflow/parallel"
	"github.com/superdurable/dex/service"
	"google.golang.org/protobuf/proto"
)

func TestParallelFlowTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestParallelFlow(t, service.BackendTypeTemporal, nil)
		smallWaitForFastTest()
		doTestParallelFlow(
			t,
			service.BackendTypeTemporal,
			minimumContinueAsNewAsyncDurabilityConfig(),
		)
		smallWaitForFastTest()
	}
}

func TestParallelFlowCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestParallelFlow(t, service.BackendTypeCadence, nil)
		smallWaitForFastTest()
		doTestParallelFlow(
			t,
			service.BackendTypeCadence,
			minimumContinueAsNewSyncDurabilityConfig(),
		)
		smallWaitForFastTest()
	}
}

func doTestParallelFlow(
	t *testing.T,
	backendType service.BackendType,
	flowConfig *dexpb.FlowConfig,
) {
	workerHandler := parallel.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowId := parallel.WorkflowType + "-" + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		FlowId:             flowId,
		FlowType:           parallel.WorkflowType,
		FlowTimeoutSeconds: 10,

		StartStepType: parallel.State1,
		FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{
			FlowConfigOverride: flowConfig,
		}, workerTarget),
	})
	require.NoError(t, err)

	response, err := flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId:          flowId,
		NeedsResults:    true,
		WaitTimeSeconds: 30,
	})
	require.NoError(t, err)

	history := workerHandler.GetTestResult().InvokeHistory
	require.Equalf(t, map[string]int64{
		"S1_waitFor":   1,
		"S1_execute":   1,
		"S11_waitFor":  1,
		"S11_execute":  1,
		"S12_waitFor":  1,
		"S12_execute":  1,
		"S13_waitFor":  1,
		"S13_execute":  1,
		"S111_waitFor": 1,
		"S111_execute": 1,
		"S112_waitFor": 1,
		"S112_execute": 1,
		"S121_waitFor": 1,
		"S121_execute": 1,
		"S122_waitFor": 1,
		"S122_execute": 1,
	}, history, "parallel test fail, %v", history)

	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, response.GetFlowStatus())
	expectedResults := []*dexpb.StepCompletionOutput{
		{
			CompletedStepType:        parallel.State13,
			CompletedStepExecutionId: parallel.State13 + "-1",
			CompletedStepOutput: encodedObjectValue(
				"json",
				[]byte("from "+parallel.State13),
			),
		},
		{
			CompletedStepType:        parallel.State111,
			CompletedStepExecutionId: parallel.State111 + "-1",
			CompletedStepOutput: encodedObjectValue(
				"json",
				[]byte("from "+parallel.State111),
			),
		},
		{
			CompletedStepType:        parallel.State112,
			CompletedStepExecutionId: parallel.State112 + "-1",
			CompletedStepOutput: encodedObjectValue(
				"json",
				[]byte("from "+parallel.State112),
			),
		},
		{
			CompletedStepType:        parallel.State121,
			CompletedStepExecutionId: parallel.State121 + "-1",
			CompletedStepOutput: encodedObjectValue(
				"json",
				[]byte("from "+parallel.State121),
			),
		},
		{
			CompletedStepType:        parallel.State122,
			CompletedStepExecutionId: parallel.State122 + "-1",
			CompletedStepOutput: encodedObjectValue(
				"json",
				[]byte("from "+parallel.State122),
			),
		},
	}
	require.Len(t, response.GetResults(), len(expectedResults))
	for _, expected := range expectedResults {
		require.True(t, proto.Equal(expected, findParallelResult(response.GetResults(), expected)))
	}
}

func findParallelResult(
	results []*dexpb.StepCompletionOutput,
	expected *dexpb.StepCompletionOutput,
) *dexpb.StepCompletionOutput {
	for _, result := range results {
		if result.GetCompletedStepType() == expected.GetCompletedStepType() &&
			result.GetCompletedStepExecutionId() == expected.GetCompletedStepExecutionId() {
			return result
		}
	}
	panic(fmt.Sprintf("missing result for step %s", expected.GetCompletedStepType()))
}
