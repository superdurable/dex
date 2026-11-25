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
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/server-zd/integ/workflow/result_loss"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Every run starts its own server, so the fixed Flow IDs never collide. The drop decisions hash
// the Flow ID, so a fixed ID is what makes the same seed drop the same results in every run.
const (
	resultLossPipelineFlowID = "result-loss-pipeline"
	resultLossHoldFlowID     = "result-loss-hold"
	resultLossSeed           = "7"
	resultLossRPCCalls       = 8
)

type pipelineRunOutcome struct {
	output        string
	externalCalls map[string]int64
	attemptsSeen  []string
}

func TestStepResultLossInjectionRetriesAndReproduces(t *testing.T) {
	baseline := runResultLossPipeline(t, nil)
	stepLossFlags := []string{"--inject-step-result-loss", "0.5", "--inject-result-loss-seed", resultLossSeed}
	injected := runResultLossPipeline(t, stepLossFlags)
	repeated := runResultLossPipeline(t, stepLossFlags)
	t.Logf("injected external calls %v, attempts %v", injected.externalCalls, injected.attemptsSeen)

	require.Equal(t, "start/S1/S2/S3", baseline.output)
	require.Equal(t, map[string]int64{"S1_execute": 1, "S2_execute": 1, "S3_execute": 1}, baseline.externalCalls)
	require.Equal(t, baseline.output, injected.output, "dropped results must not change the Flow result")
	require.Greater(t, sumCalls(injected.externalCalls), sumCalls(baseline.externalCalls),
		"the fake external service must see repeated calls: %v", injected.externalCalls)
	require.Equal(t, injected.externalCalls, repeated.externalCalls, "the same seed must drop the same results")
	require.Equal(t, injected.attemptsSeen, repeated.attemptsSeen, "the same seed must drop the same attempts")
}

func TestRPCResultLossInjectionReturnsUnavailableAndReproduces(t *testing.T) {
	baselineCalls, baselineUnavailable := runResultLossRPCs(t, nil)
	rpcLossFlags := []string{"--inject-rpc-result-loss", "0.5", "--inject-result-loss-seed", resultLossSeed}
	injectedCalls, injectedUnavailable := runResultLossRPCs(t, rpcLossFlags)
	repeatedCalls, repeatedUnavailable := runResultLossRPCs(t, rpcLossFlags)
	t.Logf("injected RPC calls %d, UNAVAILABLE answers per call %v", injectedCalls, injectedUnavailable)

	require.Equal(t, int64(resultLossRPCCalls), baselineCalls)
	require.Equal(t, make([]int, resultLossRPCCalls), baselineUnavailable)
	require.Greater(t, injectedCalls, baselineCalls, "the fake external service must see repeated RPC calls")
	require.Equal(t, int64(resultLossRPCCalls)+int64(sumInts(injectedUnavailable)), injectedCalls,
		"each UNAVAILABLE answer must follow one Worker call")
	require.Equal(t, injectedUnavailable, repeatedUnavailable, "the same seed must drop the same RPC results")
	require.Equal(t, injectedCalls, repeatedCalls)
}

// runResultLossPipeline runs the S1 -> S2 -> S3 Flow on a new server started with devFlags.
func runResultLossPipeline(t *testing.T, devFlags []string) pipelineRunOutcome {
	t.Helper()
	workerHandler := result_loss.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, serverZDTestConfig{DevFlags: devFlags})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	_, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             resultLossPipelineFlowID,
		FlowType:           result_loss.FlowType,
		FlowTimeoutSeconds: 100,
		StartStepType:      result_loss.Step1,
		StepInput:          stringValue("start"),
		StepOptions:        result_loss.FastRetryStepOptions(),
		FlowStartOptions:   withWorkerTarget(nil, workerTarget),
	})
	require.NoError(t, err)
	response := waitForCompletedFlow(t, ctx, runtime.FlowClient, resultLossPipelineFlowID)
	require.Len(t, response.GetResults(), 1)
	return pipelineRunOutcome{
		output:        response.GetResults()[0].GetCompletedStepOutput().GetStringValue(),
		externalCalls: workerHandler.ExternalCalls(),
		attemptsSeen:  workerHandler.AttemptsSeen(),
	}
}

// waitForCompletedFlow waits for the Flow's results; the server caps each wait, so it polls.
func waitForCompletedFlow(
	t *testing.T,
	ctx context.Context,
	flowClient dexpb.FlowServiceClient,
	flowID string,
) *dexpb.FlowResult {
	t.Helper()
	var response *dexpb.FlowResult
	require.Eventually(t, func() bool {
		var err error
		response, err = flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
			FlowId: flowID, NeedsResults: true, WaitTimeSeconds: 10,
		})
		require.NoError(t, err)
		return response.GetFlowStatus() != dexpb.FlowStatus_FLOW_STATUS_RUNNING
	}, 90*time.Second, 100*time.Millisecond)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, response.GetFlowStatus())
	return response
}

// runResultLossRPCs invokes the charge RPC resultLossRPCCalls times, retrying each call on
// UNAVAILABLE as a careful caller would, on a new server started with devFlags.
//
// Returns the fake service's RPC call count and the UNAVAILABLE answers of each logical call.
func runResultLossRPCs(t *testing.T, devFlags []string) (int64, []int) {
	t.Helper()
	workerHandler := result_loss.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, serverZDTestConfig{DevFlags: devFlags})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	_, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             resultLossHoldFlowID,
		FlowType:           result_loss.FlowType,
		FlowTimeoutSeconds: 100,
		StartStepType:      result_loss.StepHold,
		FlowStartOptions:   withWorkerTarget(nil, workerTarget),
	})
	require.NoError(t, err)
	unavailableAnswers := make([]int, resultLossRPCCalls)
	expectedAttributes := make([]string, 0, resultLossRPCCalls)
	for call := range resultLossRPCCalls {
		input := fmt.Sprintf("c%d", call+1)
		expectedAttributes = append(expectedAttributes, result_loss.ChargePrefix+input)
		for {
			response, rpcErr := runtime.FlowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
				RequestId:      newRequestID(),
				FlowId:         resultLossHoldFlowID,
				RpcName:        result_loss.RPCCharge,
				Input:          stringValue(input),
				TimeoutSeconds: 10,
			})
			if status.Code(rpcErr) == codes.Unavailable {
				unavailableAnswers[call]++
				require.Less(t, unavailableAnswers[call], 20, "a 0.5 loss must let a retry through")
				continue
			}
			require.NoError(t, rpcErr)
			require.Equal(t, input, response.GetOutput().GetStringValue())
			break
		}
	}
	attributes, err := runtime.FlowClient.GetAttributes(ctx, &dexpb.GetAttributesRequest{
		FlowId: resultLossHoldFlowID, AllKeys: true,
	})
	require.NoError(t, err)
	attributeKeys := make([]string, 0, len(attributes.GetAttributes()))
	for _, attribute := range attributes.GetAttributes() {
		attributeKeys = append(attributeKeys, attribute.GetKey())
	}
	sort.Strings(attributeKeys)
	require.Equal(t, expectedAttributes, attributeKeys, "only the RPC results that arrived commit")
	return workerHandler.ExternalCalls()[result_loss.RPCCharge+"_rpc"], unavailableAnswers
}

func sumCalls(calls map[string]int64) int64 {
	var total int64
	for _, count := range calls {
		total += count
	}
	return total
}

func sumInts(values []int) int {
	total := 0
	for _, value := range values {
		total += value
	}
	return total
}
