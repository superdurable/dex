// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package integ

import (
	"context"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex/ptr"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/server-zd/integ/workflow/signal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const (
	waitForAttributeBlobKey = "wait-for-attribute-blob-key"
	waitForAttributeKey     = "wait-for-attribute-key"
)

func TestWaitForAttributeBlobBackedTemporal(t *testing.T) {
	for i := 0; i < *repeatIntegTest; i++ {
		doTestWaitForAttributeBlobBacked(t)
		smallWaitForFastTest()
	}
}

func TestWaitForAttributeTemporal(t *testing.T) {
	for i := 0; i < *repeatIntegTest; i++ {
		doTestWaitForAttributeSuccess(t)
		smallWaitForFastTest()
		doTestWaitForAttributeOperators(t)
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
		// These cover Temporal Update handlers, Update counters, Continue-As-New (C3), and two Servers
		// sharing one Flow, which binary mode cannot start.

		continue

	}
}

func doTestWaitForAttributeBlobBacked(t *testing.T) {
	workerTarget := startWorker(t, signal.NewHandler())
	runtime := startDexService(t, serverZDTestConfig{

		ObjectPayloadThresholdBytes: 50,
		LazyLoading:                 ptr.Any(true),
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
	require.Eventually(t, func() bool {
		response, getErr := flowClient.GetAttributes(ctx, &dexpb.GetAttributesRequest{
			FlowId: flowId,
			Keys:   []string{waitForAttributeBlobKey},
		})
		return getErr == nil && len(response.GetAttributes()) == 1
	}, 5*time.Second, 50*time.Millisecond)

	_, err = flowClient.WaitForAttribute(ctx, &dexpb.WaitForAttributeRequest{
		FlowId: flowId,
		Match: equalAttributeMatch(
			waitForAttributeBlobKey,
			stringValue("anything"),
		),
		RequestTimeoutSeconds: 0,
		RequestId:             uuid.NewString(),
	})
	require.Error(t, err)
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	errResp := grpcServiceErrorResponse(t, err)
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
	runtime := startDexService(t, serverZDTestConfig{})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowId := startParkedWaitForAttributeFlow(t, ctx, flowClient, workerTarget, nil)
	expectedValue := stringValue("wait-for-attribute-success")

	_, err := flowClient.SetAttributes(ctx, &dexpb.SetAttributesRequest{
		RequestId: newRequestID(),
		FlowId:    flowId,
		Attributes: []*dexpb.AttributeWrite{
			{Key: waitForAttributeKey, Value: expectedValue},
		},
	})
	require.NoError(t, err)

	response, err := flowClient.WaitForAttribute(ctx, &dexpb.WaitForAttributeRequest{
		FlowId: flowId,
		Match: equalAttributeMatch(
			waitForAttributeKey,
			expectedValue,
		),
		RequestTimeoutSeconds: 0,
	})
	require.NoError(t, err)
	require.True(t, proto.Equal(expectedValue, response.GetMatchedValue()))

	_, err = flowClient.SetAttributes(ctx, &dexpb.SetAttributesRequest{
		RequestId: newRequestID(),
		FlowId:    flowId,
		Attributes: []*dexpb.AttributeWrite{
			{Key: waitForAttributeKey, Value: intValue(1)},
		},
	})
	require.NoError(t, err)
	nonEqualResponse := make(chan *dexpb.WaitForAttributeResponse, 1)
	nonEqualError := make(chan error, 1)
	go func() {
		matchedResponse, waitErr := flowClient.WaitForAttribute(ctx, &dexpb.WaitForAttributeRequest{
			FlowId: flowId,
			Match: waitForAttributeMatch(
				waitForAttributeKey,
				dexpb.AttributeMatchOperator_ATTRIBUTE_MATCH_OPERATOR_GREATER_THAN,
				intValue(5),
			),
			RequestTimeoutSeconds: 0,
		})
		nonEqualResponse <- matchedResponse
		nonEqualError <- waitErr
	}()
	_, err = flowClient.SetAttributes(ctx, &dexpb.SetAttributesRequest{
		RequestId: newRequestID(),
		FlowId:    flowId,
		Attributes: []*dexpb.AttributeWrite{
			{Key: waitForAttributeKey, Value: intValue(7)},
		},
	})
	require.NoError(t, err)
	require.NoError(t, <-nonEqualError)
	require.True(t, proto.Equal(intValue(7), (<-nonEqualResponse).GetMatchedValue()))

	stopParkedWaitForAttributeFlow(t, ctx, flowClient, flowId)
}

func doTestWaitForAttributeOperators(t *testing.T) {
	workerTarget := startWorker(t, signal.NewHandler())
	runtime := startDexService(t, serverZDTestConfig{})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowId := startParkedWaitForAttributeFlow(t, ctx, flowClient, workerTarget, nil)
	valuesByKey := map[string]*dexpb.Value{
		"match-string":    stringValue("ready"),
		"match-bool":      boolValue(true),
		"match-int":       intValue(3),
		"match-double":    doubleValue(3.5),
		"match-object":    jsonObjValue(map[string]string{"state": "ready"}),
		"match-nonfinite": doubleValue(math.Inf(1)),
	}
	writes := make([]*dexpb.AttributeWrite, 0, len(valuesByKey))
	for key, value := range valuesByKey {
		writes = append(writes, &dexpb.AttributeWrite{Key: key, Value: value})
	}
	_, err := flowClient.SetAttributes(ctx, &dexpb.SetAttributesRequest{
		RequestId:  newRequestID(),
		FlowId:     flowId,
		Attributes: writes,
	})
	require.NoError(t, err)

	testCases := []struct {
		name     string
		key      string
		operator dexpb.AttributeMatchOperator
		operand  *dexpb.Value
	}{
		{name: "string equal", key: "match-string", operator: dexpb.AttributeMatchOperator_ATTRIBUTE_MATCH_OPERATOR_EQUAL, operand: stringValue("ready")},
		{name: "string not equal", key: "match-string", operator: dexpb.AttributeMatchOperator_ATTRIBUTE_MATCH_OPERATOR_NOT_EQUAL, operand: stringValue("pending")},
		{name: "bool equal", key: "match-bool", operator: dexpb.AttributeMatchOperator_ATTRIBUTE_MATCH_OPERATOR_EQUAL, operand: boolValue(true)},
		{name: "bool not equal", key: "match-bool", operator: dexpb.AttributeMatchOperator_ATTRIBUTE_MATCH_OPERATOR_NOT_EQUAL, operand: boolValue(false)},
		{name: "int equal", key: "match-int", operator: dexpb.AttributeMatchOperator_ATTRIBUTE_MATCH_OPERATOR_EQUAL, operand: intValue(3)},
		{name: "int not equal", key: "match-int", operator: dexpb.AttributeMatchOperator_ATTRIBUTE_MATCH_OPERATOR_NOT_EQUAL, operand: intValue(2)},
		{name: "int greater", key: "match-int", operator: dexpb.AttributeMatchOperator_ATTRIBUTE_MATCH_OPERATOR_GREATER_THAN, operand: intValue(0)},
		{name: "int greater equal", key: "match-int", operator: dexpb.AttributeMatchOperator_ATTRIBUTE_MATCH_OPERATOR_GREATER_THAN_OR_EQUAL, operand: intValue(3)},
		{name: "int less", key: "match-int", operator: dexpb.AttributeMatchOperator_ATTRIBUTE_MATCH_OPERATOR_LESS_THAN, operand: intValue(4)},
		{name: "int less equal", key: "match-int", operator: dexpb.AttributeMatchOperator_ATTRIBUTE_MATCH_OPERATOR_LESS_THAN_OR_EQUAL, operand: intValue(3)},
		{name: "double equal", key: "match-double", operator: dexpb.AttributeMatchOperator_ATTRIBUTE_MATCH_OPERATOR_EQUAL, operand: doubleValue(3.5)},
		{name: "double not equal", key: "match-double", operator: dexpb.AttributeMatchOperator_ATTRIBUTE_MATCH_OPERATOR_NOT_EQUAL, operand: doubleValue(2.5)},
		{name: "double greater", key: "match-double", operator: dexpb.AttributeMatchOperator_ATTRIBUTE_MATCH_OPERATOR_GREATER_THAN, operand: doubleValue(3.0)},
		{name: "double greater equal", key: "match-double", operator: dexpb.AttributeMatchOperator_ATTRIBUTE_MATCH_OPERATOR_GREATER_THAN_OR_EQUAL, operand: doubleValue(3.5)},
		{name: "double less", key: "match-double", operator: dexpb.AttributeMatchOperator_ATTRIBUTE_MATCH_OPERATOR_LESS_THAN, operand: doubleValue(4.0)},
		{name: "double less equal", key: "match-double", operator: dexpb.AttributeMatchOperator_ATTRIBUTE_MATCH_OPERATOR_LESS_THAN_OR_EQUAL, operand: doubleValue(3.5)},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			response, waitErr := flowClient.WaitForAttribute(ctx, &dexpb.WaitForAttributeRequest{
				FlowId:                flowId,
				Match:                 waitForAttributeMatch(testCase.key, testCase.operator, testCase.operand),
				RequestTimeoutSeconds: 0,
				RequestId:             uuid.NewString(),
			})
			require.NoError(t, waitErr)
			require.True(t, proto.Equal(valuesByKey[testCase.key], response.GetMatchedValue()))
		})
	}

	invalidMatches := []struct {
		name  string
		match *dexpb.AttributeMatch
	}{
		{name: "unspecified", match: waitForAttributeMatch("match-int", dexpb.AttributeMatchOperator_ATTRIBUTE_MATCH_OPERATOR_UNSPECIFIED, intValue(0))},
		{name: "string ordering", match: waitForAttributeMatch("match-string", dexpb.AttributeMatchOperator_ATTRIBUTE_MATCH_OPERATOR_GREATER_THAN, stringValue("a"))},
		{name: "object", match: equalAttributeMatch("match-string", &dexpb.Value{Kind: &dexpb.Value_ObjValue{ObjValue: &dexpb.EncodedObject{Encoding: "json", Payload: []byte("{}")}}})},
		{name: "null", match: equalAttributeMatch("match-string", &dexpb.Value{Kind: &dexpb.Value_NullValue{}})},
		{name: "non-finite", match: equalAttributeMatch("match-double", doubleValue(math.Inf(1)))},
	}
	for _, testCase := range invalidMatches {
		t.Run(testCase.name, func(t *testing.T) {
			_, waitErr := flowClient.WaitForAttribute(ctx, &dexpb.WaitForAttributeRequest{
				FlowId:                flowId,
				Match:                 testCase.match,
				RequestTimeoutSeconds: 0,
				RequestId:             uuid.NewString(),
			})
			require.Equal(t, codes.InvalidArgument, status.Code(waitErr))
		})
	}

	for _, waitRequest := range []*dexpb.WaitForAttributeRequest{
		{
			FlowId:                flowId,
			Match:                 equalAttributeMatch("match-string", stringValue("ready")),
			RequestTimeoutSeconds: -1,
		},
		{
			FlowId:                        flowId,
			Match:                         equalAttributeMatch("match-string", stringValue("ready")),
			InternalHandlerTimeoutSeconds: -1,
		},
	} {
		_, waitErr := flowClient.WaitForAttribute(ctx, waitRequest)
		require.Equal(t, codes.InvalidArgument, status.Code(waitErr))
	}

	invalidStoredValues := []struct {
		name    string
		key     string
		operand *dexpb.Value
	}{
		{name: "stored object", key: "match-object", operand: stringValue("ready")},
		{name: "stored non-finite", key: "match-nonfinite", operand: doubleValue(0)},
	}
	for _, testCase := range invalidStoredValues {
		t.Run(testCase.name, func(t *testing.T) {
			_, waitErr := flowClient.WaitForAttribute(ctx, &dexpb.WaitForAttributeRequest{
				FlowId:                flowId,
				Match:                 equalAttributeMatch(testCase.key, testCase.operand),
				RequestTimeoutSeconds: 0,
				RequestId:             uuid.NewString(),
			})
			require.Equal(t, codes.FailedPrecondition, status.Code(waitErr))
		})
	}

	_, err = flowClient.WaitForAttribute(ctx, &dexpb.WaitForAttributeRequest{
		FlowId: flowId,
		Match: waitForAttributeMatch(
			"match-string",
			dexpb.AttributeMatchOperator_ATTRIBUTE_MATCH_OPERATOR_GREATER_THAN,
			intValue(0),
		),
		RequestTimeoutSeconds: 1,
		RequestId:             uuid.NewString(),
	})
	require.Equal(t, codes.DeadlineExceeded, status.Code(err))
	require.Equal(
		t,
		dexpb.ErrorSubStatus_ERROR_SUB_STATUS_REQUEST_TIMEOUT,
		grpcServiceErrorResponse(t, err).GetSubStatus(),
	)

	stopParkedWaitForAttributeFlow(t, ctx, flowClient, flowId)
}

