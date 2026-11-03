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
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/server-zd/integ/workflow/common"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	runStateLimitFlowType          = "run-state-limit"
	runStateLimitTurnStep          = "Turn"
	runStateLimitIdleStep          = "Idle"
	runStateLimitMessagePrefix     = "messages/"
	runStateLimitAppendRPC         = "appendMessage"
	runStateLimitCountRPC          = "countMessages"
	runStateLimitMaxValueBytes     = 64 * 1024
	runStateLimitMaxFlowStateBytes = 128 * 1024
	runStateLimitMessageBytes      = 300
	// The handler completes the Flow after this many turns, so an unbounded run still ends.
	runStateLimitMaxTurns = 1500

	childResultLimitParentType = "child-result-limit-parent"
	childResultLimitChildType  = "child-result-limit-child"
	childResultLimitParentStep = "Parent"
	childResultLimitChildStep  = "Child"
	childResultLimitMaxBytes   = 1024
)

// runStateLimitHandler appends one message per turn, loading every earlier message first.
type runStateLimitHandler struct {
	mu                  sync.Mutex
	lastLoadedMessages  int
	completedTurnsCount int
}

func (h *runStateLimitHandler) InvokeWaitForMethod(
	_ context.Context,
	request *dexpb.InvokeWaitForMethodRequest,
) (*dexpb.InvokeWaitForMethodResponse, error) {
	if request.GetFlowType() != runStateLimitFlowType || request.GetStepType() != runStateLimitIdleStep {
		return nil, status.Error(codes.InvalidArgument, "unexpected WaitFor invocation")
	}
	return &dexpb.InvokeWaitForMethodResponse{
		WaitingCondition: &dexpb.WaitingCondition{
			WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
			ChannelConditions:    []*dexpb.ChannelCondition{{ChannelName: "never"}},
		},
	}, nil
}

func (h *runStateLimitHandler) InvokeExecuteMethod(
	_ context.Context,
	request *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	if request.GetFlowType() != runStateLimitFlowType || request.GetStepType() != runStateLimitTurnStep {
		return nil, status.Error(codes.InvalidArgument, "unexpected Execute invocation")
	}
	loadedMessages := 0
	for _, attribute := range request.GetAttributes() {
		if strings.HasPrefix(attribute.GetKey(), runStateLimitMessagePrefix) {
			loadedMessages++
		}
	}
	h.mu.Lock()
	h.lastLoadedMessages = loadedMessages
	h.completedTurnsCount++
	h.mu.Unlock()
	if loadedMessages >= runStateLimitMaxTurns {
		return &dexpb.InvokeExecuteMethodResponse{
			StepDecision: &dexpb.StepDecision{CloseDecision: common.ForceCompleteDecision(nil)},
		}, nil
	}
	return &dexpb.InvokeExecuteMethodResponse{
		UpsertAttributes: []*dexpb.AttributeWrite{runStateLimitMessage(fmt.Sprintf("turn-%05d", loadedMessages))},
		StepDecision: &dexpb.StepDecision{NextSteps: []*dexpb.StepMovement{{
			StepType:    runStateLimitTurnStep,
			StepOptions: runStateLimitTurnOptions(),
		}}},
	}, nil
}

func (h *runStateLimitHandler) InvokeWorkerRPC(
	_ context.Context,
	request *dexpb.InvokeWorkerRPCRequest,
) (*dexpb.InvokeWorkerRPCResponse, error) {
	switch request.GetRpcName() {
	case runStateLimitAppendRPC:
		return &dexpb.InvokeWorkerRPCResponse{
			UpsertAttributes: []*dexpb.AttributeWrite{
				runStateLimitMessage("rpc-" + request.GetInput().GetStringValue()),
			},
		}, nil
	case runStateLimitCountRPC:
		return &dexpb.InvokeWorkerRPCResponse{Output: stringValue("counted")}, nil
	default:
		return nil, status.Errorf(codes.InvalidArgument, "unexpected RPC %q", request.GetRpcName())
	}
}

func (h *runStateLimitHandler) turnCounts() (int, int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lastLoadedMessages, h.completedTurnsCount
}

func runStateLimitMessage(name string) *dexpb.AttributeWrite {
	text := name + "-" + strings.Repeat("m", runStateLimitMessageBytes-len(name)-1)
	return &dexpb.AttributeWrite{Key: runStateLimitMessagePrefix + name, Value: stringValue(text)}
}

func runStateLimitTurnOptions() *dexpb.StepOptions {
	return &dexpb.StepOptions{
		SkipWaitFor:                      true,
		ExecuteLoadAttributeMapInstances: []string{runStateLimitMessagePrefix},
	}
}

