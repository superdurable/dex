// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package dex

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/blob-cache-go/blobcache"
	"github.com/superdurable/dex/sdk-go/dex/ptr"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	clientTestStatus = DefineAttribute[string](
		"status",
		Indexed(AttributeIndex{Type: IndexKeyword}),
		SyncToAttributeStore(),
	)
	clientTestItems    = DefineAttributeMap[int]("items")
	clientTestCommands = DefineChannel[string]("commands")
	clientTestByOrder  = DefineChannelMap[string]("commands-by-order")
)

type clientTestStep struct {
	StepDefaultsNoWaitFor[clientTestInput]
}

type clientTestInput struct {
	OrderID string
}

type clientTestRPCInput struct {
	Status string
}

type clientTestRPCOutput struct {
	Status string
}

func (clientTestStep) Execute(Context, clientTestInput) (*StepDecision, error) {
	return DeadEnd(), nil
}

type clientTestFlow struct {
	FlowDefaults
}

type clientNoStartFlow struct {
	FlowDefaults
}

func (clientTestFlow) GetSteps() []StepDef {
	return []StepDef{DefineStartStep(clientTestStep{})}
}

func (clientTestFlow) GetPersistenceSchema() PersistenceSchema {
	return PersistenceSchema{
		Attributes: []AttributeDef{clientTestStatus, clientTestItems},
		Channels:   []ChannelDef{clientTestCommands, clientTestByOrder},
	}
}

func (clientTestFlow) Update(
	Context,
	clientTestRPCInput,
) (*RPCResult[clientTestRPCOutput], error) {
	return &RPCResult[clientTestRPCOutput]{Output: clientTestRPCOutput{}}, nil
}

func (clientNoStartFlow) GetSteps() []StepDef {
	return nil
}

func (clientNoStartFlow) GetPersistenceSchema() PersistenceSchema {
	return PersistenceSchema{}
}

type clientTestFlowService struct {
	dexpb.UnimplementedFlowServiceServer
	startRequest         *dexpb.StartFlowRequest
	publishRequest       *dexpb.PublishToChannelRequest
	setRequests          []*dexpb.SetAttributesRequest
	invokeRequest        *dexpb.InvokeRPCRequest
	waitAttributeRequest *dexpb.WaitForAttributeRequest
	stopRequest          *dexpb.StopFlowRequest
	waitFlowRequest      *dexpb.WaitForFlowRequest
	flowSummaryRequest   *dexpb.GetFlowSummaryRequest
	searchRequest        *dexpb.SearchFlowsRequest
	resetRequest         *dexpb.ResetFlowRequest
	skipTimerRequest     *dexpb.SkipTimerRequest
	updateConfigRequest  *dexpb.UpdateFlowConfigRequest
	waitStepRequest      *dexpb.WaitForStepCompletionRequest
	continueAsNewRequest *dexpb.TriggerContinueAsNewRequest
	getAttributesRequest *dexpb.GetAttributesRequest
}

func (service *clientTestFlowService) StartFlow(
	_ context.Context,
	request *dexpb.StartFlowRequest,
) (*dexpb.StartFlowResponse, error) {
	service.startRequest = request
	if request.FlowId == "duplicate" {
		return nil, clientTestServiceError(
			codes.AlreadyExists,
			dexpb.ErrorSubStatus_ERROR_SUB_STATUS_FLOW_ALREADY_STARTED,
			"Flow already started",
			nil,
		)
	}
	return &dexpb.StartFlowResponse{RunId: "run-1"}, nil
}

func (service *clientTestFlowService) PublishToChannel(
	_ context.Context,
	request *dexpb.PublishToChannelRequest,
) (*emptypb.Empty, error) {
	service.publishRequest = request
	if request.FlowId == "inactive" {
		return nil, clientTestMissingFlowError()
	}
	return &emptypb.Empty{}, nil
}

