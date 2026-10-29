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
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/server-zd/integ/workflow/deadend"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestDeadEndFlowTemporal(t *testing.T) {
	for i := 0; i < *repeatIntegTest; i++ {
		doTestDeadEndFlow(t, nil)
		smallWaitForFastTest()
	}
}

func TestSynchronousUpdateRequestIDTemporal(t *testing.T) {
	for i := 0; i < *repeatIntegTest; i++ {
		doTestSynchronousUpdateRequestID(t)
		smallWaitForFastTest()
	}
}

func doTestSynchronousUpdateRequestID(t *testing.T) {
	workerHandler := deadend.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	// server-zd has no Temporal Update delivery; its RPCs still deduplicate by request ID.
	runtime := startDexService(t, serverZDTestConfig{})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowId := deadend.WorkflowType + "-request-id-" + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           deadend.WorkflowType,
		FlowTimeoutSeconds: 30,
		FlowStartOptions:   withWorkerTarget(nil, workerTarget),
	})
	require.NoError(t, err)

	_, err = flowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
		FlowId:  flowId,
		RpcName: deadend.RPCWriteData,
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Equal(t, "request ID is required", grpcServiceErrorResponse(t, err).GetDetail())
	require.Zero(t, workerHandler.GetRPCInvokes())
	// The debug dump is a Temporal query; server-zd applies the start before StartFlow returns.

	requestId := uuid.NewString()
	request := &dexpb.InvokeRPCRequest{
		FlowId:    flowId,
		RpcName:   deadend.RPCWriteData,
		RequestId: requestId,
		// Update delivery runs every RPC once per request ID; server-zd does so for transactional RPCs.
		IsTransactional: true,
	}
	var waitGroup sync.WaitGroup
	waitGroup.Add(2)
	requestErrors := make([]error, 2)
	responses := make([]*dexpb.InvokeRPCResponse, 2)
	for requestIndex := range requestErrors {
		go func() {
			defer waitGroup.Done()
			responses[requestIndex], requestErrors[requestIndex] = flowClient.InvokeRPC(ctx, request)
		}()
	}
	waitGroup.Wait()
	for _, requestErr := range requestErrors {
		require.NoError(t, requestErr)
	}
	require.Equal(t, responses[0], responses[1])
	concurrentInvokes := workerHandler.GetRPCInvokes()
	require.Equal(t, int32(1), concurrentInvokes)

	retryResponse, err := flowClient.InvokeRPC(ctx, request)
	require.NoError(t, err)
	require.Equal(t, responses[0], retryResponse)
	require.Equal(t, concurrentInvokes, workerHandler.GetRPCInvokes())

	request.RequestId = uuid.NewString()
	_, err = flowClient.InvokeRPC(ctx, request)
	require.NoError(t, err)
	require.Equal(t, concurrentInvokes+1, workerHandler.GetRPCInvokes())

	_, err = flowClient.StopFlow(ctx, &dexpb.StopFlowRequest{FlowId: flowId})
	require.NoError(t, err)
}

func doTestDeadEndFlow(
	t *testing.T,
	flowConfig *dexpb.FlowConfig,
) {
	workerHandler := deadend.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, serverZDTestConfig{})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	flowId := deadend.WorkflowType + "-" + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           deadend.WorkflowType,
		FlowTimeoutSeconds: 100,

		FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{
			FlowConfigOverride: flowConfig,
		}, workerTarget),
	})
	require.NoError(t, err)

	for range 3 {
		time.Sleep(2 * time.Second)
		_, err = flowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
			RequestId: newRequestID(),
			FlowId:    flowId,
			RpcName:   deadend.RPCWriteData,
		})
		require.NoError(t, err)
	}

	for range 3 {
		time.Sleep(2 * time.Second)
		_, err = flowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
			RequestId: newRequestID(),
			FlowId:    flowId,
			RpcName:   deadend.RPCTriggerState,
		})
		require.NoError(t, err)
	}

	time.Sleep(2 * time.Second)

	_, err = flowClient.StopFlow(ctx, &dexpb.StopFlowRequest{
		FlowId: flowId,
	})
	require.NoError(t, err)

	history := workerHandler.GetTestResult().InvokeHistory
	require.Equalf(t, map[string]int64{
		"S1_execute": 3,
	}, history, "deadend test fail, %v", history)
}
