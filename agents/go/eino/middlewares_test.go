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
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/filesystem"
	fsmiddleware "github.com/cloudwego/eino/adk/middlewares/filesystem"
	"github.com/cloudwego/eino/adk/middlewares/reduction"
	"github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"

	dexagents "github.com/superdurable/dex/agents/go/core"
	dexeino "github.com/superdurable/dex/agents/go/eino"
)

// roundsModel calls the tools of one round per model call after the last user message, then answers
// with every tool result it saw, as "<tool>=<result>" joined by " | ".
func roundsModel(rounds ...[]schema.ToolCall) *scriptedChatModel {
	return &scriptedChatModel{respond: func(input []*schema.Message) (*schema.Message, error) {
		lastUser := 0
		for index, message := range input {
			if message.Role == schema.User {
				lastUser = index
			}
		}
		toolRounds := 0
		var results []string
		for _, message := range input[lastUser:] {
			if message.Role == schema.Assistant && len(message.ToolCalls) > 0 {
				toolRounds++
			}
			if message.Role == schema.Tool {
				results = append(results, message.ToolName+"="+message.Content)
			}
		}
		if toolRounds < len(rounds) {
			return toolCallMessage(rounds[toolRounds]...), nil
		}
		return schema.AssistantMessage(strings.Join(results, " | "), nil), nil
	}}
}

// largeResultTool returns 60 characters and its arguments, enough for any small reduction threshold.
func largeResultTool() funcTool {
	return funcTool{name: "fetch_page", run: func(_ context.Context, arguments string) (string, error) {
		return strings.Repeat("x", 60) + arguments, nil
	}}
}

// placeholderHandler replaces the content of the last tool result in place before the model call.
type placeholderHandler struct {
	*adk.BaseChatModelAgentMiddleware
}

func (placeholderHandler) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, _ *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	if last := state.Messages[len(state.Messages)-1]; last.Role == schema.Tool {
		last.Content = "[cleared in place]"
	}
	return ctx, state, nil
}

// A before-model hook that clears a tool result in place, as Eino's legacy reduction middleware
// does, changes what that model call sees, but Dex still stores the result the tool returned: the
// stored conversation is the record, and the hook runs again before every model call.
func TestBeforeModelHooksThatClearToolResultsInPlaceKeepTheStoredResults(t *testing.T) {
	clear, err := reduction.NewClearToolResult(context.Background(), &reduction.ClearToolResultConfig{
		ToolResultTokenThreshold: 5, KeepRecentTokens: 10})
	require.NoError(t, err)
	for name, testCase := range map[string]struct {
		middlewares []adk.AgentMiddleware
		handlers    []adk.ChatModelAgentMiddleware
		modelSaw    string
	}{
		"the legacy reduction middleware": {middlewares: []adk.AgentMiddleware{clear},
			modelSaw: "fetch_page=[Old tool result content cleared] | fetch_page=" + strings.Repeat("x", 60) + `{"id":"2"}`},
		"a Handler's BeforeModelRewriteState": {handlers: []adk.ChatModelAgentMiddleware{placeholderHandler{&adk.BaseChatModelAgentMiddleware{}}},
			modelSaw: "fetch_page=" + strings.Repeat("x", 60) + `{"id":"1"} | fetch_page=[cleared in place]`},
	} {
		for _, isStreaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s, streaming %v", name, isStreaming), func(t *testing.T) {
				scripted := roundsModel([]schema.ToolCall{call("call_1", "fetch_page", `{"id":"1"}`), call("call_2", "fetch_page", `{"id":"2"}`)})
				runner, err := dexeino.NewRunner(&adk.ChatModelAgentConfig{Name: "agent", Description: "Test agent.", Model: scripted,
					ToolsConfig: toolsConfig(largeResultTool()), Middlewares: testCase.middlewares, Handlers: testCase.handlers},
					dexeino.WithToolEffects(map[string]dexagents.ToolEffect{"fetch_page": dexagents.ToolEffectRead}),
					dexeino.WithStreaming(isStreaming))
				require.NoError(t, err)
				state := newTurnState(t, runner, "fetch two pages")
				state.runModel(runner)
				require.Empty(t, state.runTools(runner))
				completed := state.runModel(runner).(*dexagents.TurnCompleted)
				require.Nil(t, completed.TurnError)
				require.Equal(t, testCase.modelSaw, *state.messages[len(state.messages)-1].Text)

				stored := map[string]string{}
				for _, message := range completed.NewMessages {
					if message.Role == dexagents.MessageRoleTool {
						stored[*message.ToolCallID] = *message.ToolOutput
						decoded, err := dexeino.DecodeMessage(dexagents.AgentMessage{Role: message.Role, FrameworkItem: message.FrameworkItem})
						require.NoError(t, err)
						require.Equal(t, *message.ToolOutput, decoded.Content)
					}
				}
				require.Equal(t, map[string]string{
					"call_1": strings.Repeat("x", 60) + `{"id":"1"}`, "call_2": strings.Repeat("x", 60) + `{"id":"2"}`,
				}, stored, "the stored results are the tools' results")
			})
		}
	}
}

