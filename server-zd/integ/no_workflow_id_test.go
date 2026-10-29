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
	"github.com/superdurable/dex/server-zd/integ/workflow/signal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestPublishToChannelNoFlowId(t *testing.T) {
	runtime := startDexService(t, serverZDTestConfig{})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := flowClient.PublishToChannel(ctx, &dexpb.PublishToChannelRequest{
		FlowId: "",
		Messages: []*dexpb.ChannelMessage{
			{
				ChannelName: signal.SignalName,
				Value:       stringValue("test"),
			},
		},
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Equal(
		t,
		"flow ID is required",
		grpcServiceErrorResponse(t, err).GetDetail(),
	)
}
