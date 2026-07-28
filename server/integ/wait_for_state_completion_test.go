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
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/iwf/gen/iwfpb"
	"github.com/superdurable/iwf/integ/workflow/wait_for_state_completion"
	"github.com/superdurable/iwf/service"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestWaitForStateCompletionTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestWaitForStateCompletion(t, service.BackendTypeTemporal, false)
		smallWaitForFastTest()
		doTestWaitForStateCompletion(t, service.BackendTypeTemporal, true)
		smallWaitForFastTest()
	}
}

func TestWaitForStateCompletionCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestWaitForStateCompletion(t, service.BackendTypeCadence, false)
		smallWaitForFastTest()
		doTestWaitForStateCompletion(t, service.BackendTypeCadence, true)
		smallWaitForFastTest()
	}
}

func doTestWaitForStateCompletion(
	t *testing.T,
	backendType service.BackendType,
	waitByStepType bool,
) {
	workerHandler := wait_for_state_completion.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startIwfService(t, IwfServiceTestConfig{BackendType: backendType})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowId := wait_for_state_completion.WorkflowType + uuid.NewString()
	nowTimestamp := time.Now().Unix()
	_, err := flowClient.StartFlow(ctx, &iwfpb.StartFlowRequest{
		FlowId:             flowId,
		FlowType:           wait_for_state_completion.WorkflowType,
		FlowTimeoutSeconds: 30,
		WorkerTarget:       workerTarget,
		StartStepType:      wait_for_state_completion.State1,
		StepInput:          stringValue(strconv.FormatInt(nowTimestamp, 10)),
	})
	require.NoError(t, err)

	assertions := assert.New(t)

	if backendType == service.BackendTypeCadence {
		_, err = flowClient.WaitForStepCompletion(ctx, &iwfpb.WaitForStepCompletionRequest{
			FlowId:              flowId,
			StepType:            wait_for_state_completion.State2,
			StepExecutionNumber: "1",
			WaitTimeSeconds:     30,
		})
		require.Equal(t, codes.Unimplemented, status.Code(err))
	} else if waitByStepType {
		_, err = flowClient.WaitForStepCompletion(ctx, &iwfpb.WaitForStepCompletionRequest{
			FlowId:              flowId,
			StepType:            wait_for_state_completion.State2,
			StepExecutionNumber: "1",
			WaitTimeSeconds:     30,
		})
		require.NoError(t, err)
	} else {
		_, err = flowClient.WaitForStepCompletion(ctx, &iwfpb.WaitForStepCompletionRequest{
			FlowId:              flowId,
			StepType:            wait_for_state_completion.State1,
			StepExecutionNumber: "1",
			WaitTimeSeconds:     30,
		})
		require.NoError(t, err)
	}

	_, err = flowClient.WaitForFlow(ctx, &iwfpb.WaitForFlowRequest{
		FlowId: flowId,
	})
	require.NoError(t, err)

	result := workerHandler.GetTestResult()
	history := result.InvokeHistory
	data := result.InvokeData
	assertions.Equalf(map[string]int64{
		"S1_waitFor": 1,
		"S1_execute": 1,
		"S2_waitFor": 1,
		"S2_execute": 1,
	}, history, "wait for step completion test fail, %v", history)
	duration := data["fired_at"].(int64) - data["scheduled_at"].(int64)
	assertions.Equal("timer-cmd-id", data["timer_id"])
	assertions.True(duration >= 9 && duration <= 11, duration)
}
