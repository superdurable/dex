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
	"errors"
	"flag"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	adminUsage         = "Usage: dexcli admin <describe|step-down> [flags]"
	adminDescribeUsage = "Usage: dexcli admin describe [global flags]"
	adminStepDownUsage = "Usage: dexcli admin step-down --yes " +
		"[--expected-node-id id --expected-node-epoch epoch] [global flags]"
	adminServiceName       = "AdminService"
	unknownStepDownOutcome = "unknown"
	// notAttemptedStepDownOutcome marks a failed describe before any step down request was sent.
	notAttemptedStepDownOutcome = "notAttempted"
	// adminStepDownDefaultTimeout exceeds the commit drain bound, hold-off, and lease attempt with a wide margin.
	adminStepDownDefaultTimeout = 2 * time.Minute
	notAttemptedStepDownHint    = "Nothing was stepped down. If a step down or its hold-off is in progress, " +
		"or no server process is reachable, wait past the hold-off and run the command again."
)

func (a *App) executeAdmin(ctx context.Context, args []string, options options) error {
	if len(args) == 0 {
		return newUsageError("admin", errors.New("expected subcommand describe or step-down"))
	}
	switch args[0] {
	case "describe":
		return a.executeAdminDescribe(ctx, args[1:], options)
	case "step-down":
		return a.executeAdminStepDown(ctx, args[1:], options)
	case "help", "--help", "-h":
		fmt.Fprintln(a.stdout, adminUsage)
		fmt.Fprintln(a.stdout)
		fmt.Fprintln(a.stdout, "Subcommands:")
		fmt.Fprintln(a.stdout, "  describe   Print the node ID, node epoch, and holder process ID of the active server process")
		fmt.Fprintln(a.stdout, "  step-down  Make the active server process give up its node lease")
		return nil
	default:
		return newUsageError("admin", fmt.Errorf("unknown subcommand %q", args[0]))
	}
}

// executeAdminDescribe prints the node lease of the active server process behind --server.
func (a *App) executeAdminDescribe(ctx context.Context, args []string, options options) error {
	flags := a.newAdminFlagSet("dexcli admin describe")
	addCommonFlags(flags, &options)
	if isHelp, err := a.parseAdminFlags(flags, args, adminDescribeUsage, options); isHelp || err != nil {
		return err
	}
	return withServerConnection(ctx, options, adminServiceName, func(callCtx context.Context, connection *grpc.ClientConn) error {
		response, err := dexpb.NewAdminServiceClient(connection).
			DescribeActiveNode(callCtx, &dexpb.DescribeActiveNodeRequest{})
		if err != nil {
			return newOperationError("admin describe", err)
		}
		return writeOutput(a.stdout, options.output, map[string]any{
			"nodeId":          response.GetNodeId(),
			"nodeEpoch":       strconv.FormatUint(response.GetNodeEpoch(), 10),
			"holderProcessId": response.GetHolderProcessId(),
		})
	})
}

