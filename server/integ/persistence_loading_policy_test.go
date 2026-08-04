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
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/integ/workflow/persistence"
	"github.com/superdurable/dex/integ/workflow/persistence_loading_policy"
	"github.com/superdurable/dex/service"
)

func TestPersistenceLoadingPolicy(t *testing.T) {
	for _, backendType := range getBackendTypes() {
		for i := 0; i < *repeatIntegTest; i++ {
			doTestPersistenceLoadingPolicy(t, backendType, false)
			smallWaitForFastTest()
			doTestPersistenceLoadingPolicy(t, backendType, true)
			smallWaitForFastTest()
		}
	}
}

func doTestPersistenceLoadingPolicy(
	t *testing.T,
	backendType service.BackendType,
	useLockingRPC bool,
) {
	workerHandler := persistence_loading_policy.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, DexServiceTestConfig{
		BackendType: backendType,
	})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowId := persistence_loading_policy.WorkflowType + uuid.NewString()
	flowInput := objJSONValue(`"ALL_WITHOUT_LOCKING"`)

	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           persistence_loading_policy.WorkflowType,
		FlowTimeoutSeconds: 10,

		StartStepType: persistence_loading_policy.State1,
		StepInput:     flowInput,
		StepOptions: &dexpb.StepOptions{
			SkipWaitFor: true,
		},
		FlowStartOptions: withWorkerTarget(nil, workerTarget),
	})
	require.NoError(t, err)

	time.Sleep(2 * time.Second)

	rpcRequest := &dexpb.InvokeRPCRequest{
		RequestId:      newRequestID(),
		FlowId:         flowId,
		RpcName:        persistence_loading_policy.WorkflowType + "_rpc",
		Input:          flowInput,
		TimeoutSeconds: 3,
	}
	if useLockingRPC {
		rpcRequest.LockAttributeKeys = []string{
			persistence.TestSearchAttributeTextKey,
			"da_2",
		}
		rpcRequest.RequestId = uuid.NewString()
	}

	_, err = flowClient.InvokeRPC(ctx, rpcRequest)
	if useLockingRPC && backendType == service.BackendTypeCadence {
		require.Error(t, err)
		return
	}
	require.NoError(t, err)

	_, err = flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId: flowId,
	})
	require.NoError(t, err)

	history := workerHandler.GetTestResult().InvokeHistory
	require.Equal(t, map[string]int64{
		"S1_execute": 1,
		"rpc":        1,
		"S2_waitFor": 1,
		"S2_execute": 1,
	}, history)
}
