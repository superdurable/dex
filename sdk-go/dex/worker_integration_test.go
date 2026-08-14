// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package dex

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/blob-cache-go/blobcache"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

var (
	workerTestStatus   = DefineAttribute[string]("status", SyncToAttributeStore())
	workerTestItems    = DefineAttributeMap[int]("items", SyncToAttributeStore())
	workerTestCommands = DefineChannel[string]("commands")
	workerTestByOrder  = DefineChannelMap[string]("commands-by-order")
)

type workerTestInput struct {
	OrderID string
	Mode    string
}

type workerTestOutput struct {
	Before int
	After  int
}

type concreteValueHydrator struct{}

func (concreteValueHydrator) HydrateValuesInPlace(
	_ context.Context,
	valuePointers []**dexpb.Value,
) error {
	for index, valuePointer := range valuePointers {
		if valuePointer == nil {
			return newWorkerFailure(
				codes.InvalidArgument,
				fmt.Errorf("dex: value pointer at index %d is nil", index),
			)
		}
		if err := validateConcreteValue(*valuePointer); err != nil {
			return newWorkerFailure(codes.InvalidArgument, err)
		}
	}
	return nil
}

type workerWaitingStep struct {
	StepDefaults
}

func (workerWaitingStep) WaitFor(
	ctx Context,
	input workerTestInput,
) (*Wait, error) {
	if err := workerTestStatus.Set(ctx, "waiting"); err != nil {
		return nil, err
	}
	statusValue, err := workerTestStatus.Get(ctx)
	if err != nil || statusValue != "waiting" {
		return nil, errors.New("buffered attribute write was not visible")
	}
	_, err = workerTestItems.Get(ctx, "missing")
	var missingItem *AttributeNotFoundError
	if !errors.As(err, &missingItem) ||
		missingItem.AttributeName != workerTestItems.AttributeName() ||
		missingItem.Instance != "missing" {
		return nil, errors.New("missing attribute map value returned the wrong error")
	}
	if err := ctx.SetStepExecutionLocal("input", input); err != nil {
		return nil, err
	}
	if err := ctx.RecordEvent("waiting", input); err != nil {
		return nil, err
	}
	if err := workerTestByOrder.Publish(ctx, input.OrderID, "published"); err != nil {
		return nil, err
	}
	if input.Mode == "nil-wait" {
		return nil, nil
	}
	if input.Mode == "immediate" {
		return SkipWaitImmediately(), nil
	}
	if input.Mode == "combo" {
		return AnyComboOf(Combo(
			workerTestCommands.ForOne(WithConditionID("command")),
			Timer(time.Second, WithConditionID("timer")),
		)), nil
	}
	return AnyOf(
		workerTestCommands.ForOne(WithConditionID("command")),
		Timer(time.Second, WithConditionID("timer")),
	), nil
}

func (workerWaitingStep) Execute(
	ctx Context,
	input workerTestInput,
) (*StepDecision, error) {
	if input.Mode == "empty" {
		return nil, nil
	}
	if input.Mode == "error" {
		if err := workerTestStatus.Set(ctx, "discarded"); err != nil {
			return nil, err
		}
		return nil, errors.New("execute failed")
	}
	if input.Mode == "missing-local-target" {
		found, err := ctx.GetStepExecutionLocal("missing", nil)
		if err == nil || found {
			return nil, errors.New("missing local accepted a nil target")
		}
		return DeadEnd(), nil
	}
	values, err := workerTestCommands.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	if len(values) != 2 || values[0] != "first" || values[1] != "second" {
		return nil, errors.New("channel results are out of order")
	}
	mapValues, err := workerTestByOrder.GetConditionResults(ctx, input.OrderID)
	if err != nil {
		return nil, err
	}
	if len(mapValues) != 1 || mapValues[0] != "mapped" {
		return nil, errors.New("map channel results are invalid")
	}
	var local workerTestInput
	found, err := ctx.GetStepExecutionLocal("input", &local)
	if err != nil || !found || local != input {
		return nil, errors.New("step-execution local is invalid")
	}
	timerFired := input.Mode == "timer"
	if !ctx.WaitForMethodFailed() || ctx.HasTimerFired() != timerFired ||
		ctx.HasTimerFiredByIndex(0) != timerFired {
		return nil, errors.New("condition helpers are invalid")
	}
	if err := workerTestStatus.Delete(ctx); err != nil {
		return nil, err
	}
	_, err = workerTestStatus.Get(ctx)
	var notFound *AttributeNotFoundError
	if !errors.As(err, &notFound) ||
		notFound.AttributeName != workerTestStatus.AttributeName() {
		return nil, errors.New("buffered attribute delete was not visible")
	}
	return GoTo(workerTestFinish, input), nil
}

var workerTestWait = workerWaitingStep{}

type workerFinishStep struct {
	StepDefaultsNoWaitFor[workerTestInput]
}

func (workerFinishStep) Execute(
	Context,
	workerTestInput,
) (*StepDecision, error) {
	return DeadEnd(), nil
}

var workerTestFinish = workerFinishStep{}

type workerTestFlow struct {
	FlowDefaults
}

func (workerTestFlow) GetSteps() []StepDef {
	return []StepDef{
		DefineStartStep(workerTestWait),
		DefineStep(workerTestFinish),
	}
}

