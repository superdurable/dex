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
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex/ptr"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/server-zd/integ/internal/testconfig"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

type integRuntime struct {
	FlowClient        dexpb.FlowServiceClient
	PublicAddress     string
	defaultFlowConfig *dexpb.FlowConfig
}

type resultOnlyWaitForWorker interface {
	InvokeWaitForMethod(context.Context, *dexpb.InvokeWaitForMethodRequest) (
		*dexpb.InvokeWaitForMethodResponse,
		error,
	)
}

type resultOnlyExecuteWorker interface {
	InvokeExecuteMethod(context.Context, *dexpb.InvokeExecuteMethodRequest) (
		*dexpb.InvokeExecuteMethodResponse,
		error,
	)
}

type resultOnlyRPCWorker interface {
	InvokeWorkerRPC(context.Context, *dexpb.InvokeWorkerRPCRequest) (
		*dexpb.InvokeWorkerRPCResponse,
		error,
	)
}

type resultOnlyWorkerAdapter struct {
	dexpb.UnimplementedWorkerServiceServer
	handler any
}

func (adapter *resultOnlyWorkerAdapter) InvokeWaitForMethod(
	req *dexpb.InvokeWaitForMethodRequest,
	stream grpc.ServerStreamingServer[dexpb.InvokeWaitForMethodOutput],
) error {
	handler, ok := adapter.handler.(resultOnlyWaitForWorker)
	if !ok {
		return status.Error(codes.Unimplemented, "WaitFor is not implemented")
	}
	response, err := handler.InvokeWaitForMethod(stream.Context(), req)
	if err != nil {
		return err
	}
	return stream.Send(&dexpb.InvokeWaitForMethodOutput{
		Output: &dexpb.InvokeWaitForMethodOutput_Result{Result: response},
	})
}

func (adapter *resultOnlyWorkerAdapter) InvokeExecuteMethod(
	req *dexpb.InvokeExecuteMethodRequest,
	stream grpc.ServerStreamingServer[dexpb.InvokeExecuteMethodOutput],
) error {
	handler, ok := adapter.handler.(resultOnlyExecuteWorker)
	if !ok {
		return status.Error(codes.Unimplemented, "Execute is not implemented")
	}
	response, err := handler.InvokeExecuteMethod(stream.Context(), req)
	if err != nil {
		return err
	}
	return stream.Send(&dexpb.InvokeExecuteMethodOutput{
		Output: &dexpb.InvokeExecuteMethodOutput_Result{Result: response},
	})
}

func (adapter *resultOnlyWorkerAdapter) InvokeWorkerRPC(
	ctx context.Context,
	req *dexpb.InvokeWorkerRPCRequest,
) (*dexpb.InvokeWorkerRPCResponse, error) {
	handler, ok := adapter.handler.(resultOnlyRPCWorker)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "Worker RPC is not implemented")
	}
	return handler.InvokeWorkerRPC(ctx, req)
}

func startWorker(t *testing.T, handler any) *dexpb.WorkerTarget {
	t.Helper()
	if streamingHandler, ok := handler.(dexpb.WorkerServiceServer); ok {
		return startStreamingWorker(t, streamingHandler)
	}
	_, hasWaitFor := handler.(resultOnlyWaitForWorker)
	_, hasExecute := handler.(resultOnlyExecuteWorker)
	_, hasRPC := handler.(resultOnlyRPCWorker)
	require.True(t, hasWaitFor || hasExecute || hasRPC,
		"worker handler must implement streaming or result-only methods")
	return startStreamingWorker(t, &resultOnlyWorkerAdapter{handler: handler})
}

func startStreamingWorker(t *testing.T, handler dexpb.WorkerServiceServer, options ...grpc.ServerOption) *dexpb.WorkerTarget {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	workerOptions := []grpc.ServerOption{grpc.MaxRecvMsgSize(testconfig.DefaultGrpcMaxMessageBytes), grpc.MaxSendMsgSize(testconfig.DefaultGrpcMaxMessageBytes)}
	server := grpc.NewServer(append(workerOptions, options...)...)
	dexpb.RegisterWorkerServiceServer(server, handler)
	serveError := make(chan error, 1)
	go func() { serveError <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.GracefulStop()
		if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			require.NoError(t, err)
		}
		if err := <-serveError; err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			require.NoError(t, err)
		}
	})
	return registerPullWorkerAdapter(t, listener.Addr().String())
}

func withWorkerTarget(
	options *dexpb.FlowStartOptions,
	workerTarget *dexpb.WorkerTarget,
) *dexpb.FlowStartOptions {
	if options == nil {
		options = &dexpb.FlowStartOptions{}
	}
	if options.FlowConfigOverride == nil {
		options.FlowConfigOverride = &dexpb.FlowConfig{}
	}
	options.FlowConfigOverride.WorkerTarget = workerTarget
	return options
}

func startDexService(t *testing.T, testConfig serverZDTestConfig) *integRuntime {
	t.Helper()
	return startBinaryDexService(t, testConfig)
}

