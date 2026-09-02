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
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/superdurable/dex/gen/dexpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type flowCommand struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
}

func newFlowCommand(stdin io.Reader, stdout io.Writer, stderr io.Writer) *flowCommand {
	return &flowCommand{stdin: stdin, stdout: stdout, stderr: stderr}
}

func (c *flowCommand) Execute(ctx context.Context, args []string, options options) error {
	if len(args) == 0 {
		c.printUsage()
		return nil
	}
	switch args[0] {
	case "start":
		return executeStart(c, ctx, args[1:], options)
	case "wait":
		return executeWait(c, ctx, args[1:], options)
	case "search":
		return c.search(ctx, args[1:], options)
	case "summary":
		return c.summary(ctx, args[1:], options)
	case "state":
		return c.state(ctx, args[1:], options)
	case "history":
		return c.history(ctx, args[1:], options)
	case "channel-messages":
		return c.channelMessages(ctx, args[1:], options)
	case "delete-channel-message":
		return c.deleteChannelMessage(ctx, args[1:], options)
	case "inspect":
		return executeInspect(c, ctx, args[1:], options)
	case "watch":
		return executeWatch(c, ctx, args[1:], options)
	case "stop":
		return executeStop(c, ctx, args[1:], options)
	case "skip-timer":
		return executeSkipTimer(c, ctx, args[1:], options)
	case "wait-step":
		return executeWaitStep(c, ctx, args[1:], options)
	case "update-config":
		return executeUpdateConfig(c, ctx, args[1:], options)
	case "trigger-continue-as-new":
		return executeTriggerContinueAsNew(c, ctx, args[1:], options)
	case "time-travel":
		return executeTimeTravel(c, ctx, args[1:], options)
	case "help", "--help", "-h":
		c.printUsage()
		return nil
	default:
		return newUsageError("flow", fmt.Errorf("unknown command %q", args[0]))
	}
}

func (c *flowCommand) channelMessages(ctx context.Context, args []string, options options) error {
	flags := newFlagSet("dexcli flow channel-messages", c.stderr)
	runID := flags.String("run-id", "", "Flow run ID")
	channelName := flags.String("channel", "", "physical Channel name")
	addCommonFlags(flags, &options)
	if done, err := parseFlowFlags(flags, args, c.stdout, "dexcli flow channel-messages FLOW_ID --channel NAME [flags]"); done || err != nil {
		return err
	}
	flowID, err := oneFlowID(flags, "flow channel-messages")
	if err != nil {
		return err
	}
	if strings.TrimSpace(*channelName) == "" {
		return newUsageError("flow channel-messages", fmt.Errorf("channel is required"))
	}
	return withFlowService(ctx, options, func(callCtx context.Context, client *flowService) error {
		response, callErr := client.service.GetChannelMessages(callCtx, &dexpb.GetChannelMessagesRequest{
			FlowId: flowID, RunId: *runID, ChannelName: *channelName,
		})
		if callErr != nil {
			return newOperationError("flow channel-messages", callErr)
		}
		mapped, warnings, mapErr := naturalMessage(callCtx, client.service, response, options.noHydrate)
		if mapErr != nil {
			return newOperationError("flow channel-messages", mapErr)
		}
		mapped["flowId"] = flowID
		mapped["runId"] = *runID
		mapped["channelName"] = *channelName
		if len(warnings) > 0 {
			mapped["warnings"] = warnings
		}
		return writeOutput(c.stdout, options.output, mapped)
	})
}

func (c *flowCommand) deleteChannelMessage(ctx context.Context, args []string, options options) error {
	flags := newFlagSet("dexcli flow delete-channel-message", c.stderr)
	runID := flags.String("run-id", "", "Flow run ID")
	channelName := flags.String("channel", "", "physical Channel name")
	messageID := flags.String("message-id", "", "server-assigned message ID")
	yes := flags.Bool("yes", false, "confirm the operation")
	addCommonFlags(flags, &options)
	if done, err := parseFlowFlags(flags, args, c.stdout, "dexcli flow delete-channel-message FLOW_ID --channel NAME --message-id ID --yes"); done || err != nil {
		return err
	}
	flowID, err := oneFlowID(flags, "flow delete-channel-message")
	if err != nil {
		return err
	}
	if strings.TrimSpace(*channelName) == "" || strings.TrimSpace(*messageID) == "" {
		return newUsageError("flow delete-channel-message", fmt.Errorf("channel and message-id are required"))
	}
	if !*yes {
		return newConfirmationError("flow delete-channel-message")
	}
	return withFlowService(ctx, options, func(callCtx context.Context, client *flowService) error {
		_, callErr := client.service.DeleteChannelMessage(callCtx, &dexpb.DeleteChannelMessageRequest{
			FlowId: flowID, RunId: *runID, ChannelName: *channelName,
			MessageId: *messageID, RequestId: uuid.NewString(),
		})
		if callErr != nil {
			return newOperationError("flow delete-channel-message", callErr)
		}
		return writeOutput(c.stdout, options.output, map[string]any{
			"flowId": flowID, "runId": *runID, "channelName": *channelName,
			"messageId": *messageID, "deleted": true,
		})
	})
}