func (workerTestFlow) GetPersistenceSchema() PersistenceSchema {
	return PersistenceSchema{
		Attributes: []AttributeDef{workerTestStatus, workerTestItems},
		Channels:   []ChannelDef{workerTestCommands, workerTestByOrder},
	}
}

func (workerTestFlow) Update(
	ctx Context,
	input workerTestInput,
) (*RPCResult[workerTestOutput], error) {
	if input.Mode == "panic" {
		panic("RPC panic")
	}
	if input.Mode == "nil-result" {
		return nil, nil
	}
	if input.Mode == "status" {
		return nil, status.Error(codes.PermissionDenied, "denied")
	}
	before := workerTestCommands.Size(ctx)
	if err := workerTestCommands.Publish(ctx, "local"); err != nil {
		return nil, err
	}
	after := workerTestCommands.Size(ctx)
	if err := workerTestItems.Set(ctx, input.OrderID, after); err != nil {
		return nil, err
	}
	if input.Mode == "introspection" {
		if keys := workerTestItems.AllInstanceKeys(ctx); !reflect.DeepEqual(
			keys,
			[]string{"initial / key", input.OrderID},
		) || workerTestItems.MapSize(ctx) != len(keys) {
			return nil, fmt.Errorf("unexpected AttributeMap keys %v", keys)
		}
		if err := workerTestItems.Delete(ctx, "initial / key"); err != nil {
			return nil, err
		}
		if keys := workerTestItems.AllInstanceKeys(ctx); !reflect.DeepEqual(
			keys,
			[]string{input.OrderID},
		) || workerTestItems.MapSize(ctx) != len(keys) {
			return nil, fmt.Errorf("unexpected updated AttributeMap keys %v", keys)
		}
		if keys := workerTestByOrder.AllInstanceKeys(ctx); !reflect.DeepEqual(
			keys,
			[]string{"initial / key"},
		) || workerTestByOrder.MapSize(ctx) != len(keys) {
			return nil, fmt.Errorf("unexpected ChannelMap keys %v", keys)
		}
		if err := workerTestByOrder.Publish(ctx, input.OrderID, "mapped"); err != nil {
			return nil, err
		}
		if keys := workerTestByOrder.AllInstanceKeys(ctx); !reflect.DeepEqual(
			keys,
			[]string{"initial / key", input.OrderID},
		) || workerTestByOrder.MapSize(ctx) != len(keys) {
			return nil, fmt.Errorf("unexpected updated ChannelMap keys %v", keys)
		}
	}
	if err := ctx.RecordEvent("updated", input); err != nil {
		return nil, err
	}
	return &RPCResult[workerTestOutput]{
		Output: workerTestOutput{Before: before, After: after},
		NextSteps: []StepMovement{
			MovementOf(workerTestFinish, input),
		},
	}, nil
}

var workerFlow = workerTestFlow{}

type workerBlockingFlow struct {
	FlowDefaults
	entered     chan struct{}
	release     chan struct{}
	enteredOnce sync.Once
}

func (*workerBlockingFlow) GetSteps() []StepDef {
	return nil
}

func (*workerBlockingFlow) GetPersistenceSchema() PersistenceSchema {
	return PersistenceSchema{}
}

