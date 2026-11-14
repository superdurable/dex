// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dex

import (
	"fmt"
	"runtime/debug"
	"strings"
	"sync/atomic"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	minimumSupportedServerProtocolVersion uint32 = 4
	maximumSupportedServerProtocolVersion uint32 = 5
	// hardDeleteAttributeProtocolVersion is the first Server protocol that keeps hard-delete Attributes out of history.
	hardDeleteAttributeProtocolVersion uint32 = 3
	// taskQueueProtocolVersion is the first Server protocol that routes Flows to named task queues.
	taskQueueProtocolVersion uint32 = 5
	goSDKModulePath                 = "github.com/superdurable/dex/sdk-go"
	// minimumSupportedServerZDRelease names the first server-zd releases with minimumSupportedServerProtocolVersion.
	minimumSupportedServerZDRelease = "server-zd newer than v0.1.16"
)

func negotiateServerProtocol(
	serverInfo *dexpb.ServerInfo,
	sdkVersion string,
) (uint32, error) {
	if serverInfo == nil {
		return 0, invalidServerInfoError(sdkVersion, "unknown", 0, 0, "Server returned an empty response")
	}
	serverMinimum := serverInfo.GetMinimumSupportedProtocolVersion()
	serverCurrent := serverInfo.GetCurrentProtocolVersion()
	if minimumSupportedServerProtocolVersion == 0 ||
		maximumSupportedServerProtocolVersion == 0 ||
		minimumSupportedServerProtocolVersion > maximumSupportedServerProtocolVersion {
		return 0, invalidServerInfoError(
			sdkVersion,
			serverInfo.GetServerVersion(),
			serverMinimum,
			serverCurrent,
			"Go SDK protocol interval is invalid",
		)
	}
	if serverMinimum == 0 || serverCurrent == 0 || serverMinimum > serverCurrent {
		return 0, invalidServerInfoError(
			sdkVersion,
			serverInfo.GetServerVersion(),
			serverMinimum,
			serverCurrent,
			"Server protocol interval is invalid",
		)
	}
	if serverCurrent < minimumSupportedServerProtocolVersion {
		return 0, serverTooOldProtocolError(sdkVersion, serverInfo.GetServerVersion(), serverMinimum, serverCurrent)
	}
	if serverMinimum > maximumSupportedServerProtocolVersion {
		return 0, sdkTooOldProtocolError(sdkVersion, serverInfo.GetServerVersion(), serverMinimum, serverCurrent)
	}
	return min(serverCurrent, maximumSupportedServerProtocolVersion), nil
}

// isDefinitiveServerProtocolMismatch reports whether a valid ServerInfo has no protocol in common with this SDK.
func isDefinitiveServerProtocolMismatch(serverInfo *dexpb.ServerInfo) bool {
	serverMinimum := serverInfo.GetMinimumSupportedProtocolVersion()
	serverCurrent := serverInfo.GetCurrentProtocolVersion()
	if serverMinimum == 0 || serverCurrent == 0 || serverMinimum > serverCurrent {
		return false
	}
	return serverCurrent < minimumSupportedServerProtocolVersion || serverMinimum > maximumSupportedServerProtocolVersion
}

// negotiatedServerProtocol holds the protocol version agreed with the Server; zero means not negotiated.
type negotiatedServerProtocol struct {
	version atomic.Uint32
}

// requireHardDeleteAttributeSupport fails closed because older Servers would store hard-delete values in history.
func (protocol *negotiatedServerProtocol) requireHardDeleteAttributeSupport(
	writes []*dexpb.AttributeWrite,
) error {
	if protocol.version.Load() >= hardDeleteAttributeProtocolVersion {
		return nil
	}
	for _, write := range writes {
		if write.GetIsHardDeleteOnNull() {
			return &HardDeleteAttributeUnsupportedError{
				AttributeName:                 write.GetKey(),
				NegotiatedServerProtocol:      protocol.version.Load(),
				RequiredServerProtocolVersion: hardDeleteAttributeProtocolVersion,
			}
		}
	}
	return nil
}

