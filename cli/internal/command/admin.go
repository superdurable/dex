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
	adminUsage             = "Usage: dexcli admin <describe|step-down> [flags]"
	adminDescribeUsage     = "Usage: dexcli admin describe [global flags]"
	adminStepDownUsage     = "Usage: dexcli admin step-down --yes [--expected-node-epoch epoch] [global flags]"
	adminServiceName       = "AdminService"
	unknownStepDownOutcome = "unknown"
	// adminStepDownDefaultTimeout exceeds the commit drain bound, hold-off, and lease attempt with a wide margin.
	adminStepDownDefaultTimeout = 2 * time.Minute
	unknownStepDownOutcomeHint  = "The step down may have happened. Run dexcli admin describe, " +
		"then retry only with --expected-node-epoch set to the node epoch it reports."
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
func (a *App) executeAdminStepDown(ctx context.Context, args []string, options options) error {
	if !options.isTimeoutSet {
		options.timeout = adminStepDownDefaultTimeout
	}
	flags := a.newAdminFlagSet("dexcli admin step-down")
	isConfirmed := flags.Bool("yes", false, "confirm that the active server process stops serving")
	expectedNodeEpoch := &nodeEpochFlag{}
	flags.Var(expectedNodeEpoch, "expected-node-epoch",
		"step down only if the active process holds this `epoch`, as dexcli admin describe prints it")
	addCommonFlags(flags, &options)
	if isHelp, err := a.parseAdminFlags(flags, args, adminStepDownUsage, options); isHelp || err != nil {
		return err
	}
	if !*isConfirmed {
		return newConfirmationError("admin step-down")
	}
	return withServerConnection(ctx, options, adminServiceName, func(callCtx context.Context, connection *grpc.ClientConn) error {
		response, err := dexpb.NewAdminServiceClient(connection).StepDownActiveNode(
			callCtx,
			&dexpb.StepDownActiveNodeRequest{ExpectedNodeEpoch: expectedNodeEpoch.optionalValue()},
		)
		if err != nil {
			return newAdminStepDownError(err)
		}
		return writeOutput(a.stdout, options.output, stepDownOutput(response))
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

// newAdminStepDownError marks client-side deadline, cancellation, and transport failures as an unknown outcome.
func newAdminStepDownError(cause error) error {
	commandError := &Error{operation: "admin step-down", kind: "operation", cause: cause}
	switch status.Code(cause) {
	case codes.DeadlineExceeded, codes.Canceled, codes.Unavailable:
		commandError.details = map[string]any{
			"stepDownOutcome": unknownStepDownOutcome,
			"hint":            unknownStepDownOutcomeHint,
		}
	}
	return commandError
}

// stepDownOutput renders uint64 node epochs as decimal strings, as protojson does.
func stepDownOutput(response *dexpb.StepDownActiveNodeResponse) map[string]any {
	output := map[string]any{
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
