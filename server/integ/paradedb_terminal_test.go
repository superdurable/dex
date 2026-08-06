// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

//go:build paradedb

package integ

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/config"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service"
	"github.com/superdurable/dex/service/common/flowindex"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	paradeTerminalFlowType = "parade-terminal-flow"
	paradeTerminalStep     = "blocked-step"
	paradeUnexpectedStep   = "must-not-start"
)

type paradeTerminalHandler struct {
	dexpb.UnimplementedWorkerServiceServer
	started        chan struct{}
	release        chan struct{}
	startedOnce    sync.Once
	unexpectedRuns atomic.Int32
	complete       bool
}

type retryingFlowIndexStore struct {
	flowindex.Store
	failUntil time.Time
	attempts  atomic.Int32
}

type paradeResetHandler struct {
	dexpb.UnimplementedWorkerServiceServer
	calls         atomic.Int32
	secondStarted chan struct{}
	releaseSecond chan struct{}
}

type paradeRPCHandler struct {
	dexpb.UnimplementedWorkerServiceServer
	started chan struct{}
	release chan struct{}
}

func newParadeTerminalHandler(complete bool) *paradeTerminalHandler {
	return &paradeTerminalHandler{
		started:  make(chan struct{}),
		release:  make(chan struct{}),
		complete: complete,
	}
}

func newParadeResetHandler() *paradeResetHandler {
	return &paradeResetHandler{
		secondStarted: make(chan struct{}),
		releaseSecond: make(chan struct{}),
	}
}

func newParadeRPCHandler() *paradeRPCHandler {
	return &paradeRPCHandler{started: make(chan struct{}), release: make(chan struct{})}
}

