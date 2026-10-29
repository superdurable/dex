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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex/ptr"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/server-zd/integ/workflow/signal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestWebAPITemporal(t *testing.T) {
	t.Skip("#1003: server-zd Web API history assertions need a binary harness port")
	testWebAPI(t)
}

func testWebAPI(t *testing.T) {
	for _, lazyLoading := range []bool{true, false} {
		t.Run(fmt.Sprintf("current-state-lazy-%t", lazyLoading), func(t *testing.T) { testWebCurrentState(t, lazyLoading) })
	}
	t.Run("set-attributes-history", func(t *testing.T) { testWebSetAttributesHistory(t) })
	t.Run("parallel-attribute-snapshots", func(t *testing.T) { testWebParallelAttributeSnapshots(t) })
	t.Run("parallel-same-type-failures", func(t *testing.T) { testWebParallelSameTypeFailures(t) })
	t.Run("force-closed-pending-step", func(t *testing.T) { testWebForceClosedPendingStep(t) })
	t.Run("force-closed-pending-wait-for", func(t *testing.T) { testWebForceClosedPendingWaitFor(t) })
}

func testWebForceClosedPendingStep(t *testing.T) {
	handler := newCooperativeStopHandler()
	workerTarget := startWorker(t, handler)
	runtime := startDexService(t, serverZDTestConfig{})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	flowID := "web-force-closed-pending-" + uuid.NewString()
	startResponse, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           cooperativeStopFlowType,
		FlowTimeoutSeconds: 20,
		StartStepType:      cooperativeStopStepType,
		FlowStartOptions: &dexpb.FlowStartOptions{FlowConfigOverride: &dexpb.FlowConfig{
			StepDurability: ptr.Any(dexpb.StepDurability_STEP_DURABILITY_SYNC),
			WorkerTarget:   workerTarget,
		}},
	})
	require.NoError(t, err)
	select {
	case <-handler.executeStarted:
	case <-ctx.Done():
		require.FailNow(t, "Execute did not start", ctx.Err())
	}
	_, err = runtime.FlowClient.StopFlow(ctx, &dexpb.StopFlowRequest{
		FlowId:   flowID,
		RunId:    startResponse.GetRunId(),
		StopType: dexpb.StopType_STOP_TYPE_TERMINATE,
	})
	require.NoError(t, err)
	result, err := runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId: flowID,
		RunId:  startResponse.GetRunId(),
	})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_TERMINATED, result.GetFlowStatus())

	events, _ := getAllWebHistoryEvents(
		t,
		ctx,
		runtime.FlowClient,
		flowID,
		startResponse.GetRunId(),
	)
	var closed *dexpb.FlowClosedHistoryEvent
	var pendingEvent *dexpb.FlowHistoryEvent
	for _, event := range events {
		if event.GetFlowClosed() != nil {
			closed = event.GetFlowClosed()
		}
		if event.GetStepExecutePending() != nil {
			pendingEvent = event
		}
		require.Nil(t, event.GetStepExecuteCompleted())
		require.Nil(t, event.GetStepExecuteFailed())
	}
	require.NotNil(t, closed)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_TERMINATED, closed.GetFlowStatus())
	require.NotNil(t, pendingEvent)
	require.Less(t, pendingEvent.GetEventId(), events[len(events)-1].GetEventId())
	require.NoError(t, pendingEvent.GetEventTime().CheckValid())
	pending := pendingEvent.GetStepExecutePending()
	require.Contains(t, []dexpb.PendingStepMethodPhase{
		dexpb.PendingStepMethodPhase_PENDING_STEP_METHOD_PHASE_SCHEDULED,
		dexpb.PendingStepMethodPhase_PENDING_STEP_METHOD_PHASE_STARTED,
	}, pending.GetPhase())
	require.NotNil(t, pending.GetInput())
	require.False(t, pending.GetInput().GetUnavailable())
	methodContext := pending.GetContext()
	require.Equal(t, cooperativeStopStepType+"-1", methodContext.GetStepExecutionId())
	require.Equal(t, startingStepExecutionID, methodContext.GetFromStepExecutionId())
	require.Equal(t, cooperativeStopStepType, methodContext.GetStepType())
	require.Equal(t, dexpb.StepDurability_STEP_DURABILITY_SYNC, methodContext.GetDurability())
	require.Equal(t, int32(1), methodContext.GetFinalAttempt())
	require.NoError(t, methodContext.GetStartedTime().CheckValid())
	require.NoError(t, methodContext.GetDuration().CheckValid())
	require.Equal(t, methodContext.GetStartedTime(), pendingEvent.GetEventTime())
}

