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
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"

	dexagents "github.com/superdurable/dex/agents/go/core"
	dexeino "github.com/superdurable/dex/agents/go/eino"
)

const noteSchema = `{"type":"object","description":"One note.","additionalProperties":false,` +
	`"properties":{"text":{"type":"string","description":"The note."},"priority":{"type":"integer","minimum":1}},` +
	`"required":["text"]}`

// recordingSource is a ToolSource whose calls are writes that record what the tool Step passed them.
type recordingSource struct {
	name     string
	tools    []dexagents.SourceToolDefinition
	listErr  error
	declared []string

	mutex    sync.Mutex
	executed []dexagents.DeferredToolExecution
}

func (source *recordingSource) SourceName() string { return source.name }

func (source *recordingSource) ListTools(context.Context) ([]dexagents.SourceToolDefinition, error) {
	return source.tools, source.listErr
}

func (source *recordingSource) DeclaredToolNames() []string { return source.declared }

func (source *recordingSource) DeferredToolCall(turn dexagents.AgentTurnContext, call dexagents.SourceToolCall) (dexagents.DeferredToolCall, error) {
	isApprovalRequired := false
	if policy, hasPolicy := turn.ToolPolicies[call.ToolName]; hasPolicy && policy.RequiresApproval != nil {
		isApprovalRequired = *policy.RequiresApproval
	}
	reference := "ref-" + call.ToolName
	return dexagents.DeferredToolCall{
		ToolCallID: call.ToolCallID, ToolName: call.ToolName, Arguments: call.Arguments,
		Policy:              dexagents.ToolPolicy{Effect: dexagents.ToolEffectNonIdempotentWrite, RequiresApproval: &isApprovalRequired},
		ToolSourceReference: &reference,
		Ordered:             true,
	}, nil
}

func (source *recordingSource) ExecuteDeferredToolCall(_ context.Context, call dexagents.DeferredToolExecution) (dexagents.DeferredToolOutcome, error) {
	source.mutex.Lock()
	defer source.mutex.Unlock()
	source.executed = append(source.executed, call)
	return &dexagents.ToolExecutionResult{Output: "noted by " + *call.ToolCall.ToolSourceReference}, nil
}

func (source *recordingSource) ToolCallStepOptions(toolCall dexagents.AgentToolCall) (*dexagents.ToolCallStepOptions, error) {
	if toolCall.ToolSourceReference == nil || *toolCall.ToolSourceReference != "ref-"+toolCall.ToolName {
		return nil, errors.New("the source got another reference")
	}
	return &dexagents.ToolCallStepOptions{ExecuteMethodTimeout: 42 * time.Second}, nil
}

func (source *recordingSource) executions() []dexagents.DeferredToolExecution {
	source.mutex.Lock()
	defer source.mutex.Unlock()
	return append([]dexagents.DeferredToolExecution{}, source.executed...)
}

func noteSource() *recordingSource {
	return &recordingSource{name: "ledger", tools: []dexagents.SourceToolDefinition{
		{Name: "ledger_post_note", Description: "Post a note.", InputSchema: json.RawMessage(noteSchema)},
	}}
}

// sourceAgent is an agent with one read tool, a counting tool middleware and Handler, and a source.
func sourceAgent(t *testing.T, scripted *scriptedChatModel, source *recordingSource, middlewareRuns, handlerWraps *atomic.Int64,
	options ...dexeino.Option) *dexeino.Runner {
	t.Helper()
	config := &adk.ChatModelAgentConfig{Name: "agent", Description: "Test agent.", Instruction: "Be brief.", Model: scripted,
		ToolsConfig: adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{
			Tools: []tool.BaseTool{noopTool("lookup_order")},
			ToolCallMiddlewares: []compose.ToolMiddleware{{Invokable: func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
				return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
					middlewareRuns.Add(1)
					return next(ctx, input)
				}
			}}},
		}},
		Handlers: []adk.ChatModelAgentMiddleware{countingToolHandler{BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{},
			wraps: handlerWraps}},
	}
	runner, err := dexeino.NewRunner(config, append([]dexeino.Option{
		dexeino.WithToolEffects(map[string]dexagents.ToolEffect{"lookup_order": dexagents.ToolEffectRead}),
		dexeino.WithToolSources(source),
	}, options...)...)
	require.NoError(t, err)
	return runner
}