func (h *paradeTerminalHandler) InvokeExecuteMethod(
	ctx context.Context,
	request *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	if request.GetStepType() == paradeUnexpectedStep {
		h.unexpectedRuns.Add(1)
		return &dexpb.InvokeExecuteMethodResponse{StepDecision: &dexpb.StepDecision{
			CloseDecision: &dexpb.CloseDecision{
				CloseDecisionType: dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_GRACEFUL_COMPLETE,
			},
		}}, nil
	}
	h.startedOnce.Do(func() { close(h.started) })
	select {
	case <-h.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	response := &dexpb.InvokeExecuteMethodResponse{
		UpsertAttributes: []*dexpb.AttributeWrite{indexedKeywordAttribute("stage", "producer-finished")},
	}
	if h.complete {
		response.StepDecision = &dexpb.StepDecision{CloseDecision: &dexpb.CloseDecision{
			CloseDecisionType: dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_GRACEFUL_COMPLETE,
		}}
	} else {
		response.StepDecision = &dexpb.StepDecision{NextSteps: []*dexpb.StepMovement{{
			StepType: paradeUnexpectedStep, StepOptions: &dexpb.StepOptions{SkipWaitFor: true},
		}}}
	}
	return response, nil
}

func (s *retryingFlowIndexStore) Write(
	ctx context.Context,
	input *dexpb.WriteFlowIndexActivityInput,
) error {
	s.attempts.Add(1)
	if time.Now().Before(s.failUntil) {
		return errors.New("injected ParadeDB writer outage")
	}
	return s.Store.Write(ctx, input)
}

func (h *paradeResetHandler) InvokeExecuteMethod(
	ctx context.Context,
	_ *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	if h.calls.Add(1) == 2 {
		close(h.secondStarted)
		select {
		case <-h.releaseSecond:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return &dexpb.InvokeExecuteMethodResponse{
		UpsertAttributes: []*dexpb.AttributeWrite{indexedKeywordAttribute("later", "written-after-reset-point")},
		StepDecision: &dexpb.StepDecision{CloseDecision: &dexpb.CloseDecision{
			CloseDecisionType: dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_GRACEFUL_COMPLETE,
		}},
	}, nil
}

func (h *paradeRPCHandler) InvokeWorkerRPC(
	ctx context.Context,
	_ *dexpb.InvokeWorkerRPCRequest,
) (*dexpb.InvokeWorkerRPCResponse, error) {
	close(h.started)
	select {
	case <-h.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &dexpb.InvokeWorkerRPCResponse{
		Output: stringValue("late result"),
		UpsertAttributes: []*dexpb.AttributeWrite{
			indexedKeywordAttribute("rpc", "must-not-apply"),
		},
	}, nil
}

func TestParadeDBTerminalFlushTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for _, testCase := range []struct {
		name       string
		stopType   dexpb.StopType
		expected   dexpb.FlowStatus
		completion bool
	}{
		{name: "cancel", stopType: dexpb.StopType_STOP_TYPE_CANCEL, expected: dexpb.FlowStatus_FLOW_STATUS_CANCELED},
		{name: "fail", stopType: dexpb.StopType_STOP_TYPE_FAIL, expected: dexpb.FlowStatus_FLOW_STATUS_FAILED},
		{name: "complete", expected: dexpb.FlowStatus_FLOW_STATUS_COMPLETED, completion: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			runParadeTerminalTest(t, testCase.stopType, testCase.expected, testCase.completion)
		})
	}
}

func TestParadeDBLocalActivityRetriesBeyondWindowTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	handler := newParadeTerminalHandler(true)
	close(handler.release)
	workerTarget := startWorker(t, handler)
	var retryingStore *retryingFlowIndexStore
	runtime := startDexService(t, DexServiceTestConfig{
		BackendType: service.BackendTypeTemporal,
		FlowIndex:   paradeTestFlowIndexConfig(fmt.Sprintf("dex_retry_%d", time.Now().UnixNano())),
		FlowIndexStoreWrapper: func(store flowindex.Store) flowindex.Store {
			retryingStore = &retryingFlowIndexStore{Store: store, failUntil: time.Now().Add(8 * time.Second)}
			return retryingStore
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	applyParadeTestSchema(t, ctx, runtime)

	startedAt := time.Now()
	flowID := startParadeTestFlow(t, ctx, runtime, workerTarget)
	waitResponse, err := waitForParadeFlow(ctx, runtime.FlowClient, flowID)
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, waitResponse.GetFlowStatus())
	require.GreaterOrEqual(t, time.Since(startedAt), 7*time.Second)
	require.GreaterOrEqual(t, retryingStore.attempts.Load(), int32(3))
}

func TestParadeDBWriterHydratesIndexedBlobsTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	handler := newParadeTerminalHandler(true)
	close(handler.release)
	workerTarget := startWorker(t, handler)
	runtime := startDexService(t, DexServiceTestConfig{
		BackendType:     service.BackendTypeTemporal,
		S3TestThreshold: 16,
		FlowIndex:       paradeTestFlowIndexConfig(fmt.Sprintf("dex_blob_%d", time.Now().UnixNano())),
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := runtime.AdminClient.ApplyFlowIndexSchema(ctx, &dexpb.ApplyFlowIndexSchemaRequest{
		DefinitionVersion: 1,
		Attributes: []*dexpb.FlowIndexField{{
			Name: "large_text", Type: dexpb.IndexType_INDEX_TYPE_TEXT,
		}},
	})
	require.NoError(t, err)

	flowID := paradeTerminalFlowType + "-" + uuid.NewString()
	_, err = runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId: newRequestID(), FlowId: flowID, FlowType: paradeTerminalFlowType,
		FlowTimeoutSeconds: 25, FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{}, workerTarget),
	})
	require.NoError(t, err)
	summary, err := runtime.FlowClient.GetFlowSummary(ctx, &dexpb.GetFlowSummaryRequest{FlowId: flowID})
	require.NoError(t, err)
	require.Equal(t, paradeTerminalFlowType, summary.GetFlowType())
	_, err = runtime.FlowClient.SetAttributes(ctx, &dexpb.SetAttributesRequest{
		RequestId: newRequestID(), FlowId: flowID,
		Attributes: []*dexpb.AttributeWrite{{
			Key: "large", Value: stringValue("hydratable ParadeDB text larger than the blob threshold"),
			IndexConfig: &dexpb.IndexConfig{
				Enable: true, Type: dexpb.IndexType_INDEX_TYPE_TEXT, IndexKey: "large_text",
			},
		}},
	})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		response, searchErr := runtime.FlowClient.SearchFlows(ctx, &dexpb.SearchFlowsRequest{
			Query: `large_text:hydratable`, PageSize: 10,
		})
		return searchErr == nil && len(response.GetFlowRuns()) == 1 && response.GetFlowRuns()[0].GetFlowId() == flowID
	}, 10*time.Second, 20*time.Millisecond)

	_, err = runtime.FlowClient.StopFlow(ctx, &dexpb.StopFlowRequest{
		FlowId: flowID, StopType: dexpb.StopType_STOP_TYPE_CANCEL,
	})
	require.NoError(t, err)
	waitResponse, err := waitForParadeFlow(ctx, runtime.FlowClient, flowID)
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_CANCELED, waitResponse.GetFlowStatus())
}

