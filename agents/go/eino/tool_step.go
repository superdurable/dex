// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dexeino

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	dexagents "github.com/superdurable/dex/agents/go/core"
)

// einoADKPackagePath is the package of Eino ADK's own Handlers, such as its event senders.
const einoADKPackagePath = "github.com/cloudwego/eino/adk"

// ToolCallContext describes the Dex tool call that a tool Step runs.
//
// The adapter puts it in the context of every tool call it runs, so tool code
// and tool middlewares read it with ToolCallContextFrom. Pass IdempotencyKey to
// an external system that supports idempotency: it is the same on every attempt
// of the call. compose.GetToolCallID returns the same ToolCallID. The tools
// that the call's tool runs in turn, such as the tools of an adk.NewAgentTool
// sub-agent, do not get it; see ToolCallContextFrom. Since 0.6.16.
type ToolCallContext struct {
	// FlowID is the session ID, which is the Flow ID.
	FlowID string
	// TurnID is "turn-<n>" of the turn that made the call.
	TurnID string
	// ToolCallID is the tool call ID that the model, or the adapter, gave the call.
	ToolCallID string
	// ToolName is the tool name the model called.
	ToolName string
	// IdempotencyKey is "<flowID>/<turnID>/<toolCallID>", the same on every attempt.
	IdempotencyKey string
	// Attempt is the one-based tool Step attempt.
	Attempt int64
	// Effect is the call's effect after policies, as the session recorded it.
	Effect dexagents.ToolEffect
	// Events writes live events, bound to the tool call Step attempt.
	Events *dexagents.AgentEventWriter
	// Heartbeat records Step liveness. The session already heartbeats in the background.
	Heartbeat func() error
	// HeartbeatCheckpoint is the checkpoint an earlier attempt of this tool Step saved, or nil.
	HeartbeatCheckpoint *string
	// SaveHeartbeatCheckpoint records a heartbeat that carries a checkpoint for the next attempt.
	SaveHeartbeatCheckpoint func(checkpoint string) error
}

type toolCallContextKey struct{}

// toolStepCall is the context value of a tool Step's call.
type toolStepCall struct {
	call ToolCallContext
	// toolSegments counts the Eino tool address segments of the context the tool Step started from.
	toolSegments int
}

// withToolStepCall returns ctx carrying the tool Step's call, for the one-call ToolsNode to run it in.
func withToolStepCall(ctx context.Context, call ToolCallContext) context.Context {
	return context.WithValue(ctx, toolCallContextKey{}, toolStepCall{call: call, toolSegments: toolAddressSegments(ctx)})
}

// ToolCallContextFrom returns the Dex tool call of a tool Step's context.
//
// It returns false outside a tool Step, such as in a unit test that calls the
// tool directly. It also returns false in a tool that another tool runs
// through an Eino ToolsNode, such as a tool of an adk.NewAgentTool sub-agent
// or of a skill with context fork. Such a tool is not a Dex tool call: it runs
// inside the outer call's Step, again on each attempt, and its sub-agent's
// model may call it several times, so the outer call's IdempotencyKey and
// heartbeat checkpoint are not its own. Since 0.6.16.
func ToolCallContextFrom(ctx context.Context) (ToolCallContext, bool) {
	stepCall, isToolStep := ctx.Value(toolCallContextKey{}).(toolStepCall)
	if !isToolStep || toolAddressSegments(ctx) > stepCall.toolSegments+1 {
		return ToolCallContext{}, false
	}
	return stepCall.call, true
}

// toolAddressSegments counts the tool calls in ctx's Eino address: each ToolsNode adds one for the call it runs.
func toolAddressSegments(ctx context.Context) int {
	count := 0
	for _, segment := range compose.GetCurrentAddress(ctx) {
		if segment.Type == compose.AddressSegmentTool {
			count++
		}
	}
	return count
}

// refuseUnknownToolCall is the tool Step's UnknownToolsHandler: the model sees why nothing ran.
func refuseUnknownToolCall(_ context.Context, toolName string, _ string) (string, error) {
	return "", &dexagents.ToolExecutionError{Message: "unknown tool " + toolName}
}

