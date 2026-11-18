// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package api

import (
	"strconv"
	"strings"
)

type historyEventID int64

func (id *historyEventID) UnmarshalJSON(value []byte) error {
	decimal := string(value)
	if strings.HasPrefix(decimal, `"`) {
		unquoted, err := strconv.Unquote(decimal)
		if err != nil {
			return err
		}
		decimal = unquoted
	}
	parsed, err := strconv.ParseInt(decimal, 10, 64)
	if err != nil {
		return err
	}
	*id = historyEventID(parsed)
	return nil
}

type errorResponse struct {
	Error    string `json:"error"`
	Code     string `json:"code,omitempty"`
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
	FlowID string          `json:"flowId"`
	Values []blobReference `json:"values"`
}

type loadBlobsResponse struct {
	Values map[string]interface{} `json:"values"`
	// Unavailable maps the cache key of each value that is too large to load to the reason.
	Unavailable map[string]string `json:"unavailable,omitempty"`
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
	// RunAttempt is zero on Servers without run attempts.
	RunAttempt int32 `json:"runAttempt"`
}

type historyPage struct {
	Events              []historyEvent `json:"events"`
	NextPageToken       string         `json:"nextPageToken"`
	NextInternalEventID int64          `json:"nextInternalEventId"`
	// PrunedEventIDRange is the event ID range the history window removed; omitted when none.
	PrunedEventIDRange *historyEventIDRange `json:"prunedEventIdRange,omitempty"`
}

// historyEventIDRange is an inclusive range of history event IDs.
type historyEventIDRange struct {
	StartEventID int64 `json:"startEventId"`
	EndEventID   int64 `json:"endEventId"`
}

type historyEvent struct {
	EventID   int64                  `json:"eventId"`
	EventTime *string                `json:"eventTime"`
	Type      string                 `json:"type"`
	Payload   map[string]interface{} `json:"payload"`
	// RunAttempt is the run attempt that recorded the event, or zero.
	RunAttempt int32 `json:"runAttempt"`
}

type flowState struct {
	StickyAppWorkerID      string                 `json:"stickyAppWorkerId,omitempty"`
	FlowConfig             map[string]interface{} `json:"flowConfig"`
	Attributes             []keyValue             `json:"attributes"`
	ActiveStepExecutions   []activeStepExecution  `json:"activeStepExecutions"`
	QueuedSteps            []interface{}          `json:"queuedSteps"`
	PendingChannelMessages map[string]interface{} `json:"pendingChannelMessages"`
	CompletedSteps         []interface{}          `json:"completedSteps"`
}

type publishChannelMessageRequest struct {
	FlowID      string      `json:"flowId"`
	RunID       string      `json:"runId"`
	ChannelName string      `json:"channelName"`
	ValueType   string      `json:"valueType"`
	Value       interface{} `json:"value"`
}

type setAttributeRequest struct {
	FlowID    string      `json:"flowId"`
	RunID     string      `json:"runId"`
	Key       string      `json:"key"`
	ValueType string      `json:"valueType"`
	Value     interface{} `json:"value"`
}

// updateFlowHistoryConfigRequest changes only the run's history window.
type updateFlowHistoryConfigRequest struct {
	FlowID              string `json:"flowId"`
	RunID               string `json:"runId"`
	HistoryWindowEvents *int32 `json:"historyWindowEvents"`
	// Accepted for compatibility and never forwarded; the server always deletes expired history.
	DeleteExpiredHistoryObjects *bool `json:"deleteExpiredHistoryObjects"`
}

// updateFlowConfigRequest names the run and the Flow config fields to change; nil fields stay unchanged.
type updateFlowConfigRequest struct {
	FlowID              string `json:"flowId"`
	RunID               string `json:"runId"`
	HistoryWindowEvents *int32 `json:"historyWindowEvents"`
	// StepDurability is 1 for sync or 2 for async.
	StepDurability *int32 `json:"stepDurability"`
	// ActiveStepSearchMode is 1 for every Step, 2 for Steps with WaitFor, or 3 for off.
	ActiveStepSearchMode *int32 `json:"activeStepSearchMode"`
	// AttributeStoreNames replaces the run's stores; an empty list turns synchronization off.
	AttributeStoreNames *[]string `json:"attributeStoreNames"`
}