type webPendingWaitForHandler struct {
	dexpb.UnimplementedWorkerServiceServer
	waitForStartedOnce sync.Once
	waitForStarted     chan struct{}
}

func newWebPendingWaitForHandler() *webPendingWaitForHandler {
	return &webPendingWaitForHandler{waitForStarted: make(chan struct{})}
}

func (h *webPendingWaitForHandler) InvokeWaitForMethod(
	ctx context.Context,
	_ *dexpb.InvokeWaitForMethodRequest,
) (*dexpb.InvokeWaitForMethodResponse, error) {
	h.waitForStartedOnce.Do(func() { close(h.waitForStarted) })
	<-ctx.Done()
	return nil, ctx.Err()
}

func (h *webPendingWaitForHandler) InvokeExecuteMethod(
	context.Context,
	*dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	return nil, status.Error(codes.Internal, "Execute should not start")
}

func testWebForceClosedPendingWaitFor(t *testing.T) {
	handler := newWebPendingWaitForHandler()
	workerTarget := startWorker(t, handler)
	runtime := startDexService(t, serverZDTestConfig{})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	flowID := "web-force-closed-pending-wait-for-" + uuid.NewString()
	startResponse, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           "pending-wait-for",
		FlowTimeoutSeconds: 20,
		StartStepType:      "waiting-step",
		FlowStartOptions: &dexpb.FlowStartOptions{FlowConfigOverride: &dexpb.FlowConfig{
			StepDurability: ptr.Any(dexpb.StepDurability_STEP_DURABILITY_SYNC),
			WorkerTarget:   workerTarget,
		}},
	})
	require.NoError(t, err)
	select {
	case <-handler.waitForStarted:
	case <-ctx.Done():
		require.FailNow(t, "WaitFor did not start", ctx.Err())
	}
	_, err = runtime.FlowClient.StopFlow(ctx, &dexpb.StopFlowRequest{
		FlowId:   flowID,
		RunId:    startResponse.GetRunId(),
		StopType: dexpb.StopType_STOP_TYPE_TERMINATE,
	})
	require.NoError(t, err)
	result, err := runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId: flowID,
		RunId:  startResponse.GetRunId(),
	})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_TERMINATED, result.GetFlowStatus())

	events, _ := getAllWebHistoryEvents(
		t,
		ctx,
		runtime.FlowClient,
		flowID,
		startResponse.GetRunId(),
	)
	var closed *dexpb.FlowClosedHistoryEvent
	var pendingEvent *dexpb.FlowHistoryEvent
	for _, event := range events {
		if event.GetFlowClosed() != nil {
			closed = event.GetFlowClosed()
		}
		if event.GetStepWaitForPending() != nil {
			pendingEvent = event
		}
		require.Nil(t, event.GetStepWaitForCompleted())
		require.Nil(t, event.GetStepWaitForFailed())
		require.Nil(t, event.GetStepExecutePending())
	}
	require.NotNil(t, closed)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_TERMINATED, closed.GetFlowStatus())
	require.NotNil(t, pendingEvent)
	require.Less(t, pendingEvent.GetEventId(), events[len(events)-1].GetEventId())
	require.NoError(t, pendingEvent.GetEventTime().CheckValid())
	pending := pendingEvent.GetStepWaitForPending()
	require.Contains(t, []dexpb.PendingStepMethodPhase{
		dexpb.PendingStepMethodPhase_PENDING_STEP_METHOD_PHASE_SCHEDULED,
		dexpb.PendingStepMethodPhase_PENDING_STEP_METHOD_PHASE_STARTED,
	}, pending.GetPhase())
	require.NotNil(t, pending.GetInput())
	require.False(t, pending.GetInput().GetUnavailable())
	methodContext := pending.GetContext()
	require.Equal(t, "waiting-step-1", methodContext.GetStepExecutionId())
	require.Equal(t, startingStepExecutionID, methodContext.GetFromStepExecutionId())
	require.Equal(t, "waiting-step", methodContext.GetStepType())
	require.Equal(t, dexpb.StepDurability_STEP_DURABILITY_SYNC, methodContext.GetDurability())
	require.Equal(t, int32(1), methodContext.GetFinalAttempt())
	require.NoError(t, methodContext.GetStartedTime().CheckValid())
	require.NoError(t, methodContext.GetDuration().CheckValid())
	require.Equal(t, methodContext.GetStartedTime(), pendingEvent.GetEventTime())
}