func (flow *workerBlockingFlow) Block(
	ctx Context,
	_ struct{},
) (*RPCResult[bool], error) {
	flow.enteredOnce.Do(func() { close(flow.entered) })
	select {
	case <-flow.release:
		return &RPCResult[bool]{Output: true}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestWorkerServiceDispatchesWaitExecuteAndRPC(t *testing.T) {
	client, closeService := newWorkerTestClient(t, nil)
	defer closeService()

	waitInput := workerTestInput{OrderID: "order-1"}
	waitResponse, err := client.InvokeWaitForMethod(
		context.Background(),
		&dexpb.InvokeWaitForMethodRequest{
			Context:   workerStepContext(),
			FlowType:  GetFinalFlowType(workerFlow),
			StepType:  GetFinalStepType(workerTestWait),
			StepInput: mustEncodeWorkerTestValue(t, waitInput),
		},
	)
	require.NoError(t, err)
	require.Len(t, waitResponse.UpsertAttributes, 1)
	require.Equal(t, "waiting", waitResponse.UpsertAttributes[0].Value.GetStringValue())
	require.True(t, waitResponse.UpsertAttributes[0].GetSyncConfig().GetEnabled())
	require.Len(t, waitResponse.UpsertStepExeLocals, 1)
	require.Len(t, waitResponse.RecordEvents, 1)
	require.Len(t, waitResponse.PublishToChannel, 1)
	require.Equal(t, "commands-by-order/order-1", waitResponse.PublishToChannel[0].ChannelName)
	require.Len(t, waitResponse.WaitingCondition.ChannelConditions, 1)
	require.Len(t, waitResponse.WaitingCondition.TimerConditions, 1)

	immediateRequest := &dexpb.InvokeWaitForMethodRequest{
		Context:  workerStepContext(),
		FlowType: GetFinalFlowType(workerFlow),
		StepType: GetFinalStepType(workerTestWait),
		StepInput: mustEncodeWorkerTestValue(t, workerTestInput{
			OrderID: "order-1",
			Mode:    "immediate",
		}),
	}
	immediateResponse, err := client.InvokeWaitForMethod(context.Background(), immediateRequest)
	require.NoError(t, err)
	require.Nil(t, immediateResponse.WaitingCondition)

	comboRequest := &dexpb.InvokeWaitForMethodRequest{
		Context:  workerStepContext(),
		FlowType: GetFinalFlowType(workerFlow),
		StepType: GetFinalStepType(workerTestWait),
		StepInput: mustEncodeWorkerTestValue(t, workerTestInput{
			OrderID: "order-1",
			Mode:    "combo",
		}),
	}
	comboResponse, err := client.InvokeWaitForMethod(context.Background(), comboRequest)
	require.NoError(t, err)
	require.Len(t, comboResponse.WaitingCondition.ConditionCombinations, 1)

	executeResponse, err := client.InvokeExecuteMethod(
		context.Background(),
		workerExecuteRequest(t, waitInput),
	)
	require.NoError(t, err)
	require.Len(t, executeResponse.StepDecision.NextSteps, 1)
	require.Equal(t, GetFinalStepType(workerTestFinish), executeResponse.StepDecision.NextSteps[0].StepType)
	require.Len(t, executeResponse.UpsertAttributes, 1)
	_, deleted := executeResponse.UpsertAttributes[0].Value.Kind.(*dexpb.Value_NullValue)
	require.True(t, deleted)
	require.True(t, executeResponse.UpsertAttributes[0].GetSyncConfig().GetEnabled())

	timerRequest := workerExecuteRequest(t, workerTestInput{
		OrderID: "order-1",
		Mode:    "timer",
	})
	timerRequest.ConditionResults.TimerResults[0].ConditionStatus =
		dexpb.ConditionStatus_CONDITION_STATUS_COMPLETED
	_, err = client.InvokeExecuteMethod(context.Background(), timerRequest)
	require.NoError(t, err)

	_, err = client.InvokeExecuteMethod(
		context.Background(),
		workerExecuteRequest(t, workerTestInput{
			OrderID: "order-1",
			Mode:    "missing-local-target",
		}),
	)
	require.NoError(t, err)

	rpcInput := workerTestInput{OrderID: "order-1", Mode: "introspection"}
	rpcResponse, err := client.InvokeWorkerRPC(
		context.Background(),
		&dexpb.InvokeWorkerRPCRequest{
			Context:  workerRPCContext(),
			FlowType: GetFinalFlowType(workerFlow),
			RpcName:  "Update",
			Input:    mustEncodeWorkerTestValue(t, rpcInput),
			Attributes: []*dexpb.KV{{
				Key:   "items/initial%20%2F%20key",
				Value: mustEncodeWorkerTestValue(t, 1),
			}},
			ChannelInfos: map[string]*dexpb.ChannelInfo{
				"commands":                              {Size: 2},
				"commands-by-order/empty":               {Size: 0},
				"commands-by-order/initial%20%2F%20key": {Size: 1},
			},
		},
	)
	require.NoError(t, err)
	var output workerTestOutput
	require.NoError(t, decodeValue(rpcResponse.Output, &output))
	require.Equal(t, workerTestOutput{Before: 2, After: 3}, output)
	require.Len(t, rpcResponse.PublishToChannel, 2)
	require.Len(t, rpcResponse.UpsertAttributes, 2)
	require.Equal(t, "items/order-1", rpcResponse.UpsertAttributes[0].Key)
	require.True(t, rpcResponse.UpsertAttributes[0].GetSyncConfig().GetEnabled())
	require.Len(t, rpcResponse.RecordEvents, 1)
	require.Len(t, rpcResponse.StepDecision.NextSteps, 1)
}

func TestWorkerServiceMapsErrorsAndDiscardsResponses(t *testing.T) {
	client, closeService := newWorkerTestClient(t, nil)
	defer closeService()

	tests := []struct {
		name      string
		call      func() error
		code      codes.Code
		detail    string
		errorType string
	}{
		{
			name: "unknown flow",
			call: func() error {
				_, err := client.InvokeWorkerRPC(context.Background(), &dexpb.InvokeWorkerRPCRequest{
					Context: workerRPCContext(), FlowType: "missing", RpcName: "Update",
					Input: mustEncodeWorkerTestValue(t, workerTestInput{}),
				})
				return err
			},
			code: codes.NotFound,
		},
		{
			name: "execute-only WaitFor",
			call: func() error {
				_, err := client.InvokeWaitForMethod(context.Background(), &dexpb.InvokeWaitForMethodRequest{
					Context: workerStepContext(), FlowType: GetFinalFlowType(workerFlow),
					StepType:  GetFinalStepType(workerTestFinish),
					StepInput: mustEncodeWorkerTestValue(t, workerTestInput{}),
				})
				return err
			},
			code: codes.FailedPrecondition,
		},
		{
			name: "unknown step",
			call: func() error {
				_, err := client.InvokeExecuteMethod(context.Background(), &dexpb.InvokeExecuteMethodRequest{
					Context: workerStepContext(), FlowType: GetFinalFlowType(workerFlow),
					StepType: "missing", StepInput: mustEncodeWorkerTestValue(t, workerTestInput{}),
				})
				return err
			},
			code: codes.NotFound,
		},
		{
			name: "unknown RPC",
			call: func() error {
				_, err := client.InvokeWorkerRPC(context.Background(), &dexpb.InvokeWorkerRPCRequest{
					Context: workerRPCContext(), FlowType: GetFinalFlowType(workerFlow),
					RpcName: "Missing", Input: mustEncodeWorkerTestValue(t, workerTestInput{}),
				})
				return err
			},
			code: codes.NotFound,
		},
		{
			name: "nil wait",
			call: func() error {
				_, err := client.InvokeWaitForMethod(
					context.Background(),
					&dexpb.InvokeWaitForMethodRequest{
						Context:  workerStepContext(),
						FlowType: GetFinalFlowType(workerFlow),
						StepType: GetFinalStepType(workerTestWait),
						StepInput: mustEncodeWorkerTestValue(
							t,
							workerTestInput{OrderID: "order-1", Mode: "nil-wait"},
						),
					},
				)
				return err
			},
			code:   codes.InvalidArgument,
			detail: "WaitFor returned nil",
			errorType: fmt.Sprintf(
				"%T",
				&InvalidStepResultError{},
			),
		},
		{
			name: "nil decision",
			call: func() error {
				_, err := client.InvokeExecuteMethod(
					context.Background(),
					workerExecuteRequest(t, workerTestInput{OrderID: "order-1", Mode: "empty"}),
				)
				return err
			},
			code:   codes.InvalidArgument,
			detail: "Execute returned nil",
			errorType: fmt.Sprintf(
				"%T",
				&InvalidStepResultError{},
			),
		},
		{
			name: "nil RPC result",
			call: func() error {
				_, err := client.InvokeWorkerRPC(context.Background(), workerRPCRequest(
					t, workerTestInput{Mode: "nil-result"},
				))
				return err
			},
			code:   codes.InvalidArgument,
			detail: "RPC returned nil",
			errorType: fmt.Sprintf(
				"%T",
				&InvalidStepResultError{},
			),
		},
		{
			name: "application error",
			call: func() error {
				_, err := client.InvokeExecuteMethod(
					context.Background(),
					workerExecuteRequest(t, workerTestInput{OrderID: "order-1", Mode: "error"}),
				)
				return err
			},
			code: codes.Unknown,
		},
		{
			name: "application status",
			call: func() error {
				_, err := client.InvokeWorkerRPC(context.Background(), workerRPCRequest(
					t, workerTestInput{Mode: "status"},
				))
				return err
			},
			code: codes.PermissionDenied,
		},
		{
			name: "panic",
			call: func() error {
				_, err := client.InvokeWorkerRPC(context.Background(), workerRPCRequest(
					t, workerTestInput{Mode: "panic"},
				))
				return err
			},
			code: codes.Internal,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.call()
			require.Error(t, err)
			rpcStatus := status.Convert(err)
			require.Equal(t, testCase.code, rpcStatus.Code())
			if testCase.detail != "" {
				require.Contains(t, rpcStatus.Message(), testCase.detail)
			}
			details := requireWorkerErrorDetail(t, rpcStatus)
			if testCase.errorType != "" {
				require.Equal(t, testCase.errorType, details.ErrorType)
				require.Contains(t, details.Detail, GetFinalFlowType(workerFlow))
			}
		})
	}
}

func TestWorkerLifecycleAndTargetDefaults(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	concrete, err := newWorkerForTest(t, []Flow{workerFlow}, WorkerOptions{
		BindAddress: "127.0.0.1:9900",
		Logger:      logger,
	})
	require.NoError(t, err)
	require.Same(t, logger, concrete.logger)
	require.Equal(t, "127.0.0.1:9900", concrete.WorkerTarget().Address)
	require.NoError(t, concrete.Stop(context.Background()))

	wildcard, err := newWorkerForTest(t, []Flow{workerFlow}, WorkerOptions{BindAddress: ":8804"})
	require.NoError(t, err)
	require.Equal(t, "localhost:8804", wildcard.WorkerTarget().Address)
	require.NoError(t, wildcard.Stop(context.Background()))
	require.Error(t, wildcard.Start())

	address := unusedWorkerAddress(t)
	worker, err := newWorkerForTest(t, []Flow{workerFlow}, WorkerOptions{
		BindAddress: address,
		WorkerTarget: WorkerTarget{
			Address:  "worker.example:8803",
			Headless: true,
		},
	})
	require.NoError(t, err)
	require.Equal(t, &WorkerTarget{Address: "worker.example:8803", Headless: true}, worker.WorkerTarget())
	startResult := make(chan error, 1)
	go func() {
		startResult <- worker.Start()
	}()

	conn, err := grpc.NewClient(
		address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, conn.Close()) }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = dexpb.NewWorkerServiceClient(conn).InvokeWorkerRPC(
		ctx,
		workerRPCRequest(t, workerTestInput{OrderID: "order-1"}),
		grpc.WaitForReady(true),
	)
	require.NoError(t, err)
	require.NoError(t, worker.Stop(context.Background()))
	require.NoError(t, <-startResult)
	require.NoError(t, worker.Stop(context.Background()))
	stoppedCtx, cancelStopped := context.WithCancel(context.Background())
	cancelStopped()
	require.NoError(t, worker.Stop(stoppedCtx))

	_, err = newWorkerForTest(t, []Flow{workerFlow}, WorkerOptions{WorkerTarget: WorkerTarget{Address: "https://worker"}})
	require.ErrorContains(t, err, "plaintext")
	_, err = newWorkerForTest(t, []Flow{workerFlow}, WorkerOptions{BindAddress: ":0"})
	require.ErrorContains(t, err, "1-65535")
	_, err = newWorkerForTest(t, []Flow{workerFlow}, WorkerOptions{AttributeIndexSyncTimeout: -time.Second})
	require.ErrorContains(t, err, "sync timeout must be positive")
}

