// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package integ

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/integ/workflow/signal"
	"github.com/superdurable/dex/service"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestPublishToChannelNoFlowId(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	runtime := startDexService(t, DexServiceTestConfig{
		BackendType: service.BackendTypeTemporal,
	})
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
