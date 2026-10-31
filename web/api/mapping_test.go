// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestMapHistoryEventCoversEveryPayload(t *testing.T) {
	payloads := (&dexpb.FlowHistoryEvent{}).ProtoReflect().Descriptor().Oneofs().ByName("payload").Fields()
	for index := 0; index < payloads.Len(); index++ {
		field := payloads.Get(index)
		event := &dexpb.FlowHistoryEvent{EventId: int64(index + 1)}
		message := event.ProtoReflect()
		message.Set(field, protoreflect.ValueOfMessage(message.NewField(field).Message()))
		if _, err := mapHistoryEvent(event); err != nil {
			t.Fatalf("history payload %s: %v", field.Name(), err)
		}
	}
}

func TestMapChannelExternalDeleteHistoryEvent(t *testing.T) {
	mapped, err := mapHistoryEvent(&dexpb.FlowHistoryEvent{
		EventId:    7,
		RunAttempt: 2,
		Payload: &dexpb.FlowHistoryEvent_ChannelExternalDelete{ChannelExternalDelete: &dexpb.ChannelExternalDeleteEvent{
			Messages: []*dexpb.ChannelMessageDeletion{{ChannelName: "notes", MessageId: "message-1"}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	messages, ok := mapped.Payload["messages"].([]interface{})
	if mapped.Type != "ChannelExternalDelete" || mapped.RunAttempt != 2 || !ok || len(messages) != 1 {
		t.Fatalf("mapped event: %#v", mapped)
	}
	deletion := messages[0].(map[string]interface{})
	if deletion["channelName"] != "notes" || deletion["messageId"] != "message-1" {
		t.Fatalf("deletion: %#v", deletion)
	}
}

func TestMapSummaryReportsRunAttempt(t *testing.T) {
	summary := mapSummary(&dexpb.GetFlowSummaryResponse{
		FlowExecutionId: &dexpb.FlowExecutionID{FlowId: "flow-1", RunId: "run-1"},
		RunAttempt:      3,
	})
	if summary.RunAttempt != 3 || summary.RunID != "run-1" {
		t.Fatalf("summary: %#v", summary)
	}
}

func TestMapPullRouting(t *testing.T) {
	routing := &dexpb.PullRouting{StickyAppWorkerId: "worker-first", ServedByAppWorkerId: "worker-second", Source: dexpb.PullRoutingSource_PULL_ROUTING_SOURCE_FLOW_TYPE_QUEUE}
	state, err := mapFlowState(&dexpb.GetFlowStateResponse{
		StickyAppWorkerId:    "worker-second",
		ActiveStepExecutions: []*dexpb.ActiveStepExecutionState{{StepExecutionId: "CheckStep-1", WaitForPullRouting: routing}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if state.StickyAppWorkerID != "worker-second" {
		t.Fatalf("sticky Worker: %q", state.StickyAppWorkerID)
	}
	mapped := state.ActiveStepExecutions[0].WaitForPullRouting
	if mapped["servedByAppWorkerId"] != "worker-second" || mapped["stickyAppWorkerId"] != "worker-first" || mapped["source"] != float64(2) {
		t.Fatalf("routing: %#v", mapped)
	}
	history, err := mapHistoryEvent(&dexpb.FlowHistoryEvent{Payload: &dexpb.FlowHistoryEvent_StepExecuteCompleted{StepExecuteCompleted: &dexpb.StepExecuteCompletedEvent{Context: &dexpb.StepMethodEventContext{PullRouting: routing}}}})
	if err != nil {
		t.Fatal(err)
	}
	if history.Payload["context"].(map[string]interface{})["pullRouting"].(map[string]interface{})["servedByAppWorkerId"] != "worker-second" {
		t.Fatalf("history: %#v", history)
	}
	rpc, err := mapHistoryEvent(&dexpb.FlowHistoryEvent{Payload: &dexpb.FlowHistoryEvent_RpcExecutionCompleted{RpcExecutionCompleted: &dexpb.RpcExecutionCompletedEvent{ServedByAppWorkerId: "rpc-worker"}}})
	if err != nil {
		t.Fatal(err)
	}
	if rpc.Payload["servedByAppWorkerId"] != "rpc-worker" {
		t.Fatalf("RPC: %#v", rpc)
	}
}

func TestMapLegacyRouting(t *testing.T) {
	state, err := mapFlowState(&dexpb.GetFlowStateResponse{ActiveStepExecutions: []*dexpb.ActiveStepExecutionState{{}}})
	if err != nil {
		t.Fatal(err)
	}
	if state.StickyAppWorkerID != "" || state.ActiveStepExecutions[0].WaitForPullRouting != nil || state.ActiveStepExecutions[0].ExecutePullRouting != nil {
		t.Fatalf("legacy routing: %#v", state)
	}
}

func TestMapHistoryPagePassesPrunedRangeAndRelatedEventIDs(t *testing.T) {
	waitForCompletedTime := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	page, err := mapHistoryPage(&dexpb.GetHistoryEventsResponse{
		Events: []*dexpb.FlowHistoryEvent{{
			EventId: 12,
			Payload: &dexpb.FlowHistoryEvent_StepExecuteCompleted{StepExecuteCompleted: &dexpb.StepExecuteCompletedEvent{
				Context: &dexpb.StepMethodEventContext{
					StepExecutionId:         "ReviewStep-3",
					FromEventId:             4,
					WaitForCompletedEventId: 9,
					WaitForCompletedTime:    timestamppb.New(waitForCompletedTime),
				},
			}},
		}},
		NextInternalEventId:  13,
		PrunedEventIdRange:   &dexpb.HistoryEventIdRange{StartEventId: 2, EndEventId: 8},
		FirstRetainedEventId: 9,
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	pruned := decoded["prunedEventIdRange"].(map[string]interface{})
	if pruned["startEventId"] != float64(2) || pruned["endEventId"] != float64(8) || decoded["firstRetainedEventId"] != float64(9) {
		t.Fatalf("retained boundary: %s", encoded)
	}
	event := decoded["events"].([]interface{})[0].(map[string]interface{})
	context := event["payload"].(map[string]interface{})["context"].(map[string]interface{})
	if context["fromEventId"] != "4" || context["waitForCompletedEventId"] != "9" || context["waitForCompletedTime"] != "2026-10-30T12:00:00Z" {
		t.Fatalf("related event IDs: %s", encoded)
	}

	unpruned, err := mapHistoryPage(&dexpb.GetHistoryEventsResponse{
		FirstRetainedEventId: 2,
		Events: []*dexpb.FlowHistoryEvent{{
			EventId: 2,
			Payload: &dexpb.FlowHistoryEvent_StepExecuteCompleted{StepExecuteCompleted: &dexpb.StepExecuteCompletedEvent{
				Context: &dexpb.StepMethodEventContext{StepExecutionId: "ReviewStep-1"},
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err = json.Marshal(unpruned)
	if err != nil {
		t.Fatal(err)
	}
	decoded = nil
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, ok := decoded["prunedEventIdRange"]; ok || decoded["firstRetainedEventId"] != float64(2) {
		t.Fatalf("unpruned history must omit the range: %s", encoded)
	}
	context = decoded["events"].([]interface{})[0].(map[string]interface{})["payload"].(map[string]interface{})["context"].(map[string]interface{})
	for _, field := range []string{"fromEventId", "waitForCompletedEventId", "waitForCompletedTime"} {
		if _, ok := context[field]; ok {
			t.Fatalf("an event without %s must omit it: %s", field, encoded)
		}
	}
}
