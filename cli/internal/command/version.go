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

	"github.com/superdurable/dex/gen/dexpb"
)

const (
	minimumSupportedServerProtocolVersion uint32 = 1
	maximumSupportedServerProtocolVersion uint32 = 1
)

func (a *App) executeVersion(ctx context.Context, args []string, options options) error {
	if len(args) == 0 || args[0] != "check" {
		return newUsageError("version", fmt.Errorf("expected subcommand check"))
	}
	flags := newFlagSet("dexcli version check", a.stderr)
	addCommonFlags(flags, &options)
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(a.stdout, "Usage: dexcli version check [global flags]")
			return nil
		}
		return newUsageError("version check", err)
	}
	if flags.NArg() != 0 {
		return newUsageError("version check", fmt.Errorf("unexpected arguments: %v", flags.Args()))
	}
	if err := options.validate(); err != nil {
		return newUsageError("version check", err)
	}
	return withFlowService(ctx, options, func(callCtx context.Context, client *flowService) error {
		serverInfo, err := client.service.GetServerInfo(callCtx, emptyRequest())
		if err != nil {
			return newCompatibilityError(a.artifactVersion, nil, "GetServerInfo failed", err)
		}
		negotiatedProtocol, err := negotiateServerProtocol(serverInfo)
		if err != nil {
			return newCompatibilityError(a.artifactVersion, serverInfo, err.Error(), err)
		}
		return writeOutput(a.stdout, options.output, map[string]any{
			"clientVersion": a.artifactVersion,
			"serverVersion": serverInfo.GetServerVersion(),
			"clientProtocol": map[string]uint32{
				"minimum": minimumSupportedServerProtocolVersion,
				"maximum": maximumSupportedServerProtocolVersion,
			},
			"serverProtocol": map[string]uint32{
				"minimum": serverInfo.GetMinimumSupportedProtocolVersion(),
				"maximum": serverInfo.GetCurrentProtocolVersion(),
			},
			"negotiatedProtocol": negotiatedProtocol,
			"compatible":         true,
		})
	})
}

func negotiateServerProtocol(serverInfo *dexpb.ServerInfo) (uint32, error) {
	if serverInfo == nil {
		return 0, fmt.Errorf("Server returned an empty response")
	}
	serverMinimum := serverInfo.GetMinimumSupportedProtocolVersion()
	serverCurrent := serverInfo.GetCurrentProtocolVersion()
	if minimumSupportedServerProtocolVersion == 0 ||
		maximumSupportedServerProtocolVersion == 0 ||
		minimumSupportedServerProtocolVersion > maximumSupportedServerProtocolVersion {
		return 0, fmt.Errorf("dexcli protocol interval is invalid")
	}
	if serverMinimum == 0 || serverCurrent == 0 || serverMinimum > serverCurrent {
		return 0, fmt.Errorf("Server protocol interval is invalid")
	}
	negotiatedProtocol := min(serverCurrent, maximumSupportedServerProtocolVersion)
	if negotiatedProtocol < serverMinimum ||
		negotiatedProtocol < minimumSupportedServerProtocolVersion {
		return 0, fmt.Errorf("protocol intervals do not overlap")
	}
	return negotiatedProtocol, nil
}

func newCompatibilityError(
	clientVersion string,
	serverInfo *dexpb.ServerInfo,
	reason string,
	cause error,
) *Error {
	serverVersion := "unknown"
	var serverMinimum any = "unknown"
	var serverMaximum any = "unknown"
	if serverInfo != nil {
		serverVersion = serverInfo.GetServerVersion()
		if serverVersion == "" {
			serverVersion = "unknown"
		}
		serverMinimum = serverInfo.GetMinimumSupportedProtocolVersion()
		serverMaximum = serverInfo.GetCurrentProtocolVersion()
	}
	return &Error{
		operation: "version check",
		kind:      "compatibility",
		cause:     cause,
		details: map[string]any{
			"reason":        reason,
			"compatible":    false,
			"clientVersion": clientVersion,
			"serverVersion": serverVersion,
			"clientProtocol": map[string]uint32{
				"minimum": minimumSupportedServerProtocolVersion,
				"maximum": maximumSupportedServerProtocolVersion,
			},
			"serverProtocol": map[string]any{
				"minimum": serverMinimum,
				"maximum": serverMaximum,
			},
		},
	}
}
