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
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/filesystem"
	fsmiddleware "github.com/cloudwego/eino/adk/middlewares/filesystem"
	"github.com/cloudwego/eino/adk/middlewares/plantask"
	"github.com/cloudwego/eino/adk/middlewares/reduction"
	"github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
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

// memoryTaskFiles is a plantask Backend in memory, keyed by full path.
type memoryTaskFiles struct {
	mutex sync.Mutex
	files map[string]string
}

func (backend *memoryTaskFiles) LsInfo(_ context.Context, request *plantask.LsInfoRequest) ([]plantask.FileInfo, error) {
	backend.mutex.Lock()
	defer backend.mutex.Unlock()
	var infos []plantask.FileInfo
	for path := range backend.files {
		if filepath.Dir(path) == strings.TrimSuffix(request.Path, "/") {
			infos = append(infos, plantask.FileInfo{Path: path})
		}
	}
	return infos, nil
}

func (backend *memoryTaskFiles) Read(_ context.Context, request *plantask.ReadRequest) (*filesystem.FileContent, error) {
	backend.mutex.Lock()
	defer backend.mutex.Unlock()
	content, isFound := backend.files[request.FilePath]
	if !isFound {
		return nil, fmt.Errorf("no task file %s", request.FilePath)
	}
	return &filesystem.FileContent{Content: content}, nil
}

func (backend *memoryTaskFiles) Write(_ context.Context, request *plantask.WriteRequest) error {
	backend.mutex.Lock()
	defer backend.mutex.Unlock()
	backend.files[request.FilePath] = request.Content
	return nil
}

func (backend *memoryTaskFiles) Delete(_ context.Context, request *plantask.DeleteRequest) error {
	backend.mutex.Lock()
	defer backend.mutex.Unlock()
	delete(backend.files, request.FilePath)
	return nil
}

// orderedCalls returns whether each requested call runs in the model's order, by tool call ID.
func orderedCalls(requested *dexagents.ToolCallsRequested) map[string]bool {
	ordered := map[string]bool{}
	for _, toolCall := range requested.ToolCalls {
		ordered[toolCall.ToolCallID] = toolCall.Ordered
	}
	return ordered
}

// Eino's plantask tools share one lock per agent run, so in native Eino two TaskCreate calls of one
// response take task numbers one after the other. Dex runs each call in its own Step, possibly on
// another Worker, so the adapter asks the session to run the plantask calls in the model's order:
// the session starts one write at a time and ignores the order for a read. Other tools still run
// in parallel.
func TestPlanTaskCallsRunInTheModelsOrder(t *testing.T) {
	backend := &memoryTaskFiles{files: map[string]string{}}
	handler, err := plantask.New(context.Background(), &plantask.Config{Backend: backend, BaseDir: "/tasks"})
	require.NoError(t, err)
	scripted := roundsModel([]schema.ToolCall{
		call("create_docs", "TaskCreate", `{"subject":"Write the docs","description":"Docs."}`),
		call("create_tests", "TaskCreate", `{"subject":"Write the tests","description":"Tests."}`),
		call("list_tasks", "TaskList", `{}`),
		call("lookup", "lookup_order", `{"id":"1"}`),
	})
	lookup := funcTool{name: "lookup_order", run: func(context.Context, string) (string, error) { return "shipped", nil }}
	runner, err := dexeino.NewRunner(&adk.ChatModelAgentConfig{Name: "agent", Description: "Test agent.", Model: scripted,
		ToolsConfig: toolsConfig(lookup), Handlers: []adk.ChatModelAgentMiddleware{handler}},
		dexeino.WithToolEffects(map[string]dexagents.ToolEffect{"TaskCreate": dexagents.ToolEffectNonIdempotentWrite,
			"TaskList": dexagents.ToolEffectRead, "lookup_order": dexagents.ToolEffectRead}))
	require.NoError(t, err)
	state := newTurnState(t, runner, "plan the release")
	requested := state.runModel(runner).(*dexagents.ToolCallsRequested)
	require.Equal(t, map[string]bool{"create_docs": true, "create_tests": true, "list_tasks": true, "lookup": false},
		orderedCalls(requested))

	for _, id := range []string{"create_docs", "create_tests"} {
		outcome, err := runner.ExecuteDeferredToolCall(context.Background(), dexagents.DeferredToolExecution{
			FlowID: "session", ToolCall: state.records[id], IdempotencyKey: "session/turn-1/" + id, Attempt: 1,
		})
		require.NoError(t, err)
		state.records[id] = dexagents.AgentToolCall{ToolCallID: id, Output: ptr(outcome.(*dexagents.ToolExecutionResult).Output)}
	}
	require.Equal(t, `{"result":"Task #1 created successfully: Write the docs"}`, *state.records["create_docs"].Output)
	require.Equal(t, `{"result":"Task #2 created successfully: Write the tests"}`, *state.records["create_tests"].Output)
}

