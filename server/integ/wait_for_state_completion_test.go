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
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/integ/workflow/wait_for_state_completion"
	"github.com/superdurable/dex/service"
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
		doTestWaitForStateCompletionTimeout(t)
		smallWaitForFastTest()
		doTestWaitForStateCompletionAcrossContinueAsNew(t)
		smallWaitForFastTest()
		doTestWaitForStateCompletionCancel(t)
		smallWaitForFastTest()
		doTestWaitForStateCompletionNotFound(t)
		smallWaitForFastTest()
		doTestWaitForStateCompletionClosed(t)
		smallWaitForFastTest()
		doTestWaitForStateCompletionConcurrent(t)
		smallWaitForFastTest()
		doTestWaitForStateCompletionInvalidArgs(t)
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
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowId := wait_for_state_completion.WorkflowType + uuid.NewString()
	nowTimestamp := time.Now().Unix()
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		FlowId:             flowId,
		FlowType:           wait_for_state_completion.WorkflowType,
		FlowTimeoutSeconds: 30,

		StartStepType:    wait_for_state_completion.State1,
		StepInput:        stringValue(strconv.FormatInt(nowTimestamp, 10)),
		FlowStartOptions: withWorkerTarget(nil, workerTarget),
	})
	require.NoError(t, err)

	assertions := assert.New(t)

	if backendType == service.BackendTypeCadence {
		_, err = flowClient.WaitForStepCompletion(ctx, &dexpb.WaitForStepCompletionRequest{
			FlowId:              flowId,
			StepType:            wait_for_state_completion.State2,
			StepExecutionNumber: "1",
			WaitTimeSeconds:     30,
		})
		require.Equal(t, codes.Unimplemented, status.Code(err))
	} else if waitByStepType {
		_, err = flowClient.WaitForStepCompletion(ctx, &dexpb.WaitForStepCompletionRequest{
			FlowId:              flowId,
			StepType:            wait_for_state_completion.State2,
			StepExecutionNumber: "1",
			WaitTimeSeconds:     30,
			RequestId:           uuid.NewString(),
		})
		require.NoError(t, err)
	} else {
		_, err = flowClient.WaitForStepCompletion(ctx, &dexpb.WaitForStepCompletionRequest{
			FlowId:              flowId,
			StepType:            wait_for_state_completion.State1,
			StepExecutionNumber: "1",
			WaitTimeSeconds:     30,
			RequestId:           uuid.NewString(),
		})
		require.NoError(t, err)
	}

	_, err = flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
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

func doTestWaitForStateCompletionTimeout(t *testing.T) {
	workerHandler := wait_for_state_completion.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: service.BackendTypeTemporal})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowId := wait_for_state_completion.WorkflowType + "-timeout-" + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		FlowId:             flowId,
		FlowType:           wait_for_state_completion.WorkflowType,
		FlowTimeoutSeconds: 30,

		StartStepType:    wait_for_state_completion.State1,
		StepInput:        stringValue(strconv.FormatInt(time.Now().Unix(), 10)),
		FlowStartOptions: withWorkerTarget(nil, workerTarget),
	})
	require.NoError(t, err)

	_, err = flowClient.WaitForStepCompletion(ctx, &dexpb.WaitForStepCompletionRequest{
		FlowId:              flowId,
		StepType:            wait_for_state_completion.State1,
		StepExecutionNumber: "999",
		WaitTimeSeconds:     0,
		RequestId:           uuid.NewString(),
	})
	require.Error(t, err)
	require.Equal(t, codes.DeadlineExceeded, status.Code(err))
	errResp := grpcErrorResponse(t, err)
	require.Equal(
		t,
		dexpb.ErrorSubStatus_ERROR_SUB_STATUS_LONG_POLL_TIME_OUT,
		errResp.GetSubStatus(),
	)
	require.Equal(t, "step completion wait timed out", errResp.GetDetail())

	_, err = flowClient.StopFlow(ctx, &dexpb.StopFlowRequest{
		FlowId:   flowId,
		StopType: dexpb.StopType_STOP_TYPE_TERMINATE,
	})
	require.NoError(t, err)
}

