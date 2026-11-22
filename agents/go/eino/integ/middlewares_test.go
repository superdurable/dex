// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package integ

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/filesystem"
	"github.com/cloudwego/eino/adk/middlewares/agentsmd"
	"github.com/cloudwego/eino/adk/middlewares/dynamictool/toolsearch"
	fsmiddleware "github.com/cloudwego/eino/adk/middlewares/filesystem"
	"github.com/cloudwego/eino/adk/middlewares/patchtoolcalls"
	"github.com/cloudwego/eino/adk/middlewares/plantask"
	"github.com/cloudwego/eino/adk/middlewares/reduction"
	"github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"

	dexagents "github.com/superdurable/dex/agents/go/core"
	dexeino "github.com/superdurable/dex/agents/go/eino"
	"github.com/superdurable/dex/sdk-go/dex"
)

// middlewareScenario is one Eino middleware or prebuilt shape, configured as its README shows.
type middlewareScenario struct {
	// rounds are the tool calls of each model call; the model answers after the last round.
	rounds [][]schema.ToolCall
	// configure adds the middleware to an agent with one lookup_order tool.
	configure func(config *adk.ChatModelAgentConfig, coordination coordinationFiles) error
	options   []dexeino.Option
	// approvals are the tools that wait for a Dex approval, in order.
	approvals []string
	// steps are the completed tool Step types after the first RunModel, one group per round, in any order within a
	// round. Ordered writes after the first start one per round, from a RunModel that makes no model call.
	steps [][]string
	// expectations check the final answer, which lists every tool result the model saw as "<tool>=<result>".
	expectations []string
	// modelInput checks what the last model call received, as "<role>: <content>" lines.
	modelInput []string
	// storedResults checks the stored tool results by tool call ID.
	storedResults map[string]string
	// toolRuns checks how many times a tool that counts its runs per session ran.
	toolRuns map[string]int
}

// The modes every middleware Flow runs in.
const (
	generateMode = "generate"
	streamMode   = "stream"
)

func middlewareFlowType(name, mode string) string {
	return "EinoMiddleware_" + name + "_" + mode
}

// largeResult is what fetch_page returns: longer than every reduction threshold below.
func largeResult(arguments string) string {
	return strings.Repeat("page text ", 8) + arguments
}