func TestWorkerSynchronizesAttributeIndexesBeforeListening(t *testing.T) {
	indexedStatus := DefineAttribute[string](
		"status",
		Indexed(AttributeIndex{Type: IndexKeyword, IndexKey: "WorkerTestStatus"}),
	)
	flow := &registrationFlow{
		flowType: "worker-index-sync",
		schema: PersistenceSchema{Attributes: []AttributeDef{
			indexedStatus,
		}},
	}
	address := unusedWorkerAddress(t)
	flowService := &workerSyncFlowService{
		probeAddress: address,
		received:     make(chan *dexpb.SyncAttributeIndexRequest, 1),
		wasListening: make(chan bool, 1),
	}
	flowServiceAddress := startWorkerFlowService(t, flowService)
	worker, err := newWorkerForTest(t, []Flow{flow}, WorkerOptions{
		BindAddress:        address,
		FlowServiceAddress: flowServiceAddress,
	})
	require.NoError(t, err)
	startResult := make(chan error, 1)
	go func() { startResult <- worker.Start() }()

	request := <-flowService.received
	require.Equal(t, map[string]dexpb.IndexType{
		"WorkerTestStatus": dexpb.IndexType_INDEX_TYPE_KEYWORD,
	}, request.AttributeIndexes)
	require.False(t, <-flowService.wasListening)
	require.Eventually(t, func() bool {
		connection, dialErr := net.DialTimeout("tcp", address, 50*time.Millisecond)
		if dialErr != nil {
			return false
		}
		require.NoError(t, connection.Close())
		return true
	}, 5*time.Second, 10*time.Millisecond)
	require.NoError(t, worker.Stop(context.Background()))
	require.NoError(t, <-startResult)
}

