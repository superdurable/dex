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
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/cmd/server/dex"
	"github.com/superdurable/dex/config"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service"
	"github.com/superdurable/dex/service/api"
	uclient "github.com/superdurable/dex/service/client"
	cadenceapi "github.com/superdurable/dex/service/client/cadence"
	temporalapi "github.com/superdurable/dex/service/client/temporal"
	"github.com/superdurable/dex/service/common/blobstore"
	dexconverter "github.com/superdurable/dex/service/common/converter"
	"github.com/superdurable/dex/service/common/flowindex"
	"github.com/superdurable/dex/service/common/log"
	"github.com/superdurable/dex/service/common/log/loggerimpl"
	"github.com/superdurable/dex/service/common/ptr"
	"github.com/superdurable/dex/service/common/workerclient"
	"github.com/superdurable/dex/service/interpreter/cadence"
	"github.com/superdurable/dex/service/interpreter/temporal"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

// integRuntime is the in-process gRPC Dex stack started for one test.
type integRuntime struct {
	FlowClient    dexpb.FlowServiceClient
	AdminClient   dexpb.AdminServiceClient
	UnifiedClient uclient.UnifiedClient
	BlobStore     blobstore.BlobStore

	defaultFlowConfig *dexpb.FlowConfig
	taskQueue         string

	internalDumpCapture *internalDumpHeaderCapture
}

type internalDumpHeaderCapture struct {
	mu      sync.Mutex
	headers []metadata.MD
}

func (capture *internalDumpHeaderCapture) append(incoming metadata.MD) {
	capture.mu.Lock()
	capture.headers = append(capture.headers, incoming)
	capture.mu.Unlock()
}

func (capture *internalDumpHeaderCapture) snapshot() []metadata.MD {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return append([]metadata.MD(nil), capture.headers...)
}

func newInternalDumpHeaderCaptureInterceptor(
	capture *internalDumpHeaderCapture,
) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		if info.FullMethod == dexpb.InternalService_DumpFlowForContinueAsNew_FullMethodName {
			captured := metadata.MD{}
			if incomingMetadata, ok := metadata.FromIncomingContext(ctx); ok {
				captured = incomingMetadata.Copy()
			}
			capture.append(captured)
		}
		return handler(ctx, req)
	}
}

type interpreterWorker interface {
	Start() error
	StartWithStickyCacheDisabledForTest() error
	Close()
}

// startWorker serves a WorkerServiceServer and returns its dial target.
func startWorker(t *testing.T, handler dexpb.WorkerServiceServer) *dexpb.WorkerTarget {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	// Match Api.EffectiveGrpcMaxMessageBytes; bare grpc.NewServer defaults to 4MiB.
	server := grpc.NewServer(
		grpc.MaxRecvMsgSize(config.DefaultGrpcMaxMessageBytes),
		grpc.MaxSendMsgSize(config.DefaultGrpcMaxMessageBytes),
	)
	dexpb.RegisterWorkerServiceServer(server, handler)
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
	return &dexpb.WorkerTarget{Address: listener.Addr().String()}
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

// startDexService returns clients, starting API and interpreter unless an external Dex address is configured.
func startDexService(t *testing.T, testConfig DexServiceTestConfig) *integRuntime {
	t.Helper()
	if *dexServerAddress != "" {
		return connectToExternalDexService(t, testConfig)
	}
	return startInProcessDexService(t, testConfig)
}

func startInProcessDexService(t *testing.T, testConfig DexServiceTestConfig) *integRuntime {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	cfg := createTestConfig(testConfig)
	cfg.Interpreter.InterpreterActivityConfig.InternalServiceTarget = listener.Addr().String()
	taskQueue := fmt.Sprintf("%s_INTEG_%d", service.TaskQueue, time.Now().UnixNano())
	var flowIndexStore flowindex.Store
	if cfg.FlowIndex.EffectiveBackend() == config.FlowIndexBackendParadeDB {
		flowIndexStore, err = flowindex.NewParadeDBStore(context.Background(), &cfg.FlowIndex)
		require.NoError(t, err)
		if testConfig.FlowIndexStoreWrapper != nil {
			flowIndexStore = testConfig.FlowIndexStoreWrapper(flowIndexStore)
		}
		t.Cleanup(flowIndexStore.Close)
	}
	workerPool, err := workerclient.NewWorkerClientPool(&cfg)
	require.NoError(t, err)
	t.Cleanup(workerPool.Close)
	logger, err := loggerimpl.NewDevelopment()
	require.NoError(t, err)
	s3Client, err := dex.CreateS3Client(cfg, context.Background())
	require.NoError(t, err)

	var worker interpreterWorker
	var unifiedClient uclient.UnifiedClient
	var store blobstore.BlobStore
	switch testConfig.BackendType {
	case service.BackendTypeTemporal:
		dataConverter := dexconverter.NewTemporalDataConverter()
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
			taskQueue,
			dataConverter,
			unifiedClient,
			store,
			flowIndexStore,
			workerPool,
		)
	case service.BackendTypeCadence:
		serviceClient, closeServiceClient, err := dex.BuildCadenceServiceClient(
			dex.DefaultCadenceHostPort,
		)
		require.NoError(t, err)
		dataConverter := dexconverter.NewCadenceDataConverter()
		cadenceClient, err := dex.BuildCadenceClient(
			serviceClient,
			dex.DefaultCadenceDomain,
			dataConverter,
		)
		require.NoError(t, err)
		store = blobstore.NewBlobStore(
			s3Client,
			dex.DefaultCadenceDomain,
			cfg.ExternalStorage,
			logger,
			client.MetricsNopHandler,
		)
		unifiedClient = cadenceapi.NewCadenceClient(
			dex.DefaultCadenceDomain,
			cadenceClient,
			serviceClient,
			dataConverter,
			closeServiceClient,
			&cfg.Api.QueryWorkflowFailedRetryPolicy,
		)
		worker = cadence.NewInterpreterWorker(
			&cfg,
			serviceClient,
			dex.DefaultCadenceDomain,
			taskQueue,
			closeServiceClient,
			dataConverter,
			unifiedClient,
			store,
			flowIndexStore,
			workerPool,
		)
	default:
		require.FailNow(t, "unsupported backend", testConfig.BackendType)
	}
	startInterpreter(t, worker)
	internalDumpCapture := &internalDumpHeaderCapture{}
	runtime := &integRuntime{
		defaultFlowConfig:   cfg.Interpreter.DefaultWorkflowConfig,
		internalDumpCapture: internalDumpCapture,
		taskQueue:           taskQueue,
	}
	previousDumpObserver := api.DumpFlowForContinueAsNewHeaderObserver
	api.DumpFlowForContinueAsNewHeaderObserver = func(ctx context.Context) {
		if incomingMetadata, ok := metadata.FromIncomingContext(ctx); ok {
			internalDumpCapture.append(incomingMetadata.Copy())
		}
	}
	t.Cleanup(func() {
		api.DumpFlowForContinueAsNewHeaderObserver = previousDumpObserver
	})
	startApiServer(
		t,
		listener,
		&cfg,
		taskQueue,
		unifiedClient,
		logger,
		store,
		flowIndexStore,
		worker.Close,
		workerPool,
		newInternalDumpHeaderCaptureInterceptor(internalDumpCapture),
	)

	connection, err := newDexClientConnection(
		listener.Addr().String(),
		cfg.Api.EffectiveGrpcMaxMessageBytes(),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, connection.Close())
	})

	globalBlobStore = store
	runtime.FlowClient = dexpb.NewFlowServiceClient(connection)
	runtime.AdminClient = dexpb.NewAdminServiceClient(connection)
	runtime.UnifiedClient = unifiedClient
	runtime.BlobStore = store
	return runtime
}

