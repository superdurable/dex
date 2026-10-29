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
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/server-zd/integ/workflow/wait_for_state_completion"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestWaitForStateCompletionTemporal(t *testing.T) {
	for i := 0; i < *repeatIntegTest; i++ {
		doTestWaitForStateCompletion(t, false)
		smallWaitForFastTest()
		doTestWaitForStateCompletion(t, true)
		smallWaitForFastTest()
		doTestWaitForStateCompletionTimeout(t)
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
		// These cover Temporal Update handlers, Update counters, and Continue-As-New (C3).

		continue

	}
}

func doTestWaitForStateCompletion(
	t *testing.T,
	waitByStepType bool,
) {
	workerHandler := wait_for_state_completion.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, serverZDTestConfig{})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowId := wait_for_state_completion.WorkflowType + uuid.NewString()
	nowTimestamp := time.Now().Unix()
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           wait_for_state_completion.WorkflowType,
		FlowTimeoutSeconds: 30,

		StartStepType:    wait_for_state_completion.State1,
		StepInput:        stringValue(strconv.FormatInt(nowTimestamp, 10)),
		FlowStartOptions: withWorkerTarget(nil, workerTarget),
	})
	require.NoError(t, err)

	assertions := assert.New(t)

	if waitByStepType {
		_, err = flowClient.WaitForStepCompletion(ctx, &dexpb.WaitForStepCompletionRequest{
			FlowId:                flowId,
			StepType:              wait_for_state_completion.State2,
			StepExecutionNumber:   "1",
			RequestTimeoutSeconds: 30,
			RequestId:             uuid.NewString(),
		})
		require.NoError(t, err)
	} else {
		_, err = flowClient.WaitForStepCompletion(ctx, &dexpb.WaitForStepCompletionRequest{
			FlowId:                flowId,
			StepType:              wait_for_state_completion.State1,
			StepExecutionNumber:   "1",
			RequestTimeoutSeconds: 30,
			RequestId:             uuid.NewString(),
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
	runtime := startDexService(t, serverZDTestConfig{})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowId := wait_for_state_completion.WorkflowType + "-timeout-" + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           wait_for_state_completion.WorkflowType,
		FlowTimeoutSeconds: 30,

		StartStepType:    wait_for_state_completion.State1,
		StepInput:        stringValue(strconv.FormatInt(time.Now().Unix(), 10)),
		FlowStartOptions: withWorkerTarget(nil, workerTarget),
	})
	require.NoError(t, err)

	requestID := uuid.NewString()
	_, err = flowClient.WaitForStepCompletion(ctx, &dexpb.WaitForStepCompletionRequest{
		FlowId:                flowId,
		StepType:              wait_for_state_completion.State1,
		StepExecutionNumber:   "1",
		RequestTimeoutSeconds: 1,
		RequestId:             requestID,
	})
	require.Error(t, err)
	require.Equal(t, codes.DeadlineExceeded, status.Code(err))
	errResp := grpcServiceErrorResponse(t, err)
	require.Equal(
		t,
		dexpb.ErrorSubStatus_ERROR_SUB_STATUS_REQUEST_TIMEOUT,
		errResp.GetSubStatus(),
	)
	require.Equal(t, "step completion request timed out", errResp.GetDetail())

	_, err = flowClient.WaitForStepCompletion(ctx, &dexpb.WaitForStepCompletionRequest{
		FlowId:                flowId,
		StepType:              wait_for_state_completion.State1,
		StepExecutionNumber:   "1",
		RequestTimeoutSeconds: 15,
		RequestId:             requestID,
	})
	require.NoError(t, err)

	_, err = flowClient.StopFlow(ctx, &dexpb.StopFlowRequest{
		FlowId:   flowId,
		StopType: dexpb.StopType_STOP_TYPE_TERMINATE,
	})
	require.NoError(t, err)
}