func TestWorkerAttributeIndexSyncFailureKeepsPortClosed(t *testing.T) {
	address := unusedWorkerAddress(t)
	flowServiceAddress := startWorkerFlowService(t, &workerSyncFlowService{
		failure: status.Error(codes.PermissionDenied, "denied"),
	})
	worker, err := newWorkerForTest(t, []Flow{workerFlow}, WorkerOptions{
		BindAddress:        address,
		FlowServiceAddress: flowServiceAddress,
	})
	require.NoError(t, err)
	err = worker.Start()
	require.ErrorContains(t, err, "sync attribute indexes")
	require.Equal(t, codes.PermissionDenied, status.Code(err))

	connection, dialErr := net.DialTimeout("tcp", address, 50*time.Millisecond)
	if dialErr == nil {
		require.NoError(t, connection.Close())
	}
	require.Error(t, dialErr)
}

func TestWorkerHydrationUsesLoadBlobsAndDiskCache(t *testing.T) {
	flowService := &workerBlobFlowService{}
	client, closeFlowService := newFlowServiceTestClient(t, flowService)
	defer closeFlowService()
	cache, err := blobcache.New(&blobcache.Config{
		Dir:      t.TempDir(),
		MaxBytes: 1 << 20,
	})
	require.NoError(t, err)
	defer func() { require.NoError(t, cache.Close()) }()
	hydrator := newValueHydrator(client, cache, nil)
	request := &dexpb.Value{Kind: &dexpb.Value_InternalBlobIdForStringValue{
		InternalBlobIdForStringValue: "blob-1",
	}}

	first := request
	err = hydrator.HydrateValuesInPlace(context.Background(), []**dexpb.Value{&first})
	require.NoError(t, err)
	require.Equal(t, "loaded-blob-1", first.GetStringValue())
	second := request
	err = hydrator.HydrateValuesInPlace(context.Background(), []**dexpb.Value{&second})
	require.NoError(t, err)
	require.Equal(t, "loaded-blob-1", second.GetStringValue())
	require.Equal(t, 1, flowService.callCount())

	cached, err := cache.Put("corrupt", []byte{0xff})
	require.NoError(t, err)
	require.True(t, cached)
	corruptRequest := &dexpb.Value{Kind: &dexpb.Value_InternalBlobIdForStringValue{
		InternalBlobIdForStringValue: "corrupt",
	}}
	loaded := corruptRequest
	err = hydrator.HydrateValuesInPlace(context.Background(), []**dexpb.Value{&loaded})
	require.NoError(t, err)
	require.Equal(t, "loaded-corrupt", loaded.GetStringValue())
	require.Equal(t, 2, flowService.callCount())

	encodedInput := mustEncodeWorkerTestValue(t, workerTestInput{OrderID: "order-1"})
	flowService.setValue("input-blob", encodedInput)
	workerClient, closeWorker := newWorkerTestClient(
		t,
		newValueHydrator(client, cache, nil),
	)
	defer closeWorker()
	rpcRequest := workerRPCRequest(t, workerTestInput{})
	rpcRequest.Input = &dexpb.Value{Kind: &dexpb.Value_InternalBlobIdForObjValue{
		InternalBlobIdForObjValue: "input-blob",
	}}
	response, err := workerClient.InvokeWorkerRPC(context.Background(), rpcRequest)
	require.NoError(t, err)
	var output workerTestOutput
	require.NoError(t, decodeValue(response.Output, &output))
	require.Equal(t, workerTestOutput{Before: 0, After: 1}, output)

	wrongID := "wrong-kind"
	flowService.setValue(wrongID, encodedInput)
	wrongRequest := &dexpb.Value{Kind: &dexpb.Value_InternalBlobIdForStringValue{
		InternalBlobIdForStringValue: wrongID,
	}}
	wrongValue := wrongRequest
	err = hydrator.HydrateValuesInPlace(context.Background(), []**dexpb.Value{&wrongValue})
	require.ErrorContains(t, err, "hydrated to")
	require.Same(t, wrongRequest, wrongValue)

	omittedID := "omitted"
	flowService.omitValue(omittedID)
	omittedRequest := &dexpb.Value{Kind: &dexpb.Value_InternalBlobIdForStringValue{
		InternalBlobIdForStringValue: omittedID,
	}}
	omittedValue := omittedRequest
	err = hydrator.HydrateValuesInPlace(context.Background(), []**dexpb.Value{&omittedValue})
	require.ErrorContains(t, err, "omitted blob")
	require.Same(t, omittedRequest, omittedValue)

	var logOutput bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logOutput, nil))
	closedCache, err := blobcache.New(&blobcache.Config{
		Dir:      t.TempDir(),
		MaxBytes: 1 << 20,
		Logger:   logger,
	})
	require.NoError(t, err)
	require.NoError(t, closedCache.Close())
	uncachedHydrator := newValueHydrator(client, closedCache, nil)
	uncached := request
	err = uncachedHydrator.HydrateValuesInPlace(
		context.Background(),
		[]**dexpb.Value{&uncached},
	)
	require.NoError(t, err)
	require.Equal(t, "loaded-blob-1", uncached.GetStringValue())
	require.Contains(t, logOutput.String(), "read blob cache")
	require.Contains(t, logOutput.String(), "write blob cache")
}

