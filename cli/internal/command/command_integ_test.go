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

	mu                sync.Mutex
	loadCalls         int
	stopCalls         int
	timeTravelCalls   int
	timeTravelRequest *dexpb.ResetFlowRequest
	waitStarted       chan struct{}
	waitOnce          sync.Once
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
	context.Context,
	*dexpb.StopFlowRequest,
) (*emptypb.Empty, error) {
	s.mu.Lock()
	s.stopCalls++
	s.mu.Unlock()
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

func TestTimeTravelRequiresConfirmationAndMapsHistoryEvent(t *testing.T) {
	service, address := startTestFlowService(t)
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	app := NewApp(bytes.NewReader(nil), stdout, stderr)
	err := app.Execute(context.Background(), []string{
		"flow", "time-travel", "flow-1", "--run-id", "run-1", "--type", "history-event-id",
		"--target", "42", "--reason", "retry fixed code", "--server", address,
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
		"flow", "time-travel", "flow-1", "--run-id", "run-1", "--type", "history-event-id",
		"--target", "42", "--reason", "retry fixed code", "--yes", "--server", address,
	)
	if result["previousRunId"] != "run-1" || result["runId"] != "run-2" {
		t.Fatalf("unexpected time travel result: %#v", result)
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.timeTravelCalls != 1 || service.timeTravelRequest.GetHistoryEventId() != 42 {
		t.Fatalf("unexpected time travel request: %#v", service.timeTravelRequest)
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
		if err := <-serveFinished; err != nil {
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
