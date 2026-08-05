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
	"github.com/superdurable/dex/integ/workflow/basic"
	"github.com/superdurable/dex/service"
)

func TestStartFlowNoOptionsTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	doTestStartFlowWithoutStartOptions(t, service.BackendTypeTemporal)
}

func TestStartFlowNoOptionsCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	doTestStartFlowWithoutStartOptions(t, service.BackendTypeCadence)
}

func doTestStartFlowWithoutStartOptions(t *testing.T, backendType service.BackendType) {
	workerTarget := startWorker(t, basic.NewHandler())
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowId := "TestStartFlowWithoutStartOptions-" + uuid.NewString()
	stepInput := encodedObjectValue("json", []byte("test data"))
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           basic.FlowType,
		FlowTimeoutSeconds: 0,

		StartStepType:    basic.Step1,
		StepInput:        stepInput,
		FlowStartOptions: withWorkerTarget(nil, workerTarget),
	})
	require.NoError(t, err)

	response, err := runtime.UnifiedClient.DescribeWorkflowExecution(
		ctx,
		flowId,
		"",
		map[string]dexpb.IndexType{
			service.SearchAttributeDexWorkflowType: dexpb.IndexType_INDEX_TYPE_KEYWORD,
		},
	)
	require.NoError(t, err)
	attribute := response.IndexedAttributes[service.SearchAttributeDexWorkflowType]
	require.Equal(t, basic.FlowType, attribute.GetStringValue())

	_, err = flowClient.StopFlow(ctx, &dexpb.StopFlowRequest{
		FlowId:   flowId,
		StopType: dexpb.StopType_STOP_TYPE_TERMINATE,
	})
	require.NoError(t, err)
}
