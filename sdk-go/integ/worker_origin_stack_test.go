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

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex"
)

func TestWaitForOriginStackTrace(t *testing.T) {
	ctx := integrationContext(t)
	flowID := newFlowID(t, "wait-origin-stack")
	flowService := newFlowServiceClient(t)

	runID, err := integClient.StartFlow(
		ctx,
		workerOriginStackWaitForFlow{},
		flowID,
		struct{}{},
		dex.StartFlowOptions{},
	)
	require.NoError(t, err)
	require.NotEmpty(t, runID)

	failure := awaitLiveWorkerFailure(t, flowService, flowID, runID)
	assertWorkerFailureStackTrace(t, failure, originStackDetail)
	require.Contains(
		t,
		failure.GetDetails().GetOriginalWorkerErrorStackTrace(),
		"originStackFailure",
	)
	require.NotContains(
		t,
		failure.GetDetails().GetOriginalWorkerErrorStackTrace(),
		"finishWorkerCall",
	)

	result := waitForFlow(t, flowID, false)
	require.Equal(t, dex.FlowCompleted, result.Status)
}