func TestParadeDBPendingMutationsContinueAsNewTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	handler := newParadeTerminalHandler(false)
	close(handler.release)
	workerTarget := startWorker(t, handler)
	var retryingStore *retryingFlowIndexStore
	runtime := startDexService(t, DexServiceTestConfig{
		BackendType: service.BackendTypeTemporal,
		FlowIndex:   paradeTestFlowIndexConfig(fmt.Sprintf("dex_can_%d", time.Now().UnixNano())),
		FlowIndexStoreWrapper: func(store flowindex.Store) flowindex.Store {
			retryingStore = &retryingFlowIndexStore{Store: store, failUntil: time.Now().Add(8 * time.Second)}
			return retryingStore
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	applyParadeTestSchema(t, ctx, runtime)

	flowID := paradeTerminalFlowType + "-" + uuid.NewString()
	startResponse, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId: newRequestID(), FlowId: flowID, FlowType: paradeTerminalFlowType,
		FlowTimeoutSeconds: 40, StartStepType: paradeTerminalStep,
		StepOptions: &dexpb.StepOptions{SkipWaitFor: true},
		FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{
			Attributes:         []*dexpb.AttributeWrite{indexedKeywordAttribute("stage", "started")},
			FlowConfigOverride: minimumContinueAsNewSyncDurabilityConfig(),
		}, workerTarget),
	})
	require.NoError(t, err)
	waitResponse, err := waitForParadeFlow(ctx, runtime.FlowClient, flowID)
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, waitResponse.GetFlowStatus())
	require.Equal(t, int32(1), handler.unexpectedRuns.Load())
	require.GreaterOrEqual(t, retryingStore.attempts.Load(), int32(3))

	searchResponse, err := runtime.FlowClient.SearchFlows(ctx, &dexpb.SearchFlowsRequest{
		Query: fmt.Sprintf(`FlowID:"%s"`, flowID), PageSize: 10,
	})
	require.NoError(t, err)
	require.Len(t, searchResponse.GetFlowRuns(), 1)
	flow := searchResponse.GetFlowRuns()[0]
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, flow.GetFlowStatus())
	require.NotEqual(t, startResponse.GetRunId(), flow.GetRunId())
	require.Equal(t, "producer-finished", searchAttributeString(flow.GetSearchAttributes(), "stage"))
}