// failingLookupHandler adds its tool in the Eino run, and its BeforeAgent fails every later time,
// as when a registry it reads becomes unreachable.
type failingLookupHandler struct {
	*adk.BaseChatModelAgentMiddleware
	calls     *atomic.Int64
	addedTool tool.BaseTool
}

// When RunModel cannot read the tools the Handlers add, because a BeforeAgent fails outside the Eino
// run, it cannot tell a plantask call from another, so every call of a Handler-added tool waits
// for an approval and runs in the model's order.
func TestHandlerAddedCallsRunInTheModelsOrderWhenTheirToolsCannotBeRead(t *testing.T) {
	added := funcTool{name: "add_note", run: func(context.Context, string) (string, error) { return "added", nil }}
	lookup := funcTool{name: "lookup_order", run: func(context.Context, string) (string, error) { return "shipped", nil }}
	scripted := roundsModel([]schema.ToolCall{call("note_1", "add_note", `{}`), call("note_2", "add_note", `{}`),
		call("lookup", "lookup_order", `{}`)})
	runner, err := dexeino.NewRunner(&adk.ChatModelAgentConfig{Name: "agent", Description: "Test agent.", Model: scripted,
		ToolsConfig: toolsConfig(lookup),
		Handlers:    []adk.ChatModelAgentMiddleware{failingLookupHandler{&adk.BaseChatModelAgentMiddleware{}, &atomic.Int64{}, added}}},
		dexeino.WithToolEffects(map[string]dexagents.ToolEffect{"add_note": dexagents.ToolEffectIdempotentWrite,
			"lookup_order": dexagents.ToolEffectRead}))
	require.NoError(t, err)
	requested := newTurnState(t, runner, "take notes").runModel(runner).(*dexagents.ToolCallsRequested)
	require.Equal(t, map[string]bool{"note_1": true, "note_2": true, "lookup": false}, orderedCalls(requested))
	for _, toolCall := range requested.ToolCalls[:2] {
		require.Equal(t, dexagents.ToolEffectUnclassified, toolCall.Policy.Effect)
		require.True(t, toolCall.Policy.IsApprovalRequired())
	}
}

// checkoutSubAgent returns an Eino agent whose model charges the card, then sends the receipt, then
// answers. The receipt tool fails as a definite ToolExecutionError.
func checkoutSubAgent(t *testing.T, charges *atomic.Int64) adk.Agent {
	charge := funcTool{name: "charge_card", run: func(context.Context, string) (string, error) {
		charges.Add(1)
		return "charged", nil
	}}
	receipt := funcTool{name: "send_receipt", run: func(context.Context, string) (string, error) {
		return "", &dexagents.ToolExecutionError{Message: "no email on file"}
	}}
	checkout, err := adk.NewChatModelAgent(context.Background(), &adk.ChatModelAgentConfig{Name: "checkout",
		Description: "Checks out an order.", Model: roundsModel(
			[]schema.ToolCall{call("sub_charge", "charge_card", `{}`)},
			[]schema.ToolCall{call("sub_receipt", "send_receipt", `{}`)},
		), ToolsConfig: toolsConfig(charge, receipt)})
	require.NoError(t, err)
	return checkout
}

// checkoutSkills serves one fork skill whose sub-agent is the checkout agent.
type checkoutSkills struct{}

func (checkoutSkills) List(context.Context) ([]skill.FrontMatter, error) {
	return []skill.FrontMatter{{Name: "checkout", Description: "Checks out in a sub-agent.", Context: skill.ContextModeFork}}, nil
}