func doTestWaitForStateCompletionAcrossContinueAsNew(t *testing.T) {
	workerHandler := wait_for_state_completion.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: service.BackendTypeTemporal})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowId := wait_for_state_completion.WorkflowType + "-can-" + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		FlowId:             flowId,
		FlowType:           wait_for_state_completion.WorkflowType,
		FlowTimeoutSeconds: 30,

		StartStepType: wait_for_state_completion.State1,
		StepInput:     stringValue(strconv.FormatInt(time.Now().Unix(), 10)),
		FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{
			FlowConfigOverride: minimumContinueAsNewSyncDurabilityConfig(),
		}, workerTarget),
	})
	require.NoError(t, err)

	_, err = flowClient.WaitForStepCompletion(ctx, &dexpb.WaitForStepCompletionRequest{
		FlowId:              flowId,
		StepType:            wait_for_state_completion.State2,
		StepExecutionNumber: "1",
		WaitTimeSeconds:     30,
		RequestId:           uuid.NewString(),
	})
	require.NoError(t, err)

	_, err = flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId: flowId,
	})
	require.NoError(t, err)
}

func doTestWaitForStateCompletionCancel(t *testing.T) {
	workerHandler := wait_for_state_completion.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: service.BackendTypeTemporal})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowId := wait_for_state_completion.WorkflowType + "-cancel-" + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		FlowId:             flowId,
		FlowType:           wait_for_state_completion.WorkflowType,
		FlowTimeoutSeconds: 30,

		StartStepType:    wait_for_state_completion.State1,
		StepInput:        stringValue(strconv.FormatInt(time.Now().Unix(), 10)),
		FlowStartOptions: withWorkerTarget(nil, workerTarget),
	})
	require.NoError(t, err)

	waitCtx, waitCancel := context.WithCancel(ctx)
	defer waitCancel()
	waitDone := make(chan error, 1)
	go func() {
		_, waitErr := flowClient.WaitForStepCompletion(waitCtx, &dexpb.WaitForStepCompletionRequest{
			FlowId:              flowId,
			StepType:            wait_for_state_completion.State2,
			StepExecutionNumber: "1",
			WaitTimeSeconds:     30,
			RequestId:           uuid.NewString(),
		})
		waitDone <- waitErr
	}()

	time.Sleep(200 * time.Millisecond)
	waitCancel()

	select {
	case waitErr := <-waitDone:
		require.Error(t, waitErr)
		require.Equal(t, codes.Canceled, status.Code(waitErr))
	case <-time.After(10 * time.Second):
		t.Fatal("WaitForStepCompletion did not return after cancel")
	}

	_, err = flowClient.StopFlow(ctx, &dexpb.StopFlowRequest{
		FlowId:   flowId,
		StopType: dexpb.StopType_STOP_TYPE_TERMINATE,
	})
	require.NoError(t, err)
}

func doTestWaitForStateCompletionNotFound(t *testing.T) {
	runtime := startDexService(t, DexServiceTestConfig{BackendType: service.BackendTypeTemporal})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowId := wait_for_state_completion.WorkflowType + "-notfound-" + uuid.NewString()
	_, err := flowClient.WaitForStepCompletion(ctx, &dexpb.WaitForStepCompletionRequest{
		FlowId:              flowId,
		StepType:            wait_for_state_completion.State2,
		StepExecutionNumber: "1",
		WaitTimeSeconds:     1,
		RequestId:           uuid.NewString(),
	})
	require.Error(t, err)
	require.Equal(t, codes.NotFound, status.Code(err))
	require.Equal(
		t,
		dexpb.ErrorSubStatus_ERROR_SUB_STATUS_FLOW_NOT_EXISTS,
		grpcErrorResponse(t, err).GetSubStatus(),
	)
}

func doTestWaitForStateCompletionClosed(t *testing.T) {
	workerHandler := wait_for_state_completion.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: service.BackendTypeTemporal})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowId := wait_for_state_completion.WorkflowType + "-closed-" + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		FlowId:             flowId,
		FlowType:           wait_for_state_completion.WorkflowType,
		FlowTimeoutSeconds: 30,

		StartStepType:    wait_for_state_completion.State1,
		StepInput:        stringValue(strconv.FormatInt(time.Now().Unix(), 10)),
		FlowStartOptions: withWorkerTarget(nil, workerTarget),
	})
	require.NoError(t, err)

	_, err = flowClient.StopFlow(ctx, &dexpb.StopFlowRequest{
		FlowId:   flowId,
		StopType: dexpb.StopType_STOP_TYPE_TERMINATE,
	})
	require.NoError(t, err)

	_, err = flowClient.WaitForStepCompletion(ctx, &dexpb.WaitForStepCompletionRequest{
		FlowId:              flowId,
		StepType:            wait_for_state_completion.State2,
		StepExecutionNumber: "1",
		WaitTimeSeconds:     30,
		RequestId:           uuid.NewString(),
	})
	require.Error(t, err)
	require.Equal(t, codes.NotFound, status.Code(err))
}

