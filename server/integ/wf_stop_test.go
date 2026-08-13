// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package integ

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/integ/workflow/signal"
	"github.com/superdurable/dex/service"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestWorkflowCanceledTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		// Cancel must wait for an in-flight Execute producer before closing.
		doTestWorkflowCancelWaitsForProducer(t, service.BackendTypeTemporal, nil)
		smallWaitForFastTest()
		doTestWorkflowCancelWaitsForProducer(
			t,
			service.BackendTypeTemporal,
			minimumContinueAsNewSyncDurabilityConfig(),
		)
		smallWaitForFastTest()
		// Cancel and Fail must wake a step suspended on a channel.
		doTestWorkflowStopWhileSuspended(t, service.BackendTypeTemporal, nil)
		smallWaitForFastTest()
		doTestWorkflowStopWhileSuspended(
			t,
			service.BackendTypeTemporal,
			minimumContinueAsNewSyncDurabilityConfig(),
		)
		smallWaitForFastTest()

		doTestWorkflowTerminated(t, service.BackendTypeTemporal, nil)
		smallWaitForFastTest()
		doTestWorkflowTerminated(t, service.BackendTypeTemporal, minimumContinueAsNewSyncDurabilityConfig())
		smallWaitForFastTest()

		doTestWorkflowFail(t, service.BackendTypeTemporal, nil)
		smallWaitForFastTest()
		doTestWorkflowFail(t, service.BackendTypeTemporal, minimumContinueAsNewSyncDurabilityConfig())
		smallWaitForFastTest()
	}
}

func TestInvokeRPCTerminalValidationTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for _, stopType := range []dexpb.StopType{
		dexpb.StopType_STOP_TYPE_CANCEL,
		dexpb.StopType_STOP_TYPE_FAIL,
	} {
		t.Run(stopType.String(), func(t *testing.T) {
			doTestInvokeRPCTerminalValidation(t, stopType)
		})
	}
}

func TestWorkflowCanceledCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		// Cancel must wait for an in-flight Execute producer before closing.
		doTestWorkflowCancelWaitsForProducer(t, service.BackendTypeCadence, nil)
		smallWaitForFastTest()
		doTestWorkflowCancelWaitsForProducer(
			t,
			service.BackendTypeCadence,
			minimumContinueAsNewSyncDurabilityConfig(),
		)
		smallWaitForFastTest()
		// Cancel and Fail must wake a step suspended on a channel.
		doTestWorkflowStopWhileSuspended(t, service.BackendTypeCadence, nil)
		smallWaitForFastTest()
		doTestWorkflowStopWhileSuspended(
			t,
			service.BackendTypeCadence,
			minimumContinueAsNewSyncDurabilityConfig(),
		)
		smallWaitForFastTest()

		doTestWorkflowTerminated(t, service.BackendTypeCadence, nil)
		smallWaitForFastTest()
		doTestWorkflowTerminated(t, service.BackendTypeCadence, minimumContinueAsNewSyncDurabilityConfig())
		smallWaitForFastTest()

		doTestWorkflowFail(t, service.BackendTypeCadence, nil)
		smallWaitForFastTest()
		doTestWorkflowFail(t, service.BackendTypeCadence, minimumContinueAsNewSyncDurabilityConfig())
		smallWaitForFastTest()
	}
}

func doTestWorkflowCancelWaitsForProducer(
	t *testing.T,
	backendType service.BackendType,
	flowConfig *dexpb.FlowConfig,
) {
	workerHandler := newCooperativeStopHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowID := "wf-cooperative-cancel-test-" + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           cooperativeStopFlowType,
		FlowTimeoutSeconds: 20,
		StartStepType:      cooperativeStopStepType,
		FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{
			FlowConfigOverride: flowConfig,
		}, workerTarget),
	})
	require.NoError(t, err)

	select {
	case <-workerHandler.executeStarted:
	case <-ctx.Done():
		require.FailNow(t, "producer did not start", ctx.Err())
	}
	_, err = flowClient.StopFlow(ctx, &dexpb.StopFlowRequest{
		FlowId:   flowID,
		StopType: dexpb.StopType_STOP_TYPE_CANCEL,
	})
	require.NoError(t, err)
	close(workerHandler.releaseExecute)

	response, err := flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowID})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_CANCELED, response.GetFlowStatus())
	require.Equal(t, dexpb.FlowErrorType_FLOW_ERROR_TYPE_UNSPECIFIED, response.GetErrorType())
	require.Empty(t, response.GetErrorMessage())
}

