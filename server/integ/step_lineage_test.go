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
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/integ/workflow/basic"
	"github.com/superdurable/dex/integ/workflow/deadend"
	"github.com/superdurable/dex/service"
	dexconverter "github.com/superdurable/dex/service/common/converter"
	"github.com/superdurable/dex/service/common/ptr"
	temporalcommon "go.temporal.io/api/common/v1"
	temporalenums "go.temporal.io/api/enums/v1"
	temporalhistory "go.temporal.io/api/history/v1"
	"go.temporal.io/api/workflowservice/v1"
	cadenceshared "go.uber.org/cadence/.gen/go/shared"
	cadenceclient "go.uber.org/cadence/client"
)

type temporalLocalActivityMarkerData struct {
	ActivityType string
}

type cadenceLocalActivityMarkerData struct {
	ActivityType string `json:"activityType,omitempty"`
	ResultJSON   string `json:"resultJson,omitempty"`
}

func TestStepLineageTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	testStepLineageDurabilities(t, service.BackendTypeTemporal)
}

func TestStepLineageCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	testStepLineageDurabilities(t, service.BackendTypeCadence)
}

func testStepLineageDurabilities(t *testing.T, backendType service.BackendType) {
	durabilities := []dexpb.StepDurability{
		dexpb.StepDurability_STEP_DURABILITY_SYNC,
		dexpb.StepDurability_STEP_DURABILITY_ASYNC,
	}
	for _, durability := range durabilities {
		for i := 0; i < *repeatIntegTest; i++ {
			t.Run(fmt.Sprintf("%s-%d", durability.String(), i), func(t *testing.T) {
				testStepLineageAcrossContinueAsNew(t, backendType, durability)
			})
		}
	}
	for i := 0; i < *repeatIntegTest; i++ {
		t.Run(fmt.Sprintf("RPC_CONTINUE_AS_NEW-%d", i), func(t *testing.T) {
			testRPCStepLineageAcrossContinueAsNew(t, backendType)
		})
	}
}

func testStepLineageAcrossContinueAsNew(
	t *testing.T,
	backendType service.BackendType,
	durability dexpb.StepDurability,
) {
	workerTarget := startWorker(t, basic.NewHandler())
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	flowID := basic.FlowType + "-lineage-" + uuid.NewString()
	startResponse, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		FlowId:             flowID,
		FlowType:           basic.FlowType,
		FlowTimeoutSeconds: 60,
		WorkerTarget:       workerTarget,
		StartStepType:      basic.Step1,
		FlowStartOptions: &dexpb.FlowStartOptions{
			FlowConfigOverride: &dexpb.FlowConfig{
				StepDurability:         ptr.Any(durability),
				ContinueAsNewThreshold: ptr.Any(int32(1)),
			},
		},
	})
	require.NoError(t, err)

	_, err = runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowID})
	require.NoError(t, err)

	lineage := collectStepLineage(
		t,
		ctx,
		runtime,
		backendType,
		flowID,
		startResponse.GetRunId(),
	)
	require.Equal(t, service.StartingStepSource, lineage[basic.Step1+"-1"])
	require.Equal(t, basic.Step1+"-1", lineage[basic.Step2+"-1"])
}

func testRPCStepLineageAcrossContinueAsNew(
	t *testing.T,
	backendType service.BackendType,
) {
	workerHandler := deadend.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	flowID := deadend.WorkflowType + "-lineage-" + uuid.NewString()
	startResponse, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		FlowId:             flowID,
		FlowType:           deadend.WorkflowType,
		FlowTimeoutSeconds: 60,
		WorkerTarget:       workerTarget,
		FlowStartOptions: &dexpb.FlowStartOptions{
			FlowConfigOverride: &dexpb.FlowConfig{
				StepDurability: ptr.Any(
					dexpb.StepDurability_STEP_DURABILITY_ASYNC,
				),
				ContinueAsNewThreshold: ptr.Any(int32(1)),
			},
		},
	})
	require.NoError(t, err)

	_, err = runtime.FlowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
		FlowId:  flowID,
		RpcName: deadend.RPCTriggerState,
	})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return workerHandler.GetTestResult().InvokeHistory[deadend.State1+"_execute"] > 0
	}, 30*time.Second, 50*time.Millisecond)

	_, err = runtime.FlowClient.StopFlow(ctx, &dexpb.StopFlowRequest{FlowId: flowID})
	require.NoError(t, err)

	lineage := collectStepLineage(
		t,
		ctx,
		runtime,
		backendType,
		flowID,
		startResponse.GetRunId(),
	)
	require.Equal(
		t,
		service.RPCStepSource(deadend.RPCTriggerState),
		lineage[deadend.State1+"-1"],
	)
}