// requireTaskQueueSupport fails closed because older Servers would route a named task queue to the default one.
func (protocol *negotiatedServerProtocol) requireTaskQueueSupport(taskQueue string) error {
	if taskQueue == "" || protocol.version.Load() >= taskQueueProtocolVersion {
		return nil
	}
	return &TaskQueueUnsupportedError{
		TaskQueue:                     taskQueue,
		NegotiatedServerProtocol:      protocol.version.Load(),
		RequiredServerProtocolVersion: taskQueueProtocolVersion,
	}
}

func serverProtocolRequestError(sdkVersion string, err error) error {
	switch status.Code(err) {
	case codes.Unavailable:
		// Unavailable covers connection and TLS handshake failures.
		return fmt.Errorf(
			"dex: FlowService connection failed during GetServerInfo for Go SDK version %q: %w",
			sdkVersion,
			err,
		)
	case codes.Unimplemented:
		// Only Servers that predate protocol negotiation lack GetServerInfo.
		return fmt.Errorf(
			"dex: Go SDK version %q protocol [%d,%d] is incompatible with Server version %q protocol [unknown,unknown]: "+
				"GetServerInfo failed; upgrade the Server to protocol version %d or later (%s): %w",
			sdkVersion,
			minimumSupportedServerProtocolVersion,
			maximumSupportedServerProtocolVersion,
			"unknown",
			minimumSupportedServerProtocolVersion,
			minimumSupportedServerZDRelease,
			err,
		)
	default:
		return fmt.Errorf("dex: GetServerInfo failed for Go SDK version %q: %w", sdkVersion, err)
	}
}

func serverTooOldProtocolError(sdkVersion string, serverVersion string, serverMinimum uint32, serverCurrent uint32) error {
	return fmt.Errorf(
		"dex: this SDK requires a Dex Server with protocol version %d or later (%s). "+
			"The Server reports protocol versions %d-%d (server version %s). "+
			"Upgrade the Server before upgrading the SDK. Go SDK version %q protocol [%d,%d] is incompatible: protocol intervals do not overlap",
		minimumSupportedServerProtocolVersion,
		minimumSupportedServerZDRelease,
		serverMinimum,
		serverCurrent,
		displayServerVersion(serverVersion),
		sdkVersion,
		minimumSupportedServerProtocolVersion,
		maximumSupportedServerProtocolVersion,
	)
}

func sdkTooOldProtocolError(sdkVersion string, serverVersion string, serverMinimum uint32, serverCurrent uint32) error {
	return fmt.Errorf(
		"dex: this Go SDK version %q supports Server protocol versions %d-%d, but the Server requires protocol version %d or later. "+
			"The Server reports protocol versions %d-%d (server version %s). "+
			"Upgrade the Go SDK. The Go SDK is incompatible: protocol intervals do not overlap",
		sdkVersion,
		minimumSupportedServerProtocolVersion,
		maximumSupportedServerProtocolVersion,
		serverMinimum,
		serverMinimum,
		serverCurrent,
		displayServerVersion(serverVersion),
	)
}

func invalidServerInfoError(
	sdkVersion string,
	serverVersion string,
	serverMinimum uint32,
	serverCurrent uint32,
	reason string,
) error {
	return fmt.Errorf(
		"dex: Server version %s returned invalid ServerInfo with protocol versions %d-%d to Go SDK version %q protocol [%d,%d]: %s",
		displayServerVersion(serverVersion),
		serverMinimum,
		serverCurrent,
		sdkVersion,
		minimumSupportedServerProtocolVersion,
		maximumSupportedServerProtocolVersion,
		reason,
	)
}

func displayServerVersion(serverVersion string) string {
	if serverVersion == "" {
		return "unknown"
	}
	return serverVersion
}

func currentGoSDKVersion() string {
	buildInfo, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	if buildInfo.Main.Path == goSDKModulePath {
		return displayGoModuleVersion(buildInfo.Main.Version)
	}
	for _, dependency := range buildInfo.Deps {
		if dependency.Path != goSDKModulePath {
			continue
		}
		if dependency.Replace != nil {
			return displayGoModuleVersion(dependency.Replace.Version)
		}
		return displayGoModuleVersion(dependency.Version)
	}
	return "dev"
}

func displayGoModuleVersion(version string) string {
	if version == "" || version == "(devel)" {
		return "dev"
	}
	return strings.TrimPrefix(version, "v")
}
