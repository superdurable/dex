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
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/iwf/gen/iwfpb"
	"github.com/superdurable/iwf/integ/workflow/persistence"
	"github.com/superdurable/iwf/integ/workflow/persistence_loading_policy"
	"github.com/superdurable/iwf/service"
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
	runtime := startIwfService(t, IwfServiceTestConfig{
		BackendType: backendType,
	})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowId := persistence_loading_policy.WorkflowType + uuid.NewString()
	flowInput := objJSONValue(`"ALL_WITHOUT_LOCKING"`)

	_, err := flowClient.StartFlow(ctx, &iwfpb.StartFlowRequest{
		FlowId:             flowId,
		FlowType:           persistence_loading_policy.WorkflowType,
		FlowTimeoutSeconds: 10,
		WorkerTarget:       workerTarget,
		StartStepType:      persistence_loading_policy.State1,
		StepInput:          flowInput,
		StepOptions: &iwfpb.StepOptions{
			SkipWaitFor: true,
		},
	})
	require.NoError(t, err)

	time.Sleep(2 * time.Second)

	rpcRequest := &iwfpb.InvokeRPCRequest{
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
	}

	_, err = flowClient.InvokeRPC(ctx, rpcRequest)
	if useLockingRPC && backendType == service.BackendTypeCadence {
		require.Error(t, err)
		return
	}
	require.NoError(t, err)

	_, err = flowClient.WaitForFlow(ctx, &iwfpb.WaitForFlowRequest{
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