// toolStepNodeConfig returns the ToolsNodeConfig that runs one call with the agent's tool middlewares.
//
// The order is Eino's, outermost first: ToolsConfig.ToolCallMiddlewares, then
// AgentMiddleware.WrapToolCall, then the Handlers' tool wrappers.
// Adapter middlewares in outermost wrap all of them.
func toolStepNodeConfig(config *agentConfig, tools []tool.BaseTool, outermost ...compose.ToolMiddleware) *compose.ToolsNodeConfig {
	middlewares := append(slices.Clone(outermost), config.agent.ToolsConfig.ToolCallMiddlewares...)
	for _, agentMiddleware := range config.agent.Middlewares {
		middlewares = append(middlewares, agentMiddleware.WrapToolCall)
	}
	for _, handler := range config.agent.Handlers {
		if !isEinoADKHandler(handler) {
			middlewares = append(middlewares, handlerToolMiddleware(handler))
		}
	}
	return &compose.ToolsNodeConfig{
		Tools:                tools,
		ToolAliases:          config.agent.ToolsConfig.ToolAliases,
		ToolArgumentsHandler: config.agent.ToolsConfig.ToolArgumentsHandler,
		UnknownToolsHandler:  refuseUnknownToolCall,
		ToolCallMiddlewares:  middlewares,
	}
}

// isEinoADKHandler reports whether a Handler is Eino ADK's own, such as an event sender that needs the agent run.
func isEinoADKHandler(handler adk.ChatModelAgentMiddleware) bool {
	handlerType := reflect.TypeOf(handler)
	for handlerType != nil && handlerType.Kind() == reflect.Pointer {
		handlerType = handlerType.Elem()
	}
	return handlerType != nil && handlerType.PkgPath() == einoADKPackagePath
}

// handlerToolMiddleware applies one Handler's four tool wrappers as a compose tool middleware.
func handlerToolMiddleware(handler adk.ChatModelAgentMiddleware) compose.ToolMiddleware {
	toolContext := func(input *compose.ToolInput) *adk.ToolContext {
		return &adk.ToolContext{Name: input.Name, CallID: input.CallID}
	}
	retarget := func(input *compose.ToolInput, arguments string, options []tool.Option) *compose.ToolInput {
		return &compose.ToolInput{Name: input.Name, CallID: input.CallID, Arguments: arguments, CallOptions: options}
	}
	return compose.ToolMiddleware{
		Invokable: func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
			return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
				endpoint, err := handler.WrapInvokableToolCall(ctx, func(ctx context.Context, arguments string, options ...tool.Option) (string, error) {
					output, err := next(ctx, retarget(input, arguments, options))
					if err != nil {
						return "", err
					}
					return output.Result, nil
				}, toolContext(input))
				if err != nil {
					return nil, err
				}
				result, err := endpoint(ctx, input.Arguments, input.CallOptions...)
				if err != nil {
					return nil, err
				}
				return &compose.ToolOutput{Result: result}, nil
			}
		},
		Streamable: func(next compose.StreamableToolEndpoint) compose.StreamableToolEndpoint {
			return func(ctx context.Context, input *compose.ToolInput) (*compose.StreamToolOutput, error) {
				endpoint, err := handler.WrapStreamableToolCall(ctx, func(ctx context.Context, arguments string, options ...tool.Option) (*schema.StreamReader[string], error) {
					output, err := next(ctx, retarget(input, arguments, options))
					if err != nil {
						return nil, err
					}
					return output.Result, nil
				}, toolContext(input))
				if err != nil {
					return nil, err
				}
				result, err := endpoint(ctx, input.Arguments, input.CallOptions...)
				if err != nil {
					return nil, err
				}
				return &compose.StreamToolOutput{Result: result}, nil
			}
		},
		EnhancedInvokable: func(next compose.EnhancedInvokableToolEndpoint) compose.EnhancedInvokableToolEndpoint {
			return func(ctx context.Context, input *compose.ToolInput) (*compose.EnhancedInvokableToolOutput, error) {
				endpoint, err := handler.WrapEnhancedInvokableToolCall(ctx, func(ctx context.Context, argument *schema.ToolArgument, options ...tool.Option) (*schema.ToolResult, error) {
					output, err := next(ctx, retarget(input, toolArgumentText(argument), options))
					if err != nil {
						return nil, err
					}
					return output.Result, nil
				}, toolContext(input))
				if err != nil {
					return nil, err
				}
				result, err := endpoint(ctx, &schema.ToolArgument{Text: input.Arguments}, input.CallOptions...)
				if err != nil {
					return nil, err
				}
				return &compose.EnhancedInvokableToolOutput{Result: result}, nil
			}
		},
		EnhancedStreamable: func(next compose.EnhancedStreamableToolEndpoint) compose.EnhancedStreamableToolEndpoint {
			return func(ctx context.Context, input *compose.ToolInput) (*compose.EnhancedStreamableToolOutput, error) {
				endpoint, err := handler.WrapEnhancedStreamableToolCall(ctx, func(ctx context.Context, argument *schema.ToolArgument, options ...tool.Option) (*schema.StreamReader[*schema.ToolResult], error) {
					output, err := next(ctx, retarget(input, toolArgumentText(argument), options))
					if err != nil {
						return nil, err
					}
					return output.Result, nil
				}, toolContext(input))
				if err != nil {
					return nil, err
				}
				result, err := endpoint(ctx, &schema.ToolArgument{Text: input.Arguments}, input.CallOptions...)
				if err != nil {
					return nil, err
				}
				return &compose.EnhancedStreamableToolOutput{Result: result}, nil
			}
		},
	}
}