func (checkoutSkills) Get(context.Context, string) (skill.Skill, error) {
	return skill.Skill{FrontMatter: skill.FrontMatter{Name: "checkout", Description: "Checks out in a sub-agent.",
		Context: skill.ContextModeFork}, Content: "Check out the order.", BaseDirectory: "/skills/checkout"}, nil
}

type checkoutAgentHub struct {
	t       *testing.T
	charges *atomic.Int64
}

func (hub checkoutAgentHub) Get(context.Context, string, *skill.AgentHubOptions) (adk.Agent, error) {
	return checkoutSubAgent(hub.t, hub.charges), nil
}

// An agent tool, or a fork skill, runs a whole Eino agent in its tool Step. When a tool of that
// agent fails with a ToolExecutionError, the agent's earlier tools may have taken effect, so the
// failure is not the agent tool's definite answer: the attempt fails, and the session decides the
// outcome from the agent tool's effect. A read keeps the definite failure, which a read records
// anyway. The agent tool's own argument check, before anything ran, stays definite.
func TestSubAgentToolFailuresAreDecidedByTheirEffect(t *testing.T) {
	for name, testCase := range map[string]struct {
		effect       dexagents.ToolEffect
		isDefinite   bool
		usesSkill    bool
		arguments    string
		chargesAfter int64
	}{
		"agent tool, non_idempotent_write": {effect: dexagents.ToolEffectNonIdempotentWrite, chargesAfter: 1},
		"agent tool, idempotent_write":     {effect: dexagents.ToolEffectIdempotentWrite, chargesAfter: 1},
		"agent tool, unclassified":         {effect: dexagents.ToolEffectUnclassified, chargesAfter: 1},
		"agent tool, read":                 {effect: dexagents.ToolEffectRead, isDefinite: true, chargesAfter: 1},
		"fork skill, non_idempotent_write": {effect: dexagents.ToolEffectNonIdempotentWrite, usesSkill: true, chargesAfter: 1},
		"fork skill, read":                 {effect: dexagents.ToolEffectRead, usesSkill: true, isDefinite: true, chargesAfter: 1},
		"agent tool arguments the schema rejects": {effect: dexagents.ToolEffectNonIdempotentWrite, isDefinite: true,
			arguments: `{"request":7}`},
	} {
		t.Run(name, func(t *testing.T) {
			var charges atomic.Int64
			toolName, arguments := "checkout", `{"request":"order 7"}`
			config := &adk.ChatModelAgentConfig{Name: "agent", Description: "Test agent."}
			if testCase.usesSkill {
				handler, err := skill.NewMiddleware(context.Background(), &skill.Config{Backend: checkoutSkills{},
					AgentHub: checkoutAgentHub{t, &charges}})
				require.NoError(t, err)
				config.Handlers = []adk.ChatModelAgentMiddleware{handler}
				toolName, arguments = "skill", `{"skill":"checkout"}`
			} else {
				config.ToolsConfig = toolsConfig(adk.NewAgentTool(context.Background(), checkoutSubAgent(t, &charges)))
			}
			if testCase.arguments != "" {
				arguments = testCase.arguments
			}
			config.Model = roundsModel([]schema.ToolCall{call("call_checkout", toolName, arguments)})
			runner, err := dexeino.NewRunner(config, dexeino.WithToolEffects(map[string]dexagents.ToolEffect{toolName: testCase.effect}))
			require.NoError(t, err)
			state := newTurnState(t, runner, "check out order 7")
			state.runModel(runner)
			require.Equal(t, testCase.effect, state.records["call_checkout"].Effect)
			_, err = runner.ExecuteDeferredToolCall(context.Background(), dexagents.DeferredToolExecution{
				FlowID: "session", ToolCall: state.records["call_checkout"], IdempotencyKey: "session/turn-1/call_checkout", Attempt: 1,
			})
			require.Equal(t, testCase.chargesAfter, charges.Load())
			var toolError *dexagents.ToolExecutionError
			if testCase.arguments != "" {
				require.ErrorAs(t, err, &toolError)
				require.Equal(t, "invalid arguments for checkout: arguments.request must be string", toolError.Message)
				return
			}
			require.ErrorContains(t, err, "no email on file")
			require.Equal(t, testCase.isDefinite, errors.As(err, &toolError), "definite failure: %v", err)
			if !testCase.isDefinite {
				require.ErrorContains(t, err, "tool "+toolName+" runs an Eino agent, and a tool of that agent failed, "+
					"so the agent's earlier tools may have taken effect: ")
			}
		})
	}
}

