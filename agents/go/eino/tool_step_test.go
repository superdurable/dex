// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dexeino_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"

	dexagents "github.com/superdurable/dex/agents/go/core"
	dexeino "github.com/superdurable/dex/agents/go/eino"
)

// countingToolHandler counts its tool wrapper runs and the BeforeAgent tool it adds.
type countingToolHandler struct {
	*adk.BaseChatModelAgentMiddleware
	wraps     *atomic.Int64
	addedTool tool.BaseTool
}

func (handler countingToolHandler) WrapInvokableToolCall(_ context.Context, endpoint adk.InvokableToolCallEndpoint, toolContext *adk.ToolContext) (adk.InvokableToolCallEndpoint, error) {
	return func(ctx context.Context, arguments string, options ...tool.Option) (string, error) {
		handler.wraps.Add(1)
		output, err := endpoint(ctx, arguments, options...)
		return output + " [handler saw " + toolContext.CallID + "]", err
	}, nil
}

// definiteFailure is a tool error type that wraps a Dex definite failure.
type definiteFailure struct{ inner *dexagents.ToolExecutionError }

func (failure *definiteFailure) Error() string { return "order service: " + failure.inner.Message }
func (failure *definiteFailure) Unwrap() error { return failure.inner }

// Tool middlewares, AgentMiddleware.WrapToolCall, and Handler tool wrappers run once, in the tool Step,
// and the tool reads its call ID and Dex call there.
func TestToolStepRunsMiddlewaresOnceAndGivesTheToolItsCall(t *testing.T) {
	var middlewareRuns, agentMiddlewareRuns, handlerWraps, aliasArguments atomic.Int64
	countingMiddleware := func(counter *atomic.Int64) compose.ToolMiddleware {
		return compose.ToolMiddleware{Invokable: func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
			return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
				counter.Add(1)
				return next(ctx, input)
			}
		}}
	}
	echo := funcTool{name: "echo_call", run: func(ctx context.Context, arguments string) (string, error) {
		dexCall, isToolStep := dexeino.ToolCallContextFrom(ctx)
		if !isToolStep {
			return "", errors.New("no Dex call in the context")
		}
		return fmt.Sprintf("id=%s key=%s effect=%s args=%s", compose.GetToolCallID(ctx), dexCall.IdempotencyKey, dexCall.Effect, arguments), nil
	}}
	scripted := &scriptedChatModel{respond: func(input []*schema.Message) (*schema.Message, error) {
		if last := input[len(input)-1]; last.Role == schema.Tool {
			return schema.AssistantMessage("Echo: "+last.Content, nil), nil
		}
		return toolCallMessage(call("call_echo", "echo_alias", `{"identifier":"7"}`)), nil
	}}
	config := &adk.ChatModelAgentConfig{Name: "agent", Description: "Test agent.", Model: scripted,
		ToolsConfig: adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{
			Tools:               []tool.BaseTool{echo},
			ToolCallMiddlewares: []compose.ToolMiddleware{countingMiddleware(&middlewareRuns)},
			ToolAliases: map[string]compose.ToolAliasConfig{"echo_call": {
				NameAliases: []string{"echo_alias"}, ArgumentsAliases: map[string][]string{"id": {"identifier"}},
			}},
			ToolArgumentsHandler: func(_ context.Context, _ string, arguments string) (string, error) {
				aliasArguments.Add(1)
				return arguments, nil
			},
		}},
		Middlewares: []adk.AgentMiddleware{{WrapToolCall: countingMiddleware(&agentMiddlewareRuns)}},
		Handlers:    []adk.ChatModelAgentMiddleware{countingToolHandler{&adk.BaseChatModelAgentMiddleware{}, &handlerWraps, nil}},
	}
	runner, err := dexeino.NewRunner(config, dexeino.WithToolEffects(map[string]dexagents.ToolEffect{"echo_call": dexagents.ToolEffectRead}))
	require.NoError(t, err)
	state := newTurnState(t, runner, "echo")
	requested := state.runModel(runner).(*dexagents.ToolCallsRequested)
	require.Equal(t, "echo_alias", requested.ToolCalls[0].ToolName)
	require.Equal(t, dexagents.ToolEffectRead, requested.ToolCalls[0].Policy.Effect, "an alias takes its tool's effect")
	require.Zero(t, middlewareRuns.Load()+agentMiddlewareRuns.Load()+handlerWraps.Load()+aliasArguments.Load(),
		"no tool middleware runs inside RunModel")

	require.Empty(t, state.runTools(runner))
	require.Equal(t, int64(1), middlewareRuns.Load())
	require.Equal(t, int64(1), agentMiddlewareRuns.Load())
	require.Equal(t, int64(1), handlerWraps.Load())
	require.Equal(t, int64(1), aliasArguments.Load())
	require.Equal(t, `id=call_echo key=session/turn-1/call_echo effect=read args={"id":"7"} [handler saw call_echo]`,
		*state.records["call_echo"].Output)
}

