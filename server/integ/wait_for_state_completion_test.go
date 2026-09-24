// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Sustainable Use License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

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
	"github.com/superdurable/dex/integ/workflow/deadend"
	"github.com/superdurable/dex/integ/workflow/signal"
	"github.com/superdurable/dex/integ/workflow/wait_for_state_completion"
	"github.com/superdurable/dex/service"
	"github.com/superdurable/dex/service/common/ptr"
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
		doTestWaitForStateCompletionInternalHandlerTimeout(t)
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
		doTestWaitForStateCompletionCounterSuccess(t)
		smallWaitForFastTest()
		doTestWaitForStateCompletionCounterFailure(t)
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

	if backendType == service.BackendTypeCadence {
		_, err = flowClient.WaitForStepCompletion(ctx, &dexpb.WaitForStepCompletionRequest{
			FlowId:                flowId,
			StepType:              wait_for_state_completion.State2,
			StepExecutionNumber:   "1",
			RequestTimeoutSeconds: 30,
		})
		require.Equal(t, codes.Unimplemented, status.Code(err))
	} else if waitByStepType {
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
	runtime := startDexService(t, DexServiceTestConfig{BackendType: service.BackendTypeTemporal})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowId := wait_for_state_completion.WorkflowType + "-timeout-" + uuid.NewString()
	startResponse, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
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
	counts := inspectTemporalUpdateHistory(
		t,
		ctx,
		runtime,
		flowId,
		startResponse.GetRunId(),
		requestID,
	)
	require.Equal(t, 1, counts.accepted)
	require.Zero(t, counts.completed)
	require.Zero(t, counts.oneSecondTimerStarted)
	require.Zero(t, counts.oneSecondTimerCanceled)
	require.Eventually(t, func() bool {
		return !time.Now().Before(counts.acceptedAt.Add(time.Second))
	}, 2*time.Second, 10*time.Millisecond)

	_, err = flowClient.WaitForStepCompletion(ctx, &dexpb.WaitForStepCompletionRequest{
		FlowId:                flowId,
		StepType:              wait_for_state_completion.State1,
		StepExecutionNumber:   "1",
		RequestTimeoutSeconds: 15,
		RequestId:             requestID,
	})
	require.NoError(t, err)
	counts = inspectTemporalUpdateHistory(
		t,
		ctx,
		runtime,
		flowId,
		startResponse.GetRunId(),
		requestID,
	)
	require.Equal(t, 1, counts.accepted)
	require.Equal(t, 1, counts.completed)
	require.Zero(t, counts.oneSecondTimerStarted)
	require.Zero(t, counts.oneSecondTimerCanceled)

	_, err = flowClient.StopFlow(ctx, &dexpb.StopFlowRequest{
		FlowId:   flowId,
		StopType: dexpb.StopType_STOP_TYPE_TERMINATE,
	})
	require.NoError(t, err)
}

func doTestWaitForStateCompletionInternalHandlerTimeout(t *testing.T) {
	workerHandler := wait_for_state_completion.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: service.BackendTypeTemporal})
	flowClient := runtime.FlowClient
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	flowID := wait_for_state_completion.WorkflowType + "-handler-timeout-" + uuid.NewString()
	startResponse, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           wait_for_state_completion.WorkflowType,
		FlowTimeoutSeconds: 20,
		StartStepType:      wait_for_state_completion.State1,
		StepInput:          stringValue(strconv.FormatInt(time.Now().Unix(), 10)),
		FlowStartOptions:   withWorkerTarget(nil, workerTarget),
	})
	require.NoError(t, err)
	requestID := uuid.NewString()
	_, err = flowClient.WaitForStepCompletion(ctx, &dexpb.WaitForStepCompletionRequest{
		FlowId:                        flowID,
		StepType:                      wait_for_state_completion.State1,
		StepExecutionNumber:           "1",
		RequestTimeoutSeconds:         15,
		InternalHandlerTimeoutSeconds: 1,
		RequestId:                     requestID,
	})
	require.NoError(t, err)
	baseAccepted, baseCompleted := countTemporalUpdateEvents(
		t,
		ctx,
		runtime,
		flowID,
		startResponse.GetRunId(),
		requestID,
	)
	nextAccepted, nextCompleted := countTemporalUpdateEvents(
		t,
		ctx,
		runtime,
		flowID,
		startResponse.GetRunId(),
		requestID+"-1",
	)
	require.Equal(t, 1, baseAccepted)
	require.Equal(t, 1, baseCompleted)
	require.Equal(t, 1, nextAccepted)
	require.Equal(t, 1, nextCompleted)
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
		RequestId:          newRequestID(),
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
		FlowId:                flowId,
		StepType:              wait_for_state_completion.State2,
		StepExecutionNumber:   "1",
		RequestTimeoutSeconds: 30,
		RequestId:             uuid.NewString(),
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
	runtime := startDexService(t, DexServiceTestConfig{BackendType: service.BackendTypeTemporal})
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
	runtime := startDexService(t, DexServiceTestConfig{BackendType: service.BackendTypeTemporal})
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
	runtime := startDexService(t, DexServiceTestConfig{
		BackendType:    service.BackendTypeTemporal,
		MaxWaitSeconds: 60,
	})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	flowId := wait_for_state_completion.WorkflowType + "-concurrent-" + uuid.NewString()
	startResponse, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
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
	accepted, completed := countTemporalUpdateEvents(
		t,
		ctx,
		runtime,
		flowId,
		startResponse.GetRunId(),
		"wait-for-step-completion:"+wait_for_state_completion.State2+"-1",
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

func doTestWaitForStateCompletionCounterSuccess(t *testing.T) {
	workerTarget := startWorker(t, deadend.NewHandler())
	runtime := startDexService(t, DexServiceTestConfig{BackendType: service.BackendTypeTemporal})
	flowClient := runtime.FlowClient
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowId := "wait-for-step-completion-counter-success-" + uuid.NewString()
	startResponse, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           deadend.WorkflowType,
		FlowTimeoutSeconds: 30,
		FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{
			FlowConfigOverride: &dexpb.FlowConfig{
				ContinueAsNewThreshold: ptr.Any(int32(3)),
			},
		}, workerTarget),
	})
	require.NoError(t, err)

	_, err = flowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
		RequestId: newRequestID(),
		FlowId:    flowId,
		RpcName:   deadend.RPCTriggerState,
	})
	require.NoError(t, err)
	_, err = flowClient.WaitForStepCompletion(ctx, &dexpb.WaitForStepCompletionRequest{
		FlowId:              flowId,
		StepType:            deadend.State1,
		StepExecutionNumber: "1",
		RequestId:           uuid.NewString(),
	})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		description, describeErr := runtime.UnifiedClient.DescribeWorkflowExecution(ctx, flowId, "", nil)
		return describeErr == nil && description.RunId != startResponse.GetRunId()
	}, 5*time.Second, 50*time.Millisecond)
	_, err = flowClient.StopFlow(ctx, &dexpb.StopFlowRequest{
		FlowId:   flowId,
		StopType: dexpb.StopType_STOP_TYPE_TERMINATE,
	})
	require.NoError(t, err)
}

