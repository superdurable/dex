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
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/integ/workflow/signal"
	"github.com/superdurable/dex/service"
)

func TestUpdateWorkerTargetTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	testUpdateWorkerTarget(t, service.BackendTypeTemporal)
}

func TestUpdateWorkerTargetCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	testUpdateWorkerTarget(t, service.BackendTypeCadence)
}

func testUpdateWorkerTarget(t *testing.T, backendType service.BackendType) {
	firstHandler := signal.NewHandler()
	firstTarget := startWorker(t, firstHandler)
	secondHandler := signal.NewHandler()
	secondTarget := startWorker(t, secondHandler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowID := "worker-target-update-" + uuid.NewString()
	_, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           signal.WorkflowType,
		FlowTimeoutSeconds: 20,
		StartStepType:      signal.State1,
		FlowStartOptions:   withWorkerTarget(nil, firstTarget),
	})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return firstHandler.GetTestResult().InvokeHistory["S1_waitFor"] == 1
	}, 10*time.Second, 20*time.Millisecond)

	_, err = runtime.FlowClient.UpdateFlowConfig(ctx, &dexpb.UpdateFlowConfigRequest{
		FlowId: flowID,
		FlowConfig: &dexpb.FlowConfig{
			WorkerTarget: secondTarget,
		},
	})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		var dump dexpb.DebugDumpResponse
		queryErr := runtime.UnifiedClient.QueryWorkflow(
			ctx,
			&dump,
			flowID,
			"",
			service.DebugDumpQueryType,
		)
		return queryErr == nil &&
			dump.GetConfig().GetWorkerTarget().GetAddress() == secondTarget.GetAddress()
	}, 10*time.Second, 20*time.Millisecond)

	for index := 0; index < 4; index++ {
		_, err = runtime.FlowClient.PublishToChannel(ctx, &dexpb.PublishToChannelRequest{
			FlowId: flowID,
			Messages: []*dexpb.ChannelMessage{
				{
					ChannelName: signal.SignalName,
					Value:       stringValue(fmt.Sprintf("signal-%d", index)),
				},
			},
		})
		require.NoError(t, err)
	}
	_, err = runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowID})
	require.NoError(t, err)

	require.Equal(t, map[string]int64{"S1_waitFor": 1}, firstHandler.GetTestResult().InvokeHistory)
	require.Equal(t, map[string]int64{
		"S1_execute": 1,
		"S2_waitFor": 1,
		"S2_execute": 1,
	}, secondHandler.GetTestResult().InvokeHistory)
}
