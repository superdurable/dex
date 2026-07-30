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

import "reflect"

type RPC[IN, OUT any] func(
	ctx Context,
	input IN,
) (RPCResult[OUT], error)

type RPCResult[OUT any] struct {
	Output    OUT
	NextSteps []StepMovement
}

func Reply[OUT any](output OUT) RPCResult[OUT] {
	return RPCResult[OUT]{Output: output}
}

func ReplyAndMove[OUT any](
	output OUT,
	movements ...StepMovement,
) RPCResult[OUT] {
	return RPCResult[OUT]{
		Output:    output,
		NextSteps: movements,
	}
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
