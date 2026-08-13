// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package locking

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/integ/workflow/common"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

/**
 * This test flow has three steps, using WorkerServiceServer to implement the flow directly.
 *
 * Step1:
 *		- WaitFor method does nothing
 * 		- Execute method will move to Step Waiting, and 10 instances of Step 2
 * Step2:
 * 		- WaitFor update indexed attributes
 * 		- Execute method will update data attributes and will gracefully complete flow
 * StateWaiting:
 * 		- WaitFor will proceed once the internal channel has been published to
 *      - Execute method will gracefully complete flow
 */
const (
	WorkflowType                  = "locking"
	State1                        = "S1"
	State2                        = "S2"
	StateWaiting                  = "StateWaiting"
	TestDataAttributeKey1         = "test-data-attribute-1"
	TestDataAttributeKey2         = "test-data-attribute-2"
	RPCName                       = "increase-counter"
	InternalChannelName           = "test-channel"
	TestSearchAttributeKeywordKey = "CustomKeywordField"
	TestSearchAttributeIntKey     = "CustomIntField"

	ShouldUnblockStateWaiting = "shouldUnblockStateWaiting"

	InParallelS2 = 10

	NumUnusedSignals = 4

	UnusedSignalChannelName   = "test-unused-signal-channel"
	UnusedInternalChannelName = "test-unused-internal-channel"
)

var testValue = jsonObjValue("data")

var state2StepOptions = &dexpb.StepOptions{
	WaitForLockAttributeKeys: []string{
		TestSearchAttributeIntKey,
		TestDataAttributeKey1,
	},
	ExecuteLockAttributeKeys: []string{
		TestSearchAttributeIntKey,
		TestDataAttributeKey1,
	},
}

type handler struct {
	dexpb.UnimplementedWorkerServiceServer
	invokeHistoryMutex sync.Mutex
	invokeHistory      map[string]int64
	rpcInvokesMutex    sync.Mutex
	rpcInvokes         map[string]*rpcRunState
}

// RPCResult summarizes one successful test Worker RPC.
type RPCResult struct {
	InputPayload          string
	OutputPayload         string
	UpsertAttributeCount  int
	RecordEventCount      int
	PublishedMessageCount int
	NextStepCount         int
	NextStepType          string
}

type rpcRunState struct {
	initialInternalChannelSize int32
	invokeCount                int32
	results                    []RPCResult
}

func NewHandler() *handler {
	return &handler{
		invokeHistory: make(map[string]int64),
		rpcInvokes:    make(map[string]*rpcRunState),
	}
}

func (h *handler) InvokeWorkerRPC(
	_ context.Context,
	request *dexpb.InvokeWorkerRPCRequest,
) (*dexpb.InvokeWorkerRPCResponse, error) {
	common.LogRequest("received worker rpc request, ", request)

	if request.GetFlowType() != WorkflowType || request.GetRpcName() != RPCName {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("invalid rpc name: %s", request.GetRpcName()))
	}

	inputObj := request.GetInput().GetObjValue()
	if inputObj == nil || inputObj.GetEncoding() != "json" {
		return nil, status.Error(codes.InvalidArgument, "input is incorrect")
	}
	inputPayload := string(inputObj.GetPayload())

	if inputPayload == ShouldUnblockStateWaiting {
		return &dexpb.InvokeWorkerRPCResponse{
			PublishToChannel: []*dexpb.ChannelMessage{
				{
					ChannelName: InternalChannelName,
					Value:       testValue,
				},
			},
		}, nil
	}

	signalChannelInfo := request.GetChannelInfos()[UnusedSignalChannelName]
	if signalChannelInfo.GetSize() != NumUnusedSignals {
		return nil, status.Error(codes.InvalidArgument, "incorrect signal channel size")
	}

	if err := h.recordRPCInvoke(request); err != nil {
		return nil, err
	}

	time.Sleep(time.Millisecond)

	saInt := int64(0)
	for _, attribute := range request.GetAttributes() {
		if attribute.GetKey() == TestSearchAttributeIntKey {
			saInt = attribute.GetValue().GetIntValue()
		}
	}
	saInt++

	stepContext := request.GetContext()
	upsertAttributes := []*dexpb.AttributeWrite{
		indexedKeywordWrite(TestSearchAttributeKeywordKey, stepContext.GetStepExecutionId()),
		indexedIntWrite(TestSearchAttributeIntKey, saInt),
	}

	daInt := 0
	for _, attribute := range request.GetAttributes() {
		if attribute.GetKey() == TestDataAttributeKey1 {
			payload, hasPayload := objPayloadFromValue(attribute.GetValue())
			if hasPayload && payload != "" {
				parsed, err := strconv.ParseInt(payload, 10, 32)
				if err != nil {
					return nil, status.Error(codes.InvalidArgument, err.Error())
				}
				daInt = int(parsed)
			}
		}
	}
	daInt++

	upsertAttributes = append(upsertAttributes,
		dataObjectWrite(TestDataAttributeKey1, fmt.Sprintf("%v", daInt)),
		dataObjectWrite(TestDataAttributeKey2, stepContext.GetStepExecutionId()),
	)

	response := &dexpb.InvokeWorkerRPCResponse{
		Output: testValue,
		StepDecision: &dexpb.StepDecision{
			NextSteps: []*dexpb.StepMovement{
				{
					StepType:    State2,
					StepOptions: state2StepOptions,
				},
			},
		},
		UpsertAttributes: upsertAttributes,
		RecordEvents: []*dexpb.KV{
			{Key: "test-key", Value: testValue},
		},
		PublishToChannel: []*dexpb.ChannelMessage{
			{
				ChannelName: UnusedInternalChannelName,
				Value:       testValue,
			},
		},
	}
	h.recordRPCResult(request, inputPayload, response)
	return response, nil
}

