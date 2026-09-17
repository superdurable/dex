// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Sustainable Use License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package dex

import (
	"fmt"
	"reflect"
)

// RPC is a typed Flow method that may read persistence and return output plus Step movements.
//
// Register a direct bound Flow method with DefineRPC. Dex uses the Go method name as the durable
// RPC name. Return a non-nil RPCResult on success or an error to report a Worker invocation failure.
//
// Example:
//
//	func (OrderFlow) GetStatus(
//		ctx dex.Context,
//		input GetStatusInput,
//	) (*dex.RPCResult[OrderStatus], error) {
//		return &dex.RPCResult[OrderStatus]{Output: OrderStatus{}}, nil
//	}
type RPC[IN, OUT any] func(
	ctx Context,
	input IN,
) (*RPCResult[OUT], error)

// DefineRPC registers a direct bound Flow method with immutable execution options.
//
// Pass nil options to use server defaults without Attribute locks, transactional execution, or
// selective collection loading. The Registry validates the method and options before publishing
// the Flow definition.
//
//	func (flow *OrderFlow) GetRPCs() []dex.RPCDef {
//		return []dex.RPCDef{
//			dex.DefineRPC(flow.GetOrder, nil),
//			dex.DefineRPC(flow.UpdateOrder, &dex.RPCOptions{
//				LockAttributes: []dex.AttributeLock{dex.LockAttribute(OrderStatus)},
//			}),
//		}
//	}
func DefineRPC[IN, OUT any](rpc RPC[IN, OUT], options *RPCOptions) RPCDef {
	return typedRPCDef[IN, OUT]{rpc: rpc, options: options}
}

// RPCDef is the registration representation of a typed RPC method.
//
// Applications create definitions with DefineRPC and return them from Flow.GetRPCs. Its methods
// are sealed so only SDK-provided definitions can be registered.
type RPCDef interface {
	rpcHandler() any
	rpcInputType() reflect.Type
	rpcOutputType() reflect.Type
	rpcOptions() *RPCOptions
	invoke(Context, any) (rpcResult, error)
}

type typedRPCDef[IN, OUT any] struct {
	rpc     RPC[IN, OUT]
	options *RPCOptions
}

func (definition typedRPCDef[IN, OUT]) rpcHandler() any {
	return definition.rpc
}

func (typedRPCDef[IN, OUT]) rpcInputType() reflect.Type {
	return reflect.TypeFor[IN]()
}

func (typedRPCDef[IN, OUT]) rpcOutputType() reflect.Type {
	return reflect.TypeFor[OUT]()
}

func (definition typedRPCDef[IN, OUT]) rpcOptions() *RPCOptions {
	return definition.options
}

func (definition typedRPCDef[IN, OUT]) invoke(
	ctx Context,
	input any,
) (rpcResult, error) {
	typedInput, ok := input.(IN)
	if !ok && (input != nil || !isNilableType(reflect.TypeFor[IN]())) {
		return nil, fmt.Errorf(
			"dex: RPC input %T is not assignable to %s",
			input,
			reflect.TypeFor[IN](),
		)
	}
	result, err := definition.rpc(ctx, typedInput)
	if result == nil {
		return nil, err
	}
	return result, err
}

func rpcStateLoads(options *RPCOptions) stateLoads {
	if options == nil {
		return stateLoads{}
	}
	return stateLoads{
		attributeMaps:         options.LoadAttributeMaps,
		attributeMapInstances: options.LoadAttributeMapInstances,
		channels:              options.LoadChannels,
		channelMaps:           options.LoadChannelMaps,
		channelMapInstances:   options.LoadChannelMapInstances,
	}
}

// RPCResult carries typed output, Step movements, and Flow-wide Step cancellation.
type RPCResult[OUT any] struct {
	// Output is encoded as the RPC response value.
	Output OUT
	// NextSteps are scheduled in order after the RPC persistence changes commit.
	NextSteps []StepMovement
	// CancelingSteps selects registered Step types canceled before NextSteps are scheduled.
	CancelingSteps []StepSelector
}

// CancelSteps selects queued or active executions of registered Step types.
//
// Dex resolves the selection after RPC persistence commits and before NextSteps are queued.
// Finished, already-canceled, and absent executions are no-ops. RPCs cannot select siblings.
// The result is mutated and returned for fluent use.
func (result *RPCResult[OUT]) CancelSteps(steps ...StepSelector) *RPCResult[OUT] {
	result.CancelingSteps = append(result.CancelingSteps, steps...)
	return result
}

type rpcResult interface {
	rpcOutput() any
	rpcOutputType() reflect.Type
	rpcMovements() []StepMovement
	rpcCancelingSteps() []StepSelector
}

func (result RPCResult[OUT]) rpcOutput() any {
	return result.Output
}

func (RPCResult[OUT]) rpcOutputType() reflect.Type {
	return reflect.TypeFor[OUT]()
}

func (result RPCResult[OUT]) rpcMovements() []StepMovement {
	return result.NextSteps
}

func (result RPCResult[OUT]) rpcCancelingSteps() []StepSelector {
	return result.CancelingSteps
}
