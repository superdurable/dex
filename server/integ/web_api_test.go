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
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/integ/workflow/basic"
	"github.com/superdurable/dex/integ/workflow/signal"
	"github.com/superdurable/dex/integ/workflow/wf_state_api_fail"
	"github.com/superdurable/dex/service"
	"github.com/superdurable/dex/service/common/ptr"
	"google.golang.org/protobuf/proto"
)

func TestWebAPITemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	testWebAPI(t, service.BackendTypeTemporal)
}

func TestWebAPICadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	testWebAPI(t, service.BackendTypeCadence)
}

func testWebAPI(t *testing.T, backendType service.BackendType) {
	for _, durability := range []dexpb.StepDurability{
		dexpb.StepDurability_STEP_DURABILITY_SYNC,
		dexpb.StepDurability_STEP_DURABILITY_ASYNC,
	} {
		lazyLoadingValues := []bool{true}
		if durability == dexpb.StepDurability_STEP_DURABILITY_ASYNC {
			lazyLoadingValues = []bool{true, false}
		}
		for _, lazyLoading := range lazyLoadingValues {
			t.Run(fmt.Sprintf("%s-lazy-%t", durability, lazyLoading), func(t *testing.T) {
				testWebHistoryAndSummary(t, backendType, durability, lazyLoading)
			})
		}
	}
	for _, lazyLoading := range []bool{true, false} {
		t.Run(fmt.Sprintf("current-state-lazy-%t", lazyLoading), func(t *testing.T) {
			testWebCurrentState(t, backendType, lazyLoading)
		})
	}
	for _, conditionType := range []string{"any", "all"} {
		t.Run("condition-results-"+conditionType, func(t *testing.T) {
			testWebConditionResults(t, backendType, conditionType)
		})
	}
	t.Run("async-local-fallback", func(t *testing.T) {
		testWebAsyncLocalFallback(t, backendType)
	})
	t.Run("parallel-attribute-snapshots", func(t *testing.T) {
		testWebParallelAttributeSnapshots(t, backendType)
	})
}

func testWebParallelAttributeSnapshots(t *testing.T, backendType service.BackendType) {
	handler := newWebParallelSnapshotHandler()
	workerTarget := startWorker(t, handler)
	runtime := startDexService(t, DexServiceTestConfig{
		BackendType:        backendType,
		LazyLoading:        ptr.Any(false),
		LocalBlobDirectory: t.TempDir(),
		LocalBlobThreshold: 10,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	flowID := handler.flowType + "-" + uuid.NewString()
	startResponse, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           handler.flowType,
		FlowTimeoutSeconds: 20,
		StartStepType:      handler.rootStep,
		StepInput: &dexpb.Value{Kind: &dexpb.Value_StringValue{
			StringValue: largeWebTestValue("parallel-root-input"),
		}},
		FlowStartOptions: &dexpb.FlowStartOptions{
			Attributes: []*dexpb.AttributeWrite{webStringAttribute("snapshot", "initial")},
			FlowConfigOverride: &dexpb.FlowConfig{
				StepDurability: ptr.Any(dexpb.StepDurability_STEP_DURABILITY_ASYNC),
				WorkerTarget:   workerTarget,
			},
		},
	})
	require.NoError(t, err)
	_, err = runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowID})
	require.NoError(t, err)

	events, nextInternalEventID := getAllWebHistoryEvents(
		t, ctx, runtime.FlowClient, flowID, startResponse.GetRunId(),
	)
	require.Positive(t, nextInternalEventID)
	request := &dexpb.GetStepEventInputsRequest{
		FlowExecutionId: &dexpb.FlowExecutionID{FlowId: flowID, RunId: startResponse.GetRunId()},
	}
	for _, event := range events {
		execute := event.GetStepExecuteCompleted()
		if execute == nil || execute.GetExecution().GetStepType() == handler.rootStep {
			continue
		}
		request.Keys = append(request.Keys, &dexpb.StepEventInputKey{
			EventId:         event.GetEventId(),
			StepExecutionId: execute.GetExecution().GetStepExecutionId(),
			MethodType:      dexpb.StepMethodType_STEP_METHOD_TYPE_EXECUTE,
		})
	}
	require.Len(t, request.GetKeys(), 2)
	inputs, err := runtime.FlowClient.GetStepEventInputs(ctx, request)
	require.NoError(t, err)
	require.Empty(t, inputs.GetUnavailableEventIds())
	require.Len(t, inputs.GetInputs(), 2)
	for _, input := range inputs.GetInputs() {
		executeRequest := input.GetExecuteRequest()
		require.NotNil(t, executeRequest)
		require.Len(t, executeRequest.GetAttributes(), 1)
		require.Equal(t, "snapshot", executeRequest.GetAttributes()[0].GetKey())
		require.Equal(
			t,
			largeWebTestValue("root-snapshot"),
			executeRequest.GetAttributes()[0].GetValue().GetStringValue(),
		)
		expected := handler.executeRequest(executeRequest.GetContext().GetStepExecutionId())
		require.NotNil(t, expected)
		require.True(t, proto.Equal(expected, executeRequest))
	}
}

