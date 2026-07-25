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
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/superdurable/iwf/integ/helpers"
	"github.com/superdurable/iwf/integ/workflow/wait_until_search_attributes_optimization"
	"github.com/superdurable/iwf/service/common/ptr"
	"go.temporal.io/api/common/v1"
	"go.temporal.io/api/enums/v1"
	history "go.temporal.io/api/history/v1"
	"go.temporal.io/api/workflowservice/v1"

	"github.com/stretchr/testify/assert"
	"github.com/superdurable/iwf/gen/iwfidl"
	"github.com/superdurable/iwf/service"
)

func TestWaitUntilSearchAttributesOptimizationWorkflowTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestWaitUntilHistoryCompleted(t, service.BackendTypeTemporal, &iwfidl.WorkflowConfig{
			ExecutingStateIdMode: ptr.Any(iwfidl.DISABLED),
		})
		smallWaitForFastTest()
	}

	for i := 0; i < *repeatIntegTest; i++ {
		doTestWaitUntilHistoryCompleted(t, service.BackendTypeTemporal, &iwfidl.WorkflowConfig{
			ExecutingStateIdMode: ptr.Any(iwfidl.ENABLED_FOR_ALL),
		})
		smallWaitForFastTest()
	}

	for i := 0; i < *repeatIntegTest; i++ {
		doTestWaitUntilHistoryCompleted(t, service.BackendTypeTemporal, nil) // defaults to ExecutingStateIdMode: ENABLED_FOR_STATES_WITH_WAIT_UNTIL
		smallWaitForFastTest()
	}
}

