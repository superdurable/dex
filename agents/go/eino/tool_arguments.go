// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dexeino

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/agents/go/core/builtinloop"
)

// noParametersSchema accepts any JSON object.
var noParametersSchema = json.RawMessage(`{}`)

// argumentsCheck checks a call's arguments against the schema the tool's Info reports now.
//
// It returns the tool Step's outermost tool middleware. Eino converts the
// arguments first (ArgumentsAliases, then ToolArgumentsHandler), so the
// middleware sees the converted text. It refuses arguments that break the
// schema before any tool middleware, Handler wrapper, or the tool runs, with
// the built-in loop's *dexagents.ToolExecutionError "invalid arguments for
// <name>: <problem>", because the check is a builtinloop.FunctionTool whose
// function does nothing. It returns an error when Info fails or the schema
// does not encode.
func argumentsCheck(ctx context.Context, toolName string, checked tool.BaseTool) (compose.ToolMiddleware, error) {
	info, err := checked.Info(ctx)
	if err != nil {
		return compose.ToolMiddleware{}, &toolInfoError{cause: err}
	}
	parameters, err := toolParametersSchema(info)
	if err != nil {
		return compose.ToolMiddleware{}, fmt.Errorf("dexeino: tool %s parameters: %w", toolName, err)
	}
	checker, err := builtinloop.NewFunctionTool(toolName, "", parameters, dexagents.ToolEffectRead, acceptCheckedArguments)
	if err != nil {
		return compose.ToolMiddleware{}, fmt.Errorf("dexeino: tool %s parameters: %w", toolName, err)
	}
	check := func(ctx context.Context, input *compose.ToolInput) error {
		_, err := checker.Invoke(ctx, input.Arguments, builtinloop.ToolCallContext{})
		return err
	}
	return compose.ToolMiddleware{
		Invokable: func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
			return checkedEndpoint(check, next)
		},
		Streamable: func(next compose.StreamableToolEndpoint) compose.StreamableToolEndpoint {
			return checkedEndpoint(check, next)
		},
		EnhancedInvokable: func(next compose.EnhancedInvokableToolEndpoint) compose.EnhancedInvokableToolEndpoint {
			return checkedEndpoint(check, next)
		},
		EnhancedStreamable: func(next compose.EnhancedStreamableToolEndpoint) compose.EnhancedStreamableToolEndpoint {
			return checkedEndpoint(check, next)
		},
	}, nil
}

// acceptCheckedArguments is the checker's function, reached only by arguments that passed.
func acceptCheckedArguments(context.Context, json.RawMessage, builtinloop.ToolCallContext) (string, error) {
	return "", nil
}

// checkedEndpoint calls next only when check accepts the call's arguments.
func checkedEndpoint[Output any](
	check func(ctx context.Context, input *compose.ToolInput) error,
	next func(ctx context.Context, input *compose.ToolInput) (Output, error),
) func(ctx context.Context, input *compose.ToolInput) (Output, error) {
	return func(ctx context.Context, input *compose.ToolInput) (Output, error) {
		if err := check(ctx, input); err != nil {
			var refused Output
			return refused, err
		}
		return next(ctx, input)
	}
}

// toolParametersSchema returns the JSON Schema a tool's Info gives its arguments.
//
// It returns noParametersSchema for a tool without ParamsOneOf and for an
// empty schema, which Eino encodes as true, and an error when the schema does
// not encode.
func toolParametersSchema(info *schema.ToolInfo) (json.RawMessage, error) {
	if info == nil || info.ParamsOneOf == nil {
		return noParametersSchema, nil
	}
	parameters, err := info.ToJSONSchema()
	if err != nil {
		return nil, err
	}
	if parameters == nil {
		return noParametersSchema, nil
	}
	encoded, err := json.Marshal(parameters)
	if err != nil {
		return nil, err
	}
	if !bytes.HasPrefix(bytes.TrimSpace(encoded), []byte("{")) {
		return noParametersSchema, nil
	}
	return encoded, nil
}
