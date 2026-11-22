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
	"io"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"

	dexagents "github.com/superdurable/dex/agents/go/core"
	dexeino "github.com/superdurable/dex/agents/go/eino"
)

func readRunner(t *testing.T, scripted *scriptedChatModel, options ...dexeino.Option) *dexeino.Runner {
	t.Helper()
	config := &adk.ChatModelAgentConfig{Name: "agent", Description: "Test agent.", Instruction: "Be brief.",
		Model: scripted, ToolsConfig: toolsConfig(noopTool("lookup_order"))}
	runner, err := dexeino.NewRunner(config, append([]dexeino.Option{
		dexeino.WithToolEffects(map[string]dexagents.ToolEffect{"lookup_order": dexagents.ToolEffectRead}),
	}, options...)...)
	require.NoError(t, err)
	return runner
}

// Empty, "/"-containing, and duplicate tool call IDs get unique IDs derived from the Step execution,
// so a retry of the same attempt stores the same IDs.
func TestToolCallIDsAreSynthesizedForEmptySlashAndDuplicateIDs(t *testing.T) {
	scripted := &scriptedChatModel{respond: func([]*schema.Message) (*schema.Message, error) {
		return toolCallMessage(
			call("", "lookup_order", `{"id":"1"}`),
			call("a/b", "lookup_order", `{"id":"2"}`),
			call("dup", "lookup_order", `{"id":"3"}`),
			call("dup", "lookup_order", `{"id":"4"}`),
			call("used-earlier", "lookup_order", `{"id":"5"}`),
			call("call_ok", "lookup_order", ``),
		), nil
	}}
	runner := readRunner(t, scripted)
	idsOf := func() []string {
		state := newTurnState(t, runner, "lookup everything")
		state.records["used-earlier"] = dexagents.AgentToolCall{ToolCallID: "used-earlier", Status: dexagents.ToolCallStatusSucceeded,
			Output: ptr("earlier"), ToolName: "lookup_order", TurnID: "turn-1"}
		state.runState = ptr(`{"v":1,"model_calls":1}`)
		requested := state.runModel(runner).(*dexagents.ToolCallsRequested)
		ids := []string{}
		for _, deferred := range requested.ToolCalls {
			ids = append(ids, deferred.ToolCallID)
		}
		require.Equal(t, "{}", requested.ToolCalls[5].Arguments, "empty arguments become an empty object")
		require.Equal(t, `{"v":1,"model_calls":2}`, requested.FrameworkRunState)
		return ids
	}
	ids := idsOf()
	require.Equal(t, []string{"RunModel-1-0", "RunModel-1-1", "dup", "RunModel-1-3", "RunModel-1-4", "call_ok"}, ids)
	require.Equal(t, ids, idsOf(), "the same Step execution synthesizes the same IDs")
}

// doubleCallingHandler calls the model twice from inside the gate, as a retrying wrapper would.
type doubleCallingHandler struct {
	*adk.BaseChatModelAgentMiddleware
}

type doubleCallingModel struct {
	inner model.BaseModel[*schema.Message]
}

func (handler doubleCallingHandler) WrapModel(_ context.Context, inner model.BaseModel[*schema.Message], _ *adk.ModelContext) (model.BaseModel[*schema.Message], error) {
	return doubleCallingModel{inner: inner}, nil
}

func (double doubleCallingModel) Generate(ctx context.Context, input []*schema.Message, options ...model.Option) (*schema.Message, error) {
	if _, err := double.inner.Generate(ctx, input, options...); err != nil {
		return nil, err
	}
	return double.inner.Generate(ctx, input, options...)
}

func (double doubleCallingModel) Stream(ctx context.Context, input []*schema.Message, options ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	message, err := double.Generate(ctx, input, options...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{message}), nil
}

// requireTurnError returns the TurnError of an outcome that ends the turn as failed without an error.
func requireTurnError(t *testing.T, outcome dexagents.AgentTurnOutcome, err error) *dexagents.TurnCompleted {
	t.Helper()
	require.NoError(t, err, "a stop a retry would repeat is no Step error, so RunModel never pays for it again")
	completed, isCompleted := outcome.(*dexagents.TurnCompleted)
	require.True(t, isCompleted, "outcome %T", outcome)
	require.NotNil(t, completed.TurnError)
	return completed
}