// callContexts records what each place a tool Step runs sees from ToolCallContextFrom.
type callContexts struct {
	mutex sync.Mutex
	seen  []string
}

func (contexts *callContexts) record(where string, ctx context.Context) {
	toolCall, isToolStep := dexeino.ToolCallContextFrom(ctx)
	line := where + ": none"
	if isToolStep {
		line = fmt.Sprintf("%s: %s %s %s %s", where, toolCall.ToolCallID, toolCall.ToolName, toolCall.Effect, toolCall.IdempotencyKey)
	}
	contexts.mutex.Lock()
	defer contexts.mutex.Unlock()
	contexts.seen = append(contexts.seen, line)
}

// recordingToolHandler records what its tool wrapper sees.
type recordingToolHandler struct {
	*adk.BaseChatModelAgentMiddleware
	contexts *callContexts
}

func (handler recordingToolHandler) WrapInvokableToolCall(_ context.Context, endpoint adk.InvokableToolCallEndpoint, _ *adk.ToolContext) (adk.InvokableToolCallEndpoint, error) {
	return func(ctx context.Context, arguments string, options ...tool.Option) (string, error) {
		handler.contexts.record("handler wrapper", ctx)
		return endpoint(ctx, arguments, options...)
	}, nil
}

// recordingSubAgent returns an Eino agent whose model refunds two orders, then answers. One of its
// calls has the agent tool's call ID, as a model that numbers its calls from one gives.
func recordingSubAgent(t *testing.T, contexts *callContexts) adk.Agent {
	refund := funcTool{name: "refund_order", run: func(ctx context.Context, arguments string) (string, error) {
		contexts.record("refund_order "+compose.GetToolCallID(ctx), ctx)
		return "refunded", nil
	}}
	refunds, err := adk.NewChatModelAgent(context.Background(), &adk.ChatModelAgentConfig{Name: "billing",
		Description: "Refunds orders.", Model: roundsModel([]schema.ToolCall{
			call("call_billing", "refund_order", `{"id":"A"}`), call("sub_refund_b", "refund_order", `{"id":"B"}`)}),
		ToolsConfig: toolsConfig(refund)})
	require.NoError(t, err)
	return refunds
}

type recordingAgentHub struct {
	t        *testing.T
	contexts *callContexts
}

func (hub recordingAgentHub) Get(context.Context, string, *skill.AgentHubOptions) (adk.Agent, error) {
	return recordingSubAgent(hub.t, hub.contexts), nil
}

// billingSkills serves one fork skill whose sub-agent is the recording billing agent.
type billingSkills struct{}

func (billingSkills) List(context.Context) ([]skill.FrontMatter, error) {
	return []skill.FrontMatter{{Name: "billing", Description: "Refunds in a sub-agent.", Context: skill.ContextModeFork}}, nil
}

func (billingSkills) Get(context.Context, string) (skill.Skill, error) {
	return skill.Skill{FrontMatter: skill.FrontMatter{Name: "billing", Description: "Refunds in a sub-agent.",
		Context: skill.ContextModeFork}, Content: "Refund the orders.", BaseDirectory: "/skills/billing"}, nil
}

// nestedToolsNodeTool runs refund_order through a ToolsNode of its own, as a composite tool may.
type nestedToolsNodeTool struct {
	t        *testing.T
	contexts *callContexts
}

func (nested nestedToolsNodeTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "billing", Desc: "Refunds orders.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{"request": {Type: schema.String}})}, nil
}

func (nested nestedToolsNodeTool) InvokableRun(ctx context.Context, _ string, _ ...tool.Option) (string, error) {
	refund := funcTool{name: "refund_order", run: func(ctx context.Context, arguments string) (string, error) {
		nested.contexts.record("refund_order "+compose.GetToolCallID(ctx), ctx)
		return "refunded", nil
	}}
	node, err := compose.NewToolNode(ctx, &compose.ToolsNodeConfig{Tools: []tool.BaseTool{refund}})
	if err != nil {
		return "", err
	}
	results, err := node.Invoke(ctx, schema.AssistantMessage("", []schema.ToolCall{
		call("call_billing", "refund_order", `{"id":"A"}`), call("sub_refund_b", "refund_order", `{"id":"B"}`)}))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d refunds", len(results)), nil
}