func (c *flowCommand) search(ctx context.Context, args []string, options options) error {
	flags := newFlagSet("dexcli flow search", c.stderr)
	query := flags.String("query", "", "visibility query")
	pageSize := flags.Int("page-size", 50, "page size")
	pageToken := flags.String("page-token", "", "next page token")
	all := flags.Bool("all", false, "load every page")
	addCommonFlags(flags, &options)
	if done, err := parseFlowFlags(flags, args, c.stdout, "dexcli flow search [flags]"); done || err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return newUsageError("flow search", fmt.Errorf("unexpected arguments: %v", flags.Args()))
	}
	if *pageSize <= 0 {
		return newUsageError("flow search", fmt.Errorf("page-size must be positive"))
	}
	return withFlowService(ctx, options, func(callCtx context.Context, client *flowService) error {
		flows := make([]any, 0)
		nextToken := *pageToken
		for {
			response, err := client.service.SearchFlows(callCtx, &dexpb.SearchFlowsRequest{
				Query: *query, PageSize: int32(*pageSize), NextPageToken: nextToken,
			})
			if err != nil {
				return newOperationError("flow search", err)
			}
			for _, entry := range response.GetFlowRuns() {
				mapped, warnings, mapErr := naturalMessage(callCtx, client.service, entry, options.noHydrate)
				if mapErr != nil {
					return newOperationError("flow search", mapErr)
				}
				if len(warnings) > 0 {
					mapped["warnings"] = warnings
				}
				mapped["flowStatusCode"] = int32(entry.GetFlowStatus())
				flows = append(flows, mapped)
			}
			nextToken = response.GetNextPageToken()
			if !*all || nextToken == "" {
				break
			}
		}
		return writeOutput(c.stdout, options.output, map[string]any{
			"flows": flows, "nextPageToken": nextToken,
		})
	})
}

func (c *flowCommand) summary(ctx context.Context, args []string, options options) error {
	flags := newFlagSet("dexcli flow summary", c.stderr)
	runID := flags.String("run-id", "", "Flow run ID")
	addCommonFlags(flags, &options)
	if done, err := parseFlowFlags(flags, args, c.stdout, "dexcli flow summary FLOW_ID [flags]"); done || err != nil {
		return err
	}
	flowID, err := oneFlowID(flags, "flow summary")
	if err != nil {
		return err
	}
	return withFlowService(ctx, options, func(callCtx context.Context, client *flowService) error {
		response, callErr := client.service.GetFlowSummary(callCtx, &dexpb.GetFlowSummaryRequest{
			FlowId: flowID, RunId: *runID,
		})
		if callErr != nil {
			return newOperationError("flow summary", callErr)
		}
		return writeOutput(c.stdout, options.output, flowSummary(response))
	})
}

func (c *flowCommand) state(ctx context.Context, args []string, options options) error {
	flags := newFlagSet("dexcli flow state", c.stderr)
	runID := flags.String("run-id", "", "Flow run ID")
	addCommonFlags(flags, &options)
	if done, err := parseFlowFlags(flags, args, c.stdout, "dexcli flow state FLOW_ID [flags]"); done || err != nil {
		return err
	}
	flowID, err := oneFlowID(flags, "flow state")
	if err != nil {
		return err
	}
	return withFlowService(ctx, options, func(callCtx context.Context, client *flowService) error {
		resolvedRunID, resolveErr := resolveRunID(callCtx, client.service, flowID, *runID)
		if resolveErr != nil {
			return newOperationError("flow state", resolveErr)
		}
		response, callErr := client.service.GetFlowState(callCtx, &dexpb.GetFlowStateRequest{
			FlowId: flowID, RunId: resolvedRunID,
		})
		if callErr != nil {
			return newOperationError("flow state", callErr)
		}
		mapped, warnings, mapErr := naturalMessage(callCtx, client.service, response, options.noHydrate)
		if mapErr != nil {
			return newOperationError("flow state", mapErr)
		}
		mapped["flowId"] = flowID
		mapped["runId"] = resolvedRunID
		if len(warnings) > 0 {
			mapped["warnings"] = warnings
		}
		return writeOutput(c.stdout, options.output, mapped)
	})
}