func doTestWaitForStateCompletionCancel(t *testing.T) {
	workerHandler := wait_for_state_completion.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, serverZDTestConfig{})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowId := wait_for_state_completion.WorkflowType + "-cancel-" + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
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
			FlowId:                flowId,
			StepType:              wait_for_state_completion.State2,
			StepExecutionNumber:   "1",
			RequestTimeoutSeconds: 30,
			RequestId:             uuid.NewString(),
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
	runtime := startDexService(t, serverZDTestConfig{})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowId := wait_for_state_completion.WorkflowType + "-notfound-" + uuid.NewString()
	_, err := flowClient.WaitForStepCompletion(ctx, &dexpb.WaitForStepCompletionRequest{
		FlowId:                flowId,
		StepType:              wait_for_state_completion.State2,
		StepExecutionNumber:   "1",
		RequestTimeoutSeconds: 1,
		RequestId:             uuid.NewString(),
	})
	require.Error(t, err)
	require.Equal(t, codes.NotFound, status.Code(err))
	require.Equal(
		t,
		dexpb.ErrorSubStatus_ERROR_SUB_STATUS_FLOW_NOT_EXISTS,
		grpcServiceErrorResponse(t, err).GetSubStatus(),
	)
}

func doTestWaitForStateCompletionClosed(t *testing.T) {
	workerHandler := wait_for_state_completion.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, serverZDTestConfig{})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowId := wait_for_state_completion.WorkflowType + "-closed-" + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
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
		FlowId:                flowId,
		StepType:              wait_for_state_completion.State2,
		StepExecutionNumber:   "1",
		RequestTimeoutSeconds: 30,
		RequestId:             uuid.NewString(),
	})
	require.Error(t, err)
	require.Equal(t, codes.NotFound, status.Code(err))
}

func doTestWaitForStateCompletionConcurrent(t *testing.T) {
	workerHandler := wait_for_state_completion.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, serverZDTestConfig{

		MaxWaitSeconds: 60,
	})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	flowId := wait_for_state_completion.WorkflowType + "-concurrent-" + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           wait_for_state_completion.WorkflowType,
		FlowTimeoutSeconds: 60,

		StartStepType:    wait_for_state_completion.State1,
		StepInput:        stringValue(strconv.FormatInt(time.Now().Unix(), 10)),
		FlowStartOptions: withWorkerTarget(nil, workerTarget),
	})
	require.NoError(t, err)

	waitRequest := &dexpb.WaitForStepCompletionRequest{
		FlowId:                flowId,
		StepType:              wait_for_state_completion.State2,
		StepExecutionNumber:   "1",
		RequestTimeoutSeconds: 30,
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

	_, err = flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId: flowId,
	})
	require.NoError(t, err)
}

func doTestWaitForStateCompletionInvalidArgs(t *testing.T) {
	workerHandler := wait_for_state_completion.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, serverZDTestConfig{})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowId := wait_for_state_completion.WorkflowType + "-invalid-" + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           wait_for_state_completion.WorkflowType,
		FlowTimeoutSeconds: 30,

		StartStepType:    wait_for_state_completion.State1,
		StepInput:        stringValue(strconv.FormatInt(time.Now().Unix(), 10)),
		FlowStartOptions: withWorkerTarget(nil, workerTarget),
	})
	require.NoError(t, err)

	_, err = flowClient.WaitForStepCompletion(ctx, &dexpb.WaitForStepCompletionRequest{
		FlowId:                flowId,
		StepType:              "",
		StepExecutionNumber:   "1",
		RequestTimeoutSeconds: 1,
		RequestId:             uuid.NewString(),
	})
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	_, err = flowClient.WaitForStepCompletion(ctx, &dexpb.WaitForStepCompletionRequest{
		FlowId:                flowId,
		StepType:              wait_for_state_completion.State2,
		StepExecutionNumber:   "abc",
		RequestTimeoutSeconds: 1,
		RequestId:             uuid.NewString(),
	})
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	_, err = flowClient.WaitForStepCompletion(ctx, &dexpb.WaitForStepCompletionRequest{
		FlowId:                flowId,
		StepType:              wait_for_state_completion.State2,
		StepExecutionNumber:   "1",
		RequestTimeoutSeconds: -1,
		RequestId:             uuid.NewString(),
	})
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	_, err = flowClient.WaitForStepCompletion(ctx, &dexpb.WaitForStepCompletionRequest{
		FlowId:                        flowId,
		StepType:                      wait_for_state_completion.State2,
		StepExecutionNumber:           "1",
		InternalHandlerTimeoutSeconds: -1,
		RequestId:                     uuid.NewString(),
	})
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	_, err = flowClient.StopFlow(ctx, &dexpb.StopFlowRequest{
		FlowId:   flowId,
		StopType: dexpb.StopType_STOP_TYPE_TERMINATE,
	})
	require.NoError(t, err)
}
