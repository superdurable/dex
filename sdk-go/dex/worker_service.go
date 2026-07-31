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

package dex

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc/codes"
)

type workerService struct {
	dexpb.UnimplementedWorkerServiceServer
	registry *registry
	hydrator valueHydrator
}

func newWorkerService(
	registry *registry,
	hydrator valueHydrator,
) *workerService {
	if registry == nil {
		panic("dex: WorkerService requires registry")
	}
	return &workerService{registry: registry, hydrator: hydrator}
}

func (service *workerService) InvokeWaitForMethod(
	ctx context.Context,
	request *dexpb.InvokeWaitForMethodRequest,
) (response *dexpb.InvokeWaitForMethodResponse, err error) {
	defer func() {
		if finalErr := finishWorkerCall(recover(), err); finalErr != nil {
			response = nil
			err = finalErr
		}
	}()
	return service.invokeWaitForMethod(ctx, request)
}

func (service *workerService) invokeWaitForMethod(
	ctx context.Context,
	request *dexpb.InvokeWaitForMethodRequest,
) (*dexpb.InvokeWaitForMethodResponse, error) {
	if request == nil {
		return nil, newWorkerFailure(
			codes.InvalidArgument,
			fmt.Errorf("dex: Worker request is nil"),
		)
	}
	if err := validateStepWorkerRequest(
		request.Context,
		request.FlowType,
		request.StepType,
		request.StepInput,
	); err != nil {
		return nil, newWorkerFailure(codes.InvalidArgument, err)
	}
	flow, step, err := service.lookupStep(request.FlowType, request.StepType)
	if err != nil {
		return nil, err
	}
	if step.skipWaitFor {
		return nil, newWorkerFailure(
			codes.FailedPrecondition,
			fmt.Errorf("dex: step %q is execute-only", step.stepType),
		)
	}
	if err := validateKVEnvelopes("attribute", request.Attributes); err != nil {
		return nil, newWorkerFailure(codes.InvalidArgument, err)
	}
	slots := stepRequestValueSlots(&request.StepInput, request.Attributes)
	if err := service.hydrateSlots(ctx, slots); err != nil {
		return nil, err
	}
	input, err := decodeHandlerInput(request.StepInput, step.inputType)
	if err != nil {
		return nil, newWorkerFailure(codes.InvalidArgument, err)
	}
	invocation, err := newInvocationContext(
		ctx,
		invocationWaitFor,
		flow,
		request.Context,
		request.Attributes,
		nil,
		nil,
		nil,
	)
	if err != nil {
		return nil, newWorkerFailure(codes.InvalidArgument, err)
	}
	wait, err := callWaitForHandler(step, invocation, input)
	if err != nil {
		return nil, err
	}
	waiting, transient, err := mapRegisteredWait(flow, wait)
	if err != nil {
		return nil, newWorkerFailure(codes.InvalidArgument, err)
	}
	return &dexpb.InvokeWaitForMethodResponse{
		UpsertAttributes:      invocation.mappedAttributeWrites(),
		WaitingCondition:      waiting,
		UpsertStepExeLocals:   invocation.mappedLocalWrites(),
		RecordEvents:          invocation.recordedEvents,
		PublishToChannel:      invocation.publications,
		TransientStepMovement: transient,
	}, nil
}

func (service *workerService) InvokeExecuteMethod(
	ctx context.Context,
	request *dexpb.InvokeExecuteMethodRequest,
) (response *dexpb.InvokeExecuteMethodResponse, err error) {
	defer func() {
		if finalErr := finishWorkerCall(recover(), err); finalErr != nil {
			response = nil
			err = finalErr
		}
	}()
	return service.invokeExecuteMethod(ctx, request)
}

