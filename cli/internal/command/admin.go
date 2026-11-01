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

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
)

const adminStepDownUsage = "Usage: dexcli admin step-down --yes [global flags]"

func (a *App) executeAdmin(ctx context.Context, args []string, options options) error {
	if len(args) == 0 {
		return newUsageError("admin", errors.New("expected subcommand step-down"))
	}
	switch args[0] {
	case "step-down":
		return a.executeAdminStepDown(ctx, args[1:], options)
	case "help", "--help", "-h":
		fmt.Fprintln(a.stdout, adminStepDownUsage)
		return nil
	default:
		return newUsageError("admin", fmt.Errorf("unknown subcommand %q", args[0]))
	}
}

// executeAdminStepDown makes the active server process behind --server give up its node lease.
func (a *App) executeAdminStepDown(ctx context.Context, args []string, options options) error {
	flags := newFlagSet("dexcli admin step-down", a.stderr)
	isConfirmed := flags.Bool("yes", false, "confirm that the active server process stops serving")
	addCommonFlags(flags, &options)
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(a.stdout, adminStepDownUsage)
			return nil
		}
		return newUsageError("admin step-down", err)
	}
	if flags.NArg() != 0 {
		return newUsageError("admin step-down", fmt.Errorf("unexpected arguments: %v", flags.Args()))
	}
	if err := options.validate(); err != nil {
		return newUsageError("admin step-down", err)
	}
	if !*isConfirmed {
		return newConfirmationError("admin step-down")
	}
	return withFlowService(ctx, options, func(callCtx context.Context, connection *flowService) error {
		response, err := dexpb.NewAdminServiceClient(connection.connection).
			StepDownActiveNode(callCtx, &dexpb.StepDownActiveNodeRequest{})
		if err != nil {
			return newOperationError("admin step-down", err)
		}
		return writeOutput(a.stdout, options.output, stepDownOutput(response))
	})
}

func stepDownOutput(response *dexpb.StepDownActiveNodeResponse) map[string]any {
	output := map[string]any{
		"nodeId":                    response.GetNodeId(),
		"releasedNodeEpoch":         response.GetReleasedNodeEpoch(),
		"isTakeoverObserved":        response.GetObservedTakeover() != nil,
		"leaseReacquisitionHoldOff": response.GetLeaseReacquisitionHoldOff().AsDuration().String(),
	}
	if takeover := response.GetObservedTakeover(); takeover != nil {
		output["observedTakeover"] = map[string]any{
			"nodeEpoch":       takeover.GetNodeEpoch(),
			"holderProcessId": takeover.GetHolderProcessId(),
		}
	}
	return output
}
