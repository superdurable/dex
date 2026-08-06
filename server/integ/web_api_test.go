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
	"sync/atomic"
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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
	for _, durability := range []dexpb.StepDurability{
		dexpb.StepDurability_STEP_DURABILITY_SYNC,
		dexpb.StepDurability_STEP_DURABILITY_ASYNC,
	} {
		for _, conditionType := range []string{"any", "all"} {
			t.Run(fmt.Sprintf("condition-results-%s-%s", durability, conditionType), func(t *testing.T) {
				testWebConditionResults(t, backendType, durability, conditionType)
			})
		}
	}
	t.Run("async-local-fallback", func(t *testing.T) {
		testWebAsyncLocalFallback(t, backendType)
	})
	t.Run("parallel-attribute-snapshots", func(t *testing.T) {
		testWebParallelAttributeSnapshots(t, backendType)
	})
	t.Run("sync-last-failure", func(t *testing.T) {
		testWebSyncLastFailure(t, backendType)
	})
}

func testWebSyncLastFailure(t *testing.T, backendType service.BackendType) {
	handler := &webSyncRetryHandler{flowType: "web-sync-last-failure"}
	workerTarget := startWorker(t, handler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	flowID := handler.flowType + "-" + uuid.NewString()
	startResponse, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           handler.flowType,
		FlowTimeoutSeconds: 20,
		StartStepType:      "retry-step",
		StepOptions: &dexpb.StepOptions{
			WaitForTimeoutSeconds: 5,
			WaitForRetryPolicy: &dexpb.RetryPolicy{
				InitialIntervalSeconds: 1,
				MaximumIntervalSeconds: 1,
				MaximumAttempts:        2,
			},
		},
		FlowStartOptions: &dexpb.FlowStartOptions{FlowConfigOverride: &dexpb.FlowConfig{
			StepDurability: ptr.Any(dexpb.StepDurability_STEP_DURABILITY_SYNC),
			WorkerTarget:   workerTarget,
		}},
	})
	require.NoError(t, err)
	_, err = runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowID})
	require.NoError(t, err)

	events, nextInternalEventID := getAllWebHistoryEvents(
		t, ctx, runtime.FlowClient, flowID, startResponse.GetRunId(),
	)
	require.Positive(t, nextInternalEventID)
	waitForEvent := firstStepEvent(events)
	require.NotNil(t, waitForEvent)
	execution := waitForEvent.GetExecution()
	require.Equal(t, dexpb.StepDurability_STEP_DURABILITY_SYNC, execution.GetDurability())
	require.Len(t, execution.GetPreviousAttemptFailures(), 1)
	lastFailure := execution.GetLastFailureInfo()
	require.NotNil(t, lastFailure)
	require.Equal(t, int32(1), lastFailure.GetAttempt())
	require.True(t, proto.Equal(execution.GetPreviousAttemptFailures()[0], lastFailure))
	require.Equal(
		t,
		dexpb.FlowErrorType_FLOW_ERROR_TYPE_WORKER_API_FAIL.String(),
		lastFailure.GetFailure().GetErrorType(),
	)
	require.Equal(t, "sync retry failure", lastFailure.GetFailure().GetDetails().GetDetail())
	require.Equal(t, int32(codes.Unavailable), lastFailure.GetFailure().GetDetails().GetOriginalWorkerErrorStatus())
}

type webSyncRetryHandler struct {
	dexpb.UnimplementedWorkerServiceServer
	flowType     string
	waitForCalls atomic.Int32
}

func (h *webSyncRetryHandler) InvokeWaitForMethod(
	_ context.Context,
	request *dexpb.InvokeWaitForMethodRequest,
) (*dexpb.InvokeWaitForMethodResponse, error) {
	if request.GetFlowType() != h.flowType {
		return nil, fmt.Errorf("unexpected flow type %q", request.GetFlowType())
	}
	if h.waitForCalls.Add(1) == 1 {
		return nil, status.Error(codes.Unavailable, "sync retry failure")
	}
	return &dexpb.InvokeWaitForMethodResponse{}, nil
}

func (h *webSyncRetryHandler) InvokeExecuteMethod(
	_ context.Context,
	request *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	if request.GetFlowType() != h.flowType {
		return nil, fmt.Errorf("unexpected flow type %q", request.GetFlowType())
	}
	return &dexpb.InvokeExecuteMethodResponse{StepDecision: &dexpb.StepDecision{
		CloseDecision: &dexpb.CloseDecision{
			CloseDecisionType: dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_FORCE_COMPLETE,
		},
	}}, nil
}

