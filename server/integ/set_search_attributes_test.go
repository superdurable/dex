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

func TestSetSearchAttributes(t *testing.T) {
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

	searchAttributes := []*dexpb.AttributeWrite{
		indexedIntAttribute(
			persistence.TestSearchAttributeIntKey,
			persistence.TestSearchAttributeIntValue1,
		),
		indexedKeywordAttribute(
			persistence.TestSearchAttributeKeywordKey,
			persistence.TestSearchAttributeKeywordValue1,
		),
		indexedKeywordArrayAttribute(
			persistence.TestSearchAttributeKeywordArrayKey,
			persistence.TestSearchAttributeKeywordValue2,
			persistence.TestSearchAttributeKeywordValue1,
		),
	}

	_, err = flowClient.SetAttributes(ctx, &dexpb.SetAttributesRequest{
		RequestId:  newRequestID(),
		FlowId:     flowId,
		Attributes: searchAttributes,
	})
	require.NoError(t, err)

	time.Sleep(time.Second)

	searchResult, err := flowClient.GetAttributes(ctx, &dexpb.GetAttributesRequest{
		FlowId: flowId,
		Keys: []string{
			persistence.TestSearchAttributeIntKey,
			persistence.TestSearchAttributeKeywordKey,
			persistence.TestSearchAttributeKeywordArrayKey,
		},
	})
	require.NoError(t, err)

	expected := []*dexpb.KV{
		{Key: persistence.TestSearchAttributeIntKey, Value: searchAttributes[0].GetValue()},
		{Key: persistence.TestSearchAttributeKeywordKey, Value: searchAttributes[1].GetValue()},
		{Key: persistence.TestSearchAttributeKeywordArrayKey, Value: searchAttributes[2].GetValue()},
	}
	require.Len(t, searchResult.GetAttributes(), len(expected))
	for _, want := range expected {
		found := false
		for _, got := range searchResult.GetAttributes() {
			if got.GetKey() == want.GetKey() && proto.Equal(got.GetValue(), want.GetValue()) {
				found = true
				break
			}
		}
		require.True(t, found, "missing attribute %s", want.GetKey())
	}

	// Describe reads Temporal indexed fields, not workflow persistence.
	desc, err := runtime.UnifiedClient.DescribeWorkflowExecution(
		ctx,
		flowId,
		"",
		map[string]dexpb.IndexType{
			persistence.TestSearchAttributeIntKey:          dexpb.IndexType_INDEX_TYPE_INT,
			persistence.TestSearchAttributeKeywordKey:      dexpb.IndexType_INDEX_TYPE_KEYWORD,
			persistence.TestSearchAttributeKeywordArrayKey: dexpb.IndexType_INDEX_TYPE_KEYWORD_ARRAY,
		},
	)
	require.NoError(t, err)
	for _, want := range expected {
		got, ok := desc.IndexedAttributes[want.GetKey()]
		require.True(t, ok, "missing backend indexed attribute %s", want.GetKey())
		require.True(t, proto.Equal(got, want.GetValue()), "backend mismatch for %s", want.GetKey())
	}

	_, err = flowClient.StopFlow(ctx, &dexpb.StopFlowRequest{
		FlowId:   flowId,
		StopType: dexpb.StopType_STOP_TYPE_TERMINATE,
	})
	require.NoError(t, err)
}