func doTestWaitForAttributeTimeout(t *testing.T) {
	workerTarget := startWorker(t, signal.NewHandler())
	runtime := startDexService(t, serverZDTestConfig{})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowId := startParkedWaitForAttributeFlow(t, ctx, flowClient, workerTarget, nil)

	waitRequest := &dexpb.WaitForAttributeRequest{
		FlowId: flowId,
		Match: equalAttributeMatch(
			waitForAttributeKey,
			stringValue("never-set"),
		),
		RequestTimeoutSeconds: 1,
	}
	_, err := flowClient.WaitForAttribute(ctx, waitRequest)
	require.Error(t, err)
	require.Equal(t, codes.DeadlineExceeded, status.Code(err))
	errResp := grpcServiceErrorResponse(t, err)
	require.Equal(
		t,
		dexpb.ErrorSubStatus_ERROR_SUB_STATUS_REQUEST_TIMEOUT,
		errResp.GetSubStatus(),
	)
	require.Equal(t, "attribute request timed out", errResp.GetDetail())

	waitRequest.RequestTimeoutSeconds = 10
	responseChannel := make(chan *dexpb.WaitForAttributeResponse, 1)
	errorChannel := make(chan error, 1)
	go func() {
		response, waitErr := flowClient.WaitForAttribute(ctx, waitRequest)
		responseChannel <- response
		errorChannel <- waitErr
	}()
	_, err = flowClient.SetAttributes(ctx, &dexpb.SetAttributesRequest{
		RequestId: newRequestID(),
		FlowId:    flowId,
		Attributes: []*dexpb.AttributeWrite{
			{Key: waitForAttributeKey, Value: stringValue("never-set")},
		},
	})
	require.NoError(t, err)
	require.NoError(t, <-errorChannel)
	require.True(t, proto.Equal(stringValue("never-set"), (<-responseChannel).GetMatchedValue()))

	stopParkedWaitForAttributeFlow(t, ctx, flowClient, flowId)
}