func (service *clientTestFlowService) GetAttributes(
	_ context.Context,
	request *dexpb.GetAttributesRequest,
) (*dexpb.GetAttributesResponse, error) {
	service.getAttributesRequest = request
	if request.FlowId == "missing-read" {
		return nil, clientTestMissingFlowError()
	}
	attributes := make([]*dexpb.KV, 0, len(request.Keys))
	for _, key := range request.Keys {
		var value *dexpb.Value
		if key == "status" {
			value = &dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: "ready"}}
		} else {
			value = &dexpb.Value{Kind: &dexpb.Value_IntValue{IntValue: 3}}
		}
		attributes = append(attributes, &dexpb.KV{Key: key, Value: value})
	}
	return &dexpb.GetAttributesResponse{Attributes: attributes}, nil
}

func (service *clientTestFlowService) SetAttributes(
	_ context.Context,
	request *dexpb.SetAttributesRequest,
) (*emptypb.Empty, error) {
	service.setRequests = append(service.setRequests, request)
	if request.FlowId == "inactive" {
		return nil, clientTestMissingFlowError()
	}
	return &emptypb.Empty{}, nil
}

func (service *clientTestFlowService) LoadBlobs(
	_ context.Context,
	request *dexpb.LoadBlobsRequest,
) (*dexpb.LoadBlobsResponse, error) {
	values := make(map[string]*dexpb.Value, len(request.Values))
	for _, value := range request.Values {
		blobID, found, err := getBlobID(value)
		if err != nil {
			return nil, err
		}
		if found {
			if blobID.isObjectOrString {
				values[blobID.value] = &dexpb.Value{Kind: &dexpb.Value_ObjValue{
					ObjValue: &dexpb.EncodedObject{Encoding: rawBytesEncoding, Payload: []byte("done")},
				}}
			} else {
				values[blobID.value] = &dexpb.Value{
					Kind: &dexpb.Value_StringValue{StringValue: "hydrated"},
				}
			}
		}
	}
	return &dexpb.LoadBlobsResponse{Values: values}, nil
}

func (service *clientTestFlowService) InvokeRPC(
	_ context.Context,
	request *dexpb.InvokeRPCRequest,
) (*dexpb.InvokeRPCResponse, error) {
	service.invokeRequest = request
	if request.FlowId == "worker" {
		return nil, clientTestServiceError(
			codes.FailedPrecondition,
			dexpb.ErrorSubStatus_ERROR_SUB_STATUS_WORKER_API_ERROR,
			"Worker invocation failed",
			&WorkerError{
				Code:   codes.InvalidArgument,
				Type:   "ValidationError",
				Detail: "invalid input",
			},
		)
	}
	if request.FlowId == "lock" {
		return nil, clientTestServiceError(
			codes.Aborted,
			dexpb.ErrorSubStatus_ERROR_SUB_STATUS_WORKER_API_ERROR,
			"RPC lock conflict",
			nil,
		)
	}
	if request.FlowId == "inactive" {
		return nil, clientTestMissingFlowError()
	}
	output, err := encodeValue(clientTestRPCOutput{Status: "updated"})
	if err != nil {
		return nil, err
	}
	return &dexpb.InvokeRPCResponse{Output: output}, nil
}

func (service *clientTestFlowService) WaitForAttribute(
	_ context.Context,
	request *dexpb.WaitForAttributeRequest,
) (*emptypb.Empty, error) {
	service.waitAttributeRequest = request
	if request.FlowId == "inactive" {
		return nil, clientTestMissingFlowError()
	}
	return &emptypb.Empty{}, nil
}

func (service *clientTestFlowService) StopFlow(
	_ context.Context,
	request *dexpb.StopFlowRequest,
) (*emptypb.Empty, error) {
	service.stopRequest = request
	if request.FlowId == "inactive" {
		return nil, clientTestMissingFlowError()
	}
	return &emptypb.Empty{}, nil
}

