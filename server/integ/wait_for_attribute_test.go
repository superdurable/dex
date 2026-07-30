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
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/integ/workflow/signal"
	"github.com/superdurable/dex/service"
	"github.com/superdurable/dex/service/common/ptr"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	waitForAttributeBlobKey = "wait-for-attribute-blob-key"
	waitForAttributeKey     = "wait-for-attribute-key"
)

func TestWaitForAttributeBlobBackedTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestWaitForAttributeBlobBacked(t)
		smallWaitForFastTest()
	}
}

func TestWaitForAttributeTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestWaitForAttributeSuccess(t)
		smallWaitForFastTest()
		doTestWaitForAttributeTimeout(t)
		smallWaitForFastTest()
		doTestWaitForAttributeCancel(t)
		smallWaitForFastTest()
		doTestWaitForAttributeNotFound(t)
		smallWaitForFastTest()
		doTestWaitForAttributeClosed(t)
		smallWaitForFastTest()
		doTestWaitForAttributeConcurrent(t)
		smallWaitForFastTest()
		doTestWaitForAttributeAcrossContinueAsNew(t)
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
	runtime := startDexService(t, DexServiceTestConfig{
		BackendType:     service.BackendTypeTemporal,
		S3TestThreshold: 50,
		LazyLoading:     ptr.Any(true),
	})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowId := "wait-for-attribute-" + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           signal.WorkflowType,
		FlowTimeoutSeconds: 30,

		StartStepType:    signal.State1,
		FlowStartOptions: withWorkerTarget(nil, workerTarget),
	})
	require.NoError(t, err)

	largeValue := stringValue(strings.Repeat("x", 120))
	_, err = flowClient.SetAttributes(ctx, &dexpb.SetAttributesRequest{
		RequestId: newRequestID(),
		FlowId:    flowId,
		Attributes: []*dexpb.AttributeWrite{
			{Key: waitForAttributeBlobKey, Value: largeValue},
		},
	})
	require.NoError(t, err)

	_, err = flowClient.WaitForAttribute(ctx, &dexpb.WaitForAttributeRequest{
		FlowId: flowId,
		Condition: waitForAttributeEqualCondition(
			waitForAttributeBlobKey,
			stringValue("anything"),
		),
		WaitTimeSeconds: 0,
		RequestId:       uuid.NewString(),
	})
	require.Error(t, err)
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	errResp := grpcErrorResponse(t, err)
	require.Equal(
		t,
		dexpb.ErrorSubStatus_ERROR_SUB_STATUS_UNCATEGORIZED,
		errResp.GetSubStatus(),
	)
	require.Contains(t, errResp.GetDetail(), "blob-backed")
	require.Contains(t, errResp.GetDetail(), waitForAttributeBlobKey)

	_, err = flowClient.StopFlow(ctx, &dexpb.StopFlowRequest{
		FlowId:   flowId,
		StopType: dexpb.StopType_STOP_TYPE_TERMINATE,
	})
	require.NoError(t, err)
}