func collectStepLineage(
	t *testing.T,
	ctx context.Context,
	runtime *integRuntime,
	backendType service.BackendType,
	flowID string,
	firstRunID string,
) map[string]string {
	t.Helper()
	lineage := map[string]string{}
	visitedRunIDs := map[string]struct{}{}
	runID := firstRunID
	for runID != "" {
		_, duplicate := visitedRunIDs[runID]
		require.False(t, duplicate, "continue-as-new cycle at run %s", runID)
		visitedRunIDs[runID] = struct{}{}

		switch backendType {
		case service.BackendTypeTemporal:
			runID = collectTemporalRunLineage(t, ctx, runtime, flowID, runID, lineage)
		case service.BackendTypeCadence:
			runID = collectCadenceRunLineage(t, ctx, runtime, flowID, runID, lineage)
		default:
			require.FailNow(t, "unsupported backend", backendType)
		}
	}
	return lineage
}

func collectTemporalRunLineage(
	t *testing.T,
	ctx context.Context,
	runtime *integRuntime,
	flowID string,
	runID string,
	lineage map[string]string,
) string {
	t.Helper()
	api := runtime.UnifiedClient.GetApiService().(workflowservice.WorkflowServiceClient)
	dataConverter := dexconverter.NewTemporalDataConverter()
	nextPageToken := []byte(nil)
	nextRunID := ""
	for {
		response, err := api.GetWorkflowExecutionHistory(
			ctx,
			&workflowservice.GetWorkflowExecutionHistoryRequest{
				Namespace: testNamespace,
				Execution: &temporalcommon.WorkflowExecution{
					WorkflowId: flowID,
					RunId:      runID,
				},
				MaximumPageSize: 1000,
				NextPageToken:   nextPageToken,
			},
		)
		require.NoError(t, err)
		for _, event := range response.GetHistory().GetEvents() {
			switch event.GetEventType() {
			case temporalenums.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED:
				recordTemporalScheduledLineage(t, dataConverter, event, lineage)
			case temporalenums.EVENT_TYPE_MARKER_RECORDED:
				recordTemporalLocalLineage(t, dataConverter, event, lineage)
			case temporalenums.EVENT_TYPE_WORKFLOW_EXECUTION_CONTINUED_AS_NEW:
				nextRunID = event.GetWorkflowExecutionContinuedAsNewEventAttributes().
					GetNewExecutionRunId()
			}
		}
		nextPageToken = response.GetNextPageToken()
		if len(nextPageToken) == 0 {
			return nextRunID
		}
	}
}

func recordTemporalScheduledLineage(
	t *testing.T,
	dataConverter interface {
		FromPayloads(payloads *temporalcommon.Payloads, valuePtrs ...interface{}) error
	},
	event *temporalhistory.HistoryEvent,
	lineage map[string]string,
) {
	t.Helper()
	attributes := event.GetActivityTaskScheduledEventAttributes()
	activityType := attributes.GetActivityType().GetName()
	switch {
	case strings.Contains(activityType, "InvokeWaitForMethod"):
		var input dexpb.InvokeWaitForMethodActivityInput
		require.NoError(t, dataConverter.FromPayloads(attributes.GetInput(), &input))
		recordStepContext(t, lineage, input.GetRequest().GetContext())
	case strings.Contains(activityType, "InvokeExecuteMethod"):
		var input dexpb.InvokeExecuteMethodActivityInput
		require.NoError(t, dataConverter.FromPayloads(attributes.GetInput(), &input))
		recordStepContext(t, lineage, input.GetRequest().GetContext())
	}
}

func recordTemporalLocalLineage(
	t *testing.T,
	dataConverter interface {
		FromPayloads(payloads *temporalcommon.Payloads, valuePtrs ...interface{}) error
	},
	event *temporalhistory.HistoryEvent,
	lineage map[string]string,
) {
	t.Helper()
	attributes := event.GetMarkerRecordedEventAttributes()
	if attributes.GetMarkerName() != "LocalActivity" {
		return
	}
	details := attributes.GetDetails()
	markerPayload := details["data"]
	resultPayload := details["result"]
	if markerPayload == nil || resultPayload == nil {
		return
	}
	var marker temporalLocalActivityMarkerData
	require.NoError(t, dataConverter.FromPayloads(markerPayload, &marker))
	switch {
	case strings.Contains(marker.ActivityType, "InvokeWaitForMethod"):
		var output dexpb.InvokeWaitForMethodActivityOutput
		require.NoError(t, dataConverter.FromPayloads(resultPayload, &output))
		recordLocalActivityInput(t, lineage, output.GetResponse().GetLocalActivityInput())
	case strings.Contains(marker.ActivityType, "InvokeExecuteMethod"):
		var output dexpb.InvokeExecuteMethodActivityOutput
		require.NoError(t, dataConverter.FromPayloads(resultPayload, &output))
		recordLocalActivityInput(t, lineage, output.GetResponse().GetLocalActivityInput())
	}
}