func middlewareScenarios() map[string]middlewareScenario {
	readFile := func(id, path string) schema.ToolCall {
		return toolCall(id, "read_file", fmt.Sprintf(`{"file_path":%q,"offset":1,"limit":20}`, path))
	}
	return map[string]middlewareScenario{
		"agentsmd": {
			rounds: [][]schema.ToolCall{{toolCall("md_lookup", "lookup_order", `{"order_id":"1"}`)}},
			configure: func(config *adk.ChatModelAgentConfig, _ coordinationFiles) error {
				backend := filesystem.NewInMemoryBackend()
				if err := backend.Write(context.Background(), &filesystem.WriteRequest{FilePath: "/AGENTS.md", Content: "Answer in one line."}); err != nil {
					return err
				}
				handler, err := agentsmd.New(context.Background(), &agentsmd.Config{Backend: backend, AgentsMDFiles: []string{"/AGENTS.md"}})
				config.Handlers = append(config.Handlers, handler)
				return err
			},
			steps:        [][]string{{"CallTool"}},
			expectations: []string{"lookup_order=order 1 is shipped"},
			modelInput:   []string{"Answer in one line."},
		},
		"patchtoolcalls": {
			rounds: [][]schema.ToolCall{{toolCall("patch_lookup", "lookup_order", `{"order_id":"2"}`)}},
			configure: func(config *adk.ChatModelAgentConfig, _ coordinationFiles) error {
				handler, err := patchtoolcalls.New(context.Background(), nil)
				config.Handlers = append(config.Handlers, handler)
				return err
			},
			steps:        [][]string{{"CallTool"}},
			expectations: []string{"lookup_order=order 2 is shipped"},
		},
		"toolsearch": {
			rounds: [][]schema.ToolCall{
				{toolCall("search", "tool_search", `{"query":"select:cancel_order"}`)},
				{toolCall("search_cancel", "cancel_order", `{"order_id":"3"}`)},
			},
			configure: func(config *adk.ChatModelAgentConfig, coordination coordinationFiles) error {
				handler, err := toolsearch.New(context.Background(), &toolsearch.Config{DynamicTools: []tool.BaseTool{cancelOrderTool(coordination)}})
				config.Handlers = append(config.Handlers, handler)
				return err
			},
			steps:        [][]string{{"CallTool"}, {"CallTool"}},
			expectations: []string{`tool_search={"matches":["cancel_order"]}`, "cancel_order=cancelled order 3"},
		},
		"toolsearchnative": {
			rounds: [][]schema.ToolCall{{toolCall("native_search", "tool_search", `{"query":"select:cancel_order"}`),
				toolCall("native_cancel", "cancel_order", `{"order_id":"4"}`)}},
			configure: func(config *adk.ChatModelAgentConfig, coordination coordinationFiles) error {
				handler, err := toolsearch.New(context.Background(), &toolsearch.Config{
					DynamicTools: []tool.BaseTool{cancelOrderTool(coordination)}, UseModelToolSearch: true})
				config.Handlers = append(config.Handlers, handler)
				return err
			},
			steps: [][]string{{"CallTool", "CallTool"}},
			expectations: []string{"tool_search=[tool_search_result output omitted: Dex stores text tool results]",
				"cancel_order=cancelled order 4"},
		},
		"filesystem": {
			rounds: [][]schema.ToolCall{
				{toolCall("fs_write", "write_file", `{"file_path":"/notes.txt","content":"draft one"}`)},
				{readFile("fs_read", "/notes.txt"), toolCall("fs_glob", "glob", `{"pattern":"*.txt","path":"/"}`),
					toolCall("fs_execute", "execute", `{"command":"wc notes.txt"}`)},
				{toolCall("fs_edit", "edit_file", `{"file_path":"/notes.txt","old_string":"one","new_string":"two","replace_all":false}`)},
				{readFile("fs_reread", "/notes.txt")},
			},
			configure: func(config *adk.ChatModelAgentConfig, _ coordinationFiles) error {
				handler, err := fsmiddleware.New(context.Background(), &fsmiddleware.MiddlewareConfig{
					Backend: filesystem.NewInMemoryBackend(), StreamingShell: echoShell{}})
				config.Handlers = append(config.Handlers, handler)
				return err
			},
			options: []dexeino.Option{dexeino.WithToolPolicies(map[string]dexagents.ToolPolicy{
				"execute": {Effect: dexagents.ToolEffectNonIdempotentWrite, RequiresApproval: ptr(false)},
			})},
			approvals: []string{"edit_file"},
			steps:     [][]string{{"CallTool"}, {"CallTool", "CallTool", "CallSingleAttemptTool"}, {"AwaitToolApproval", "CallSingleAttemptTool"}, {"CallTool"}},
			expectations: []string{"write_file=Updated file /notes.txt", "read_file=     1\tdraft one", "glob=notes.txt",
				"execute=ran wc notes.txt", "edit_file=Successfully replaced the string in '/notes.txt'", "read_file=     1\tdraft two"},
		},
		"filesystemlegacy": {
			rounds: [][]schema.ToolCall{
				{toolCall("legacy_write", "write_file", `{"file_path":"/report.txt","content":"`+strings.Repeat("line ", 30)+`"}`)},
				{readFile("legacy_read", "/large_tool_result/legacy_write")},
			},
			configure: func(config *adk.ChatModelAgentConfig, _ coordinationFiles) error {
				middleware, err := fsmiddleware.NewMiddleware(context.Background(), &fsmiddleware.Config{
					Backend: filesystem.NewInMemoryBackend(), LargeToolResultOffloadingTokenLimit: 2})
				config.Middlewares = append(config.Middlewares, middleware)
				return err
			},
			steps: [][]string{{"CallTool"}, {"CallTool"}},
			expectations: []string{"write_file=Tool result too large, the result of this tool call legacy_write was saved in the filesystem at this path: /large_tool_result/legacy_write",
				"Updated file /report.txt"},
		},
		"reduction": {
			rounds: [][]schema.ToolCall{
				{toolCall("red_fetch_1", "fetch_page", `{"page":1}`)},
				{toolCall("red_fetch_2", "fetch_page", `{"page":2}`)},
				{readFile("red_read", "/offload/trunc/red_fetch_1")},
			},
			configure: func(config *adk.ChatModelAgentConfig, coordination coordinationFiles) error {
				backend := filesystem.NewInMemoryBackend()
				files, err := fsmiddleware.New(context.Background(), &fsmiddleware.MiddlewareConfig{Backend: backend})
				if err != nil {
					return err
				}
				reducer, err := reduction.New(context.Background(), &reduction.Config{Backend: backend, RootDir: "/offload",
					MaxLengthForTrunc: 40, MaxTokensForClear: 100, TruncExcludeTools: []string{"read_file"}})
				config.Handlers = append(config.Handlers, files, reducer)
				config.ToolsConfig.Tools = append(config.ToolsConfig.Tools, fetchPageTool(coordination))
				return err
			},
			steps: [][]string{{"CallTool"}, {"CallTool"}, {"CallTool"}},
			expectations: []string{"fetch_page=<persisted-output>Tool result saved to: /offload/clear/red_fetch_1",
				"fetch_page=<persisted-output>Tool result saved to: /offload/clear/red_fetch_2",
				"read_file=     1\t" + largeResult(`{"page":1}`)},
			storedResults: map[string]string{"red_fetch_1": "Full output saved to: /offload/trunc/red_fetch_1",
				"red_fetch_2": "Full output saved to: /offload/trunc/red_fetch_2"},
		},
		"reductionlegacy": {
			rounds: [][]schema.ToolCall{{toolCall("old_fetch_1", "fetch_page", `{"page":1}`), toolCall("old_fetch_2", "fetch_page", `{"page":2}`)}},
			configure: func(config *adk.ChatModelAgentConfig, coordination coordinationFiles) error {
				middleware, err := reduction.NewClearToolResult(context.Background(), &reduction.ClearToolResultConfig{
					ToolResultTokenThreshold: 5, KeepRecentTokens: 10})
				config.Middlewares = append(config.Middlewares, middleware)
				config.ToolsConfig.Tools = append(config.ToolsConfig.Tools, fetchPageTool(coordination))
				return err
			},
			steps:         [][]string{{"CallTool", "CallTool"}},
			expectations:  []string{"fetch_page=[Old tool result content cleared]", "fetch_page=" + largeResult(`{"page":2}`)},
			storedResults: map[string]string{"old_fetch_1": largeResult(`{"page":1}`), "old_fetch_2": largeResult(`{"page":2}`)},
		},
		"toolresultlegacy": {
			rounds: [][]schema.ToolCall{{toolCall("tr_fetch_1", "fetch_page", `{"page":1}`), toolCall("tr_fetch_2", "fetch_page", `{"page":2}`)}},
			configure: func(config *adk.ChatModelAgentConfig, coordination coordinationFiles) error {
				middleware, err := reduction.NewToolResultMiddleware(context.Background(), &reduction.ToolResultConfig{
					Backend: filesystem.NewInMemoryBackend(), ClearingTokenThreshold: 5, KeepRecentTokens: 10, OffloadingTokenLimit: 2})
				config.Middlewares = append(config.Middlewares, middleware)
				config.ToolsConfig.Tools = append(config.ToolsConfig.Tools, fetchPageTool(coordination))
				return err
			},
			steps:        [][]string{{"CallTool", "CallTool"}},
			expectations: []string{"fetch_page=[Old tool result content cleared]", "/large_tool_result/tr_fetch_2"},
			storedResults: map[string]string{"tr_fetch_1": "/large_tool_result/tr_fetch_1",
				"tr_fetch_2": "/large_tool_result/tr_fetch_2"},
		},
		"plantask": {
			rounds: [][]schema.ToolCall{
				{toolCall("task_create", "TaskCreate", `{"subject":"Write the docs","description":"Write the docs page.","activeForm":"Writing the docs"}`)},
				{toolCall("task_list", "TaskList", `{}`)},
			},
			configure: func(config *adk.ChatModelAgentConfig, _ coordinationFiles) error {
				handler, err := plantask.New(context.Background(), &plantask.Config{Backend: newTaskFiles(), BaseDir: "/tasks"})
				config.Handlers = append(config.Handlers, handler)
				return err
			},
			approvals:    []string{"TaskCreate"},
			steps:        [][]string{{"AwaitToolApproval", "CallSingleAttemptTool"}, {"CallTool"}},
			expectations: []string{`TaskCreate={"result":"Task #1 created successfully: Write the docs"}`, `TaskList={"result":"#1 [pending] Write the docs"}`},
		},
		"plantaskparallel": {
			rounds: [][]schema.ToolCall{{
				toolCall("create_docs", "TaskCreate", `{"subject":"Write the docs","description":"Docs.","activeForm":"Writing the docs"}`),
				toolCall("create_tests", "TaskCreate", `{"subject":"Write the tests","description":"Tests.","activeForm":"Writing the tests"}`),
			}},
			configure: func(config *adk.ChatModelAgentConfig, _ coordinationFiles) error {
				handler, err := plantask.New(context.Background(), &plantask.Config{Backend: &slowTaskFiles{newTaskFiles()}, BaseDir: "/tasks"})
				config.Handlers = append(config.Handlers, handler)
				return err
			},
			options: []dexeino.Option{dexeino.WithToolPolicies(map[string]dexagents.ToolPolicy{
				"TaskCreate": {Effect: dexagents.ToolEffectNonIdempotentWrite, RequiresApproval: ptr(false)}})},
			// The second TaskCreate starts after the first one finished, from a RunModel that makes no model call.
			steps: [][]string{{"CallSingleAttemptTool"}, {"CallSingleAttemptTool"}},
			expectations: []string{`TaskCreate={"result":"Task #1 created successfully: Write the docs"}`,
				`TaskCreate={"result":"Task #2 created successfully: Write the tests"}`},
		},
		"skill": {
			rounds: [][]schema.ToolCall{{toolCall("skill_notes", "skill", `{"skill":"release_notes"}`), toolCall("skill_triage", "skill", `{"skill":"triage"}`)}},
			configure: func(config *adk.ChatModelAgentConfig, _ coordinationFiles) error {
				handler, err := skill.NewMiddleware(context.Background(), &skill.Config{Backend: memorySkills{}, AgentHub: skillAgents{}})
				config.Handlers = append(config.Handlers, handler)
				return err
			},
			steps: [][]string{{"CallTool", "CallTool"}},
			expectations: []string{"Launching skill: release_notes", "Follow the release_notes steps.",
				`Skill "triage" completed (sub-agent execution).`, "the triage sub-agent got 1 messages"},
		},
		"skilllegacy": {
			rounds: [][]schema.ToolCall{{toolCall("legacy_skill", "skill", `{"skill":"release_notes"}`)}},
			configure: func(config *adk.ChatModelAgentConfig, _ coordinationFiles) error {
				middleware, err := skill.New(context.Background(), &skill.Config{Backend: memorySkills{}})
				config.Middlewares = append(config.Middlewares, middleware)
				return err
			},
			steps:        [][]string{{"CallTool"}},
			expectations: []string{"Launching skill: release_notes"},
		},
		"agenttool": {
			rounds: [][]schema.ToolCall{{toolCall("ask_researcher", "researcher", `{"request":"Why did order 5 ship late?"}`)}},
			configure: func(config *adk.ChatModelAgentConfig, _ coordinationFiles) error {
				researcher, err := adk.NewChatModelAgent(context.Background(), &adk.ChatModelAgentConfig{Name: "researcher",
					Description: "Researches a question.", Instruction: "You research.", Model: &subAgentModel{name: "researcher"}})
				config.ToolsConfig.Tools = append(config.ToolsConfig.Tools, adk.NewAgentTool(context.Background(), researcher))
				return err
			},
			steps:        [][]string{{"CallTool"}},
			expectations: []string{"researcher=the researcher sub-agent got 2 messages"},
		},
		"agenttoolwrite": {
			rounds: [][]schema.ToolCall{{toolCall("checkout_7", "checkout", `{"request":"Check out order 7."}`)}},
			configure: func(config *adk.ChatModelAgentConfig, coordination coordinationFiles) error {
				charge := coordination.tool("charge_card", func(ctx context.Context, _ map[string]any) (string, error) {
					coordination.recordSessionCall(ctx, "charge_card")
					return "charged", nil
				})
				receipt := coordination.tool("send_receipt", func(ctx context.Context, _ map[string]any) (string, error) {
					coordination.recordSessionCall(ctx, "send_receipt")
					return "", &dexagents.ToolExecutionError{Message: "no email on file"}
				})
				checkout, err := adk.NewChatModelAgent(context.Background(), &adk.ChatModelAgentConfig{Name: "checkout",
					Description: "Checks out an order.", Instruction: "You check out.", Model: &checkoutModel{},
					ToolsConfig: adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: []tool.BaseTool{charge, receipt}}}})
				config.ToolsConfig.Tools = append(config.ToolsConfig.Tools, adk.NewAgentTool(context.Background(), checkout))
				return err
			},
			options: []dexeino.Option{dexeino.WithToolPolicies(map[string]dexagents.ToolPolicy{
				"checkout": {Effect: dexagents.ToolEffectNonIdempotentWrite, RequiresApproval: ptr(false)}})},
			// The sub-agent charged the card before its receipt tool failed, so the CallSingleAttemptTool
			// attempt fails, and its failure route records the call's outcome unknown.
			steps:        [][]string{{"RecordToolOutcomeUnknown"}},
			expectations: []string{"checkout=" + dexagents.OutcomeUnknownToolResult},
			toolRuns:     map[string]int{"charge_card": 1, "send_receipt": 1},
		},
		"eventsenders": {
			rounds: [][]schema.ToolCall{{toolCall("events_lookup", "lookup_order", `{"order_id":"6"}`)}},
			configure: func(config *adk.ChatModelAgentConfig, _ coordinationFiles) error {
				config.ToolsConfig.EmitInternalEvents = true
				config.Handlers = append(config.Handlers, adk.NewEventSenderModelWrapper(), adk.NewEventSenderToolWrapper())
				return nil
			},
			steps:        [][]string{{"CallTool"}},
			expectations: []string{"lookup_order=order 6 is shipped"},
		},
	}
}

