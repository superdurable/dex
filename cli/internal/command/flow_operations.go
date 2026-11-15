// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package command

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
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
			mapped, valueWarnings, mapErr := naturalMessage(ctx, client, flowID, event, noHydrate)
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
		resolvedRunID := summaryResponse.GetFlowRunId().GetRunId()
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
			state, warnings, mapErr := naturalMessage(callCtx, client.service, flowID, stateResponse, options.noHydrate)
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
		return watchRun(c, ctx, client.service, flowID, *runID, *fromEventID, int32(*pageSize), options)
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
			mapped, warnings, mapErr := naturalMessage(ctx, client, flowID, event, options.noHydrate)
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
			return nil
		}
		if nextEventID != lastStateEventID {
			stateResponse, stateErr := client.GetFlowState(ctx, &dexpb.GetFlowStateRequest{
				FlowId: flowID, RunId: currentRunID,
			})
			if stateErr != nil {
				return newOperationError("flow watch", stateErr)
			}
			state, warnings, mapErr := naturalMessage(ctx, client, flowID, stateResponse, options.noHydrate)
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
	stopTypeName := flags.String("type", "cancel", "cancel, terminate, or fail")
	reason := flags.String("reason", "", "stop reason")
	yes := flags.Bool("yes", false, "confirm the operation")
	addCommonFlags(flags, &options)
	if done, err := parseFlowFlags(flags, args, c.stdout, "dexcli flow stop FLOW_ID [--run-id ID] [--type TYPE] [--reason TEXT] --yes"); done || err != nil {
		return err
	}
	flowID, err := oneFlowID(flags, "flow stop")
	if err != nil {
		return err
	}
	stopType, err := parseStopType(*stopTypeName)
	if err != nil {
		return newUsageError("flow stop", err)
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

func executeTimeTravel(c *flowCommand, ctx context.Context, args []string, options options) error {
	flags := newFlagSet("dexcli flow time-travel", c.stderr)
	runID := flags.String("run-id", "", "Flow run ID")
	timeTravelTypeName := flags.String("type", "", "time travel point type")
	target := flags.String("target", "", "time travel point value")
	stepMethodName := flags.String("step-method", "", "WaitFor or Execute boundary")
	runAttempt := flags.Int("run-attempt", 0, "run attempt whose history holds the point; 0 selects the current attempt")
	subFlowResetPolicyName := flags.String("sub-flow-reset-policy", "", subFlowResetPolicyUsage)
	reason := flags.String("reason", "", "time travel reason")
	shouldSkipWritesReapply := flags.Bool(
		"skip-writes-reapply",
		false,
		"skip RPC, Channel publication, and Attribute write reapply",
	)
	yes := flags.Bool("yes", false, "confirm the operation")
	addCommonFlags(flags, &options)
	if done, err := parseFlowFlags(flags, args, c.stdout, "dexcli flow time-travel FLOW_ID [--run-id ID] --type TYPE [--target VALUE] [--run-attempt N] [--sub-flow-reset-policy POLICY] [--reason TEXT] --yes"); done || err != nil {
		return err
	}
	flowID, err := oneFlowID(flags, "flow time-travel")
	if err != nil {
		return err
	}
	request, err := timeTravelRequest(
		flowID,
		*runID,
		*timeTravelTypeName,
		*target,
		*stepMethodName,
		*reason,
	)
	if err != nil {
		return newUsageError("flow time-travel", err)
	}
	request.RunAttempt, err = parseRunAttempt(*runAttempt)
	if err != nil {
		return newUsageError("flow time-travel", err)
	}
	request.SubFlowResetPolicy, err = parseSubFlowResetPolicy(*subFlowResetPolicyName)
	if err != nil {
		return newUsageError("flow time-travel", err)
	}
	request.SkipWritesReapply = *shouldSkipWritesReapply
	if !*yes {
		return newConfirmationError("flow time-travel")
	}
	return withFlowService(ctx, options, func(callCtx context.Context, client *flowService) error {
		response, callErr := client.service.ResetFlow(callCtx, request)
		if callErr != nil {
			return newOperationError("flow time-travel", callErr)
		}
		return writeOutput(c.stdout, options.output, map[string]any{
			"flowId": flowID, "previousRunId": *runID, "runId": response.GetRunId(),
			"runAttempt": response.GetRunAttempt(),
		})
	})
}

func timeTravelRequest(
	flowID string,
	runID string,
	typeName string,
	target string,
	stepMethodName string,
	reason string,
) (*dexpb.ResetFlowRequest, error) {
	point, err := parseHistoryPoint(typeName, target, stepMethodName, false)
	if err != nil {
		return nil, err
	}
	request := &dexpb.ResetFlowRequest{
		FlowId: flowID, RunId: runID, Reason: reason,
		HistoryEventTime: point.historyEventTime, StepType: point.stepType,
		StepExecutionId: point.stepExecutionID, StepMethod: point.stepMethod, EventId: point.eventID,
	}
	switch typeName {
	case "beginning":
		request.ResetType = dexpb.FlowResetType_FLOW_RESET_TYPE_BEGINNING
	case "history-event-time":
		request.ResetType = dexpb.FlowResetType_FLOW_RESET_TYPE_HISTORY_EVENT_TIME
	case "step-type":
		request.ResetType = dexpb.FlowResetType_FLOW_RESET_TYPE_STEP_TYPE
	case "step-execution-id":
		request.ResetType = dexpb.FlowResetType_FLOW_RESET_TYPE_STEP_EXECUTION_ID
	case "event-id":
		request.ResetType = dexpb.FlowResetType_FLOW_RESET_TYPE_EVENT_ID
	}
	return request, nil
}

// historyPoint holds the request fields that select a point of a Flow run's history.
type historyPoint struct {
	historyEventTime string
	stepType         string
	stepExecutionID  string
	stepMethod       dexpb.FlowResetStepMethod
	eventID          int64
}

// parseHistoryPoint validates a --type, --target, and --step-method combination. It returns the
// point fields to copy into the request, or an error naming the invalid flag.
func parseHistoryPoint(
	typeName string,
	target string,
	stepMethodName string,
	isCurrentAllowed bool,
) (historyPoint, error) {
	var point historyPoint
	switch typeName {
	case "beginning":
	case "current":
		if !isCurrentAllowed {
			return historyPoint{}, fmt.Errorf("type must be beginning, history-event-time, step-type, step-execution-id, or event-id")
		}
	case "history-event-time":
		if _, err := time.Parse(time.RFC3339, target); err != nil {
			return historyPoint{}, fmt.Errorf("target must be an RFC3339 timestamp: %w", err)
		}
		point.historyEventTime = target
	case "step-type":
		point.stepType = target
	case "step-execution-id":
		point.stepExecutionID = target
		switch stepMethodName {
		case "wait-for":
			point.stepMethod = dexpb.FlowResetStepMethod_FLOW_RESET_STEP_METHOD_WAIT_FOR
		case "execute":
			point.stepMethod = dexpb.FlowResetStepMethod_FLOW_RESET_STEP_METHOD_EXECUTE
		default:
			return historyPoint{}, fmt.Errorf("step-method must be wait-for or execute for step-execution-id")
		}
	case "event-id":
		eventID, err := strconv.ParseInt(target, 10, 64)
		if err != nil || eventID <= 0 {
			return historyPoint{}, fmt.Errorf("target must be a positive history event ID for event-id")
		}
		point.eventID = eventID
	default:
		if isCurrentAllowed {
			return historyPoint{}, fmt.Errorf("type must be current, beginning, history-event-time, step-type, step-execution-id, or event-id")
		}
		return historyPoint{}, fmt.Errorf("type must be beginning, history-event-time, step-type, step-execution-id, or event-id")
	}
	if typeName != "step-execution-id" && stepMethodName != "" {
		return historyPoint{}, fmt.Errorf("step-method is only valid for step-execution-id")
	}
	if typeName != "beginning" && typeName != "current" && strings.TrimSpace(target) == "" {
		return historyPoint{}, fmt.Errorf("target is required for type %s", typeName)
	}
	return point, nil
}

func parseRunAttempt(runAttempt int) (int32, error) {
	if runAttempt < 0 || runAttempt > math.MaxInt32 {
		return 0, fmt.Errorf("run-attempt must be between 0 and %d", math.MaxInt32)
	}
	return int32(runAttempt), nil
}

const subFlowResetPolicyUsage = "attach-else-restart (default), attach-else-fail, attach-else-reject, always-restart, always-fail, or always-reject"

func parseSubFlowResetPolicy(value string) (dexpb.SubFlowResetPolicy, error) {
	switch value {
	case "":
		return dexpb.SubFlowResetPolicy_SUB_FLOW_RESET_POLICY_UNSPECIFIED, nil
	case "attach-else-restart":
		return dexpb.SubFlowResetPolicy_SUB_FLOW_RESET_POLICY_ATTACH_ELSE_RESTART, nil
	case "attach-else-fail":
		return dexpb.SubFlowResetPolicy_SUB_FLOW_RESET_POLICY_ATTACH_ELSE_FAIL, nil
	case "attach-else-reject":
		return dexpb.SubFlowResetPolicy_SUB_FLOW_RESET_POLICY_ATTACH_ELSE_REJECT, nil
	case "always-restart":
		return dexpb.SubFlowResetPolicy_SUB_FLOW_RESET_POLICY_ALWAYS_RESTART, nil
	case "always-fail":
		return dexpb.SubFlowResetPolicy_SUB_FLOW_RESET_POLICY_ALWAYS_FAIL, nil
	case "always-reject":
		return dexpb.SubFlowResetPolicy_SUB_FLOW_RESET_POLICY_ALWAYS_REJECT, nil
	default:
		return dexpb.SubFlowResetPolicy_SUB_FLOW_RESET_POLICY_UNSPECIFIED, fmt.Errorf("sub-flow-reset-policy must be %s", subFlowResetPolicyUsage)
	}
}

func executeCloneFork(c *flowCommand, ctx context.Context, args []string, options options) error {
	flags := newFlagSet("dexcli flow clone-fork", c.stderr)
	newFlowID := flags.String("flow-id", "", "Flow ID of the new Flow")
	sourceRunID := flags.String("source-run-id", "", "source Flow run ID; empty selects the latest run")
	pointTypeName := flags.String("type", "current", "copied point type")
	target := flags.String("target", "", "copied point value")
	stepMethodName := flags.String("step-method", "", "WaitFor or Execute boundary")
	runAttempt := flags.Int("run-attempt", 0, "source run attempt whose history holds the point; 0 selects the current attempt")
	attributesSource := flags.String("attributes", "", "Attribute JSON written after the copy, @file, or - for stdin")
	configSource := flags.String("config", "", "Flow configuration override JSON, @file, or - for stdin")
	taskQueue := flags.String("task-queue", "", "task queue of the new Flow; empty keeps the source run's task queue")
	flowTimeout := flags.String("flow-timeout", "", "Flow timeout duration; empty keeps the source run's duration")
	idReusePolicy := flags.String("id-reuse-policy", "", "previous-failed, not-running, disallow, or terminate-running")
	ignoreAlreadyStarted := flags.Bool("ignore-already-started", false, "return the existing run when the request ID matches")
	subFlowResetPolicyName := flags.String("sub-flow-reset-policy", "", subFlowResetPolicyUsage)
	reason := flags.String("reason", "", "clone fork reason")
	requestID := flags.String("request-id", "", "idempotency request ID")
	yes := flags.Bool("yes", false, "confirm the operation")
	addCommonFlags(flags, &options)
	if done, err := parseFlowFlags(flags, args, c.stdout, "dexcli flow clone-fork SOURCE_FLOW_ID --flow-id NEW_FLOW_ID [--source-run-id ID] [--type TYPE] [--target VALUE] [flags] --yes"); done || err != nil {
		return err
	}
	sourceFlowID, err := oneFlowID(flags, "flow clone-fork")
	if err != nil {
		return err
	}
	if err := atMostOneStdinSource(*attributesSource, *configSource); err != nil {
		return newUsageError("flow clone-fork", err)
	}
	request, err := cloneForkFlowRequest(c.stdin, cloneForkFlowFlags{
		sourceFlowID: sourceFlowID, sourceRunID: *sourceRunID, flowID: *newFlowID,
		pointTypeName: *pointTypeName, target: *target, stepMethodName: *stepMethodName,
		runAttempt: *runAttempt, attributesSource: *attributesSource, configSource: *configSource,
		taskQueue: *taskQueue, flowTimeout: *flowTimeout, idReusePolicy: *idReusePolicy,
		shouldIgnoreAlreadyStarted: *ignoreAlreadyStarted, subFlowResetPolicy: *subFlowResetPolicyName,
		reason: *reason, requestID: *requestID,
	})
	if err != nil {
		return newUsageError("flow clone-fork", err)
	}
	if !*yes {
		return newConfirmationError("flow clone-fork")
	}
	return withFlowService(ctx, options, func(callCtx context.Context, client *flowService) error {
		response, callErr := client.service.CloneForkFlow(callCtx, request)
		if callErr != nil {
			return newOperationError("flow clone-fork", callErr)
		}
		return writeOutput(c.stdout, options.output, map[string]any{
			"flowId": request.GetFlowId(), "runId": response.GetRunId(),
			"sourceFlowId": sourceFlowID, "sourceRunId": response.GetSourceRunId(),
			"sourceRunAttempt": response.GetSourceRunAttempt(), "sourceEventId": response.GetSourceEventId(),
		})
	})
}

// cloneForkFlowFlags holds the parsed flag values of dexcli flow clone-fork.
type cloneForkFlowFlags struct {
	sourceFlowID               string
	sourceRunID                string
	flowID                     string
	pointTypeName              string
	target                     string
	stepMethodName             string
	runAttempt                 int
	attributesSource           string
	configSource               string
	taskQueue                  string
	flowTimeout                string
	idReusePolicy              string
	shouldIgnoreAlreadyStarted bool
	subFlowResetPolicy         string
	reason                     string
	requestID                  string
}

func cloneForkFlowRequest(reader io.Reader, values cloneForkFlowFlags) (*dexpb.CloneForkFlowRequest, error) {
	if strings.TrimSpace(values.flowID) == "" {
		return nil, fmt.Errorf("flow-id is required")
	}
	if values.flowID == values.sourceFlowID {
		return nil, fmt.Errorf("flow-id must differ from the source Flow ID; use flow time-travel to return a Flow to an earlier point")
	}
	point, err := parseHistoryPoint(values.pointTypeName, values.target, values.stepMethodName, true)
	if err != nil {
		return nil, err
	}
	runAttempt, err := parseRunAttempt(values.runAttempt)
	if err != nil {
		return nil, err
	}
	if values.pointTypeName == "current" && runAttempt != 0 {
		return nil, fmt.Errorf("run-attempt is not valid for type current")
	}
	attributes, err := parseInitialAttributes(reader, values.attributesSource)
	if err != nil {
		return nil, fmt.Errorf("attributes: %w", err)
	}
	config, err := parseOptionalFlowConfig(reader, values.configSource)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if values.taskQueue != "" {
		if config == nil {
			config = &dexpb.FlowConfig{}
		}
		config.WorkerTarget = &dexpb.WorkerTarget{IsPullMode: true, TaskQueue: values.taskQueue}
	}
	timeoutSeconds, err := parseOptionalDurationSeconds(values.flowTimeout, "flow-timeout")
	if err != nil {
		return nil, err
	}
	reusePolicy, err := parseIDReusePolicy(values.idReusePolicy)
	if err != nil {
		return nil, err
	}
	subFlowResetPolicy, err := parseSubFlowResetPolicy(values.subFlowResetPolicy)
	if err != nil {
		return nil, err
	}
	requestID := values.requestID
	if requestID == "" {
		requestID = uuid.NewString()
	}
	request := &dexpb.CloneForkFlowRequest{
		SourceFlowId: values.sourceFlowID, SourceRunId: values.sourceRunID, FlowId: values.flowID,
		RequestId: requestID, IdReusePolicy: reusePolicy, FlowConfigOverride: config,
		Attributes: attributes, FlowTimeoutSeconds: timeoutSeconds, Reason: values.reason,
		HistoryEventTime: point.historyEventTime, StepType: point.stepType,
		StepExecutionId: point.stepExecutionID, StepMethod: point.stepMethod, EventId: point.eventID,
		RunAttempt: runAttempt, SubFlowResetPolicy: subFlowResetPolicy,
	}
	if values.shouldIgnoreAlreadyStarted {
		request.FlowAlreadyStartedOptions = &dexpb.FlowAlreadyStartedOptions{IgnoreAlreadyStartedError: true}
	}
	switch values.pointTypeName {
	case "current":
		request.PointType = dexpb.FlowCloneForkPointType_FLOW_CLONE_FORK_POINT_TYPE_CURRENT
	case "beginning":
		request.PointType = dexpb.FlowCloneForkPointType_FLOW_CLONE_FORK_POINT_TYPE_BEGINNING
	case "history-event-time":
		request.PointType = dexpb.FlowCloneForkPointType_FLOW_CLONE_FORK_POINT_TYPE_HISTORY_EVENT_TIME
	case "step-type":
		request.PointType = dexpb.FlowCloneForkPointType_FLOW_CLONE_FORK_POINT_TYPE_STEP_TYPE
	case "step-execution-id":
		request.PointType = dexpb.FlowCloneForkPointType_FLOW_CLONE_FORK_POINT_TYPE_STEP_EXECUTION_ID
	case "event-id":
		request.PointType = dexpb.FlowCloneForkPointType_FLOW_CLONE_FORK_POINT_TYPE_EVENT_ID
	}
	return request, nil
}