// A wrapper that makes a second model call ends the turn as failed, with the one call it made billed.
func TestSecondModelCallInOneStepEndsTheTurnAsFailed(t *testing.T) {
	scripted := echoModel()
	config := &adk.ChatModelAgentConfig{Name: "agent", Description: "Test agent.", Model: scripted,
		Handlers: []adk.ChatModelAgentMiddleware{doubleCallingHandler{&adk.BaseChatModelAgentMiddleware{}}}}
	runner, err := dexeino.NewRunner(config)
	require.NoError(t, err)
	state := newTurnState(t, runner, "hello")
	outcome, err := runner.RunModelStep(context.Background(), state.context())
	completed := requireTurnError(t, outcome, err)
	require.Contains(t, *completed.TurnError, "second model call")
	require.Equal(t, int64(1), completed.Usage.ModelCalls, "the call that ran is billed")
	require.Empty(t, completed.NewMessages, "the refused call's message is not stored")
	require.Equal(t, 1, scripted.calls(), "the second call never reaches the model")
}

// The turn's model calls stop at the agent's MaxIterations, Eino's per-run limit; the unstored
// tool results are stored, so every stored call keeps its result.
func TestMaxIterationsEndsTheTurnAsFailed(t *testing.T) {
	scripted := &scriptedChatModel{respond: func([]*schema.Message) (*schema.Message, error) {
		return toolCallMessage(call("call_1", "lookup_order", `{"id":"1"}`)), nil
	}}
	config := &adk.ChatModelAgentConfig{Name: "agent", Description: "Test agent.", Model: scripted, MaxIterations: 1,
		ToolsConfig: toolsConfig(noopTool("lookup_order"))}
	runner, err := dexeino.NewRunner(config, dexeino.WithDefaultToolEffect(dexagents.ToolEffectRead))
	require.NoError(t, err)
	state := newTurnState(t, runner, "hello")
	state.runModel(runner)
	require.Empty(t, state.runTools(runner))
	outcome, err := runner.RunModelStep(context.Background(), state.context())
	completed := requireTurnError(t, outcome, err)
	require.Equal(t, "the turn reached the Eino agent's MaxIterations of 1 model calls", *completed.TurnError)
	require.Zero(t, completed.Usage.ModelCalls)
	require.Len(t, completed.NewMessages, 1)
	require.Equal(t, dexagents.MessageRoleTool, completed.NewMessages[0].Role)
	require.Equal(t, "call_1", *completed.NewMessages[0].ToolCallID)
	require.Equal(t, 1, scripted.calls())
}

// A builder configuration that NewRunner would refuse ends the turn as failed before any model call.
func TestRefusedBuilderConfigurationEndsTheTurnAsFailed(t *testing.T) {
	scripted := echoModel()
	runner, err := dexeino.NewRunnerWithBuilder(func(_ context.Context, build dexeino.AgentBuildContext) (*adk.ChatModelAgentConfig, error) {
		config := &adk.ChatModelAgentConfig{Name: "agent", Description: "Test agent.", Model: scripted}
		if build.Purpose == dexeino.BuildPurposeModelCall {
			config.ModelRetryConfig = &adk.ModelRetryConfig{MaxRetries: 2}
		}
		return config, nil
	})
	require.NoError(t, err)
	state := newTurnState(t, runner, "hello")
	outcome, err := runner.RunModelStep(context.Background(), state.context())
	completed := requireTurnError(t, outcome, err)
	require.Contains(t, *completed.TurnError, "cannot run in Dex Step mode: ModelRetryConfig")
	require.Zero(t, scripted.calls())
}

