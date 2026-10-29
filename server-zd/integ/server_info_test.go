// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package integ

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestServerInfoTemporal(t *testing.T) {
	testServerInfo(t)
}

func testServerInfo(t *testing.T) {
	runtime := startDexService(t, serverZDTestConfig{})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	serverInfo, err := runtime.FlowClient.GetServerInfo(ctx, &emptypb.Empty{})
	require.NoError(t, err)

	// The launched binary reports its own build version.
	require.NotEmpty(t, serverInfo.GetServerVersion())

	require.Equal(t, uint32(1), serverInfo.GetMinimumSupportedProtocolVersion())
	require.Equal(t, uint32(3), serverInfo.GetCurrentProtocolVersion())

	require.Equal(t, dexpb.ServerEngine_SERVER_ENGINE_ZD, serverInfo.GetServerEngine())

}
