// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package integ

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/integ/workflow/step_cancellation"
	"github.com/superdurable/dex/service"
	"github.com/superdurable/dex/service/common/ptr"
)

func TestStepCancellationTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	testStepCancellation(t, service.BackendTypeTemporal)
}

func TestStepCancellationCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	testStepCancellation(t, service.BackendTypeCadence)
}

func TestQueuedStepCancellationAcrossContinueAsNewTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	testQueuedStepCancellationAcrossContinueAsNew(t, service.BackendTypeTemporal)
}

func TestQueuedStepCancellationAcrossContinueAsNewCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	testQueuedStepCancellationAcrossContinueAsNew(t, service.BackendTypeCadence)
}

func TestLocalStepCancellationTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	testLocalStepCancellation(t, service.BackendTypeTemporal)
}

func TestLocalStepCancellationCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	testLocalStepCancellation(t, service.BackendTypeCadence)
}

func TestRpcStepCancellationTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	testRpcStepCancellation(t, service.BackendTypeTemporal, false)
}

func TestRpcStepCancellationSynchronousUpdateTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	testRpcStepCancellation(t, service.BackendTypeTemporal, true)
}

func TestRpcStepCancellationCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	testRpcStepCancellation(t, service.BackendTypeCadence, false)
}

func testStepCancellation(t *testing.T, backendType service.BackendType) {
	handler := step_cancellation.NewHandler()
	workerTarget := startWorker(t, handler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	flowID := step_cancellation.FlowType + "-" + uuid.NewString()
	start, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           step_cancellation.FlowType,
		FlowTimeoutSeconds: 30,
		StartStepType:      step_cancellation.Root,
		FlowStartOptions: withWorkerTarget(
			&dexpb.FlowStartOptions{},
			workerTarget,
		),
	})
	require.NoError(t, err)

	completed, err := runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId:          flowID,
		WaitTimeSeconds: 30,
		NeedsResults:    true,
	})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, completed.GetFlowStatus())

	for _, stepType := range []string{
		step_cancellation.GlobalWaitForMethod,
		step_cancellation.SiblingExecute,
		step_cancellation.GlobalExecute,
	} {
		require.Eventually(t, func() bool {
			return handler.IsCancellationObserved(stepType)
		}, 5*time.Second, 50*time.Millisecond)
	}
	require.Eventually(t, handler.HasNoHeartbeatLateReturn, 6*time.Second, 50*time.Millisecond)
	events, _ := getAllWebHistoryEvents(t, ctx, runtime.FlowClient, flowID, start.GetRunId())
	assertNoWaitHistory(t, events, step_cancellation.GlobalWaitForMethod+"-1")
	assertNoExecuteHistory(t, events,
		step_cancellation.SiblingExecute+"-1",
		step_cancellation.GlobalExecute+"-1",
		step_cancellation.GlobalNoHeartbeat+"-1",
		step_cancellation.CanceledRecovery+"-1",
	)

	var state *dexpb.GetFlowStateResponse
	require.Eventually(t, func() bool {
		queryCtx, queryCancel := context.WithTimeout(ctx, 2*time.Second)
		defer queryCancel()
		var stateErr error
		state, stateErr = runtime.FlowClient.GetFlowState(queryCtx, &dexpb.GetFlowStateRequest{
			FlowId: flowID,
			RunId:  start.GetRunId(),
		})
		return stateErr == nil
	}, 10*time.Second, 100*time.Millisecond)
	require.Empty(t, state.GetActiveStepExecutions())
	require.Empty(t, state.GetQueuedSteps())
	for _, attribute := range state.GetAttributes() {
		require.NotEqual(t, "canceled-write", attribute.GetKey())
	}
}

func testQueuedStepCancellationAcrossContinueAsNew(
	t *testing.T,
	backendType service.BackendType,
) {
	handler := step_cancellation.NewHandler()
	workerTarget := startWorker(t, handler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	flowID := step_cancellation.FlowType + "-queued-" + uuid.NewString()
	start, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           step_cancellation.FlowType,
		FlowTimeoutSeconds: 30,
		StartStepType:      step_cancellation.QueuedRoot,
		StepOptions: &dexpb.StepOptions{
			SkipWaitFor:               true,
			ExecuteDurabilityOverride: dexpb.StepDurability_STEP_DURABILITY_SYNC,
		},
		FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{
			FlowConfigOverride: &dexpb.FlowConfig{
				ContinueAsNewThreshold: ptr.Any(int32(2)),
			},
		}, workerTarget),
	})
	require.NoError(t, err)
	completed, err := runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId:          flowID,
		WaitTimeSeconds: 30,
	})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, completed.GetFlowStatus())
	require.False(t, handler.WasQueuedLoserExecuted())

	firstRunEvents, _ := getAllWebHistoryEvents(
		t,
		ctx,
		runtime.FlowClient,
		flowID,
		start.GetRunId(),
	)
	require.NotEmpty(t, continuedToRunID(firstRunEvents))
}