type workerBlobFlowService struct {
	dexpb.UnimplementedFlowServiceServer
	mu      sync.Mutex
	calls   int
	values  map[string]*dexpb.Value
	omitted map[string]struct{}
}

type workerSyncFlowService struct {
	dexpb.UnimplementedFlowServiceServer
	probeAddress string
	received     chan *dexpb.SyncAttributeIndexRequest
	wasListening chan bool
	failure      error
}

func (service *workerSyncFlowService) SyncAttributeIndexes(
	_ context.Context,
	request *dexpb.SyncAttributeIndexRequest,
) (*dexpb.SyncAttributeIndexResponse, error) {
	if service.probeAddress != "" {
		connection, dialErr := net.DialTimeout("tcp", service.probeAddress, 50*time.Millisecond)
		listening := dialErr == nil
		if connection != nil {
			if err := connection.Close(); err != nil {
				return nil, err
			}
		}
		service.wasListening <- listening
	}
	if service.received != nil {
		service.received <- request
	}
	if service.failure != nil {
		return nil, service.failure
	}
	return &dexpb.SyncAttributeIndexResponse{}, nil
}

func (service *workerBlobFlowService) LoadBlobs(
	_ context.Context,
	request *dexpb.LoadBlobsRequest,
) (*dexpb.LoadBlobsResponse, error) {
	service.mu.Lock()
	service.calls++
	service.mu.Unlock()
	values := make(map[string]*dexpb.Value, len(request.Values))
	for _, value := range request.Values {
		id := value.GetInternalBlobIdForStringValue()
		if id == "" {
			id = value.GetInternalBlobIdForObjValue()
		}
		service.mu.Lock()
		configured := service.values[id]
		_, omitted := service.omitted[id]
		service.mu.Unlock()
		if omitted {
			continue
		}
		if configured != nil {
			values[id] = configured
			continue
		}
		values[id] = &dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: "loaded-" + id}}
	}
	return &dexpb.LoadBlobsResponse{Values: values}, nil
}

func (service *workerBlobFlowService) setValue(id string, value *dexpb.Value) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.values == nil {
		service.values = make(map[string]*dexpb.Value)
	}
	service.values[id] = value
}

func (service *workerBlobFlowService) omitValue(id string) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.omitted == nil {
		service.omitted = make(map[string]struct{})
	}
	service.omitted[id] = struct{}{}
}

func (service *workerBlobFlowService) callCount() int {
	service.mu.Lock()
	defer service.mu.Unlock()
	return service.calls
}

func TestWorkerConcurrentInvocationsAreIsolated(t *testing.T) {
	client, closeService := newWorkerTestClient(t, nil)
	defer closeService()

	const callCount = 24
	requests := make([]*dexpb.InvokeWorkerRPCRequest, callCount)
	for index := range requests {
		request := workerRPCRequest(t, workerTestInput{OrderID: fmt.Sprintf("order-%d", index)})
		request.ChannelInfos = map[string]*dexpb.ChannelInfo{
			"commands": {Size: int32(index)},
		}
		requests[index] = request
	}

	errorsByCall := make(chan error, callCount)
	var waitGroup sync.WaitGroup
	for index := range requests {
		waitGroup.Add(1)
		go func(request *dexpb.InvokeWorkerRPCRequest, expected int) {
			defer waitGroup.Done()
			response, err := client.InvokeWorkerRPC(context.Background(), request)
			if err == nil {
				var output workerTestOutput
				err = decodeValue(response.Output, &output)
				if err == nil && output != (workerTestOutput{Before: expected, After: expected + 1}) {
					err = fmt.Errorf("unexpected output: %+v", output)
				}
			}
			errorsByCall <- err
		}(requests[index], index)
	}
	waitGroup.Wait()
	close(errorsByCall)
	for err := range errorsByCall {
		require.NoError(t, err)
	}
}