func doTestWaitForStateCompletionCounterFailure(t *testing.T) {
	workerTarget := startWorker(t, signal.NewHandler())
	runtime := startDexService(t, DexServiceTestConfig{BackendType: service.BackendTypeTemporal})
	flowClient := runtime.FlowClient
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	flowConfig := syncDurabilityConfig()
	flowConfig.ContinueAsNewThreshold = ptr.Any(int32(2))

	flowId := startParkedWaitForAttributeFlow(
		t,
		ctx,
		flowClient,
		workerTarget,
		flowConfig,
	)
	description, err := runtime.UnifiedClient.DescribeWorkflowExecution(ctx, flowId, "", nil)
	require.NoError(t, err)
	requestID := uuid.NewString()
	timeoutDone := make(chan error, 1)
	go func() {
		var timeoutResponse dexpb.WaitForStepCompletionResponse
		timeoutDone <- runtime.UnifiedClient.SynchronousUpdateWorkflow(
			ctx,
			&timeoutResponse,
			flowId,
			"",
			requestID,
			service.WaitForStepCompletionUpdateType,
			&dexpb.WaitForStepCompletionRequest{
				FlowId:                        flowId,
				StepType:                      signal.State2,
				StepExecutionNumber:           "999",
				InternalHandlerTimeoutSeconds: 1,
			},
		)
	}()
	require.Eventually(t, func() bool {
		counts := inspectTemporalUpdateHistory(
			t,
			ctx,
			runtime,
			flowId,
			description.RunId,
			requestID,
		)
		return counts.accepted == 1
	}, 5*time.Second, 50*time.Millisecond)
	acceptedCounts := inspectTemporalUpdateHistory(
		t,
		ctx,
		runtime,
		flowId,
		description.RunId,
		requestID,
	)
	require.Eventually(t, func() bool {
		return !time.Now().Before(acceptedCounts.acceptedAt.Add(time.Second))
	}, 2*time.Second, 10*time.Millisecond)

	validationRequestID := uuid.NewString()
	var rejectedResponse dexpb.WaitForAttributeResponse
	err = runtime.UnifiedClient.SynchronousUpdateWorkflow(
		ctx,
		&rejectedResponse,
		flowId,
		"",
		validationRequestID,
		service.WaitForAttributeUpdateType,
		&dexpb.WaitForAttributeRequest{FlowId: flowId},
	)
	require.Error(t, err)
	select {
	case timeoutErr := <-timeoutDone:
		require.Error(t, timeoutErr)
	case <-ctx.Done():
		require.Fail(t, "timed out waiting for internal handler timeout")
	}
	rejectedCounts := inspectTemporalUpdateHistory(
		t,
		ctx,
		runtime,
		flowId,
		description.RunId,
		validationRequestID,
	)
	require.Zero(t, rejectedCounts.accepted)
	require.Zero(t, rejectedCounts.completed)
	_, err = flowClient.PublishToChannel(ctx, &dexpb.PublishToChannelRequest{
		FlowId: flowId,
		Messages: []*dexpb.ChannelMessage{
			{ChannelName: signal.UnhandledSignalName, Value: stringValue("wake")},
		},
	})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		counts := inspectTemporalUpdateHistory(
			t,
			ctx,
			runtime,
			flowId,
			description.RunId,
			requestID,
		)
		return counts.completed == 1
	}, 5*time.Second, 50*time.Millisecond)
	require.Eventually(t, func() bool {
		current, describeErr := runtime.UnifiedClient.DescribeWorkflowExecution(ctx, flowId, "", nil)
		return describeErr == nil && current.RunId != description.RunId
	}, 5*time.Second, 50*time.Millisecond)
	stopParkedWaitForAttributeFlow(t, ctx, flowClient, flowId)
}