// A source's tools are offered beside the agent's own, the source builds their calls, and each call
// runs through the source alone in its tool Step, outside Eino's tool middlewares and Handlers.
func TestSourceToolsAreOfferedAndRunThroughTheirSource(t *testing.T) {
	scripted := &scriptedChatModel{respond: func(input []*schema.Message) (*schema.Message, error) {
		if input[len(input)-1].Role == schema.Tool {
			return schema.AssistantMessage("done", nil), nil
		}
		return toolCallMessage(call("call_lookup", "lookup_order", `{"id":"1"}`), call("call_note", "ledger_post_note", ``)), nil
	}}
	source := noteSource()
	var middlewareRuns, handlerWraps atomic.Int64
	runner := sourceAgent(t, scripted, source, &middlewareRuns, &handlerWraps)
	state := newTurnState(t, runner, "lookup and note")
	requested := state.runModel(runner).(*dexagents.ToolCallsRequested)

	require.Equal(t, []string{"lookup_order", "ledger_post_note"}, scripted.tools[0])
	offered, err := scripted.toolInfos[0][1].ToJSONSchema()
	require.NoError(t, err)
	offeredJSON, err := json.Marshal(offered)
	require.NoError(t, err)
	require.JSONEq(t, noteSchema, string(offeredJSON), "the model sees the source's input schema")
	require.Equal(t, "Post a note.", scripted.toolInfos[0][1].Desc)

	lookup, note := requested.ToolCalls[0], requested.ToolCalls[1]
	require.Nil(t, lookup.ToolSourceReference, "an Eino tool's call carries no source reference")
	require.False(t, lookup.Ordered)
	require.Equal(t, `{"dex_tool_source": "ledger", "reference": "ref-ledger_post_note"}`, *note.ToolSourceReference)
	require.Equal(t, dexagents.ToolEffectNonIdempotentWrite, note.Policy.Effect, "the source picks the effect")
	require.True(t, note.Ordered, "the source picks the ordering")
	require.Equal(t, "{}", note.Arguments)

	stepOptions, err := runner.ToolCallStepOptions(state.records["call_note"])
	require.NoError(t, err)
	require.Equal(t, 42*time.Second, stepOptions.ExecuteMethodTimeout)
	stepOptions, err = runner.ToolCallStepOptions(state.records["call_lookup"])
	require.NoError(t, err)
	require.Nil(t, stepOptions)

	require.Empty(t, state.runTools(runner))
	executions := source.executions()
	require.Len(t, executions, 1)
	require.Equal(t, "ref-ledger_post_note", *executions[0].ToolCall.ToolSourceReference, "the source reads its own reference")
	require.Equal(t, "noted by ref-ledger_post_note", *state.records["call_note"].Output)
	require.Equal(t, int64(1), middlewareRuns.Load(), "only the Eino tool runs through the tool middleware")
	require.Equal(t, int64(1), handlerWraps.Load(), "only the Eino tool runs through the Handler")

	completed := state.runModel(runner).(*dexagents.TurnCompleted)
	require.Equal(t, "done", *completed.NewMessages[len(completed.NewMessages)-1].Text)
	require.Equal(t, []string{"lookup_order", "ledger_post_note"}, scripted.tools[1], "each model call lists the source again")
}

// A session policy reaches the source through the turn, as in the built-in loop.
func TestSessionPolicyReachesTheSource(t *testing.T) {
	scripted := &scriptedChatModel{respond: func([]*schema.Message) (*schema.Message, error) {
		return toolCallMessage(call("call_note", "ledger_post_note", `{"text":"hi"}`)), nil
	}}
	var middlewareRuns, handlerWraps atomic.Int64
	runner := sourceAgent(t, scripted, noteSource(), &middlewareRuns, &handlerWraps)
	state := newTurnState(t, runner, "note")
	turn := state.context()
	turn.ToolPolicies = map[string]dexagents.ToolPolicy{
		"ledger_post_note": {Effect: dexagents.ToolEffectNonIdempotentWrite, RequiresApproval: ptr(true)},
	}
	outcome, err := runner.RunModelStep(context.Background(), turn)
	require.NoError(t, err)
	require.True(t, outcome.(*dexagents.ToolCallsRequested).ToolCalls[0].Policy.IsApprovalRequired())
}