// newFlowClientWithCredential dials FlowService; a non-empty credential is sent as a bearer token on every call.
func newFlowClientWithCredential(t *testing.T, address string, credential string) dexpb.FlowServiceClient {
	t.Helper()
	connection := newDexClientConnectionWithCredential(t, address, credential, testconfig.DefaultGrpcMaxMessageBytes)
	client := dexpb.NewFlowServiceClient(connection)

	return &pullWorkerFlowClient{FlowServiceClient: client, test: t, serverAddress: address}

}

func newDexClientConnectionWithCredential(
	t *testing.T,
	address string,
	credential string,
	maxMessageBytes int,
) *grpc.ClientConn {
	t.Helper()
	options := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(maxMessageBytes),
			grpc.MaxCallSendMsgSize(maxMessageBytes),
		),
	}
	if credential != "" {
		options = append(options, grpc.WithPerRPCCredentials(bearerCredential(credential)))
	}
	connection, err := grpc.NewClient(address, options...)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, connection.Close())
	})
	return connection
}

type bearerCredential string

func grpcServiceErrorResponse(t *testing.T, err error) *dexpb.ServiceErrorResponse {
	t.Helper()
	require.Error(t, err)
	statusError, ok := status.FromError(err)
	require.True(t, ok)
	for _, detail := range statusError.Details() {
		if response, ok := detail.(*dexpb.ServiceErrorResponse); ok {
			return response
		}
	}
	require.FailNow(t, "gRPC error has no ServiceErrorResponse details", err)
	return nil
}

func smallWaitForFastTest() {
	if *repeatInterval > 0 {
		timer := time.NewTimer(time.Duration(*repeatInterval) * time.Millisecond)
		defer timer.Stop()
		<-timer.C
	}
}

func minimumContinueAsNewAsyncDurabilityConfig() *dexpb.FlowConfig {
	config := asyncDurabilityConfig()
	config.ContinueAsNewThreshold = ptr.Any(int32(1))
	return config
}

func minimumContinueAsNewSyncDurabilityConfig() *dexpb.FlowConfig {
	config := syncDurabilityConfig()
	config.ContinueAsNewThreshold = ptr.Any(int32(1))
	return config
}

func asyncDurabilityConfig() *dexpb.FlowConfig {
	return &dexpb.FlowConfig{
		StepDurability: ptr.Any(dexpb.StepDurability_STEP_DURABILITY_ASYNC),
	}
}

func syncDurabilityConfig() *dexpb.FlowConfig {
	return &dexpb.FlowConfig{
		StepDurability: ptr.Any(dexpb.StepDurability_STEP_DURABILITY_SYNC),
	}
}

func copyFlowConfigForMutation(flowConfig *dexpb.FlowConfig) *dexpb.FlowConfig {
	return &dexpb.FlowConfig{
		ActiveStepSearchMode:         flowConfig.ActiveStepSearchMode,
		ContinueAsNewThreshold:       flowConfig.ContinueAsNewThreshold,
		ContinueAsNewPageSizeInBytes: flowConfig.ContinueAsNewPageSizeInBytes,
		StepDurability:               flowConfig.StepDurability,
		WorkerTarget:                 flowConfig.WorkerTarget,
	}
}

func encodedObjectValue(encoding string, payload []byte) *dexpb.Value {
	return &dexpb.Value{
		Kind: &dexpb.Value_ObjValue{
			ObjValue: &dexpb.EncodedObject{
				Encoding: encoding,
				Payload:  payload,
			},
		},
	}
}

func jsonObjValue(payload any) *dexpb.Value {
	bytes, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return &dexpb.Value{
		Kind: &dexpb.Value_ObjValue{
			ObjValue: &dexpb.EncodedObject{
				Encoding: "json",
				Payload:  bytes,
			},
		},
	}
}

func stringValue(value string) *dexpb.Value {
	return &dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: value}}
}

func intValue(value int64) *dexpb.Value {
	return &dexpb.Value{Kind: &dexpb.Value_IntValue{IntValue: value}}
}

func boolValue(value bool) *dexpb.Value {
	return &dexpb.Value{Kind: &dexpb.Value_BoolValue{BoolValue: value}}
}

func doubleValue(value float64) *dexpb.Value {
	return &dexpb.Value{Kind: &dexpb.Value_DoubleValue{DoubleValue: value}}
}

func nullValue() *dexpb.Value {
	return &dexpb.Value{
		Kind: &dexpb.Value_NullValue{NullValue: structpb.NullValue_NULL_VALUE},
	}
}

func objJSONValue(payload string) *dexpb.Value {
	return &dexpb.Value{
		Kind: &dexpb.Value_ObjValue{
			ObjValue: &dexpb.EncodedObject{
				Encoding: "json",
				Payload:  []byte(payload),
			},
		},
	}
}

func indexedKeywordAttribute(key, value string) *dexpb.AttributeWrite {
	return &dexpb.AttributeWrite{
		Key:   key,
		Value: stringValue(value),
		IndexConfig: &dexpb.IndexConfig{
			Enable: true,
			Type:   dexpb.IndexType_INDEX_TYPE_KEYWORD,
		},
	}
}

