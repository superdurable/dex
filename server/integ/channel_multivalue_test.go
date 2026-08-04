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
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/integ/workflow/channel_multivalue"
	"github.com/superdurable/dex/integ/workflow/common"
	"github.com/superdurable/dex/service"
	"github.com/superdurable/dex/service/common/ptr"
	"google.golang.org/protobuf/proto"
)

func TestChannelMultivalueTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestChannelMultivalueExactN(t, service.BackendTypeTemporal, nil)
		smallWaitForFastTest()
		doTestChannelMultivalueOneToAll(t, service.BackendTypeTemporal, nil)
		smallWaitForFastTest()
		doTestChannelMultivalueZeroToAllEmpty(t, service.BackendTypeTemporal, nil)
		smallWaitForFastTest()
		doTestChannelMultivalueAtMostEmpty(t, service.BackendTypeTemporal, nil)
		doTestChannelMultivalueAtMostCapped(t, service.BackendTypeTemporal, nil)
		doTestChannelMultivalueRange(t, service.BackendTypeTemporal, nil)
		smallWaitForFastTest()
		doTestChannelMultivalueSameChannelExact(t, service.BackendTypeTemporal, nil)
		smallWaitForFastTest()
		doTestChannelMultivalueExact2PlusZero(t, service.BackendTypeTemporal, nil)
		smallWaitForFastTest()
		doTestChannelMultivalueAnyNoPremature(t, service.BackendTypeTemporal, nil)
		smallWaitForFastTest()
		doTestChannelMultivalueInvalidBounds(t, service.BackendTypeTemporal, nil)
		smallWaitForFastTest()
		doTestChannelMultivalueCanBuffered(t, service.BackendTypeTemporal)
		smallWaitForFastTest()
		doTestChannelMultivalueCanMatchBoundary(t, service.BackendTypeTemporal)
		smallWaitForFastTest()
	}
}

func TestChannelMultivalueCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestChannelMultivalueExactN(t, service.BackendTypeCadence, nil)
		smallWaitForFastTest()
		doTestChannelMultivalueOneToAll(t, service.BackendTypeCadence, nil)
		smallWaitForFastTest()
		doTestChannelMultivalueZeroToAllEmpty(t, service.BackendTypeCadence, nil)
		smallWaitForFastTest()
		doTestChannelMultivalueAtMostEmpty(t, service.BackendTypeCadence, nil)
		doTestChannelMultivalueAtMostCapped(t, service.BackendTypeCadence, nil)
		doTestChannelMultivalueRange(t, service.BackendTypeCadence, nil)
		smallWaitForFastTest()
		doTestChannelMultivalueSameChannelExact(t, service.BackendTypeCadence, nil)
		smallWaitForFastTest()
		doTestChannelMultivalueExact2PlusZero(t, service.BackendTypeCadence, nil)
		smallWaitForFastTest()
		doTestChannelMultivalueAnyNoPremature(t, service.BackendTypeCadence, nil)
		smallWaitForFastTest()
		doTestChannelMultivalueInvalidBounds(t, service.BackendTypeCadence, nil)
		smallWaitForFastTest()
	}
}

func doTestChannelMultivalueExactN(
	t *testing.T,
	backendType service.BackendType,
	flowConfig *dexpb.FlowConfig,
) {
	workerHandler, runtime, flowId, ctx := startChannelMultivalueFlow(
		t, backendType, channel_multivalue.ScenarioExactN, flowConfig,
	)
	publishChannelStrings(t, ctx, runtime.FlowClient, flowId, channel_multivalue.ChannelName, "m0", "m1", "m2", "m3", "m4")
	waitChannelMultivalueComplete(t, ctx, runtime.FlowClient, flowId)

	results := channelResultsFromData(t, workerHandler.GetTestResult(), channel_multivalue.ScenarioExactN)
	require.Len(t, results, 1)
	require.Equal(t, dexpb.ConditionStatus_CONDITION_STATUS_COMPLETED, results[0].GetConditionStatus())
	requireStringValues(t, results[0].GetValues(), "m0", "m1", "m2")

	leftover := channelReceivedFromDump(t, runtime, flowId, channel_multivalue.ChannelName)
	requireStringValues(t, leftover, "m3", "m4")
}

func doTestChannelMultivalueOneToAll(
	t *testing.T,
	backendType service.BackendType,
	flowConfig *dexpb.FlowConfig,
) {
	workerHandler, runtime, flowId, ctx := startChannelMultivalueFlow(
		t, backendType, channel_multivalue.ScenarioOneToAll, flowConfig,
	)
	publishChannelStrings(t, ctx, runtime.FlowClient, flowId, channel_multivalue.ChannelName, "a", "b", "c", "d")
	waitChannelMultivalueComplete(t, ctx, runtime.FlowClient, flowId)

	results := channelResultsFromData(t, workerHandler.GetTestResult(), channel_multivalue.ScenarioOneToAll)
	require.Len(t, results, 1)
	requireStringValues(t, results[0].GetValues(), "a", "b", "c", "d")
}