func TestRunStateLimitTemporal(t *testing.T) {
	for iteration := 0; iteration < *repeatIntegTest; iteration++ {
		t.Run(fmt.Sprintf("turns-that-free-their-snapshot-%d", iteration), testRunStateLimitFailsGrowingTurns)
		t.Run(fmt.Sprintf("rpc-writes-%d", iteration), testRunStateLimitRejectsGrowingRPCs)
	}
}

func startRunStateLimitService(t *testing.T) (*integRuntime, *dexpb.WorkerTarget, *runStateLimitHandler) {
	t.Helper()
	handler := &runStateLimitHandler{}
	workerTarget := startWorker(t, handler)
	runtime := startDexService(t, serverZDTestConfig{
		ServerZDMaxValueBytes:     runStateLimitMaxValueBytes,
		ServerZDMaxFlowStateBytes: runStateLimitMaxFlowStateBytes,
	})
	return runtime, workerTarget, handler
}

// testRunStateLimitFailsGrowingTurns reproduces a Step that loads a whole map and appends one entry per turn.
func testRunStateLimitFailsGrowingTurns(t *testing.T) {
	runtime, workerTarget, handler := startRunStateLimitService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	flowID := runStateLimitFlowType + "-turns-" + uuid.NewString()
	startResponse, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:        newRequestID(),
		FlowId:           flowID,
		FlowType:         runStateLimitFlowType,
		StartStepType:    runStateLimitTurnStep,
		StepOptions:      runStateLimitTurnOptions(),
		FlowStartOptions: withWorkerTarget(nil, workerTarget),
	})
	require.NoError(t, err)

	// A wait ends with DeadlineExceeded at the harness wait cap, so wait again until the run closes.
	var result *dexpb.FlowResult
	for result == nil {
		require.NoError(t, ctx.Err(), "the run did not close")
		result, err = runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
			FlowId:          flowID,
			RunId:           startResponse.GetRunId(),
			WaitTimeSeconds: 10,
		})
		if status.Code(err) == codes.DeadlineExceeded {
			result = nil
			continue
		}
		require.NoError(t, err)
	}
	loadedMessages, completedTurns := handler.turnCounts()
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_FAILED, result.GetFlowStatus(),
		"after %d turns: %s", completedTurns, result.GetErrorMessage())
	requireRunStateLimitMessage(t, result.GetErrorMessage())
	// The run accepted turns until its state approached the budget, not only the first few.
	require.Greater(t, loadedMessages*runStateLimitMessageBytes, runStateLimitMaxFlowStateBytes/4)
	require.Less(t, loadedMessages, runStateLimitMaxTurns)
}

// testRunStateLimitRejectsGrowingRPCs appends one message per RPC until the run is over budget.
func testRunStateLimitRejectsGrowingRPCs(t *testing.T) {
	runtime, workerTarget, _ := startRunStateLimitService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	flowID := runStateLimitFlowType + "-rpc-" + uuid.NewString()
	_, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:        newRequestID(),
		FlowId:           flowID,
		FlowType:         runStateLimitFlowType,
		StartStepType:    runStateLimitIdleStep,
		FlowStartOptions: withWorkerTarget(nil, workerTarget),
	})
	require.NoError(t, err)

	acceptedAppends := 0
	var rejection error
	for index := 0; index < runStateLimitMaxTurns && rejection == nil; index++ {
		_, err = runtime.FlowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
			FlowId:    flowID,
			RpcName:   runStateLimitAppendRPC,
			Input:     stringValue(strconv.Itoa(index)),
			RequestId: newRequestID(),
		})
		if err != nil {
			rejection = err
			break
		}
		acceptedAppends++
	}
	require.Error(t, rejection, "no RPC was rejected after %d appends", acceptedAppends)
	require.Equal(t, codes.ResourceExhausted, status.Code(rejection), rejection.Error())
	requireRunStateLimitMessage(t, status.Convert(rejection).Message())
	require.Greater(t, acceptedAppends*runStateLimitMessageBytes, runStateLimitMaxFlowStateBytes/4)

	// An RPC that writes no Flow state still runs on a full run.
	countResponse, err := runtime.FlowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
		FlowId:    flowID,
		RpcName:   runStateLimitCountRPC,
		RequestId: newRequestID(),
	})
	require.NoError(t, err)
	require.Equal(t, "counted", countResponse.GetOutput().GetStringValue())
	summary, err := runtime.FlowClient.GetFlowSummary(ctx, &dexpb.GetFlowSummaryRequest{FlowId: flowID})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_RUNNING, summary.GetFlowStatus())
}