// A *dexagents.ToolExecutionError anywhere in a tool's error chain is its definite failure; any
// other error, and an Eino interrupt, fails the Step attempt.
func TestToolStepClassifiesToolErrors(t *testing.T) {
	tools := []tool.BaseTool{
		funcTool{name: "typed_failure", run: func(context.Context, string) (string, error) {
			return "", &definiteFailure{inner: &dexagents.ToolExecutionError{Message: "order 9 is already shipped"}}
		}},
		funcTool{name: "plain_failure", run: func(context.Context, string) (string, error) {
			return "", errors.New("connection reset by peer")
		}},
		funcTool{name: "asks_a_person", run: func(ctx context.Context, _ string) (string, error) {
			return "", tool.Interrupt(ctx, "approve the refund?")
		}},
	}
	config := &adk.ChatModelAgentConfig{Name: "agent", Description: "Test agent.", Model: echoModel(), ToolsConfig: toolsConfig(tools...)}
	runner, err := dexeino.NewRunner(config, dexeino.WithDefaultToolEffect(dexagents.ToolEffectNonIdempotentWrite))
	require.NoError(t, err)
	execute := func(toolName string) error {
		_, err := runner.ExecuteDeferredToolCall(context.Background(), dexagents.DeferredToolExecution{
			FlowID: "session", Attempt: 1, IdempotencyKey: "session/turn-1/call",
			ToolCall: dexagents.AgentToolCall{ToolCallID: "call", TurnID: "turn-1", ToolName: toolName, Arguments: "{}",
				Effect: dexagents.ToolEffectNonIdempotentWrite, RequiresApproval: true, Status: dexagents.ToolCallStatusApproved},
		})
		return err
	}
	var toolError *dexagents.ToolExecutionError
	require.ErrorAs(t, execute("typed_failure"), &toolError)
	require.Equal(t, "order 9 is already shipped", toolError.Message)

	plain := execute("plain_failure")
	require.False(t, errors.As(plain, &toolError), "a plain error is not a definite failure")
	require.ErrorContains(t, plain, "connection reset by peer")

	interrupted := execute("asks_a_person")
	require.False(t, errors.As(interrupted, &toolError))
	require.ErrorContains(t, interrupted, "Eino interrupt")

	require.ErrorAs(t, execute("renamed_since"), &toolError)
	require.Equal(t, "unknown tool renamed_since", toolError.Message)
}

