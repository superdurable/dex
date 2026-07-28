// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package integ

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/iwf/gen/iwfpb"
	"github.com/superdurable/iwf/integ/workflow/wait_until_search_attributes"
	"github.com/superdurable/iwf/service"
	"github.com/superdurable/iwf/service/common/ptr"
)

func TestWaitUntilSearchAttributesWorkflowTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestWaitUntilSearchAttributes(t, &iwfpb.FlowConfig{
			ActiveStepSearchMode: ptr.Any(
				iwfpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_DISABLED,
			),
		})
		smallWaitForFastTest()
	}

	for i := 0; i < *repeatIntegTest; i++ {
		doTestWaitUntilSearchAttributes(t, &iwfpb.FlowConfig{
			ActiveStepSearchMode: ptr.Any(
				iwfpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_ENABLED_FOR_ALL,
			),
		})
		smallWaitForFastTest()
	}

	for i := 0; i < *repeatIntegTest; i++ {
		doTestWaitUntilSearchAttributes(t, nil)
		smallWaitForFastTest()
	}
}

func doTestWaitUntilSearchAttributes(t *testing.T, flowConfig *iwfpb.FlowConfig) {
	workerHandler := wait_until_search_attributes.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startIwfService(t, IwfServiceTestConfig{
		BackendType: service.BackendTypeTemporal,
	})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowId := wait_until_search_attributes.WorkflowType + uuid.NewString()
	startRequest := &iwfpb.StartFlowRequest{
		FlowId:             flowId,
		FlowType:           wait_until_search_attributes.WorkflowType,
		FlowTimeoutSeconds: 20,
		WorkerTarget:       workerTarget,
		StartStepType:      wait_until_search_attributes.State1,
	}
	if flowConfig != nil {
		startRequest.FlowStartOptions = &iwfpb.FlowStartOptions{
			FlowConfigOverride: flowConfig,
		}
	}
	_, err := flowClient.StartFlow(ctx, startRequest)
	require.NoError(t, err)

	time.Sleep(time.Duration(*searchWaitTimeIntegTest) * time.Millisecond)

	mode := iwfpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_ENABLED_FOR_STEPS_WITH_WAIT_FOR
	if flowConfig != nil && flowConfig.ActiveStepSearchMode != nil {
		mode = flowConfig.GetActiveStepSearchMode()
	}

	switch mode {
	case iwfpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_ENABLED_FOR_ALL:
		assertSearchFlows(t, flowClient, fmt.Sprintf("WorkflowId='%v'", flowId), 1)
		assertSearchFlows(
			t,
			flowClient,
			fmt.Sprintf(
				"WorkflowId='%v' AND %v='%v'",
				flowId,
				wait_until_search_attributes.TestSearchAttributeExecutingStateIdsKey,
				wait_until_search_attributes.State2,
			),
			1,
		)
	case iwfpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_ENABLED_FOR_STEPS_WITH_WAIT_FOR,
		iwfpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_DISABLED:
		assertSearchFlows(t, flowClient, fmt.Sprintf("WorkflowId='%v'", flowId), 1)
		assertSearchFlows(
			t,
			flowClient,
			fmt.Sprintf(
				"WorkflowId='%v' AND %v='%v'",
				flowId,
				wait_until_search_attributes.TestSearchAttributeExecutingStateIdsKey,
				wait_until_search_attributes.State2,
			),
			0,
		)
	}

	resp, err := flowClient.WaitForFlow(ctx, &iwfpb.WaitForFlowRequest{
		FlowId: flowId,
	})
	require.NoError(t, err)
	require.Equal(t, iwfpb.FlowStatus_FLOW_STATUS_COMPLETED, resp.GetFlowStatus())
}
