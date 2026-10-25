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
)

func TestDescribeFlow(t *testing.T) {
	ctx := integrationContext(t)

	runningFlowID := newFlowID(t, "describe-running")
	runningRunID, err := integClient.StartFlow(ctx, noStepFlow{}, runningFlowID, nil, dex.StartFlowOptions{})
	require.NoError(t, err)
	runningInfo, err := integClient.DescribeFlow(ctx, runningFlowID)
	require.NoError(t, err)
	require.Equal(t, runningFlowID, runningInfo.FlowID)
	require.Equal(t, runningRunID, runningInfo.RunID)
	require.Equal(t, "noStepFlow", runningInfo.FlowType)
	require.Equal(t, dex.FlowRunning, runningInfo.Status)
	require.False(t, runningInfo.StartedAt.IsZero())
	require.True(t, runningInfo.ClosedAt.IsZero())
	require.NoError(t, integClient.StopFlow(ctx, runningFlowID, dex.StopOptions{}))
	waitForFlow(t, runningFlowID, false)

	completedFlowID := newFlowID(t, "describe-completed")
	completedRunID, err := integClient.StartFlow(ctx, basicFlow{}, completedFlowID, 1, dex.StartFlowOptions{})
	require.NoError(t, err)
	require.Equal(t, dex.FlowCompleted, waitForFlow(t, completedFlowID, false).Status)
	completedInfo, err := integClient.DescribeFlow(ctx, completedFlowID)
	require.NoError(t, err)
	require.Equal(t, completedFlowID, completedInfo.FlowID)
	require.Equal(t, completedRunID, completedInfo.RunID)
	require.Equal(t, "basicFlow", completedInfo.FlowType)
	require.Equal(t, dex.FlowCompleted, completedInfo.Status)
	require.False(t, completedInfo.StartedAt.IsZero())
	require.False(t, completedInfo.ClosedAt.IsZero())

	failedFlowID := newFlowID(t, "describe-failed")
	failedRunID, err := integClient.StartFlow(ctx, forceFailFlow{}, failedFlowID, struct{}{}, dex.StartFlowOptions{})
	require.NoError(t, err)
	require.Equal(t, dex.FlowFailed, waitForFlow(t, failedFlowID, false).Status)
	failedInfo, err := integClient.DescribeFlow(ctx, failedFlowID)
	require.NoError(t, err)
	require.Equal(t, failedFlowID, failedInfo.FlowID)
	require.Equal(t, failedRunID, failedInfo.RunID)
	require.Equal(t, "forceFailFlow", failedInfo.FlowType)
	require.Equal(t, dex.FlowFailed, failedInfo.Status)
	require.False(t, failedInfo.StartedAt.IsZero())
	require.False(t, failedInfo.ClosedAt.IsZero())

	_, err = integClient.DescribeFlow(ctx, newFlowID(t, "describe-missing"))
	var missing *dex.FlowNotFoundError
	require.ErrorAs(t, err, &missing)
}