// With streaming, text and reasoning reach AgentEvents as deltas, and streamed tool calls get IDs.
func TestStreamingWritesDeltasAndCapturesTheMessage(t *testing.T) {
	scripted := &scriptedChatModel{respond: func(input []*schema.Message) (*schema.Message, error) {
		if input[len(input)-1].Role == schema.Tool {
			return schema.AssistantMessage("Hello there", nil), nil
		}
		return toolCallMessage(call("", "lookup_order", `{"id":"1"}`)), nil
	}}
	scripted.chunks = func(message *schema.Message) []*schema.Message {
		if len(message.ToolCalls) > 0 {
			index := 0
			first := message.ToolCalls[0]
			first.Index = &index
			first.Function.Arguments = `{"id":`
			second := schema.ToolCall{Index: &index, Function: schema.FunctionCall{Arguments: `"1"}`}}
			return []*schema.Message{
				{Role: schema.Assistant, ReasoningContent: "checking"},
				{Role: schema.Assistant, ToolCalls: []schema.ToolCall{first}},
				{Role: schema.Assistant, ToolCalls: []schema.ToolCall{second}},
			}
		}
		return []*schema.Message{{Role: schema.Assistant, Content: "Hello "}, {Role: schema.Assistant, Content: "there",
			ResponseMeta: &schema.ResponseMeta{Usage: &schema.TokenUsage{PromptTokens: 12, CompletionTokens: 3}}}}
	}
	runner := readRunner(t, scripted, dexeino.WithStreaming(true))
	state := newTurnState(t, runner, "lookup 1")
	requested := state.runModel(runner).(*dexagents.ToolCallsRequested)
	require.Equal(t, "RunModel-1-0", requested.ToolCalls[0].ToolCallID)
	require.Equal(t, `{"id":"1"}`, requested.ToolCalls[0].Arguments)
	require.Empty(t, state.runTools(runner))
	completed := state.runModel(runner).(*dexagents.TurnCompleted)
	require.Equal(t, "Hello there", *completed.NewMessages[1].Text)
	require.Equal(t, dexagents.AgentUsage{ModelCalls: 1, InputTokens: 12, OutputTokens: 3}, completed.Usage)

	var deltas []string
	for _, event := range state.events {
		if event.Type == string(dexagents.AgentEventTypeTextDelta) || event.Type == string(dexagents.AgentEventTypeReasoningDelta) {
			deltas = append(deltas, fmt.Sprintf("%s:%v", event.Type, decodeArguments(t, event.Payload)["text"]))
		}
	}
	require.Equal(t, []string{"reasoning_delta:checking", "text_delta:Hello ", "text_delta:there"}, deltas)
}

// httpStatusCodeError has the status field of go-openai's and Ark's errors, which the Go core reads since 0.6.16.
type httpStatusCodeError struct{ HTTPStatusCode int }

func (failure *httpStatusCodeError) Error() string {
	return fmt.Sprintf("error, status code: %d", failure.HTTPStatusCode)
}

type statusCodeError struct{ StatusCode int }

func (failure *statusCodeError) Error() string { return fmt.Sprintf("status %d", failure.StatusCode) }

type headerError struct {
	HTTPStatusCode int
	Header         http.Header
}

func (failure *headerError) Error() string { return "limited" }

// headerWrapper carries response headers around an error that holds the status.
type headerWrapper struct {
	Header http.Header
	cause  error
}

func (wrapper *headerWrapper) Error() string { return "request failed: " + wrapper.cause.Error() }
func (wrapper *headerWrapper) Unwrap() error { return wrapper.cause }