func doTestInvokeRPCTerminalValidation(t *testing.T, stopType dexpb.StopType) {
	handler := newFinalizingRPCHandler(false)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: service.BackendTypeTemporal})
	workerTarget := startWorker(t, handler)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	flowID := terminalRPCFlowType + "-" + uuid.NewString()
	startResponse, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           terminalRPCFlowType,
		FlowTimeoutSeconds: 20,
		FlowStartOptions:   withWorkerTarget(nil, workerTarget),
	})
	require.NoError(t, err)
	_, err = runtime.FlowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
		RequestId: newRequestID(), FlowId: flowID, RpcName: terminalRPCAccepted,
	})
	require.NoError(t, err)
	waitRequestID := newRequestID()
	waitResult := make(chan error, 1)
	go func() {
		_, waitErr := runtime.FlowClient.WaitForAttribute(ctx, &dexpb.WaitForAttributeRequest{
			FlowId: flowID,
			Condition: waitForAttributeEqualCondition(
				terminalRPCReleaseAttribute,
				stringValue("release"),
			),
			WaitTimeSeconds: 20,
			RequestId:       waitRequestID,
		})
		waitResult <- waitErr
	}()
	require.Eventually(t, func() bool {
		accepted, _ := countTemporalUpdateEvents(
			t, ctx, runtime, flowID, startResponse.GetRunId(), waitRequestID,
		)
		return accepted == 1
	}, 5*time.Second, 20*time.Millisecond)
	_, err = runtime.FlowClient.StopFlow(ctx, &dexpb.StopFlowRequest{
		FlowId: flowID, StopType: stopType, Reason: "terminal validation",
	})
	require.NoError(t, err)
	assertRPCRejectedDuringFinalization(t, ctx, runtime.FlowClient, flowID, handler)
	_, err = runtime.FlowClient.SetAttributes(ctx, &dexpb.SetAttributesRequest{
		RequestId: newRequestID(),
		FlowId:    flowID,
		Attributes: []*dexpb.AttributeWrite{{
			Key: terminalRPCReleaseAttribute, Value: stringValue("release"),
		}},
	})
	require.NoError(t, err)
	require.NoError(t, <-waitResult)
	response, err := runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowID})
	require.NoError(t, err)
	if stopType == dexpb.StopType_STOP_TYPE_CANCEL {
		require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_CANCELED, response.GetFlowStatus())
	} else {
		require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_FAILED, response.GetFlowStatus())
	}
	assertAcceptedRPCResultInHistory(
		t, ctx, runtime.FlowClient, flowID, startResponse.GetRunId(),
	)
}

func assertRPCRejectedDuringFinalization(
	t *testing.T,
	ctx context.Context,
	flowClient dexpb.FlowServiceClient,
	flowID string,
	handler *finalizingRPCHandler,
) {
	t.Helper()
	require.Eventually(t, func() bool {
		_, err := flowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
			RequestId: newRequestID(), FlowId: flowID, RpcName: terminalRPCProbe,
		})
		return status.Code(err) == codes.FailedPrecondition
	}, 5*time.Second, 20*time.Millisecond)
	invokesBefore := handler.probeInvokeCount()
	_, err := flowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
		RequestId: newRequestID(), FlowId: flowID, RpcName: terminalRPCProbe,
	})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	require.Equal(t, invokesBefore, handler.probeInvokeCount())
}

func assertAcceptedRPCResultInHistory(
	t *testing.T,
	ctx context.Context,
	flowClient dexpb.FlowServiceClient,
	flowID string,
	runID string,
) {
	t.Helper()
	events, _ := getAllWebHistoryEvents(t, ctx, flowClient, flowID, runID)
	for _, event := range events {
		rpcEvent := event.GetRpcExecutionCompleted()
		if rpcEvent.GetRpcName() != terminalRPCAccepted {
			continue
		}
		require.Equal(t, terminalRPCAcceptedOutput, rpcEvent.GetOutput().GetStringValue())
		return
	}
	require.Fail(t, "accepted RPC history event is missing")
}

