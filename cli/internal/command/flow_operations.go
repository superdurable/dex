// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package command

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/superdurable/dex/gen/dexpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func loadHistory(
	ctx context.Context,
	client dexpb.FlowServiceClient,
	flowID string,
	runID string,
	startEventID int64,
	pageSize int32,
	pageToken []byte,
	all bool,
	noHydrate bool,
) (map[string]any, error) {
	events := make([]any, 0)
	warnings := make([]string, 0)
	nextEventID := startEventID
	nextToken := pageToken
	for {
		response, err := client.GetHistoryEvents(ctx, &dexpb.GetHistoryEventsRequest{
			FlowId: flowID, RunId: runID, StartInternalEventId: nextEventID,
			EstimatePageSize: pageSize, NextPageToken: nextToken,
		})
		if err != nil {
			return nil, err
		}
		for _, event := range response.GetEvents() {
			mapped, valueWarnings, mapErr := naturalMessage(ctx, client, event, noHydrate)
			if mapErr != nil {
				return nil, mapErr
			}
			normalizeHistoryEvent(mapped, event)
			events = append(events, mapped)
			warnings = append(warnings, valueWarnings...)
		}
		nextEventID = response.GetNextInternalEventId()
		nextToken = response.GetNextPageToken()
		if !all || len(nextToken) == 0 {
			break
		}
	}
	result := map[string]any{
		"flowId": flowID, "runId": runID, "events": events,
		"nextPageToken":       base64.StdEncoding.EncodeToString(nextToken),
		"nextInternalEventId": nextEventID,
	}
	if len(warnings) > 0 {
		result["warnings"] = uniqueStrings(warnings)
	}
	return result, nil
}

func normalizeHistoryEvent(mapped map[string]any, event *dexpb.FlowHistoryEvent) {
	eventType := historyEventType(event)
	mapped["type"] = eventType
	payloadKey := historyPayloadKey(event)
	if payloadKey == "" {
		mapped["payload"] = nil
		return
	}
	mapped["payload"] = mapped[payloadKey]
	delete(mapped, payloadKey)
}

func historyPayloadKey(event *dexpb.FlowHistoryEvent) string {
	switch event.GetPayload().(type) {
	case *dexpb.FlowHistoryEvent_FlowStartedOrContinued:
		return "flowStartedOrContinued"
	case *dexpb.FlowHistoryEvent_FlowClosed:
		return "flowClosed"
	case *dexpb.FlowHistoryEvent_StepWaitForCompleted:
		return "stepWaitForCompleted"
	case *dexpb.FlowHistoryEvent_StepWaitForFailed:
		return "stepWaitForFailed"
	case *dexpb.FlowHistoryEvent_StepExecuteCompleted:
		return "stepExecuteCompleted"
	case *dexpb.FlowHistoryEvent_StepExecuteFailed:
		return "stepExecuteFailed"
	case *dexpb.FlowHistoryEvent_RpcExecutionCompleted:
		return "rpcExecutionCompleted"
	case *dexpb.FlowHistoryEvent_ChannelExternalPublish:
		return "channelExternalPublish"
	default:
		return ""
	}
}