func testLocalStepCancellation(t *testing.T, backendType service.BackendType) {
	handler := step_cancellation.NewHandler()
	workerTarget := startWorker(t, handler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowID := step_cancellation.FlowType + "-local-" + uuid.NewString()
	start, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           step_cancellation.FlowType,
		FlowTimeoutSeconds: 20,
		StartStepType:      step_cancellation.LocalRoot,
		FlowStartOptions:   withWorkerTarget(nil, workerTarget),
	})
	require.NoError(t, err)
	completed, err := runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId:          flowID,
		WaitTimeSeconds: 15,
	})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, completed.GetFlowStatus())
	require.True(t, handler.IsCancellationObserved(step_cancellation.LocalLoser))
	events, _ := getAllWebHistoryEvents(t, ctx, runtime.FlowClient, flowID, start.GetRunId())
	assertNoExecuteHistory(t, events, step_cancellation.LocalLoser+"-1")
}

func testRpcStepCancellation(
	t *testing.T,
	backendType service.BackendType,
	useSynchronousUpdate bool,
) {
	handler := step_cancellation.NewHandler()
	workerTarget := startWorker(t, handler)
	runtime := startDexService(t, DexServiceTestConfig{
		BackendType:                            backendType,
		UseTemporalSynchronousUpdateForAllRPCs: useSynchronousUpdate,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowID := step_cancellation.FlowType + "-rpc-" + uuid.NewString()
	start, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           step_cancellation.FlowType,
		FlowTimeoutSeconds: 20,
		FlowStartOptions:   withWorkerTarget(nil, workerTarget),
	})
	require.NoError(t, err)
	invokeCancellationRpc(t, ctx, runtime.FlowClient, flowID, step_cancellation.RpcA, step_cancellation.RpcSpawn)
	invokeCancellationRpc(t, ctx, runtime.FlowClient, flowID, step_cancellation.RpcB, step_cancellation.RpcSpawn)
	require.Eventually(t, func() bool {
		state, stateErr := runtime.FlowClient.GetFlowState(ctx, &dexpb.GetFlowStateRequest{
			FlowId: flowID,
			RunId:  start.GetRunId(),
		})
		if stateErr != nil || len(state.GetActiveStepExecutions()) != 4 {
			return false
		}
		for _, execution := range state.GetActiveStepExecutions() {
			if execution.GetPhase() != dexpb.ActiveStepPhase_ACTIVE_STEP_PHASE_WAITING {
				return false
			}
		}
		return true
	}, 10*time.Second, 50*time.Millisecond)

	response := invokeCancellationRpc(
		t,
		ctx,
		runtime.FlowClient,
		flowID,
		step_cancellation.RpcA,
		step_cancellation.RpcCancel,
	)
	require.Equal(t, step_cancellation.RpcCancel, response.GetOutput().GetStringValue())
	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		state, stateErr := runtime.FlowClient.GetFlowState(ctx, &dexpb.GetFlowStateRequest{
			FlowId: flowID,
			RunId:  start.GetRunId(),
		})
		assert.NoError(collect, stateErr)
		active := make([]string, 0, len(state.GetActiveStepExecutions()))
		for _, execution := range state.GetActiveStepExecutions() {
			active = append(active, execution.GetStepExecutionId())
		}
		assert.ElementsMatch(collect, []string{
			step_cancellation.RpcSibling + "-2",
			step_cancellation.RpcFinal + "-1",
		}, active)
	}, 2*time.Second, 50*time.Millisecond)
	completed, err := runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId:          flowID,
		WaitTimeSeconds: 15,
	})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, completed.GetFlowStatus())
}

func invokeCancellationRpc(
	t *testing.T,
	ctx context.Context,
	flowClient dexpb.FlowServiceClient,
	flowID string,
	rpcName string,
	input string,
) *dexpb.InvokeRPCResponse {
	t.Helper()
	response, err := flowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
		RequestId: newRequestID(),
		FlowId:    flowID,
		RpcName:   rpcName,
		Input: &dexpb.Value{Kind: &dexpb.Value_StringValue{
			StringValue: input,
		}},
	})
	require.NoError(t, err)
	return response
}

func assertNoWaitHistory(
	t *testing.T,
	events []*dexpb.FlowHistoryEvent,
	stepExecutionID string,
) {
	t.Helper()
	for _, event := range events {
		var context *dexpb.StepMethodEventContext
		switch {
		case event.GetStepWaitForCompleted() != nil:
			context = event.GetStepWaitForCompleted().GetContext()
		case event.GetStepWaitForFailed() != nil:
			context = event.GetStepWaitForFailed().GetContext()
		case event.GetStepWaitForPending() != nil:
			context = event.GetStepWaitForPending().GetContext()
		}
		if context != nil {
			require.NotEqual(t, stepExecutionID, context.GetStepExecutionId())
		}
	}
}

func assertNoExecuteHistory(
	t *testing.T,
	events []*dexpb.FlowHistoryEvent,
	stepExecutionIDs ...string,
) {
	t.Helper()
	for _, event := range events {
		var context *dexpb.StepMethodEventContext
		switch {
		case event.GetStepExecuteCompleted() != nil:
			context = event.GetStepExecuteCompleted().GetContext()
		case event.GetStepExecuteFailed() != nil:
			context = event.GetStepExecuteFailed().GetContext()
		case event.GetStepExecutePending() != nil:
			context = event.GetStepExecutePending().GetContext()
		}
		if context == nil {
			continue
		}
		for _, stepExecutionID := range stepExecutionIDs {
			require.NotEqual(t, stepExecutionID, context.GetStepExecutionId())
		}
	}
}
