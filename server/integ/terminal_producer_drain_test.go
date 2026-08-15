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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	terminalProducerDrainFlowType    = "terminal-producer-drain"
	terminalProducerDrainStepType    = "blocking-execute"
	terminalProducerDrainProbeRPC    = "terminal-producer-drain-probe"
	terminalProducerDrainFailureText = "stop while Execute is active"
)

type terminalProducerDrainHandler struct {
	dexpb.UnimplementedWorkerServiceServer
	executeStartedOnce sync.Once
	executeStarted     chan struct{}
}

func newTerminalProducerDrainHandler() *terminalProducerDrainHandler {
	return &terminalProducerDrainHandler{executeStarted: make(chan struct{})}
}

func TestTerminalProducerDrainTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	testTerminalProducerDrain(t, service.BackendTypeTemporal)
}

func TestTerminalProducerDrainCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	testTerminalProducerDrain(t, service.BackendTypeCadence)
}

func testTerminalProducerDrain(t *testing.T, backendType service.BackendType) {
	handler := newTerminalProducerDrainHandler()
	workerTarget := startWorker(t, handler)
	runtime := startDexService(t, DexServiceTestConfig{
		BackendType:                            backendType,
		UseTemporalSynchronousUpdateForAllRPCs: true,
	})
	flowID := terminalProducerDrainFlowType + "-" + uuid.NewString()
	cleanupTerminalProducerDrainFlow(t, runtime.FlowClient, flowID)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           terminalProducerDrainFlowType,
		FlowTimeoutSeconds: 30,
		StartStepType:      terminalProducerDrainStepType,
		StepOptions:        &dexpb.StepOptions{SkipWaitFor: true},
		FlowStartOptions:   withWorkerTarget(nil, workerTarget),
	})
	require.NoError(t, err)

	select {
	case <-handler.executeStarted:
	case <-ctx.Done():
		require.FailNow(t, "Execute did not start", ctx.Err())
	}
	_, err = runtime.FlowClient.StopFlow(ctx, &dexpb.StopFlowRequest{
		FlowId:   flowID,
		StopType: dexpb.StopType_STOP_TYPE_FAIL,
		Reason:   terminalProducerDrainFailureText,
	})
	require.NoError(t, err)

	result, waitErr := runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId:          flowID,
		WaitTimeSeconds: 5,
	})
	if waitErr != nil {
		confirmTerminalFinalizationStarted(t, ctx, runtime.FlowClient, flowID, backendType)
		t.Fatalf(
			"Flow remained running because an active Execute Step blocked terminal producer drain: %v",
			waitErr,
		)
	}
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_FAILED, result.GetFlowStatus())
	require.Equal(
		t,
		dexpb.FlowErrorType_FLOW_ERROR_TYPE_CLIENT_API_FAILING_FLOW,
		result.GetErrorType(),
	)
	require.Equal(t, terminalProducerDrainFailureText, result.GetErrorMessage())
}

func cleanupTerminalProducerDrainFlow(
	t *testing.T,
	flowClient dexpb.FlowServiceClient,
	flowID string,
) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := flowClient.StopFlow(ctx, &dexpb.StopFlowRequest{
			FlowId:   flowID,
			StopType: dexpb.StopType_STOP_TYPE_TERMINATE,
		})
		if err != nil && status.Code(err) != codes.NotFound {
			t.Logf("terminal producer drain cleanup failed: %v", err)
		}
	})
}

func confirmTerminalFinalizationStarted(
	t *testing.T,
	ctx context.Context,
	flowClient dexpb.FlowServiceClient,
	flowID string,
	backendType service.BackendType,
) {
	t.Helper()
	if backendType != service.BackendTypeTemporal {
		return
	}
	require.Eventually(t, func() bool {
		_, err := flowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
			RequestId: newRequestID(),
			FlowId:    flowID,
			RpcName:   terminalProducerDrainProbeRPC,
		})
		return status.Code(err) == codes.FailedPrecondition &&
			strings.Contains(err.Error(), "flow terminal cleanup is in progress")
	}, 5*time.Second, 20*time.Millisecond)
}

func (h *terminalProducerDrainHandler) InvokeWorkerRPC(
	_ context.Context,
	request *dexpb.InvokeWorkerRPCRequest,
) (*dexpb.InvokeWorkerRPCResponse, error) {
	if request.GetRpcName() != terminalProducerDrainProbeRPC {
		return nil, status.Errorf(codes.InvalidArgument, "unexpected RPC name %q", request.GetRpcName())
	}
	return &dexpb.InvokeWorkerRPCResponse{}, nil
}

func (h *terminalProducerDrainHandler) InvokeExecuteMethod(
	ctx context.Context,
	_ *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	h.executeStartedOnce.Do(func() { close(h.executeStarted) })
	<-ctx.Done()
	return nil, status.Error(codes.Canceled, "Execute canceled")
}
