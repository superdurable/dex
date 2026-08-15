// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package temporal

import (
	"context"
	"fmt"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service/common/timeparser"
	"github.com/superdurable/dex/service/common/utils"
	"go.temporal.io/api/common/v1"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/history/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/converter"
	"strconv"
	"strings"
)

func getResetEventIDByType(ctx context.Context, resetType dexpb.FlowResetType,
	namespace, wid, rid string,
	frontendClient workflowservice.WorkflowServiceClient, converter converter.DataConverter,
	earliestHistoryTimeStr string, stepType, stepExecutionId string,
	stepMethod dexpb.FlowResetStepMethod,
) (resetBaseRunID string, workflowTaskFinishID int64, err error) {
	// default to the same runID
	resetBaseRunID = rid

	switch resetType {
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
		workflowTaskFinishID, err = getDecisionEventIDByStepTypeOrStepExecutionId(
			ctx,
			namespace,
			wid,
			rid,
			resetType,
			stepType,
			stepExecutionId,
			stepMethod,
			frontendClient,
			converter,
		)
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
		return 0, composeErrorWithMessage("find reset boundary failed", fmt.Errorf("no workflow task boundary"))
	}
	return
}

// getDecisionEventIDByStepTypeOrStepExecutionId scans the invoke-method activities
// (both wait-for and execute) whose request shapes share step_type/context fields.
func getDecisionEventIDByStepTypeOrStepExecutionId(
	ctx context.Context,
	namespace string, wid string,
	rid string,
	resetType dexpb.FlowResetType,
	stepType, stepExecutionId string,
	stepMethod dexpb.FlowResetStepMethod,
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
		for _, event := range resp.GetHistory().GetEvents() {
			if event.GetEventType() == enums.EVENT_TYPE_WORKFLOW_TASK_COMPLETED {
				decisionFinishID = event.GetEventId()
			}
			if event.GetEventType() == enums.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED {
				typeName := event.GetActivityTaskScheduledEventAttributes().GetActivityType().GetName()
				if strings.Contains(typeName, "InvokeExecuteMethod") {
					var input dexpb.InvokeExecuteMethodActivityInput
					err = converter.FromPayloads(event.GetActivityTaskScheduledEventAttributes().Input, &input)
					if err != nil {
						return 0, composeErrorWithMessage("GetWorkflowExecutionHistory failed", err)
					}
					if matchesStepResetTarget(
						resetType,
						stepMethod,
						dexpb.FlowResetStepMethod_FLOW_RESET_STEP_METHOD_EXECUTE,
						stepType,
						stepExecutionId,
						input.Request.GetStepType(),
						input.Request.GetContext().GetStepExecutionId(),
					) {
						if decisionFinishID == 0 {
							return 0, composeErrorWithMessage("GetWorkflowExecutionHistory failed", fmt.Errorf("invalid history or something goes very wrong"))
						}
						return
					}
				} else if strings.Contains(typeName, "InvokeWaitForMethod") {
					var input dexpb.InvokeWaitForMethodActivityInput
					err = converter.FromPayloads(event.GetActivityTaskScheduledEventAttributes().Input, &input)
					if err != nil {
						return 0, composeErrorWithMessage("GetWorkflowExecutionHistory failed", err)
					}
					if matchesStepResetTarget(
						resetType,
						stepMethod,
						dexpb.FlowResetStepMethod_FLOW_RESET_STEP_METHOD_WAIT_FOR,
						stepType,
						stepExecutionId,
						input.Request.GetStepType(),
						input.Request.GetContext().GetStepExecutionId(),
					) {
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
			if event.GetEventType() == enums.EVENT_TYPE_MARKER_RECORDED {
				var isMatch bool
				isMatch, decisionFinishID, err = matchesTemporalLocalActivityResetTarget(
					event,
					resetType,
					stepMethod,
					stepType,
					stepExecutionId,
					converter,
				)
				if err != nil {
					return 0, composeErrorWithMessage("GetWorkflowExecutionHistory failed", err)
				}
				if isMatch {
					if decisionFinishID == 0 {
						return 0, composeErrorWithMessage(
							"GetWorkflowExecutionHistory failed",
							fmt.Errorf("local activity marker has no workflow task boundary"),
						)
					}
					return
				}
			}
		}
		if len(resp.NextPageToken) != 0 {
			req.NextPageToken = resp.NextPageToken
		} else {
			break
		}
	}
	return 0, composeErrorWithMessage("find reset boundary failed", fmt.Errorf("no workflow task boundary"))
}

func matchesTemporalLocalActivityResetTarget(
	event *history.HistoryEvent,
	resetType dexpb.FlowResetType,
	requestedMethod dexpb.FlowResetStepMethod,
	requestedStepType string,
	requestedStepExecutionID string,
	dataConverter converter.DataConverter,
) (bool, int64, error) {
	attributes := event.GetMarkerRecordedEventAttributes()
	if attributes.GetMarkerName() != "LocalActivity" {
		return false, 0, nil
	}
	var marker localActivityMarkerData
	if err := dataConverter.FromPayloads(attributes.GetDetails()["data"], &marker); err != nil {
		return false, 0, err
	}
	candidateMethod := stepMethodFromActivityType(marker.ActivityType)
	if candidateMethod == dexpb.FlowResetStepMethod_FLOW_RESET_STEP_METHOD_UNSPECIFIED {
		return false, 0, nil
	}
	candidateStepExecutionID, err := temporalLocalActivityStepExecutionID(
		attributes,
		candidateMethod,
		dataConverter,
	)
	if err != nil || candidateStepExecutionID == "" {
		return false, 0, err
	}
	return matchesStepResetTarget(
		resetType,
		requestedMethod,
		candidateMethod,
		requestedStepType,
		requestedStepExecutionID,
		stepTypeFromExecutionID(candidateStepExecutionID),
		candidateStepExecutionID,
	), attributes.GetWorkflowTaskCompletedEventId(), nil
}

func temporalLocalActivityStepExecutionID(
	attributes *history.MarkerRecordedEventAttributes,
	method dexpb.FlowResetStepMethod,
	dataConverter converter.DataConverter,
) (string, error) {
	result := attributes.GetDetails()["result"]
	if result == nil {
		applicationFailure := attributes.GetFailure().GetApplicationFailureInfo()
		if applicationFailure == nil || len(applicationFailure.GetDetails().GetPayloads()) == 0 {
			return "", nil
		}
		metadata := &dexpb.InternalLocalStepActivityFailure{}
		if err := dataConverter.FromPayload(applicationFailure.GetDetails().GetPayloads()[0], metadata); err != nil {
			return "", err
		}
		return metadata.GetLocalActivityMetadata().GetCurrentStepExecutionId(), nil
	}
	if method == dexpb.FlowResetStepMethod_FLOW_RESET_STEP_METHOD_WAIT_FOR {
		var output dexpb.InvokeWaitForMethodActivityOutput
		if err := dataConverter.FromPayloads(result, &output); err != nil {
			return "", err
		}
		return output.GetResponse().GetLocalActivityMetadata().GetCurrentStepExecutionId(), nil
	}
	var output dexpb.InvokeExecuteMethodActivityOutput
	if err := dataConverter.FromPayloads(result, &output); err != nil {
		return "", err
	}
	return output.GetResponse().GetLocalActivityMetadata().GetCurrentStepExecutionId(), nil
}

func stepMethodFromActivityType(activityType string) dexpb.FlowResetStepMethod {
	switch {
	case strings.Contains(activityType, "InvokeWaitForMethod"):
		return dexpb.FlowResetStepMethod_FLOW_RESET_STEP_METHOD_WAIT_FOR
	case strings.Contains(activityType, "InvokeExecuteMethod"):
		return dexpb.FlowResetStepMethod_FLOW_RESET_STEP_METHOD_EXECUTE
	default:
		return dexpb.FlowResetStepMethod_FLOW_RESET_STEP_METHOD_UNSPECIFIED
	}
}

func stepTypeFromExecutionID(stepExecutionID string) string {
	separatorIndex := strings.LastIndex(stepExecutionID, "-")
	if separatorIndex <= 0 || separatorIndex == len(stepExecutionID)-1 {
		return ""
	}
	executionNumber, err := strconv.ParseUint(stepExecutionID[separatorIndex+1:], 10, 32)
	if err != nil || executionNumber == 0 {
		return ""
	}
	return stepExecutionID[:separatorIndex]
}

func matchesStepResetTarget(
	resetType dexpb.FlowResetType,
	requestedMethod dexpb.FlowResetStepMethod,
	candidateMethod dexpb.FlowResetStepMethod,
	requestedStepType string,
	requestedStepExecutionID string,
	candidateStepType string,
	candidateStepExecutionID string,
) bool {
	switch resetType {
	case dexpb.FlowResetType_FLOW_RESET_TYPE_STEP_TYPE:
		return candidateStepType == requestedStepType
	case dexpb.FlowResetType_FLOW_RESET_TYPE_STEP_EXECUTION_ID:
		return candidateStepExecutionID == requestedStepExecutionID && candidateMethod == requestedMethod
	default:
		return false
	}
}

func composeErrorWithMessage(msg string, err error) error {
	err = fmt.Errorf("%v, %v", msg, err)
	return err
}
