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
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex/ptr"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/server-zd/integ/workflow/basic"
	"github.com/superdurable/dex/server-zd/integ/workflow/deadend"
)

func TestStepLineageTemporal(t *testing.T) {
	testStepLineageDurabilities(t)
}

func testStepLineageDurabilities(t *testing.T) {
	durabilities := []dexpb.StepDurability{
		dexpb.StepDurability_STEP_DURABILITY_SYNC,
		dexpb.StepDurability_STEP_DURABILITY_ASYNC,
	}
	for _, durability := range durabilities {
		for i := 0; i < *repeatIntegTest; i++ {
			t.Run(fmt.Sprintf("%s-%d", durability.String(), i), func(t *testing.T) {
				testStepLineageAcrossContinueAsNew(t, durability)
			})
		}
	}
	for i := 0; i < *repeatIntegTest; i++ {
		t.Run(fmt.Sprintf("RPC_CONTINUE_AS_NEW-%d", i), func(t *testing.T) {
			testRPCStepLineageAcrossContinueAsNew(t)
		})
	}
}

func testStepLineageAcrossContinueAsNew(
	t *testing.T,
	durability dexpb.StepDurability,
) {
	workerTarget := startWorker(t, basic.NewHandler())
	runtime := startDexService(t, serverZDTestConfig{})

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	flowID := basic.FlowType + "-lineage-" + uuid.NewString()
	startResponse, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           basic.FlowType,
		FlowTimeoutSeconds: 60,
		StartStepType:      basic.Step1,
		FlowStartOptions: &dexpb.FlowStartOptions{
			FlowConfigOverride: &dexpb.FlowConfig{
				StepDurability:         ptr.Any(durability),
				ContinueAsNewThreshold: ptr.Any(int32(1)),
				WorkerTarget:           workerTarget,
			},
		},
	})
	require.NoError(t, err)

	_, err = runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowID})
	require.NoError(t, err)

	lineage := collectStepLineage(
		t,
		ctx,
		runtime,

		flowID,
		startResponse.GetRunId(),
	)
	require.Equal(t, startingStepExecutionID, lineage[basic.Step1+"-1"])
	require.Equal(t, basic.Step1+"-1", lineage[basic.Step2+"-1"])
}

func testRPCStepLineageAcrossContinueAsNew(
	t *testing.T,
) {
	workerHandler := deadend.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, serverZDTestConfig{})

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	flowID := deadend.WorkflowType + "-lineage-" + uuid.NewString()
	startResponse, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           deadend.WorkflowType,
		FlowTimeoutSeconds: 60,
		FlowStartOptions: &dexpb.FlowStartOptions{
			FlowConfigOverride: &dexpb.FlowConfig{
				StepDurability: ptr.Any(
					dexpb.StepDurability_STEP_DURABILITY_ASYNC,
				),
				ContinueAsNewThreshold: ptr.Any(int32(1)),
				WorkerTarget:           workerTarget,
			},
		},
	})
	require.NoError(t, err)

	_, err = runtime.FlowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
		RequestId: newRequestID(),
		FlowId:    flowID,
		RpcName:   deadend.RPCTriggerState,
	})
	require.NoError(t, err)

	_, err = runtime.FlowClient.WaitForStepCompletion(ctx, &dexpb.WaitForStepCompletionRequest{
		FlowId:                flowID,
		StepType:              deadend.State1,
		StepExecutionNumber:   "1",
		RequestTimeoutSeconds: 30,
		RequestId:             newRequestID(),
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return isExecuteLineagePersisted(
			ctx,
			runtime.FlowClient,
			flowID,
			startResponse.GetRunId(),
			deadend.State1+"-1",
			rpcStepExecutionSource(deadend.RPCTriggerState),
		)
	}, 30*time.Second, 50*time.Millisecond)

	_, err = runtime.FlowClient.StopFlow(ctx, &dexpb.StopFlowRequest{FlowId: flowID})
	require.NoError(t, err)

	lineage := collectStepLineage(
		t,
		ctx,
		runtime,

		flowID,
		startResponse.GetRunId(),
	)
	require.Equal(
		t,
		rpcStepExecutionSource(deadend.RPCTriggerState),
		lineage[deadend.State1+"-1"],
	)
}

func isExecuteLineagePersisted(
	ctx context.Context,
	flowClient dexpb.FlowServiceClient,
	flowID string,
	runID string,
	stepExecutionID string,
	fromStepExecutionID string,
) bool {
	visitedRunIDs := map[string]struct{}{}
	for runID != "" {
		if _, isVisited := visitedRunIDs[runID]; isVisited {
			return false
		}
		visitedRunIDs[runID] = struct{}{}
		nextRunID := ""
		var pageToken []byte
		for {
			response, err := flowClient.GetHistoryEvents(ctx, &dexpb.GetHistoryEventsRequest{
				FlowId:        flowID,
				RunId:         runID,
				NextPageToken: pageToken,
			})
			if err != nil {
				return false
			}
			for _, event := range response.GetEvents() {
				execute := event.GetStepExecuteCompleted()
				if execute.GetContext().GetStepExecutionId() == stepExecutionID &&
					execute.GetContext().GetFromStepExecutionId() == fromStepExecutionID {
					return true
				}
				if continuedToRunID := event.GetFlowClosed().GetContinuedToRunId(); continuedToRunID != "" {
					nextRunID = continuedToRunID
				}
			}
			pageToken = response.GetNextPageToken()
			if len(pageToken) == 0 {
				break
			}
		}
		runID = nextRunID
	}
	return false
}

func collectStepLineage(
	t *testing.T,
	ctx context.Context,
	runtime *integRuntime,

	flowID string,
	firstRunID string,
) map[string]string {
	t.Helper()

	// server-zd never continues a run as new (C3); its Dex history events carry the lineage.
	return collectHistoryEventLineage(t, ctx, runtime.FlowClient, flowID, firstRunID)

}

func collectHistoryEventLineage(
	t *testing.T,
	ctx context.Context,
	flowClient dexpb.FlowServiceClient,
	flowID string,
	runID string,
) map[string]string {
	t.Helper()
	lineage := map[string]string{}
	var pageToken []byte
	for {
		response, err := flowClient.GetHistoryEvents(ctx, &dexpb.GetHistoryEventsRequest{
			FlowId: flowID, RunId: runID, NextPageToken: pageToken,
		})
		require.NoError(t, err)
		for _, event := range response.GetEvents() {
			for _, eventContext := range []*dexpb.StepMethodEventContext{
				event.GetStepWaitForCompleted().GetContext(), event.GetStepExecuteCompleted().GetContext(),
			} {
				if eventContext == nil {
					continue
				}
				recordStepLineage(t, lineage, eventContext.GetStepExecutionId(), eventContext.GetFromStepExecutionId())
			}
		}
		pageToken = response.GetNextPageToken()
		if len(pageToken) == 0 {
			return lineage
		}
	}
}

func recordStepLineage(
	t *testing.T,
	lineage map[string]string,
	currentStepExecutionID string,
	fromStepExecutionID string,
) {
	t.Helper()
	require.NotEmpty(t, currentStepExecutionID)
	if existingSource, exists := lineage[currentStepExecutionID]; exists {
		require.Equal(t, existingSource, fromStepExecutionID)
		return
	}
	lineage[currentStepExecutionID] = fromStepExecutionID
}