func requireRunStateLimitMessage(t *testing.T, message string) {
	t.Helper()
	require.Contains(t, message, "run state of ")
	require.Contains(t, message, fmt.Sprintf("exceeds the maximum of %d bytes", runStateLimitMaxFlowStateBytes))
}

// childResultLimitHandler starts one child whose result has the size the parent's input names.
type childResultLimitHandler struct {
	mu            sync.Mutex
	parentResults map[string]*dexpb.FlowResult
}

func (h *childResultLimitHandler) InvokeWaitForMethod(
	_ context.Context,
	request *dexpb.InvokeWaitForMethodRequest,
) (*dexpb.InvokeWaitForMethodResponse, error) {
	if request.GetFlowType() != childResultLimitParentType || request.GetStepType() != childResultLimitParentStep {
		return nil, status.Error(codes.InvalidArgument, "unexpected WaitFor invocation")
	}
	return &dexpb.InvokeWaitForMethodResponse{
		WaitingCondition: &dexpb.WaitingCondition{
			WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
			SubFlowConditions: []*dexpb.SubFlowCondition{{
				SubFlowType:   childResultLimitChildType,
				StartStepType: childResultLimitChildStep,
				StepInput:     request.GetStepInput(),
				StepOptions:   &dexpb.StepOptions{SkipWaitFor: true},
			}},
		},
	}, nil
}

func (h *childResultLimitHandler) InvokeExecuteMethod(
	_ context.Context,
	request *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	switch request.GetFlowType() {
	case childResultLimitChildType:
		resultBytes := int(request.GetStepInput().GetIntValue())
		return &dexpb.InvokeExecuteMethodResponse{
			StepDecision: &dexpb.StepDecision{
				CloseDecision: common.ForceCompleteDecision(stringValue(strings.Repeat("r", resultBytes))),
			},
		}, nil
	case childResultLimitParentType:
		results := request.GetConditionResults().GetSubFlowResults()
		if len(results) != 1 {
			return nil, status.Error(codes.InvalidArgument, "expected one child result")
		}
		h.mu.Lock()
		h.parentResults[request.GetContext().GetFlowId()] = results[0]
		h.mu.Unlock()
		return &dexpb.InvokeExecuteMethodResponse{
			StepDecision: &dexpb.StepDecision{CloseDecision: common.ForceCompleteDecision(nil)},
		}, nil
	default:
		return nil, status.Error(codes.InvalidArgument, "unexpected Flow type")
	}
}

func (h *childResultLimitHandler) parentResult(flowID string) *dexpb.FlowResult {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.parentResults[flowID]
}

func TestSubFlowResultLimitTemporal(t *testing.T) {
	for iteration := 0; iteration < *repeatIntegTest; iteration++ {
		doTestSubFlowResultLimit(t)
	}
}

func doTestSubFlowResultLimit(t *testing.T) {
	handler := &childResultLimitHandler{parentResults: make(map[string]*dexpb.FlowResult)}
	workerTarget := startWorker(t, handler)
	runtime := startDexService(t, serverZDTestConfig{ServerZDMaxChildFlowResultBytes: childResultLimitMaxBytes})
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	runParent := func(resultBytes int) (string, *dexpb.FlowResult) {
		parentFlowID := childResultLimitParentType + "-" + uuid.NewString()
		_, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
			RequestId:          newRequestID(),
			FlowId:             parentFlowID,
			FlowType:           childResultLimitParentType,
			FlowTimeoutSeconds: 45,
			StartStepType:      childResultLimitParentStep,
			StepInput:          intValue(int64(resultBytes)),
			FlowStartOptions:   withWorkerTarget(nil, workerTarget),
		})
		require.NoError(t, err)
		result, err := runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
			FlowId:          parentFlowID,
			WaitTimeSeconds: 30,
		})
		require.NoError(t, err)
		require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, result.GetFlowStatus(), result.GetErrorMessage())
		childResult := handler.parentResult(parentFlowID)
		require.NotNil(t, childResult)
		return "SubFlow:" + parentFlowID + "-" + childResultLimitParentStep + "-1-0", childResult
	}

	_, accepted := runParent(childResultLimitMaxBytes / 2)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, accepted.GetFlowStatus())
	require.Len(t, accepted.GetResults(), 1)

	childFlowID, rejected := runParent(childResultLimitMaxBytes * 2)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_FAILED, rejected.GetFlowStatus())
	require.Empty(t, rejected.GetResults())
	require.Contains(t, rejected.GetErrorMessage(), "limits.maxChildFlowResultBytes")
	childSummary, err := runtime.FlowClient.GetFlowSummary(ctx, &dexpb.GetFlowSummaryRequest{FlowId: childFlowID})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_FAILED, childSummary.GetFlowStatus())
}
