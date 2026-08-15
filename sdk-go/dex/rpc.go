// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package dex

import "reflect"

// RPC is a typed Flow method that may read persistence and return output plus Step movements.
//
// Exported Flow methods matching this signature are registered under their Go method names. Return
// a non-nil RPCResult on success or an error to report a Worker invocation failure.
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