func (h *handler) recordRPCInvoke(request *dexpb.InvokeWorkerRPCRequest) error {
	h.rpcInvokesMutex.Lock()
	defer h.rpcInvokesMutex.Unlock()

	runID := request.GetContext().GetRunId()
	internalChannelSize := request.GetChannelInfos()[UnusedInternalChannelName].GetSize()
	runState, hasRunState := h.rpcInvokes[runID]
	if !hasRunState {
		runState = &rpcRunState{initialInternalChannelSize: internalChannelSize}
		h.rpcInvokes[runID] = runState
	}
	expectedChannelSize := runState.initialInternalChannelSize + runState.invokeCount
	if internalChannelSize != expectedChannelSize {
		return status.Error(codes.InvalidArgument, "incorrect internal channel size")
	}
	runState.invokeCount++
	return nil
}

func (h *handler) recordRPCResult(
	request *dexpb.InvokeWorkerRPCRequest,
	inputPayload string,
	response *dexpb.InvokeWorkerRPCResponse,
) {
	h.rpcInvokesMutex.Lock()
	defer h.rpcInvokesMutex.Unlock()

	outputPayload, _ := objPayloadFromValue(response.GetOutput())
	nextSteps := response.GetStepDecision().GetNextSteps()
	nextStepType := ""
	if len(nextSteps) > 0 {
		nextStepType = nextSteps[0].GetStepType()
	}
	runState := h.rpcInvokes[request.GetContext().GetRunId()]
	runState.results = append(runState.results, RPCResult{
		InputPayload:          inputPayload,
		OutputPayload:         outputPayload,
		UpsertAttributeCount:  len(response.GetUpsertAttributes()),
		RecordEventCount:      len(response.GetRecordEvents()),
		PublishedMessageCount: len(response.GetPublishToChannel()),
		NextStepCount:         len(nextSteps),
		NextStepType:          nextStepType,
	})
}

func (h *handler) InvokeWaitForMethod(
	_ context.Context,
	request *dexpb.InvokeWaitForMethodRequest,
) (*dexpb.InvokeWaitForMethodResponse, error) {
	common.LogRequest("received waitFor request, ", request)

	if err := validateStepContext(request.GetContext()); err != nil {
		return nil, err
	}

	if request.GetFlowType() != WorkflowType {
		return nil, status.Error(codes.InvalidArgument, "invalid flow type or step type")
	}

	h.incrementInvokeHistory(request.GetStepType() + "_waitFor")

	switch request.GetStepType() {
	case State1:
		return &dexpb.InvokeWaitForMethodResponse{
			WaitingCondition: &dexpb.WaitingCondition{
				WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
			},
		}, nil
	case StateWaiting:
		return &dexpb.InvokeWaitForMethodResponse{
			WaitingCondition: &dexpb.WaitingCondition{
				WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
				ChannelConditions: []*dexpb.ChannelCondition{
					{ChannelName: InternalChannelName},
				},
			},
		}, nil
	case State2:
		time.Sleep(time.Second)
		saInt := int64(0)
		for _, attribute := range request.GetAttributes() {
			if attribute.GetKey() == TestSearchAttributeIntKey {
				saInt = attribute.GetValue().GetIntValue()
			}
		}
		saInt++

		stepContext := request.GetContext()
		return &dexpb.InvokeWaitForMethodResponse{
			WaitingCondition: &dexpb.WaitingCondition{
				WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
			},
			UpsertAttributes: []*dexpb.AttributeWrite{
				indexedKeywordWrite(TestSearchAttributeKeywordKey, stepContext.GetStepExecutionId()),
				indexedIntWrite(TestSearchAttributeIntKey, saInt),
			},
		}, nil
	default:
		return nil, status.Error(codes.InvalidArgument, "invalid flow type or step type")
	}
}

