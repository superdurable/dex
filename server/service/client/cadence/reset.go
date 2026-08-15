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
	"strconv"
	"strings"
)

func getResetIDsByType(
	ctx context.Context,
	resetType dexpb.FlowResetType,
	domain, wid, rid string,
	frontendClient workflowserviceclient.Interface, converter encoded.DataConverter,
	earliestHistoryTimeStr string, stepType, stepExecutionId string,
	stepMethod dexpb.FlowResetStepMethod,
) (resetBaseRunID string, decisionFinishID int64, err error) {
	// default to the same runID
	resetBaseRunID = rid

	switch resetType {
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
		decisionFinishID, err = getDecisionEventIDByStepTypeOrStepExecutionId(
			ctx,
			domain,
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
		return 0, composeErrorWithMessage("find reset boundary failed", fmt.Errorf("no decision task boundary"))
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
		return 0, composeErrorWithMessage("find reset boundary failed", fmt.Errorf("no decision task boundary"))
	}
	return
}

// getDecisionEventIDByStepTypeOrStepExecutionId scans the invoke-method activities
// (both wait-for and execute) whose request shapes share step_type/context fields.
func getDecisionEventIDByStepTypeOrStepExecutionId(
	ctx context.Context,
	domain string, wid string,
	rid string,
	resetType dexpb.FlowResetType,
	stepType, stepExecutionId string,
	stepMethod dexpb.FlowResetStepMethod,
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
		for _, event := range resp.GetHistory().GetEvents() {
			if event.GetEventType() == shared.EventTypeDecisionTaskCompleted {
				decisionFinishID = event.GetEventId()
			}
			if event.GetEventType() == shared.EventTypeActivityTaskScheduled {
				typeName := event.GetActivityTaskScheduledEventAttributes().GetActivityType().GetName()
				if strings.Contains(typeName, "InvokeExecuteMethod") {
					var input dexpb.InvokeExecuteMethodActivityInput
					var localInput *dexpb.InternalLocalActivityInput
					err = converter.FromData(
						event.GetActivityTaskScheduledEventAttributes().Input,
						&input,
						&localInput,
					)
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
					var localInput *dexpb.InternalLocalActivityInput
					err = converter.FromData(
						event.GetActivityTaskScheduledEventAttributes().Input,
						&input,
						&localInput,
					)
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
								fmt.Errorf("activity was scheduled before a decision task completed"),
							)
						}
						return
					}
				}
			}
			if event.GetEventType() == shared.EventTypeMarkerRecorded {
				var isMatch bool
				isMatch, decisionFinishID, err = matchesCadenceLocalActivityResetTarget(
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
							fmt.Errorf("local activity marker has no decision task boundary"),
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
	return 0, composeErrorWithMessage("find reset boundary failed", fmt.Errorf("no decision task boundary"))
}

func matchesCadenceLocalActivityResetTarget(
	event *shared.HistoryEvent,
	resetType dexpb.FlowResetType,
	requestedMethod dexpb.FlowResetStepMethod,
	requestedStepType string,
	requestedStepExecutionID string,
	dataConverter encoded.DataConverter,
) (bool, int64, error) {
	attributes := event.GetMarkerRecordedEventAttributes()
	if attributes.GetMarkerName() != "LocalActivity" {
		return false, 0, nil
	}
	var marker localActivityMarkerData
	if err := dataConverter.FromData(attributes.GetDetails(), &marker); err != nil {
		return false, 0, err
	}
	candidateMethod := stepMethodFromActivityType(marker.ActivityType)
	if candidateMethod == dexpb.FlowResetStepMethod_FLOW_RESET_STEP_METHOD_UNSPECIFIED {
		return false, 0, nil
	}
	candidateStepExecutionID, err := cadenceLocalActivityStepExecutionID(
		marker,
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
	), attributes.GetDecisionTaskCompletedEventId(), nil
}

func cadenceLocalActivityStepExecutionID(
	marker localActivityMarkerData,
	method dexpb.FlowResetStepMethod,
	dataConverter encoded.DataConverter,
) (string, error) {
	if marker.ResultJSON == "" {
		if marker.ErrJSON == "" {
			return "", nil
		}
		var metadata *dexpb.InternalLocalStepActivityFailure
		if err := dataConverter.FromData([]byte(marker.ErrJSON), &metadata); err != nil {
			return "", err
		}
		return metadata.GetLocalActivityMetadata().GetCurrentStepExecutionId(), nil
	}
	result := []byte(marker.ResultJSON)
	if method == dexpb.FlowResetStepMethod_FLOW_RESET_STEP_METHOD_WAIT_FOR {
		var output dexpb.InvokeWaitForMethodActivityOutput
		if err := dataConverter.FromData(result, &output); err != nil {
			return "", err
		}
		return output.GetResponse().GetLocalActivityMetadata().GetCurrentStepExecutionId(), nil
	}
	var output dexpb.InvokeExecuteMethodActivityOutput
	if err := dataConverter.FromData(result, &output); err != nil {
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