func testWebParallelSameTypeFailures(t *testing.T) {
	handler := &webParallelFailureHandler{
		flowType:        "web-parallel-same-type-failures",
		failureObserved: make(chan struct{}),
	}
	workerTarget := startWorker(t, handler)
	runtime := startDexService(t, serverZDTestConfig{})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	flowID := handler.flowType + "-" + uuid.NewString()
	startResponse, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           handler.flowType,
		FlowTimeoutSeconds: 20,
		StartStepType:      "root",
		StepOptions:        &dexpb.StepOptions{SkipWaitFor: true},
		FlowStartOptions: &dexpb.FlowStartOptions{FlowConfigOverride: &dexpb.FlowConfig{
			StepDurability: ptr.Any(dexpb.StepDurability_STEP_DURABILITY_SYNC),
			WorkerTarget:   workerTarget,
		}},
	})
	require.NoError(t, err)
	select {
	case <-handler.failureObserved:
	case <-ctx.Done():
		require.NoError(t, ctx.Err())
	}

	var activeSteps []*dexpb.ActiveStepExecutionState
	require.Eventually(t, func() bool {
		state, stateErr := runtime.FlowClient.GetFlowState(ctx, &dexpb.GetFlowStateRequest{
			FlowId: flowID,
			RunId:  startResponse.GetRunId(),
		})
		if stateErr != nil {
			return false
		}
		activeUserSteps := userActiveStepExecutions(state)
		if len(activeUserSteps) != 2 {
			return false
		}
		for _, activeStep := range activeUserSteps {
			if activeStep.GetStepType() != "branch" || activeStep.GetLastFailureInfo() == nil {
				return false
			}
		}
		activeSteps = activeUserSteps
		return true
	}, 2*time.Second, 50*time.Millisecond)
	for _, activeStep := range activeSteps {
		failure := activeStep.GetLastFailureInfo()
		require.Equal(t, int32(1), failure.GetAttempt())
		require.Equal(
			t,
			activeStep.GetStepExecutionId(),
			failure.GetDetails().GetOriginalWorkerErrorDetail(),
		)
	}

	result, err := runtime.FlowClient.WaitForFlow(
		ctx,
		&dexpb.WaitForFlowRequest{FlowId: flowID},
	)
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, result.GetFlowStatus())
}

type webParallelFailureHandler struct {
	dexpb.UnimplementedWorkerServiceServer
	flowType        string
	failureCount    atomic.Int32
	failureObserved chan struct{}
}