// A model error keeps its chain; the adapter wraps it only to expose a status or headers the Go core would not read.
func TestModelErrorsExposeTheirHTTPStatus(t *testing.T) {
	for name, testCase := range map[string]struct {
		failure    error
		isWrapped  bool
		status     int
		retryAfter string
	}{
		"HTTPStatusCode the core reads":         {&httpStatusCodeError{HTTPStatusCode: 429}, false, 429, ""},
		"wrapped HTTPStatusCode the core reads": {fmt.Errorf("openai: %w", &httpStatusCodeError{HTTPStatusCode: 503}), false, 503, ""},
		"HTTPStatusCode with headers the core would not read": {&headerError{HTTPStatusCode: 429,
			Header: http.Header{"Retry-After": []string{"7"}}}, true, 429, "7"},
		"headers outside the HTTPStatusCode error": {&headerWrapper{Header: http.Header{"Retry-After": []string{"3"}},
			cause: &httpStatusCodeError{HTTPStatusCode: 503}}, true, 503, "3"},
		"StatusCode already readable": {&statusCodeError{StatusCode: 400}, false, 400, ""},
	} {
		for _, isStreaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s streaming=%v", name, isStreaming), func(t *testing.T) {
				scripted := &scriptedChatModel{respond: func([]*schema.Message) (*schema.Message, error) {
					return nil, testCase.failure
				}}
				runner := readRunner(t, scripted, dexeino.WithStreaming(isStreaming))
				state := newTurnState(t, runner, "hello")
				_, err := runner.RunModelStep(context.Background(), state.context())
				require.Error(t, err)
				var exposed *dexeino.ModelAPIError
				require.Equal(t, testCase.isWrapped, errors.As(err, &exposed))
				if testCase.isWrapped {
					require.Equal(t, testCase.status, exposed.StatusCode)
					require.ErrorIs(t, exposed, testCase.failure)
					if testCase.retryAfter != "" {
						require.Equal(t, testCase.retryAfter, exposed.Response.Header.Get("Retry-After"))
					}
				}
				require.ErrorIs(t, err, testCase.failure, "the provider's own error stays reachable")
			})
		}
	}
}

// A mid-stream error is exposed like one at the start of the call.
func TestMidStreamModelErrorIsExposed(t *testing.T) {
	failing := &midStreamModel{failure: &headerError{HTTPStatusCode: 502, Header: http.Header{"Retry-After": []string{"2"}}}}
	config := &adk.ChatModelAgentConfig{Name: "agent", Description: "Test agent.", Model: failing}
	runner, err := dexeino.NewRunner(config, dexeino.WithStreaming(true))
	require.NoError(t, err)
	state := newTurnState(t, runner, "hello")
	_, err = runner.RunModelStep(context.Background(), state.context())
	var exposed *dexeino.ModelAPIError
	require.ErrorAs(t, err, &exposed)
	require.Equal(t, 502, exposed.StatusCode)
}

type midStreamModel struct{ failure error }

func (failing *midStreamModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	return nil, failing.failure
}

func (failing *midStreamModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	reader, writer := schema.Pipe[*schema.Message](2)
	go func() {
		writer.Send(&schema.Message{Role: schema.Assistant, Content: "partial "}, nil)
		writer.Send(nil, failing.failure)
		writer.Close()
	}()
	return reader, nil
}

func (failing *midStreamModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return failing, nil
}

// The model may call a tool it was never offered; the call is deferred as a read the tool Step refuses.
func TestHallucinatedToolIsAnsweredAsUnknown(t *testing.T) {
	scripted := &scriptedChatModel{respond: func(input []*schema.Message) (*schema.Message, error) {
		if last := input[len(input)-1]; last.Role == schema.Tool {
			return schema.AssistantMessage("Ghost: "+last.Content, nil), nil
		}
		return toolCallMessage(call("call_ghost", "ghost_tool", `{}`)), nil
	}}
	runner := readRunner(t, scripted)
	state := newTurnState(t, runner, "summon")
	requested := state.runModel(runner).(*dexagents.ToolCallsRequested)
	require.Equal(t, dexagents.ToolEffectRead, requested.ToolCalls[0].Policy.Effect)
	require.False(t, requested.ToolCalls[0].Policy.IsApprovalRequired())
	failures := state.runTools(runner)
	var toolError *dexagents.ToolExecutionError
	require.ErrorAs(t, failures["call_ghost"], &toolError)
	require.Equal(t, "unknown tool ghost_tool", toolError.Message)
}

