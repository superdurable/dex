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
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/integ/workflow/wait_until_search_attributes"
	"github.com/superdurable/dex/service"
	"github.com/superdurable/dex/service/common/ptr"
)

func TestWaitUntilSearchAttributesWorkflowTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestWaitUntilSearchAttributes(t, &dexpb.FlowConfig{
			ActiveStepSearchMode: ptr.Any(
				dexpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_DISABLED,
			),
		})
		smallWaitForFastTest()
	}

	for i := 0; i < *repeatIntegTest; i++ {
		doTestWaitUntilSearchAttributes(t, &dexpb.FlowConfig{
			ActiveStepSearchMode: ptr.Any(
				dexpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_ENABLED_FOR_ALL,
			),
		})
		smallWaitForFastTest()
	}

	for i := 0; i < *repeatIntegTest; i++ {
		doTestWaitUntilSearchAttributes(t, nil)
		smallWaitForFastTest()
	}
}

func doTestWaitUntilSearchAttributes(t *testing.T, flowConfig *dexpb.FlowConfig) {
	workerHandler := wait_until_search_attributes.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, DexServiceTestConfig{
		BackendType: service.BackendTypeTemporal,
	})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowId := wait_until_search_attributes.WorkflowType + uuid.NewString()
	startRequest := &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           wait_until_search_attributes.WorkflowType,
		FlowTimeoutSeconds: 20,

		StartStepType:    wait_until_search_attributes.State1,
		FlowStartOptions: withWorkerTarget(nil, workerTarget),
	}
	if flowConfig != nil {
		startRequest.FlowStartOptions = withWorkerTarget(&dexpb.FlowStartOptions{
			FlowConfigOverride: flowConfig,
		}, workerTarget)
	}
	_, err := flowClient.StartFlow(ctx, startRequest)
	require.NoError(t, err)

	mode := dexpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_ENABLED_FOR_STEPS_WITH_WAIT_FOR
	if flowConfig != nil && flowConfig.ActiveStepSearchMode != nil {
		mode = flowConfig.GetActiveStepSearchMode()
	}

	switch mode {
	case dexpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_ENABLED_FOR_ALL:
		requireSearchFlowEventually(t, flowClient, fmt.Sprintf("WorkflowId='%v'", flowId))
		requireSearchFlowEventually(
			t,
			flowClient,
			fmt.Sprintf(
				"WorkflowId='%v' AND %v='%v'",
				flowId,
				service.SearchAttributeActiveStepTypes,
				wait_until_search_attributes.State2,
			),
		)
	case dexpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_ENABLED_FOR_STEPS_WITH_WAIT_FOR,
		dexpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_DISABLED:
		requireSearchFlowEventually(t, flowClient, fmt.Sprintf("WorkflowId='%v'", flowId))
		assertSearchFlows(
			t,
			flowClient,
			fmt.Sprintf(
				"WorkflowId='%v' AND %v='%v'",
				flowId,
				service.SearchAttributeActiveStepTypes,
				wait_until_search_attributes.State2,
			),
			0,
		)
	}

	resp, err := flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId: flowId,
	})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, resp.GetFlowStatus())
}

func requireSearchFlowEventually(
	t *testing.T,
	flowClient dexpb.FlowServiceClient,
	query string,
) {
	t.Helper()
	var searchResponse *dexpb.SearchFlowsResponse
	var searchErr error
	require.Eventually(t, func() bool {
		searchResponse, searchErr = flowClient.SearchFlows(context.Background(), &dexpb.SearchFlowsRequest{
			Query:    query,
			PageSize: 2,
		})
		return searchErr == nil && len(searchResponse.GetFlowRuns()) == 1
	}, 10*time.Second, 100*time.Millisecond, "expected one result for query %v", query)
	require.NoError(t, searchErr)
	require.NotEmpty(t, searchResponse.GetFlowRuns()[0].GetIndexedAttributes())
	require.Empty(t, searchResponse.GetNextPageToken())
}