func TestWorkerStopDrainsAndForceCancels(t *testing.T) {
	t.Run("graceful drain", func(t *testing.T) {
		flow := &workerBlockingFlow{entered: make(chan struct{}), release: make(chan struct{})}
		worker, client, startResult, closeConn := startBlockingWorker(t, flow)
		defer closeConn()
		rpcResult := invokeBlockingRPC(client)
		<-flow.entered
		stopResult := make(chan error, 1)
		go func() { stopResult <- worker.Stop(context.Background()) }()
		close(flow.release)
		require.NoError(t, <-rpcResult)
		require.NoError(t, <-stopResult)
		require.NoError(t, <-startResult)
	})

	t.Run("force cancel", func(t *testing.T) {
		flow := &workerBlockingFlow{entered: make(chan struct{}), release: make(chan struct{})}
		worker, client, startResult, closeConn := startBlockingWorker(t, flow)
		defer closeConn()
		rpcResult := invokeBlockingRPC(client)
		<-flow.entered
		stopCtx, cancel := context.WithCancel(context.Background())
		cancel()
		require.ErrorIs(t, worker.Stop(stopCtx), context.Canceled)
		require.Error(t, <-rpcResult)
		require.NoError(t, <-startResult)
	})
}

func TestWorkerRegisteredDecisionMapping(t *testing.T) {
	registered, err := NewRegistry([]Flow{workerFlow})
	require.NoError(t, err)
	flow, found := registered.lookupFlow(GetFinalFlowType(workerFlow))
	require.True(t, found)

	tests := []struct {
		name     string
		decision *StepDecision
		close    dexpb.CloseDecisionType
	}{
		{name: "graceful", decision: GracefulComplete("done"), close: dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_GRACEFUL_COMPLETE},
		{name: "force complete", decision: ForceComplete("done"), close: dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_FORCE_COMPLETE},
		{name: "force fail", decision: ForceFail("failed"), close: dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_FORCE_FAIL},
		{name: "dead end", decision: DeadEnd(), close: dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_DEAD_END},
		{
			name: "conditional",
			decision: ForceCompleteIfChannelsEmpty(
				"done",
				[]ChannelDef{workerTestCommands},
				MovementOf(workerTestFinish, workerTestInput{}),
			),
			close: dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_FORCE_COMPLETE_ON_CHANNELS_EMPTY,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			mapped, err := mapRegisteredDecision(flow, testCase.decision)
			require.NoError(t, err)
			require.Equal(t, testCase.close, mapped.CloseDecision.CloseDecisionType)
		})
	}

	mapped, err := mapRegisteredDecision(
		flow,
		GoTo(workerTestFinish, workerTestInput{}),
	)
	require.NoError(t, err)
	require.Len(t, mapped.NextSteps, 1)
}

func newWorkerForTest(
	t *testing.T,
	flows []Flow,
	options WorkerOptions,
) (*Worker, error) {
	t.Helper()
	if options.FlowServiceAddress == "" {
		options.FlowServiceAddress = startWorkerFlowService(t, &workerSyncFlowService{})
	}
	registry, err := NewRegistry(flows)
	if err != nil {
		return nil, err
	}
	cache, err := blobcache.New(&blobcache.Config{
		Dir:      t.TempDir(),
		MaxBytes: 1 << 20,
	})
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() {
		require.NoError(t, cache.Close())
	})
	return NewWorker(registry, cache, options)
}

func startWorkerFlowService(
	t *testing.T,
	service dexpb.FlowServiceServer,
) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	grpcServer := grpc.NewServer()
	dexpb.RegisterFlowServiceServer(grpcServer, service)
	serveResult := make(chan error, 1)
	go func() {
		serveResult <- grpcServer.Serve(listener)
	}()
	t.Cleanup(func() {
		grpcServer.Stop()
		if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			require.NoError(t, err)
		}
		requireServeStopped(t, <-serveResult)
	})
	return listener.Addr().String()
}

