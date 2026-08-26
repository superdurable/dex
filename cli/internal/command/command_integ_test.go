// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

//go:build integration

package command

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/superdurable/dex/gen/dexpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

type testFlowService struct {
	dexpb.UnimplementedFlowServiceServer

	mu                   sync.Mutex
	loadCalls            int
	startCalls           int
	stopCalls            int
	skipTimerCalls       int
	waitStepCalls        int
	updateConfigCalls    int
	continueAsNewCalls   int
	startRequest         *dexpb.StartFlowRequest
	stopRequest          *dexpb.StopFlowRequest
	waitRequest          *dexpb.WaitForFlowRequest
	skipTimerRequest     *dexpb.SkipTimerRequest
	waitStepRequest      *dexpb.WaitForStepCompletionRequest
	updateConfigRequest  *dexpb.UpdateFlowConfigRequest
	continueAsNewRequest *dexpb.TriggerContinueAsNewRequest
	timeTravelCalls      int
	timeTravelRequest    *dexpb.ResetFlowRequest
	waitStarted          chan struct{}
	waitOnce             sync.Once
}

func (s *testFlowService) StartFlow(
	_ context.Context,
	request *dexpb.StartFlowRequest,
) (*dexpb.StartFlowResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.startCalls++
	s.startRequest = request
	return &dexpb.StartFlowResponse{RunId: "run-started"}, nil
}

func (s *testFlowService) HealthCheck(context.Context, *emptypb.Empty) (*dexpb.HealthInfo, error) {
	return &dexpb.HealthInfo{Condition: "SERVING", Hostname: "test", Duration: 7}, nil
}

func (s *testFlowService) SearchFlows(
	context.Context,
	*dexpb.SearchFlowsRequest,
) (*dexpb.SearchFlowsResponse, error) {
	return &dexpb.SearchFlowsResponse{FlowRuns: []*dexpb.SearchFlowsResponseEntry{{
		FlowId: "flow-1", RunId: "run-1", FlowType: "OrderFlow",
		FlowStatus: dexpb.FlowStatus_FLOW_STATUS_RUNNING,
		IndexedAttributes: []*dexpb.KV{{
			Key: "LargeValue",
			Value: &dexpb.Value{Kind: &dexpb.Value_InternalBlobIdForStringValue{
				InternalBlobIdForStringValue: "blob-1",
			}},
		}},
	}}}, nil
}

func (s *testFlowService) LoadBlobs(
	context.Context,
	*dexpb.LoadBlobsRequest,
) (*dexpb.LoadBlobsResponse, error) {
	s.mu.Lock()
	s.loadCalls++
	s.mu.Unlock()
	return &dexpb.LoadBlobsResponse{Values: map[string]*dexpb.Value{
		"blob-1": {Kind: &dexpb.Value_StringValue{StringValue: "hydrated"}},
	}}, nil
}

func (s *testFlowService) StopFlow(
	_ context.Context,
	request *dexpb.StopFlowRequest,
) (*emptypb.Empty, error) {
	s.mu.Lock()
	s.stopRequest = request
	s.stopCalls++
	s.mu.Unlock()
	return &emptypb.Empty{}, nil
}

func (s *testFlowService) WaitForFlow(
	_ context.Context,
	request *dexpb.WaitForFlowRequest,
) (*dexpb.FlowResult, error) {
	s.mu.Lock()
	s.waitRequest = request
	s.mu.Unlock()
	return &dexpb.FlowResult{
		FlowStatus: dexpb.FlowStatus_FLOW_STATUS_COMPLETED,
		Results: []*dexpb.StepCompletionOutput{{
			CompletedStepType: "ShipOrder", CompletedStepExecutionId: "ShipOrder-1",
			CompletedStepOutput: &dexpb.Value{Kind: &dexpb.Value_InternalBlobIdForStringValue{InternalBlobIdForStringValue: "blob-1"}},
		}},
	}, nil
}

func (s *testFlowService) SkipTimer(
	_ context.Context,
	request *dexpb.SkipTimerRequest,
) (*emptypb.Empty, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.skipTimerCalls++
	s.skipTimerRequest = request
	return &emptypb.Empty{}, nil
}

