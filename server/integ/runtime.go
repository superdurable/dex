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
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/iwf/cmd/server/iwf"
	"github.com/superdurable/iwf/config"
	"github.com/superdurable/iwf/gen/iwfpb"
	"github.com/superdurable/iwf/service"
	"github.com/superdurable/iwf/service/api"
	uclient "github.com/superdurable/iwf/service/client"
	cadenceapi "github.com/superdurable/iwf/service/client/cadence"
	temporalapi "github.com/superdurable/iwf/service/client/temporal"
	"github.com/superdurable/iwf/service/common/blobstore"
	iwfconverter "github.com/superdurable/iwf/service/common/converter"
	"github.com/superdurable/iwf/service/common/log"
	"github.com/superdurable/iwf/service/common/log/loggerimpl"
	"github.com/superdurable/iwf/service/common/ptr"
	"github.com/superdurable/iwf/service/interpreter/cadence"
	"github.com/superdurable/iwf/service/interpreter/temporal"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

// integRuntime is the in-process gRPC iWF stack started for one test.
type integRuntime struct {
	FlowClient    iwfpb.FlowServiceClient
	UnifiedClient uclient.UnifiedClient
	BlobStore     blobstore.BlobStore
}

type interpreterWorker interface {
	Start()
	StartWithStickyCacheDisabledForTest()
	Close()
}

// startWorker serves a WorkerServiceServer on 127.0.0.1:0 and returns the dial target.
func startWorker(t *testing.T, handler iwfpb.WorkerServiceServer) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	// Match Api.EffectiveGrpcMaxMessageBytes; bare grpc.NewServer defaults to 4MiB.
	server := grpc.NewServer(
		grpc.MaxRecvMsgSize(config.DefaultGrpcMaxMessageBytes),
		grpc.MaxSendMsgSize(config.DefaultGrpcMaxMessageBytes),
	)
	iwfpb.RegisterWorkerServiceServer(server, handler)
	serveError := make(chan error, 1)
	go func() {
		serveError <- server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.GracefulStop()
		if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			require.NoError(t, err)
		}
		if err := <-serveError; err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			require.NoError(t, err)
		}
	})
	return listener.Addr().String()
}

// startIwfService starts API + interpreter against Temporal or Cadence and returns clients.
func startIwfService(t *testing.T, testConfig IwfServiceTestConfig) integRuntime {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	cfg := createTestConfig(testConfig)
	cfg.Interpreter.InterpreterActivityConfig.InternalServiceTarget = listener.Addr().String()
	logger, err := loggerimpl.NewDevelopment()
	require.NoError(t, err)
	s3Client := iwf.CreateS3Client(cfg, context.Background())

	var worker interpreterWorker
	var unifiedClient uclient.UnifiedClient
	var store blobstore.BlobStore
	switch testConfig.BackendType {
	case service.BackendTypeTemporal:
		dataConverter := iwfconverter.NewTemporalDataConverter()
		if testConfig.MemoEncryption {
			dataConverter = encryptionDataConverter
		}
		temporalClient := createTemporalClient(t, dataConverter)
		store = blobstore.NewBlobStore(
			s3Client,
			testNamespace,
			cfg.ExternalStorage,
			logger,
			client.MetricsNopHandler,
		)
		unifiedClient = temporalapi.NewTemporalClient(
			temporalClient,
			testNamespace,
			dataConverter,
			testConfig.MemoEncryption,
			&cfg.Api.QueryWorkflowFailedRetryPolicy,
		)
		worker = temporal.NewInterpreterWorker(
			&cfg,
			temporalClient,
			service.TaskQueue,
			dataConverter,
			unifiedClient,
			store,
		)
	case service.BackendTypeCadence:
		serviceClient, closeServiceClient, err := iwf.BuildCadenceServiceClient(
			iwf.DefaultCadenceHostPort,
		)
		require.NoError(t, err)
		dataConverter := iwfconverter.NewCadenceDataConverter()
		cadenceClient, err := iwf.BuildCadenceClient(
			serviceClient,
			iwf.DefaultCadenceDomain,
			dataConverter,
		)
		require.NoError(t, err)
		store = blobstore.NewBlobStore(
			s3Client,
			iwf.DefaultCadenceDomain,
			cfg.ExternalStorage,
			logger,
			client.MetricsNopHandler,
		)
		unifiedClient = cadenceapi.NewCadenceClient(
			iwf.DefaultCadenceDomain,
			cadenceClient,
			serviceClient,
			dataConverter,
			closeServiceClient,
			&cfg.Api.QueryWorkflowFailedRetryPolicy,
		)
		worker = cadence.NewInterpreterWorker(
			&cfg,
			serviceClient,
			iwf.DefaultCadenceDomain,
			service.TaskQueue,
			closeServiceClient,
			dataConverter,
			unifiedClient,
			store,
		)
	default:
		require.FailNow(t, "unsupported backend", testConfig.BackendType)
	}
	startInterpreter(worker)
	startApiServer(t, listener, &cfg, unifiedClient, logger, store, worker.Close)

	connection, err := grpc.NewClient(
		listener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(cfg.Api.EffectiveGrpcMaxMessageBytes()),
			grpc.MaxCallSendMsgSize(cfg.Api.EffectiveGrpcMaxMessageBytes()),
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, connection.Close())
	})

	globalBlobStore = store
	return integRuntime{
		FlowClient:    iwfpb.NewFlowServiceClient(connection),
		UnifiedClient: unifiedClient,
		BlobStore:     store,
	}
}