func TestParadeDBTerminateStopsRetryAndReportsPartialSuccessTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	handler := newParadeTerminalHandler(true)
	close(handler.release)
	workerTarget := startWorker(t, handler)
	var retryingStore *retryingFlowIndexStore
	runtime := startDexService(t, DexServiceTestConfig{
		BackendType: service.BackendTypeTemporal,
		FlowIndex:   paradeTestFlowIndexConfig(fmt.Sprintf("dex_terminate_%d", time.Now().UnixNano())),
		FlowIndexStoreWrapper: func(store flowindex.Store) flowindex.Store {
			retryingStore = &retryingFlowIndexStore{Store: store, failUntil: time.Now().Add(time.Hour)}
			return retryingStore
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	applyParadeTestSchema(t, ctx, runtime)
	flowID := startParadeTestFlow(t, ctx, runtime, workerTarget)
	require.Eventually(t, func() bool {
		return retryingStore.attempts.Load() > 0
	}, 10*time.Second, 20*time.Millisecond)

	_, err := runtime.FlowClient.StopFlow(ctx, &dexpb.StopFlowRequest{
		FlowId: flowID, StopType: dexpb.StopType_STOP_TYPE_TERMINATE, Reason: "hard stop",
	})
	require.Equal(t, codes.Internal, status.Code(err))
	require.ErrorContains(t, err, "workflow was terminated")
	waitResponse, err := runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowID})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_TERMINATED, waitResponse.GetFlowStatus())
	terminatedAt := time.Now()
	require.Eventually(t, func() bool {
		return time.Since(terminatedAt) >= 8*time.Second
	}, 9*time.Second, 20*time.Millisecond)
	attemptsAfterWindow := retryingStore.attempts.Load()
	require.Never(t, func() bool {
		return retryingStore.attempts.Load() > attemptsAfterWindow
	}, 2*time.Second, 20*time.Millisecond)
}

func TestParadeDBResetReplacesStaleColumnsTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	handler := newParadeResetHandler()
	workerTarget := startWorker(t, handler)
	runtime := startDexService(t, DexServiceTestConfig{
		BackendType: service.BackendTypeTemporal,
		FlowIndex:   paradeTestFlowIndexConfig(fmt.Sprintf("dex_reset_%d", time.Now().UnixNano())),
	})
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	applyParadeSchema(t, ctx, runtime, "stage", "later")

	flowID := paradeTerminalFlowType + "-" + uuid.NewString()
	startResponse, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId: newRequestID(), FlowId: flowID, FlowType: paradeTerminalFlowType,
		FlowTimeoutSeconds: 40, StartStepType: paradeTerminalStep,
		StepOptions: &dexpb.StepOptions{SkipWaitFor: true},
		FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{
			Attributes: []*dexpb.AttributeWrite{indexedKeywordAttribute("stage", "started")},
		}, workerTarget),
	})
	require.NoError(t, err)
	firstResult, err := waitForParadeFlow(ctx, runtime.FlowClient, flowID)
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, firstResult.GetFlowStatus())

	resetResponse, err := runtime.FlowClient.ResetFlow(ctx, &dexpb.ResetFlowRequest{
		FlowId: flowID, RunId: startResponse.GetRunId(), ResetType: dexpb.FlowResetType_FLOW_RESET_TYPE_BEGINNING,
	})
	require.NoError(t, err)
	select {
	case <-handler.secondStarted:
	case <-ctx.Done():
		require.FailNow(t, "reset producer did not start", ctx.Err().Error())
	}
	require.Eventually(t, func() bool {
		searchResponse, searchErr := runtime.FlowClient.SearchFlows(ctx, &dexpb.SearchFlowsRequest{
			Query: fmt.Sprintf(`FlowID:"%s"`, flowID), PageSize: 10,
		})
		return searchErr == nil && len(searchResponse.GetFlowRuns()) == 1 &&
			searchResponse.GetFlowRuns()[0].GetRunId() == resetResponse.GetRunId() &&
			searchResponse.GetFlowRuns()[0].GetFlowStatus() == dexpb.FlowStatus_FLOW_STATUS_RUNNING &&
			searchAttributeString(searchResponse.GetFlowRuns()[0].GetSearchAttributes(), "later") == ""
	}, 10*time.Second, 20*time.Millisecond)

	close(handler.releaseSecond)
	resetResult, err := waitForParadeFlow(ctx, runtime.FlowClient, flowID)
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, resetResult.GetFlowStatus())
}