func (service *clientTestFlowService) WaitForFlow(
	_ context.Context,
	request *dexpb.WaitForFlowRequest,
) (*dexpb.FlowResult, error) {
	service.waitFlowRequest = request
	if request.FlowId == "timeout" {
		return nil, clientTestServiceError(
			codes.DeadlineExceeded,
			dexpb.ErrorSubStatus_ERROR_SUB_STATUS_LONG_POLL_TIME_OUT,
			"long poll timed out",
			nil,
		)
	}
	if request.FlowId == "missing-read" {
		return nil, clientTestMissingFlowError()
	}
	if request.FlowId == "uncompleted" {
		return &dexpb.FlowResult{
			FlowStatus:   dexpb.FlowStatus_FLOW_STATUS_FAILED,
			ErrorType:    dexpb.FlowErrorType_FLOW_ERROR_TYPE_WORKER_API_FAIL,
			ErrorMessage: "worker failed",
			Results: []*dexpb.StepCompletionOutput{{
				CompletedStepType:        "dex.clientTestStep",
				CompletedStepExecutionId: "dex.clientTestStep-1",
				CompletedStepOutput: &dexpb.Value{
					Kind: &dexpb.Value_StringValue{StringValue: "partial"},
				},
			}},
		}, nil
	}
	if request.FlowId == "multi" {
		return &dexpb.FlowResult{
			FlowStatus: dexpb.FlowStatus_FLOW_STATUS_COMPLETED,
			Results: []*dexpb.StepCompletionOutput{
				{
					CompletedStepType:        "First",
					CompletedStepExecutionId: "First-1",
					CompletedStepOutput: &dexpb.Value{
						Kind: &dexpb.Value_InternalBlobIdForStringValue{
							InternalBlobIdForStringValue: "first-completion-blob",
						},
					},
				},
				{
					CompletedStepType:        "Second",
					CompletedStepExecutionId: "Second-2",
					CompletedStepOutput: &dexpb.Value{
						Kind: &dexpb.Value_InternalBlobIdForObjValue{
							InternalBlobIdForObjValue: "second-completion-blob",
						},
					},
				},
			},
		}, nil
	}
	return &dexpb.FlowResult{
		FlowStatus: dexpb.FlowStatus_FLOW_STATUS_COMPLETED,
		Results: []*dexpb.StepCompletionOutput{{
			CompletedStepType:        "dex.clientTestStep",
			CompletedStepExecutionId: "dex.clientTestStep-1",
			CompletedStepOutput: &dexpb.Value{
				Kind: &dexpb.Value_InternalBlobIdForStringValue{
					InternalBlobIdForStringValue: "completion-blob",
				},
			},
		}},
	}, nil
}

func (service *clientTestFlowService) GetFlowSummary(
	_ context.Context,
	request *dexpb.GetFlowSummaryRequest,
) (*dexpb.GetFlowSummaryResponse, error) {
	service.flowSummaryRequest = request
	return &dexpb.GetFlowSummaryResponse{
		FlowExecutionId: &dexpb.FlowExecutionID{
			FlowId: request.FlowId,
			RunId:  "run-uncompleted",
		},
	}, nil
}

func (service *clientTestFlowService) SearchFlows(
	_ context.Context,
	request *dexpb.SearchFlowsRequest,
) (*dexpb.SearchFlowsResponse, error) {
	service.searchRequest = request
	return &dexpb.SearchFlowsResponse{
		FlowRuns: []*dexpb.SearchFlowsResponseEntry{{
			FlowId:     "order-1",
			RunId:      "run-1",
			FlowType:   "dex.clientTestFlow",
			FlowStatus: dexpb.FlowStatus_FLOW_STATUS_RUNNING,
			StartTime:  timestamppb.New(time.Unix(100, 0)),
			IndexedAttributes: []*dexpb.KV{{
				Key: "status",
				Value: &dexpb.Value{Kind: &dexpb.Value_InternalBlobIdForStringValue{
					InternalBlobIdForStringValue: "search-blob",
				}},
			}},
		}},
		NextPageToken: "next",
	}, nil
}