func doTestWorkflowStopWhileSuspended(
	t *testing.T,
	backendType service.BackendType,
	flowConfig *dexpb.FlowConfig,
) {
	workerHandler := newSuspendedStopHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	testCases := []struct {
		stopType dexpb.StopType
		status   dexpb.FlowStatus
	}{
		{stopType: dexpb.StopType_STOP_TYPE_CANCEL, status: dexpb.FlowStatus_FLOW_STATUS_CANCELED},
		{stopType: dexpb.StopType_STOP_TYPE_FAIL, status: dexpb.FlowStatus_FLOW_STATUS_FAILED},
	}
	for _, testCase := range testCases {
		flowID := "wf-suspended-stop-test-" + uuid.NewString()
		_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
			RequestId:          newRequestID(),
			FlowId:             flowID,
			FlowType:           suspendedStopFlowType,
			FlowTimeoutSeconds: 20,
			StartStepType:      suspendedStopStepType,
			FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{
				FlowConfigOverride: flowConfig,
			}, workerTarget),
		})
		require.NoError(t, err)

		select {
		case startedFlowID := <-workerHandler.waitForStarted:
			require.Equal(t, flowID, startedFlowID)
		case <-ctx.Done():
			require.FailNow(t, "suspended step did not start", ctx.Err())
		}
		_, err = flowClient.StopFlow(ctx, &dexpb.StopFlowRequest{
			FlowId:   flowID,
			StopType: testCase.stopType,
		})
		require.NoError(t, err)

		response, err := flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowID})
		require.NoError(t, err)
		require.Equal(t, testCase.status, response.GetFlowStatus())
	}
}

func doTestWorkflowTerminated(
	t *testing.T,
	backendType service.BackendType,
	flowConfig *dexpb.FlowConfig,
) {
	workerHandler := signal.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowId := "wf-cancel-test-" + uuid.NewString()
	startRequest := &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           signal.WorkflowType,
		FlowTimeoutSeconds: 10,

		FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{
			FlowConfigOverride: flowConfig,
		}, workerTarget),
	}
	_, err := flowClient.StartFlow(ctx, startRequest)
	require.NoError(t, err)

	_, err = flowClient.StartFlow(ctx, startRequest)
	require.Equal(t, codes.AlreadyExists, status.Code(err))

	startRequest.FlowStartOptions.IdReusePolicy =
		dexpb.IdReusePolicy_ID_REUSE_POLICY_ALLOW_TERMINATE_IF_RUNNING
	_, err = flowClient.StartFlow(ctx, startRequest)
	require.NoError(t, err)

	_, err = flowClient.StopFlow(ctx, &dexpb.StopFlowRequest{
		FlowId:   flowId,
		StopType: dexpb.StopType_STOP_TYPE_TERMINATE,
	})
	require.NoError(t, err)

	response, err := flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId: flowId,
	})
	require.NoError(t, err)

	assertions := assert.New(t)
	assertions.Equal(dexpb.FlowStatus_FLOW_STATUS_TERMINATED, response.GetFlowStatus())
	assertions.Equal(dexpb.FlowErrorType_FLOW_ERROR_TYPE_UNSPECIFIED, response.GetErrorType())
	assertions.Empty(response.GetErrorMessage())
}

func doTestWorkflowFail(
	t *testing.T,
	backendType service.BackendType,
	flowConfig *dexpb.FlowConfig,
) {
	workerHandler := signal.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowId := "wf-cancel-test-" + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           signal.WorkflowType,
		FlowTimeoutSeconds: 10,

		FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{
			FlowConfigOverride: flowConfig,
		}, workerTarget),
	})
	require.NoError(t, err)

	_, err = flowClient.StopFlow(ctx, &dexpb.StopFlowRequest{
		FlowId:   flowId,
		StopType: dexpb.StopType_STOP_TYPE_FAIL,
		Reason:   "fail reason",
	})
	require.NoError(t, err)

	response, err := flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId: flowId,
	})
	require.NoError(t, err)

	assertions := assert.New(t)
	assertions.Equal(dexpb.FlowStatus_FLOW_STATUS_FAILED, response.GetFlowStatus())
	assertions.Equal(
		dexpb.FlowErrorType_FLOW_ERROR_TYPE_CLIENT_API_FAILING_FLOW,
		response.GetErrorType(),
	)
	assertions.Equal("fail reason", response.GetErrorMessage())
}

const (
	cooperativeStopFlowType     = "cooperative-stop"
	cooperativeStopStepType     = "running-producer"
	suspendedStopFlowType       = "suspended-stop"
	suspendedStopStepType       = "suspended-step"
	suspendedStopChannel        = "never-published"
	terminalRPCFlowType         = "terminal-rpc"
	terminalRPCAccepted         = "accepted"
	terminalRPCProbe            = "probe"
	terminalRPCFinishStep       = "finish-step"
	terminalRPCReleaseAttribute = "release-finalization"
	terminalRPCAcceptedOutput   = "accepted-before-finalize"
)

type finalizingRPCHandler struct {
	dexpb.UnimplementedWorkerServiceServer
	finishExecuted     chan struct{}
	releaseFinish      chan struct{}
	finishExecutedOnce sync.Once
	blockFinish        bool
	mu                 sync.Mutex
	probeInvokes       int
}