type webParallelSnapshotHandler struct {
	dexpb.UnimplementedWorkerServiceServer
	flowType        string
	rootStep        string
	leftStep        string
	rightStep       string
	requestsMutex   sync.Mutex
	executeRequests map[string][]byte
}

func newWebParallelSnapshotHandler() *webParallelSnapshotHandler {
	return &webParallelSnapshotHandler{
		flowType:        "web-parallel-snapshot",
		rootStep:        "root",
		leftStep:        "left",
		rightStep:       "right",
		executeRequests: map[string][]byte{},
	}
}

func (h *webParallelSnapshotHandler) InvokeWaitForMethod(
	_ context.Context,
	request *dexpb.InvokeWaitForMethodRequest,
) (*dexpb.InvokeWaitForMethodResponse, error) {
	if request.GetFlowType() != h.flowType {
		return nil, fmt.Errorf("unexpected flow type %q", request.GetFlowType())
	}
	return &dexpb.InvokeWaitForMethodResponse{}, nil
}

func (h *webParallelSnapshotHandler) InvokeExecuteMethod(
	_ context.Context,
	request *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	if request.GetFlowType() != h.flowType {
		return nil, fmt.Errorf("unexpected flow type %q", request.GetFlowType())
	}
	data, err := (proto.MarshalOptions{Deterministic: true}).Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("marshal received execute request: %w", err)
	}
	h.requestsMutex.Lock()
	h.executeRequests[request.GetContext().GetStepExecutionId()] = data
	h.requestsMutex.Unlock()

	switch request.GetStepType() {
	case h.rootStep:
		return &dexpb.InvokeExecuteMethodResponse{
			StepDecision: &dexpb.StepDecision{NextSteps: []*dexpb.StepMovement{
				{StepType: h.leftStep, StepInput: request.GetStepInput()},
				{StepType: h.rightStep, StepInput: request.GetStepInput()},
			}},
			UpsertAttributes: []*dexpb.AttributeWrite{
				webStringAttribute("snapshot", "root-snapshot"),
			},
		}, nil
	case h.leftStep:
		time.Sleep(100 * time.Millisecond)
		return h.closeParallelBranch(request), nil
	case h.rightStep:
		return h.closeParallelBranch(request), nil
	default:
		return nil, fmt.Errorf("unexpected step type %q", request.GetStepType())
	}
}

func (h *webParallelSnapshotHandler) closeParallelBranch(
	request *dexpb.InvokeExecuteMethodRequest,
) *dexpb.InvokeExecuteMethodResponse {
	return &dexpb.InvokeExecuteMethodResponse{
		StepDecision: &dexpb.StepDecision{CloseDecision: &dexpb.CloseDecision{
			CloseDecisionType: dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_GRACEFUL_COMPLETE,
			CloseInput:        request.GetStepInput(),
		}},
		UpsertAttributes: []*dexpb.AttributeWrite{
			webStringAttribute("snapshot", request.GetStepType()),
		},
	}
}

func (h *webParallelSnapshotHandler) executeRequest(stepExecutionID string) *dexpb.InvokeExecuteMethodRequest {
	h.requestsMutex.Lock()
	defer h.requestsMutex.Unlock()
	data := h.executeRequests[stepExecutionID]
	if len(data) == 0 {
		return nil
	}
	request := &dexpb.InvokeExecuteMethodRequest{}
	if err := proto.Unmarshal(data, request); err != nil {
		panic(err)
	}
	return request
}

func webStringAttribute(key string, value string) *dexpb.AttributeWrite {
	return &dexpb.AttributeWrite{
		Key: key,
		Value: &dexpb.Value{Kind: &dexpb.Value_StringValue{
			StringValue: largeWebTestValue(value),
		}},
	}
}

