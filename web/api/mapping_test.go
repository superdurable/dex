// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package api

import (
	"testing"

	"github.com/superdurable/dex/gen/dexpb"
	"google.golang.org/protobuf/reflect/protoreflect"
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