// With sources, a name that neither the agent nor a listing offers is unclassified without an
// approval, as in the built-in loop, and its tool Step answers it as unknown.
func TestUnlistedToolNameWithSourcesFailsAsUnknownWithoutApproval(t *testing.T) {
	scripted := &scriptedChatModel{respond: func([]*schema.Message) (*schema.Message, error) {
		return toolCallMessage(call("call_ghost", "ghost_tool", `{}`)), nil
	}}
	source := noteSource()
	var middlewareRuns, handlerWraps atomic.Int64
	runner := sourceAgent(t, scripted, source, &middlewareRuns, &handlerWraps)
	state := newTurnState(t, runner, "summon")
	requested := state.runModel(runner).(*dexagents.ToolCallsRequested)
	require.Equal(t, dexagents.ToolEffectUnclassified, requested.ToolCalls[0].Policy.Effect)
	require.False(t, requested.ToolCalls[0].Policy.IsApprovalRequired())
	require.Nil(t, requested.ToolCalls[0].ToolSourceReference)
	failures := state.runTools(runner)
	var toolError *dexagents.ToolExecutionError
	require.ErrorAs(t, failures["call_ghost"], &toolError)
	require.Equal(t, "unknown tool ghost_tool", toolError.Message)
	require.Empty(t, source.executions())
}

// A source whose listing fails offers nothing in that model call, and the turn goes on.
func TestFailedSourceListingOffersNoSourceTools(t *testing.T) {
	scripted := echoModel()
	source := noteSource()
	source.listErr = errors.New("the ledger is down")
	var middlewareRuns, handlerWraps atomic.Int64
	runner := sourceAgent(t, scripted, source, &middlewareRuns, &handlerWraps)
	state := newTurnState(t, runner, "hello")
	completed := state.runModel(runner).(*dexagents.TurnCompleted)
	require.Nil(t, completed.TurnError)
	require.Equal(t, []string{"lookup_order"}, scripted.tools[0])
}

// A listed tool whose input schema is not a JSON Schema is left out of the model call.
func TestListedToolWithABrokenSchemaIsNotOffered(t *testing.T) {
	scripted := echoModel()
	source := noteSource()
	source.tools = append(source.tools, dexagents.SourceToolDefinition{Name: "ledger_broken", InputSchema: json.RawMessage(`[1, 2]`)})
	var middlewareRuns, handlerWraps atomic.Int64
	runner := sourceAgent(t, scripted, source, &middlewareRuns, &handlerWraps)
	state := newTurnState(t, runner, "hello")
	state.runModel(runner)
	require.Equal(t, []string{"lookup_order", "ledger_post_note"}, scripted.tools[0])
}

// cachedSource is a recordingSource whose CachedTools the construction check reads.
type cachedSource struct {
	*recordingSource
}

func (source cachedSource) CachedTools() []dexagents.SourceToolDefinition { return source.tools }

// Construction refuses sources that collide with each other or with the agent's tools and aliases.
func TestConstructionRefusesCollidingToolSources(t *testing.T) {
	newConfig := func() *adk.ChatModelAgentConfig {
		tools := toolsConfig(noopTool("lookup_order"))
		tools.ToolAliases = map[string]compose.ToolAliasConfig{"lookup_order": {NameAliases: []string{"find_order"}}}
		return &adk.ChatModelAgentConfig{Name: "agent", Description: "Test agent.", Model: echoModel(), ToolsConfig: tools}
	}
	effects := dexeino.WithToolEffects(map[string]dexagents.ToolEffect{"lookup_order": dexagents.ToolEffectRead})
	brokenCache := noteSource()
	brokenCache.tools = []dexagents.SourceToolDefinition{{Name: "ledger_broken", InputSchema: json.RawMessage(`"object"`)}}
	for name, testCase := range map[string]struct {
		options []dexeino.Option
		refusal string
	}{
		"two sources of one name": {[]dexeino.Option{dexeino.WithToolSources(noteSource(), noteSource())},
			"two tool sources are named ledger"},
		"a declared name of an agent tool": {[]dexeino.Option{dexeino.WithToolSources(
			&recordingSource{name: "ledger", declared: []string{"lookup_order"}})}, "uses the name of a framework tool"},
		"a declared name of a tool alias": {[]dexeino.Option{dexeino.WithToolSources(
			&recordingSource{name: "ledger", declared: []string{"find_order"}})}, "uses the name of a framework tool"},
		"a zero listing timeout": {[]dexeino.Option{dexeino.WithToolSources(noteSource()), dexeino.WithToolSourceListingTimeout(0)},
			"listing timeout must be positive"},
		"a cached tool with a broken schema": {[]dexeino.Option{dexeino.WithToolSources(cachedSource{brokenCache})},
			"input schema of source tool ledger_broken"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := dexeino.NewRunner(newConfig(), append([]dexeino.Option{effects}, testCase.options...)...)
			require.ErrorContains(t, err, testCase.refusal)
		})
	}
	_, err := dexeino.NewRunner(newConfig(), effects, dexeino.WithToolSources(cachedSource{noteSource()}))
	require.NoError(t, err, "a valid cached tool builds")
}