func doTestWaitForAttributeCancel(t *testing.T) {
	workerTarget := startWorker(t, signal.NewHandler())
	runtime := startDexService(t, serverZDTestConfig{

		// One waiter slot lets a probe observe server-zd waiter registration.
		ServerZDMaxLongPollWaiters: 1,
	})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	flowId := startParkedWaitForAttributeFlow(t, ctx, flowClient, workerTarget, nil)
	requestID := uuid.NewString()

	done := make(chan error, 1)
	go func() {
		for {
			_, waitErr := flowClient.WaitForAttribute(ctx, &dexpb.WaitForAttributeRequest{
				FlowId: flowId,
				Match: equalAttributeMatch(
					waitForAttributeKey,
					stringValue("never-set"),
				),
				RequestTimeoutSeconds: 30,
				RequestId:             requestID,
			})
			// A probe can hold the only server-zd waiter slot for one state read.
			if status.Code(waitErr) == codes.ResourceExhausted {
				continue
			}
			done <- waitErr
			return
		}
	}()

	requireServerZDWaiterProbeCode(t, flowClient, codes.ResourceExhausted, done)

	cancel()

	select {
	case err := <-done:
		require.Equal(t, codes.Canceled, status.Code(err))
	case <-time.After(10 * time.Second):
		t.Fatal("WaitForAttribute did not return after cancel")
	}

	// The cancelled waiter must release its server-zd slot.
	requireServerZDWaiterProbeCode(t, flowClient, codes.NotFound, nil)

	stopParkedWaitForAttributeFlow(t, context.Background(), flowClient, flowId)
}