// historyReadingAgentTool returns an Eino agent tool whose sub-agent counts its runs.
func historyReadingAgentTool(t *testing.T, runs *atomic.Int64, options ...adk.AgentToolOption) tool.BaseTool {
	subModel := &scriptedChatModel{respond: func(input []*schema.Message) (*schema.Message, error) {
		runs.Add(1)
		return schema.AssistantMessage(fmt.Sprintf("researched with %d messages", len(input)), nil), nil
	}}
	researcher, err := adk.NewChatModelAgent(context.Background(), &adk.ChatModelAgentConfig{Name: "researcher",
		Description: "Researches a question.", Model: subModel})
	require.NoError(t, err)
	return adk.NewAgentTool(context.Background(), researcher, options...)
}

// An Eino agent tool runs its sub-agent in its tool Step. With WithFullChatHistoryAsInput it reads
// the parent agent's history there, which a Dex tool Step does not have, so the adapter refuses it at
// construction, ends a turn whose builder returns one, and answers a call of a Handler-added one
// without running it.
func TestAgentToolsThatReadTheAgentHistoryAreRefused(t *testing.T) {
	effects := dexeino.WithToolEffects(map[string]dexagents.ToolEffect{"researcher": dexagents.ToolEffectRead})
	question := roundsModel([]schema.ToolCall{call("call_research", "researcher", `{"request":"why"}`)})
	var runs atomic.Int64

	plain := historyReadingAgentTool(t, &runs)
	runner, err := dexeino.NewRunner(&adk.ChatModelAgentConfig{Name: "agent", Description: "Test agent.", Model: question,
		ToolsConfig: toolsConfig(plain)}, effects)
	require.NoError(t, err)
	state := newTurnState(t, runner, "research")
	state.runModel(runner)
	require.Empty(t, state.runTools(runner))
	require.Equal(t, "researched with 1 messages", *state.records["call_research"].Output, "a plain agent tool runs")
	require.Equal(t, int64(1), runs.Load())

	fullHistory := historyReadingAgentTool(t, &runs, adk.WithFullChatHistoryAsInput())
	for name, config := range map[string]*adk.ChatModelAgentConfig{
		"tool": {Name: "agent", Description: "Test agent.", Model: question, ToolsConfig: toolsConfig(fullHistory)},
		"AgentMiddleware tool": {Name: "agent", Description: "Test agent.", Model: question,
			Middlewares: []adk.AgentMiddleware{{AdditionalTools: []tool.BaseTool{fullHistory}}}},
	} {
		_, err := dexeino.NewRunner(config, effects)
		require.ErrorContains(t, err, `tool "researcher" is an Eino agent tool with WithFullChatHistoryAsInput`, name)
	}

	builder, err := dexeino.NewRunnerWithBuilder(func(_ context.Context, build dexeino.AgentBuildContext) (*adk.ChatModelAgentConfig, error) {
		config := &adk.ChatModelAgentConfig{Name: "agent", Description: "Test agent.", Model: question, ToolsConfig: toolsConfig(plain)}
		if build.Purpose == dexeino.BuildPurposeModelCall {
			config.ToolsConfig = toolsConfig(fullHistory)
		}
		return config, nil
	}, effects)
	require.NoError(t, err)
	outcome, err := builder.RunModelStep(context.Background(), newTurnState(t, builder, "research").context())
	completed := requireTurnError(t, outcome, err)
	require.Contains(t, *completed.TurnError, "cannot run in Dex Step mode: tool \"researcher\" is an Eino agent tool with WithFullChatHistoryAsInput")

	added, err := dexeino.NewRunner(&adk.ChatModelAgentConfig{Name: "agent", Description: "Test agent.", Model: question,
		Handlers: []adk.ChatModelAgentMiddleware{countingToolHandler{&adk.BaseChatModelAgentMiddleware{}, &atomic.Int64{}, fullHistory}}}, effects)
	require.NoError(t, err)
	state = newTurnState(t, added, "research")
	state.runModel(added)
	failures := state.runTools(added)
	var toolError *dexagents.ToolExecutionError
	require.ErrorAs(t, failures["call_research"], &toolError, "a definite failure: nothing ran")
	require.Contains(t, toolError.Message, "WithFullChatHistoryAsInput")
	require.Equal(t, int64(1), runs.Load(), "the sub-agent never ran")
}

// skillBackend serves skills from memory, one per context mode.
type skillBackend struct{}

func (skillBackend) List(context.Context) ([]skill.FrontMatter, error) {
	return []skill.FrontMatter{
		{Name: "release_notes", Description: "Writes release notes."},
		{Name: "triage", Description: "Triages an issue in a fresh sub-agent.", Context: skill.ContextModeFork},
		{Name: "review", Description: "Reviews with the conversation.", Context: skill.ContextModeForkWithContext},
	}, nil
}

func (backend skillBackend) Get(ctx context.Context, name string) (skill.Skill, error) {
	matters, err := backend.List(ctx)
	if err != nil {
		return skill.Skill{}, err
	}
	for _, matter := range matters {
		if matter.Name == name {
			return skill.Skill{FrontMatter: matter, Content: "Follow the " + name + " steps.", BaseDirectory: "/skills/" + name}, nil
		}
	}
	return skill.Skill{}, fmt.Errorf("skill %s not found", name)
}