func (service *workerService) invokeExecuteMethod(
	ctx context.Context,
	request *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	if request == nil {
		return nil, newWorkerFailure(
			codes.InvalidArgument,
			fmt.Errorf("dex: Worker request is nil"),
		)
	}
	if err := validateStepWorkerRequest(
		request.Context,
		request.FlowType,
		request.StepType,
		request.StepInput,
	); err != nil {
		return nil, newWorkerFailure(codes.InvalidArgument, err)
	}
	flow, step, err := service.lookupStep(request.FlowType, request.StepType)
	if err != nil {
		return nil, err
	}
	if err := validateKVEnvelopes("attribute", request.Attributes); err != nil {
		return nil, newWorkerFailure(codes.InvalidArgument, err)
	}
	if err := validateKVEnvelopes("step-execution local", request.StepExeLocals); err != nil {
		return nil, newWorkerFailure(codes.InvalidArgument, err)
	}
	slots, err := executeRequestValueSlots(request)
	if err != nil {
		return nil, newWorkerFailure(codes.InvalidArgument, err)
	}
	if err := service.hydrateSlots(ctx, slots); err != nil {
		return nil, err
	}
	input, err := decodeHandlerInput(request.StepInput, step.inputType)
	if err != nil {
		return nil, newWorkerFailure(codes.InvalidArgument, err)
	}
	invocation, err := newInvocationContext(
		ctx,
		invocationExecute,
		flow,
		request.Context,
		request.Attributes,
		request.StepExeLocals,
		request.ConditionResults,
		nil,
	)
	if err != nil {
		return nil, newWorkerFailure(codes.InvalidArgument, err)
	}
	decision, err := callExecuteHandler(step, invocation, input)
	if err != nil {
		return nil, err
	}
	mapped, err := mapRegisteredDecision(flow, decision)
	if err != nil {
		return nil, newWorkerFailure(codes.InvalidArgument, err)
	}
	return &dexpb.InvokeExecuteMethodResponse{
		StepDecision:     mapped,
		UpsertAttributes: invocation.mappedAttributeWrites(),
		RecordEvents:     invocation.recordedEvents,
		PublishToChannel: invocation.publications,
	}, nil
}

func (service *workerService) InvokeWorkerRPC(
	ctx context.Context,
	request *dexpb.InvokeWorkerRPCRequest,
) (response *dexpb.InvokeWorkerRPCResponse, err error) {
	defer func() {
		if finalErr := finishWorkerCall(recover(), err); finalErr != nil {
			response = nil
			err = finalErr
		}
	}()
	return service.invokeWorkerRPC(ctx, request)
}

func (service *workerService) invokeWorkerRPC(
	ctx context.Context,
	request *dexpb.InvokeWorkerRPCRequest,
) (*dexpb.InvokeWorkerRPCResponse, error) {
	if err := validateRPCWorkerRequest(request); err != nil {
		return nil, newWorkerFailure(codes.InvalidArgument, err)
	}
	flow, found := service.registry.lookupFlow(request.FlowType)
	if !found {
		return nil, newWorkerFailure(
			codes.NotFound,
			fmt.Errorf("dex: flow %q is not registered", request.FlowType),
		)
	}
	rpc, found := flow.lookupRPC(request.RpcName)
	if !found {
		return nil, newWorkerFailure(
			codes.NotFound,
			fmt.Errorf("dex: RPC %q is not registered in flow %q", request.RpcName, request.FlowType),
		)
	}
	if err := validateKVEnvelopes("attribute", request.Attributes); err != nil {
		return nil, newWorkerFailure(codes.InvalidArgument, err)
	}
	if err := service.hydrateSlots(
		ctx,
		stepRequestValueSlots(&request.Input, request.Attributes),
	); err != nil {
		return nil, err
	}
	input, err := decodeHandlerInput(request.Input, rpc.input)
	if err != nil {
		return nil, newWorkerFailure(codes.InvalidArgument, err)
	}
	invocation, err := newInvocationContext(
		ctx,
		invocationRPC,
		flow,
		request.Context,
		request.Attributes,
		nil,
		nil,
		request.ChannelInfos,
	)
	if err != nil {
		return nil, newWorkerFailure(codes.InvalidArgument, err)
	}
	result, err := callRPCHandler(rpc, invocation, input)
	if err != nil {
		return nil, err
	}
	response, err := mapRegisteredRPCResult(flow, result)
	if err != nil {
		return nil, newWorkerFailure(codes.InvalidArgument, err)
	}
	response.UpsertAttributes = invocation.mappedAttributeWrites()
	response.RecordEvents = invocation.recordedEvents
	response.PublishToChannel = invocation.publications
	return response, nil
}

func (service *workerService) lookupStep(
	flowType string,
	stepType string,
) (*registeredFlow, *registeredStep, error) {
	flow, found := service.registry.lookupFlow(flowType)
	if !found {
		return nil, nil, newWorkerFailure(
			codes.NotFound,
			fmt.Errorf("dex: flow %q is not registered", flowType),
		)
	}
	step, found := flow.lookupStep(stepType)
	if !found {
		return nil, nil, newWorkerFailure(
			codes.NotFound,
			fmt.Errorf("dex: step %q is not registered in flow %q", stepType, flowType),
		)
	}
	return flow, step, nil
}