func doTestWaitForAttributeSuccess(t *testing.T) {
	workerTarget := startWorker(t, signal.NewHandler())
	runtime := startDexService(t, DexServiceTestConfig{BackendType: service.BackendTypeTemporal})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowId := startParkedWaitForAttributeFlow(t, ctx, flowClient, workerTarget, nil)
	expectedValue := stringValue("wait-for-attribute-success")

	_, err := flowClient.WaitForAttribute(ctx, &dexpb.WaitForAttributeRequest{
		FlowId: flowId,
		Condition: waitForAttributeEqualCondition(
			waitForAttributeKey,
			expectedValue,
		),
		WaitTimeSeconds: 0,
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Equal(t, "request ID is required", grpcErrorResponse(t, err).GetDetail())

	_, err = flowClient.SetAttributes(ctx, &dexpb.SetAttributesRequest{
		RequestId: newRequestID(),
		FlowId:    flowId,
		Attributes: []*dexpb.AttributeWrite{
			{Key: waitForAttributeKey, Value: expectedValue},
		},
	})
	require.NoError(t, err)

	_, err = flowClient.WaitForAttribute(ctx, &dexpb.WaitForAttributeRequest{
		FlowId: flowId,
		Condition: waitForAttributeEqualCondition(
			waitForAttributeKey,
			expectedValue,
		),
		WaitTimeSeconds: 0,
		RequestId:       uuid.NewString(),
	})
	require.NoError(t, err)

	stopParkedWaitForAttributeFlow(t, ctx, flowClient, flowId)
}

func doTestWaitForAttributeTimeout(t *testing.T) {
	workerTarget := startWorker(t, signal.NewHandler())
	runtime := startDexService(t, DexServiceTestConfig{BackendType: service.BackendTypeTemporal})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowId := startParkedWaitForAttributeFlow(t, ctx, flowClient, workerTarget, nil)

	_, err := flowClient.WaitForAttribute(ctx, &dexpb.WaitForAttributeRequest{
		FlowId: flowId,
		Condition: waitForAttributeEqualCondition(
			waitForAttributeKey,
			stringValue("never-set"),
		),
		WaitTimeSeconds: 0,
		RequestId:       uuid.NewString(),
	})
	require.Error(t, err)
	require.Equal(t, codes.DeadlineExceeded, status.Code(err))
	errResp := grpcErrorResponse(t, err)
	require.Equal(
		t,
		dexpb.ErrorSubStatus_ERROR_SUB_STATUS_LONG_POLL_TIME_OUT,
		errResp.GetSubStatus(),
	)
	require.Equal(t, "attribute wait timed out", errResp.GetDetail())

	stopParkedWaitForAttributeFlow(t, ctx, flowClient, flowId)
}

func doTestWaitForAttributeCancel(t *testing.T) {
	workerTarget := startWorker(t, signal.NewHandler())
	runtime := startDexService(t, DexServiceTestConfig{BackendType: service.BackendTypeTemporal})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	flowId := startParkedWaitForAttributeFlow(t, ctx, flowClient, workerTarget, nil)

	done := make(chan error, 1)
	go func() {
		_, waitErr := flowClient.WaitForAttribute(ctx, &dexpb.WaitForAttributeRequest{
			FlowId: flowId,
			Condition: waitForAttributeEqualCondition(
				waitForAttributeKey,
				stringValue("never-set"),
			),
			WaitTimeSeconds: 30,
			RequestId:       uuid.NewString(),
		})
		done <- waitErr
	}()

	time.Sleep(500 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		require.Equal(t, codes.Canceled, status.Code(err))
	case <-time.After(10 * time.Second):
		t.Fatal("WaitForAttribute did not return after cancel")
	}

	stopParkedWaitForAttributeFlow(t, context.Background(), flowClient, flowId)
}

func doTestWaitForAttributeNotFound(t *testing.T) {
	runtime := startDexService(t, DexServiceTestConfig{BackendType: service.BackendTypeTemporal})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := flowClient.WaitForAttribute(ctx, &dexpb.WaitForAttributeRequest{
		FlowId: "wait-for-attribute-missing-" + uuid.NewString(),
		Condition: waitForAttributeEqualCondition(
			waitForAttributeKey,
			stringValue("anything"),
		),
		WaitTimeSeconds: 0,
		RequestId:       uuid.NewString(),
	})
	require.Equal(t, codes.NotFound, status.Code(err))
	require.Equal(
		t,
		dexpb.ErrorSubStatus_ERROR_SUB_STATUS_FLOW_NOT_EXISTS,
		grpcErrorResponse(t, err).GetSubStatus(),
	)
}

func doTestWaitForAttributeClosed(t *testing.T) {
	workerTarget := startWorker(t, signal.NewHandler())
	runtime := startDexService(t, DexServiceTestConfig{BackendType: service.BackendTypeTemporal})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowId := startParkedWaitForAttributeFlow(t, ctx, flowClient, workerTarget, nil)

	_, err := flowClient.StopFlow(ctx, &dexpb.StopFlowRequest{
		FlowId:   flowId,
		StopType: dexpb.StopType_STOP_TYPE_TERMINATE,
	})
	require.NoError(t, err)

	_, err = flowClient.WaitForAttribute(ctx, &dexpb.WaitForAttributeRequest{
		FlowId: flowId,
		Condition: waitForAttributeEqualCondition(
			waitForAttributeKey,
			stringValue("anything"),
		),
		WaitTimeSeconds: 0,
		RequestId:       uuid.NewString(),
	})
	require.Error(t, err)
	require.Equal(t, codes.NotFound, status.Code(err))
	require.Equal(
		t,
		dexpb.ErrorSubStatus_ERROR_SUB_STATUS_FLOW_NOT_EXISTS,
		grpcErrorResponse(t, err).GetSubStatus(),
	)
}

func doTestWaitForAttributeConcurrent(t *testing.T) {
	workerTarget := startWorker(t, signal.NewHandler())
	runtime := startDexService(t, DexServiceTestConfig{BackendType: service.BackendTypeTemporal})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowId := startParkedWaitForAttributeFlow(t, ctx, flowClient, workerTarget, nil)
	expectedValue := stringValue("wait-for-attribute-concurrent")
	requestId := uuid.NewString()
	description, err := runtime.UnifiedClient.DescribeWorkflowExecution(ctx, flowId, "", nil)
	require.NoError(t, err)

	var waitGroup sync.WaitGroup
	errors := make([]error, 2)
	for index := range errors {
		waitGroup.Add(1)
		go func(resultIndex int) {
			defer waitGroup.Done()
			_, waitErr := flowClient.WaitForAttribute(ctx, &dexpb.WaitForAttributeRequest{
				FlowId: flowId,
				Condition: waitForAttributeEqualCondition(
					waitForAttributeKey,
					expectedValue,
				),
				WaitTimeSeconds: 30,
				RequestId:       requestId,
			})
			errors[resultIndex] = waitErr
		}(index)
	}

	time.Sleep(500 * time.Millisecond)

	_, err = flowClient.SetAttributes(ctx, &dexpb.SetAttributesRequest{
		RequestId: newRequestID(),
		FlowId:    flowId,
		Attributes: []*dexpb.AttributeWrite{
			{Key: waitForAttributeKey, Value: expectedValue},
		},
	})
	require.NoError(t, err)

	waitGroup.Wait()
	for _, waitErr := range errors {
		require.NoError(t, waitErr)
	}
	accepted, completed := countTemporalUpdateEvents(
		t,
		ctx,
		runtime,
		flowId,
		description.RunId,
		requestId,
	)
	require.Equal(t, 1, accepted)
	require.Equal(t, 1, completed)

	stopParkedWaitForAttributeFlow(t, ctx, flowClient, flowId)
}

func doTestWaitForAttributeAcrossContinueAsNew(t *testing.T) {
	workerTarget := startWorker(t, signal.NewHandler())
	runtime := startDexService(t, DexServiceTestConfig{BackendType: service.BackendTypeTemporal})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowId := startParkedWaitForAttributeFlow(
		t,
		ctx,
		flowClient,
		workerTarget,
		minimumContinueAsNewSyncDurabilityConfig(),
	)
	expectedValue := stringValue("wait-for-attribute-can")

	go func() {
		time.Sleep(500 * time.Millisecond)
		_, setErr := flowClient.SetAttributes(context.Background(), &dexpb.SetAttributesRequest{
			RequestId: newRequestID(),
			FlowId:    flowId,
			Attributes: []*dexpb.AttributeWrite{
				{Key: waitForAttributeKey, Value: expectedValue},
			},
		})
		if setErr != nil {
			t.Logf("Warning: failed to set attribute during CAN wait: %v", setErr)
		}
	}()

	_, err := flowClient.WaitForAttribute(ctx, &dexpb.WaitForAttributeRequest{
		FlowId: flowId,
		Condition: waitForAttributeEqualCondition(
			waitForAttributeKey,
			expectedValue,
		),
		WaitTimeSeconds: 30,
		RequestId:       uuid.NewString(),
	})
	require.NoError(t, err)

	stopParkedWaitForAttributeFlow(t, ctx, flowClient, flowId)
}

func doTestWaitForAttributeCadenceUnimplemented(t *testing.T) {
	workerTarget := startWorker(t, signal.NewHandler())
	runtime := startDexService(t, DexServiceTestConfig{BackendType: service.BackendTypeCadence})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowId := "wait-for-attribute-cadence-" + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           signal.WorkflowType,
		FlowTimeoutSeconds: 20,

		StartStepType:    signal.State1,
		FlowStartOptions: withWorkerTarget(nil, workerTarget),
	})
	require.NoError(t, err)

	_, err = flowClient.WaitForAttribute(ctx, &dexpb.WaitForAttributeRequest{
		FlowId: flowId,
		Condition: waitForAttributeEqualCondition(
			waitForAttributeBlobKey,
			stringValue("anything"),
		),
		WaitTimeSeconds: 1,
	})
	require.Equal(t, codes.Unimplemented, status.Code(err))

	_, err = flowClient.StopFlow(ctx, &dexpb.StopFlowRequest{
		FlowId:   flowId,
		StopType: dexpb.StopType_STOP_TYPE_TERMINATE,
	})
	require.NoError(t, err)
}