// newMiddlewareFlows builds a Generate and a Stream Flow for every middleware scenario.
func newMiddlewareFlows(coordination coordinationFiles) ([]dex.Flow, error) {
	scenarios := middlewareScenarios()
	chatModel := &middlewareChatModel{coordination: coordination, scenarios: scenarios}
	var flows []dex.Flow
	for name, scenario := range scenarios {
		for _, mode := range []string{generateMode, streamMode} {
			config := &adk.ChatModelAgentConfig{Name: "middleware_agent", Description: "Runs one Eino middleware.",
				Instruction: "You are the middleware test agent.", Model: chatModel,
				ToolsConfig: adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: []tool.BaseTool{
					coordination.tool("lookup_order", func(_ context.Context, arguments map[string]any) (string, error) {
						return fmt.Sprintf("order %v is shipped", arguments["order_id"]), nil
					}),
				}}}}
			if err := scenario.configure(config, coordination); err != nil {
				return nil, fmt.Errorf("middleware %s: %w", name, err)
			}
			options := append([]dexeino.Option{middlewareToolEffects(), dexeino.WithStreaming(mode == streamMode)}, scenario.options...)
			flow, err := dexeino.NewAgentSessionFlow(config, middlewareFlowType(name, mode), options...)
			if err != nil {
				return nil, fmt.Errorf("middleware %s: %w", name, err)
			}
			flows = append(flows, flow)
		}
	}
	return flows, nil
}

