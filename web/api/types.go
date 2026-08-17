// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package api

type errorResponse struct {
	Error    string `json:"error"`
	GRPCCode *int32 `json:"grpcCode,omitempty"`
}

type searchFlowsRequest struct {
	Query         string `json:"query"`
	PageSize      int32  `json:"pageSize"`
	NextPageToken string `json:"nextPageToken"`
}

type searchFlowsResponse struct {
	Flows         []flowExecution `json:"flows"`
	NextPageToken string          `json:"nextPageToken"`
}

type flowExecution struct {
	FlowID            string     `json:"flowId"`
	RunID             string     `json:"runId"`
	FlowType          string     `json:"flowType"`
	FlowStatus        string     `json:"flowStatus"`
	FlowStatusCode    int32      `json:"flowStatusCode"`
	StartTime         *string    `json:"startTime"`
	CloseTime         *string    `json:"closeTime"`
	IndexedAttributes []keyValue `json:"indexedAttributes"`
}

type keyValue struct {
	Key   string      `json:"key"`
	Value interface{} `json:"value"`
}

type blobReference struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
}

type loadBlobsRequest struct {
	Values []blobReference `json:"values"`
}

type loadBlobsResponse struct {
	Values map[string]interface{} `json:"values"`
}

type flowSummary struct {
	FlowID         string  `json:"flowId"`
	RunID          string  `json:"runId"`
	FirstRunID     string  `json:"firstRunId"`
	RequestID      string  `json:"requestId"`
	FlowType       string  `json:"flowType"`
	FlowStatus     string  `json:"flowStatus"`
	FlowStatusCode int32   `json:"flowStatusCode"`
	StartTime      *string `json:"startTime"`
	CloseTime      *string `json:"closeTime"`
}

type historyPage struct {
	Events              []historyEvent `json:"events"`
	NextPageToken       string         `json:"nextPageToken"`
	NextInternalEventID int64          `json:"nextInternalEventId"`
}

type historyEvent struct {
	EventID   int64                  `json:"eventId"`
	EventTime *string                `json:"eventTime"`
	Type      string                 `json:"type"`
	Payload   map[string]interface{} `json:"payload"`
}

type flowState struct {
	FlowConfig             map[string]interface{} `json:"flowConfig"`
	Attributes             []keyValue             `json:"attributes"`
	ActiveStepExecutions   []activeStepExecution  `json:"activeStepExecutions"`
	QueuedSteps            []interface{}          `json:"queuedSteps"`
	PendingChannelMessages map[string]interface{} `json:"pendingChannelMessages"`
	CompletedSteps         []interface{}          `json:"completedSteps"`
}

type activeStepExecution struct {
	StepExecutionID     string                 `json:"stepExecutionId"`
	FromStepExecutionID string                 `json:"fromStepExecutionId"`
	StepType            string                 `json:"stepType"`
	Phase               string                 `json:"phase"`
	Movement            map[string]interface{} `json:"movement,omitempty"`
	WaitingCondition    map[string]interface{} `json:"waitingCondition,omitempty"`
	CompletedConditions map[string]interface{} `json:"completedConditions,omitempty"`
	StepExecutionLocals []keyValue             `json:"stepExecutionLocals"`
	Timers              []interface{}          `json:"timers"`
	LastFailureInfo     map[string]interface{} `json:"lastFailureInfo,omitempty"`
}

type timeTravelFlowRequest struct {
	FlowID           string `json:"flowId"`
	RunID            string `json:"runId"`
	TimeTravelType   int32  `json:"timeTravelType"`
	Reason           string `json:"reason"`
	StepType         string `json:"stepType"`
	StepExecutionID  string `json:"stepExecutionId"`
	HistoryEventTime string `json:"historyEventTime"`
	StepMethod       int32  `json:"stepMethod"`
}

type timeTravelFlowResponse struct {
	RunID string `json:"runId"`
}

type stopFlowRequest struct {
	FlowID   string `json:"flowId"`
	RunID    string `json:"runId"`
	StopType int32  `json:"stopType"`
	Reason   string `json:"reason"`
}