func indexedTextAttribute(key, value string) *dexpb.AttributeWrite {
	return &dexpb.AttributeWrite{
		Key:   key,
		Value: stringValue(value),
		IndexConfig: &dexpb.IndexConfig{
			Enable: true,
			Type:   dexpb.IndexType_INDEX_TYPE_TEXT,
		},
	}
}

func indexedIntAttribute(key string, value int64) *dexpb.AttributeWrite {
	return &dexpb.AttributeWrite{
		Key:   key,
		Value: intValue(value),
		IndexConfig: &dexpb.IndexConfig{
			Enable: true,
			Type:   dexpb.IndexType_INDEX_TYPE_INT,
		},
	}
}

func indexedDoubleAttribute(key string, value float64) *dexpb.AttributeWrite {
	return &dexpb.AttributeWrite{
		Key:   key,
		Value: doubleValue(value),
		IndexConfig: &dexpb.IndexConfig{
			Enable: true,
			Type:   dexpb.IndexType_INDEX_TYPE_DOUBLE,
		},
	}
}

func indexedBoolAttribute(key string, value bool) *dexpb.AttributeWrite {
	return &dexpb.AttributeWrite{
		Key:   key,
		Value: boolValue(value),
		IndexConfig: &dexpb.IndexConfig{
			Enable: true,
			Type:   dexpb.IndexType_INDEX_TYPE_BOOL,
		},
	}
}

func indexedDatetimeAttribute(key, value string) *dexpb.AttributeWrite {
	return &dexpb.AttributeWrite{
		Key:   key,
		Value: stringValue(value),
		IndexConfig: &dexpb.IndexConfig{
			Enable: true,
			Type:   dexpb.IndexType_INDEX_TYPE_DATETIME,
		},
	}
}

func indexedKeywordArrayAttribute(key string, values ...string) *dexpb.AttributeWrite {
	return &dexpb.AttributeWrite{
		Key:   key,
		Value: jsonObjValue(values),
		IndexConfig: &dexpb.IndexConfig{
			Enable: true,
			Type:   dexpb.IndexType_INDEX_TYPE_KEYWORD_ARRAY,
		},
	}
}

func dataObjectAttribute(key, jsonPayload string) *dexpb.AttributeWrite {
	return &dexpb.AttributeWrite{
		Key:   key,
		Value: objJSONValue(jsonPayload),
	}
}

func assertSearchFlows(
	t *testing.T,
	flowClient dexpb.FlowServiceClient,
	query string,
	expectedCount int,
	includeContinuedAsNew bool,
) {
	t.Helper()
	assertions := require.New(t)
	ctx := context.Background()

	if expectedCount == 0 {
		searchResp, err := flowClient.SearchFlows(ctx, &dexpb.SearchFlowsRequest{
			IncludeContinuedAsNew: includeContinuedAsNew,
			Query:                 query,
			PageSize:              2,
		})
		require.NoError(t, err)
		assertions.Empty(searchResp.GetFlowRuns(), "expected zero results for query %v", query)
		assertions.Empty(searchResp.GetNextPageToken())
		return
	}

	var nextPageToken string
	currentCount := 0
	for currentCount < expectedCount {
		searchResp, err := flowClient.SearchFlows(ctx, &dexpb.SearchFlowsRequest{
			IncludeContinuedAsNew: includeContinuedAsNew,
			Query:                 query,
			PageSize:              2,
			NextPageToken:         nextPageToken,
		})
		require.NoError(t, err)

		for _, flowRun := range searchResp.GetFlowRuns() {
			assertions.NotEmpty(flowRun.GetIndexedAttributes())
		}
		currentCount += len(searchResp.GetFlowRuns())
		if currentCount < expectedCount {
			assertions.Equal(2, len(searchResp.GetFlowRuns()))
			assertions.NotEmpty(t, searchResp.GetNextPageToken())
			nextPageToken = searchResp.GetNextPageToken()
		} else if currentCount == expectedCount {
			if searchResp.GetNextPageToken() != "" {
				nextPageToken = searchResp.GetNextPageToken()
				searchResp, err = flowClient.SearchFlows(ctx, &dexpb.SearchFlowsRequest{
					IncludeContinuedAsNew: includeContinuedAsNew,
					Query:                 query,
					PageSize:              2,
					NextPageToken:         nextPageToken,
				})
				require.NoError(t, err)
				assertions.Empty(t, searchResp.GetFlowRuns())
				assertions.Empty(t, searchResp.GetNextPageToken())
			}
		} else {
			assertions.FailNow(
				fmt.Sprintf(
					"currentCount %v is greater than expectedCount %v, for query %v",
					currentCount,
					expectedCount,
					query,
				),
			)
		}
	}
}

func (credential bearerCredential) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	return map[string]string{"authorization": "Bearer " + string(credential)}, nil
}
func (credential bearerCredential) RequireTransportSecurity() bool { return false }
