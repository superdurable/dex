// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

type runHistoryOperationsTestClient struct {
	dexpb.FlowServiceClient
	resetRequests     []*dexpb.ResetFlowRequest
	cloneForkRequests []*dexpb.CloneForkFlowRequest
	// serverInfo is nil for a protocol 6 server.
	serverInfo      *dexpb.ServerInfo
	serverInfoError error
	serverInfoCalls int
}

func (c *runHistoryOperationsTestClient) GetServerInfo(
	context.Context,
	*emptypb.Empty,
	...grpc.CallOption,
) (*dexpb.ServerInfo, error) {
	c.serverInfoCalls++
	if c.serverInfoError != nil {
		return nil, c.serverInfoError
	}
	if c.serverInfo == nil {
		return &dexpb.ServerInfo{CurrentProtocolVersion: 6, ServerEngine: dexpb.ServerEngine_SERVER_ENGINE_ZD}, nil
	}
	return c.serverInfo, nil
}

func (c *runHistoryOperationsTestClient) ResetFlow(
	_ context.Context,
	request *dexpb.ResetFlowRequest,
	_ ...grpc.CallOption,
) (*dexpb.ResetFlowResponse, error) {
	c.resetRequests = append(c.resetRequests, request)
	return &dexpb.ResetFlowResponse{RunId: request.GetRunId(), RunAttempt: 2}, nil
}

func (c *runHistoryOperationsTestClient) CloneForkFlow(
	_ context.Context,
	request *dexpb.CloneForkFlowRequest,
	_ ...grpc.CallOption,
) (*dexpb.CloneForkFlowResponse, error) {
	c.cloneForkRequests = append(c.cloneForkRequests, request)
	return &dexpb.CloneForkFlowResponse{
		RunId:            "new-run",
		SourceRunId:      "run-1",
		SourceRunAttempt: 1,
		SourceEventId:    12,
	}, nil
}

func performRunHistoryOperation(client *runHistoryOperationsTestClient, path string, body string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	RegisterHandlers(mux, client)
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	return response
}