// A tool that a Handler adds in BeforeAgent is offered to the model, gets the default effect, and runs.
func TestHandlerAddedToolRunsInItsToolStep(t *testing.T) {
	var handlerWraps atomic.Int64
	dynamic := funcTool{name: "dynamic_tool", run: func(context.Context, string) (string, error) { return "dynamic ran", nil }}
	scripted := &scriptedChatModel{respond: func(input []*schema.Message) (*schema.Message, error) {
		if last := input[len(input)-1]; last.Role == schema.Tool {
			return schema.AssistantMessage(last.Content, nil), nil
		}
		return toolCallMessage(call("call_dynamic", "dynamic_tool", `{}`)), nil
	}}
	config := &adk.ChatModelAgentConfig{Name: "agent", Description: "Test agent.", Model: scripted,
		Handlers: []adk.ChatModelAgentMiddleware{countingToolHandler{&adk.BaseChatModelAgentMiddleware{}, &handlerWraps, dynamic}}}
	runner, err := dexeino.NewRunner(config)
	require.NoError(t, err)
	state := newTurnState(t, runner, "dynamic")
	requested := state.runModel(runner).(*dexagents.ToolCallsRequested)
	require.Equal(t, []string{"dynamic_tool"}, scripted.tools[0])
	require.Equal(t, dexagents.ToolEffectUnclassified, requested.ToolCalls[0].Policy.Effect)
	require.True(t, requested.ToolCalls[0].Policy.IsApprovalRequired())
	require.Empty(t, state.runTools(runner))
	require.Equal(t, "dynamic ran [handler saw call_dynamic]", *state.records["call_dynamic"].Output)

	declared, err := dexeino.NewRunner(config, dexeino.WithToolEffects(map[string]dexagents.ToolEffect{"dynamic_tool": dexagents.ToolEffectRead}))
	require.NoError(t, err)
	requested = newTurnState(t, declared, "dynamic").runModel(declared).(*dexagents.ToolCallsRequested)
	require.Equal(t, dexagents.ToolEffectRead, requested.ToolCalls[0].Policy.Effect, "WithToolEffects names a run-time tool's effect")
	require.False(t, requested.ToolCalls[0].Policy.IsApprovalRequired())
}

// A call recorded with a weaker effect than this deploy gives its tool does not run.
func TestToolStepRefusesAToolWhoseEffectGotStricter(t *testing.T) {
	var runs atomic.Int64
	refund := funcTool{name: "refund", run: func(context.Context, string) (string, error) { runs.Add(1); return "refunded", nil }}
	config := &adk.ChatModelAgentConfig{Name: "agent", Description: "Test agent.", Model: echoModel(), ToolsConfig: toolsConfig(refund)}
	runner, err := dexeino.NewRunner(config, dexeino.WithToolEffects(map[string]dexagents.ToolEffect{"refund": dexagents.ToolEffectNonIdempotentWrite}))
	require.NoError(t, err)
	_, err = runner.ExecuteDeferredToolCall(context.Background(), dexagents.DeferredToolExecution{
		FlowID: "session", Attempt: 1,
		ToolCall: dexagents.AgentToolCall{ToolCallID: "call", TurnID: "turn-1", ToolName: "refund", Arguments: "{}",
			Effect: dexagents.ToolEffectRead, Status: dexagents.ToolCallStatusApproved},
	})
	var toolError *dexagents.ToolExecutionError
	require.ErrorAs(t, err, &toolError)
	require.True(t, strings.Contains(toolError.Message, "stricter effect"), toolError.Message)
	require.Zero(t, runs.Load())
}

// BuildUnfinishedTurnMessages stores a result for every finished call whose result is not stored yet.
func TestUnfinishedTurnMessagesPairEveryStoredCall(t *testing.T) {
	var toolRuns atomic.Int64
	config, _ := lookupAgent(t, &toolRuns)
	runner, err := dexeino.NewRunner(config, dexeino.WithToolEffects(map[string]dexagents.ToolEffect{"lookup_order": dexagents.ToolEffectRead}))
	require.NoError(t, err)
	state := newTurnState(t, runner, "lookup 7")
	state.runModel(runner)
	require.Empty(t, state.runTools(runner))
	messages, err := runner.BuildUnfinishedTurnMessages(context.Background(), state.context())
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.Equal(t, dexagents.MessageRoleTool, messages[0].Role)
	require.Equal(t, "RunModel-1-0", *messages[0].ToolCallID)
	require.Equal(t, "order 7 is shipped", *messages[0].ToolOutput)
}