func doTestChannelMultivalueZeroToAllEmpty(
	t *testing.T,
	backendType service.BackendType,
	flowConfig *dexpb.FlowConfig,
) {
	workerHandler, runtime, flowId, ctx := startChannelMultivalueFlow(
		t, backendType, channel_multivalue.ScenarioZeroToAllEmpty, flowConfig,
	)
	waitChannelMultivalueComplete(t, ctx, runtime.FlowClient, flowId)

	results := channelResultsFromData(t, workerHandler.GetTestResult(), channel_multivalue.ScenarioZeroToAllEmpty)
	require.Len(t, results, 1)
	require.Equal(t, dexpb.ConditionStatus_CONDITION_STATUS_COMPLETED, results[0].GetConditionStatus())
	require.Empty(t, results[0].GetValues())
}

func doTestChannelMultivalueAtMostEmpty(
	t *testing.T,
	backendType service.BackendType,
	flowConfig *dexpb.FlowConfig,
) {
	workerHandler, runtime, flowId, ctx := startChannelMultivalueFlow(
		t, backendType, channel_multivalue.ScenarioAtMostEmpty, flowConfig,
	)
	waitChannelMultivalueComplete(t, ctx, runtime.FlowClient, flowId)

	results := channelResultsFromData(t, workerHandler.GetTestResult(), channel_multivalue.ScenarioAtMostEmpty)
	require.Len(t, results, 1)
	require.Equal(t, dexpb.ConditionStatus_CONDITION_STATUS_COMPLETED, results[0].GetConditionStatus())
	require.Empty(t, results[0].GetValues())
}

func doTestChannelMultivalueAtMostCapped(
	t *testing.T,
	backendType service.BackendType,
	flowConfig *dexpb.FlowConfig,
) {
	workerHandler, runtime, flowId, ctx := startChannelMultivalueFlow(
		t, backendType, channel_multivalue.ScenarioAtMostCapped, flowConfig,
	)
	waitChannelMultivalueComplete(t, ctx, runtime.FlowClient, flowId)

	results := channelResultsFromData(t, workerHandler.GetTestResult(), channel_multivalue.ScenarioAtMostCapped)
	require.Len(t, results, 1)
	require.Equal(t, dexpb.ConditionStatus_CONDITION_STATUS_COMPLETED, results[0].GetConditionStatus())
	requireStringValues(t, results[0].GetValues(), "m0", "m1", "m2")

	leftover := channelReceivedFromDump(t, runtime, flowId, channel_multivalue.ChannelName)
	requireStringValues(t, leftover, "m3", "m4")
}

func doTestChannelMultivalueRange(
	t *testing.T,
	backendType service.BackendType,
	flowConfig *dexpb.FlowConfig,
) {
	workerHandler, runtime, flowId, ctx := startChannelMultivalueFlow(
		t, backendType, channel_multivalue.ScenarioRange, flowConfig,
	)
	publishChannelStrings(t, ctx, runtime.FlowClient, flowId, channel_multivalue.ChannelName, "r0", "r1", "r2")
	waitChannelMultivalueComplete(t, ctx, runtime.FlowClient, flowId)

	results := channelResultsFromData(t, workerHandler.GetTestResult(), channel_multivalue.ScenarioRange)
	require.Len(t, results, 1)
	requireStringValues(t, results[0].GetValues(), "r0", "r1", "r2")
}

func doTestChannelMultivalueSameChannelExact(
	t *testing.T,
	backendType service.BackendType,
	flowConfig *dexpb.FlowConfig,
) {
	workerHandler, runtime, flowId, ctx := startChannelMultivalueFlow(
		t, backendType, channel_multivalue.ScenarioSameChannelExact, flowConfig,
	)
	publishChannelStrings(t, ctx, runtime.FlowClient, flowId, channel_multivalue.ChannelName, "m0", "m1", "m2", "m3")
	waitChannelMultivalueComplete(t, ctx, runtime.FlowClient, flowId)

	results := channelResultsFromData(t, workerHandler.GetTestResult(), channel_multivalue.ScenarioSameChannelExact)
	require.Len(t, results, 2)
	byID := channelResultsByID(results)
	requireStringValues(t, byID["c1"].GetValues(), "m0", "m1")
	requireStringValues(t, byID["c2"].GetValues(), "m2", "m3")
}