func testWebConditionResults(t *testing.T, backendType service.BackendType, conditionType string) {
	flowType := "web-step-input-" + conditionType
	workerTarget := startWorker(t, &webStepInputHandler{flowType: flowType, conditionType: conditionType})
	runtime := startDexService(t, DexServiceTestConfig{
		BackendType:        backendType,
		LazyLoading:        ptr.Any(true),
		LocalBlobDirectory: t.TempDir(),
		LocalBlobThreshold: 10,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	flowID := flowType + "-" + uuid.NewString()
	startResponse, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           flowType,
		FlowTimeoutSeconds: 20,
		StartStepType:      "condition-step",
		StepInput: &dexpb.Value{Kind: &dexpb.Value_StringValue{
			StringValue: largeWebTestValue("condition-step-input"),
		}},
		FlowStartOptions: &dexpb.FlowStartOptions{
			Attributes: []*dexpb.AttributeWrite{{
				Key: "condition-attribute",
				Value: &dexpb.Value{Kind: &dexpb.Value_StringValue{
					StringValue: largeWebTestValue("condition-attribute"),
				}},
			}},
			FlowConfigOverride: &dexpb.FlowConfig{
				StepDurability: ptr.Any(dexpb.StepDurability_STEP_DURABILITY_ASYNC),
				WorkerTarget:   workerTarget,
			},
		},
	})
	require.NoError(t, err)
	if conditionType == "any" {
		_, err = runtime.FlowClient.PublishToChannel(ctx, &dexpb.PublishToChannelRequest{
			FlowId: flowID,
			RunId:  startResponse.GetRunId(),
			Messages: []*dexpb.ChannelMessage{{
				ChannelName: "condition-channel",
				Value: &dexpb.Value{Kind: &dexpb.Value_StringValue{
					StringValue: largeWebTestValue("condition-channel-value"),
				}},
			}},
		})
		require.NoError(t, err)
	}
	_, err = runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowID})
	require.NoError(t, err)
	events, nextInternalEventID := getAllWebHistoryEvents(
		t, ctx, runtime.FlowClient, flowID, startResponse.GetRunId(),
	)
	require.Positive(t, nextInternalEventID)
	executeEvent := firstExecuteEvent(events)
	require.NotNil(t, executeEvent)
	inputs, err := runtime.FlowClient.GetStepEventInputs(ctx, &dexpb.GetStepEventInputsRequest{
		FlowExecutionId: &dexpb.FlowExecutionID{FlowId: flowID, RunId: startResponse.GetRunId()},
		Keys: []*dexpb.StepEventInputKey{{
			EventId:         flowEventIDForExecute(events),
			StepExecutionId: executeEvent.GetExecution().GetStepExecutionId(),
			MethodType:      dexpb.StepMethodType_STEP_METHOD_TYPE_EXECUTE,
		}},
	})
	require.NoError(t, err)
	require.Len(t, inputs.GetInputs(), 1)
	request := inputs.GetInputs()[0].GetExecuteRequest()
	require.Equal(t, largeWebTestValue("condition-step-input"), request.GetStepInput().GetStringValue())
	require.Len(t, request.GetAttributes(), 1)
	require.Equal(t, largeWebTestValue("condition-attribute"), request.GetAttributes()[0].GetValue().GetStringValue())
	require.Len(t, request.GetStepExeLocals(), 1)
	require.Equal(t, "condition-local", request.GetStepExeLocals()[0].GetKey())
	require.Equal(
		t,
		largeWebTestValue("condition-local"),
		request.GetStepExeLocals()[0].GetValue().GetStringValue(),
	)
	if conditionType == "any" {
		require.Len(t, request.GetConditionResults().GetChannelResults(), 1)
		require.Len(t, request.GetConditionResults().GetTimerResults(), 2)
		require.Equal(
			t,
			largeWebTestValue("condition-channel-value"),
			request.GetConditionResults().GetChannelResults()[0].GetValues()[0].GetStringValue(),
		)
		for _, result := range request.GetConditionResults().GetTimerResults() {
			require.Equal(t, dexpb.ConditionStatus_CONDITION_STATUS_WAITING, result.GetConditionStatus())
		}
	} else {
		require.Empty(t, request.GetConditionResults().GetChannelResults())
		require.Len(t, request.GetConditionResults().GetTimerResults(), 2)
		for _, result := range request.GetConditionResults().GetTimerResults() {
			require.Equal(t, dexpb.ConditionStatus_CONDITION_STATUS_COMPLETED, result.GetConditionStatus())
		}
	}
}

type webStepInputHandler struct {
	dexpb.UnimplementedWorkerServiceServer
	flowType      string
	conditionType string
}

