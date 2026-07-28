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

package temporal

import (
	"context"
	"fmt"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service/common/timeparser"
	"github.com/superdurable/dex/service/common/utils"
	"go.temporal.io/api/common/v1"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/converter"
	"strings"
)

func getResetEventIDByType(ctx context.Context, resetType dexpb.FlowResetType,
	namespace, wid, rid string,
	frontendClient workflowservice.WorkflowServiceClient, converter converter.DataConverter,
	historyEventId int32, earliestHistoryTimeStr string, stepType, stepExecutionId string,
) (resetBaseRunID string, workflowTaskFinishID int64, err error) {
	// default to the same runID
	resetBaseRunID = rid

	switch resetType {
	case dexpb.FlowResetType_FLOW_RESET_TYPE_HISTORY_EVENT_ID:
		workflowTaskFinishID = int64(historyEventId)
		return
	case dexpb.FlowResetType_FLOW_RESET_TYPE_HISTORY_EVENT_TIME:
		var earliestTimeUnixNano int64
		earliestTimeUnixNano, err = timeparser.ParseTime(earliestHistoryTimeStr)
		if err != nil {
			return
		}
		workflowTaskFinishID, err = getEarliestDecisionEventID(ctx, namespace, wid, rid, earliestTimeUnixNano, frontendClient)
		if err != nil {
			return
		}
	case dexpb.FlowResetType_FLOW_RESET_TYPE_BEGINNING:
		firstRunID, firstRunErr := getTemporalFirstExecutionRunID(ctx, namespace, wid, rid, frontendClient)
		if firstRunErr != nil {
			err = firstRunErr
			return
		}
		if firstRunID != "" {
			rid = firstRunID
		}
		resetBaseRunID, workflowTaskFinishID, err = getFirstWorkflowTaskEventID(ctx, namespace, wid, rid, frontendClient)
		if err != nil {
			return
		}
	case dexpb.FlowResetType_FLOW_RESET_TYPE_STEP_TYPE, dexpb.FlowResetType_FLOW_RESET_TYPE_STEP_EXECUTION_ID:
		workflowTaskFinishID, err = getDecisionEventIDByStepTypeOrStepExecutionId(ctx, namespace, wid, rid, stepType, stepExecutionId, frontendClient, converter)
		if err != nil {
			return
		}
	default:
		panic("not supported resetType")
	}
	return
}

func getFirstWorkflowTaskEventID(ctx context.Context, namespace, wid, rid string, frontendClient workflowservice.WorkflowServiceClient) (resetBaseRunID string, workflowTaskEventID int64, err error) {
	resetBaseRunID = rid
	req := &workflowservice.GetWorkflowExecutionHistoryRequest{
		Namespace: namespace,
		Execution: &common.WorkflowExecution{
			WorkflowId: wid,
			RunId:      rid,
		},
		MaximumPageSize: 1000,
		NextPageToken:   nil,
	}
	for {
		var resp *workflowservice.GetWorkflowExecutionHistoryResponse
		resp, err = frontendClient.GetWorkflowExecutionHistory(ctx, req)
		if err != nil {
			return
		}
		for _, e := range resp.GetHistory().GetEvents() {
			if e.GetEventType() == enums.EVENT_TYPE_WORKFLOW_TASK_COMPLETED {
				workflowTaskEventID = e.GetEventId()
				return resetBaseRunID, workflowTaskEventID, nil
			}
			if e.GetEventType() == enums.EVENT_TYPE_WORKFLOW_TASK_SCHEDULED {
				if workflowTaskEventID == 0 {
					workflowTaskEventID = e.GetEventId() + 1
				}
			}
		}
		if len(resp.NextPageToken) != 0 {
			req.NextPageToken = resp.NextPageToken
		} else {
			break
		}
	}
	if workflowTaskEventID == 0 {
		err = fmt.Errorf("unable to find any scheduled or completed task")
		return
	}
	return
}

func getTemporalFirstExecutionRunID(
	ctx context.Context,
	namespace, wid, rid string,
	frontendClient workflowservice.WorkflowServiceClient,
) (string, error) {
	resp, err := frontendClient.GetWorkflowExecutionHistory(ctx, &workflowservice.GetWorkflowExecutionHistoryRequest{
		Namespace: namespace,
		Execution: &common.WorkflowExecution{
			WorkflowId: wid,
			RunId:      rid,
		},
		MaximumPageSize: 1,
	})
	if err != nil {
		return "", err
	}
	events := resp.GetHistory().GetEvents()
	if len(events) == 0 {
		return "", nil
	}
	attrs := events[0].GetWorkflowExecutionStartedEventAttributes()
	if attrs == nil {
		return "", nil
	}
	return attrs.GetFirstExecutionRunId(), nil
}

