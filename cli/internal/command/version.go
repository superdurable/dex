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

	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
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
		clientMinimum, clientMaximum := dex.SupportedServerProtocolVersionRange()
		return writeOutput(a.stdout, options.output, map[string]any{
			"clientVersion": a.artifactVersion,
			"serverVersion": serverInfo.GetServerVersion(),
			"serverEngine":  serverEngineName(serverInfo.GetServerEngine()),
			"clientProtocol": map[string]uint32{
				"minimum": clientMinimum,
				"maximum": clientMaximum,
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

// negotiateServerProtocol mirrors the Go SDK's negotiation, because every other dexcli command connects through it.
// It returns the highest protocol version in both ranges, or an error whose text names the side to upgrade.
func negotiateServerProtocol(serverInfo *dexpb.ServerInfo) (uint32, error) {
	if serverInfo == nil {
		return 0, fmt.Errorf("Server returned an empty response")
	}
	clientMinimum, clientMaximum := dex.SupportedServerProtocolVersionRange()
	serverMinimum := serverInfo.GetMinimumSupportedProtocolVersion()
	serverCurrent := serverInfo.GetCurrentProtocolVersion()
	if serverMinimum == 0 || serverCurrent == 0 || serverMinimum > serverCurrent {
		return 0, fmt.Errorf("Server protocol interval is invalid")
	}
	if serverCurrent < clientMinimum {
		return 0, fmt.Errorf(
			"protocol intervals do not overlap: this dexcli requires Server protocol version %d or later, "+
				"but the Server supports %d-%d; upgrade the Server or use an older dexcli",
			clientMinimum,
			serverMinimum,
			serverCurrent,
		)
	}
	if serverMinimum > clientMaximum {
		return 0, fmt.Errorf(
			"protocol intervals do not overlap: the Server requires protocol version %d or later, "+
				"but this dexcli supports %d-%d; upgrade dexcli",
			serverMinimum,
			clientMinimum,
			clientMaximum,
		)
	}
	return min(serverCurrent, clientMaximum), nil
}

// serverEngineName returns the engine name that Dex Web's GET /api/server-info also reports.
func serverEngineName(engine dexpb.ServerEngine) string {
	switch engine {
	case dexpb.ServerEngine_SERVER_ENGINE_ZD:
		return "zd"
	case dexpb.ServerEngine_SERVER_ENGINE_TEMPORAL:
		return "temporal"
	case dexpb.ServerEngine_SERVER_ENGINE_CADENCE:
		return "cadence"
	default:
		return "unspecified"
	}
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
	clientMinimum, clientMaximum := dex.SupportedServerProtocolVersionRange()
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
				"minimum": clientMinimum,
				"maximum": clientMaximum,
			},
			"serverProtocol": map[string]any{
				"minimum": serverMinimum,
				"maximum": serverMaximum,
			},
		},
	}
}