// The instruction and tools of each model call come from the configuration current at that call.
func TestEachModelCallUsesTheCurrentConfiguration(t *testing.T) {
	scripted := &scriptedChatModel{respond: func(input []*schema.Message) (*schema.Message, error) {
		if input[len(input)-1].Role == schema.Tool {
			return schema.AssistantMessage("done", nil), nil
		}
		return toolCallMessage(call("call_1", "lookup_order", `{"id":"1"}`)), nil
	}}
	var instruction atomic.Value
	instruction.Store("instruction v1")
	var builds []dexeino.AgentBuildContext
	runner, err := dexeino.NewRunnerWithBuilder(func(_ context.Context, build dexeino.AgentBuildContext) (*adk.ChatModelAgentConfig, error) {
		builds = append(builds, build)
		tools := toolsConfig(noopTool("lookup_order"))
		if instruction.Load() == "instruction v2" {
			tools = toolsConfig(noopTool("lookup_order"), noopTool("lookup_v2"))
		}
		return &adk.ChatModelAgentConfig{Name: "agent", Description: "Test agent.", Instruction: instruction.Load().(string),
			Model: scripted, ToolsConfig: tools}, nil
	}, dexeino.WithDefaultToolEffect(dexagents.ToolEffectRead))
	require.NoError(t, err)
	state := newTurnState(t, runner, "lookup 1")
	state.runModel(runner)
	require.Empty(t, state.runTools(runner))
	instruction.Store("instruction v2")
	state.runModel(runner)

	require.Equal(t, "instruction v2", scripted.lastInput()[0].Content)
	require.Equal(t, []string{"lookup_order", "lookup_v2"}, scripted.tools[1])
	require.Equal(t, []dexeino.BuildPurpose{dexeino.BuildPurposeCheck, dexeino.BuildPurposeModelCall,
		dexeino.BuildPurposeToolCall, dexeino.BuildPurposeModelCall}, purposes(builds))
	require.Equal(t, "call_1", builds[2].ToolCallID)
	require.Empty(t, builds[2].SessionSettings)
}

// The model is chosen from the turn's session settings.
func TestBuilderChoosesTheModelFromSessionSettings(t *testing.T) {
	models := map[string]*scriptedChatModel{}
	for _, name := range []string{"alpha", "beta"} {
		modelName := name
		models[name] = &scriptedChatModel{respond: func([]*schema.Message) (*schema.Message, error) {
			return schema.AssistantMessage("model "+modelName, nil), nil
		}}
	}
	runner, err := dexeino.NewRunnerWithBuilder(func(_ context.Context, build dexeino.AgentBuildContext) (*adk.ChatModelAgentConfig, error) {
		chosen, isAllowed := models[fmt.Sprint(build.SessionSettings["model"])]
		if !isAllowed {
			chosen = models["alpha"]
		}
		return &adk.ChatModelAgentConfig{Name: "agent", Description: "Test agent.", Model: chosen}, nil
	})
	require.NoError(t, err)
	state := newTurnState(t, runner, "which model")
	turn := state.context()
	turn.SessionSettings = map[string]any{"model": "beta"}
	outcome, err := runner.RunModelStep(context.Background(), turn)
	require.NoError(t, err)
	require.Equal(t, "model beta", *outcome.(*dexagents.TurnCompleted).NewMessages[0].Text)
}

func purposes(builds []dexeino.AgentBuildContext) []dexeino.BuildPurpose {
	result := make([]dexeino.BuildPurpose, 0, len(builds))
	for _, build := range builds {
		result = append(result, build.Purpose)
	}
	return result
}

// An Eino interrupt the adapter did not raise ends the turn as failed instead of being treated as a tool call.
func TestForeignInterruptEndsTheTurnAsFailed(t *testing.T) {
	scripted := echoModel()
	config := &adk.ChatModelAgentConfig{Name: "agent", Description: "Test agent.", Model: scripted,
		Handlers: []adk.ChatModelAgentMiddleware{interruptingHandler{&adk.BaseChatModelAgentMiddleware{}}}}
	runner, err := dexeino.NewRunner(config)
	require.NoError(t, err)
	state := newTurnState(t, runner, "hello")
	outcome, err := runner.RunModelStep(context.Background(), state.context())
	completed := requireTurnError(t, outcome, err)
	require.Contains(t, *completed.TurnError, "did not raise")
}

type interruptingHandler struct {
	*adk.BaseChatModelAgentMiddleware
}

func (interruptingHandler) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, _ *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	return ctx, state, compose.Interrupt(ctx, "a person must look first")
}