// executeAdminStepDown makes the active server process behind --server give up its node lease.
//
// Every request carries a node ID and node epoch precondition. Without explicit flags, it comes
// from DescribeActiveNode on the same connection.
func (a *App) executeAdminStepDown(ctx context.Context, args []string, options options) error {
	if !options.isTimeoutSet {
		options.timeout = adminStepDownDefaultTimeout
	}
	flags := a.newAdminFlagSet("dexcli admin step-down")
	isConfirmed := flags.Bool("yes", false, "confirm that the active server process stops serving")
	expectedNodeID := &nodeIDFlag{}
	flags.Var(expectedNodeID, "expected-node-id",
		"step down only if the active process serves this node `ID`; set it with --expected-node-epoch")
	expectedNodeEpoch := &nodeEpochFlag{}
	flags.Var(expectedNodeEpoch, "expected-node-epoch",
		"step down only if the active process holds this node `epoch`; without both expectations, "+
			"the command describes the active process first and expects its node ID and epoch")
	addCommonFlags(flags, &options)
	if isHelp, err := a.parseAdminFlags(flags, args, adminStepDownUsage, options); isHelp || err != nil {
		return err
	}
	if expectedNodeID.isSet != expectedNodeEpoch.isSet {
		return newUsageError("admin step-down",
			errors.New("set --expected-node-id and --expected-node-epoch together, or neither"))
	}
	if !*isConfirmed {
		return newConfirmationError("admin step-down")
	}
	return withServerConnection(ctx, options, adminServiceName, func(callCtx context.Context, connection *grpc.ClientConn) error {
		client := dexpb.NewAdminServiceClient(connection)
		request := &dexpb.StepDownActiveNodeRequest{
			ExpectedNodeId:    expectedNodeID.optionalValue(),
			ExpectedNodeEpoch: expectedNodeEpoch.optionalValue(),
		}
		if !expectedNodeID.isSet {
			description, err := client.DescribeActiveNode(callCtx, &dexpb.DescribeActiveNodeRequest{})
			if err != nil {
				return newNotAttemptedStepDownError(err)
			}
			nodeID, nodeEpoch := description.GetNodeId(), description.GetNodeEpoch()
			request.ExpectedNodeId, request.ExpectedNodeEpoch = &nodeID, &nodeEpoch
		}
		response, err := client.StepDownActiveNode(callCtx, request)
		if err != nil {
			return newAdminStepDownError(err, request)
		}
		return writeOutput(a.stdout, options.output, stepDownOutput(request, response))
	})
}

// newAdminFlagSet reports flag errors on stderr but prints help, with every flag, on stdout.
func (a *App) newAdminFlagSet(name string) *flag.FlagSet {
	flags := newFlagSet(name, a.stderr)
	flags.Usage = func() {}
	return flags
}

// parseAdminFlags reports true after printing help, or a usage error for invalid flags or arguments.
func (a *App) parseAdminFlags(flags *flag.FlagSet, args []string, usage string, options options) (bool, error) {
	operation := strings.TrimPrefix(flags.Name(), "dexcli ")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(a.stdout, usage)
			fmt.Fprintln(a.stdout)
			fmt.Fprintln(a.stdout, "Flags:")
			flags.SetOutput(a.stdout)
			flags.PrintDefaults()
			return true, nil
		}
		return false, newUsageError(operation, err)
	}
	if flags.NArg() != 0 {
		return false, newUsageError(operation, fmt.Errorf("unexpected arguments: %v", flags.Args()))
	}
	if err := options.validate(); err != nil {
		return false, newUsageError(operation, err)
	}
	return false, nil
}

// newNotAttemptedStepDownError reports a failed describe, after which no step down request was sent.
func newNotAttemptedStepDownError(cause error) error {
	return &Error{
		operation: "admin step-down",
		kind:      "operation",
		cause:     fmt.Errorf("describe the active server process before stepping it down: %w", cause),
		details: map[string]any{
			"stepDownOutcome": notAttemptedStepDownOutcome,
			"hint":            notAttemptedStepDownHint,
		},
	}
}

// newAdminStepDownError marks client-side deadline, cancellation, and transport failures as an unknown outcome.
func newAdminStepDownError(cause error, request *dexpb.StepDownActiveNodeRequest) error {
	commandError := &Error{operation: "admin step-down", kind: "operation", cause: cause}
	switch status.Code(cause) {
	case codes.DeadlineExceeded, codes.Canceled, codes.Unavailable:
		nodeID, nodeEpoch := request.GetExpectedNodeId(), request.GetExpectedNodeEpoch()
		commandError.details = map[string]any{
			"stepDownOutcome":   unknownStepDownOutcome,
			"expectedNodeId":    nodeID,
			"expectedNodeEpoch": strconv.FormatUint(nodeEpoch, 10),
			"hint": fmt.Sprintf("The step down may have happened. Retry only with "+
				"--expected-node-id %q --expected-node-epoch %d, the values this call used, never with "+
				"values read after this failure. If dexcli admin describe now shows another node epoch "+
				"or holder process ID, the step down already happened, so do not retry; such a retry "+
				"would fail with FailedPrecondition and change nothing.", nodeID, nodeEpoch),
		}
	}
	return commandError
}