func (h *webStepInputHandler) InvokeWaitForMethod(
	_ context.Context,
	request *dexpb.InvokeWaitForMethodRequest,
) (*dexpb.InvokeWaitForMethodResponse, error) {
	if request.GetFlowType() != h.flowType {
		return nil, fmt.Errorf("unexpected flow type %q", request.GetFlowType())
	}
	condition := &dexpb.WaitingCondition{}
	if h.conditionType == "any" {
		condition.WaitingConditionType = dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMPLETED
		condition.ChannelConditions = []*dexpb.ChannelCondition{{
			ConditionId: "channel",
			ChannelName: "condition-channel",
		}}
		condition.TimerConditions = []*dexpb.TimerCondition{
			{ConditionId: "timer-1", DurationSeconds: 60},
			{ConditionId: "timer-2", DurationSeconds: 120},
		}
	} else {
		condition.WaitingConditionType = dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED
		condition.TimerConditions = []*dexpb.TimerCondition{
			{ConditionId: "timer-1", DurationSeconds: 1},
			{ConditionId: "timer-2", DurationSeconds: 1},
		}
	}
	return &dexpb.InvokeWaitForMethodResponse{
		WaitingCondition: condition,
		UpsertStepExeLocals: []*dexpb.KV{{
			Key: "condition-local",
			Value: &dexpb.Value{Kind: &dexpb.Value_StringValue{
				StringValue: largeWebTestValue("condition-local"),
			}},
		}},
	}, nil
}

func (h *webStepInputHandler) InvokeExecuteMethod(
	_ context.Context,
	request *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	if request.GetFlowType() != h.flowType {
		return nil, fmt.Errorf("unexpected flow type %q", request.GetFlowType())
	}
	return &dexpb.InvokeExecuteMethodResponse{StepDecision: &dexpb.StepDecision{
		CloseDecision: &dexpb.CloseDecision{
			CloseDecisionType: dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_FORCE_COMPLETE,
			CloseInput:        request.GetStepInput(),
		},
	}}, nil
}

