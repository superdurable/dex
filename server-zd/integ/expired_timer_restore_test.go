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

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/server-zd/integ/workflow/expiredtimer"
)

// The tests require a restored or copied expired timer to fire within this long.
const expiredTimerPromptFireLimit = 10 * time.Second

func TestResetToAWaitWhoseTimerExpiredFiresPromptly(t *testing.T) {
	workerHandler := expiredtimer.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, serverZDTestConfig{})
	flowClient := runtime.FlowClient
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	flowID := expiredtimer.WorkflowType + "-reset-" + uuid.NewString()
	waitEventID := runExpiredTimerFlowToCompletion(t, ctx, flowClient, flowID, workerTarget)
	require.Equal(t, int64(1), workerHandler.ExecuteCount())

	_, err := flowClient.ResetFlow(ctx, &dexpb.ResetFlowRequest{
		FlowId:    flowID,
		ResetType: dexpb.FlowResetType_FLOW_RESET_TYPE_EVENT_ID,
		EventId:   waitEventID,
		Reason:    "restore a wait whose timer expired",
	})
	require.NoError(t, err)
	resetAt := time.Now()

	requireFlowCompletedPromptly(t, ctx, flowClient, flowID, resetAt)
	require.Equal(t, int64(2), workerHandler.ExecuteCount())
}

func TestCloneForkOfAWaitWhoseTimerExpiredFiresPromptly(t *testing.T) {
	workerHandler := expiredtimer.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, serverZDTestConfig{})
	flowClient := runtime.FlowClient
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	sourceFlowID := expiredtimer.WorkflowType + "-source-" + uuid.NewString()
	waitEventID := runExpiredTimerFlowToCompletion(t, ctx, flowClient, sourceFlowID, workerTarget)
	require.Equal(t, int64(1), workerHandler.ExecuteCount())

	forkFlowID := expiredtimer.WorkflowType + "-fork-" + uuid.NewString()
	_, err := flowClient.CloneForkFlow(ctx, &dexpb.CloneForkFlowRequest{
		SourceFlowId: sourceFlowID,
		FlowId:       forkFlowID,
		RequestId:    newRequestID(),
		PointType:    dexpb.FlowCloneForkPointType_FLOW_CLONE_FORK_POINT_TYPE_EVENT_ID,
		EventId:      waitEventID,
		Reason:       "copy a wait whose timer expired",
	})
	require.NoError(t, err)
	forkedAt := time.Now()

	requireFlowCompletedPromptly(t, ctx, flowClient, forkFlowID, forkedAt)
	require.Equal(t, int64(2), workerHandler.ExecuteCount())
}

// runExpiredTimerFlowToCompletion runs the Flow to completion and returns its WaitFor completion
// event ID.
func runExpiredTimerFlowToCompletion(
	t *testing.T,
	ctx context.Context,
	flowClient dexpb.FlowServiceClient,
	flowID string,
	workerTarget *dexpb.WorkerTarget,
) int64 {
	t.Helper()
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:        newRequestID(),
		FlowId:           flowID,
		FlowType:         expiredtimer.WorkflowType,
		StartStepType:    expiredtimer.WaitStep,
		FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{}, workerTarget),
	})
	require.NoError(t, err)
	response, err := flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId:          flowID,
		WaitTimeSeconds: 30,
	})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, response.GetFlowStatus())

	history, err := flowClient.GetHistoryEvents(ctx, &dexpb.GetHistoryEventsRequest{FlowId: flowID})
	require.NoError(t, err)
	for _, event := range history.GetEvents() {
		if event.GetStepWaitForCompleted().GetContext().GetStepExecutionId() ==
			expiredtimer.WaitStepExecutionID {
			return event.GetEventId()
		}
	}
	require.FailNow(t, "the WaitFor completion event is missing", "%v", history)
	return 0
}

// requireFlowCompletedPromptly requires the Flow to complete within expiredTimerPromptFireLimit.
func requireFlowCompletedPromptly(
	t *testing.T,
	ctx context.Context,
	flowClient dexpb.FlowServiceClient,
	flowID string,
	restoredAt time.Time,
) {
	t.Helper()
	response, err := flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId:          flowID,
		WaitTimeSeconds: int32(expiredTimerPromptFireLimit / time.Second),
	})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, response.GetFlowStatus())
	require.Less(t, time.Since(restoredAt), expiredTimerPromptFireLimit)
}