func (service *clientTestFlowService) ResetFlow(
	_ context.Context,
	request *dexpb.ResetFlowRequest,
) (*dexpb.ResetFlowResponse, error) {
	service.resetRequest = request
	if request.FlowId == "missing-read" {
		return nil, clientTestMissingFlowError()
	}
	return &dexpb.ResetFlowResponse{RunId: "run-2"}, nil
}

func (service *clientTestFlowService) SkipTimer(
	_ context.Context,
	request *dexpb.SkipTimerRequest,
) (*emptypb.Empty, error) {
	service.skipTimerRequest = request
	if request.FlowId == "inactive" {
		return nil, clientTestMissingFlowError()
	}
	return &emptypb.Empty{}, nil
}

func (service *clientTestFlowService) UpdateFlowConfig(
	_ context.Context,
	request *dexpb.UpdateFlowConfigRequest,
) (*emptypb.Empty, error) {
	service.updateConfigRequest = request
	if request.FlowId == "inactive" {
		return nil, clientTestMissingFlowError()
	}
	return &emptypb.Empty{}, nil
}

func (service *clientTestFlowService) WaitForStepCompletion(
	_ context.Context,
	request *dexpb.WaitForStepCompletionRequest,
) (*dexpb.WaitForStepCompletionResponse, error) {
	service.waitStepRequest = request
	if request.FlowId == "inactive" {
		return nil, clientTestMissingFlowError()
	}
	return &dexpb.WaitForStepCompletionResponse{}, nil
}

func (service *clientTestFlowService) TriggerContinueAsNew(
	_ context.Context,
	request *dexpb.TriggerContinueAsNewRequest,
) (*emptypb.Empty, error) {
	service.continueAsNewRequest = request
	if request.FlowId == "inactive" {
		return nil, clientTestMissingFlowError()
	}
	return &emptypb.Empty{}, nil
}

func (service *clientTestFlowService) HealthCheck(
	context.Context,
	*emptypb.Empty,
) (*dexpb.HealthInfo, error) {
	return &dexpb.HealthInfo{Condition: "SERVING", Hostname: "test", Duration: 1}, nil
}

func clientTestServiceError(
	code codes.Code,
	subStatus dexpb.ErrorSubStatus,
	detail string,
	worker *WorkerError,
) error {
	response := &dexpb.ServiceErrorResponse{SubStatus: subStatus, Detail: detail}
	if worker != nil {
		response.OriginalWorkerErrorStatus = int32(worker.Code)
		response.OriginalWorkerErrorType = worker.Type
		response.OriginalWorkerErrorDetail = worker.Detail
	}
	withDetails, err := status.New(code, detail).WithDetails(response)
	if err != nil {
		panic(err)
	}
	return withDetails.Err()
}

func clientTestMissingFlowError() error {
	return clientTestServiceError(
		codes.NotFound,
		dexpb.ErrorSubStatus_ERROR_SUB_STATUS_FLOW_NOT_EXISTS,
		"Flow does not exist",
		nil,
	)
}

