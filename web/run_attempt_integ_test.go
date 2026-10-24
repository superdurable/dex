// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package web_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/superdurable/dex/gen/dexpb"
)

type runAttemptFlowService struct {
	dexpb.UnimplementedFlowServiceServer
	historyRequests    chan *dexpb.GetHistoryEventsRequest
	timeTravelRequests chan *dexpb.ResetFlowRequest
}

func (s *runAttemptFlowService) GetFlowSummary(
	context.Context,
	*dexpb.GetFlowSummaryRequest,
) (*dexpb.GetFlowSummaryResponse, error) {
	return &dexpb.GetFlowSummaryResponse{
		FlowExecutionId: &dexpb.FlowExecutionID{FlowId: "order-1", RunId: "run-1"},
		FirstRunId:      "run-1",
		FlowType:        "OrderFlow",
		FlowStatus:      dexpb.FlowStatus_FLOW_STATUS_RUNNING,
		RunAttempt:      3,
	}, nil
}

func (s *runAttemptFlowService) GetHistoryEvents(
	_ context.Context,
	request *dexpb.GetHistoryEventsRequest,
) (*dexpb.GetHistoryEventsResponse, error) {
	s.historyRequests <- request
	return &dexpb.GetHistoryEventsResponse{
		Events: []*dexpb.FlowHistoryEvent{
			{
				EventId:    4,
				RunAttempt: 1,
				Payload: &dexpb.FlowHistoryEvent_ChannelExternalDelete{ChannelExternalDelete: &dexpb.ChannelExternalDeleteEvent{
					Messages: []*dexpb.ChannelMessageDeletion{{ChannelName: "notes", MessageId: "message-1"}},
				}},
			},
			{
				EventId:    9,
				RunAttempt: 2,
				Payload: &dexpb.FlowHistoryEvent_TimeTravelFork{TimeTravelFork: &dexpb.TimeTravelForkHistoryEvent{
					PreviousRunId:      "run-1",
					PreviousRunAttempt: 1,
					TargetRunAttempt:   1,
					TargetEventId:      3,
				}},
			},
		},
		NextInternalEventId: 10,
	}, nil
}

func (s *runAttemptFlowService) ResetFlow(
	_ context.Context,
	request *dexpb.ResetFlowRequest,
) (*dexpb.ResetFlowResponse, error) {
	s.timeTravelRequests <- request
	return &dexpb.ResetFlowResponse{RunId: request.GetRunId(), RunAttempt: 4}, nil
}

func TestWebServerReadsRunAttemptHistory(t *testing.T) {
	service := &runAttemptFlowService{historyRequests: make(chan *dexpb.GetHistoryEventsRequest, 1)}
	harness := newHarness(t, service)

	response := get(t, harness.http.URL+"/api/flows/history?flowId=order-1&runId=run-1&runAttempt=2")
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("history status = %d body=%q", response.StatusCode, readBody(t, response))
	}
	request := <-service.historyRequests
	if request.GetRunAttempt() != 2 || request.GetRunId() != "run-1" {
		t.Fatalf("history request = %+v", request)
	}
	var page struct {
		Events []struct {
			EventID    int64                  `json:"eventId"`
			Type       string                 `json:"type"`
			RunAttempt int32                  `json:"runAttempt"`
			Payload    map[string]interface{} `json:"payload"`
		} `json:"events"`
	}
	decodeResponse(t, response, &page)
	if len(page.Events) != 2 {
		t.Fatalf("history events = %+v", page.Events)
	}
	deletion := page.Events[0]
	deletedMessage := deletion.Payload["messages"].([]interface{})[0].(map[string]interface{})
	if deletion.Type != "ChannelExternalDelete" || deletion.RunAttempt != 1 || deletedMessage["messageId"] != "message-1" {
		t.Fatalf("deletion event = %+v", deletion)
	}
	fork := page.Events[1]
	if fork.Type != "TimeTravelFork" || fork.RunAttempt != 2 || fork.Payload["previousRunAttempt"] != float64(1) {
		t.Fatalf("fork event = %+v", fork)
	}

	for _, query := range []string{"runAttempt=-1", "runAttempt=attempt", "runAttempt=2147483648"} {
		invalid := get(t, harness.http.URL+"/api/flows/history?flowId=order-1&runId=run-1&"+query)
		if invalid.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s status = %d body=%q", query, invalid.StatusCode, readBody(t, invalid))
		}
		invalid.Body.Close()
	}
}

func TestWebServerReportsAndTargetsRunAttempts(t *testing.T) {
	service := &runAttemptFlowService{timeTravelRequests: make(chan *dexpb.ResetFlowRequest, 1)}
	harness := newHarness(t, service)

	summaryResponse := get(t, harness.http.URL+"/api/flows/summary?flowId=order-1")
	var summary struct {
		RunID      string `json:"runId"`
		RunAttempt int32  `json:"runAttempt"`
	}
	decodeResponse(t, summaryResponse, &summary)
	summaryResponse.Body.Close()
	if summary.RunID != "run-1" || summary.RunAttempt != 3 {
		t.Fatalf("summary = %+v", summary)
	}

	response := postJSON(
		t,
		harness.http.URL+"/api/flows/time-travel",
		`{"flowId":"order-1","runId":"run-1","timeTravelType":2,"historyEventTime":"2026-10-24T17:30:05.000Z","runAttempt":2,"reason":"replay"}`,
	)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("time travel status = %d body=%q", response.StatusCode, readBody(t, response))
	}
	request := <-service.timeTravelRequests
	if request.GetRunAttempt() != 2 || request.GetHistoryEventTime() != "2026-10-24T17:30:05.000Z" {
		t.Fatalf("time travel request = %+v", request)
	}
	var result struct {
		RunID      string `json:"runId"`
		RunAttempt int32  `json:"runAttempt"`
	}
	decodeResponse(t, response, &result)
	if result.RunID != "run-1" || result.RunAttempt != 4 {
		t.Fatalf("time travel result = %+v", result)
	}

	invalid := postJSON(
		t,
		harness.http.URL+"/api/flows/time-travel",
		`{"flowId":"order-1","runId":"run-1","timeTravelType":1,"runAttempt":-1,"reason":"replay"}`,
	)
	defer invalid.Body.Close()
	if invalid.StatusCode != http.StatusBadRequest {
		t.Fatalf("negative run attempt status = %d", invalid.StatusCode)
	}
}
