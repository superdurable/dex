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
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/server-zd/integ/workflow/rpc"
)

func TestCreateFlowTemporal(t *testing.T) {
	for i := 0; i < *repeatIntegTest; i++ {
		doTestCreateWithoutStartingStep(t, nil)
		smallWaitForFastTest()
	}
}

func TestCreateFlowTemporalContinueAsNew(t *testing.T) {
	for i := 0; i < *repeatIntegTest; i++ {
		doTestCreateWithoutStartingStep(
			t,

			minimumContinueAsNewAsyncDurabilityConfig(),
		)
		smallWaitForFastTest()
	}
}

func doTestCreateWithoutStartingStep(
	t *testing.T,
	flowConfig *dexpb.FlowConfig,
) {
	workerHandler := rpc.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, serverZDTestConfig{})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowId := rpc.WorkflowType + "-" + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           rpc.WorkflowType,
		FlowTimeoutSeconds: 10,

		FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{
			FlowConfigOverride: flowConfig,
		}, workerTarget),
	})
	require.NoError(t, err)

	// The debug dump is a Temporal workflow query; server-zd has no equivalent.

	_, err = flowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
		RequestId:      newRequestID(),
		FlowId:         flowId,
		RpcName:        rpc.RPCName,
		Input:          rpc.TestInput,
		TimeoutSeconds: 2,
	})
	require.NoError(t, err)

	respWait, err := flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId:          flowId,
		WaitTimeSeconds: 20,
	})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, respWait.GetFlowStatus())

	history := workerHandler.GetTestResult().InvokeHistory
	require.Equalf(t, map[string]int64{
		"S2_waitFor": 1,
		"S2_execute": 1,
	}, history, "create test fail, %v", history)
}
