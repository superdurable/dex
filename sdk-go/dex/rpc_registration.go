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
	"fmt"
	"reflect"
	"runtime"
	"sort"
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

var flowInterfaceMethodNames = func() map[string]struct{} {
	flowType := reflect.TypeFor[Flow]()
	names := make(map[string]struct{}, flowType.NumMethod())
	for index := 0; index < flowType.NumMethod(); index++ {
		names[flowType.Method(index).Name] = struct{}{}
	}
	return names
}()

func discoverRPCs(flow Flow) (map[string]*registeredRPC, error) {
	receiver := reflect.ValueOf(flow)
	receiverType := receiver.Type()
	registered := make(map[string]*registeredRPC)
	var invalid []string
	for index := 0; index < receiverType.NumMethod(); index++ {
		method := receiverType.Method(index)
		if _, skip := flowInterfaceMethodNames[method.Name]; skip {
			continue
		}
		rpc, matches := newRegisteredRPC(receiver, method)
		if !matches {
			invalid = append(invalid, method.Name)
			continue
		}
		registered[rpc.durableName] = rpc
	}
	if len(invalid) > 0 {
		sort.Strings(invalid)
		return nil, fmt.Errorf(
			"exported methods %v must be RPCs with signature (Context, IN) (RPCResult[OUT], error)",
			invalid,
		)
	}
	if err := rejectPointerOnlyMethods(receiverType); err != nil {
		return nil, err
	}
	return registered, nil
}

func rejectPointerOnlyMethods(receiverType reflect.Type) error {
	if receiverType.Kind() == reflect.Pointer {
		return nil
	}
	pointerType := reflect.PointerTo(receiverType)
	var missing []string
	for index := 0; index < pointerType.NumMethod(); index++ {
		method := pointerType.Method(index)
		if _, skip := flowInterfaceMethodNames[method.Name]; skip {
			continue
		}
		if _, found := receiverType.MethodByName(method.Name); found {
			continue
		}
		missing = append(missing, method.Name)
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return fmt.Errorf(
		"exported methods %v have pointer receivers; register *%s, not %s",
		missing,
		receiverType.Name(),
		receiverType,
	)
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