func (h *webParallelFailureHandler) InvokeExecuteMethod(
	_ context.Context,
	request *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	if request.GetFlowType() != h.flowType {
		return nil, fmt.Errorf("unexpected flow type %q", request.GetFlowType())
	}
	if request.GetStepType() == "root" {
		options := webParallelFailureStepOptions()
		return &dexpb.InvokeExecuteMethodResponse{StepDecision: &dexpb.StepDecision{
			NextSteps: []*dexpb.StepMovement{
				{StepType: "branch", StepOptions: options},
				{StepType: "branch", StepOptions: options},
			},
		}}, nil
	}
	if request.GetStepType() != "branch" {
		return nil, fmt.Errorf("unexpected step type %q", request.GetStepType())
	}
	if request.GetContext().GetAttempt() == 1 {
		if h.failureCount.Add(1) == 2 {
			close(h.failureObserved)
		}
		return nil, webParallelFailureError(request.GetContext().GetStepExecutionId())
	}
	return &dexpb.InvokeExecuteMethodResponse{StepDecision: &dexpb.StepDecision{
		CloseDecision: &dexpb.CloseDecision{
			CloseDecisionType: dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_GRACEFUL_COMPLETE,
		},
	}}, nil
}

func webParallelFailureStepOptions() *dexpb.StepOptions {
	return &dexpb.StepOptions{
		SkipWaitFor: true,
		ExecuteRetryPolicy: &dexpb.RetryPolicy{
			InitialIntervalSeconds: 3,
			BackoffCoefficient:     1,
			MaximumIntervalSeconds: 3,
			MaximumAttempts:        2,
		},
	}
}

func webParallelFailureError(stepExecutionID string) error {
	workerStatus, err := status.New(
		codes.Internal,
		"parallel branch failure",
	).WithDetails(&dexpb.WorkerErrorResponse{
		Detail:     stepExecutionID,
		ErrorType:  "ParallelBranchFailure",
		StackTrace: "parallel worker stack " + stepExecutionID,
	})
	if err != nil {
		return err
	}
	return workerStatus.Err()
}