// The tools that a tool runs, such as an agent tool's or a fork skill's sub-agent tools, are not the
// tool Step's call: they run again whenever the Step does, so sharing the Step's IdempotencyKey would
// let a key-deduplicating API drop the second refund. ToolCallContextFrom returns false in them,
// even when a nested call has the Step's call ID. The Step's own ToolArgumentsHandler, tool
// middlewares, and Handler tool wrappers still get the Step's call, also when the Step's context
// already is inside an Eino tool call.
func TestToolsThatAToolRunsDoNotGetItsToolCallContext(t *testing.T) {
	const stepCall = "call_billing billing non_idempotent_write session/turn-1/call_billing"
	for name, shape := range map[string]string{"agent tool": "agent tool", "fork skill": "fork skill",
		"tool with its own ToolsNode": "nested ToolsNode", "agent tool, Step context inside a ToolsNode": "inside"} {
		t.Run(name, func(t *testing.T) {
			contexts := &callContexts{}
			toolName, arguments := "billing", `{"request":"refund A and B"}`
			config := &adk.ChatModelAgentConfig{Name: "agent", Description: "Test agent."}
			switch shape {
			case "agent tool", "inside":
				config.ToolsConfig = toolsConfig(adk.NewAgentTool(context.Background(), recordingSubAgent(t, contexts)))
			case "fork skill":
				handler, err := skill.NewMiddleware(context.Background(), &skill.Config{Backend: billingSkills{},
					AgentHub: recordingAgentHub{t, contexts}})
				require.NoError(t, err)
				config.Handlers = []adk.ChatModelAgentMiddleware{handler}
				toolName, arguments = "skill", `{"skill":"billing"}`
			default:
				config.ToolsConfig = toolsConfig(nestedToolsNodeTool{t, contexts})
			}
			config.ToolsConfig.ToolArgumentsHandler = func(ctx context.Context, _ string, arguments string) (string, error) {
				contexts.record("arguments handler", ctx)
				return arguments, nil
			}
			config.ToolsConfig.ToolCallMiddlewares = []compose.ToolMiddleware{{Invokable: func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
				return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
					contexts.record("middleware", ctx)
					return next(ctx, input)
				}
			}}}
			config.Handlers = append(config.Handlers, recordingToolHandler{&adk.BaseChatModelAgentMiddleware{}, contexts})
			config.Model = roundsModel([]schema.ToolCall{call("call_billing", toolName, arguments)})
			runner, err := dexeino.NewRunner(config, dexeino.WithToolEffects(map[string]dexagents.ToolEffect{
				toolName: dexagents.ToolEffectNonIdempotentWrite}))
			require.NoError(t, err)
			state := newTurnState(t, runner, "refund orders A and B")
			state.runModel(runner)
			runStep := func(ctx context.Context, _ string) (string, error) {
				_, err := runner.ExecuteDeferredToolCall(ctx, dexagents.DeferredToolExecution{
					FlowID: "session", ToolCall: state.records["call_billing"], IdempotencyKey: "session/turn-1/call_billing", Attempt: 1,
				})
				return "ran", err
			}
			if shape == "inside" {
				harness, err := compose.NewToolNode(context.Background(), &compose.ToolsNodeConfig{
					Tools: []tool.BaseTool{funcTool{name: "harness", run: runStep}}})
				require.NoError(t, err)
				_, err = harness.Invoke(context.Background(), toolCallMessage(call("call_harness", "harness", `{}`)))
				require.NoError(t, err)
			} else {
				_, err = runStep(context.Background(), "")
				require.NoError(t, err)
			}
			expectedStepCall := strings.Replace(stepCall, "billing non_idempotent", toolName+" non_idempotent", 1)
			require.ElementsMatch(t, []string{
				"arguments handler: " + expectedStepCall,
				"middleware: " + expectedStepCall,
				"handler wrapper: " + expectedStepCall,
				"refund_order call_billing: none",
				"refund_order sub_refund_b: none",
			}, contexts.seen)
		})
	}
}
