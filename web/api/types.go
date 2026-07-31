// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

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
	FlowID           string     `json:"flowId"`
	RunID            string     `json:"runId"`
	FlowType         string     `json:"flowType"`
	FlowStatus       string     `json:"flowStatus"`
	FlowStatusCode   int32      `json:"flowStatusCode"`
	StartTime        *string    `json:"startTime"`
	CloseTime        *string    `json:"closeTime"`
	SearchAttributes []keyValue `json:"searchAttributes"`
}

type keyValue struct {
	Key   string      `json:"key"`
	Value interface{} `json:"value"`
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
}

type resetFlowRequest struct {
	FlowID           string `json:"flowId"`
	RunID            string `json:"runId"`
	ResetType        int32  `json:"resetType"`
	HistoryEventID   int32  `json:"historyEventId"`
	Reason           string `json:"reason"`
	StepType         string `json:"stepType"`
	StepExecutionID  string `json:"stepExecutionId"`
	HistoryEventTime string `json:"historyEventTime"`
}

type resetFlowResponse struct {
	RunID string `json:"runId"`
}
