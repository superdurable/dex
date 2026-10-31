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

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
)

func (a *App) executeSearchProjection(ctx context.Context, args []string, options options) error {
	if len(args) == 0 {
		return newUsageError("search-projection", errors.New("choose status or rebuild"))
	}
	operation := args[0]
	if operation != "status" && operation != "rebuild" {
		return newUsageError("search-projection", fmt.Errorf("unknown operation %q", operation))
	}
	flags := newFlagSet("dexcli search-projection "+operation, a.stderr)
	addCommonFlags(flags, &options)
	var partitionIDs string
	var shouldRebuildMarked, shouldRebuildAll bool
	if operation == "rebuild" {
		flags.StringVar(&partitionIDs, "partitions", "", "comma-separated partition IDs")
		flags.BoolVar(&shouldRebuildMarked, "marked", false, "rebuild every partition marked as needing rebuilding")
		flags.BoolVar(&shouldRebuildAll, "all", false, "rebuild every configured partition")
	}
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(a.stdout, "Usage: dexcli search-projection status [global flags]")
			fmt.Fprintln(a.stdout, "       dexcli search-projection rebuild (--partitions 1,2 | --marked | --all) [global flags]")
			return nil
		}
		return newUsageError("search-projection", err)
	}
	if flags.NArg() != 0 {
		return newUsageError("search-projection", fmt.Errorf("unexpected arguments: %v", flags.Args()))
	}
	if err := options.validate(); err != nil {
		return newUsageError("search-projection", err)
	}
	request := &dexpb.RebuildSearchProjectionRequest{AllMarkedPartitions: shouldRebuildMarked, AllPartitions: shouldRebuildAll}
	if operation == "rebuild" {
		selectionCount := 0
		if partitionIDs != "" {
			selectionCount++
			for _, partitionText := range strings.Split(partitionIDs, ",") {
				partitionNumber, err := strconv.ParseUint(strings.TrimSpace(partitionText), 10, 32)
				if err != nil {
					return newUsageError("search-projection", fmt.Errorf("invalid partition %q: %w", partitionText, err))
				}
				request.PartitionIds = append(request.PartitionIds, uint32(partitionNumber))
			}
		}
		if shouldRebuildMarked {
			selectionCount++
		}
		if shouldRebuildAll {
			selectionCount++
		}
		if selectionCount != 1 {
			return newUsageError("search-projection", errors.New("select exactly one of --partitions, --marked, or --all"))
		}
	}
	return withFlowService(ctx, options, func(callCtx context.Context, connection *flowService) error {
		client := dexpb.NewAdminServiceClient(connection.connection)
		if operation == "status" {
			response, err := client.GetSearchProjectionStatus(callCtx, emptyRequest())
			if err != nil {
				return newOperationError("search-projection status", err)
			}
			return writeOutput(a.stdout, options.output, response)
		}
		response, err := client.RebuildSearchProjection(callCtx, request)
		if err != nil {
			return newOperationError("search-projection rebuild", err)
		}
		return writeOutput(a.stdout, options.output, response)
	})
}
