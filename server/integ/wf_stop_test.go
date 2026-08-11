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
		doTestWorkflowCanceled(t, service.BackendTypeTemporal, nil)
		smallWaitForFastTest()
		doTestWorkflowCanceled(t, service.BackendTypeTemporal, minimumContinueAsNewSyncDurabilityConfig())
		smallWaitForFastTest()
		doTestWorkflowCancelWaitsForProducer(t, service.BackendTypeTemporal)
		smallWaitForFastTest()
		doTestWorkflowStopWhileSuspended(t, service.BackendTypeTemporal)
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

func TestWorkflowCanceledCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestWorkflowCanceled(t, service.BackendTypeCadence, nil)
		smallWaitForFastTest()
		doTestWorkflowCanceled(t, service.BackendTypeCadence, minimumContinueAsNewSyncDurabilityConfig())
		smallWaitForFastTest()
		doTestWorkflowCancelWaitsForProducer(t, service.BackendTypeCadence)
		smallWaitForFastTest()
		doTestWorkflowStopWhileSuspended(t, service.BackendTypeCadence)
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

func doTestWorkflowCanceled(
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
		StopType: dexpb.StopType_STOP_TYPE_CANCEL,
	})
	require.NoError(t, err)

	response, err := flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId: flowId,
	})
	require.NoError(t, err)

	assertions := assert.New(t)
	assertions.Equal(dexpb.FlowStatus_FLOW_STATUS_CANCELED, response.GetFlowStatus())
	assertions.Equal(dexpb.FlowErrorType_FLOW_ERROR_TYPE_UNSPECIFIED, response.GetErrorType())
	assertions.Empty(response.GetErrorMessage())
}

func doTestWorkflowCancelWaitsForProducer(t *testing.T, backendType service.BackendType) {
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
		FlowStartOptions:   withWorkerTarget(&dexpb.FlowStartOptions{}, workerTarget),
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
}

func doTestWorkflowStopWhileSuspended(t *testing.T, backendType service.BackendType) {
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
			FlowStartOptions:   withWorkerTarget(&dexpb.FlowStartOptions{}, workerTarget),
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
	cooperativeStopFlowType = "cooperative-stop"
	cooperativeStopStepType = "running-producer"
	suspendedStopFlowType   = "suspended-stop"
	suspendedStopStepType   = "suspended-step"
	suspendedStopChannel    = "never-published"
)

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