// middlewareToolEffects gives every tool the middlewares add the effect its README describes.
func middlewareToolEffects() dexeino.Option {
	return dexeino.WithToolEffects(map[string]dexagents.ToolEffect{
		"lookup_order": dexagents.ToolEffectRead, "fetch_page": dexagents.ToolEffectRead,
		"ls": dexagents.ToolEffectRead, "read_file": dexagents.ToolEffectRead, "glob": dexagents.ToolEffectRead,
		"grep": dexagents.ToolEffectRead, "write_file": dexagents.ToolEffectIdempotentWrite,
		"edit_file": dexagents.ToolEffectNonIdempotentWrite, "execute": dexagents.ToolEffectNonIdempotentWrite,
		"tool_search": dexagents.ToolEffectRead, "cancel_order": dexagents.ToolEffectIdempotentWrite,
		"TaskCreate": dexagents.ToolEffectNonIdempotentWrite, "TaskGet": dexagents.ToolEffectRead,
		"TaskList": dexagents.ToolEffectRead, "TaskUpdate": dexagents.ToolEffectIdempotentWrite,
		"skill": dexagents.ToolEffectRead, "researcher": dexagents.ToolEffectRead,
	})
}

func cancelOrderTool(coordination coordinationFiles) tool.BaseTool {
	return coordination.tool("cancel_order", func(_ context.Context, arguments map[string]any) (string, error) {
		return fmt.Sprintf("cancelled order %v", arguments["order_id"]), nil
	})
}