func (runtime *integRuntime) requireInternalDumpHeaders(
	t *testing.T,
	headerKey string,
	headerValue string,
) {
	t.Helper()
	captured := runtime.internalDumpCapture.snapshot()

	require.NotEmpty(t, captured, "expected at least one DumpFlowForContinueAsNew call")
	found := false
	for _, capturedMetadata := range captured {
		values := capturedMetadata.Get(headerKey)
		if len(values) > 0 && values[0] == headerValue {
			found = true
			break
		}
	}
	require.True(
		t,
		found,
		"DumpFlowForContinueAsNew metadata missing %q=%q, captured: %v",
		headerKey,
		headerValue,
		captured,
	)
}

// globalBlobStore is set by startDexService for S3 cleanup tests that need the store.
var globalBlobStore blobstore.BlobStore

func startInterpreter(t *testing.T, worker interpreterWorker) {
	t.Helper()
	if *disableStickyCache {
		require.NoError(t, worker.StartWithStickyCacheDisabledForTest())
		return
	}
	require.NoError(t, worker.Start())
}

func startApiServer(
	t *testing.T,
	listener net.Listener,
	cfg *config.Config,
	taskQueue string,
	unifiedClient uclient.UnifiedClient,
	logger log.Logger,
	store blobstore.BlobStore,
	flowIndexStore flowindex.Store,
	closeInterpreter func(),
	workerPool *workerclient.WorkerClientPool,
	extraUnaryInterceptors ...grpc.UnaryServerInterceptor,
) {
	t.Helper()

	server := api.NewServer(
		&cfg.Api,
		&cfg.ExternalStorage,
		&cfg.Interpreter,
		&cfg.FlowIndex,
		taskQueue,
		unifiedClient,
		logger,
		store,
		flowIndexStore,
		func(context.Context) error { return nil },
		workerPool,
		extraUnaryInterceptors...,
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

func grpcErrorResponse(t *testing.T, err error) *dexpb.ErrorResponse {
	t.Helper()
	require.Error(t, err)
	statusError, ok := status.FromError(err)
	require.True(t, ok)
	for _, detail := range statusError.Details() {
		if response, ok := detail.(*dexpb.ErrorResponse); ok {
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
) {
	t.Helper()
	assertions := require.New(t)
	ctx := context.Background()

	if expectedCount == 0 {
		searchResp, err := flowClient.SearchFlows(ctx, &dexpb.SearchFlowsRequest{
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
		searchResp, err := flowClient.SearchFlows(ctx, &dexpb.SearchFlowsRequest{
			Query:         query,
			PageSize:      2,
			NextPageToken: nextPageToken,
		})
		require.NoError(t, err)

		for _, flowRun := range searchResp.GetFlowRuns() {
			assertions.NotEmpty(flowRun.GetSearchAttributes())
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