func (s *testFlowService) WaitForStepCompletion(
	_ context.Context,
	request *dexpb.WaitForStepCompletionRequest,
) (*dexpb.WaitForStepCompletionResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.waitStepCalls++
	s.waitStepRequest = request
	return &dexpb.WaitForStepCompletionResponse{}, nil
}

func (s *testFlowService) UpdateFlowConfig(
	_ context.Context,
	request *dexpb.UpdateFlowConfigRequest,
) (*emptypb.Empty, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updateConfigCalls++
	s.updateConfigRequest = request
	return &emptypb.Empty{}, nil
}

func (s *testFlowService) TriggerContinueAsNew(
	_ context.Context,
	request *dexpb.TriggerContinueAsNewRequest,
) (*emptypb.Empty, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.continueAsNewCalls++
	s.continueAsNewRequest = request
	return &emptypb.Empty{}, nil
}

func (s *testFlowService) ResetFlow(
	_ context.Context,
	request *dexpb.ResetFlowRequest,
) (*dexpb.ResetFlowResponse, error) {
	s.mu.Lock()
	s.timeTravelCalls++
	s.timeTravelRequest = request
	s.mu.Unlock()
	return &dexpb.ResetFlowResponse{RunId: "run-2"}, nil
}

func (s *testFlowService) GetFlowSummary(
	_ context.Context,
	request *dexpb.GetFlowSummaryRequest,
) (*dexpb.GetFlowSummaryResponse, error) {
	flowStatus := dexpb.FlowStatus_FLOW_STATUS_RUNNING
	if request.GetFlowId() == "terminal-flow" {
		flowStatus = dexpb.FlowStatus_FLOW_STATUS_COMPLETED
	}
	return &dexpb.GetFlowSummaryResponse{
		FlowExecutionId: &dexpb.FlowExecutionID{FlowId: request.GetFlowId(), RunId: "run-1"},
		FirstRunId:      "run-1", FlowType: "OrderFlow", FlowStatus: flowStatus,
	}, nil
}

func (s *testFlowService) GetHistoryEvents(
	context.Context,
	*dexpb.GetHistoryEventsRequest,
) (*dexpb.GetHistoryEventsResponse, error) {
	return &dexpb.GetHistoryEventsResponse{
		Events: []*dexpb.FlowHistoryEvent{{
			EventId: 1,
			Payload: &dexpb.FlowHistoryEvent_ChannelExternalPublish{
				ChannelExternalPublish: &dexpb.ChannelExternalPublishEvent{Messages: []*dexpb.ChannelMessage{{
					ChannelName: "commands",
					Value:       &dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: "go"}},
				}}},
			},
		}},
		NextInternalEventId: 2,
	}, nil
}

func (s *testFlowService) GetFlowState(
	context.Context,
	*dexpb.GetFlowStateRequest,
) (*dexpb.GetFlowStateResponse, error) {
	return &dexpb.GetFlowStateResponse{
		FlowConfig: &dexpb.FlowConfig{},
		Attributes: []*dexpb.KV{{
			Key: "status", Value: &dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: "ready"}},
		}},
	}, nil
}