func testWebHistoryAndSummary(
	t *testing.T,
	backendType service.BackendType,
	durability dexpb.StepDurability,
	lazyLoading bool,
) {
	workerTarget := startWorker(t, basic.NewHandler())
	blobDirectory := t.TempDir()
	runtime := startDexService(t, DexServiceTestConfig{
		BackendType:        backendType,
		LazyLoading:        ptr.Any(lazyLoading),
		LocalBlobDirectory: blobDirectory,
		LocalBlobThreshold: 10,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	flowID := fmt.Sprintf("%s-web-%s-%s", basic.FlowType, durability, uuid.NewString())
	stepInput := largeWebTestValue("step-input")
	attributePayload := []byte(fmt.Sprintf(`{"source":%q}`, largeWebTestValue("web-step-event-input")))
	requestID := newRequestID()
	startResponse, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          requestID,
		FlowId:             flowID,
		FlowType:           basic.FlowType,
		FlowTimeoutSeconds: 60,
		StartStepType:      basic.Step1,
		StepInput:          &dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: stepInput}},
		FlowStartOptions: &dexpb.FlowStartOptions{
			Attributes: []*dexpb.AttributeWrite{{
				Key: "web-test-attribute",
				Value: &dexpb.Value{Kind: &dexpb.Value_ObjValue{ObjValue: &dexpb.EncodedObject{
					Encoding: "json",
					Payload:  attributePayload,
				}}},
			}},
			FlowConfigOverride: &dexpb.FlowConfig{
				ContinueAsNewThreshold: ptr.Any(int32(1)),
				StepDurability:         ptr.Any(durability),
				WorkerTarget:           workerTarget,
			},
		},
	})
	require.NoError(t, err)
	_, err = runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowID})
	require.NoError(t, err)

	summary, err := runtime.FlowClient.GetFlowSummary(ctx, &dexpb.GetFlowSummaryRequest{
		FlowId: flowID,
		RunId:  startResponse.GetRunId(),
	})
	require.NoError(t, err)
	require.Equal(t, flowID, summary.GetFlowExecutionId().GetFlowId())
	require.Equal(t, startResponse.GetRunId(), summary.GetFlowExecutionId().GetRunId())
	require.Equal(t, requestID, summary.GetRequestId())
	require.Equal(t, basic.FlowType, summary.GetFlowType())
	require.NotNil(t, summary.GetStartTime())

	firstRunEvents, nextEventID := getAllWebHistoryEvents(
		t,
		ctx,
		runtime.FlowClient,
		flowID,
		startResponse.GetRunId(),
	)
	require.NotEmpty(t, firstRunEvents)
	initialStart := firstRunEvents[0].GetFlowStartedOrContinued().GetInitialStart()
	require.NotNil(t, initialStart)
	require.NotEmpty(t, initialStart.GetStepInput().GetInternalBlobIdForStringValue())
	require.Len(t, initialStart.GetInitialAttributes(), 1)
	require.NotEmpty(t, initialStart.GetInitialAttributes()[0].GetValue().GetInternalBlobIdForObjValue())
	loadedStartValues, err := runtime.FlowClient.LoadBlobs(ctx, &dexpb.LoadBlobsRequest{Values: []*dexpb.Value{
		initialStart.GetStepInput(),
		initialStart.GetInitialAttributes()[0].GetValue(),
	}})
	require.NoError(t, err)
	require.Equal(t, stepInput, loadedStartValues.GetValues()[initialStart.GetStepInput().GetInternalBlobIdForStringValue()].GetStringValue())
	require.Equal(t, attributePayload, loadedStartValues.GetValues()[initialStart.GetInitialAttributes()[0].GetValue().GetInternalBlobIdForObjValue()].GetObjValue().GetPayload())
	firstStep := firstStepEvent(firstRunEvents)
	require.NotNil(t, firstStep)
	require.Equal(
		t,
		service.StartingStepFromStepExecutionId,
		firstStep.GetExecution().GetFromStepExecutionId(),
	)
	firstExecute := firstExecuteEvent(firstRunEvents)
	require.NotNil(t, firstExecute)
	var storedInputRequest *dexpb.GetStepEventInputsRequest
	if durability == dexpb.StepDurability_STEP_DURABILITY_ASYNC {
		require.Nil(t, firstStep.GetRequest())
		require.Nil(t, firstExecute.GetRequest())
		storedInputRequest = &dexpb.GetStepEventInputsRequest{
			FlowExecutionId: &dexpb.FlowExecutionID{FlowId: flowID, RunId: startResponse.GetRunId()},
			Keys: []*dexpb.StepEventInputKey{
				{
					EventId:         flowEventIDForWaitFor(firstRunEvents),
					StepExecutionId: firstStep.GetExecution().GetStepExecutionId(),
					MethodType:      dexpb.StepMethodType_STEP_METHOD_TYPE_WAIT_FOR,
				},
				{
					EventId:         flowEventIDForExecute(firstRunEvents),
					StepExecutionId: firstExecute.GetExecution().GetStepExecutionId(),
					MethodType:      dexpb.StepMethodType_STEP_METHOD_TYPE_EXECUTE,
				},
			},
		}
		inputs, inputErr := runtime.FlowClient.GetStepEventInputs(ctx, storedInputRequest)
		require.NoError(t, inputErr)
		require.Empty(t, inputs.GetUnavailableEventIds())
		require.Len(t, inputs.GetInputs(), 2)
		assertStepMethodRequestValues(
			t,
			inputs.GetInputs()[0].GetWaitForRequest().GetStepInput(),
			inputs.GetInputs()[0].GetWaitForRequest().GetAttributes(),
			stepInput,
			attributePayload,
		)
		assertStepMethodRequestValues(
			t,
			inputs.GetInputs()[1].GetExecuteRequest().GetStepInput(),
			inputs.GetInputs()[1].GetExecuteRequest().GetAttributes(),
			stepInput,
			attributePayload,
		)
	} else {
		require.NotNil(t, firstStep.GetRequest())
		require.NotNil(t, firstExecute.GetRequest())
	}

	continuedToRunID := continuedToRunID(firstRunEvents)
	require.NotEmpty(t, continuedToRunID)
	continuedEvents, _ := getAllWebHistoryEvents(
		t,
		ctx,
		runtime.FlowClient,
		flowID,
		continuedToRunID,
	)
	require.NotEmpty(t, continuedEvents)
	continuedStart := continuedEvents[0].GetFlowStartedOrContinued().GetContinuedStart()
	require.NotNil(t, continuedStart)
	require.Equal(t, startResponse.GetRunId(), continuedStart.GetPreviousRunId())
	require.True(
		t,
		len(continuedStart.GetStepsToStart()) > 0 ||
			len(continuedStart.GetStepsToResume()) > 0 ||
			len(continuedStart.GetCompletedSteps()) > 0,
	)
	if durability == dexpb.StepDurability_STEP_DURABILITY_ASYNC {
		continuedStep := firstStepEvent(continuedEvents)
		require.NotNil(t, continuedStep)
		continuedExecute := firstExecuteEvent(continuedEvents)
		require.NotNil(t, continuedExecute)
		continuedInputs, inputErr := runtime.FlowClient.GetStepEventInputs(
			ctx,
			&dexpb.GetStepEventInputsRequest{
				FlowExecutionId: &dexpb.FlowExecutionID{FlowId: flowID, RunId: continuedToRunID},
				Keys: []*dexpb.StepEventInputKey{
					{
						EventId:         flowEventIDForWaitFor(continuedEvents),
						StepExecutionId: continuedStep.GetExecution().GetStepExecutionId(),
						MethodType:      dexpb.StepMethodType_STEP_METHOD_TYPE_WAIT_FOR,
					},
					{
						EventId:         flowEventIDForExecute(continuedEvents),
						StepExecutionId: continuedExecute.GetExecution().GetStepExecutionId(),
						MethodType:      dexpb.StepMethodType_STEP_METHOD_TYPE_EXECUTE,
					},
				},
			},
		)
		require.NoError(t, inputErr)
		require.Empty(t, continuedInputs.GetUnavailableEventIds())
		require.Len(t, continuedInputs.GetInputs(), 2)
		for _, input := range continuedInputs.GetInputs() {
			requestStepInput := input.GetWaitForRequest().GetStepInput()
			requestAttributes := input.GetWaitForRequest().GetAttributes()
			if input.GetExecuteRequest() != nil {
				requestStepInput = input.GetExecuteRequest().GetStepInput()
				requestAttributes = input.GetExecuteRequest().GetAttributes()
			}
			assertStepMethodRequestValues(
				t,
				requestStepInput,
				requestAttributes,
				stepInput,
				attributePayload,
			)
		}
	}
	closeOutput := firstWebCloseOutput(continuedEvents)
	require.NotNil(t, closeOutput)
	require.NotEmpty(t, closeOutput.GetInternalBlobIdForStringValue())
	loadedCloseOutput, err := runtime.FlowClient.LoadBlobs(
		ctx,
		&dexpb.LoadBlobsRequest{Values: []*dexpb.Value{closeOutput}},
	)
	require.NoError(t, err)
	require.Equal(
		t,
		stepInput,
		loadedCloseOutput.GetValues()[closeOutput.GetInternalBlobIdForStringValue()].GetStringValue(),
	)

	waitResponse, err := runtime.FlowClient.WaitForHistoryEvent(
		ctx,
		&dexpb.WaitForHistoryEventRequest{
			FlowId:              flowID,
			RunId:               startResponse.GetRunId(),
			NextInternalEventId: nextEventID,
		},
	)
	require.NoError(t, err)
	require.False(t, waitResponse.GetEventAvailable())
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_CONTINUED_AS_NEW, waitResponse.GetFlowStatus())
	if storedInputRequest != nil && *dexServerAddress == "" {
		require.NoError(t, os.RemoveAll(blobDirectory))
		unavailable, unavailableErr := runtime.FlowClient.GetStepEventInputs(ctx, storedInputRequest)
		require.NoError(t, unavailableErr)
		require.Empty(t, unavailable.GetInputs())
		require.ElementsMatch(
			t,
			[]int64{flowEventIDForWaitFor(firstRunEvents), flowEventIDForExecute(firstRunEvents)},
			unavailable.GetUnavailableEventIds(),
		)
	}
}

