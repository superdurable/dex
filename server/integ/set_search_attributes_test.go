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
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/iwf/gen/iwfpb"
	"github.com/superdurable/iwf/integ/workflow/persistence"
	"github.com/superdurable/iwf/integ/workflow/signal"
	"github.com/superdurable/iwf/service"
	"google.golang.org/protobuf/proto"
)

func TestSetSearchAttributes(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}

	workerHandler := signal.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startIwfService(t, IwfServiceTestConfig{
		BackendType: service.BackendTypeTemporal,
	})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowId := signal.WorkflowType + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &iwfpb.StartFlowRequest{
		FlowId:             flowId,
		FlowType:           signal.WorkflowType,
		FlowTimeoutSeconds: 10,
		WorkerTarget:       workerTarget,
		StartStepType:      signal.State1,
	})
	require.NoError(t, err)

	searchAttributes := []*iwfpb.AttributeWrite{
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

	_, err = flowClient.SetAttributes(ctx, &iwfpb.SetAttributesRequest{
		FlowId:     flowId,
		Attributes: searchAttributes,
	})
	require.NoError(t, err)

	time.Sleep(time.Second)

	searchResult, err := flowClient.GetAttributes(ctx, &iwfpb.GetAttributesRequest{
		FlowId: flowId,
		Keys: []string{
			persistence.TestSearchAttributeIntKey,
			persistence.TestSearchAttributeKeywordKey,
			persistence.TestSearchAttributeKeywordArrayKey,
		},
	})
	require.NoError(t, err)

	expected := []*iwfpb.KV{
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

	_, err = flowClient.StopFlow(ctx, &iwfpb.StopFlowRequest{
		FlowId:   flowId,
		StopType: iwfpb.StopType_STOP_TYPE_TERMINATE,
	})
	require.NoError(t, err)
}
