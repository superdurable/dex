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
	"fmt"
	"reflect"
	"runtime"
	"strings"
)

type registeredRPC struct {
	method      reflect.Value
	durableName string
	input       reflect.Type
	output      reflect.Type
}

var (
	contextType   = reflect.TypeFor[Context]()
	errorType     = reflect.TypeFor[error]()
	rpcResultType = reflect.TypeFor[rpcResult]()
)

func discoverRPCs(flow Flow) (map[string]*registeredRPC, error) {
	receiver := reflect.ValueOf(flow)
	receiverType := receiver.Type()
	registered := make(map[string]*registeredRPC)
	for index := 0; index < receiverType.NumMethod(); index++ {
		method := receiverType.Method(index)
		rpc, matches := newRegisteredRPC(receiver, method)
		if !matches {
			continue
		}
		if _, found := registered[rpc.durableName]; found {
			return nil, fmt.Errorf(
				"duplicate RPC method %q",
				rpc.durableName,
			)
		}
		registered[rpc.durableName] = rpc
	}
	return registered, nil
}

func newRegisteredRPC(
	receiver reflect.Value,
	method reflect.Method,
) (*registeredRPC, bool) {
	methodType := method.Type
	if !rpcMethodType(methodType, true) {
		return nil, false
	}
	result := reflect.Zero(methodType.Out(0)).Interface().(rpcResult)
	return &registeredRPC{
		method:      receiver.Method(method.Index),
		durableName: method.Name,
		input:       methodType.In(2),
		output:      result.rpcOutputType(),
	}, true
}

func (rpc *registeredRPC) invoke(
	ctx Context,
	input any,
) (rpcResult, error) {
	contextValue, err := reflectionArgument(ctx, contextType)
	if err != nil {
		return nil, err
	}
	inputValue, err := reflectionArgument(input, rpc.input)
	if err != nil {
		return nil, err
	}
	results := rpc.method.Call([]reflect.Value{contextValue, inputValue})
	if !results[1].IsNil() {
		return nil, results[1].Interface().(error)
	}
	return results[0].Interface().(rpcResult), nil
}

func rpcMethodName(rpc any) (string, error) {
	if rpc == nil {
		return "", fmt.Errorf("dex: RPC must be a direct bound Flow method")
	}
	value := reflect.ValueOf(rpc)
	if value.Kind() != reflect.Func || !rpcMethodType(value.Type(), false) {
		return "", fmt.Errorf("dex: RPC must be a direct bound Flow method")
	}
	function := runtime.FuncForPC(value.Pointer())
	if function == nil {
		return "", fmt.Errorf("dex: RPC method identity is unavailable")
	}
	runtimeName := function.Name()
	if !strings.HasSuffix(runtimeName, "-fm") {
		return "", fmt.Errorf("dex: RPC must be a direct bound Flow method")
	}
	canonical := strings.TrimSuffix(runtimeName, "-fm")
	separator := strings.LastIndex(canonical, ".")
	if separator < 0 || separator == len(canonical)-1 {
		return "", fmt.Errorf("dex: RPC method identity %q is invalid", runtimeName)
	}
	return canonical[separator+1:], nil
}

func rpcMethodType(methodType reflect.Type, hasReceiver bool) bool {
	inputOffset := 0
	expectedInputs := 2
	if hasReceiver {
		inputOffset = 1
		expectedInputs = 3
	}
	return methodType.NumIn() == expectedInputs &&
		methodType.In(inputOffset) == contextType &&
		methodType.NumOut() == 2 &&
		methodType.Out(0).Kind() == reflect.Struct &&
		methodType.Out(0).Implements(rpcResultType) &&
		methodType.Out(1) == errorType
}

func reflectionArgument(
	value any,
	targetType reflect.Type,
) (reflect.Value, error) {
	if value == nil {
		if isNilableType(targetType) {
			return reflect.Zero(targetType), nil
		}
		return reflect.Value{}, fmt.Errorf(
			"dex: nil is not assignable to %s",
			targetType,
		)
	}
	reflected := reflect.ValueOf(value)
	if !reflected.Type().AssignableTo(targetType) {
		return reflect.Value{}, fmt.Errorf(
			"dex: value type %s is not assignable to %s",
			reflected.Type(),
			targetType,
		)
	}
	return reflected, nil
}