func testWebCurrentState(t *testing.T, backendType service.BackendType, lazyLoading bool) {
	workerTarget := startWorker(t, signal.NewHandler())
	runtime := startDexService(t, DexServiceTestConfig{
		BackendType:        backendType,
		LazyLoading:        ptr.Any(lazyLoading),
		LocalBlobDirectory: t.TempDir(),
		LocalBlobThreshold: 10,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowID := signal.WorkflowType + "-web-state-" + uuid.NewString()
	startResponse, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           signal.WorkflowType,
		FlowTimeoutSeconds: 30,
		StartStepType:      signal.State1,
		FlowStartOptions: &dexpb.FlowStartOptions{
			FlowConfigOverride: &dexpb.FlowConfig{
				StepDurability: ptr.Any(dexpb.StepDurability_STEP_DURABILITY_ASYNC),
				WorkerTarget:   workerTarget,
			},
		},
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		state, stateErr := runtime.FlowClient.GetFlowState(
			ctx,
			&dexpb.GetFlowStateRequest{
				FlowId: flowID,
				RunId:  startResponse.GetRunId(),
			},
		)
		if stateErr != nil || len(state.GetActiveStepExecutions()) != 1 {
			return false
		}
		active := state.GetActiveStepExecutions()[0]
		return active.GetStepExecutionId() == signal.State1+"-1" &&
			active.GetFromStepExecutionId() == service.StartingStepFromStepExecutionId &&
			active.GetPhase() == dexpb.ActiveStepPhase_ACTIVE_STEP_PHASE_WAITING
	}, 30*time.Second, 50*time.Millisecond)

	messages := make([]*dexpb.ChannelMessage, 4)
	for index := range messages {
		messages[index] = &dexpb.ChannelMessage{
			ChannelName: signal.SignalName,
			Value: &dexpb.Value{Kind: &dexpb.Value_StringValue{
				StringValue: largeWebTestValue(fmt.Sprintf("channel-value-%d", index)),
			}},
		}
	}
	_, err = runtime.FlowClient.PublishToChannel(ctx, &dexpb.PublishToChannelRequest{
		FlowId:   flowID,
		Messages: messages,
	})
	require.NoError(t, err)
	_, err = runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowID})
	require.NoError(t, err)
	events, nextInternalEventID := getAllWebHistoryEvents(
		t, ctx, runtime.FlowClient, flowID, startResponse.GetRunId(),
	)
	require.Positive(t, nextInternalEventID)
	executeEvent := firstExecuteEvent(events)
	require.NotNil(t, executeEvent)
	inputs, err := runtime.FlowClient.GetStepEventInputs(ctx, &dexpb.GetStepEventInputsRequest{
		FlowExecutionId: &dexpb.FlowExecutionID{FlowId: flowID, RunId: startResponse.GetRunId()},
		Keys: []*dexpb.StepEventInputKey{{
			EventId:         flowEventIDForExecute(events),
			StepExecutionId: executeEvent.GetExecution().GetStepExecutionId(),
			MethodType:      dexpb.StepMethodType_STEP_METHOD_TYPE_EXECUTE,
		}},
	})
	require.NoError(t, err)
	require.Empty(t, inputs.GetUnavailableEventIds())
	require.Len(t, inputs.GetInputs(), 1)
	channelResults := inputs.GetInputs()[0].GetExecuteRequest().GetConditionResults().GetChannelResults()
	require.Len(t, channelResults, 4)
	for index, result := range channelResults {
		require.Equal(t, signal.SignalName, result.GetChannelName())
		require.Equal(t, dexpb.ConditionStatus_CONDITION_STATUS_COMPLETED, result.GetConditionStatus())
		require.Len(t, result.GetValues(), 1)
		require.Equal(
			t,
			largeWebTestValue(fmt.Sprintf("channel-value-%d", index)),
			result.GetValues()[0].GetStringValue(),
		)
	}
	assertExternalChannelValuesLoad(t, ctx, runtime.FlowClient, events)
}