func (h *handler) InvokeExecuteMethod(
	_ context.Context,
	request *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	common.LogRequest("received execute request, ", request)

	if err := validateStepContext(request.GetContext()); err != nil {
		return nil, err
	}

	if request.GetFlowType() != WorkflowType {
		return nil, status.Error(codes.InvalidArgument, "invalid flow type or step type")
	}

	h.incrementInvokeHistory(request.GetStepType() + "_execute")

	switch request.GetStepType() {
	case State1:
		nextSteps := []*dexpb.StepMovement{
			{StepType: StateWaiting},
		}
		for i := 0; i < InParallelS2; i++ {
			nextSteps = append(nextSteps, &dexpb.StepMovement{
				StepType:    State2,
				StepOptions: state2StepOptions,
			})
		}
		return &dexpb.InvokeExecuteMethodResponse{
			StepDecision: &dexpb.StepDecision{NextSteps: nextSteps},
		}, nil
	case StateWaiting:
		return &dexpb.InvokeExecuteMethodResponse{
			StepDecision: &dexpb.StepDecision{
				CloseDecision: common.GracefulCompleteDecision(nil),
			},
		}, nil
	case State2:
		time.Sleep(time.Second)
		daInt := 0
		for _, attribute := range request.GetAttributes() {
			if attribute.GetKey() == TestDataAttributeKey1 {
				payload, hasPayload := objPayloadFromValue(attribute.GetValue())
				if hasPayload && payload != "" {
					parsed, err := strconv.ParseInt(payload, 10, 32)
					if err != nil {
						return nil, status.Error(codes.InvalidArgument, err.Error())
					}
					daInt = int(parsed)
				}
			}
		}
		daInt++

		stepContext := request.GetContext()
		return &dexpb.InvokeExecuteMethodResponse{
			UpsertAttributes: []*dexpb.AttributeWrite{
				dataObjectWrite(TestDataAttributeKey1, fmt.Sprintf("%v", daInt)),
				dataObjectWrite(TestDataAttributeKey2, stepContext.GetStepExecutionId()),
			},
			StepDecision: &dexpb.StepDecision{
				CloseDecision: common.GracefulCompleteDecision(nil),
			},
		}, nil
	default:
		return nil, status.Error(codes.InvalidArgument, "invalid flow type or step type")
	}
}

func (h *handler) GetTestResult() common.TestResult {
	h.invokeHistoryMutex.Lock()
	defer h.invokeHistoryMutex.Unlock()

	invokeHistory := make(map[string]int64)
	for key, value := range h.invokeHistory {
		invokeHistory[key] = value
	}
	return common.TestResult{InvokeHistory: invokeHistory}
}

func (h *handler) incrementInvokeHistory(key string) {
	h.invokeHistoryMutex.Lock()
	defer h.invokeHistoryMutex.Unlock()
	h.invokeHistory[key]++
}

func (h *handler) GetRPCInvokeCount() int32 {
	h.rpcInvokesMutex.Lock()
	defer h.rpcInvokesMutex.Unlock()

	var invokeCount int32
	for _, runState := range h.rpcInvokes {
		invokeCount += runState.invokeCount
	}
	return invokeCount
}

func (h *handler) GetRPCResults(runID string) []RPCResult {
	h.rpcInvokesMutex.Lock()
	defer h.rpcInvokesMutex.Unlock()
	return append([]RPCResult(nil), h.rpcInvokes[runID].results...)
}

func validateStepContext(stepContext *dexpb.Context) error {
	if stepContext.GetAttempt() <= 0 || stepContext.GetFirstAttemptTimestamp() <= 0 {
		return status.Error(
			codes.InvalidArgument,
			"attempt and firstAttemptTimestamp should be greater than zero",
		)
	}
	return nil
}

func jsonObjValue(payload string) *dexpb.Value {
	return &dexpb.Value{
		Kind: &dexpb.Value_ObjValue{
			ObjValue: &dexpb.EncodedObject{
				Encoding: "json",
				Payload:  []byte(payload),
			},
		},
	}
}

func objPayloadFromValue(value *dexpb.Value) (string, bool) {
	if value == nil {
		return "", false
	}
	objValue := value.GetObjValue()
	if objValue == nil {
		return "", false
	}
	return string(objValue.GetPayload()), true
}

func indexedKeywordWrite(key, value string) *dexpb.AttributeWrite {
	return &dexpb.AttributeWrite{
		Key: key,
		Value: &dexpb.Value{
			Kind: &dexpb.Value_StringValue{StringValue: value},
		},
		IndexConfig: &dexpb.IndexConfig{
			Enable: true,
			Type:   dexpb.IndexType_INDEX_TYPE_KEYWORD,
		},
	}
}

func indexedIntWrite(key string, value int64) *dexpb.AttributeWrite {
	return &dexpb.AttributeWrite{
		Key: key,
		Value: &dexpb.Value{
			Kind: &dexpb.Value_IntValue{IntValue: value},
		},
		IndexConfig: &dexpb.IndexConfig{
			Enable: true,
			Type:   dexpb.IndexType_INDEX_TYPE_INT,
		},
	}
}

func dataObjectWrite(key, payload string) *dexpb.AttributeWrite {
	return &dexpb.AttributeWrite{
		Key:   key,
		Value: jsonObjValue(payload),
	}
}