func (c *flowCommand) history(ctx context.Context, args []string, options options) error {
	flags := newFlagSet("dexcli flow history", c.stderr)
	runID := flags.String("run-id", "", "Flow run ID")
	startEventID := flags.Int64("start-event-id", 0, "inclusive internal event cursor")
	pageSize := flags.Int("page-size", 200, "estimated backend page size")
	pageToken := flags.String("page-token", "", "base64 next page token")
	all := flags.Bool("all", false, "load every page")
	addCommonFlags(flags, &options)
	if done, err := parseFlowFlags(flags, args, c.stdout, "dexcli flow history FLOW_ID [flags]"); done || err != nil {
		return err
	}
	flowID, err := oneFlowID(flags, "flow history")
	if err != nil {
		return err
	}
	if *startEventID < 0 || *pageSize <= 0 {
		return newUsageError("flow history", fmt.Errorf("start-event-id must be non-negative and page-size positive"))
	}
	decodedToken, err := base64.StdEncoding.DecodeString(*pageToken)
	if err != nil {
		return newUsageError("flow history", fmt.Errorf("page-token must be base64: %w", err))
	}
	return withFlowService(ctx, options, func(callCtx context.Context, client *flowService) error {
		resolvedRunID, resolveErr := resolveRunID(callCtx, client.service, flowID, *runID)
		if resolveErr != nil {
			return newOperationError("flow history", resolveErr)
		}
		page, pageErr := loadHistory(callCtx, client.service, flowID, resolvedRunID, *startEventID, int32(*pageSize), decodedToken, *all, options.noHydrate)
		if pageErr != nil {
			return newOperationError("flow history", pageErr)
		}
		return writeOutput(c.stdout, options.output, page)
	})
}

func parseFlowFlags(flags *flag.FlagSet, args []string, output io.Writer, usage string) (bool, error) {
	if err := flags.Parse(interspersedArgs(flags, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(output, "Usage:", usage)
			return true, nil
		}
		return false, newUsageError(strings.TrimPrefix(flags.Name(), "dexcli "), err)
	}
	parsedOptions, err := optionsFromFlags(flags)
	if err != nil {
		return false, newUsageError(strings.TrimPrefix(flags.Name(), "dexcli "), err)
	}
	if err := parsedOptions.validate(); err != nil {
		return false, newUsageError(strings.TrimPrefix(flags.Name(), "dexcli "), err)
	}
	return false, nil
}

func interspersedArgs(flags *flag.FlagSet, args []string) []string {
	options := make([]string, 0, len(args))
	positionals := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		argument := args[index]
		if !strings.HasPrefix(argument, "-") || argument == "-" {
			positionals = append(positionals, argument)
			continue
		}
		options = append(options, argument)
		name := strings.TrimLeft(strings.SplitN(argument, "=", 2)[0], "-")
		item := flags.Lookup(name)
		if strings.Contains(argument, "=") || item == nil {
			continue
		}
		if boolFlag, ok := item.Value.(interface{ IsBoolFlag() bool }); ok && boolFlag.IsBoolFlag() {
			continue
		}
		if index+1 < len(args) {
			index++
			options = append(options, args[index])
		}
	}
	return append(options, positionals...)
}

func oneFlowID(flags *flag.FlagSet, operation string) (string, error) {
	if flags.NArg() != 1 || strings.TrimSpace(flags.Arg(0)) == "" {
		return "", newUsageError(operation, fmt.Errorf("exactly one FLOW_ID is required"))
	}
	return flags.Arg(0), nil
}

func resolveRunID(
	ctx context.Context,
	client dexpb.FlowServiceClient,
	flowID string,
	runID string,
) (string, error) {
	if runID != "" {
		return runID, nil
	}
	response, err := client.GetFlowSummary(ctx, &dexpb.GetFlowSummaryRequest{FlowId: flowID})
	if err != nil {
		return "", err
	}
	resolved := response.GetFlowExecutionId().GetRunId()
	if resolved == "" {
		return "", fmt.Errorf("GetFlowSummary response has no run ID")
	}
	return resolved, nil
}

func flowSummary(response *dexpb.GetFlowSummaryResponse) map[string]any {
	execution := response.GetFlowExecutionId()
	return map[string]any{
		"flowId": execution.GetFlowId(), "runId": execution.GetRunId(),
		"firstRunId": response.GetFirstRunId(), "requestId": response.GetRequestId(),
		"flowType": response.GetFlowType(), "flowStatus": response.GetFlowStatus().String(),
		"flowStatusCode": int32(response.GetFlowStatus()),
		"startTime":      protoTimestamp(response.GetStartTime()), "closeTime": protoTimestamp(response.GetCloseTime()),
	}
}

func protoTimestamp(timestamp *timestamppb.Timestamp) any {
	if timestamp == nil {
		return nil
	}
	return timestamp.AsTime().Format("2006-01-02T15:04:05.999999999Z07:00")
}

func (c *flowCommand) printUsage() {
	fmt.Fprintln(c.stdout, "Usage: dexcli flow <command>")
	fmt.Fprintln(c.stdout)
	fmt.Fprintln(c.stdout, "Commands: start, wait, search, summary, state, history, channel-messages, delete-channel-message, inspect, watch, stop, skip-timer, wait-step, update-config, trigger-continue-as-new, time-travel")
}

func parseInt32(value string, name string) (int32, error) {
	parsed, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	return int32(parsed), nil
}