func doTestChannelMultivalueExact2PlusZero(
	t *testing.T,
	backendType service.BackendType,
	flowConfig *dexpb.FlowConfig,
) {
	workerHandler, runtime, flowId, ctx := startChannelMultivalueFlow(
		t, backendType, channel_multivalue.ScenarioExact2PlusZero, flowConfig,
	)
	publishChannelStrings(t, ctx, runtime.FlowClient, flowId, channel_multivalue.ChannelName, "m0", "m1", "m2")
	waitChannelMultivalueComplete(t, ctx, runtime.FlowClient, flowId)

	results := channelResultsFromData(t, workerHandler.GetTestResult(), channel_multivalue.ScenarioExact2PlusZero)
	byID := channelResultsByID(results)
	requireStringValues(t, byID["exact"].GetValues(), "m0", "m1")
	requireStringValues(t, byID["zero"].GetValues(), "m2")
}

func doTestChannelMultivalueAnyNoPremature(
	t *testing.T,
	backendType service.BackendType,
	flowConfig *dexpb.FlowConfig,
) {
	workerHandler, runtime, flowId, ctx := startChannelMultivalueFlow(
		t, backendType, channel_multivalue.ScenarioAnyNoPremature, flowConfig,
	)
	publishChannelStrings(t, ctx, runtime.FlowClient, flowId, channel_multivalue.ChannelName, "keep0", "keep1")
	publishChannelStrings(t, ctx, runtime.FlowClient, flowId, channel_multivalue.ChannelB, "win")
	waitChannelMultivalueComplete(t, ctx, runtime.FlowClient, flowId)

	results := channelResultsFromData(t, workerHandler.GetTestResult(), channel_multivalue.ScenarioAnyNoPremature)
	completed := completedChannelResults(results)
	require.Len(t, completed, 1)
	require.Equal(t, "b", completed[0].GetConditionId())
	requireStringValues(t, completed[0].GetValues(), "win")

	leftover := channelReceivedFromDump(t, runtime, flowId, channel_multivalue.ChannelName)
	requireStringValues(t, leftover, "keep0", "keep1")
}

func doTestChannelMultivalueInvalidBounds(
	t *testing.T,
	backendType service.BackendType,
	flowConfig *dexpb.FlowConfig,
) {
	_, runtime, flowId, ctx := startChannelMultivalueFlowWithStepOptions(
		t,
		backendType,
		channel_multivalue.ScenarioInvalidBounds,
		flowConfig,
		&dexpb.StepOptions{
			WaitForRetryPolicy: &dexpb.RetryPolicy{
				TotalDurationSeconds: 1,
			},
		},
	)
	resp, err := runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId:          flowId,
		WaitTimeSeconds: 20,
	})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_FAILED, resp.GetFlowStatus())
	require.Equal(t, dexpb.FlowErrorType_FLOW_ERROR_TYPE_INTERNAL, resp.GetErrorType())
	require.Contains(t, resp.GetErrorMessage(), "at_most")
}

func doTestChannelMultivalueCanBuffered(t *testing.T, backendType service.BackendType) {
	workerHandler, runtime, flowId, ctx := startChannelMultivalueFlow(
		t,
		backendType,
		channel_multivalue.ScenarioCanBuffered,
		&dexpb.FlowConfig{ContinueAsNewThreshold: ptr.Any(int32(1))},
	)
	publishChannelStrings(t, ctx, runtime.FlowClient, flowId, channel_multivalue.ChannelName, "b0", "b1")
	require.Eventually(t, func() bool {
		dump := queryChannelDump(t, runtime, flowId)
		received := dump.GetSnapshot().GetChannelReceived()[channel_multivalue.ChannelName]
		return received != nil && len(received.GetValues()) >= 2
	}, 15*time.Second, 100*time.Millisecond)

	publishChannelStrings(t, ctx, runtime.FlowClient, flowId, channel_multivalue.ChannelName, "b2")
	publishChannelStrings(t, ctx, runtime.FlowClient, flowId, channel_multivalue.ChannelName, "s2")
	waitChannelMultivalueComplete(t, ctx, runtime.FlowClient, flowId)

	results := channelResultsFromData(t, workerHandler.GetTestResult(), channel_multivalue.ScenarioCanBuffered)
	require.NotEmpty(t, results)
	requireStringValues(t, results[0].GetValues(), "b0", "b1", "b2")
}

func doTestChannelMultivalueCanMatchBoundary(t *testing.T, backendType service.BackendType) {
	workerHandler, runtime, flowId, ctx := startChannelMultivalueFlow(
		t,
		backendType,
		channel_multivalue.ScenarioCanMatchBoundary,
		&dexpb.FlowConfig{ContinueAsNewThreshold: ptr.Any(int32(1))},
	)
	publishChannelStrings(t, ctx, runtime.FlowClient, flowId, channel_multivalue.ChannelName, "c0", "c1", "c2")
	waitChannelMultivalueComplete(t, ctx, runtime.FlowClient, flowId)

	results := channelResultsFromData(t, workerHandler.GetTestResult(), channel_multivalue.ScenarioCanMatchBoundary)
	require.Len(t, results, 1)
	require.Equal(t, dexpb.ConditionStatus_CONDITION_STATUS_COMPLETED, results[0].GetConditionStatus())
	requireStringValues(t, results[0].GetValues(), "c0", "c1", "c2")
}