// unregisteredExtra is a provider type that Eino's state serializer does not know.
type unregisteredExtra struct{ Kind string }

// A model message whose Extra holds a type Eino cannot serialize, such as eino-ext Claude's tool
// search events, still stops at its tool calls, and the message is stored as JSON.
func TestModelMessageWithAnUnregisteredExtraTypeStopsAtItsToolCalls(t *testing.T) {
	for _, isStreaming := range []bool{false, true} {
		t.Run(fmt.Sprintf("streaming=%v", isStreaming), func(t *testing.T) {
			scripted := &scriptedChatModel{respond: func([]*schema.Message) (*schema.Message, error) {
				message := toolCallMessage(call("call_1", "lookup_order", `{"id":"7"}`))
				message.Extra = map[string]any{"provider_events": []unregisteredExtra{{Kind: "search"}}}
				message.ToolCalls[0].Extra = map[string]any{"provider_event": unregisteredExtra{Kind: "call"}}
				return message, nil
			}}
			runner := readRunner(t, scripted, dexeino.WithStreaming(isStreaming))
			state := newTurnState(t, runner, "lookup 7")
			outcome, err := runner.RunModelStep(context.Background(), state.context())
			require.NoError(t, err)
			requested, isRequested := outcome.(*dexagents.ToolCallsRequested)
			require.True(t, isRequested, "outcome %T", outcome)
			require.Equal(t, "call_1", requested.ToolCalls[0].ToolCallID)
			require.Contains(t, requested.NewMessages[0].FrameworkItem, `"provider_events":[{"Kind":"search"}]`)
			require.Equal(t, 1, scripted.calls())
		})
	}
}

// A tool call ID of only whitespace is blank to the session, so it gets a synthesized ID too.
func TestBlankToolCallIDsAreSynthesized(t *testing.T) {
	scripted := &scriptedChatModel{respond: func([]*schema.Message) (*schema.Message, error) {
		return toolCallMessage(
			call(" ", "lookup_order", `{"id":"1"}`),
			call("\t\n", "lookup_order", `{"id":"2"}`),
			call(" \u001f", "lookup_order", `{"id":"3"}`),
			call(" call_padded ", "lookup_order", `{"id":"4"}`),
		), nil
	}}
	runner := readRunner(t, scripted)
	requested := newTurnState(t, runner, "lookup").runModel(runner).(*dexagents.ToolCallsRequested)
	ids := []string{}
	for _, deferred := range requested.ToolCalls {
		ids = append(ids, deferred.ToolCallID)
	}
	require.Equal(t, []string{"RunModel-1-0", "RunModel-1-1", "RunModel-1-2", " call_padded "}, ids)
}

// retryingHandler retries a failed model call once, as a user's retry wrapper would.
type retryingHandler struct {
	*adk.BaseChatModelAgentMiddleware
}

type retryingModel struct {
	inner model.BaseModel[*schema.Message]
}

func (retryingHandler) WrapModel(_ context.Context, inner model.BaseModel[*schema.Message], _ *adk.ModelContext) (model.BaseModel[*schema.Message], error) {
	return retryingModel{inner: inner}, nil
}

func (retrying retryingModel) Generate(ctx context.Context, input []*schema.Message, options ...model.Option) (*schema.Message, error) {
	message, err := retrying.inner.Generate(ctx, input, options...)
	if err != nil {
		return retrying.inner.Generate(ctx, input, options...)
	}
	return message, nil
}

func (retrying retryingModel) Stream(ctx context.Context, input []*schema.Message, options ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	stream, err := retrying.inner.Stream(ctx, input, options...)
	if err == nil {
		var chunks []*schema.Message
		for {
			chunk, recvErr := stream.Recv()
			if errors.Is(recvErr, io.EOF) {
				return schema.StreamReaderFromArray(chunks), nil
			}
			if recvErr != nil {
				break
			}
			chunks = append(chunks, chunk)
		}
	}
	return retrying.inner.Stream(ctx, input, options...)
}