// skillAgentHub returns a sub-agent that answers with the number of messages it got and counts its runs.
type skillAgentHub struct {
	runs *atomic.Int64
}

func (hub skillAgentHub) Get(ctx context.Context, _ string, _ *skill.AgentHubOptions) (adk.Agent, error) {
	subModel := &scriptedChatModel{respond: func(input []*schema.Message) (*schema.Message, error) {
		hub.runs.Add(1)
		return schema.AssistantMessage(fmt.Sprintf("sub-agent got %d messages", len(input)), nil), nil
	}}
	return adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{Name: "skill_agent", Description: "Runs a skill.", Model: subModel})
}

// A skill with context fork runs its sub-agent in the skill tool's Step. A skill with context
// fork_with_context reads the parent agent's history there, which a Dex tool Step does not have,
// so its call is a definite failure that names the mode, and the sub-agent never runs.
func TestSkillThatForksWithContextIsADefiniteToolFailure(t *testing.T) {
	for _, isStreaming := range []bool{false, true} {
		t.Run(fmt.Sprintf("streaming %v", isStreaming), func(t *testing.T) {
			var runs atomic.Int64
			handler, err := skill.NewMiddleware(context.Background(), &skill.Config{Backend: skillBackend{}, AgentHub: skillAgentHub{&runs}})
			require.NoError(t, err)
			scripted := roundsModel([]schema.ToolCall{call("call_triage", "skill", `{"skill":"triage"}`),
				call("call_review", "skill", `{"skill":"review"}`)})
			runner, err := dexeino.NewRunner(&adk.ChatModelAgentConfig{Name: "agent", Description: "Test agent.", Model: scripted,
				Handlers: []adk.ChatModelAgentMiddleware{handler}},
				dexeino.WithToolEffects(map[string]dexagents.ToolEffect{"skill": dexagents.ToolEffectRead}), dexeino.WithStreaming(isStreaming))
			require.NoError(t, err)
			state := newTurnState(t, runner, "triage and review")
			state.runModel(runner)
			failures := state.runTools(runner)
			require.Contains(t, *state.records["call_triage"].Output, "sub-agent got 1 messages")
			require.Equal(t, int64(1), runs.Load(), "only the fork skill's sub-agent ran")
			var toolError *dexagents.ToolExecutionError
			require.ErrorAs(t, failures["call_review"], &toolError)
			require.Equal(t, "tool skill runs a skill with context fork_with_context, which reads the Eino agent's history, "+
				"and a Dex tool Step does not have it; use context fork instead", toolError.Message)
		})
	}
}

// Eino's filesystem read_file schema marks offset and limit required, although its description says
// to omit them. The tool Step checks arguments against that schema, so a call without them is
// refused with the schema's text, and a ToolArgumentsHandler that fills them in lets it run.
func TestFilesystemReadFileFollowsItsSchema(t *testing.T) {
	backend := filesystem.NewInMemoryBackend()
	require.NoError(t, backend.Write(context.Background(), &filesystem.WriteRequest{FilePath: "/notes.txt", Content: "hello"}))
	handler, err := fsmiddleware.New(context.Background(), &fsmiddleware.MiddlewareConfig{Backend: backend})
	require.NoError(t, err)
	fillReadDefaults := func(_ context.Context, name, arguments string) (string, error) {
		if name != fsmiddleware.ToolNameReadFile {
			return arguments, nil
		}
		decoded := map[string]any{"offset": 0, "limit": 0}
		if err := json.Unmarshal([]byte(arguments), &decoded); err != nil {
			return "", err
		}
		encoded, err := json.Marshal(decoded)
		return string(encoded), err
	}
	for name, argumentsHandler := range map[string]func(context.Context, string, string) (string, error){
		"without a ToolArgumentsHandler": nil, "with a ToolArgumentsHandler": fillReadDefaults,
	} {
		t.Run(name, func(t *testing.T) {
			scripted := roundsModel([]schema.ToolCall{call("call_read", "read_file", `{"file_path":"/notes.txt"}`)})
			config := &adk.ChatModelAgentConfig{Name: "agent", Description: "Test agent.", Model: scripted,
				Handlers: []adk.ChatModelAgentMiddleware{handler}}
			config.ToolsConfig.ToolArgumentsHandler = argumentsHandler
			runner, err := dexeino.NewRunner(config, dexeino.WithToolEffects(map[string]dexagents.ToolEffect{"read_file": dexagents.ToolEffectRead}))
			require.NoError(t, err)
			state := newTurnState(t, runner, "read the notes")
			state.runModel(runner)
			failures := state.runTools(runner)
			if argumentsHandler == nil {
				var toolError *dexagents.ToolExecutionError
				require.True(t, errors.As(failures["call_read"], &toolError))
				require.Equal(t, "invalid arguments for read_file: arguments.offset is required", toolError.Message)
				return
			}
			require.Empty(t, failures)
			require.Equal(t, "     1\thello", *state.records["call_read"].Output)
		})
	}
}