func callWaitForHandler(
	step *registeredStep,
	invocation *invocationContext,
	input any,
) (Wait, error) {
	defer invocation.finish()
	return step.handler.waitFor(invocation, input)
}

func callExecuteHandler(
	step *registeredStep,
	invocation *invocationContext,
	input any,
) (StepDecision, error) {
	defer invocation.finish()
	return step.handler.execute(invocation, input)
}

func callRPCHandler(
	rpc *registeredRPC,
	invocation *invocationContext,
	input any,
) (rpcResult, error) {
	defer invocation.finish()
	return rpc.invoke(invocation, input)
}

func (service *workerService) hydrateSlots(
	ctx context.Context,
	slots []**dexpb.Value,
) error {
	err := hydrateValueSlots(ctx, service.hydrator, slots)
	if err == nil {
		return nil
	}
	var failure *workerFailure
	if errors.As(err, &failure) || errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return newWorkerFailure(codes.InvalidArgument, err)
}

func validateStepWorkerRequest(
	metadata *dexpb.Context,
	flowType string,
	stepType string,
	input *dexpb.Value,
) error {
	if err := validateWorkerContext(metadata, true); err != nil {
		return err
	}
	if flowType == "" || stepType == "" || input == nil {
		return fmt.Errorf("dex: Worker step request is missing flow, step, or input")
	}
	return nil
}

func validateRPCWorkerRequest(request *dexpb.InvokeWorkerRPCRequest) error {
	if request == nil {
		return fmt.Errorf("dex: Worker RPC request is nil")
	}
	if err := validateWorkerContext(request.Context, false); err != nil {
		return err
	}
	if request.FlowType == "" || request.RpcName == "" || request.Input == nil {
		return fmt.Errorf("dex: Worker RPC request is missing flow, RPC, or input")
	}
	return nil
}

func validateWorkerContext(metadata *dexpb.Context, step bool) error {
	if metadata == nil {
		return fmt.Errorf("dex: Worker request Context is nil")
	}
	if metadata.FlowId == "" || metadata.RunId == "" ||
		metadata.FlowStartedTimestamp == 0 {
		return fmt.Errorf("dex: Worker request Context is missing flow metadata")
	}
	if step && (metadata.StepExecutionId == "" || metadata.Attempt < 1 ||
		metadata.FirstAttemptTimestamp == 0) {
		return fmt.Errorf("dex: Worker request Context is missing step attempt metadata")
	}
	return nil
}

func validateKVEnvelopes(kind string, values []*dexpb.KV) error {
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		if value == nil || value.Key == "" || value.Value == nil {
			return fmt.Errorf("dex: invalid %s at index %d", kind, index)
		}
		if _, found := seen[value.Key]; found {
			return fmt.Errorf("dex: duplicate %s key %q", kind, value.Key)
		}
		seen[value.Key] = struct{}{}
	}
	return nil
}

func stepRequestValueSlots(
	input **dexpb.Value,
	attributes []*dexpb.KV,
) []**dexpb.Value {
	slots := make([]**dexpb.Value, 0, 1+len(attributes))
	slots = append(slots, input)
	for _, attribute := range attributes {
		slots = append(slots, &attribute.Value)
	}
	return slots
}

func executeRequestValueSlots(
	request *dexpb.InvokeExecuteMethodRequest,
) ([]**dexpb.Value, error) {
	slots := stepRequestValueSlots(&request.StepInput, request.Attributes)
	for _, local := range request.StepExeLocals {
		slots = append(slots, &local.Value)
	}
	if request.ConditionResults == nil {
		return slots, nil
	}
	for index, result := range request.ConditionResults.ChannelResults {
		if result == nil {
			return nil, fmt.Errorf("dex: channel result at index %d is nil", index)
		}
		for valueIndex := range result.Values {
			slots = append(slots, &result.Values[valueIndex])
		}
	}
	return slots, nil
}

func decodeHandlerInput(value *dexpb.Value, inputType reflect.Type) (any, error) {
	decoded, err := decodeReflectValue(value, inputType)
	if err != nil {
		return nil, err
	}
	return decoded.Interface(), nil
}
