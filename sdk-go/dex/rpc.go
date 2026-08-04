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

type RPC[IN, OUT any] func(
	ctx Context,
	input IN,
) (RPCResult[OUT], error)

type RPCResult[OUT any] struct {
	Output    OUT
	NextSteps []StepMovement
}

type rpcResult interface {
	rpcOutput() any
	rpcOutputType() reflect.Type
	rpcMovements() []StepMovement
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
