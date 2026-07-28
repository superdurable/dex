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
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/iwf/gen/iwfpb"
	"github.com/superdurable/iwf/integ/workflow/wait_until_search_attributes_optimization"
	"github.com/superdurable/iwf/service"
	"github.com/superdurable/iwf/service/common/ptr"
	"go.temporal.io/api/common/v1"
	"go.temporal.io/api/enums/v1"
	history "go.temporal.io/api/history/v1"
	"go.temporal.io/api/workflowservice/v1"
)

func TestWaitUntilSearchAttributesOptimizationWorkflowTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestWaitUntilHistoryCompleted(t, &iwfpb.FlowConfig{
			ActiveStepSearchMode: ptr.Any(
				iwfpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_DISABLED,
			),
		})
		smallWaitForFastTest()
	}

	for i := 0; i < *repeatIntegTest; i++ {
		doTestWaitUntilHistoryCompleted(t, &iwfpb.FlowConfig{
			ActiveStepSearchMode: ptr.Any(
				iwfpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_ENABLED_FOR_ALL,
			),
		})
		smallWaitForFastTest()
	}

	for i := 0; i < *repeatIntegTest; i++ {
		doTestWaitUntilHistoryCompleted(t, nil)
		smallWaitForFastTest()
	}
}

func doTestWaitUntilHistoryCompleted(t *testing.T, flowConfig *iwfpb.FlowConfig) {
	workerHandler := wait_until_search_attributes_optimization.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startIwfService(t, IwfServiceTestConfig{
		BackendType: service.BackendTypeTemporal,
	})
	flowClient := runtime.FlowClient
	unifiedClient := runtime.UnifiedClient

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	flowId := wait_until_search_attributes_optimization.WorkflowType + uuid.NewString()
	startRequest := &iwfpb.StartFlowRequest{
		FlowId:             flowId,
		FlowType:           wait_until_search_attributes_optimization.WorkflowType,
		FlowTimeoutSeconds: 15,
		WorkerTarget:       workerTarget,
		StartStepType:      wait_until_search_attributes_optimization.State1,
	}
	if flowConfig != nil {
		startRequest.FlowStartOptions = &iwfpb.FlowStartOptions{
			FlowConfigOverride: flowConfig,
		}
	}
	_, err := flowClient.StartFlow(ctx, startRequest)
	require.NoError(t, err)

	time.Sleep(5 * time.Second)

	_, err = flowClient.PublishToChannel(ctx, &iwfpb.PublishToChannelRequest{
		FlowId: flowId,
		Messages: []*iwfpb.ChannelMessage{
			{
				ChannelName: wait_until_search_attributes_optimization.SignalName,
				Value:       objJSONValue(`"test"`),
			},
		},
	})
	require.NoError(t, err)

	resp, err := flowClient.WaitForFlow(ctx, &iwfpb.WaitForFlowRequest{
		FlowId: flowId,
	})
	require.NoError(t, err)
	require.Equal(t, iwfpb.FlowStatus_FLOW_STATUS_COMPLETED, resp.GetFlowStatus())

	api := unifiedClient.GetApiService().(workflowservice.WorkflowServiceClient)
	eventHistory, err := api.GetWorkflowExecutionHistory(ctx, &workflowservice.GetWorkflowExecutionHistoryRequest{
		Namespace: testNamespace,
		Execution: &common.WorkflowExecution{
			WorkflowId: flowId,
		},
	})
	require.NoError(t, err)

	var upsertSAEvents []*history.HistoryEvent
	for _, event := range eventHistory.History.Events {
		if event.EventType == enums.EVENT_TYPE_UPSERT_WORKFLOW_SEARCH_ATTRIBUTES {
			upsertSAEvents = append(upsertSAEvents, event)
		}
	}

	mode := iwfpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_ENABLED_FOR_STEPS_WITH_WAIT_FOR
	if flowConfig != nil && flowConfig.ActiveStepSearchMode != nil {
		mode = flowConfig.GetActiveStepSearchMode()
	}

	switch mode {
	case iwfpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_ENABLED_FOR_ALL:
		require.Equal(t, 10, len(upsertSAEvents))
		require.Equal(t, []string{"S1"}, historyEventSAs(upsertSAEvents[1]))
		require.Equal(t, []string{"S2"}, historyEventSAs(upsertSAEvents[2]))
		require.Equal(t, []string{"S2", "S3"}, historyEventSAs(upsertSAEvents[3]))
		require.Equal(t, []string{"S3", "S4"}, historyEventSAs(upsertSAEvents[4]))
		require.Equal(t, []string{"S3", "S5"}, historyEventSAs(upsertSAEvents[5]))
		require.Equal(t, []string{"S3", "S6", "S7"}, historyEventSAs(upsertSAEvents[6]))
		require.Equal(t, []string{"S3", "S6"}, historyEventSAs(upsertSAEvents[7]))
		require.Equal(t, []string{"S3"}, historyEventSAs(upsertSAEvents[8]))
		require.Equal(t, []string{"null"}, historyEventSAs(upsertSAEvents[9]))
	case iwfpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_DISABLED:
		require.Equal(t, 1, len(upsertSAEvents))
	default:
		require.Equal(t, 9, len(upsertSAEvents))
		require.Equal(t, []string{"S1"}, historyEventSAs(upsertSAEvents[1]))
		require.Equal(t, []string{"null"}, historyEventSAs(upsertSAEvents[2]))
		require.Equal(t, []string{"S3"}, historyEventSAs(upsertSAEvents[3]))
		require.Equal(t, []string{"S3", "S4"}, historyEventSAs(upsertSAEvents[4]))
		require.Equal(t, []string{"S3"}, historyEventSAs(upsertSAEvents[5]))
		require.Equal(t, []string{"S3", "S6"}, historyEventSAs(upsertSAEvents[6]))
		require.Equal(t, []string{"S3"}, historyEventSAs(upsertSAEvents[7]))
		require.Equal(t, []string{"null"}, historyEventSAs(upsertSAEvents[8]))
	}
}

func historyEventSAs(event *history.HistoryEvent) []string {
	attrs := event.GetAttributes().(*history.HistoryEvent_UpsertWorkflowSearchAttributesEventAttributes)
	data := string(attrs.UpsertWorkflowSearchAttributesEventAttributes.GetSearchAttributes().GetIndexedFields()[service.SearchAttributeActiveStepTypes].GetData())
	data = strings.ReplaceAll(data, "[", "")
	data = strings.ReplaceAll(data, "]", "")
	data = strings.ReplaceAll(data, "\"", "")
	dataSlice := strings.Split(data, ",")
	slices.Sort(dataSlice)
	return dataSlice
}