func getEarliestDecisionEventID(
	ctx context.Context,
	namespace string, wid string,
	rid string, earliestTime int64,
	frontendClient workflowservice.WorkflowServiceClient,
) (decisionFinishID int64, err error) {
	req := &workflowservice.GetWorkflowExecutionHistoryRequest{
		Namespace: namespace,
		Execution: &common.WorkflowExecution{
			WorkflowId: wid,
			RunId:      rid,
		},
		MaximumPageSize: 1000,
		NextPageToken:   nil,
	}

OuterLoop:
	for {
		var resp *workflowservice.GetWorkflowExecutionHistoryResponse
		resp, err = frontendClient.GetWorkflowExecutionHistory(ctx, req)
		if err != nil {
			return 0, composeErrorWithMessage("GetWorkflowExecutionHistory failed", err)
		}
		for _, e := range resp.GetHistory().GetEvents() {
			if e.GetEventType() == enums.EVENT_TYPE_WORKFLOW_TASK_COMPLETED {
				if utils.ToNanoSeconds(e.GetEventTime()) >= earliestTime {
					decisionFinishID = e.GetEventId()
					break OuterLoop
				}
			}
		}
		if len(resp.NextPageToken) != 0 {
			req.NextPageToken = resp.NextPageToken
		} else {
			break
		}
	}
	if decisionFinishID == 0 {
		return 0, composeErrorWithMessage("Get historyEventId failed", fmt.Errorf("no historyEventId"))
	}
	return
}

// getDecisionEventIDByStepTypeOrStepExecutionId scans the invoke-method activities
// (both wait-for and execute) whose request shapes share step_type/context fields.
func getDecisionEventIDByStepTypeOrStepExecutionId(
	ctx context.Context,
	namespace string, wid string,
	rid string, stepType, stepExecutionId string,
	frontendClient workflowservice.WorkflowServiceClient, converter converter.DataConverter,
) (decisionFinishID int64, err error) {
	req := &workflowservice.GetWorkflowExecutionHistoryRequest{
		Namespace: namespace,
		Execution: &common.WorkflowExecution{
			WorkflowId: wid,
			RunId:      rid,
		},
		MaximumPageSize: 1000,
		NextPageToken:   nil,
	}

	for {
		var resp *workflowservice.GetWorkflowExecutionHistoryResponse
		resp, err = frontendClient.GetWorkflowExecutionHistory(ctx, req)
		if err != nil {
			return 0, composeErrorWithMessage("GetWorkflowExecutionHistory failed", err)
		}
		for _, e := range resp.GetHistory().GetEvents() {
			if e.GetEventType() == enums.EVENT_TYPE_WORKFLOW_TASK_COMPLETED {
				decisionFinishID = e.GetEventId()
			}
			//TODO: Add check for local activity. (DEX-403)
			if e.GetEventType() == enums.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED {
				typeName := e.GetActivityTaskScheduledEventAttributes().GetActivityType().GetName()
				if strings.Contains(typeName, "InvokeExecuteMethod") {
					var input dexpb.InvokeExecuteMethodActivityInput
					err = converter.FromPayloads(e.GetActivityTaskScheduledEventAttributes().Input, &input)
					if err != nil {
						return 0, composeErrorWithMessage("GetWorkflowExecutionHistory failed", err)
					}
					if input.Request.GetStepType() == stepType || input.Request.GetContext().GetStepExecutionId() == stepExecutionId {
						if decisionFinishID == 0 {
							return 0, composeErrorWithMessage("GetWorkflowExecutionHistory failed", fmt.Errorf("invalid history or something goes very wrong"))
						}
						return
					}
				} else if strings.Contains(typeName, "InvokeWaitForMethod") {
					var input dexpb.InvokeWaitForMethodActivityInput
					err = converter.FromPayloads(e.GetActivityTaskScheduledEventAttributes().Input, &input)
					if err != nil {
						return 0, composeErrorWithMessage("GetWorkflowExecutionHistory failed", err)
					}
					if input.Request.GetStepType() == stepType ||
						input.Request.GetContext().GetStepExecutionId() == stepExecutionId {
						if decisionFinishID == 0 {
							return 0, composeErrorWithMessage(
								"GetWorkflowExecutionHistory failed",
								fmt.Errorf("activity was scheduled before a workflow task completed"),
							)
						}
						return
					}
				}
			}
		}
		if len(resp.NextPageToken) != 0 {
			req.NextPageToken = resp.NextPageToken
		} else {
			break
		}
	}
	return 0, composeErrorWithMessage("Get historyEventId failed", fmt.Errorf("no historyEventId"))
}

func composeErrorWithMessage(msg string, err error) error {
	err = fmt.Errorf("%v, %v", msg, err)
	return err
}