func (s *testFlowService) WaitForHistoryEvent(
	ctx context.Context,
	_ *dexpb.WaitForHistoryEventRequest,
) (*dexpb.WaitForHistoryEventResponse, error) {
	s.waitOnce.Do(func() { close(s.waitStarted) })
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestHealthAndGenericAPICallUseFlowService(t *testing.T) {
	service, address := startTestFlowService(t)
	if service == nil {
		t.Fatal("test FlowService is nil")
	}

	health := executeTestCommand(t, nil, "health", "--server", address)
	if health["condition"] != "SERVING" || health["hostname"] != "test" {
		t.Fatalf("unexpected health response: %#v", health)
	}

	raw := executeTestCommand(t, []byte("{}"), "api", "call", "HealthCheck", "--data", "-", "--server", address)
	if raw["condition"] != "SERVING" {
		t.Fatalf("unexpected api response: %#v", raw)
	}
}

func TestSearchHydratesByDefaultAndCanReturnReferences(t *testing.T) {
	service, address := startTestFlowService(t)

	hydrated := executeTestCommand(t, nil, "flow", "search", "--server", address)
	flows := hydrated["flows"].([]any)
	first := flows[0].(map[string]any)
	attributes := first["indexedAttributes"].([]any)
	if attributes[0].(map[string]any)["value"] != "hydrated" {
		t.Fatalf("expected hydrated attribute: %#v", hydrated)
	}

	references := executeTestCommand(t, nil, "flow", "search", "--server", address, "--no-hydrate")
	flows = references["flows"].([]any)
	first = flows[0].(map[string]any)
	attributes = first["indexedAttributes"].([]any)
	reference := attributes[0].(map[string]any)["value"].(map[string]any)["__dexBlobReference"].(map[string]any)
	if reference["id"] != "blob-1" || reference["kind"] != "string" {
		t.Fatalf("unexpected blob reference: %#v", reference)
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.loadCalls != 1 {
		t.Fatalf("expected one hydration call, got %d", service.loadCalls)
	}
}

func TestMutationRequiresYesBeforeSendingRequest(t *testing.T) {
	service, address := startTestFlowService(t)
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	app := NewApp(bytes.NewReader(nil), stdout, stderr)
	err := app.Execute(context.Background(), []string{
		"flow", "stop", "flow-1", "--run-id", "run-1", "--type", "cancel", "--server", address,
	})
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("expected confirmation error, got %v", err)
	}
	service.mu.Lock()
	if service.stopCalls != 0 {
		service.mu.Unlock()
		t.Fatalf("stop was called without confirmation")
	}
	service.mu.Unlock()

	result := executeTestCommand(t, nil,
		"flow", "stop", "flow-1", "--run-id", "run-1", "--type", "cancel", "--yes", "--server", address,
	)
	if result["stopped"] != true {
		t.Fatalf("unexpected stop result: %#v", result)
	}
}

func TestTimeTravelRequiresConfirmationAndMapsStepMethod(t *testing.T) {
	service, address := startTestFlowService(t)
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	app := NewApp(bytes.NewReader(nil), stdout, stderr)
	err := app.Execute(context.Background(), []string{
		"flow", "time-travel", "flow-1", "--run-id", "run-1", "--type", "step-execution-id",
		"--target", "ChargeOrder-2", "--step-method", "execute", "--reason", "retry fixed code", "--server", address,
	})
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("expected confirmation error, got %v", err)
	}
	service.mu.Lock()
	if service.timeTravelCalls != 0 {
		service.mu.Unlock()
		t.Fatal("time travel was called without confirmation")
	}
	service.mu.Unlock()

	result := executeTestCommand(t, nil,
		"flow", "time-travel", "flow-1", "--run-id", "run-1", "--type", "step-execution-id",
		"--target", "ChargeOrder-2", "--step-method", "execute", "--reason", "retry fixed code", "--yes", "--server", address,
	)
	if result["previousRunId"] != "run-1" || result["runId"] != "run-2" {
		t.Fatalf("unexpected time travel result: %#v", result)
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.timeTravelCalls != 1 ||
		service.timeTravelRequest.GetStepExecutionId() != "ChargeOrder-2" ||
		service.timeTravelRequest.GetStepMethod() != dexpb.FlowResetStepMethod_FLOW_RESET_STEP_METHOD_EXECUTE {
		t.Fatalf("unexpected time travel request: %#v", service.timeTravelRequest)
	}
}

func TestFlowClientOperationsMapSDKEquivalentRequests(t *testing.T) {
	service, address := startTestFlowService(t)
	startResult := executeTestCommand(t, nil,
		"flow", "start", "flow-1", "--flow-type", "OrderFlow", "--start-step-type", "StartOrder",
		"--input", `{"order":42}`, "--attributes", `[{"key":"status","value":"new","index":{"type":"keyword"},"sync":true}]`,
		"--config", `{"activeStepSearchMode":"all","continueAsNewThreshold":12,"stepDurability":"sync","workerTarget":{"address":"127.0.0.1:9000","headless":true}}`,
		"--retry-policy", `{"initialInterval":"1500ms","backoffCoefficient":2,"maximumInterval":"5s","maximumAttempts":3}`,
		"--step-options", `{"skipWaitFor":true}`, "--flow-timeout", "90s", "--flow-timeout-policy", "cancel",
		"--id-reuse-policy", "not-running", "--start-delay", "1500ms", "--ignore-already-started", "--request-id", "request-1", "--yes", "--server", address,
	)
	if startResult["runId"] != "run-started" {
		t.Fatalf("unexpected start result: %#v", startResult)
	}

	waitResult := executeTestCommand(t, nil, "flow", "wait", "flow-1", "--needs-results", "--wait-time", "3500ms", "--server", address)
	if waitResult["flowStatus"] != "FLOW_STATUS_COMPLETED" {
		t.Fatalf("unexpected wait result: %#v", waitResult)
	}
	results := waitResult["results"].([]any)
	if results[0].(map[string]any)["completedStepOutput"] != "hydrated" {
		t.Fatalf("wait did not hydrate result values: %#v", waitResult)
	}

	executeTestCommand(t, nil, "flow", "skip-timer", "flow-1", "--step-type", "WaitForPayment", "--execution", "2", "--condition-index", "3", "--yes", "--server", address)
	executeTestCommand(t, nil, "flow", "wait-step", "flow-1", "--step-type", "ShipOrder", "--execution", "2", "--wait-time", "1500ms", "--server", address)
	executeTestCommand(t, nil, "flow", "update-config", "flow-1", "--config", `{"continueAsNewPageSizeInBytes":1024,"attributeStoreNames":[]}`, "--yes", "--server", address)
	executeTestCommand(t, nil, "flow", "trigger-continue-as-new", "flow-1", "--yes", "--server", address)

	service.mu.Lock()
	defer service.mu.Unlock()
	startRequest := service.startRequest
	if service.startCalls != 1 || startRequest.GetFlowType() != "OrderFlow" || startRequest.GetFlowTimeoutSeconds() != 90 || startRequest.GetFlowTimeoutPolicy() != dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_CANCEL || startRequest.GetRequestId() != "request-1" {
		t.Fatalf("unexpected start request: %#v", startRequest)
	}
	if string(startRequest.GetStepInput().GetObjValue().GetPayload()) != `{"order":42}` || !startRequest.GetStepOptions().GetSkipWaitFor() {
		t.Fatalf("unexpected start input: %#v", startRequest)
	}
	if startRequest.GetFlowStartOptions().GetFlowStartDelaySeconds() != 2 || startRequest.GetFlowStartOptions().GetIdReusePolicy() != dexpb.IdReusePolicy_ID_REUSE_POLICY_ALLOW_IF_NO_RUNNING || !startRequest.GetFlowStartOptions().GetFlowAlreadyStartedOptions().GetIgnoreAlreadyStartedError() {
		t.Fatalf("unexpected start options: %#v", startRequest.GetFlowStartOptions())
	}
	attribute := startRequest.GetFlowStartOptions().GetAttributes()[0]
	if attribute.GetKey() != "status" || attribute.GetValue().GetStringValue() != "new" || attribute.GetIndexConfig().GetType() != dexpb.IndexType_INDEX_TYPE_KEYWORD || !attribute.GetSyncConfig().GetEnabled() {
		t.Fatalf("unexpected start attributes: %#v", attribute)
	}
	if startRequest.GetFlowStartOptions().GetFlowConfigOverride().GetActiveStepSearchMode() != dexpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_ENABLED_FOR_ALL || !startRequest.GetFlowStartOptions().GetFlowConfigOverride().GetWorkerTarget().GetIsHeadlessAddress() {
		t.Fatalf("unexpected start config: %#v", startRequest.GetFlowStartOptions().GetFlowConfigOverride())
	}
	if service.waitRequest.GetWaitTimeSeconds() != 4 || !service.waitRequest.GetNeedsResults() {
		t.Fatalf("unexpected wait request: %#v", service.waitRequest)
	}
	if service.skipTimerRequest.GetStepExecutionId() != "WaitForPayment-2" || service.skipTimerRequest.GetTimerConditionIndex() != 3 {
		t.Fatalf("unexpected skip timer request: %#v", service.skipTimerRequest)
	}
	if service.waitStepRequest.GetStepExecutionNumber() != "2" || service.waitStepRequest.GetWaitTimeSeconds() != 2 || service.waitStepRequest.GetRequestId() == "" {
		t.Fatalf("unexpected wait-step request: %#v", service.waitStepRequest)
	}
	if service.updateConfigRequest.GetFlowConfig().GetContinueAsNewPageSizeInBytes() != 1024 || service.updateConfigRequest.GetFlowConfig().AttributeStoreNames == nil || len(service.updateConfigRequest.GetFlowConfig().GetAttributeStoreNames().GetNames()) != 0 {
		t.Fatalf("unexpected update-config request: %#v", service.updateConfigRequest)
	}
	if service.continueAsNewRequest.GetFlowId() != "flow-1" {
		t.Fatalf("unexpected continue-as-new request: %#v", service.continueAsNewRequest)
	}
}

func TestFlowMutationsRequireConfirmation(t *testing.T) {
	service, address := startTestFlowService(t)
	for _, args := range [][]string{
		{"flow", "start", "flow-1", "--flow-type", "OrderFlow", "--server", address},
		{"flow", "skip-timer", "flow-1", "--step-type", "Wait", "--condition-id", "timer", "--server", address},
		{"flow", "update-config", "flow-1", "--config", `{}`, "--server", address},
		{"flow", "trigger-continue-as-new", "flow-1", "--server", address},
	} {
		app := NewApp(bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{})
		if err := app.Execute(context.Background(), args); err == nil || ExitCode(err) != 2 {
			t.Fatalf("expected confirmation error for %v, got %v", args, err)
		}
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.startCalls != 0 || service.skipTimerCalls != 0 || service.updateConfigCalls != 0 || service.continueAsNewCalls != 0 {
		t.Fatalf("mutation reached service without confirmation: %#v", service)
	}
}

func TestFlowClientOperationValidation(t *testing.T) {
	_, address := startTestFlowService(t)
	for _, args := range [][]string{
		{"flow", "start", "flow-1", "--flow-type", "OrderFlow", "--input", "not-json", "--yes", "--server", address},
		{"flow", "start", "flow-1", "--flow-type", "OrderFlow", "--config", `{"activeStepSearchMode":"unknown"}`, "--yes", "--server", address},
		{"flow", "wait", "flow-1", "--wait-time", "not-a-duration", "--server", address},
		{"flow", "skip-timer", "flow-1", "--step-type", "Wait", "--condition-id", "timer", "--condition-index", "0", "--yes", "--server", address},
		{"flow", "wait-step", "flow-1", "--step-type", "Ship", "--execution", "0", "--server", address},
	} {
		app := NewApp(bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{})
		if err := app.Execute(context.Background(), args); err == nil || ExitCode(err) != 2 {
			t.Fatalf("expected usage error for %v, got %v", args, err)
		}
	}
}

func TestStopAndTimeTravelDefaultToCurrentRun(t *testing.T) {
	service, address := startTestFlowService(t)
	executeTestCommand(t, nil, "flow", "stop", "flow-1", "--yes", "--server", address)
	result := executeTestCommand(t, nil, "flow", "time-travel", "flow-1", "--type", "beginning", "--yes", "--server", address)
	if result["previousRunId"] != "" {
		t.Fatalf("unexpected time-travel result: %#v", result)
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.stopRequest.GetRunId() != "" || service.stopRequest.GetStopType() != dexpb.StopType_STOP_TYPE_CANCEL || service.timeTravelRequest.GetRunId() != "" || service.timeTravelRequest.GetReason() != "" {
		t.Fatalf("operations did not target current run: stop=%#v reset=%#v", service.stopRequest, service.timeTravelRequest)
	}
}

func TestHistoryUsesCurrentRunAndReturnsSemanticEvents(t *testing.T) {
	_, address := startTestFlowService(t)
	result := executeTestCommand(t, nil, "flow", "history", "flow-1", "--server", address)
	if result["runId"] != "run-1" || result["nextInternalEventId"] != float64(2) {
		t.Fatalf("unexpected history cursor: %#v", result)
	}
	events := result["events"].([]any)
	event := events[0].(map[string]any)
	if event["type"] != "ChannelExternalPublish" {
		t.Fatalf("unexpected event: %#v", event)
	}
	payload := event["payload"].(map[string]any)
	messages := payload["messages"].([]any)
	if messages[0].(map[string]any)["value"] != "go" {
		t.Fatalf("unexpected natural value: %#v", payload)
	}
}

func TestInspectOmitsStateForTerminalFlow(t *testing.T) {
	_, address := startTestFlowService(t)
	result := executeTestCommand(t, nil, "flow", "inspect", "terminal-flow", "--server", address)
	if result["state"] != nil {
		t.Fatalf("terminal inspect returned state: %#v", result)
	}
	summary := result["summary"].(map[string]any)
	if summary["flowStatusCode"] != float64(dexpb.FlowStatus_FLOW_STATUS_COMPLETED) {
		t.Fatalf("unexpected terminal summary: %#v", summary)
	}
}

func TestWatchEmitsHistoryAndCurrentStateBeforeLongPoll(t *testing.T) {
	service, address := startTestFlowService(t)
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	app := NewApp(bytes.NewReader(nil), stdout, stderr)
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() {
		finished <- app.Execute(ctx, []string{
			"flow", "watch", "flow-1", "--run-id", "run-1", "--server", address,
		})
	}()
	<-service.waitStarted
	cancel()
	err := <-finished
	if err == nil || status.Code(err) != codes.Canceled {
		t.Fatalf("expected canceled watch, got %v stderr=%s", err, stderr.String())
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected history and state lines, got %q", stdout.String())
	}
	var event map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &event); err != nil {
		t.Fatal(err)
	}
	if event["type"] != "ChannelExternalPublish" {
		t.Fatalf("unexpected watch event: %#v", event)
	}
	var snapshot map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot["type"] != "FlowStateSnapshot" {
		t.Fatalf("unexpected watch snapshot: %#v", snapshot)
	}
}

func TestAPIDescriptorIncludesEveryFlowServiceMethod(t *testing.T) {
	result := executeTestCommand(t, nil, "api", "list")
	methods := result["methods"].([]any)
	if len(methods) != dexpb.File_dex_proto.Services().ByName("FlowService").Methods().Len() {
		t.Fatalf("unexpected method count: %d", len(methods))
	}
	described := executeTestCommand(t, nil, "api", "describe", "ResetFlow")
	if described["requestType"] != "dex.ResetFlowRequest" || described["mutating"] != true {
		t.Fatalf("unexpected descriptor: %#v", described)
	}
}

func startTestFlowService(t *testing.T) (*testFlowService, string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	service := &testFlowService{waitStarted: make(chan struct{})}
	server := grpc.NewServer()
	dexpb.RegisterFlowServiceServer(server, service)
	serveFinished := make(chan error, 1)
	go func() {
		serveFinished <- server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.GracefulStop()
		if err := <-serveFinished; err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			t.Errorf("serve FlowService: %v", err)
		}
	})
	return service, listener.Addr().String()
}

func executeTestCommand(t *testing.T, input []byte, args ...string) map[string]any {
	t.Helper()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	app := NewApp(bytes.NewReader(input), stdout, stderr)
	if err := app.Execute(context.Background(), args); err != nil {
		t.Fatalf("execute %v: %v stderr=%s", args, err, stderr.String())
	}
	result := make(map[string]any)
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode %v output %q: %v", args, stdout.String(), err)
	}
	return result
}