// When the one model call fails and a wrapper tries again, the first call's error reaches the
// session, so the session backs off a 429 instead of ending the turn, and no usage is billed.
func TestFailedModelCallErrorReachesTheSessionWhenAWrapperRetries(t *testing.T) {
	rateLimited := &statusCodeError{StatusCode: 429}
	for name, failing := range map[string]model.ToolCallingChatModel{
		"failed call": &scriptedChatModel{respond: func([]*schema.Message) (*schema.Message, error) {
			return nil, rateLimited
		}},
		"failed mid-stream": &midStreamModel{failure: rateLimited},
	} {
		for _, isStreaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s streaming=%v", name, isStreaming), func(t *testing.T) {
				config := &adk.ChatModelAgentConfig{Name: "agent", Description: "Test agent.", Model: failing,
					Handlers: []adk.ChatModelAgentMiddleware{retryingHandler{&adk.BaseChatModelAgentMiddleware{}}}}
				runner, err := dexeino.NewRunner(config, dexeino.WithStreaming(isStreaming))
				require.NoError(t, err)
				state := newTurnState(t, runner, "hello")
				outcome, err := runner.RunModelStep(context.Background(), state.context())
				require.Nil(t, outcome)
				require.ErrorIs(t, err, rateLimited, "the session classifies the call's own error")
			})
		}
	}
}

// Streamed usage merges per field, as Eino merges chunks, so input tokens on the first chunk count.
func TestStreamedUsageMergesEveryChunk(t *testing.T) {
	scripted := &scriptedChatModel{
		respond: func([]*schema.Message) (*schema.Message, error) { return schema.AssistantMessage("hi", nil), nil },
		chunks: func(*schema.Message) []*schema.Message {
			first := schema.AssistantMessage("h", nil)
			first.ResponseMeta = &schema.ResponseMeta{Usage: &schema.TokenUsage{PromptTokens: 100, CompletionTokens: 1,
				PromptTokenDetails: schema.PromptTokenDetails{CachedTokens: 40}}}
			last := schema.AssistantMessage("i", nil)
			last.ResponseMeta = &schema.ResponseMeta{Usage: &schema.TokenUsage{CompletionTokens: 50,
				CompletionTokensDetails: schema.CompletionTokensDetails{ReasoningTokens: 9}}}
			return []*schema.Message{first, last}
		},
	}
	config := &adk.ChatModelAgentConfig{Name: "agent", Description: "Test agent.", Model: scripted}
	runner, err := dexeino.NewRunner(config, dexeino.WithStreaming(true))
	require.NoError(t, err)
	completed := newTurnState(t, runner, "hello").runModel(runner).(*dexagents.TurnCompleted)
	require.Equal(t, dexagents.AgentUsage{InputTokens: 100, OutputTokens: 50, CachedInputTokens: 40, ReasoningTokens: 9, ModelCalls: 1},
		completed.Usage)
}

// A ModelAPIError built without a cause, as a user's model wrapper may build one, names its status.
func TestModelAPIErrorWithoutACauseNamesItsStatus(t *testing.T) {
	modelError := &dexeino.ModelAPIError{StatusCode: 503}
	require.Equal(t, "the model API answered HTTP 503", modelError.Error())
	require.NoError(t, modelError.Unwrap())
}