func fetchPageTool(coordination coordinationFiles) tool.BaseTool {
	fetch := coordination.tool("fetch_page", func(_ context.Context, arguments map[string]any) (string, error) {
		encoded, err := json.Marshal(arguments)
		return largeResult(string(encoded)), err
	})
	return &pageTool{recordedTool: fetch}
}

// pageTool is fetch_page, whose page argument is a number.
type pageTool struct {
	*recordedTool
}

func (page *pageTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "fetch_page", Desc: "Fetches a page.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{"page": {Type: schema.Integer}})}, nil
}

// echoShell is a filesystem StreamingShell that streams "ran <command>".
type echoShell struct{}

func (echoShell) ExecuteStreaming(_ context.Context, input *filesystem.ExecuteRequest) (*schema.StreamReader[*filesystem.ExecuteResponse], error) {
	return schema.StreamReaderFromArray([]*filesystem.ExecuteResponse{{Output: "ran "}, {Output: input.Command}}), nil
}

// taskFiles is a plantask Backend in memory, keyed by full path.
type taskFiles struct {
	mutex sync.Mutex
	files map[string]string
}

func newTaskFiles() *taskFiles {
	return &taskFiles{files: map[string]string{}}
}

func (backend *taskFiles) LsInfo(_ context.Context, request *plantask.LsInfoRequest) ([]plantask.FileInfo, error) {
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

func (backend *taskFiles) Read(_ context.Context, request *plantask.ReadRequest) (*filesystem.FileContent, error) {
	backend.mutex.Lock()
	defer backend.mutex.Unlock()
	content, isFound := backend.files[request.FilePath]
	if !isFound {
		return nil, fmt.Errorf("no task file %s", request.FilePath)
	}
	return &filesystem.FileContent{Content: content}, nil
}

func (backend *taskFiles) Write(_ context.Context, request *plantask.WriteRequest) error {
	backend.mutex.Lock()
	defer backend.mutex.Unlock()
	backend.files[request.FilePath] = request.Content
	return nil
}

func (backend *taskFiles) Delete(_ context.Context, request *plantask.DeleteRequest) error {
	backend.mutex.Lock()
	defer backend.mutex.Unlock()
	delete(backend.files, request.FilePath)
	return nil
}

// slowTaskFiles is taskFiles with the latency of a shared store, so parallel calls would overlap.
type slowTaskFiles struct{ *taskFiles }

func (backend *slowTaskFiles) LsInfo(ctx context.Context, request *plantask.LsInfoRequest) ([]plantask.FileInfo, error) {
	time.Sleep(100 * time.Millisecond)
	return backend.taskFiles.LsInfo(ctx, request)
}

// memorySkills is a skill Backend with an inline skill and a fork skill.
type memorySkills struct{}

func (memorySkills) List(context.Context) ([]skill.FrontMatter, error) {
	return []skill.FrontMatter{{Name: "release_notes", Description: "Writes release notes."},
		{Name: "triage", Description: "Triages an issue in a fresh sub-agent.", Context: skill.ContextModeFork}}, nil
}

func (skills memorySkills) Get(ctx context.Context, name string) (skill.Skill, error) {
	matters, err := skills.List(ctx)
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

// skillAgents is the AgentHub of fork skills.
type skillAgents struct{}

func (skillAgents) Get(ctx context.Context, _ string, _ *skill.AgentHubOptions) (adk.Agent, error) {
	return adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{Name: "triage", Description: "Triages.", Model: &subAgentModel{name: "triage"}})
}

// checkoutModel charges the card, then sends the receipt, then answers.
type checkoutModel struct{}

func (checkout *checkoutModel) Generate(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	rounds := 0
	for _, message := range input {
		if message.Role == schema.Assistant && len(message.ToolCalls) > 0 {
			rounds++
		}
	}
	switch rounds {
	case 0:
		return schema.AssistantMessage("", []schema.ToolCall{toolCall("sub_charge", "charge_card", `{}`)}), nil
	case 1:
		return schema.AssistantMessage("", []schema.ToolCall{toolCall("sub_receipt", "send_receipt", `{}`)}), nil
	}
	return schema.AssistantMessage("checked out", nil), nil
}

func (checkout *checkoutModel) Stream(ctx context.Context, input []*schema.Message, options ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	message, err := checkout.Generate(ctx, input, options...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{message}), nil
}

func (checkout *checkoutModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return checkout, nil
}

// subAgentModel answers a sub-agent's one model call with the number of messages it got.
type subAgentModel struct {
	name string
}

func (sub *subAgentModel) Generate(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	return schema.AssistantMessage(fmt.Sprintf("the %s sub-agent got %d messages", sub.name, len(input)), nil), nil
}

func (sub *subAgentModel) Stream(ctx context.Context, input []*schema.Message, options ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	message, err := sub.Generate(ctx, input, options...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{message}), nil
}

func (sub *subAgentModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return sub, nil
}

// middlewareChatModel plays each scenario's rounds by the session's text "<scenario> <mode>".
//
// Its last model input per session goes to "<scenario>-<mode>.input" for the test to read.
type middlewareChatModel struct {
	coordination coordinationFiles
	scenarios    map[string]middlewareScenario
}

func (scripted *middlewareChatModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return scripted, nil
}

func (scripted *middlewareChatModel) Generate(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	return scripted.respond(input)
}

// Stream sends the answer in two chunks, so the adapter concatenates a real stream.
func (scripted *middlewareChatModel) Stream(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	message, err := scripted.respond(input)
	if err != nil {
		return nil, err
	}
	half := len(message.Content) / 2
	first := &schema.Message{Role: schema.Assistant, Content: message.Content[:half]}
	rest := &schema.Message{Role: schema.Assistant, Content: message.Content[half:], ToolCalls: message.ToolCalls,
		ResponseMeta: message.ResponseMeta}
	return schema.StreamReaderFromArray([]*schema.Message{first, rest}), nil
}

func (scripted *middlewareChatModel) respond(input []*schema.Message) (*schema.Message, error) {
	lastUser := -1
	var scenario middlewareScenario
	for index, message := range input {
		name, mode, _ := strings.Cut(message.Content, " ")
		if found, isScenario := scripted.scenarios[name]; isScenario && message.Role == schema.User &&
			(mode == generateMode || mode == streamMode) {
			lastUser, scenario = index, found
		}
	}
	if lastUser < 0 {
		return nil, fmt.Errorf("the middleware model got no scenario message")
	}
	session := input[lastUser].Content
	scripted.coordination.recordCall("model-" + strings.ReplaceAll(session, " ", "-"))
	rounds := 0
	var results []string
	for _, message := range input[lastUser:] {
		if message.Role == schema.Assistant && len(message.ToolCalls) > 0 {
			rounds++
		}
		if message.Role == schema.Tool {
			results = append(results, message.ToolName+"="+message.Content)
		}
	}
	if rounds < len(scenario.rounds) {
		return withUsage(schema.AssistantMessage("", scenario.rounds[rounds])), nil
	}
	lines := make([]string, 0, len(input))
	for _, message := range input {
		lines = append(lines, fmt.Sprintf("%s: %s", message.Role, message.Content))
	}
	path := filepath.Join(scripted.coordination.dir, strings.ReplaceAll(session, " ", "-")+".input")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		return nil, err
	}
	return withUsage(schema.AssistantMessage(strings.Join(results, " | "), nil)), nil
}

