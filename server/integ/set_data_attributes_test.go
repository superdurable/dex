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
	"github.com/superdurable/dex/integ/workflow/persistence"
	"github.com/superdurable/dex/integ/workflow/signal"
	"github.com/superdurable/dex/service"
	"google.golang.org/protobuf/proto"
)

func TestSetDataAttributesTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}

	workerHandler := signal.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, DexServiceTestConfig{
		BackendType: service.BackendTypeTemporal,
	})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowId := signal.WorkflowType + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           signal.WorkflowType,
		FlowTimeoutSeconds: 10,

		StartStepType:    signal.State1,
		FlowStartOptions: withWorkerTarget(nil, workerTarget),
	})
	require.NoError(t, err)

	smallDataAttributes := []*dexpb.AttributeWrite{
		dataObjectAttribute(persistence.TestDataAttributeKey, `"test-data-attribute-value1"`),
		dataObjectAttribute(persistence.TestDataAttributeKey2, `"test-data-attribute-value2"`),
	}

	_, err = flowClient.SetAttributes(ctx, &dexpb.SetAttributesRequest{
		RequestId:  newRequestID(),
		FlowId:     flowId,
		Attributes: smallDataAttributes,
	})
	require.NoError(t, err)

	time.Sleep(time.Second)

	getResult, err := flowClient.GetAttributes(ctx, &dexpb.GetAttributesRequest{
		FlowId: flowId,
		Keys: []string{
			persistence.TestDataAttributeKey,
			persistence.TestDataAttributeKey2,
		},
	})
	require.NoError(t, err)

	expected := []*dexpb.KV{
		{Key: persistence.TestDataAttributeKey, Value: smallDataAttributes[0].GetValue()},
		{Key: persistence.TestDataAttributeKey2, Value: smallDataAttributes[1].GetValue()},
	}
	require.Len(t, getResult.GetAttributes(), len(expected))
	for _, want := range expected {
		found := false
		for _, got := range getResult.GetAttributes() {
			if got.GetKey() == want.GetKey() && proto.Equal(got.GetValue(), want.GetValue()) {
				found = true
				break
			}
		}
		require.True(t, found, "missing attribute %s", want.GetKey())
	}

	_, err = flowClient.StopFlow(ctx, &dexpb.StopFlowRequest{
		FlowId:   flowId,
		StopType: dexpb.StopType_STOP_TYPE_TERMINATE,
	})
	require.NoError(t, err)
}