func doTestWaitForStateCompletionConcurrent(t *testing.T) {
	workerHandler := wait_for_state_completion.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: service.BackendTypeTemporal})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	flowId := wait_for_state_completion.WorkflowType + "-concurrent-" + uuid.NewString()
	startResponse, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		FlowId:             flowId,
		FlowType:           wait_for_state_completion.WorkflowType,
		FlowTimeoutSeconds: 60,

		StartStepType:    wait_for_state_completion.State1,
		StepInput:        stringValue(strconv.FormatInt(time.Now().Unix(), 10)),
		FlowStartOptions: withWorkerTarget(nil, workerTarget),
	})
	require.NoError(t, err)

	waitRequest := &dexpb.WaitForStepCompletionRequest{
		FlowId:              flowId,
		StepType:            wait_for_state_completion.State2,
		StepExecutionNumber: "1",
		WaitTimeSeconds:     30,
		RequestId:           uuid.NewString(),
	}

	var waitGroup sync.WaitGroup
	waitGroup.Add(2)
	waitErrors := make([]error, 2)
	for index := range waitErrors {
		go func(waitIndex int) {
			defer waitGroup.Done()
			_, waitErr := flowClient.WaitForStepCompletion(ctx, waitRequest)
			waitErrors[waitIndex] = waitErr
		}(index)
	}
	waitGroup.Wait()

	for waitIndex, waitErr := range waitErrors {
		require.NoError(t, waitErr, "waiter %d failed", waitIndex)
	}
	accepted, completed := countTemporalUpdateEvents(
		t,
		ctx,
		runtime,
		flowId,
		startResponse.GetRunId(),
		waitRequest.GetRequestId(),
	)
	require.Equal(t, 1, accepted)
	require.Equal(t, 1, completed)

	_, err = flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId: flowId,
	})
	require.NoError(t, err)
}

func doTestWaitForStateCompletionInvalidArgs(t *testing.T) {
	workerHandler := wait_for_state_completion.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: service.BackendTypeTemporal})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowId := wait_for_state_completion.WorkflowType + "-invalid-" + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		FlowId:             flowId,
		FlowType:           wait_for_state_completion.WorkflowType,
		FlowTimeoutSeconds: 30,

		StartStepType:    wait_for_state_completion.State1,
		StepInput:        stringValue(strconv.FormatInt(time.Now().Unix(), 10)),
		FlowStartOptions: withWorkerTarget(nil, workerTarget),
	})
	require.NoError(t, err)

	_, err = flowClient.WaitForStepCompletion(ctx, &dexpb.WaitForStepCompletionRequest{
		FlowId:              flowId,
		StepType:            wait_for_state_completion.State2,
		StepExecutionNumber: "1",
		WaitTimeSeconds:     1,
	})
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Equal(t, "request ID is required", grpcErrorResponse(t, err).GetDetail())

	_, err = flowClient.WaitForStepCompletion(ctx, &dexpb.WaitForStepCompletionRequest{
		FlowId:              flowId,
		StepType:            "",
		StepExecutionNumber: "1",
		WaitTimeSeconds:     1,
		RequestId:           uuid.NewString(),
	})
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	_, err = flowClient.WaitForStepCompletion(ctx, &dexpb.WaitForStepCompletionRequest{
		FlowId:              flowId,
		StepType:            wait_for_state_completion.State2,
		StepExecutionNumber: "abc",
		WaitTimeSeconds:     1,
		RequestId:           uuid.NewString(),
	})
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	_, err = flowClient.WaitForStepCompletion(ctx, &dexpb.WaitForStepCompletionRequest{
		FlowId:              flowId,
		StepType:            wait_for_state_completion.State2,
		StepExecutionNumber: "1",
		WaitTimeSeconds:     -1,
		RequestId:           uuid.NewString(),
	})
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	_, err = flowClient.StopFlow(ctx, &dexpb.StopFlowRequest{
		FlowId:   flowId,
		StopType: dexpb.StopType_STOP_TYPE_TERMINATE,
	})
	require.NoError(t, err)
}