func TestClientFlowAndPersistenceTransport(t *testing.T) {
	client, service := newClientIntegration(t)
	ctx := context.Background()
	initial, err := InitialAttribute(clientTestStatus, "created")
	require.NoError(t, err)
	runID, err := client.StartFlow(
		ctx,
		clientTestFlow{},
		"order-1",
		clientTestInput{OrderID: "order-1"},
		StartFlowOptions{
			Attributes: []InitialAttributeDef{initial},
			RequestID:  ptr.Any("business-order-1"),
		},
	)
	require.NoError(t, err)
	require.Equal(t, "run-1", runID)
	require.Equal(t, "business-order-1", service.startRequest.RequestId)
	require.Equal(t, int32(0), service.startRequest.FlowTimeoutSeconds)
	require.Equal(t, "dex.clientTestFlow", service.startRequest.FlowType)
	require.Equal(t, "dex.clientTestStep", service.startRequest.StartStepType)
	require.Equal(t, "worker.test:8803", service.startRequest.FlowStartOptions.FlowConfigOverride.WorkerTarget.Address)
	require.True(t, service.startRequest.FlowStartOptions.Attributes[0].GetSyncConfig().GetEnabled())

	require.NoError(t, client.PublishToChannel(ctx, "order-1", clientTestCommands, "approve", "ship"))
	require.Len(t, service.publishRequest.Messages, 2)
	require.NoError(t, client.PublishToChannelMap(ctx, "order-1", clientTestByOrder, "order-1", "pack"))
	require.Equal(t, "commands-by-order/order-1", service.publishRequest.Messages[0].ChannelName)

	var status string
	found, err := client.GetAttribute(ctx, "order-1", clientTestStatus, &status)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "ready", status)
	var quantity int
	found, err = client.GetAttributeMapInstance(ctx, "order-1", clientTestItems, "sku-1", &quantity)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, 3, quantity)

	require.NoError(t, client.SetAttribute(ctx, "order-1", clientTestStatus, "done"))
	require.NoError(t, client.SetAttributeMapInstance(ctx, "order-1", clientTestItems, "sku-1", 4))
	require.Len(t, service.setRequests, 2)
	require.True(t, service.setRequests[0].Attributes[0].GetSyncConfig().GetEnabled())
	require.Nil(t, service.setRequests[1].Attributes[0].SyncConfig)
	require.NotEqual(t, service.setRequests[0].RequestId, service.setRequests[1].RequestId)
	for _, request := range service.setRequests {
		_, err := uuid.Parse(request.RequestId)
		require.NoError(t, err)
	}

	values, err := client.GetAttributes(ctx, "order-1", clientTestStatus)
	require.NoError(t, err)
	require.Contains(t, values, "status")
	require.NoError(t, client.SetAttributes(ctx, "order-1", AttributeWrite{
		Name:                 "status",
		Value:                "batched",
		SyncToAttributeStore: true,
	}))
	require.Len(t, service.setRequests, 3)
	require.True(t, service.setRequests[2].Attributes[0].GetSyncConfig().GetEnabled())

	require.NoError(t, client.WaitForAttributeEqual(
		ctx,
		"order-1",
		clientTestStatus,
		"done",
		WaitOptions{},
	))
	_, err = uuid.Parse(service.waitAttributeRequest.RequestId)
	require.NoError(t, err)
	require.Equal(t, int32(0), service.waitAttributeRequest.WaitTimeSeconds)
}

func TestClientConstructionAndLocalValidation(t *testing.T) {
	registry, err := NewRegistry([]Flow{clientTestFlow{}, clientNoStartFlow{}})
	require.NoError(t, err)
	cache, err := blobcache.New(&blobcache.Config{
		Dir:      t.TempDir(),
		MaxBytes: 1 << 20,
	})
	require.NoError(t, err)
	defer func() { require.NoError(t, cache.Close()) }()
	require.Panics(t, func() { _, _ = NewClient(nil, cache, ClientOptions{}) })
	require.Panics(t, func() { _, _ = NewClient(registry, nil, ClientOptions{}) })
	_, err = NewClient(registry, cache, ClientOptions{
		WorkerTarget: &WorkerTarget{Address: "https://worker"},
	})
	require.ErrorContains(t, err, "plaintext")

	target := &WorkerTarget{Address: "worker.test:8803"}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client, err := NewClient(registry, cache, ClientOptions{
		WorkerTarget: target,
		Logger:       logger,
	})
	require.NoError(t, err)
	require.Same(t, logger, client.logger)
	target.Address = "mutated:8803"
	require.Equal(t, "worker.test:8803", client.workerTarget.Address)
	require.NoError(t, client.Close())
	cached, err := cache.Put("still-owned", []byte("payload"))
	require.NoError(t, err)
	require.True(t, cached)

	integrationClient, service := newClientIntegration(t)
	_, err = integrationClient.StartFlow(
		context.Background(),
		clientNoStartFlow{},
		"rpc-only",
		nil,
		StartFlowOptions{},
	)
	require.NoError(t, err)
	require.Empty(t, service.startRequest.StartStepType)
	_, err = integrationClient.StartFlow(
		context.Background(),
		clientNoStartFlow{},
		"invalid",
		clientTestInput{},
		StartFlowOptions{},
	)
	require.ErrorContains(t, err, "requires nil input")
}