func TestParadeDBTerminalIgnoresLateNonLockingRPCTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	handler := newParadeRPCHandler()
	workerTarget := startWorker(t, handler)
	runtime := startDexService(t, DexServiceTestConfig{
		BackendType: service.BackendTypeTemporal,
		FlowIndex:   paradeTestFlowIndexConfig(fmt.Sprintf("dex_rpc_%d", time.Now().UnixNano())),
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	applyParadeSchema(t, ctx, runtime, "stage", "rpc")

	flowID := paradeTerminalFlowType + "-" + uuid.NewString()
	_, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId: newRequestID(), FlowId: flowID, FlowType: paradeTerminalFlowType,
		FlowTimeoutSeconds: 25,
		FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{
			Attributes: []*dexpb.AttributeWrite{indexedKeywordAttribute("stage", "started")},
		}, workerTarget),
	})
	require.NoError(t, err)
	rpcError := make(chan error, 1)
	go invokeParadeRPC(ctx, runtime.FlowClient, flowID, rpcError)
	select {
	case <-handler.started:
	case <-ctx.Done():
		require.FailNow(t, "non-locking RPC did not start", ctx.Err().Error())
	}
	_, err = runtime.FlowClient.StopFlow(ctx, &dexpb.StopFlowRequest{
		FlowId: flowID, StopType: dexpb.StopType_STOP_TYPE_CANCEL, Reason: "race with RPC",
	})
	require.NoError(t, err)
	waitResponse, err := waitForParadeFlow(ctx, runtime.FlowClient, flowID)
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_CANCELED, waitResponse.GetFlowStatus())

	close(handler.release)
	select {
	case err = <-rpcError:
		require.Error(t, err)
	case <-ctx.Done():
		require.FailNow(t, "non-locking RPC did not return", ctx.Err().Error())
	}
	searchResponse, err := runtime.FlowClient.SearchFlows(ctx, &dexpb.SearchFlowsRequest{
		Query: fmt.Sprintf(`FlowID:"%s"`, flowID), PageSize: 10,
	})
	require.NoError(t, err)
	require.Len(t, searchResponse.GetFlowRuns(), 1)
	require.Equal(t, "", searchAttributeString(searchResponse.GetFlowRuns()[0].GetSearchAttributes(), "rpc"))
}

func runParadeTerminalTest(
	t *testing.T,
	stopType dexpb.StopType,
	expected dexpb.FlowStatus,
	completion bool,
) {
	schema := fmt.Sprintf("dex_terminal_%d", time.Now().UnixNano())
	handler := newParadeTerminalHandler(completion)
	workerTarget := startWorker(t, handler)
	runtime := startDexService(t, DexServiceTestConfig{
		BackendType: service.BackendTypeTemporal,
		FlowIndex:   paradeTestFlowIndexConfig(schema),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	applyParadeTestSchema(t, ctx, runtime)

	flowID := paradeTerminalFlowType + "-" + uuid.NewString()
	startResponse, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           paradeTerminalFlowType,
		FlowTimeoutSeconds: 40,
		StartStepType:      paradeTerminalStep,
		StepOptions:        &dexpb.StepOptions{SkipWaitFor: true},
		FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{
			Attributes: []*dexpb.AttributeWrite{indexedKeywordAttribute("stage", "started")},
		}, workerTarget),
	})
	require.NoError(t, err)

	select {
	case <-handler.started:
	case <-ctx.Done():
		require.FailNow(t, "producer did not start", ctx.Err().Error())
	}
	if stopType != dexpb.StopType_STOP_TYPE_UNSPECIFIED {
		_, err = runtime.FlowClient.StopFlow(ctx, &dexpb.StopFlowRequest{
			FlowId: flowID, RunId: startResponse.GetRunId(), StopType: stopType,
			Reason: "integration terminal request",
		})
		require.NoError(t, err)
	}

	close(handler.release)
	waitResponse, err := runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowID})
	require.NoError(t, err)
	require.Equal(t, expected, waitResponse.GetFlowStatus())
	require.Zero(t, handler.unexpectedRuns.Load())

	searchResponse, err := runtime.FlowClient.SearchFlows(ctx, &dexpb.SearchFlowsRequest{
		Query: fmt.Sprintf(`FlowID:"%s"`, flowID), PageSize: 10,
	})
	require.NoError(t, err)
	require.Len(t, searchResponse.GetFlowRuns(), 1)
	flow := searchResponse.GetFlowRuns()[0]
	require.Equal(t, expected, flow.GetFlowStatus())
	require.Equal(t, "producer-finished", searchAttributeString(flow.GetSearchAttributes(), "stage"))
}