func doTestWaitForAttributeNotFound(t *testing.T) {
	runtime := startDexService(t, serverZDTestConfig{})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := flowClient.WaitForAttribute(ctx, &dexpb.WaitForAttributeRequest{
		FlowId: "wait-for-attribute-missing-" + uuid.NewString(),
		Match: equalAttributeMatch(
			waitForAttributeKey,
			stringValue("anything"),
		),
		RequestTimeoutSeconds: 0,
		RequestId:             uuid.NewString(),
	})
	require.Equal(t, codes.NotFound, status.Code(err))
	require.Equal(
		t,
		dexpb.ErrorSubStatus_ERROR_SUB_STATUS_FLOW_NOT_EXISTS,
		grpcServiceErrorResponse(t, err).GetSubStatus(),
	)
}

func doTestWaitForAttributeClosed(t *testing.T) {
	workerTarget := startWorker(t, signal.NewHandler())
	runtime := startDexService(t, serverZDTestConfig{})
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
		Match: equalAttributeMatch(
			waitForAttributeKey,
			stringValue("anything"),
		),
		RequestTimeoutSeconds: 0,
		RequestId:             uuid.NewString(),
	})
	require.Error(t, err)
	require.Equal(t, codes.NotFound, status.Code(err))
	require.Equal(
		t,
		dexpb.ErrorSubStatus_ERROR_SUB_STATUS_FLOW_NOT_EXISTS,
		grpcServiceErrorResponse(t, err).GetSubStatus(),
	)
}