func newFinalizingRPCHandler(blockFinish bool) *finalizingRPCHandler {
	return &finalizingRPCHandler{
		finishExecuted: make(chan struct{}),
		releaseFinish:  make(chan struct{}),
		blockFinish:    blockFinish,
	}
}

func (h *finalizingRPCHandler) InvokeWorkerRPC(
	ctx context.Context,
	request *dexpb.InvokeWorkerRPCRequest,
) (*dexpb.InvokeWorkerRPCResponse, error) {
	switch request.GetRpcName() {
	case terminalRPCAccepted:
		return &dexpb.InvokeWorkerRPCResponse{
			Output: &dexpb.Value{Kind: &dexpb.Value_StringValue{
				StringValue: terminalRPCAcceptedOutput,
			}},
		}, nil
	case terminalRPCProbe:
		h.mu.Lock()
		h.probeInvokes++
		h.mu.Unlock()
		return &dexpb.InvokeWorkerRPCResponse{}, nil
	default:
		return nil, status.Error(codes.InvalidArgument, "unknown terminal RPC")
	}
}

func (h *finalizingRPCHandler) InvokeWaitForMethod(
	context.Context,
	*dexpb.InvokeWaitForMethodRequest,
) (*dexpb.InvokeWaitForMethodResponse, error) {
	return &dexpb.InvokeWaitForMethodResponse{
		WaitingCondition: &dexpb.WaitingCondition{
			WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
		},
	}, nil
}

func (h *finalizingRPCHandler) InvokeExecuteMethod(
	ctx context.Context,
	request *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	switch request.GetStepType() {
	case terminalRPCFinishStep:
		h.finishExecutedOnce.Do(func() { close(h.finishExecuted) })
		if h.blockFinish {
			select {
			case <-h.releaseFinish:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return &dexpb.InvokeExecuteMethodResponse{
			StepDecision: &dexpb.StepDecision{
				CloseDecision: &dexpb.CloseDecision{
					CloseDecisionType: dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_GRACEFUL_COMPLETE,
				},
			},
		}, nil
	default:
		return nil, status.Error(codes.InvalidArgument, "unknown terminal step")
	}
}

func (h *finalizingRPCHandler) probeInvokeCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.probeInvokes
}

type cooperativeStopHandler struct {
	dexpb.UnimplementedWorkerServiceServer
	executeStartedOnce sync.Once
	executeStarted     chan struct{}
	releaseExecute     chan struct{}
}

func newCooperativeStopHandler() *cooperativeStopHandler {
	return &cooperativeStopHandler{
		executeStarted: make(chan struct{}),
		releaseExecute: make(chan struct{}),
	}
}

func (h *cooperativeStopHandler) InvokeWaitForMethod(
	context.Context,
	*dexpb.InvokeWaitForMethodRequest,
) (*dexpb.InvokeWaitForMethodResponse, error) {
	return &dexpb.InvokeWaitForMethodResponse{
		WaitingCondition: &dexpb.WaitingCondition{
			WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
		},
	}, nil
}

func (h *cooperativeStopHandler) InvokeExecuteMethod(
	ctx context.Context,
	_ *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	h.executeStartedOnce.Do(func() { close(h.executeStarted) })
	select {
	case <-h.releaseExecute:
		return &dexpb.InvokeExecuteMethodResponse{
			StepDecision: &dexpb.StepDecision{
				NextSteps: []*dexpb.StepMovement{{StepType: "must-not-start"}},
			},
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type suspendedStopHandler struct {
	dexpb.UnimplementedWorkerServiceServer
	waitForStarted chan string
}

func newSuspendedStopHandler() *suspendedStopHandler {
	return &suspendedStopHandler{waitForStarted: make(chan string, 2)}
}

func (h *suspendedStopHandler) InvokeWaitForMethod(
	_ context.Context,
	request *dexpb.InvokeWaitForMethodRequest,
) (*dexpb.InvokeWaitForMethodResponse, error) {
	h.waitForStarted <- request.GetContext().GetFlowId()
	return &dexpb.InvokeWaitForMethodResponse{
		WaitingCondition: &dexpb.WaitingCondition{
			WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
			ChannelConditions: []*dexpb.ChannelCondition{{
				ChannelName: suspendedStopChannel,
			}},
		},
	}, nil
}

func (h *suspendedStopHandler) InvokeExecuteMethod(
	context.Context,
	*dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	return nil, status.Error(codes.Internal, "suspended step should not execute")
}