func waitForAttributeEqualCondition(
	key string,
	value *dexpb.Value,
) *dexpb.WaitForAttributeCondition {
	return &dexpb.WaitForAttributeCondition{
		Kind: &dexpb.WaitForAttributeCondition_Equal{
			Equal: &dexpb.WaitForAttributeEqual{
				Key:   key,
				Value: value,
			},
		},
	}
}

func startParkedWaitForAttributeFlow(
	t *testing.T,
	ctx context.Context,
	flowClient dexpb.FlowServiceClient,
	workerTarget *dexpb.WorkerTarget,
	flowConfig *dexpb.FlowConfig,
) string {
	t.Helper()

	flowId := "wait-for-attribute-" + uuid.NewString()
	startRequest := &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           signal.WorkflowType,
		FlowTimeoutSeconds: 30,

		StartStepType:    signal.State1,
		FlowStartOptions: withWorkerTarget(nil, workerTarget),
	}
	if flowConfig != nil {
		startRequest.FlowStartOptions = withWorkerTarget(&dexpb.FlowStartOptions{
			FlowConfigOverride: flowConfig,
		}, workerTarget)
	}
	_, err := flowClient.StartFlow(ctx, startRequest)
	require.NoError(t, err)
	return flowId
}

func stopParkedWaitForAttributeFlow(
	t *testing.T,
	ctx context.Context,
	flowClient dexpb.FlowServiceClient,
	flowId string,
) {
	t.Helper()

	_, err := flowClient.StopFlow(ctx, &dexpb.StopFlowRequest{
		FlowId:   flowId,
		StopType: dexpb.StopType_STOP_TYPE_TERMINATE,
	})
	require.NoError(t, err)
}