func testWebAsyncLocalFallback(t *testing.T, backendType service.BackendType) {
	workerTarget := startWorker(t, wf_state_api_fail.NewHandler())
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowID := wf_state_api_fail.FlowType + "-web-fallback-" + uuid.NewString()
	startResponse, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           wf_state_api_fail.FlowType,
		FlowTimeoutSeconds: 10,
		StartStepType:      wf_state_api_fail.Step1,
		StepOptions: &dexpb.StepOptions{
			WaitForRetryPolicy: &dexpb.RetryPolicy{TotalDurationSeconds: 1},
		},
		FlowStartOptions: &dexpb.FlowStartOptions{
			FlowConfigOverride: &dexpb.FlowConfig{
				StepDurability: ptr.Any(dexpb.StepDurability_STEP_DURABILITY_ASYNC),
				WorkerTarget:   workerTarget,
			},
		},
	})
	require.NoError(t, err)
	flowResult, err := runtime.FlowClient.WaitForFlow(
		ctx,
		&dexpb.WaitForFlowRequest{FlowId: flowID},
	)
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_FAILED, flowResult.GetFlowStatus())

	events, nextInternalEventID := getAllWebHistoryEvents(
		t,
		ctx,
		runtime.FlowClient,
		flowID,
		startResponse.GetRunId(),
	)
	require.Positive(t, nextInternalEventID)
	var failedEvent *dexpb.StepWaitForFailedEvent
	for _, event := range events {
		if event.GetStepWaitForFailed() != nil {
			failedEvent = event.GetStepWaitForFailed()
			break
		}
	}
	require.NotNil(t, failedEvent)
	require.Equal(
		t,
		dexpb.StepDurability_STEP_DURABILITY_ASYNC,
		failedEvent.GetExecution().GetDurability(),
	)
	require.NotEmpty(t, failedEvent.GetExecution().GetPreviousAttemptFailures())
	previousAttempt := failedEvent.GetExecution().GetPreviousAttemptFailures()
	require.Greater(
		t,
		failedEvent.GetExecution().GetFinalAttempt(),
		previousAttempt[len(previousAttempt)-1].GetAttempt(),
	)
	require.Equal(
		t,
		service.StartingStepFromStepExecutionId,
		failedEvent.GetExecution().GetFromStepExecutionId(),
	)
	require.NotNil(t, failedEvent.GetRequest())
	inputs, err := runtime.FlowClient.GetStepEventInputs(ctx, &dexpb.GetStepEventInputsRequest{
		FlowExecutionId: &dexpb.FlowExecutionID{FlowId: flowID, RunId: startResponse.GetRunId()},
		Keys: []*dexpb.StepEventInputKey{{
			EventId:         flowEventIDForWaitFor(events),
			StepExecutionId: failedEvent.GetExecution().GetStepExecutionId(),
			MethodType:      dexpb.StepMethodType_STEP_METHOD_TYPE_WAIT_FOR,
		}},
	})
	require.NoError(t, err)
	require.Empty(t, inputs.GetInputs())
	require.Equal(t, []int64{flowEventIDForWaitFor(events)}, inputs.GetUnavailableEventIds())
}