// globalBlobStore is set by startIwfService for S3 cleanup tests that need the store.
var globalBlobStore blobstore.BlobStore

func startInterpreter(worker interpreterWorker) {
	if *disableStickyCache {
		worker.StartWithStickyCacheDisabledForTest()
		return
	}
	worker.Start()
}

func startApiServer(
	t *testing.T,
	listener net.Listener,
	cfg *config.Config,
	unifiedClient uclient.UnifiedClient,
	logger log.Logger,
	store blobstore.BlobStore,
	closeInterpreter func(),
) {
	t.Helper()

	server := api.NewServer(
		&cfg.Api,
		&cfg.ExternalStorage,
		&cfg.Interpreter,
		unifiedClient,
		logger,
		store,
		func(context.Context) error { return nil },
	)
	serveError := make(chan error, 1)
	go func() {
		serveError <- server.Serve(listener)
	}()
	t.Cleanup(func() {
		closeInterpreter()
		server.GracefulStop(2 * time.Second)
		if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			require.NoError(t, err)
		}
		if err := <-serveError; err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			require.NoError(t, err)
		}
	})
}

func createTemporalClient(
	t *testing.T,
	dataConverter converter.DataConverter,
) client.Client {
	t.Helper()
	temporalClient, err := client.Dial(client.Options{
		HostPort:      *temporalHostPort,
		Namespace:     testNamespace,
		DataConverter: dataConverter,
	})
	require.NoError(t, err)
	return temporalClient
}

func grpcErrorResponse(t *testing.T, err error) *iwfpb.ErrorResponse {
	t.Helper()
	require.Error(t, err)
	statusError, ok := status.FromError(err)
	require.True(t, ok)
	for _, detail := range statusError.Details() {
		if response, ok := detail.(*iwfpb.ErrorResponse); ok {
			return response
		}
	}
	require.FailNow(t, "gRPC error has no ErrorResponse details", err)
	return nil
}

func smallWaitForFastTest() {
	duration := time.Millisecond * time.Duration(*repeatInterval)
	if *repeatIntegTest == 0 {
		duration = time.Millisecond
	}
	time.Sleep(duration)
}

func minimumContinueAsNewConfig(durability iwfpb.StepDurability) *iwfpb.FlowConfig {
	return &iwfpb.FlowConfig{
		ContinueAsNewThreshold: ptr.Any(int32(1)),
		StepDurability:         ptr.Any(durability),
	}
}

func minimumContinueAsNewConfigV0() *iwfpb.FlowConfig {
	return minimumContinueAsNewConfig(iwfpb.StepDurability_STEP_DURABILITY_SYNC)
}

func encodedObjectValue(encoding string, payload []byte) *iwfpb.Value {
	return &iwfpb.Value{
		Kind: &iwfpb.Value_ObjValue{
			ObjValue: &iwfpb.EncodedObject{
				Encoding: encoding,
				Payload:  payload,
			},
		},
	}
}

func getBackendTypes() []service.BackendType {
	backends := []service.BackendType{}
	if *temporalIntegTest {
		backends = append(backends, service.BackendTypeTemporal)
	}
	if *cadenceIntegTest {
		backends = append(backends, service.BackendTypeCadence)
	}
	return backends
}

func jsonObjValue(payload any) *iwfpb.Value {
	bytes, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return &iwfpb.Value{
		Kind: &iwfpb.Value_ObjValue{
			ObjValue: &iwfpb.EncodedObject{
				Encoding: "json",
				Payload:  bytes,
			},
		},
	}
}

func stringValue(value string) *iwfpb.Value {
	return &iwfpb.Value{Kind: &iwfpb.Value_StringValue{StringValue: value}}
}

func intValue(value int64) *iwfpb.Value {
	return &iwfpb.Value{Kind: &iwfpb.Value_IntValue{IntValue: value}}
}

func boolValue(value bool) *iwfpb.Value {
	return &iwfpb.Value{Kind: &iwfpb.Value_BoolValue{BoolValue: value}}
}

func doubleValue(value float64) *iwfpb.Value {
	return &iwfpb.Value{Kind: &iwfpb.Value_DoubleValue{DoubleValue: value}}
}

