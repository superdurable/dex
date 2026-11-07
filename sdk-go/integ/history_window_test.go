// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package integ

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex"
	"github.com/superdurable/dex/sdk-go/dex/ptr"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc/codes"
)

func TestHistoryWindowEventsStartAndUpdate(t *testing.T) {
	ctx := integrationContext(t)
	service := newFlowServiceClient(t)

	flowID := newFlowID(t, "history-window")
	runID, err := integClient.StartFlow(ctx, noStepFlow{}, flowID, nil, dex.StartFlowOptions{
		ConfigOverride: &dex.FlowConfig{HistoryWindowEvents: ptr.Any(int32(500))},
	})
	require.NoError(t, err)
	require.Equal(t, int32(500), readHistoryWindowEvents(t, service, flowID, runID))

	require.NoError(t, integClient.UpdateFlowConfig(ctx, flowID, dex.FlowConfig{
		HistoryWindowEvents: ptr.Any(int32(2000)),
	}))
	require.Equal(t, int32(2000), readHistoryWindowEvents(t, service, flowID, runID))

	err = integClient.UpdateFlowConfig(ctx, flowID, dex.FlowConfig{
		HistoryWindowEvents: ptr.Any(int32(0)),
	})
	requireInvalidArgument(t, err)
	require.Equal(t, int32(2000), readHistoryWindowEvents(t, service, flowID, runID))

	require.NoError(t, integClient.StopFlow(ctx, flowID, dex.StopOptions{}))
	waitForFlow(t, flowID, false)
}

func TestHistoryWindowEventsRejectsOutOfRangeStart(t *testing.T) {
	ctx := integrationContext(t)
	for _, windowEvents := range []int32{0, -1, 100001} {
		flowID := newFlowID(t, "history-window-invalid")
		_, err := integClient.StartFlow(ctx, noStepFlow{}, flowID, nil, dex.StartFlowOptions{
			ConfigOverride: &dex.FlowConfig{HistoryWindowEvents: ptr.Any(windowEvents)},
		})
		requireInvalidArgument(t, err)
	}
}

func readHistoryWindowEvents(
	t *testing.T,
	service dexpb.FlowServiceClient,
	flowID string,
	runID string,
) int32 {
	t.Helper()
	response, err := service.GetFlowState(integrationContext(t), &dexpb.GetFlowStateRequest{
		FlowId: flowID,
		RunId:  runID,
	})
	require.NoError(t, err)
	config := response.GetFlowConfig()
	require.NotNil(t, config)
	require.NotNil(t, config.HistoryWindowEvents)
	return config.GetHistoryWindowEvents()
}

func requireInvalidArgument(t *testing.T, err error) {
	t.Helper()
	var serviceError *dex.ServiceError
	require.ErrorAs(t, err, &serviceError)
	require.Equal(t, codes.InvalidArgument, serviceError.Code)
}