type channelMultivalueWorker interface {
	GetTestResult() common.TestResult
}

func startChannelMultivalueFlow(
	t *testing.T,
	backendType service.BackendType,
	scenario string,
	flowConfig *dexpb.FlowConfig,
) (channelMultivalueWorker, *integRuntime, string, context.Context) {
	return startChannelMultivalueFlowWithStepOptions(t, backendType, scenario, flowConfig, nil)
}

func startChannelMultivalueFlowWithStepOptions(
	t *testing.T,
	backendType service.BackendType,
	scenario string,
	flowConfig *dexpb.FlowConfig,
	stepOptions *dexpb.StepOptions,
) (channelMultivalueWorker, *integRuntime, string, context.Context) {
	t.Helper()
	workerHandler := channel_multivalue.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)

	flowId := channel_multivalue.WorkflowType + "-" + scenario + "-" + uuid.NewString()
	_, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           channel_multivalue.WorkflowType,
		FlowTimeoutSeconds: 40,

		StartStepType: channel_multivalue.State1,
		StepInput:     stringValue(scenario),
		StepOptions:   stepOptions,
		FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{
			FlowConfigOverride: flowConfig,
		}, workerTarget),
	})
	require.NoError(t, err)
	return workerHandler, runtime, flowId, ctx
}

func publishChannelStrings(
	t *testing.T,
	ctx context.Context,
	flowClient dexpb.FlowServiceClient,
	flowId string,
	channelName string,
	payloads ...string,
) {
	t.Helper()
	messages := make([]*dexpb.ChannelMessage, 0, len(payloads))
	for _, payload := range payloads {
		messages = append(messages, &dexpb.ChannelMessage{
			ChannelName: channelName,
			Value:       stringValue(payload),
		})
	}
	_, err := flowClient.PublishToChannel(ctx, &dexpb.PublishToChannelRequest{
		FlowId:   flowId,
		Messages: messages,
	})
	require.NoError(t, err)
}

func waitChannelMultivalueComplete(
	t *testing.T,
	ctx context.Context,
	flowClient dexpb.FlowServiceClient,
	flowId string,
) {
	t.Helper()
	resp, err := flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId:          flowId,
		WaitTimeSeconds: 30,
	})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, resp.GetFlowStatus())
}

func channelResultsFromData(
	t *testing.T,
	result common.TestResult,
	scenario string,
) []*dexpb.ChannelResult {
	t.Helper()
	raw, ok := result.InvokeData[scenario+"-results"]
	require.True(t, ok, "missing results for %s", scenario)
	results, ok := raw.([]*dexpb.ChannelResult)
	require.True(t, ok)
	return results
}

func channelResultsByID(results []*dexpb.ChannelResult) map[string]*dexpb.ChannelResult {
	out := make(map[string]*dexpb.ChannelResult, len(results))
	for _, result := range results {
		out[result.GetConditionId()] = result
	}
	return out
}

func completedChannelResults(results []*dexpb.ChannelResult) []*dexpb.ChannelResult {
	var out []*dexpb.ChannelResult
	for _, result := range results {
		if result.GetConditionStatus() == dexpb.ConditionStatus_CONDITION_STATUS_COMPLETED {
			out = append(out, result)
		}
	}
	return out
}

func requireStringValues(t *testing.T, values []*dexpb.Value, expected ...string) {
	t.Helper()
	require.Len(t, values, len(expected))
	for i, want := range expected {
		require.True(t, proto.Equal(stringValue(want), values[i]), "index %d", i)
	}
}

func channelReceivedFromDump(
	t *testing.T,
	runtime *integRuntime,
	flowId string,
	channelName string,
) []*dexpb.Value {
	t.Helper()
	dump := queryChannelDump(t, runtime, flowId)
	received := dump.GetSnapshot().GetChannelReceived()[channelName]
	if received == nil {
		return nil
	}
	return received.GetValues()
}

func queryChannelDump(
	t *testing.T,
	runtime *integRuntime,
	flowId string,
) *dexpb.DebugDumpResponse {
	t.Helper()
	var dump dexpb.DebugDumpResponse
	err := runtime.UnifiedClient.QueryWorkflow(
		context.Background(),
		&dump,
		flowId,
		"",
		service.DebugDumpQueryType,
	)
	require.NoError(t, err)
	return &dump
}