func testWebParallelAttributeSnapshots(t *testing.T, backendType service.BackendType) {
	handler := newWebParallelSnapshotHandler()
	workerTarget := startWorker(t, handler)
	runtime := startDexService(t, DexServiceTestConfig{
		BackendType:        backendType,
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
	var executeRequests []*dexpb.InvokeExecuteMethodRequest
	for _, event := range events {
		execute := event.GetStepExecuteCompleted()
		if execute == nil || execute.GetExecution().GetStepType() == handler.rootStep {
			continue
		}
		executeRequests = append(executeRequests, execute.GetRequest())
	}
	require.Len(t, executeRequests, 2)
	values := make([]*dexpb.Value, 0, len(executeRequests))
	for _, executeRequest := range executeRequests {
		require.NotNil(t, executeRequest)
		require.Len(t, executeRequest.GetAttributes(), 1)
		require.Equal(t, "snapshot", executeRequest.GetAttributes()[0].GetKey())
		values = append(values, executeRequest.GetAttributes()[0].GetValue())
	}
	loadedValues := loadWebBlobValues(t, ctx, runtime.FlowClient, values)
	for _, executeRequest := range executeRequests {
		require.Equal(
			t,
			largeWebTestValue("root-snapshot"),
			resolvedWebStringValue(executeRequest.GetAttributes()[0].GetValue(), loadedValues),
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

func testWebConditionResults(
	t *testing.T,
	backendType service.BackendType,
	durability dexpb.StepDurability,
	conditionType string,
) {
	flowType := fmt.Sprintf("web-step-input-%s-%s", durability, conditionType)
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
		StepOptions: &dexpb.StepOptions{
			WaitForTimeoutSeconds: 11,
			ExecuteTimeoutSeconds: 13,
			WaitForRetryPolicy: &dexpb.RetryPolicy{
				InitialIntervalSeconds: 2,
				BackoffCoefficient:     3,
				MaximumIntervalSeconds: 4,
				MaximumAttempts:        5,
				TotalDurationSeconds:   60,
			},
			ExecuteRetryPolicy: &dexpb.RetryPolicy{
				InitialIntervalSeconds: 3,
				BackoffCoefficient:     4,
				MaximumIntervalSeconds: 5,
				MaximumAttempts:        6,
				TotalDurationSeconds:   70,
			},
		},
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
				StepDurability: ptr.Any(durability),
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
	waitForEvent := firstStepEvent(events)
	require.NotNil(t, waitForEvent)
	executeEvent := firstExecuteEvent(events)
	require.NotNil(t, executeEvent)
	waitForRequest := waitForEvent.GetRequest()
	executeRequest := executeEvent.GetRequest()
	require.NotNil(t, waitForRequest)
	require.NotNil(t, executeRequest)
	waitForTotalDuration := int32(60)
	executeTotalDuration := int32(70)
	if durability == dexpb.StepDurability_STEP_DURABILITY_SYNC &&
		backendType == service.BackendTypeTemporal {
		waitForTotalDuration = 20
		executeTotalDuration = 20
	}
	assertStepMethodOptions(t, waitForEvent.GetExecution().GetMethodOptions(), 11, 2, 3, 4, 5, waitForTotalDuration)
	assertStepMethodOptions(t, executeEvent.GetExecution().GetMethodOptions(), 13, 3, 4, 5, 6, executeTotalDuration)
	require.Len(t, waitForRequest.GetAttributes(), 1)
	require.Len(t, executeRequest.GetAttributes(), 1)
	require.Len(t, executeRequest.GetStepExeLocals(), 1)
	values := []*dexpb.Value{
		waitForRequest.GetStepInput(),
		waitForRequest.GetAttributes()[0].GetValue(),
		executeRequest.GetStepInput(),
		executeRequest.GetAttributes()[0].GetValue(),
		executeRequest.GetStepExeLocals()[0].GetValue(),
	}
	for _, channelResult := range executeRequest.GetConditionResults().GetChannelResults() {
		values = append(values, channelResult.GetValues()...)
	}
	loadedValues := loadWebBlobValues(t, ctx, runtime.FlowClient, values)
	require.Equal(
		t,
		largeWebTestValue("condition-step-input"),
		resolvedWebStringValue(waitForRequest.GetStepInput(), loadedValues),
	)
	require.Equal(
		t,
		largeWebTestValue("condition-attribute"),
		resolvedWebStringValue(waitForRequest.GetAttributes()[0].GetValue(), loadedValues),
	)
	require.Equal(
		t,
		largeWebTestValue("condition-step-input"),
		resolvedWebStringValue(executeRequest.GetStepInput(), loadedValues),
	)
	require.Equal(
		t,
		largeWebTestValue("condition-attribute"),
		resolvedWebStringValue(executeRequest.GetAttributes()[0].GetValue(), loadedValues),
	)
	require.Equal(t, "condition-local", executeRequest.GetStepExeLocals()[0].GetKey())
	require.Equal(
		t,
		largeWebTestValue("condition-local"),
		resolvedWebStringValue(executeRequest.GetStepExeLocals()[0].GetValue(), loadedValues),
	)
	if conditionType == "any" {
		require.Len(t, executeRequest.GetConditionResults().GetChannelResults(), 1)
		require.Len(t, executeRequest.GetConditionResults().GetTimerResults(), 2)
		require.Equal(
			t,
			largeWebTestValue("condition-channel-value"),
			resolvedWebStringValue(
				executeRequest.GetConditionResults().GetChannelResults()[0].GetValues()[0],
				loadedValues,
			),
		)
		for _, result := range executeRequest.GetConditionResults().GetTimerResults() {
			require.Equal(t, dexpb.ConditionStatus_CONDITION_STATUS_WAITING, result.GetConditionStatus())
		}
	} else {
		require.Empty(t, executeRequest.GetConditionResults().GetChannelResults())
		require.Len(t, executeRequest.GetConditionResults().GetTimerResults(), 2)
		for _, result := range executeRequest.GetConditionResults().GetTimerResults() {
			require.Equal(t, dexpb.ConditionStatus_CONDITION_STATUS_COMPLETED, result.GetConditionStatus())
		}
	}
}

func assertStepMethodOptions(
	t *testing.T,
	options *dexpb.StepMethodOptions,
	timeout int32,
	initialInterval int32,
	backoff float32,
	maximumInterval int32,
	maximumAttempts int32,
	totalDuration int32,
) {
	t.Helper()
	require.Equal(t, timeout, options.GetTimeoutSeconds())
	require.Equal(t, initialInterval, options.GetRetryPolicy().GetInitialIntervalSeconds())
	require.Equal(t, backoff, options.GetRetryPolicy().GetBackoffCoefficient())
	require.Equal(t, maximumInterval, options.GetRetryPolicy().GetMaximumIntervalSeconds())
	require.Equal(t, maximumAttempts, options.GetRetryPolicy().GetMaximumAttempts())
	require.Equal(t, totalDuration, options.GetRetryPolicy().GetTotalDurationSeconds())
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
	require.NotNil(t, firstStep.GetRequest())
	require.NotNil(t, firstExecute.GetRequest())
	assertStepMethodRequestValues(
		t,
		ctx,
		runtime.FlowClient,
		firstStep.GetRequest().GetStepInput(),
		firstStep.GetRequest().GetAttributes(),
		stepInput,
		attributePayload,
	)
	assertStepMethodRequestValues(
		t,
		ctx,
		runtime.FlowClient,
		firstExecute.GetRequest().GetStepInput(),
		firstExecute.GetRequest().GetAttributes(),
		stepInput,
		attributePayload,
	)

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
	continuedStep := firstStepEvent(continuedEvents)
	require.NotNil(t, continuedStep)
	continuedExecute := firstExecuteEvent(continuedEvents)
	require.NotNil(t, continuedExecute)
	assertStepMethodRequestValues(
		t,
		ctx,
		runtime.FlowClient,
		continuedStep.GetRequest().GetStepInput(),
		continuedStep.GetRequest().GetAttributes(),
		stepInput,
		attributePayload,
	)
	assertStepMethodRequestValues(
		t,
		ctx,
		runtime.FlowClient,
		continuedExecute.GetRequest().GetStepInput(),
		continuedExecute.GetRequest().GetAttributes(),
		stepInput,
		attributePayload,
	)
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
	unknownStoreBlobID := "unknown-store|" + strings.SplitN(
		closeOutput.GetInternalBlobIdForStringValue(),
		"|",
		2,
	)[1]
	partiallyLoaded, err := runtime.FlowClient.LoadBlobs(
		ctx,
		&dexpb.LoadBlobsRequest{Values: []*dexpb.Value{
			closeOutput,
			{Kind: &dexpb.Value_InternalBlobIdForStringValue{
				InternalBlobIdForStringValue: unknownStoreBlobID,
			}},
		}},
	)
	require.NoError(t, err)
	require.Len(t, partiallyLoaded.GetValues(), 1)
	require.Contains(t, partiallyLoaded.GetValues(), closeOutput.GetInternalBlobIdForStringValue())

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
	if durability == dexpb.StepDurability_STEP_DURABILITY_ASYNC && *dexServerAddress == "" {
		require.NoError(t, os.RemoveAll(blobDirectory))
		unavailableBlobs, loadErr := runtime.FlowClient.LoadBlobs(
			ctx,
			&dexpb.LoadBlobsRequest{Values: []*dexpb.Value{closeOutput}},
		)
		require.NoError(t, loadErr)
		require.Empty(t, unavailableBlobs.GetValues())
		unavailableEvents, _ := getAllWebHistoryEvents(
			t, ctx, runtime.FlowClient, flowID, startResponse.GetRunId(),
		)
		require.True(t, firstStepEvent(unavailableEvents).GetInputUnavailable())
		require.True(t, firstExecuteEvent(unavailableEvents).GetInputUnavailable())
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
	channelResults := executeEvent.GetRequest().GetConditionResults().GetChannelResults()
	require.Len(t, channelResults, 4)
	values := make([]*dexpb.Value, 0, len(channelResults))
	for _, result := range channelResults {
		values = append(values, result.GetValues()...)
	}
	loadedValues := loadWebBlobValues(t, ctx, runtime.FlowClient, values)
	for index, result := range channelResults {
		require.Equal(t, signal.SignalName, result.GetChannelName())
		require.Equal(t, dexpb.ConditionStatus_CONDITION_STATUS_COMPLETED, result.GetConditionStatus())
		require.Len(t, result.GetValues(), 1)
		require.Equal(
			t,
			largeWebTestValue(fmt.Sprintf("channel-value-%d", index)),
			resolvedWebStringValue(result.GetValues()[0], loadedValues),
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
	require.Nil(t, failedEvent.GetExecution().GetLastFailureInfo())
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
	require.False(t, failedEvent.GetInputUnavailable())
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
	ctx context.Context,
	flowClient dexpb.FlowServiceClient,
	stepInput *dexpb.Value,
	attributes []*dexpb.KV,
	expectedInput string,
	expectedAttribute []byte,
) {
	t.Helper()
	require.Len(t, attributes, 1)
	require.Equal(t, "web-test-attribute", attributes[0].GetKey())
	loadedValues := loadWebBlobValues(
		t,
		ctx,
		flowClient,
		[]*dexpb.Value{stepInput, attributes[0].GetValue()},
	)
	require.Equal(t, expectedInput, resolvedWebStringValue(stepInput, loadedValues))
	attribute := attributes[0].GetValue()
	if blobID := attribute.GetInternalBlobIdForObjValue(); blobID != "" {
		attribute = loadedValues[blobID]
	}
	require.Equal(t, expectedAttribute, attribute.GetObjValue().GetPayload())
}

func loadWebBlobValues(
	t *testing.T,
	ctx context.Context,
	flowClient dexpb.FlowServiceClient,
	values []*dexpb.Value,
) map[string]*dexpb.Value {
	t.Helper()
	var blobValues []*dexpb.Value
	blobIDs := map[string]struct{}{}
	for _, value := range values {
		blobID := value.GetInternalBlobIdForStringValue()
		if blobID == "" {
			blobID = value.GetInternalBlobIdForObjValue()
		}
		if blobID == "" {
			continue
		}
		if _, exists := blobIDs[blobID]; !exists {
			blobIDs[blobID] = struct{}{}
			blobValues = append(blobValues, value)
		}
	}
	if len(blobValues) == 0 {
		return nil
	}
	response, err := flowClient.LoadBlobs(ctx, &dexpb.LoadBlobsRequest{Values: blobValues})
	require.NoError(t, err)
	require.Len(t, response.GetValues(), len(blobValues))
	return response.GetValues()
}

func resolvedWebStringValue(value *dexpb.Value, loadedValues map[string]*dexpb.Value) string {
	if blobID := value.GetInternalBlobIdForStringValue(); blobID != "" {
		return loadedValues[blobID].GetStringValue()
	}
	return value.GetStringValue()
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