func nullValue() *iwfpb.Value {
	return &iwfpb.Value{
		Kind: &iwfpb.Value_NullValue{NullValue: structpb.NullValue_NULL_VALUE},
	}
}

func objJSONValue(payload string) *iwfpb.Value {
	return &iwfpb.Value{
		Kind: &iwfpb.Value_ObjValue{
			ObjValue: &iwfpb.EncodedObject{
				Encoding: "json",
				Payload:  []byte(payload),
			},
		},
	}
}

func indexedKeywordAttribute(key, value string) *iwfpb.AttributeWrite {
	return &iwfpb.AttributeWrite{
		Key:   key,
		Value: stringValue(value),
		IndexConfig: &iwfpb.IndexConfig{
			Enable: true,
			Type:   iwfpb.IndexType_INDEX_TYPE_KEYWORD,
		},
	}
}

func indexedTextAttribute(key, value string) *iwfpb.AttributeWrite {
	return &iwfpb.AttributeWrite{
		Key:   key,
		Value: stringValue(value),
		IndexConfig: &iwfpb.IndexConfig{
			Enable: true,
			Type:   iwfpb.IndexType_INDEX_TYPE_TEXT,
		},
	}
}

func indexedIntAttribute(key string, value int64) *iwfpb.AttributeWrite {
	return &iwfpb.AttributeWrite{
		Key:   key,
		Value: intValue(value),
		IndexConfig: &iwfpb.IndexConfig{
			Enable: true,
			Type:   iwfpb.IndexType_INDEX_TYPE_INT,
		},
	}
}

func indexedDoubleAttribute(key string, value float64) *iwfpb.AttributeWrite {
	return &iwfpb.AttributeWrite{
		Key:   key,
		Value: doubleValue(value),
		IndexConfig: &iwfpb.IndexConfig{
			Enable: true,
			Type:   iwfpb.IndexType_INDEX_TYPE_DOUBLE,
		},
	}
}

func indexedBoolAttribute(key string, value bool) *iwfpb.AttributeWrite {
	return &iwfpb.AttributeWrite{
		Key:   key,
		Value: boolValue(value),
		IndexConfig: &iwfpb.IndexConfig{
			Enable: true,
			Type:   iwfpb.IndexType_INDEX_TYPE_BOOL,
		},
	}
}

func indexedDatetimeAttribute(key, value string) *iwfpb.AttributeWrite {
	return &iwfpb.AttributeWrite{
		Key:   key,
		Value: stringValue(value),
		IndexConfig: &iwfpb.IndexConfig{
			Enable: true,
			Type:   iwfpb.IndexType_INDEX_TYPE_DATETIME,
		},
	}
}

func indexedKeywordArrayAttribute(key string, values ...string) *iwfpb.AttributeWrite {
	return &iwfpb.AttributeWrite{
		Key:   key,
		Value: jsonObjValue(values),
		IndexConfig: &iwfpb.IndexConfig{
			Enable: true,
			Type:   iwfpb.IndexType_INDEX_TYPE_KEYWORD_ARRAY,
		},
	}
}

func dataObjectAttribute(key, jsonPayload string) *iwfpb.AttributeWrite {
	return &iwfpb.AttributeWrite{
		Key:   key,
		Value: objJSONValue(jsonPayload),
	}
}

func assertSearchFlows(
	t *testing.T,
	flowClient iwfpb.FlowServiceClient,
	query string,
	expectedCount int,
) {
	t.Helper()
	assertions := require.New(t)
	ctx := context.Background()

	if expectedCount == 0 {
		searchResp, err := flowClient.SearchFlows(ctx, &iwfpb.SearchFlowsRequest{
			Query:    query,
			PageSize: 2,
		})
		require.NoError(t, err)
		assertions.Empty(searchResp.GetFlowRuns(), "expected zero results for query %v", query)
		assertions.Empty(searchResp.GetNextPageToken())
		return
	}

	var nextPageToken string
	currentCount := 0
	for currentCount < expectedCount {
		searchResp, err := flowClient.SearchFlows(ctx, &iwfpb.SearchFlowsRequest{
			Query:         query,
			PageSize:      2,
			NextPageToken: nextPageToken,
		})
		require.NoError(t, err)

		currentCount += len(searchResp.GetFlowRuns())
		if currentCount < expectedCount {
			assertions.Equal(2, len(searchResp.GetFlowRuns()))
			assertions.NotEmpty(t, searchResp.GetNextPageToken())
			nextPageToken = searchResp.GetNextPageToken()
		} else if currentCount == expectedCount {
			if searchResp.GetNextPageToken() != "" {
				nextPageToken = searchResp.GetNextPageToken()
				searchResp, err = flowClient.SearchFlows(ctx, &iwfpb.SearchFlowsRequest{
					Query:         query,
					PageSize:      2,
					NextPageToken: nextPageToken,
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