func toolArgumentText(argument *schema.ToolArgument) string {
	if argument == nil {
		return ""
	}
	return argument.Text
}

// handlerAddedTools returns tools plus the ones the Handlers' BeforeAgent hooks add, as an agent run would.
//
// Reflection calls BeforeAgent, because its context type differs between Eino
// v0.9 and v0.10.
func handlerAddedTools(ctx context.Context, config *agentConfig, tools []tool.BaseTool) (added []tool.BaseTool, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			added, err = nil, fmt.Errorf("a Handler's BeforeAgent panicked: %v", recovered)
		}
	}()
	instruction := config.agent.Instruction
	for _, middleware := range config.agent.Middlewares {
		if middleware.AdditionalInstruction != "" {
			instruction = strings.TrimSpace(instruction + "\n" + middleware.AdditionalInstruction)
		}
	}
	current := slices.Clone(tools)
	for index, handler := range config.agent.Handlers {
		if isEinoADKHandler(handler) {
			continue
		}
		beforeAgent := reflect.ValueOf(handler).MethodByName("BeforeAgent")
		if !beforeAgent.IsValid() || beforeAgent.Type().NumIn() != 2 || beforeAgent.Type().NumOut() != 3 ||
			beforeAgent.Type().In(1).Kind() != reflect.Pointer {
			continue
		}
		agentContext := reflect.New(beforeAgent.Type().In(1).Elem())
		setField(agentContext.Elem(), "Instruction", reflect.ValueOf(instruction))
		setField(agentContext.Elem(), "Tools", reflect.ValueOf(current))
		setField(agentContext.Elem(), "ReturnDirectly", reflect.ValueOf(map[string]bool{}))
		results := beforeAgent.Call([]reflect.Value{reflect.ValueOf(ctx), agentContext})
		if failure, isError := results[2].Interface().(error); isError && failure != nil {
			return nil, fmt.Errorf("handler[%d] (%T) BeforeAgent failed: %w", index, handler, failure)
		}
		if nextContext, isContext := results[0].Interface().(context.Context); isContext && nextContext != nil {
			ctx = nextContext
		}
		if results[1].IsNil() {
			continue
		}
		if field := results[1].Elem().FieldByName("Tools"); field.IsValid() && field.CanInterface() {
			if added, isTools := field.Interface().([]tool.BaseTool); isTools {
				current = added
			}
		}
		if field := results[1].Elem().FieldByName("Instruction"); field.IsValid() && field.Kind() == reflect.String {
			instruction = field.String()
		}
	}
	return current, nil
}

func setField(structValue reflect.Value, name string, value reflect.Value) {
	field := structValue.FieldByName(name)
	if field.IsValid() && field.CanSet() && value.Type().AssignableTo(field.Type()) {
		field.Set(value)
	}
}

// classifyToolStepError returns the error a tool Step reports for a failed Eino tool call.
//
// A *dexagents.ToolExecutionError anywhere in the chain is the tool's
// definite answer. Any other error fails the Step attempt, so the session
// decides the outcome from the call's effect.
func classifyToolStepError(toolName string, err error) error {
	var toolError *dexagents.ToolExecutionError
	if errors.As(err, &toolError) {
		return toolError
	}
	if _, isInterrupt := compose.IsInterruptRerunError(err); isInterrupt {
		return fmt.Errorf("tool %s raised an Eino interrupt, which a Dex tool Step cannot answer; "+
			"use a Dex approval (ToolPolicy.RequiresApproval) instead: %w", toolName, err)
	}
	return err
}

// toolResultText returns the model-visible text of the one tool message a ToolsNode returned.
func toolResultText(messages []*schema.Message) (string, error) {
	if len(messages) != 1 || messages[0] == nil {
		return "", fmt.Errorf("dexeino: the ToolsNode returned %d messages for one tool call", len(messages))
	}
	message := messages[0]
	if message.Content != "" || len(message.UserInputMultiContent) == 0 {
		return message.Content, nil
	}
	parts := make([]string, 0, len(message.UserInputMultiContent))
	for _, part := range message.UserInputMultiContent {
		if part.Type == schema.ChatMessagePartTypeText {
			parts = append(parts, part.Text)
			continue
		}
		parts = append(parts, fmt.Sprintf("[%s output omitted: Dex stores text tool results]", part.Type))
	}
	return strings.Join(parts, "\n"), nil
}
