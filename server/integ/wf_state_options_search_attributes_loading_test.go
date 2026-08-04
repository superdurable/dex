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
	"github.com/superdurable/dex/integ/workflow/wf_state_options_search_attributes_loading"
	"github.com/superdurable/dex/service"
)

func TestWfStateOptionsSearchAttributesLoading(t *testing.T) {
	for _, backendType := range getBackendTypes() {
		for i := 0; i < *repeatIntegTest; i++ {
			doTestWfStateOptionsSearchAttributesLoading(t, backendType)
			smallWaitForFastTest()
		}
	}
}

func doTestWfStateOptionsSearchAttributesLoading(t *testing.T, backendType service.BackendType) {
	keywordArraySearchAttributeKey := "CustomKeywordArrayField"
	if backendType == service.BackendTypeCadence {
		keywordArraySearchAttributeKey = "CustomKeywordField"
	}
	workerHandler := wf_state_options_search_attributes_loading.NewHandler(keywordArraySearchAttributeKey)
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowId := wf_state_options_search_attributes_loading.WorkflowType + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           wf_state_options_search_attributes_loading.WorkflowType,
		FlowTimeoutSeconds: 10,

		StartStepType:    wf_state_options_search_attributes_loading.State1,
		StepInput:        objJSONValue(`"PARTIAL_WITHOUT_LOCKING"`),
		FlowStartOptions: withWorkerTarget(nil, workerTarget),
	})
	require.NoError(t, err)

	waitResponse, err := flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowId})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, waitResponse.GetFlowStatus())

	history := workerHandler.GetTestResult().InvokeHistory
	require.Equal(t, map[string]int64{
		"S1_waitFor": 1,
		"S1_execute": 1,
		"S2_waitFor": 1,
		"S2_execute": 1,
		"S3_waitFor": 1,
		"S3_execute": 1,
		"S4_waitFor": 1,
		"S4_execute": 1,
		"S5_waitFor": 1,
		"S5_execute": 1,
	}, history)
}
