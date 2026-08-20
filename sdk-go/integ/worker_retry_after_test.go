// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package integ

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex"
)

func TestWaitForRetryAfterStackTraceAndDelay(t *testing.T) {
	ctx := integrationContext(t)
	flowID := newFlowID(t, "wait-retry-after")
	flowService := newFlowServiceClient(t)

	startedAt := time.Now()
	runID, err := integClient.StartFlow(
		ctx,
		workerRetryAfterWaitForFlow{},
		flowID,
		struct{}{},
		dex.StartFlowOptions{},
	)
	require.NoError(t, err)
	require.NotEmpty(t, runID)

	failure := awaitLiveWorkerFailure(t, flowService, flowID, runID)
	assertWorkerFailureStackTrace(t, failure, waitForRetryAfterDetail)

	result := waitForFlow(t, flowID, false)
	require.Equal(t, dex.FlowCompleted, result.Status)
	elapsed := time.Since(startedAt)
	require.GreaterOrEqual(t, elapsed, retryAfterDelay)
	require.Less(t, elapsed, retryAfterPolicyInterval)
}

func TestExecuteRetryAfterStackTraceAndDelay(t *testing.T) {
	ctx := integrationContext(t)
	flowID := newFlowID(t, "execute-retry-after")
	flowService := newFlowServiceClient(t)

	startedAt := time.Now()
	runID, err := integClient.StartFlow(
		ctx,
		workerRetryAfterExecuteFlow{},
		flowID,
		struct{}{},
		dex.StartFlowOptions{},
	)
	require.NoError(t, err)
	require.NotEmpty(t, runID)

	failure := awaitLiveWorkerFailure(t, flowService, flowID, runID)
	assertWorkerFailureStackTrace(t, failure, executeRetryAfterDetail)

	result := waitForFlow(t, flowID, false)
	require.Equal(t, dex.FlowCompleted, result.Status)
	elapsed := time.Since(startedAt)
	require.GreaterOrEqual(t, elapsed, retryAfterDelay)
	require.Less(t, elapsed, retryAfterPolicyInterval)
}