func testWebParallelAttributeSnapshots(t *testing.T) {
	handler := newWebParallelSnapshotHandler()
	workerTarget := startWorker(t, handler)
	runtime := startDexService(t, serverZDTestConfig{

		ObjectPayloadThresholdBytes: 10,
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
	var executeEvents []*dexpb.StepExecuteCompletedEvent
	for _, event := range events {
		execute := event.GetStepExecuteCompleted()
		if execute == nil || execute.GetContext().GetStepType() == handler.rootStep {
			continue
		}
		executeEvents = append(executeEvents, execute)
	}
	require.Len(t, executeEvents, 2)
	values := make([]*dexpb.Value, 0, len(executeEvents))
	for _, executeEvent := range executeEvents {
		require.NotNil(t, executeEvent.GetInput())
		require.Len(t, executeEvent.GetInput().GetAttributes(), 1)
		require.Equal(t, "snapshot", executeEvent.GetInput().GetAttributes()[0].GetKey())
		values = append(values, executeEvent.GetInput().GetAttributes()[0].GetValue())
	}
	loadedValues := loadWebBlobValues(t, ctx, runtime.FlowClient, flowID, values)
	for _, executeEvent := range executeEvents {
		require.Equal(
			t,
			largeWebTestValue("root-snapshot"),
			resolvedWebStringValue(executeEvent.GetInput().GetAttributes()[0].GetValue(), loadedValues),
		)
		expected := handler.executeRequest(executeEvent.GetContext().GetStepExecutionId())
		require.NotNil(t, expected)
		require.True(t, proto.Equal(stepMethodEventInputFromExecuteRequest(expected), executeEvent.GetInput()))
		require.Equal(t, expected.GetStepType(), executeEvent.GetContext().GetStepType())
		require.Equal(
			t,
			expected.GetContext().GetFromStepExecutionId(),
			executeEvent.GetContext().GetFromStepExecutionId(),
		)
	}
}

func stepMethodEventInputFromExecuteRequest(
	request *dexpb.InvokeExecuteMethodRequest,
) *dexpb.StepMethodEventInput {
	return &dexpb.StepMethodEventInput{
		StepInput:           request.GetStepInput(),
		ConditionResults:    request.GetConditionResults(),
		Attributes:          request.GetAttributes(),
		StepExecutionLocals: request.GetStepExeLocals(),
	}
}

type webParallelSnapshotHandler struct {
	dexpb.UnimplementedWorkerServiceServer
	flowType              string
	rootStep              string
	leftStep              string
	rightStep             string
	requestsMutex         sync.Mutex
	executeRequests       map[string][]byte
	parallelRequestCount  int
	parallelRequestsReady chan struct{}
}

func newWebParallelSnapshotHandler() *webParallelSnapshotHandler {
	return &webParallelSnapshotHandler{
		flowType:              "web-parallel-snapshot",
		rootStep:              "root",
		leftStep:              "left",
		rightStep:             "right",
		executeRequests:       map[string][]byte{},
		parallelRequestsReady: make(chan struct{}),
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
	ctx context.Context,
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
	hasRequest := len(h.executeRequests[request.GetContext().GetStepExecutionId()]) > 0
	h.executeRequests[request.GetContext().GetStepExecutionId()] = data
	if !hasRequest && (request.GetStepType() == h.leftStep || request.GetStepType() == h.rightStep) {
		h.parallelRequestCount++
		if h.parallelRequestCount == 2 {
			close(h.parallelRequestsReady)
		}
	}
	h.requestsMutex.Unlock()
	if request.GetStepType() == h.leftStep || request.GetStepType() == h.rightStep {
		// Capture both branch inputs before either branch writes the shared Attribute.
		select {
		case <-h.parallelRequestsReady:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

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

func testWebCurrentState(t *testing.T, lazyLoading bool) {
	workerTarget := startWorker(t, signal.NewHandler())
	runtime := startDexService(t, serverZDTestConfig{

		LazyLoading: ptr.Any(lazyLoading),

		ObjectPayloadThresholdBytes: 10,
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
		if stateErr != nil {
			return false
		}
		activeUserSteps := userActiveStepExecutions(state)
		if len(activeUserSteps) != 1 {
			return false
		}
		active := activeUserSteps[0]
		return active.GetStepExecutionId() == signal.State1+"-1" &&
			active.GetFromStepExecutionId() == startingStepExecutionID &&
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
	channelResults := executeEvent.GetInput().GetConditionResults().GetChannelResults()
	require.Len(t, channelResults, 4)
	values := make([]*dexpb.Value, 0, len(channelResults))
	for _, result := range channelResults {
		values = append(values, result.GetValues()...)
	}
	loadedValues := loadWebBlobValues(t, ctx, runtime.FlowClient, flowID, values)
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
	assertExternalChannelValuesLoad(t, ctx, runtime.FlowClient, flowID, events)
}

func testWebSetAttributesHistory(t *testing.T) {
	workerTarget := startWorker(t, signal.NewHandler())
	runtime := startDexService(t, serverZDTestConfig{})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowID := signal.WorkflowType + "-web-set-attribute-" + uuid.NewString()
	startResponse, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           signal.WorkflowType,
		FlowTimeoutSeconds: 30,
		StartStepType:      signal.State1,
		FlowStartOptions: &dexpb.FlowStartOptions{FlowConfigOverride: &dexpb.FlowConfig{
			StepDurability: ptr.Any(dexpb.StepDurability_STEP_DURABILITY_ASYNC),
			WorkerTarget:   workerTarget,
		}},
	})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		state, stateErr := runtime.FlowClient.GetFlowState(ctx, &dexpb.GetFlowStateRequest{
			FlowId: flowID,
			RunId:  startResponse.GetRunId(),
		})
		return stateErr == nil && len(userActiveStepExecutions(state)) == 1
	}, 30*time.Second, 50*time.Millisecond)
	_, nextInternalEventID := getAllWebHistoryEvents(
		t, ctx, runtime.FlowClient, flowID, startResponse.GetRunId(),
	)

	attribute := &dexpb.AttributeWrite{
		Key: "web-set-attribute",
		Value: &dexpb.Value{Kind: &dexpb.Value_StringValue{
			StringValue: "updated-from-web",
		}},
	}
	_, err = runtime.FlowClient.SetAttributes(ctx, &dexpb.SetAttributesRequest{
		FlowId:    flowID,
		RunId:     startResponse.GetRunId(),
		RequestId: newRequestID(),
		Attributes: []*dexpb.AttributeWrite{
			attribute,
		},
	})
	require.NoError(t, err)
	waitResponse, err := runtime.FlowClient.WaitForHistoryEvent(ctx, &dexpb.WaitForHistoryEventRequest{
		FlowId:              flowID,
		RunId:               startResponse.GetRunId(),
		NextInternalEventId: nextInternalEventID,
	})
	require.NoError(t, err)
	require.True(t, waitResponse.GetEventAvailable())
	events, _ := getAllWebHistoryEvents(
		t, ctx, runtime.FlowClient, flowID, startResponse.GetRunId(),
	)
	var rpcEvent *dexpb.RpcExecutionCompletedEvent
	for _, event := range events {
		candidate := event.GetRpcExecutionCompleted()
		if candidate.GetIsSetAttributeApi() {
			rpcEvent = candidate
			break
		}
	}
	require.NotNil(t, rpcEvent)
	require.Empty(t, rpcEvent.GetRpcName())
	require.Len(t, rpcEvent.GetUpsertAttributes(), 1)
	require.True(t, proto.Equal(attribute, rpcEvent.GetUpsertAttributes()[0]))
	require.Nil(t, rpcEvent.GetInput())
	require.Nil(t, rpcEvent.GetOutput())
	require.Nil(t, rpcEvent.GetStepDecision())

	messages := make([]*dexpb.ChannelMessage, 4)
	for index := range messages {
		messages[index] = &dexpb.ChannelMessage{ChannelName: signal.SignalName}
	}
	_, err = runtime.FlowClient.PublishToChannel(ctx, &dexpb.PublishToChannelRequest{
		FlowId:   flowID,
		RunId:    startResponse.GetRunId(),
		Messages: messages,
	})
	require.NoError(t, err)
	_, err = runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowID})
	require.NoError(t, err)
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

func firstExecuteEvent(events []*dexpb.FlowHistoryEvent) *dexpb.StepExecuteCompletedEvent {
	for _, event := range events {
		if event.GetStepExecuteCompleted() != nil {
			return event.GetStepExecuteCompleted()
		}
	}
	return nil
}

func loadWebBlobValues(
	t *testing.T,
	ctx context.Context,
	flowClient dexpb.FlowServiceClient,
	flowID string,
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
	response, err := flowClient.LoadBlobs(ctx, &dexpb.LoadBlobsRequest{
		Entries: blobRequestEntries(flowID, blobValues),
	})
	require.NoError(t, err)
	require.Len(t, response.GetValues(), len(blobValues))
	return response.GetValues()
}

func blobRequestEntries(flowID string, values []*dexpb.Value) []*dexpb.LoadBlobRequestEntry {
	entries := make([]*dexpb.LoadBlobRequestEntry, 0, len(values))
	for _, value := range values {
		entries = append(entries, &dexpb.LoadBlobRequestEntry{FlowId: flowID, BlobValue: value})
	}
	return entries
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
	flowID string,
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
	response, err := flowClient.LoadBlobs(ctx, &dexpb.LoadBlobsRequest{
		Entries: blobRequestEntries(flowID, values),
	})
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
