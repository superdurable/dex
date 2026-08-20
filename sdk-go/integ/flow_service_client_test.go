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
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func newFlowServiceClient(t *testing.T) dexpb.FlowServiceClient {
	t.Helper()
	connection, err := grpc.NewClient(
		flowServiceAddress(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = connection.Close() })
	return dexpb.NewFlowServiceClient(connection)
}

func awaitLiveWorkerFailure(
	t *testing.T,
	client dexpb.FlowServiceClient,
	flowID string,
	runID string,
) *dexpb.StepMethodFailure {
	t.Helper()
	ctx := integrationContext(t)
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.GetFlowState(ctx, &dexpb.GetFlowStateRequest{
			FlowId: flowID,
			RunId:  runID,
		})
		require.NoError(t, err)
		for _, step := range response.GetActiveStepExecutions() {
			if failure := step.GetLastFailureInfo(); failure != nil {
				if failure.GetDetails() != nil {
					return failure
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("active Step did not expose retry failure")
	return nil
}

func assertWorkerFailureStackTrace(
	t *testing.T,
	failure *dexpb.StepMethodFailure,
	expectedDetail string,
) {
	t.Helper()
	require.NotNil(t, failure)
	require.Equal(t, int32(1), failure.GetAttempt())
	details := failure.GetDetails()
	require.NotNil(t, details)
	require.Equal(t, expectedDetail, details.GetOriginalWorkerErrorDetail())
	stackTrace := details.GetOriginalWorkerErrorStackTrace()
	require.NotEmpty(t, stackTrace)
	require.Contains(t, stackTrace, expectedDetail)
}