func paradeTestFlowIndexConfig(schema string) config.FlowIndexConfig {
	dsn := os.Getenv("DEX_PARADEDB_DSN")
	if dsn == "" {
		dsn = "postgres://dex:dex@127.0.0.1:5433/dex?sslmode=disable"
	}
	return config.FlowIndexConfig{
		Backend: config.FlowIndexBackendParadeDB,
		ParadeDB: config.ParadeDBConfig{
			DSN: dsn, Schema: schema, Table: "flow_index", MaxConnections: 4,
		},
	}
}

func applyParadeTestSchema(t *testing.T, ctx context.Context, runtime *integRuntime) {
	t.Helper()
	applyParadeSchema(t, ctx, runtime, "stage")
}

func applyParadeSchema(
	t *testing.T,
	ctx context.Context,
	runtime *integRuntime,
	fieldNames ...string,
) {
	t.Helper()
	fields := make([]*dexpb.FlowIndexField, 0, len(fieldNames))
	for _, fieldName := range fieldNames {
		fields = append(fields, &dexpb.FlowIndexField{Name: fieldName, Type: dexpb.IndexType_INDEX_TYPE_KEYWORD})
	}
	_, err := runtime.AdminClient.ApplyFlowIndexSchema(ctx, &dexpb.ApplyFlowIndexSchemaRequest{
		DefinitionVersion: 1,
		Attributes:        fields,
	})
	require.NoError(t, err)
}

func startParadeTestFlow(
	t *testing.T,
	ctx context.Context,
	runtime *integRuntime,
	workerTarget *dexpb.WorkerTarget,
) string {
	t.Helper()
	flowID := paradeTerminalFlowType + "-" + uuid.NewString()
	_, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId: newRequestID(), FlowId: flowID, FlowType: paradeTerminalFlowType,
		FlowTimeoutSeconds: 25, StartStepType: paradeTerminalStep,
		StepOptions: &dexpb.StepOptions{SkipWaitFor: true},
		FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{
			Attributes: []*dexpb.AttributeWrite{indexedKeywordAttribute("stage", "started")},
		}, workerTarget),
	})
	require.NoError(t, err)
	return flowID
}

func searchAttributeString(attributes []*dexpb.KV, key string) string {
	for _, attribute := range attributes {
		if attribute.GetKey() == key {
			return attribute.GetValue().GetStringValue()
		}
	}
	return ""
}

func invokeParadeRPC(
	ctx context.Context,
	flowClient dexpb.FlowServiceClient,
	flowID string,
	result chan<- error,
) {
	_, err := flowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
		RequestId: newRequestID(), FlowId: flowID, RpcName: "late-rpc", TimeoutSeconds: 20,
	})
	result <- err
}

func waitForParadeFlow(
	ctx context.Context,
	flowClient dexpb.FlowServiceClient,
	flowID string,
) (*dexpb.WaitForFlowResponse, error) {
	for {
		response, err := flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowID})
		if status.Code(err) != codes.DeadlineExceeded || ctx.Err() != nil {
			return response, err
		}
	}
}