func collectCadenceRunLineage(
	t *testing.T,
	ctx context.Context,
	runtime *integRuntime,
	flowID string,
	runID string,
	lineage map[string]string,
) string {
	t.Helper()
	api := runtime.UnifiedClient.GetApiService().(cadenceclient.Client)
	dataConverter := dexconverter.NewCadenceDataConverter()
	iterator := api.GetWorkflowHistory(
		ctx,
		flowID,
		runID,
		false,
		cadenceshared.HistoryEventFilterTypeAllEvent,
	)
	nextRunID := ""
	for iterator.HasNext() {
		event, err := iterator.Next()
		require.NoError(t, err)
		switch event.GetEventType() {
		case cadenceshared.EventTypeActivityTaskScheduled:
			recordCadenceScheduledLineage(t, dataConverter, event, lineage)
		case cadenceshared.EventTypeMarkerRecorded:
			recordCadenceLocalLineage(t, dataConverter, event, lineage)
		case cadenceshared.EventTypeWorkflowExecutionContinuedAsNew:
			nextRunID = event.GetWorkflowExecutionContinuedAsNewEventAttributes().
				GetNewExecutionRunId()
		}
	}
	return nextRunID
}

func recordCadenceScheduledLineage(
	t *testing.T,
	dataConverter interface {
		FromData(input []byte, valuePtrs ...interface{}) error
	},
	event *cadenceshared.HistoryEvent,
	lineage map[string]string,
) {
	t.Helper()
	attributes := event.GetActivityTaskScheduledEventAttributes()
	activityType := attributes.GetActivityType().GetName()
	switch {
	case strings.Contains(activityType, "InvokeWaitForMethod"):
		var input dexpb.InvokeWaitForMethodActivityInput
		require.NoError(t, dataConverter.FromData(attributes.GetInput(), &input))
		recordStepContext(t, lineage, input.GetRequest().GetContext())
	case strings.Contains(activityType, "InvokeExecuteMethod"):
		var input dexpb.InvokeExecuteMethodActivityInput
		require.NoError(t, dataConverter.FromData(attributes.GetInput(), &input))
		recordStepContext(t, lineage, input.GetRequest().GetContext())
	}
}

func recordCadenceLocalLineage(
	t *testing.T,
	dataConverter interface {
		FromData(input []byte, valuePtrs ...interface{}) error
	},
	event *cadenceshared.HistoryEvent,
	lineage map[string]string,
) {
	t.Helper()
	attributes := event.GetMarkerRecordedEventAttributes()
	if attributes.GetMarkerName() != "LocalActivity" {
		return
	}
	var marker cadenceLocalActivityMarkerData
	require.NoError(t, dataConverter.FromData(attributes.GetDetails(), &marker))
	if marker.ResultJSON == "" {
		return
	}
	switch {
	case strings.Contains(marker.ActivityType, "InvokeWaitForMethod"):
		var output dexpb.InvokeWaitForMethodActivityOutput
		require.NoError(
			t,
			dataConverter.FromData([]byte(marker.ResultJSON), &output),
			"activity type %s, result %x",
			marker.ActivityType,
			[]byte(marker.ResultJSON),
		)
		recordLocalActivityInput(t, lineage, output.GetResponse().GetLocalActivityInput())
	case strings.Contains(marker.ActivityType, "InvokeExecuteMethod"):
		var output dexpb.InvokeExecuteMethodActivityOutput
		require.NoError(
			t,
			dataConverter.FromData([]byte(marker.ResultJSON), &output),
			"activity type %s, result %x",
			marker.ActivityType,
			[]byte(marker.ResultJSON),
		)
		recordLocalActivityInput(t, lineage, output.GetResponse().GetLocalActivityInput())
	}
}

func recordLocalActivityInput(
	t *testing.T,
	lineage map[string]string,
	input *dexpb.LocalActivityInput,
) {
	t.Helper()
	require.NotNil(t, input)
	recordStepLineage(
		t,
		lineage,
		input.GetCurrentStepExecutionId(),
		input.GetFromStepExecutionId(),
	)
}

func recordStepContext(
	t *testing.T,
	lineage map[string]string,
	stepContext *dexpb.Context,
) {
	t.Helper()
	require.NotNil(t, stepContext)
	recordStepLineage(
		t,
		lineage,
		stepContext.GetStepExecutionId(),
		stepContext.GetFromStepExecutionId(),
	)
}

func recordStepLineage(
	t *testing.T,
	lineage map[string]string,
	currentStepExecutionID string,
	fromStepExecutionID string,
) {
	t.Helper()
	require.NotEmpty(t, currentStepExecutionID)
	if existingSource, exists := lineage[currentStepExecutionID]; exists {
		require.Equal(t, existingSource, fromStepExecutionID)
		return
	}
	lineage[currentStepExecutionID] = fromStepExecutionID
}