func TestClientRPCResultsAndAdministrativeTransport(t *testing.T) {
	client, service := newClientIntegration(t)
	ctx := context.Background()
	var output clientTestRPCOutput
	err := client.InvokeRPC(
		ctx,
		"order-1",
		clientTestFlow{}.Update,
		clientTestRPCInput{Status: "updated"},
		&output,
		InvokeOptions{LockAttributes: []AttributeLock{LockAttribute(clientTestStatus)}},
	)
	require.NoError(t, err)
	require.Equal(t, "updated", output.Status)
	_, err = uuid.Parse(service.invokeRequest.RequestId)
	require.NoError(t, err)

	result, err := client.WaitForFlow(ctx, "order-1", WaitForFlowOptions{NeedsResults: true})
	require.NoError(t, err)
	require.Equal(t, FlowCompleted, result.Status)
	var completion string
	require.NoError(t, result.Completions[0].Output.Decode(&completion))
	require.Equal(t, "hydrated", completion)
	require.NoError(t, result.DecodeSingleOutput(&completion))

	multi, err := client.WaitForFlow(ctx, "multi", WaitForFlowOptions{NeedsResults: true})
	require.NoError(t, err)
	require.Len(t, multi.Completions, 2)
	require.Equal(t, "First", multi.Completions[0].StepType)
	require.Equal(t, "Second-2", multi.Completions[1].StepExecutionID)
	var firstCompletion string
	require.NoError(t, multi.Completions[0].Output.Decode(&firstCompletion))
	require.Equal(t, "hydrated", firstCompletion)
	var secondCompletion []byte
	require.NoError(t, multi.Completions[1].Output.Decode(&secondCompletion))
	require.Equal(t, []byte("done"), secondCompletion)
	require.ErrorContains(t, multi.DecodeSingleOutput(&completion), "exactly one Step output")
	require.ErrorContains(t, (FlowResult{Status: FlowCompleted}).DecodeSingleOutput(&completion), "found 0")

	page, err := client.SearchFlows(ctx, "status = 'ready'", 10, "")
	require.NoError(t, err)
	require.Equal(t, "next", page.NextPageToken)
	require.Equal(t, FlowRunning, page.Flows[0].Status)
	require.Equal(t, time.Unix(100, 0).UTC(), page.Flows[0].StartedAt)
	require.True(t, page.Flows[0].ClosedAt.IsZero())
	var searchStatus string
	require.NoError(t, page.Flows[0].IndexedAttributes["status"].Decode(&searchStatus))
	require.Equal(t, "hydrated", searchStatus)

	require.NoError(t, client.StopFlow(ctx, "order-1", StopOptions{}))
	require.Equal(t, dexpb.StopType_STOP_TYPE_CANCEL, service.stopRequest.StopType)
	newRunID, err := client.TimeTravel(ctx, "order-1", TimeTravelOptions{
		Type:              TimeTravelToBeginning,
		SkipWritesReapply: true,
	})
	require.NoError(t, err)
	require.Equal(t, "run-2", newRunID)
	require.True(t, service.resetRequest.GetSkipWritesReapply())
	newRunID, err = client.TimeTravel(ctx, "order-1", TimeTravelOptions{
		Type:            TimeTravelByStepExecutionID,
		StepExecutionID: "dex.clientTestStep-1",
		StepMethod:      TimeTravelStepExecute,
	})
	require.NoError(t, err)
	require.Equal(t, "run-2", newRunID)
	require.Equal(t, "dex.clientTestStep-1", service.resetRequest.GetStepExecutionId())
	require.Equal(
		t,
		dexpb.FlowResetStepMethod_FLOW_RESET_STEP_METHOD_EXECUTE,
		service.resetRequest.GetStepMethod(),
	)
	require.NoError(t, client.SkipTimer(
		ctx,
		"order-1",
		StepExecutionID{StepType: GetFinalStepType(clientTestStep{})},
		TimerID{ConditionID: "timeout"},
	))
	require.Equal(t, "dex.clientTestStep-1", service.skipTimerRequest.StepExecutionId)

	require.NoError(t, client.UpdateFlowConfig(ctx, "order-1", FlowConfig{
		ContinueAsNewThreshold: ptr.Any(int32(100)),
		AttributeStoreName:     ptr.Any("reporting"),
	}))
	require.Nil(t, service.updateConfigRequest.FlowConfig.WorkerTarget)
	require.Equal(t, "reporting", service.updateConfigRequest.FlowConfig.GetAttributeSyncConfigName())
	require.NoError(t, client.WaitForStepCompletion(
		ctx,
		"order-1",
		StepExecutionID{StepType: GetFinalStepType(clientTestStep{})},
		WaitOptions{},
	))
	require.Equal(t, "1", service.waitStepRequest.StepExecutionNumber)
	_, err = uuid.Parse(service.waitStepRequest.RequestId)
	require.NoError(t, err)
	require.NoError(t, client.TriggerContinueAsNew(ctx, "order-1"))
	require.Equal(t, "order-1", service.continueAsNewRequest.FlowId)
	health, err := client.HealthCheck(ctx)
	require.NoError(t, err)
	require.Equal(t, "SERVING", health.Condition)

	require.NoError(t, client.Close())
	require.NoError(t, client.Close())
	_, err = client.HealthCheck(ctx)
	require.ErrorIs(t, err, errClientClosed)

	openClient, _ := newClientIntegration(t)
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = openClient.HealthCheck(canceledCtx)
	require.ErrorIs(t, err, context.Canceled)
}

