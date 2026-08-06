// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package cadence

import (
	"context"
	"fmt"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service/common/ptr"
	"github.com/superdurable/dex/service/common/timeparser"
	"go.uber.org/cadence/.gen/go/cadence/workflowserviceclient"
	"go.uber.org/cadence/.gen/go/shared"
	"go.uber.org/cadence/encoded"
	"strings"
)

func getResetIDsByType(
	ctx context.Context,
	resetType dexpb.FlowResetType,
	domain, wid, rid string,
	frontendClient workflowserviceclient.Interface, converter encoded.DataConverter,
	historyEventId int32, earliestHistoryTimeStr string, stepType, stepExecutionId string,
) (resetBaseRunID string, decisionFinishID int64, err error) {
	// default to the same runID
	resetBaseRunID = rid

	switch resetType {
	case dexpb.FlowResetType_FLOW_RESET_TYPE_HISTORY_EVENT_ID:
		decisionFinishID = int64(historyEventId)
		return
	case dexpb.FlowResetType_FLOW_RESET_TYPE_BEGINNING:
		firstRunID, firstRunErr := getCadenceFirstExecutionRunID(ctx, domain, wid, rid, frontendClient)
		if firstRunErr != nil {
			err = firstRunErr
			return
		}
		if firstRunID != "" {
			rid = firstRunID
			resetBaseRunID = firstRunID
		}
		decisionFinishID, err = getFirstDecisionTaskByType(ctx, domain, wid, rid, frontendClient, shared.EventTypeDecisionTaskCompleted)
		if err != nil {
			return
		}
	case dexpb.FlowResetType_FLOW_RESET_TYPE_HISTORY_EVENT_TIME:
		var earliestTimeUnixNano int64
		earliestTimeUnixNano, err = timeparser.ParseTime(earliestHistoryTimeStr)
		if err != nil {
			return
		}
		decisionFinishID, err = getEarliestDecisionID(ctx, domain, wid, rid, earliestTimeUnixNano, frontendClient)
		if err != nil {
			return
		}
	case dexpb.FlowResetType_FLOW_RESET_TYPE_STEP_TYPE, dexpb.FlowResetType_FLOW_RESET_TYPE_STEP_EXECUTION_ID:
		decisionFinishID, err = getDecisionEventIDByStepTypeOrStepExecutionId(ctx, domain, wid, rid, stepType, stepExecutionId, frontendClient, converter)
		if err != nil {
			return
		}
	default:
		err = fmt.Errorf("not supported resetType")
	}
	return
}

func getFirstDecisionTaskByType(
	ctx context.Context,
	domain string,
	workflowID string,
	runID string,
	frontendClient workflowserviceclient.Interface,
	decisionType shared.EventType,
) (decisionFinishID int64, err error) {

	req := &shared.GetWorkflowExecutionHistoryRequest{
		Domain: &domain,
		Execution: &shared.WorkflowExecution{
			WorkflowId: &workflowID,
			RunId:      &runID,
		},
		MaximumPageSize: ptr.Any(int32(1000)),
		NextPageToken:   nil,
	}

	for {
		var resp *shared.GetWorkflowExecutionHistoryResponse
		resp, err = frontendClient.GetWorkflowExecutionHistory(ctx, req)
		if err != nil {
			return 0, composeErrorWithMessage("GetWorkflowExecutionHistory failed", err)
		}

		for _, e := range resp.GetHistory().GetEvents() {
			if e.GetEventType() == decisionType {
				decisionFinishID = e.GetEventId()
				return decisionFinishID, nil
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

func getCadenceFirstExecutionRunID(
	ctx context.Context,
	domain, wid, rid string,
	frontendClient workflowserviceclient.Interface,
) (string, error) {
	resp, err := frontendClient.GetWorkflowExecutionHistory(ctx, &shared.GetWorkflowExecutionHistoryRequest{
		Domain: &domain,
		Execution: &shared.WorkflowExecution{
			WorkflowId: &wid,
			RunId:      &rid,
		},
		MaximumPageSize: ptr.Any(int32(1)),
	})
	if err != nil {
		return "", composeErrorWithMessage("GetWorkflowExecutionHistory failed", err)
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

func getEarliestDecisionID(
	ctx context.Context,
	domain string, wid string,
	rid string, earliestTime int64,
	frontendClient workflowserviceclient.Interface,
) (decisionFinishID int64, err error) {
	req := &shared.GetWorkflowExecutionHistoryRequest{
		Domain: &domain,
		Execution: &shared.WorkflowExecution{
			WorkflowId: &wid,
			RunId:      &rid,
		},
		MaximumPageSize: ptr.Any(int32(1000)),
		NextPageToken:   nil,
	}

OuterLoop:
	for {
		var resp *shared.GetWorkflowExecutionHistoryResponse
		resp, err = frontendClient.GetWorkflowExecutionHistory(ctx, req)
		if err != nil {
			return 0, composeErrorWithMessage("GetWorkflowExecutionHistory failed", err)
		}
		for _, e := range resp.GetHistory().GetEvents() {
			if e.GetEventType() == shared.EventTypeDecisionTaskCompleted {
				if e.GetTimestamp() >= earliestTime {
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
	domain string, wid string,
	rid string, stepType, stepExecutionId string,
	frontendClient workflowserviceclient.Interface,
	converter encoded.DataConverter,
) (decisionFinishID int64, err error) {
	req := &shared.GetWorkflowExecutionHistoryRequest{
		Domain: &domain,
		Execution: &shared.WorkflowExecution{
			WorkflowId: &wid,
			RunId:      &rid,
		},
		MaximumPageSize: ptr.Any(int32(1000)),
		NextPageToken:   nil,
	}

	for {
		var resp *shared.GetWorkflowExecutionHistoryResponse
		resp, err = frontendClient.GetWorkflowExecutionHistory(ctx, req)
		if err != nil {
			return 0, composeErrorWithMessage("GetWorkflowExecutionHistory failed", err)
		}
		for _, e := range resp.GetHistory().GetEvents() {
			if e.GetEventType() == shared.EventTypeDecisionTaskCompleted {
				decisionFinishID = e.GetEventId()
			}
			//TODO: Add check for local activity. (DEX-403)
			if e.GetEventType() == shared.EventTypeActivityTaskScheduled {
				typeName := e.GetActivityTaskScheduledEventAttributes().GetActivityType().GetName()
				if strings.Contains(typeName, "InvokeExecuteMethod") {
					var input dexpb.InvokeExecuteMethodActivityInput
					var localInput *dexpb.InternalLocalActivityInput
					err = converter.FromData(
						e.GetActivityTaskScheduledEventAttributes().Input,
						&input,
						&localInput,
					)
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
					var localInput *dexpb.InternalLocalActivityInput
					err = converter.FromData(
						e.GetActivityTaskScheduledEventAttributes().Input,
						&input,
						&localInput,
					)
					if err != nil {
						return 0, composeErrorWithMessage("GetWorkflowExecutionHistory failed", err)
					}
					if input.Request.GetStepType() == stepType ||
						input.Request.GetContext().GetStepExecutionId() == stepExecutionId {
						if decisionFinishID == 0 {
							return 0, composeErrorWithMessage(
								"GetWorkflowExecutionHistory failed",
								fmt.Errorf("activity was scheduled before a decision task completed"),
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
