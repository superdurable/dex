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
	"github.com/superdurable/dex/sdk-go/dex/blobcache"
	"github.com/superdurable/dex/sdk-go/dex/ptr"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	clientTestStatus   = DefineAttribute[string]("status", Indexed(AttributeIndex{Type: IndexKeyword}))
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
) (RPCResult[clientTestRPCOutput], error) {
	return RPCResult[clientTestRPCOutput]{Output: clientTestRPCOutput{}}, nil
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
	return &dexpb.StartFlowResponse{RunId: "run-1"}, nil
}

func (service *clientTestFlowService) PublishToChannel(
	_ context.Context,
	request *dexpb.PublishToChannelRequest,
) (*emptypb.Empty, error) {
	service.publishRequest = request
	return &emptypb.Empty{}, nil
}

func (service *clientTestFlowService) GetAttributes(
	_ context.Context,
	request *dexpb.GetAttributesRequest,
) (*dexpb.GetAttributesResponse, error) {
	service.getAttributesRequest = request
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
			values[blobID.value] = &dexpb.Value{
				Kind: &dexpb.Value_StringValue{StringValue: "hydrated"},
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
	return &emptypb.Empty{}, nil
}

func (service *clientTestFlowService) StopFlow(
	_ context.Context,
	request *dexpb.StopFlowRequest,
) (*emptypb.Empty, error) {
	service.stopRequest = request
	return &emptypb.Empty{}, nil
}

func (service *clientTestFlowService) WaitForFlow(
	_ context.Context,
	request *dexpb.WaitForFlowRequest,
) (*dexpb.WaitForFlowResponse, error) {
	service.waitFlowRequest = request
	return &dexpb.WaitForFlowResponse{
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
			SearchAttributes: []*dexpb.KV{{
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
	return &dexpb.ResetFlowResponse{RunId: "run-2"}, nil
}

func (service *clientTestFlowService) SkipTimer(
	_ context.Context,
	request *dexpb.SkipTimerRequest,
) (*emptypb.Empty, error) {
	service.skipTimerRequest = request
	return &emptypb.Empty{}, nil
}

func (service *clientTestFlowService) UpdateFlowConfig(
	_ context.Context,
	request *dexpb.UpdateFlowConfigRequest,
) (*emptypb.Empty, error) {
	service.updateConfigRequest = request
	return &emptypb.Empty{}, nil
}

func (service *clientTestFlowService) WaitForStepCompletion(
	_ context.Context,
	request *dexpb.WaitForStepCompletionRequest,
) (*dexpb.WaitForStepCompletionResponse, error) {
	service.waitStepRequest = request
	return &dexpb.WaitForStepCompletionResponse{}, nil
}

func (service *clientTestFlowService) TriggerContinueAsNew(
	_ context.Context,
	request *dexpb.TriggerContinueAsNewRequest,
) (*emptypb.Empty, error) {
	service.continueAsNewRequest = request
	return &emptypb.Empty{}, nil
}

func (service *clientTestFlowService) HealthCheck(
	context.Context,
	*emptypb.Empty,
) (*dexpb.HealthInfo, error) {
	return &dexpb.HealthInfo{Condition: "SERVING", Hostname: "test", Duration: 1}, nil
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
	found, err = client.GetAttributeMap(ctx, "order-1", clientTestItems, "sku-1", &quantity)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, 3, quantity)

	require.NoError(t, client.SetAttribute(ctx, "order-1", clientTestStatus, "done"))
	require.NoError(t, client.SetAttributeMap(ctx, "order-1", clientTestItems, "sku-1", 4))
	require.Len(t, service.setRequests, 2)
	require.NotEqual(t, service.setRequests[0].RequestId, service.setRequests[1].RequestId)
	for _, request := range service.setRequests {
		_, err := uuid.Parse(request.RequestId)
		require.NoError(t, err)
	}

	values, err := client.GetAttributes(ctx, "order-1", clientTestStatus)
	require.NoError(t, err)
	require.Contains(t, values, "status")
	require.NoError(t, client.SetAttributes(ctx, "order-1", AttributeWrite{Name: "status", Value: "batched"}))
	require.Len(t, service.setRequests, 3)

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

	page, err := client.SearchFlows(ctx, SearchQuery{Query: "status = 'ready'"}, 10, "")
	require.NoError(t, err)
	require.Equal(t, "next", page.NextPageToken)
	require.Equal(t, FlowRunning, page.Flows[0].Status)
	require.Equal(t, time.Unix(100, 0).UTC(), page.Flows[0].StartedAt)
	require.True(t, page.Flows[0].ClosedAt.IsZero())
	var searchStatus string
	require.NoError(t, page.Flows[0].SearchAttributes["status"].Decode(&searchStatus))
	require.Equal(t, "hydrated", searchStatus)

	require.NoError(t, client.StopFlow(ctx, "order-1", StopOptions{}))
	require.Equal(t, dexpb.StopType_STOP_TYPE_CANCEL, service.stopRequest.StopType)
	newRunID, err := client.ResetFlow(ctx, "order-1", ResetOptions{Type: ResetToBeginning})
	require.NoError(t, err)
	require.Equal(t, "run-2", newRunID)
	require.NoError(t, client.SkipTimer(
		ctx,
		"order-1",
		StepExecutionID{StepType: GetFinalStepType(clientTestStep{})},
		TimerID{ConditionID: "timeout"},
	))
	require.Equal(t, "dex.clientTestStep-1", service.skipTimerRequest.StepExecutionId)

	require.NoError(t, client.UpdateFlowConfig(ctx, "order-1", FlowConfig{
		ContinueAsNewThreshold: ptr.Any(int32(100)),
	}))
	require.Nil(t, service.updateConfigRequest.FlowConfig.WorkerTarget)
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