// A run-time builder configuration whose tools construction would refuse ends the turn as failed
// before any model call, as an unsupported setting does.
func TestRefusedBuilderToolsEndTheTurnAsFailed(t *testing.T) {
	for name, testCase := range map[string]struct {
		tools               []tool.BaseTool
		aliases             map[string]compose.ToolAliasConfig
		executeSequentially bool
		expected            string
	}{
		"two tools of one name": {[]tool.BaseTool{noopTool("lookup"), noopTool("lookup")}, nil, false, `two tools are named "lookup"`},
		"an alias of another tool": {[]tool.BaseTool{noopTool("lookup"), noopTool("refund")},
			map[string]compose.ToolAliasConfig{"lookup": {NameAliases: []string{"refund"}}}, false,
			`"refund" of "lookup" is the name of another tool`},
		"a tool without an effect": {[]tool.BaseTool{noopTool("lookup"), noopTool("refund")}, nil, false,
			`tool "refund" has no Dex effect`},
		"tool calls in order": {[]tool.BaseTool{noopTool("lookup")}, nil, true, "ExecuteSequentially"},
	} {
		t.Run(name, func(t *testing.T) {
			scripted := echoModel()
			runner, err := dexeino.NewRunnerWithBuilder(func(_ context.Context, build dexeino.AgentBuildContext) (*adk.ChatModelAgentConfig, error) {
				config := &adk.ChatModelAgentConfig{Name: "agent", Description: "Test agent.", Model: scripted,
					ToolsConfig: toolsConfig(noopTool("lookup"))}
				if build.Purpose == dexeino.BuildPurposeModelCall {
					config.ToolsConfig = toolsConfig(testCase.tools...)
					config.ToolsConfig.ToolAliases = testCase.aliases
					config.ToolsConfig.ExecuteSequentially = testCase.executeSequentially
				}
				return config, nil
			}, dexeino.WithToolEffects(map[string]dexagents.ToolEffect{"lookup": dexagents.ToolEffectRead}))
			require.NoError(t, err)
			outcome, err := runner.RunModelStep(context.Background(), newTurnState(t, runner, "hello").context())
			completed := requireTurnError(t, outcome, err)
			require.Contains(t, *completed.TurnError, "cannot run in Dex Step mode: ")
			require.Contains(t, *completed.TurnError, testCase.expected)
			require.Zero(t, scripted.calls())
		})
	}
}

// rewritingHandler changes the model's message in the agent state after the model call.
type rewritingHandler struct {
	*adk.BaseChatModelAgentMiddleware
	rewrite func(message *schema.Message)
}

func (handler rewritingHandler) AfterModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, _ *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	last := *state.Messages[len(state.Messages)-1]
	handler.rewrite(&last)
	state.Messages = append(state.Messages[:len(state.Messages)-1:len(state.Messages)-1], &last)
	return ctx, state, nil
}

// A Handler that changes the model message's tool calls after the model call ends the turn as
// failed, with the call billed, instead of failing the Step for a paid retry.
func TestHandlerThatChangesToolCallsEndsTheTurnAsFailed(t *testing.T) {
	for name, testCase := range map[string]struct {
		response func() *schema.Message
		rewrite  func(message *schema.Message)
		expected string
	}{
		"renamed call IDs": {func() *schema.Message { return toolCallMessage(call("call_1", "lookup_order", `{}`)) },
			func(message *schema.Message) {
				message.ToolCalls = []schema.ToolCall{call("call_other", "lookup_order", `{}`)}
			}, "tool calls changed after the model call"},
		"dropped calls": {func() *schema.Message { return toolCallMessage(call("call_1", "lookup_order", `{}`)) },
			func(message *schema.Message) { message.ToolCalls = nil }, "tool calls changed after the model call"},
		"added calls": {func() *schema.Message { return schema.AssistantMessage("no tools needed", nil) },
			func(message *schema.Message) {
				message.ToolCalls = []schema.ToolCall{call("call_added", "lookup_order", `{}`)}
			}, "stopped at tool calls its model message does not contain"},
	} {
		t.Run(name, func(t *testing.T) {
			scripted := &scriptedChatModel{respond: func([]*schema.Message) (*schema.Message, error) { return testCase.response(), nil }}
			config := &adk.ChatModelAgentConfig{Name: "agent", Description: "Test agent.", Model: scripted,
				ToolsConfig: toolsConfig(noopTool("lookup_order")),
				Handlers:    []adk.ChatModelAgentMiddleware{rewritingHandler{&adk.BaseChatModelAgentMiddleware{}, testCase.rewrite}}}
			runner, err := dexeino.NewRunner(config, dexeino.WithToolEffects(map[string]dexagents.ToolEffect{"lookup_order": dexagents.ToolEffectRead}))
			require.NoError(t, err)
			outcome, err := runner.RunModelStep(context.Background(), newTurnState(t, runner, "lookup").context())
			completed := requireTurnError(t, outcome, err)
			require.Contains(t, *completed.TurnError, testCase.expected)
			require.Equal(t, int64(1), completed.Usage.ModelCalls)
			require.Empty(t, completed.NewMessages)
		})
	}
}