func TestClientExplicitServiceErrors(t *testing.T) {
	client, _ := newClientIntegration(t)
	ctx := context.Background()

	_, err := client.StartFlow(
		ctx,
		clientTestFlow{},
		"duplicate",
		clientTestInput{},
		StartFlowOptions{},
	)
	var duplicate *FlowAlreadyStartedError
	require.ErrorAs(t, err, &duplicate)
	require.Equal(t, "duplicate", duplicate.FlowID)

	var value string
	_, err = client.GetAttribute(ctx, "missing-read", clientTestStatus, &value)
	var missing *FlowNotFoundError
	require.ErrorAs(t, err, &missing)
	require.Equal(t, "GetAttribute", missing.Op)
	_, err = client.WaitForFlow(ctx, "missing-read", WaitForFlowOptions{})
	require.ErrorAs(t, err, &missing)
	_, err = client.TimeTravel(ctx, "missing-read", TimeTravelOptions{Type: TimeTravelToBeginning})
	require.ErrorAs(t, err, &missing)

	activeCalls := []struct {
		name string
		call func() error
	}{
		{name: "publish", call: func() error {
			return client.PublishToChannel(ctx, "inactive", clientTestCommands, "value")
		}},
		{name: "set attribute", call: func() error {
			return client.SetAttribute(ctx, "inactive", clientTestStatus, "value")
		}},
		{name: "wait for attribute", call: func() error {
			return client.WaitForAttributeEqual(
				ctx,
				"inactive",
				clientTestStatus,
				"value",
				WaitOptions{},
			)
		}},
		{name: "stop", call: func() error {
			return client.StopFlow(ctx, "inactive", StopOptions{})
		}},
		{name: "skip timer", call: func() error {
			return client.SkipTimer(
				ctx,
				"inactive",
				StepExecutionID{StepType: GetFinalStepType(clientTestStep{})},
				TimerID{ConditionID: "timer"},
			)
		}},
		{name: "update config", call: func() error {
			return client.UpdateFlowConfig(ctx, "inactive", FlowConfig{})
		}},
		{name: "wait for step", call: func() error {
			return client.WaitForStepCompletion(
				ctx,
				"inactive",
				StepExecutionID{StepType: GetFinalStepType(clientTestStep{})},
				WaitOptions{},
			)
		}},
		{name: "continue as new", call: func() error {
			return client.TriggerContinueAsNew(ctx, "inactive")
		}},
	}
	for _, testCase := range activeCalls {
		t.Run(testCase.name, func(t *testing.T) {
			var inactive *FlowNotActiveError
			require.ErrorAs(t, testCase.call(), &inactive)
			require.Equal(t, "inactive", inactive.FlowID)
		})
	}

	var output clientTestRPCOutput
	err = client.InvokeRPC(
		ctx,
		"worker",
		clientTestFlow{}.Update,
		clientTestRPCInput{},
		&output,
		InvokeOptions{},
	)
	var worker *WorkerInvocationError
	require.ErrorAs(t, err, &worker)
	require.Equal(t, codes.InvalidArgument, worker.Worker.Code)
	require.Equal(t, "ValidationError", worker.Worker.Type)
	require.Equal(t, "invalid input", worker.Worker.Detail)

	err = client.InvokeRPC(
		ctx,
		"lock",
		clientTestFlow{}.Update,
		clientTestRPCInput{},
		&output,
		InvokeOptions{},
	)
	var conflict *RPCLockConflictError
	require.ErrorAs(t, err, &conflict)

	err = client.InvokeRPC(
		ctx,
		"inactive",
		clientTestFlow{}.Update,
		clientTestRPCInput{},
		&output,
		InvokeOptions{},
	)
	var inactive *FlowNotActiveError
	require.ErrorAs(t, err, &inactive)

	_, err = client.WaitForFlow(ctx, "timeout", WaitForFlowOptions{})
	var timeout *LongPollTimeoutError
	require.ErrorAs(t, err, &timeout)
	require.Equal(t, "timeout", timeout.FlowID)
	require.Equal(t, codes.DeadlineExceeded, status.Code(timeout))

	uncompleted, err := client.WaitForFlow(ctx, "uncompleted", WaitForFlowOptions{NeedsResults: true})
	require.NoError(t, err)
	require.Equal(t, FlowFailed, uncompleted.Status)
	require.Equal(t, FlowErrorWorkerMethod, uncompleted.ErrorType)
	require.Equal(t, "worker failed", uncompleted.ErrorMessage)
	require.Len(t, uncompleted.Completions, 1)
	var partial string
	require.NoError(t, uncompleted.Completions[0].Output.Decode(&partial))
	require.Equal(t, "partial", partial)
}

func newClientIntegration(t *testing.T) (*Client, *clientTestFlowService) {
	t.Helper()
	registry, err := NewRegistry([]Flow{clientTestFlow{}, clientNoStartFlow{}})
	require.NoError(t, err)
	cache, err := blobcache.New(&blobcache.Config{
		Dir:      t.TempDir(),
		MaxBytes: 1 << 20,
	})
	require.NoError(t, err)
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	service := &clientTestFlowService{}
	dexpb.RegisterFlowServiceServer(server, service)
	serveResult := make(chan error, 1)
	go func() { serveResult <- server.Serve(listener) }()
	connection, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
	)
	require.NoError(t, err)
	client := newClient(
		registry,
		cache,
		connection,
		&WorkerTarget{Address: "worker.test:8803"},
		nil,
	)
	t.Cleanup(func() {
		require.NoError(t, client.Close())
		server.Stop()
		serveErr := <-serveResult
		if serveErr != nil && !errors.Is(serveErr, grpc.ErrServerStopped) {
			t.Errorf("serve FlowService: %v", serveErr)
		}
		if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Errorf("close FlowService listener: %v", err)
		}
		require.NoError(t, cache.Close())
	})
	return client, service
}