func getAllWebHistoryEvents(
	t *testing.T,
	ctx context.Context,
	flowClient dexpb.FlowServiceClient,
	flowID string,
	runID string,
) ([]*dexpb.FlowHistoryEvent, int64) {
	t.Helper()
	var events []*dexpb.FlowHistoryEvent
	var pageToken []byte
	nextEventID := int64(1)
	for {
		response, err := flowClient.GetHistoryEvents(ctx, &dexpb.GetHistoryEventsRequest{
			FlowId:               flowID,
			RunId:                runID,
			StartInternalEventId: nextEventID,
			EstimatePageSize:     1,
			NextPageToken:        pageToken,
		})
		require.NoError(t, err)
		events = append(events, response.GetEvents()...)
		nextEventID = response.GetNextInternalEventId()
		pageToken = response.GetNextPageToken()
		if len(pageToken) == 0 {
			return events, nextEventID
		}
	}
}

func firstStepEvent(events []*dexpb.FlowHistoryEvent) *dexpb.StepWaitForCompletedEvent {
	for _, event := range events {
		if event.GetStepWaitForCompleted() != nil {
			return event.GetStepWaitForCompleted()
		}
	}
	return nil
}

func firstExecuteEvent(events []*dexpb.FlowHistoryEvent) *dexpb.StepExecuteCompletedEvent {
	for _, event := range events {
		if event.GetStepExecuteCompleted() != nil {
			return event.GetStepExecuteCompleted()
		}
	}
	return nil
}

func flowEventIDForWaitFor(events []*dexpb.FlowHistoryEvent) int64 {
	for _, event := range events {
		if event.GetStepWaitForCompleted() != nil || event.GetStepWaitForFailed() != nil {
			return event.GetEventId()
		}
	}
	return 0
}

func flowEventIDForExecute(events []*dexpb.FlowHistoryEvent) int64 {
	for _, event := range events {
		if event.GetStepExecuteCompleted() != nil {
			return event.GetEventId()
		}
	}
	return 0
}

func firstWebCloseOutput(events []*dexpb.FlowHistoryEvent) *dexpb.Value {
	for _, event := range events {
		results := event.GetFlowClosed().GetResults()
		if len(results) > 0 {
			return results[0].GetCompletedStepOutput()
		}
	}
	return nil
}

func assertStepMethodRequestValues(
	t *testing.T,
	stepInput *dexpb.Value,
	attributes []*dexpb.KV,
	expectedInput string,
	expectedAttribute []byte,
) {
	t.Helper()
	require.Equal(t, expectedInput, stepInput.GetStringValue())
	require.Len(t, attributes, 1)
	require.Equal(t, "web-test-attribute", attributes[0].GetKey())
	require.Equal(t, expectedAttribute, attributes[0].GetValue().GetObjValue().GetPayload())
}

func assertExternalChannelValuesLoad(
	t *testing.T,
	ctx context.Context,
	flowClient dexpb.FlowServiceClient,
	events []*dexpb.FlowHistoryEvent,
) {
	t.Helper()
	var values []*dexpb.Value
	for _, event := range events {
		for _, message := range event.GetChannelExternalPublish().GetMessages() {
			values = append(values, message.GetValue())
		}
	}
	require.Len(t, values, 4)
	for _, value := range values {
		require.NotEmpty(t, value.GetInternalBlobIdForStringValue())
	}
	response, err := flowClient.LoadBlobs(ctx, &dexpb.LoadBlobsRequest{Values: values})
	require.NoError(t, err)
	for index, value := range values {
		loaded := response.GetValues()[value.GetInternalBlobIdForStringValue()]
		require.Equal(t, largeWebTestValue(fmt.Sprintf("channel-value-%d", index)), loaded.GetStringValue())
	}
}

func largeWebTestValue(prefix string) string {
	return prefix + "-" + strings.Repeat("value", 300)
}

func continuedToRunID(events []*dexpb.FlowHistoryEvent) string {
	for _, event := range events {
		if event.GetFlowClosed().GetContinuedToRunId() != "" {
			return event.GetFlowClosed().GetContinuedToRunId()
		}
	}
	return ""
}
