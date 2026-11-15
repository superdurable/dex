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
)

type runHistoryOperationsTestClient struct {
	dexpb.FlowServiceClient
	resetRequests     []*dexpb.ResetFlowRequest
	cloneForkRequests []*dexpb.CloneForkFlowRequest
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
		request.GetPointType() != dexpb.FlowCloneForkPointType_FLOW_CLONE_FORK_POINT_TYPE_UNSPECIFIED {
		t.Fatalf("clone fork request = %+v", request)
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
	} {
		client := &runHistoryOperationsTestClient{}
		response := performRunHistoryOperation(client, "/api/flows/clone-fork", body)
		if response.Code != http.StatusBadRequest || len(client.cloneForkRequests) != 0 {
			t.Fatalf("%s: status = %d requests = %d", body, response.Code, len(client.cloneForkRequests))
		}
	}
}
