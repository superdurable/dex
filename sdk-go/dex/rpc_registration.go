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
	"runtime"
	"strings"
)

type registeredRPC struct {
	handler     RPCDef
	durableName string
	identity    string
	input       reflect.Type
	output      reflect.Type
	options     *RPCOptions
}

var (
	contextType   = reflect.TypeFor[Context]()
	errorType     = reflect.TypeFor[error]()
	rpcResultType = reflect.TypeFor[rpcResult]()
)

func (rpc *registeredRPC) invoke(
	ctx Context,
	input any,
) (rpcResult, error) {
	return rpc.handler.invoke(ctx, input)
}

func rpcMethodName(rpc any) (string, error) {
	canonical, err := rpcMethodIdentity(rpc)
	if err != nil {
		return "", err
	}
	separator := strings.LastIndex(canonical, ".")
	if separator < 0 || separator == len(canonical)-1 {
		return "", fmt.Errorf("dex: RPC method identity %q is invalid", canonical)
	}
	return canonical[separator+1:], nil
}

func rpcMethodIdentity(rpc any) (string, error) {
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
	return strings.TrimSuffix(runtimeName, "-fm"), nil
}

func rpcMethodType(methodType reflect.Type, hasReceiver bool) bool {
	inputOffset := 0
	expectedInputs := 2
	if hasReceiver {
		inputOffset = 1
		expectedInputs = 3
	}
	if methodType.NumIn() != expectedInputs || methodType.NumOut() != 2 {
		return false
	}
	resultType := methodType.Out(0)
	return methodType.In(inputOffset) == contextType &&
		resultType.Kind() == reflect.Pointer &&
		resultType.Elem().Kind() == reflect.Struct &&
		resultType.Implements(rpcResultType) &&
		methodType.Out(1) == errorType
}