// stepDownOutput renders uint64 node epochs as decimal strings, as protojson does.
func stepDownOutput(request *dexpb.StepDownActiveNodeRequest, response *dexpb.StepDownActiveNodeResponse) map[string]any {
	output := map[string]any{
		"expectedNodeId":            request.GetExpectedNodeId(),
		"expectedNodeEpoch":         strconv.FormatUint(request.GetExpectedNodeEpoch(), 10),
		"nodeId":                    response.GetNodeId(),
		"releasedNodeEpoch":         strconv.FormatUint(response.GetReleasedNodeEpoch(), 10),
		"leaseReacquisitionHoldOff": response.GetLeaseReacquisitionHoldOff().AsDuration().String(),
	}
	switch leaseHolder := response.GetLeaseHolderAfterStepDown().(type) {
	case *dexpb.StepDownActiveNodeResponse_TakenOverByAnotherProcess:
		output["leaseHolderAfterStepDown"] = "takenOverByAnotherProcess"
		addActiveNodeLeaseHolder(output, leaseHolder.TakenOverByAnotherProcess)
	case *dexpb.StepDownActiveNodeResponse_TakenBackByThisProcess:
		output["leaseHolderAfterStepDown"] = "takenBackByThisProcess"
		addActiveNodeLeaseHolder(output, leaseHolder.TakenBackByThisProcess)
	case *dexpb.StepDownActiveNodeResponse_LeaseHolderUnknown:
		output["leaseHolderAfterStepDown"] = unknownStepDownOutcome
		output["leaseHolderUnknownDetail"] = leaseHolder.LeaseHolderUnknown.GetDetail()
	default:
		output["leaseHolderAfterStepDown"] = unknownStepDownOutcome
		output["leaseHolderUnknownDetail"] = "the server reported no lease holder"
	}
	return output
}

func addActiveNodeLeaseHolder(output map[string]any, holder *dexpb.NodeLeaseHolder) {
	output["activeNodeEpoch"] = strconv.FormatUint(holder.GetNodeEpoch(), 10)
	output["activeHolderProcessId"] = holder.GetHolderProcessId()
}

// nodeIDFlag is an optional, non-empty node ID flag.
type nodeIDFlag struct {
	value string
	isSet bool
}

func (f *nodeIDFlag) String() string {
	// flag.PrintDefaults calls String on a zero value to detect defaults.
	if f == nil {
		return ""
	}
	return f.value
}

func (f *nodeIDFlag) Set(text string) error {
	if text == "" {
		return errors.New("node ID must not be empty")
	}
	f.value, f.isSet = text, true
	return nil
}

func (f *nodeIDFlag) optionalValue() *string {
	if !f.isSet {
		return nil
	}
	value := f.value
	return &value
}

// nodeEpochFlag is an optional uint64 flag, so an unset epoch differs from 0.
type nodeEpochFlag struct {
	value uint64
	isSet bool
}

func (f *nodeEpochFlag) String() string {
	// flag.PrintDefaults calls String on a zero value to detect defaults.
	if f == nil || !f.isSet {
		return ""
	}
	return strconv.FormatUint(f.value, 10)
}

func (f *nodeEpochFlag) Set(text string) error {
	value, err := strconv.ParseUint(text, 10, 64)
	if err != nil {
		return fmt.Errorf("node epoch must be an unsigned 64-bit integer: %w", err)
	}
	f.value, f.isSet = value, true
	return nil
}

func (f *nodeEpochFlag) optionalValue() *uint64 {
	if !f.isSet {
		return nil
	}
	value := f.value
	return &value
}