func startBlockingWorker(
	t *testing.T,
	flow Flow,
) (*Worker, dexpb.WorkerServiceClient, <-chan error, func()) {
	address := unusedWorkerAddress(t)
	worker, err := newWorkerForTest(t, []Flow{flow}, WorkerOptions{BindAddress: address})
	require.NoError(t, err)
	startResult := make(chan error, 1)
	go func() { startResult <- worker.Start() }()
	conn, err := grpc.NewClient(
		address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	return worker, dexpb.NewWorkerServiceClient(conn), startResult, func() {
		require.NoError(t, conn.Close())
	}
}

func invokeBlockingRPC(
	client dexpb.WorkerServiceClient,
) <-chan error {
	result := make(chan error, 1)
	input, err := encodeValue(struct{}{})
	if err != nil {
		result <- err
		return result
	}
	go func() {
		_, err := client.InvokeWorkerRPC(
			context.Background(),
			&dexpb.InvokeWorkerRPCRequest{
				Context:  workerRPCContext(),
				FlowType: "dex.workerBlockingFlow",
				RpcName:  "Block",
				Input:    input,
			},
			grpc.WaitForReady(true),
		)
		result <- err
	}()
	return result
}

func newWorkerTestClient(
	t *testing.T,
	hydrator valueHydrator,
) (dexpb.WorkerServiceClient, func()) {
	if hydrator == nil {
		hydrator = concreteValueHydrator{}
	}
	registered, err := NewRegistry([]Flow{workerFlow})
	require.NoError(t, err)
	listener := bufconn.Listen(1 << 20)
	grpcServer := grpc.NewServer()
	dexpb.RegisterWorkerServiceServer(grpcServer, newWorkerService(registered, hydrator, nil))
	serveResult := make(chan error, 1)
	go func() {
		serveResult <- grpcServer.Serve(listener)
	}()
	conn, err := grpc.NewClient(
		"passthrough:///worker-test",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	return dexpb.NewWorkerServiceClient(conn), func() {
		grpcServer.Stop()
		require.NoError(t, conn.Close())
		requireServeStopped(t, <-serveResult)
	}
}

func newFlowServiceTestClient(
	t *testing.T,
	service dexpb.FlowServiceServer,
) (dexpb.FlowServiceClient, func()) {
	listener := bufconn.Listen(1 << 20)
	grpcServer := grpc.NewServer()
	dexpb.RegisterFlowServiceServer(grpcServer, service)
	serveResult := make(chan error, 1)
	go func() {
		serveResult <- grpcServer.Serve(listener)
	}()
	conn, err := grpc.NewClient(
		"passthrough:///flow-test",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	return dexpb.NewFlowServiceClient(conn), func() {
		grpcServer.Stop()
		require.NoError(t, conn.Close())
		requireServeStopped(t, <-serveResult)
	}
}

func requireServeStopped(t *testing.T, err error) {
	if err != nil {
		require.ErrorIs(t, err, grpc.ErrServerStopped)
	}
}

func workerStepContext() *dexpb.Context {
	return &dexpb.Context{
		FlowId:                "flow-1",
		RunId:                 "run-1",
		FlowStartedTimestamp:  1,
		StepExecutionId:       "waiting-1",
		FromStepExecutionId:   "source-1",
		FirstAttemptTimestamp: 2,
		Attempt:               1,
	}
}

func workerRPCContext() *dexpb.Context {
	return &dexpb.Context{
		FlowId:               "flow-1",
		RunId:                "run-1",
		FlowStartedTimestamp: 1,
	}
}

func workerExecuteRequest(
	t *testing.T,
	input workerTestInput,
) *dexpb.InvokeExecuteMethodRequest {
	return &dexpb.InvokeExecuteMethodRequest{
		Context:   workerStepContext(),
		FlowType:  GetFinalFlowType(workerFlow),
		StepType:  GetFinalStepType(workerTestWait),
		StepInput: mustEncodeWorkerTestValue(t, input),
		StepExeLocals: []*dexpb.KV{{
			Key: "input", Value: mustEncodeWorkerTestValue(t, input),
		}},
		ConditionResults: &dexpb.ConditionResults{
			WaitForFailed: true,
			TimerResults: []*dexpb.TimerResult{{
				ConditionId:     "timer",
				ConditionStatus: dexpb.ConditionStatus_CONDITION_STATUS_WAITING,
			}},
			ChannelResults: []*dexpb.ChannelResult{
				{
					ConditionId: "command-1", ChannelName: "commands",
					ConditionStatus: dexpb.ConditionStatus_CONDITION_STATUS_COMPLETED,
					Values:          []*dexpb.Value{mustEncodeWorkerTestValue(t, "first")},
				},
				{
					ConditionId: "command-2", ChannelName: "commands",
					ConditionStatus: dexpb.ConditionStatus_CONDITION_STATUS_COMPLETED,
					Values:          []*dexpb.Value{mustEncodeWorkerTestValue(t, "second")},
				},
				{
					ConditionId: "map-command", ChannelName: "commands-by-order/" + input.OrderID,
					ConditionStatus: dexpb.ConditionStatus_CONDITION_STATUS_COMPLETED,
					Values:          []*dexpb.Value{mustEncodeWorkerTestValue(t, "mapped")},
				},
			},
		},
	}
}

func workerRPCRequest(
	t *testing.T,
	input workerTestInput,
) *dexpb.InvokeWorkerRPCRequest {
	return &dexpb.InvokeWorkerRPCRequest{
		Context:  workerRPCContext(),
		FlowType: GetFinalFlowType(workerFlow),
		RpcName:  "Update",
		Input:    mustEncodeWorkerTestValue(t, input),
	}
}

func mustEncodeWorkerTestValue(t *testing.T, value any) *dexpb.Value {
	encoded, err := encodeValue(value)
	require.NoError(t, err)
	return encoded
}

func requireWorkerErrorDetail(
	t *testing.T,
	rpcStatus *status.Status,
) *dexpb.WorkerErrorResponse {
	require.Len(t, rpcStatus.Details(), 1)
	details, ok := rpcStatus.Details()[0].(*dexpb.WorkerErrorResponse)
	require.True(t, ok)
	return details
}

func unusedWorkerAddress(t *testing.T) string {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := listener.Addr().String()
	require.NoError(t, listener.Close())
	return address
}