func doTestWaitForAttributeConcurrent(t *testing.T) {
	workerTarget := startWorker(t, signal.NewHandler())
	runtime := startDexService(t, serverZDTestConfig{})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowId := startParkedWaitForAttributeFlow(t, ctx, flowClient, workerTarget, nil)
	expectedValue := stringValue("wait-for-attribute-concurrent")
	requestId := uuid.NewString()

	var waitGroup sync.WaitGroup
	errors := make([]error, 2)
	responses := make([]*dexpb.WaitForAttributeResponse, 2)
	for index := range errors {
		waitGroup.Add(1)
		go func(resultIndex int) {
			defer waitGroup.Done()
			response, waitErr := flowClient.WaitForAttribute(ctx, &dexpb.WaitForAttributeRequest{
				FlowId: flowId,
				Match: equalAttributeMatch(
					waitForAttributeKey,
					expectedValue,
				),
				RequestTimeoutSeconds: 30,
				RequestId:             requestId,
			})
			responses[resultIndex] = response
			errors[resultIndex] = waitErr
		}(index)
	}

	_, err := flowClient.SetAttributes(ctx, &dexpb.SetAttributesRequest{
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
	for _, response := range responses {
		require.True(t, proto.Equal(expectedValue, response.GetMatchedValue()))
	}

	stopParkedWaitForAttributeFlow(t, ctx, flowClient, flowId)
}

// requireServerZDWaiterProbeCode probes a missing Flow: RESOURCE_EXHAUSTED means a waiter holds the slot; NOT_FOUND means free.
func requireServerZDWaiterProbeCode(
	t *testing.T,
	flowClient dexpb.FlowServiceClient,
	expectedCode codes.Code,
	waiterDone <-chan error,
) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		probeCtx, cancelProbe := context.WithTimeout(context.Background(), 5*time.Second)
		_, err := flowClient.WaitForAttribute(probeCtx, &dexpb.WaitForAttributeRequest{
			FlowId:                "wait-for-attribute-probe-" + uuid.NewString(),
			Match:                 equalAttributeMatch(waitForAttributeKey, stringValue("probe")),
			RequestTimeoutSeconds: 1,
		})
		cancelProbe()
		probeCode := status.Code(err)
		require.Contains(t, []codes.Code{codes.NotFound, codes.ResourceExhausted}, probeCode, "probe error: %v", err)
		if probeCode == expectedCode {
			return
		}
		select {
		case err := <-waiterDone:
			require.FailNow(t, "WaitForAttribute returned before cancel", "%v", err)
		case <-deadline:
			require.FailNow(t, "server-zd waiter probe never returned the expected code", "%v", expectedCode)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func equalAttributeMatch(
	key string,
	value *dexpb.Value,
) *dexpb.AttributeMatch {
	return waitForAttributeMatch(
		key,
		dexpb.AttributeMatchOperator_ATTRIBUTE_MATCH_OPERATOR_EQUAL,
		value,
	)
}

func waitForAttributeMatch(
	key string,
	operator dexpb.AttributeMatchOperator,
	value *dexpb.Value,
) *dexpb.AttributeMatch {
	return &dexpb.AttributeMatch{
		Key:      key,
		Operator: operator,
		Operand:  value,
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