func doTestWaitUntilHistoryCompleted(
	t *testing.T, backendType service.BackendType, config *iwfidl.WorkflowConfig,
) {
	assertions := assert.New(t)
	wfHandler := wait_until_search_attributes_optimization.NewHandler()
	closeFunc1 := startWorkflowWorker(wfHandler, t)
	defer closeFunc1()

	uclient, closeFunc2 := startIwfServiceByConfig(IwfServiceTestConfig{
		BackendType: backendType,
	})
	defer closeFunc2()

	// start a workflow
	apiClient := iwfidl.NewAPIClient(&iwfidl.Configuration{
		Servers: []iwfidl.ServerConfiguration{
			{
				URL: "http://localhost:" + testIwfServerPort,
			},
		},
	})
	wfId := wait_until_search_attributes_optimization.WorkflowType + strconv.Itoa(int(time.Now().UnixNano()))

	reqStart := apiClient.DefaultApi.ApiV1WorkflowStartPost(context.Background())
	wfReq := iwfidl.WorkflowStartRequest{
		WorkflowId:             wfId,
		IwfWorkflowType:        wait_until_search_attributes_optimization.WorkflowType,
		WorkflowTimeoutSeconds: 15,
		IwfWorkerUrl:           "http://localhost:" + testWorkflowServerPort,
		StartStateId:           ptr.Any(wait_until_search_attributes_optimization.State1),
		WorkflowStartOptions: &iwfidl.WorkflowStartOptions{
			WorkflowConfigOverride: config,
		},
	}
	_, httpResp, err := reqStart.WorkflowStartRequest(wfReq).Execute()
	failTestAtHttpError(err, httpResp, t)

	// Wait before sending the signal that would allow State 4 to proceed.
	time.Sleep(time.Second * 5)

	signalValue := iwfidl.EncodedObject{
		Encoding: iwfidl.PtrString("json"),
		Data:     iwfidl.PtrString("test"),
	}

	reqSignal := apiClient.DefaultApi.ApiV1WorkflowSignalPost(context.Background())
	httpResp, err = reqSignal.WorkflowSignalRequest(iwfidl.WorkflowSignalRequest{
		WorkflowId:        wfId,
		SignalChannelName: wait_until_search_attributes_optimization.SignalName,
		SignalValue:       &signalValue,
	}).Execute()

	failTestAtHttpError(err, httpResp, t)

	reqWait := apiClient.DefaultApi.ApiV1WorkflowGetWithWaitPost(context.Background())
	_, httpResp, err = reqWait.WorkflowGetRequest(iwfidl.WorkflowGetRequest{
		WorkflowId: wfId,
	}).Execute()
	failTestAtHttpError(err, httpResp, t)

	// wait for workflow to complete
	resp, httpResp, err := reqWait.WorkflowGetRequest(iwfidl.WorkflowGetRequest{
		WorkflowId: wfId,
	}).Execute()
	failTestAtHttpErrorOrWorkflowUncompleted(err, httpResp, resp, t)

	api := uclient.GetApiService().(workflowservice.WorkflowServiceClient)
	reqHistory := &workflowservice.GetWorkflowExecutionHistoryRequest{
		Namespace: "default",
		Execution: &common.WorkflowExecution{
			WorkflowId: wfId,
		},
	}
	eventHistory, err := api.GetWorkflowExecutionHistory(context.Background(), reqHistory)
	if err != nil {
		helpers.FailTestWithErrorMessage("couldn't load eventHistory", t)
	}

	var upsertSAEvents []*history.HistoryEvent

	for _, e := range eventHistory.History.Events {
		if e.EventType == enums.EVENT_TYPE_UPSERT_WORKFLOW_SEARCH_ATTRIBUTES {
			upsertSAEvents = append(upsertSAEvents, e)
		}
	}

	switch mode := config.GetExecutingStateIdMode(); mode {
	case iwfidl.ENABLED_FOR_ALL:
		assertions.Equal(10, len(upsertSAEvents))
		assertions.Equal([]string{"S1"}, historyEventSAs(upsertSAEvents[1]))
		assertions.Equal([]string{"S2"}, historyEventSAs(upsertSAEvents[2]))
		assertions.Equal([]string{"S2", "S3"}, historyEventSAs(upsertSAEvents[3]))
		assertions.Equal([]string{"S3", "S4"}, historyEventSAs(upsertSAEvents[4]))
		assertions.Equal([]string{"S3", "S5"}, historyEventSAs(upsertSAEvents[5]))
		assertions.Equal([]string{"S3", "S6", "S7"}, historyEventSAs(upsertSAEvents[6]))
		assertions.Equal([]string{"S3", "S6"}, historyEventSAs(upsertSAEvents[7]))
		assertions.Equal([]string{"S3"}, historyEventSAs(upsertSAEvents[8]))
		assertions.Equal([]string{"null"}, historyEventSAs(upsertSAEvents[9]))
	case iwfidl.DISABLED:
		assertions.Equal(1, len(upsertSAEvents))
	case iwfidl.ENABLED_FOR_STATES_WITH_WAIT_UNTIL:
	default:
		assertions.Equal(9, len(upsertSAEvents))
		assertions.Equal([]string{"S1"}, historyEventSAs(upsertSAEvents[1]))
		assertions.Equal([]string{"null"}, historyEventSAs(upsertSAEvents[2]))
		assertions.Equal([]string{"S3"}, historyEventSAs(upsertSAEvents[3]))
		assertions.Equal([]string{"S3", "S4"}, historyEventSAs(upsertSAEvents[4]))
		assertions.Equal([]string{"S3"}, historyEventSAs(upsertSAEvents[5]))
		assertions.Equal([]string{"S3", "S6"}, historyEventSAs(upsertSAEvents[6]))
		assertions.Equal([]string{"S3"}, historyEventSAs(upsertSAEvents[7]))
		assertions.Equal([]string{"null"}, historyEventSAs(upsertSAEvents[8]))
	}
}

func historyEventSAs(e *history.HistoryEvent) []string {
	attrs := e.GetAttributes().(*history.HistoryEvent_UpsertWorkflowSearchAttributesEventAttributes)
	data := string(attrs.UpsertWorkflowSearchAttributesEventAttributes.GetSearchAttributes().GetIndexedFields()[service.SearchAttributeActiveStepIds].GetData())
	data = strings.ReplaceAll(data, "[", "")
	data = strings.ReplaceAll(data, "]", "")
	data = strings.ReplaceAll(data, "\"", "")
	dataSlice := strings.Split(data, ",")
	slices.Sort(dataSlice)
	return dataSlice
}