// Every middleware and prebuilt agent shape that Eino ships, configured as its README shows, runs
// through the adapter in Generate and Stream mode: one tool Step per call with the effect its
// tools are given, approvals where the effect asks for one, no TurnError, the tool results the
// middleware produced stored and replayed, and one model call per RunModel.
func TestEveryShippedMiddlewareRunsInStepMode(t *testing.T) {
	stack := newAgentTestStack(t)
	stack.startWorker()
	for name, scenario := range middlewareScenarios() {
		for _, mode := range []string{generateMode, streamMode} {
			t.Run(name+" "+mode, func(t *testing.T) {
				t.Parallel()
				stack.runMiddlewareScenario(t, name, mode, scenario)
			})
		}
	}
}

func (stack *agentTestStack) runMiddlewareScenario(t *testing.T, name, mode string, scenario middlewareScenario) {
	flowType := middlewareFlowType(name, mode)
	sessionID := "middleware-" + name + "-" + mode
	sessions := stack.sessions(flowType)
	ctx := context.Background()
	_, err := sessions.StartSession(ctx, sessionID, ptr(name+" "+mode), dex.StartFlowOptions{})
	require.NoError(t, err)
	for _, toolName := range scenario.approvals {
		snapshot := stack.waitForPendingInterrupts(flowType, sessionID, 1)
		interrupt := snapshot.PendingInterrupts[0]
		require.Equal(t, toolName, interrupt.ToolName)
		isResolved, err := sessions.ResolveInterrupt(ctx, sessionID, interrupt.InterruptID, true, nil)
		require.NoError(t, err)
		require.True(t, isResolved)
	}
	snapshot := stack.waitForTurnCount(flowType, sessionID, 1)
	require.Nil(t, snapshot.State.LastTurnError)

	steps := stack.executedStepTypes(sessionID)
	require.Equal(t, []string{"StartAgentSession", "RunModel"}, steps[:2])
	position := 2
	for round, expected := range scenario.steps {
		require.GreaterOrEqual(t, len(steps), position+len(expected)+2, "steps %v", steps)
		require.ElementsMatch(t, expected, steps[position:position+len(expected)], "round %d of %v", round+1, steps)
		require.Equal(t, []string{"ResumeTurn", "RunModel"}, steps[position+len(expected):position+len(expected)+2], "steps %v", steps)
		position += len(expected) + 2
	}
	require.Len(t, steps, position, "steps %v", steps)
	modelCalls := int64(len(scenario.rounds) + 1)
	require.Equal(t, modelCalls, snapshot.Usage.ModelCalls)
	require.Equal(t, int(modelCalls), stack.callCount("model-"+name+"-"+mode), "one model call per RunModel")
	for _, attempt := range stack.runModelAttempts(sessionID) {
		require.Equal(t, 1, attempt, "no RunModel attempt is retried")
	}

	answer := *snapshot.Messages[len(snapshot.Messages)-1].Text
	for _, expected := range scenario.expectations {
		require.Contains(t, answer, expected)
	}
	input, err := os.ReadFile(filepath.Join(stack.coordination.dir, name+"-"+mode+".input"))
	require.NoError(t, err)
	for _, expected := range scenario.modelInput {
		require.Contains(t, string(input), expected)
	}

	roles := []dexagents.MessageRole{dexagents.MessageRoleUser}
	for _, round := range scenario.rounds {
		roles = append(roles, dexagents.MessageRoleAssistant)
		for range round {
			roles = append(roles, dexagents.MessageRoleTool)
		}
	}
	roles = append(roles, dexagents.MessageRoleAssistant)
	require.Equal(t, roles, messageRoles(snapshot.Messages), "only the user's, model's, and tools' messages are stored")
	stored := map[string]string{}
	for _, message := range snapshot.Messages {
		decoded, err := dexeino.DecodeMessage(message)
		require.NoError(t, err)
		if message.Role == dexagents.MessageRoleTool {
			require.Equal(t, *message.ToolOutput, decoded.Content)
			stored[*message.ToolCallID] = *message.ToolOutput
		}
	}
	for toolCallID, expected := range scenario.storedResults {
		require.Contains(t, stored[toolCallID], expected, "stored result of %s", toolCallID)
	}
	for toolName, expected := range scenario.toolRuns {
		require.Equal(t, expected, stack.callCount(toolName+"-"+sessionID), "runs of %s", toolName)
	}
}