type deleteChannelMessageRequest struct {
	FlowID      string `json:"flowId"`
	RunID       string `json:"runId"`
	ChannelName string `json:"channelName"`
	MessageID   string `json:"messageId"`
}

type streamMessage struct {
	Value       interface{} `json:"value"`
	ResumeToken string      `json:"resumeToken"`
	CreatedTime *string     `json:"createdTime"`
	Source      string      `json:"source"`
}

type activeStepExecution struct {
	WaitForPullRouting  map[string]interface{} `json:"waitForPullRouting,omitempty"`
	ExecutePullRouting  map[string]interface{} `json:"executePullRouting,omitempty"`
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
	WaitForInvocation   map[string]interface{} `json:"waitForInvocation,omitempty"`
	ExecuteInvocation   map[string]interface{} `json:"executeInvocation,omitempty"`
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
	// RunAttempt selects the run attempt searched for the time travel point; zero selects the current one.
	RunAttempt int32 `json:"runAttempt"`
	// EventID is the history event ID of the event ID time travel type.
	EventID historyEventID `json:"eventId"`
	// SubFlowResetPolicy is a SubFlowResetPolicy number; zero selects the server default.
	SubFlowResetPolicy int32 `json:"subFlowResetPolicy"`
}

type timeTravelFlowResponse struct {
	RunID      string `json:"runId"`
	RunAttempt int32  `json:"runAttempt"`
}

type cloneForkFlowRequest struct {
	SourceFlowID string `json:"sourceFlowId"`
	// SourceRunID names the source run; empty selects the latest run.
	SourceRunID string `json:"sourceRunId"`
	FlowID      string `json:"flowId"`
	// RequestID makes a repeated submit a retry; empty makes the handler generate one.
	RequestID string `json:"requestId"`
	// PointType is a FlowCloneForkPointType number; zero selects the current state.
	PointType        int32          `json:"pointType"`
	HistoryEventTime string         `json:"historyEventTime"`
	StepType         string         `json:"stepType"`
	StepExecutionID  string         `json:"stepExecutionId"`
	StepMethod       int32          `json:"stepMethod"`
	EventID          historyEventID `json:"eventId"`
	// RunAttempt selects the source run attempt searched for the point; zero selects the current one.
	RunAttempt int32 `json:"runAttempt"`
	// IDReusePolicy is an IdReusePolicy number; zero selects the server default.
	IDReusePolicy int32 `json:"idReusePolicy"`
	// TaskQueue overrides the source run's task queue; empty keeps it.
	TaskQueue string `json:"taskQueue"`
	// SubFlowResetPolicy is a SubFlowResetPolicy number; zero selects the server default.
	SubFlowResetPolicy int32  `json:"subFlowResetPolicy"`
	Reason             string `json:"reason"`
	// DeleteAttributeKeys names Attributes the new run deletes after the copy; empty deletes none.
	DeleteAttributeKeys []string `json:"deleteAttributeKeys"`
}

type cloneForkFlowResponse struct {
	FlowID           string `json:"flowId"`
	RunID            string `json:"runId"`
	SourceRunID      string `json:"sourceRunId"`
	SourceRunAttempt int32  `json:"sourceRunAttempt"`
	SourceEventID    int64  `json:"sourceEventId"`
}

type stopFlowRequest struct {
	FlowID   string `json:"flowId"`
	RunID    string `json:"runId"`
	StopType int32  `json:"stopType"`
	Reason   string `json:"reason"`
	// Output is the natural JSON text of the Flow result for a complete stop type; empty sends none.
	Output string `json:"output"`
}