func historyEventType(event *dexpb.FlowHistoryEvent) string {
	switch event.GetPayload().(type) {
	case *dexpb.FlowHistoryEvent_FlowStartedOrContinued:
		return "FlowStartedOrContinued"
	case *dexpb.FlowHistoryEvent_FlowClosed:
		return "FlowClosed"
	case *dexpb.FlowHistoryEvent_StepWaitForCompleted:
		return "StepWaitForCompleted"
	case *dexpb.FlowHistoryEvent_StepWaitForFailed:
		return "StepWaitForFailed"
	case *dexpb.FlowHistoryEvent_StepExecuteCompleted:
		return "StepExecuteCompleted"
	case *dexpb.FlowHistoryEvent_StepExecuteFailed:
		return "StepExecuteFailed"
	case *dexpb.FlowHistoryEvent_RpcExecutionCompleted:
		return "RpcExecutionCompleted"
	case *dexpb.FlowHistoryEvent_ChannelExternalPublish:
		return "ChannelExternalPublish"
	default:
		return "Unspecified"
	}
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, found := seen[value]; found {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func executeInspect(c *flowCommand, ctx context.Context, args []string, options options) error {
	flags := newFlagSet("dexcli flow inspect", c.stderr)
	runID := flags.String("run-id", "", "Flow run ID")
	allHistory := flags.Bool("all-history", false, "load all history pages")
	pageSize := flags.Int("page-size", 200, "estimated backend page size")
	addCommonFlags(flags, &options)
	if done, err := parseFlowFlags(flags, args, c.stdout, "dexcli flow inspect FLOW_ID [flags]"); done || err != nil {
		return err
	}
	flowID, err := oneFlowID(flags, "flow inspect")
	if err != nil {
		return err
	}
	if *pageSize <= 0 {
		return newUsageError("flow inspect", fmt.Errorf("page-size must be positive"))
	}
	return withFlowService(ctx, options, func(callCtx context.Context, client *flowService) error {
		summaryResponse, summaryErr := client.service.GetFlowSummary(callCtx, &dexpb.GetFlowSummaryRequest{
			FlowId: flowID, RunId: *runID,
		})
		if summaryErr != nil {
			return newOperationError("flow inspect", summaryErr)
		}
		resolvedRunID := summaryResponse.GetFlowExecutionId().GetRunId()
		history, historyErr := loadHistory(
			callCtx, client.service, flowID, resolvedRunID, 0, int32(*pageSize), nil,
			*allHistory, options.noHydrate,
		)
		if historyErr != nil {
			return newOperationError("flow inspect", historyErr)
		}
		result := map[string]any{"summary": flowSummary(summaryResponse), "history": history, "state": nil}
		if summaryResponse.GetFlowStatus() == dexpb.FlowStatus_FLOW_STATUS_RUNNING {
			stateResponse, stateErr := client.service.GetFlowState(callCtx, &dexpb.GetFlowStateRequest{
				FlowId: flowID, RunId: resolvedRunID,
			})
			if stateErr != nil {
				return newOperationError("flow inspect", stateErr)
			}
			state, warnings, mapErr := naturalMessage(callCtx, client.service, stateResponse, options.noHydrate)
			if mapErr != nil {
				return newOperationError("flow inspect", mapErr)
			}
			if len(warnings) > 0 {
				state["warnings"] = warnings
			}
			result["state"] = state
		}
		return writeOutput(c.stdout, options.output, result)
	})
}

func executeWatch(c *flowCommand, ctx context.Context, args []string, options options) error {
	flags := newFlagSet("dexcli flow watch", c.stderr)
	runID := flags.String("run-id", "", "Flow run ID")
	fromEventID := flags.Int64("from-event-id", 0, "inclusive internal event cursor")
	followRuns := flags.Bool("follow-runs", false, "follow Continue-As-New runs")
	pageSize := flags.Int("page-size", 200, "estimated backend page size")
	addCommonFlags(flags, &options)
	if done, err := parseFlowFlags(flags, args, c.stdout, "dexcli flow watch FLOW_ID [flags]"); done || err != nil {
		return err
	}
	flowID, err := oneFlowID(flags, "flow watch")
	if err != nil {
		return err
	}
	if *fromEventID < 0 || *pageSize <= 0 {
		return newUsageError("flow watch", fmt.Errorf("from-event-id must be non-negative and page-size positive"))
	}
	options.timeout = 0
	return withStreamingFlowService(ctx, options, func(client *flowService) error {
		return watchRun(c, ctx, client.service, flowID, *runID, *fromEventID, int32(*pageSize), *followRuns, options)
	})
}

func watchRun(
	c *flowCommand,
	ctx context.Context,
	client dexpb.FlowServiceClient,
	flowID string,
	runID string,
	fromEventID int64,
	pageSize int32,
	followRuns bool,
	options options,
) error {
	currentRunID, err := resolveRunID(ctx, client, flowID, runID)
	if err != nil {
		return newOperationError("flow watch", err)
	}
	nextEventID := fromEventID
	var nextPageToken []byte
	lastStateEventID := int64(-1)
	for {
		page, pageErr := client.GetHistoryEvents(ctx, &dexpb.GetHistoryEventsRequest{
			FlowId: flowID, RunId: currentRunID, StartInternalEventId: nextEventID,
			EstimatePageSize: pageSize, NextPageToken: nextPageToken,
		})
		if pageErr != nil {
			return newOperationError("flow watch", pageErr)
		}
		for _, event := range page.GetEvents() {
			mapped, warnings, mapErr := naturalMessage(ctx, client, event, options.noHydrate)
			if mapErr != nil {
				return newOperationError("flow watch", mapErr)
			}
			normalizeHistoryEvent(mapped, event)
			mapped["flowId"] = flowID
			mapped["runId"] = currentRunID
			if len(warnings) > 0 {
				mapped["warnings"] = warnings
			}
			if err := writeOutput(c.stdout, options.output, mapped); err != nil {
				return err
			}
		}
		nextEventID = page.GetNextInternalEventId()
		nextPageToken = page.GetNextPageToken()
		if len(nextPageToken) > 0 {
			continue
		}
		summary, summaryErr := client.GetFlowSummary(ctx, &dexpb.GetFlowSummaryRequest{
			FlowId: flowID, RunId: currentRunID,
		})
		if summaryErr != nil {
			return newOperationError("flow watch", summaryErr)
		}
		if summary.GetFlowStatus() != dexpb.FlowStatus_FLOW_STATUS_RUNNING {
			if followRuns && summary.GetFlowStatus() == dexpb.FlowStatus_FLOW_STATUS_CONTINUED_AS_NEW {
				currentRunID, err = resolveRunID(ctx, client, flowID, "")
				if err != nil {
					return newOperationError("flow watch", err)
				}
				nextEventID = 0
				nextPageToken = nil
				lastStateEventID = -1
				continue
			}
			return nil
		}
		if nextEventID != lastStateEventID {
			stateResponse, stateErr := client.GetFlowState(ctx, &dexpb.GetFlowStateRequest{
				FlowId: flowID, RunId: currentRunID,
			})
			if stateErr != nil {
				return newOperationError("flow watch", stateErr)
			}
			state, warnings, mapErr := naturalMessage(ctx, client, stateResponse, options.noHydrate)
			if mapErr != nil {
				return newOperationError("flow watch", mapErr)
			}
			snapshot := map[string]any{
				"type": "FlowStateSnapshot", "flowId": flowID, "runId": currentRunID,
				"nextInternalEventId": nextEventID, "state": state,
			}
			if len(warnings) > 0 {
				snapshot["warnings"] = warnings
			}
			if err := writeOutput(c.stdout, options.output, snapshot); err != nil {
				return err
			}
			lastStateEventID = nextEventID
		}
		_, waitErr := client.WaitForHistoryEvent(ctx, &dexpb.WaitForHistoryEventRequest{
			FlowId: flowID, RunId: currentRunID, NextInternalEventId: nextEventID,
		})
		if waitErr != nil && status.Code(waitErr) != codes.DeadlineExceeded {
			return newOperationError("flow watch", waitErr)
		}
	}
}

func executeStop(c *flowCommand, ctx context.Context, args []string, options options) error {
	flags := newFlagSet("dexcli flow stop", c.stderr)
	runID := flags.String("run-id", "", "Flow run ID")
	stopTypeName := flags.String("type", "", "cancel, terminate, or fail")
	reason := flags.String("reason", "", "stop reason")
	yes := flags.Bool("yes", false, "confirm the operation")
	addCommonFlags(flags, &options)
	if done, err := parseFlowFlags(flags, args, c.stdout, "dexcli flow stop FLOW_ID --run-id ID --type TYPE --yes"); done || err != nil {
		return err
	}
	flowID, err := oneFlowID(flags, "flow stop")
	if err != nil {
		return err
	}
	if *runID == "" {
		return newUsageError("flow stop", fmt.Errorf("run-id is required"))
	}
	stopType, err := parseStopType(*stopTypeName)
	if err != nil {
		return newUsageError("flow stop", err)
	}
	if stopType != dexpb.StopType_STOP_TYPE_CANCEL && strings.TrimSpace(*reason) == "" {
		return newUsageError("flow stop", fmt.Errorf("reason is required for terminate and fail"))
	}
	if !*yes {
		return newConfirmationError("flow stop")
	}
	return withFlowService(ctx, options, func(callCtx context.Context, client *flowService) error {
		_, callErr := client.service.StopFlow(callCtx, &dexpb.StopFlowRequest{
			FlowId: flowID, RunId: *runID, StopType: stopType, Reason: *reason,
		})
		if callErr != nil {
			return newOperationError("flow stop", callErr)
		}
		return writeOutput(c.stdout, options.output, map[string]any{
			"flowId": flowID, "runId": *runID, "stopped": true,
		})
	})
}

func parseStopType(value string) (dexpb.StopType, error) {
	switch value {
	case "cancel":
		return dexpb.StopType_STOP_TYPE_CANCEL, nil
	case "terminate":
		return dexpb.StopType_STOP_TYPE_TERMINATE, nil
	case "fail":
		return dexpb.StopType_STOP_TYPE_FAIL, nil
	default:
		return dexpb.StopType_STOP_TYPE_UNSPECIFIED, fmt.Errorf("type must be cancel, terminate, or fail")
	}
}

func executeReset(c *flowCommand, ctx context.Context, args []string, options options) error {
	flags := newFlagSet("dexcli flow reset", c.stderr)
	runID := flags.String("run-id", "", "Flow run ID")
	resetTypeName := flags.String("type", "", "reset point type")
	target := flags.String("target", "", "reset point value")
	reason := flags.String("reason", "", "reset reason")
	skipChannels := flags.Bool("skip-channel-reapply", false, "skip channel message reapply")
	skipLocks := flags.Bool("skip-locking-rpc-reapply", false, "skip locking RPC reapply")
	yes := flags.Bool("yes", false, "confirm the operation")
	addCommonFlags(flags, &options)
	if done, err := parseFlowFlags(flags, args, c.stdout, "dexcli flow reset FLOW_ID --run-id ID --type TYPE --reason TEXT --yes"); done || err != nil {
		return err
	}
	flowID, err := oneFlowID(flags, "flow reset")
	if err != nil {
		return err
	}
	request, err := resetRequest(flowID, *runID, *resetTypeName, *target, *reason)
	if err != nil {
		return newUsageError("flow reset", err)
	}
	request.SkipChannelMessagesReapply = *skipChannels
	request.SkipLockingRpcReapply = *skipLocks
	if !*yes {
		return newConfirmationError("flow reset")
	}
	return withFlowService(ctx, options, func(callCtx context.Context, client *flowService) error {
		response, callErr := client.service.ResetFlow(callCtx, request)
		if callErr != nil {
			return newOperationError("flow reset", callErr)
		}
		return writeOutput(c.stdout, options.output, map[string]any{
			"flowId": flowID, "previousRunId": *runID, "runId": response.GetRunId(),
		})
	})
}

func resetRequest(flowID string, runID string, typeName string, target string, reason string) (*dexpb.ResetFlowRequest, error) {
	if runID == "" || strings.TrimSpace(reason) == "" {
		return nil, fmt.Errorf("run-id and reason are required")
	}
	request := &dexpb.ResetFlowRequest{FlowId: flowID, RunId: runID, Reason: reason}
	switch typeName {
	case "beginning":
		request.ResetType = dexpb.FlowResetType_FLOW_RESET_TYPE_BEGINNING
	case "history-event-id":
		value, err := parseInt32(target, "target")
		if err != nil || value <= 0 {
			return nil, fmt.Errorf("target must be a positive history event ID")
		}
		request.ResetType = dexpb.FlowResetType_FLOW_RESET_TYPE_HISTORY_EVENT_ID
		request.HistoryEventId = value
	case "history-event-time":
		if _, err := time.Parse(time.RFC3339, target); err != nil {
			return nil, fmt.Errorf("target must be an RFC3339 timestamp: %w", err)
		}
		request.ResetType = dexpb.FlowResetType_FLOW_RESET_TYPE_HISTORY_EVENT_TIME
		request.HistoryEventTime = target
	case "step-type":
		request.ResetType = dexpb.FlowResetType_FLOW_RESET_TYPE_STEP_TYPE
		request.StepType = target
	case "step-execution-id":
		request.ResetType = dexpb.FlowResetType_FLOW_RESET_TYPE_STEP_EXECUTION_ID
		request.StepExecutionId = target
	default:
		return nil, fmt.Errorf("type must be beginning, history-event-id, history-event-time, step-type, or step-execution-id")
	}
	if typeName != "beginning" && strings.TrimSpace(target) == "" {
		return nil, fmt.Errorf("target is required for reset type %s", typeName)
	}
	return request, nil
}
