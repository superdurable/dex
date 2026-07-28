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
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/iwf/gen/iwfpb"
	"github.com/superdurable/iwf/integ/workflow/signal"
	"github.com/superdurable/iwf/service"
	"github.com/superdurable/iwf/service/common/ptr"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const waitForAttributeBlobKey = "wait-for-attribute-blob-key"

func TestWaitForAttributeBlobBackedTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestWaitForAttributeBlobBacked(t)
		smallWaitForFastTest()
	}
}

func TestWaitForAttributeCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestWaitForAttributeCadenceUnimplemented(t)
		smallWaitForFastTest()
	}
}

func doTestWaitForAttributeBlobBacked(t *testing.T) {
	workerTarget := startWorker(t, signal.NewHandler())
	runtime := startIwfService(t, IwfServiceTestConfig{
		BackendType:     service.BackendTypeTemporal,
		S3TestThreshold: 50,
		LazyLoading:     ptr.Any(true),
	})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowId := "wait-for-attribute-" + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &iwfpb.StartFlowRequest{
		FlowId:             flowId,
		FlowType:           signal.WorkflowType,
		FlowTimeoutSeconds: 30,
		WorkerTarget:       workerTarget,
		StartStepType:      signal.State1,
	})
	require.NoError(t, err)

	largeValue := stringValue(strings.Repeat("x", 120))
	_, err = flowClient.SetAttributes(ctx, &iwfpb.SetAttributesRequest{
		FlowId: flowId,
		Attributes: []*iwfpb.AttributeWrite{
			{Key: waitForAttributeBlobKey, Value: largeValue},
		},
	})
	require.NoError(t, err)

	_, err = flowClient.WaitForAttribute(ctx, &iwfpb.WaitForAttributeRequest{
		FlowId: flowId,
		Condition: &iwfpb.WaitForAttributeCondition{
			Kind: &iwfpb.WaitForAttributeCondition_Equal{
				Equal: &iwfpb.WaitForAttributeEqual{
					Key:   waitForAttributeBlobKey,
					Value: stringValue("anything"),
				},
			},
		},
		WaitTimeSeconds: 0,
	})
	require.Error(t, err)
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	errResp := grpcErrorResponse(t, err)
	require.Equal(
		t,
		iwfpb.ErrorSubStatus_ERROR_SUB_STATUS_UNCATEGORIZED,
		errResp.GetSubStatus(),
	)
	require.Contains(t, errResp.GetDetail(), "blob-backed")
	require.Contains(t, errResp.GetDetail(), waitForAttributeBlobKey)

	_, err = flowClient.StopFlow(ctx, &iwfpb.StopFlowRequest{
		FlowId:   flowId,
		StopType: iwfpb.StopType_STOP_TYPE_TERMINATE,
	})
	require.NoError(t, err)
}

func doTestWaitForAttributeCadenceUnimplemented(t *testing.T) {
	workerTarget := startWorker(t, signal.NewHandler())
	runtime := startIwfService(t, IwfServiceTestConfig{BackendType: service.BackendTypeCadence})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowId := "wait-for-attribute-cadence-" + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &iwfpb.StartFlowRequest{
		FlowId:             flowId,
		FlowType:           signal.WorkflowType,
		FlowTimeoutSeconds: 20,
		WorkerTarget:       workerTarget,
		StartStepType:      signal.State1,
	})
	require.NoError(t, err)

	_, err = flowClient.WaitForAttribute(ctx, &iwfpb.WaitForAttributeRequest{
		FlowId: flowId,
		Condition: &iwfpb.WaitForAttributeCondition{
			Kind: &iwfpb.WaitForAttributeCondition_Equal{
				Equal: &iwfpb.WaitForAttributeEqual{
					Key:   waitForAttributeBlobKey,
					Value: stringValue("anything"),
				},
			},
		},
		WaitTimeSeconds: 1,
	})
	require.Equal(t, codes.Unimplemented, status.Code(err))

	_, err = flowClient.StopFlow(ctx, &iwfpb.StopFlowRequest{
		FlowId:   flowId,
		StopType: iwfpb.StopType_STOP_TYPE_TERMINATE,
	})
	require.NoError(t, err)
}
