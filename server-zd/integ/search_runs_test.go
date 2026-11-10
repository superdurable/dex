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
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/server-zd/integ/workflow/basic"
	"github.com/superdurable/dex/server-zd/integ/workflow/deadend"
)

func TestSearchRunsTemporal(t *testing.T) {
	verifySearchRunInclusion(t)
}

func verifySearchRunInclusion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cfg := serverZDTestConfig{}

	runtime := startDexService(t, cfg)
	flowClient := runtime.FlowClient

	completedID := "search-completed-" + uuid.NewString()
	workerTarget := startWorker(t, basic.NewHandler())
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		FlowId: completedID, FlowType: basic.FlowType, RequestId: newRequestID(),
		StartStepType: basic.Step1,
		FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{
			FlowConfigOverride: asyncDurabilityConfig(),
		}, workerTarget),
	})
	require.NoError(t, err)
	result, err := flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId: completedID, WaitTimeSeconds: 20,
	})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, result.GetFlowStatus())

	expected := map[string]dexpb.FlowStatus{completedID: dexpb.FlowStatus_FLOW_STATUS_COMPLETED}
	deadendTarget := startWorker(t, deadend.NewHandler())
	runningID := ""
	for _, scenario := range []struct {
		stop   dexpb.StopType
		status dexpb.FlowStatus
	}{
		{dexpb.StopType_STOP_TYPE_UNSPECIFIED, dexpb.FlowStatus_FLOW_STATUS_RUNNING},
		{dexpb.StopType_STOP_TYPE_FAIL, dexpb.FlowStatus_FLOW_STATUS_FAILED},
		{dexpb.StopType_STOP_TYPE_CANCEL, dexpb.FlowStatus_FLOW_STATUS_CANCELED},
		{dexpb.StopType_STOP_TYPE_TERMINATE, dexpb.FlowStatus_FLOW_STATUS_TERMINATED},
	} {
		id := "search-status-" + uuid.NewString()
		_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
			FlowId: id, FlowType: deadend.WorkflowType, RequestId: newRequestID(),
			StartStepType:    deadend.State1,
			StepOptions:      &dexpb.StepOptions{SkipWaitFor: true},
			FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{}, deadendTarget),
		})
		require.NoError(t, err)
		if scenario.status != dexpb.FlowStatus_FLOW_STATUS_RUNNING {
			_, err = flowClient.StopFlow(ctx, &dexpb.StopFlowRequest{
				FlowId: id, StopType: scenario.stop, Reason: "search status coverage",
			})
			require.NoError(t, err)
		} else {
			runningID = id
			t.Cleanup(func() {
				_, err := flowClient.StopFlow(context.Background(), &dexpb.StopFlowRequest{
					FlowId: id, StopType: dexpb.StopType_STOP_TYPE_TERMINATE,
				})
				require.NoError(t, err)
			})
		}
		expected[id] = scenario.status
	}
	workflowIDField := "WorkflowId"

	query := ""
	for id := range expected {
		if query != "" {
			query += " OR "
		}
		query += fmt.Sprintf("%s = '%s'", workflowIDField, id)
	}
	var allRuns []*dexpb.SearchFlowsResponseEntry
	require.Eventually(t, func() bool {
		var err error
		allRuns, err = searchRunPages(ctx, flowClient, query, 2)
		if err != nil {
			return false
		}
		statuses := map[string]dexpb.FlowStatus{}
		for _, run := range allRuns {
			statuses[run.GetFlowId()] = run.GetFlowStatus()
		}
		return len(statuses) == len(expected) && sameSearchStatuses(statuses, expected)
	}, 30*time.Second, 200*time.Millisecond, "expected search results with all statuses")

	currentRuns, err := searchRunPages(ctx, flowClient, query, 1)
	require.NoError(t, err)
	require.Len(t, currentRuns, len(expected))
	allRunIDs := map[string]bool{}
	for _, run := range allRuns {
		require.False(t, allRunIDs[run.GetRunId()], "duplicate run across pages")
		allRunIDs[run.GetRunId()] = true
	}
	for _, run := range currentRuns {
		require.True(t, allRunIDs[run.GetRunId()], "missing run from paginated search")
	}
	orQuery := fmt.Sprintf("%s = '%s' OR %s = '%s'", workflowIDField, completedID, workflowIDField, runningID)
	page, err := flowClient.SearchFlows(ctx, &dexpb.SearchFlowsRequest{Query: orQuery, PageSize: 100})
	require.NoError(t, err)
	require.Len(t, page.GetFlowRuns(), 2)

	completedQuery := fmt.Sprintf("%s = '%s' AND ExecutionStatus = \"Completed\"", workflowIDField, completedID)
	page, err = flowClient.SearchFlows(ctx, &dexpb.SearchFlowsRequest{Query: completedQuery, PageSize: 100})
	require.NoError(t, err)
	require.Len(t, page.GetFlowRuns(), 1)

	for _, emptyQuery := range []string{"", "   "} {
		page, err = flowClient.SearchFlows(ctx, &dexpb.SearchFlowsRequest{Query: emptyQuery, PageSize: 10})
		require.NoError(t, err)
		require.NotEmpty(t, page.GetFlowRuns())
	}
}

func searchRunPages(ctx context.Context, client dexpb.FlowServiceClient, query string, pageSize int32) ([]*dexpb.SearchFlowsResponseEntry, error) {
	var runs []*dexpb.SearchFlowsResponseEntry
	seen := map[string]bool{}
	token := ""
	for pageNumber := 0; pageNumber < 100; pageNumber++ {
		response, err := client.SearchFlows(ctx, &dexpb.SearchFlowsRequest{
			Query: query, PageSize: pageSize, NextPageToken: token,
		})
		if err != nil {
			return nil, err
		}
		for _, run := range response.GetFlowRuns() {
			if seen[run.GetRunId()] {
				return nil, fmt.Errorf("visibility changed during pagination: repeated run %s", run.GetRunId())
			}
			seen[run.GetRunId()] = true
		}
		runs = append(runs, response.GetFlowRuns()...)
		token = response.GetNextPageToken()
		if token == "" {
			return runs, nil
		}
	}
	return nil, fmt.Errorf("search did not finish within 100 pages")
}

func sameSearchStatuses(actual, expected map[string]dexpb.FlowStatus) bool {
	for id, status := range expected {
		if actual[id] != status {
			return false
		}
	}
	return true
}