func TestTimeTravelSendsTheEventIDAndSubFlowResetPolicy(t *testing.T) {
	client := &runHistoryOperationsTestClient{}
	response := performRunHistoryOperation(client, "/api/flows/time-travel",
		`{"flowId":"order-1","runId":"run-1","timeTravelType":6,"eventId":12,"subFlowResetPolicy":5,"reason":"replay"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q", response.Code, response.Body.String())
	}
	request := client.resetRequests[0]
	if request.GetResetType() != dexpb.FlowResetType_FLOW_RESET_TYPE_EVENT_ID ||
		request.GetEventId() != 12 ||
		request.GetSubFlowResetPolicy() != dexpb.SubFlowResetPolicy_SUB_FLOW_RESET_POLICY_ATTACH_ELSE_FAIL {
		t.Fatalf("reset request = %+v", request)
	}
}

func TestTimeTravelAcceptsExactInt64EventID(t *testing.T) {
	for _, eventIDJSON := range []string{`"9007199254740993"`, `9007199254740993`} {
		t.Run(eventIDJSON, func(t *testing.T) {
			client := &runHistoryOperationsTestClient{}
			response := performRunHistoryOperation(client, "/api/flows/time-travel",
				`{"flowId":"order-1","runId":"run-1","timeTravelType":6,"eventId":`+eventIDJSON+`,"reason":"replay"}`)
			if response.Code != http.StatusOK || client.resetRequests[0].GetEventId() != 9007199254740993 {
				t.Fatalf("status = %d requests = %+v", response.Code, client.resetRequests)
			}
		})
	}
}

func TestTimeTravelRejectsAnEventIDPointWithoutAnEventID(t *testing.T) {
	client := &runHistoryOperationsTestClient{}
	response := performRunHistoryOperation(client, "/api/flows/time-travel",
		`{"flowId":"order-1","runId":"run-1","timeTravelType":6,"reason":"replay"}`)
	if response.Code != http.StatusBadRequest || len(client.resetRequests) != 0 {
		t.Fatalf("status = %d requests = %d", response.Code, len(client.resetRequests))
	}
}

func TestCloneForkSendsTheEventPointAndOptions(t *testing.T) {
	client := &runHistoryOperationsTestClient{}
	response := performRunHistoryOperation(client, "/api/flows/clone-fork", `{
		"sourceFlowId":"order-1","sourceRunId":"run-1","flowId":" order-1-fork ","requestId":"request-1",
		"pointType":6,"eventId":12,"runAttempt":1,"idReusePolicy":2,"taskQueue":"fork-queue",
		"subFlowResetPolicy":1,"reason":"try a fix"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q", response.Code, response.Body.String())
	}
	request := client.cloneForkRequests[0]
	if request.GetSourceFlowId() != "order-1" || request.GetSourceRunId() != "run-1" ||
		request.GetFlowId() != "order-1-fork" || request.GetRequestId() != "request-1" ||
		request.GetPointType() != dexpb.FlowCloneForkPointType_FLOW_CLONE_FORK_POINT_TYPE_EVENT_ID ||
		request.GetEventId() != 12 || request.GetRunAttempt() != 1 ||
		request.GetIdReusePolicy() != dexpb.IdReusePolicy_ID_REUSE_POLICY_ALLOW_IF_NO_RUNNING ||
		request.GetSubFlowResetPolicy() != dexpb.SubFlowResetPolicy_SUB_FLOW_RESET_POLICY_ALWAYS_RESTART ||
		request.GetReason() != "try a fix" ||
		!request.GetFlowAlreadyStartedOptions().GetIgnoreAlreadyStartedError() {
		t.Fatalf("clone fork request = %+v", request)
	}
	workerTarget := request.GetFlowConfigOverride().GetWorkerTarget()
	if !workerTarget.GetIsPullMode() || workerTarget.GetTaskQueue() != "fork-queue" {
		t.Fatalf("worker target override = %+v", workerTarget)
	}
	var result cloneForkFlowResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result != (cloneForkFlowResponse{
		FlowID: "order-1-fork", RunID: "new-run", SourceRunID: "run-1", SourceRunAttempt: 1, SourceEventID: 12,
	}) {
		t.Fatalf("clone fork result = %+v", result)
	}
}

func TestCloneForkKeepsTheSourceConfigAndGeneratesARequestID(t *testing.T) {
	client := &runHistoryOperationsTestClient{}
	response := performRunHistoryOperation(client, "/api/flows/clone-fork",
		`{"sourceFlowId":"order-1","flowId":"order-1-fork","reason":"branch"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q", response.Code, response.Body.String())
	}
	request := client.cloneForkRequests[0]
	if request.GetFlowConfigOverride() != nil || request.GetRequestId() == "" ||
		request.GetPointType() != dexpb.FlowCloneForkPointType_FLOW_CLONE_FORK_POINT_TYPE_UNSPECIFIED ||
		len(request.GetAttributes()) != 0 {
		t.Fatalf("clone fork request = %+v", request)
	}
}

func TestCloneForkDeletesTheNamedAttributes(t *testing.T) {
	client := &runHistoryOperationsTestClient{}
	response := performRunHistoryOperation(client, "/api/flows/clone-fork",
		`{"sourceFlowId":"session-1","flowId":"session-2","reason":"fork",`+
			`"deleteAttributeKeys":["SessionTerminalLease","SessionLastTerminalEnvironment"]}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q", response.Code, response.Body.String())
	}
	writes := client.cloneForkRequests[0].GetAttributes()
	if len(writes) != 2 ||
		writes[0].GetKey() != "SessionTerminalLease" ||
		writes[1].GetKey() != "SessionLastTerminalEnvironment" {
		t.Fatalf("attribute writes = %+v", writes)
	}
	for _, write := range writes {
		if _, isNull := write.GetValue().GetKind().(*dexpb.Value_NullValue); !isNull {
			t.Fatalf("attribute write %q is not a deletion: %+v", write.GetKey(), write.GetValue())
		}
	}
}

func TestCloneForkMapsEveryPointAndSubFlowPolicy(t *testing.T) {
	points := []struct {
		name   string
		number int
		want   dexpb.FlowCloneForkPointType
		field  string
	}{
		{"current", 1, dexpb.FlowCloneForkPointType_FLOW_CLONE_FORK_POINT_TYPE_CURRENT, ""},
		{"beginning", 2, dexpb.FlowCloneForkPointType_FLOW_CLONE_FORK_POINT_TYPE_BEGINNING, ""},
		{"history event time", 3, dexpb.FlowCloneForkPointType_FLOW_CLONE_FORK_POINT_TYPE_HISTORY_EVENT_TIME, `,"historyEventTime":"2026-11-15T12:00:00Z"`},
		{"step type", 4, dexpb.FlowCloneForkPointType_FLOW_CLONE_FORK_POINT_TYPE_STEP_TYPE, `,"stepType":"ChargeOrder"`},
		{"step execution", 5, dexpb.FlowCloneForkPointType_FLOW_CLONE_FORK_POINT_TYPE_STEP_EXECUTION_ID, `,"stepExecutionId":"ChargeOrder-1","stepMethod":2`},
		{"event ID", 6, dexpb.FlowCloneForkPointType_FLOW_CLONE_FORK_POINT_TYPE_EVENT_ID, `,"eventId":"9007199254740993"`},
	}
	policies := []struct {
		number int
		want   dexpb.SubFlowResetPolicy
	}{
		{1, dexpb.SubFlowResetPolicy_SUB_FLOW_RESET_POLICY_ALWAYS_RESTART},
		{2, dexpb.SubFlowResetPolicy_SUB_FLOW_RESET_POLICY_ALWAYS_FAIL},
		{3, dexpb.SubFlowResetPolicy_SUB_FLOW_RESET_POLICY_ALWAYS_REJECT},
		{4, dexpb.SubFlowResetPolicy_SUB_FLOW_RESET_POLICY_ATTACH_ELSE_RESTART},
		{5, dexpb.SubFlowResetPolicy_SUB_FLOW_RESET_POLICY_ATTACH_ELSE_FAIL},
		{6, dexpb.SubFlowResetPolicy_SUB_FLOW_RESET_POLICY_ATTACH_ELSE_REJECT},
	}
	for _, point := range points {
		for _, policy := range policies {
			t.Run(point.name+"/policy "+strconv.Itoa(policy.number), func(t *testing.T) {
				client := &runHistoryOperationsTestClient{}
				body := fmt.Sprintf(`{"sourceFlowId":"order-1","flowId":"order-1-fork","reason":"branch","pointType":%d,"subFlowResetPolicy":%d%s}`,
					point.number, policy.number, point.field)
				response := performRunHistoryOperation(client, "/api/flows/clone-fork", body)
				if response.Code != http.StatusOK {
					t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
				}
				request := client.cloneForkRequests[0]
				if request.GetPointType() != point.want || request.GetSubFlowResetPolicy() != policy.want ||
					request.GetHistoryEventTime() != map[bool]string{true: "2026-11-15T12:00:00Z"}[point.number == 3] ||
					request.GetStepType() != map[bool]string{true: "ChargeOrder"}[point.number == 4] ||
					request.GetStepExecutionId() != map[bool]string{true: "ChargeOrder-1"}[point.number == 5] ||
					request.GetStepMethod() != map[bool]dexpb.FlowResetStepMethod{true: dexpb.FlowResetStepMethod_FLOW_RESET_STEP_METHOD_EXECUTE}[point.number == 5] ||
					request.GetEventId() != map[bool]int64{true: 9007199254740993}[point.number == 6] {
					t.Fatalf("clone fork request = %+v", request)
				}
			})
		}
	}
}

func TestCloneForkRejectsInvalidRequests(t *testing.T) {
	for _, body := range []string{
		`{"sourceFlowId":"order-1","flowId":"order-1-fork"}`,
		`{"sourceFlowId":"order-1","flowId":" ","reason":"branch"}`,
		`{"sourceFlowId":"order-1","flowId":"order-1","reason":"branch"}`,
		`{"sourceFlowId":"order-1","flowId":"order-1-fork","runAttempt":-1,"reason":"branch"}`,
		`{"sourceFlowId":"order-1","flowId":"order-1-fork","pointType":6,"reason":"branch"}`,
		`{"sourceFlowId":"order-1","flowId":"order-1-fork","reason":"branch","deleteAttributeKeys":[""]}`,
	} {
		client := &runHistoryOperationsTestClient{}
		response := performRunHistoryOperation(client, "/api/flows/clone-fork", body)
		if response.Code != http.StatusBadRequest || len(client.cloneForkRequests) != 0 {
			t.Fatalf("%s: status = %d requests = %d", body, response.Code, len(client.cloneForkRequests))
		}
	}
}

func TestRunHistoryForkOptionsRequireProtocol6(t *testing.T) {
	olderServers := map[string]*runHistoryOperationsTestClient{
		"protocol 5":             {serverInfo: &dexpb.ServerInfo{CurrentProtocolVersion: 5, ServerEngine: dexpb.ServerEngine_SERVER_ENGINE_ZD}},
		"without GetServerInfo":  {serverInfoError: status.Error(codes.Unimplemented, "unknown method")},
		"without protocol field": {serverInfo: &dexpb.ServerInfo{ServerEngine: dexpb.ServerEngine_SERVER_ENGINE_ZD}},
	}
	requests := []struct{ name, path, body string }{
		{"clone fork", "/api/flows/clone-fork", `{"sourceFlowId":"order-1","flowId":"order-1-fork","reason":"branch"}`},
		{"event ID time travel", "/api/flows/time-travel", `{"flowId":"order-1","runId":"run-1","timeTravelType":6,"eventId":12,"reason":"replay"}`},
		{"SubFlow reset policy", "/api/flows/time-travel", `{"flowId":"order-1","runId":"run-1","timeTravelType":1,"subFlowResetPolicy":4,"reason":"replay"}`},
	}
	for serverName, client := range olderServers {
		for _, request := range requests {
			t.Run(serverName+"/"+request.name, func(t *testing.T) {
				response := performRunHistoryOperation(client, request.path, request.body)
				var result errorResponse
				if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if response.Code != http.StatusBadRequest || result.Code != serverProtocolUnsupportedCode ||
					!strings.Contains(result.Error, "server-zd 0.1.33") {
					t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
				}
				if len(client.resetRequests) != 0 || len(client.cloneForkRequests) != 0 {
					t.Fatalf("an older server received %d resets and %d clone forks", len(client.resetRequests), len(client.cloneForkRequests))
				}
			})
		}
	}
}

func TestTimeTravelWithoutNewOptionsSkipsTheProtocolCheck(t *testing.T) {
	client := &runHistoryOperationsTestClient{serverInfo: &dexpb.ServerInfo{CurrentProtocolVersion: 5}}
	response := performRunHistoryOperation(client, "/api/flows/time-travel",
		`{"flowId":"order-1","runId":"run-1","timeTravelType":1,"reason":"replay"}`)
	if response.Code != http.StatusOK || client.serverInfoCalls != 0 ||
		client.resetRequests[0].GetSubFlowResetPolicy() != dexpb.SubFlowResetPolicy_SUB_FLOW_RESET_POLICY_UNSPECIFIED {
		t.Fatalf("status = %d GetServerInfo calls = %d body = %s", response.Code, client.serverInfoCalls, response.Body.String())
	}
}

func TestRunHistoryForkOptionsWaitForAnUnavailableFlowService(t *testing.T) {
	client := &runHistoryOperationsTestClient{serverInfoError: status.Error(codes.Unavailable, "FlowService is starting")}
	response := performRunHistoryOperation(client, "/api/flows/clone-fork",
		`{"sourceFlowId":"order-1","flowId":"order-1-fork","reason":"branch"}`)
	if response.Code != http.StatusServiceUnavailable || len(client.cloneForkRequests) != 0 {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
}

func TestServerInfoReportsTheProtocolVersion(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		client *runHistoryOperationsTestClient
		want   serverInfoResponse
	}{
		{"protocol 6", &runHistoryOperationsTestClient{}, serverInfoResponse{ServerEngine: "zd", CurrentProtocolVersion: 6}},
		{"protocol 5", &runHistoryOperationsTestClient{serverInfo: &dexpb.ServerInfo{CurrentProtocolVersion: 5, ServerEngine: dexpb.ServerEngine_SERVER_ENGINE_ZD}},
			serverInfoResponse{ServerEngine: "zd", CurrentProtocolVersion: 5}},
		{"unavailable", &runHistoryOperationsTestClient{serverInfoError: status.Error(codes.Unavailable, "starting")},
			serverInfoResponse{ServerEngine: "unspecified"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			mux := http.NewServeMux()
			RegisterHandlers(mux, testCase.client)
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/server-info", nil))
			var result serverInfoResponse
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if response.Code != http.StatusOK || result != testCase.want {
				t.Fatalf("status = %d server info = %+v", response.Code, result)
			}
		})
	}
}
