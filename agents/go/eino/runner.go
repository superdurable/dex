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
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	dexagents "github.com/superdurable/dex/agents/go/core"
)

// summarizationPackagePath is the Eino middleware that makes its own model call before each model call.
const summarizationPackagePath = "github.com/cloudwego/eino/adk/middlewares/summarization"

// defaultMaxIterations is Eino's default ChatModelAgentConfig.MaxIterations.
const defaultMaxIterations = 20

// runStateVersion is the version of the FrameworkRunState this adapter writes.
const runStateVersion = 1

// BuildPurpose says why an AgentConfigBuilder is called. Since 0.6.16.
type BuildPurpose string

const (
	// BuildPurposeCheck is the call at construction that checks the configuration. Since 0.6.16.
	BuildPurposeCheck BuildPurpose = "check"
	// BuildPurposeModelCall builds the agent of one RunModel Step. Since 0.6.16.
	BuildPurposeModelCall BuildPurpose = "model_call"
	// BuildPurposeToolCall builds the tools of one tool Step. Since 0.6.16.
	BuildPurposeToolCall BuildPurpose = "tool_call"
)

// AgentBuildContext tells an AgentConfigBuilder which Step it builds the agent for.
//
// Since 0.6.16.
type AgentBuildContext struct {
	// Purpose is why the builder is called.
	Purpose BuildPurpose
	// FlowID is the session ID, or "" for BuildPurposeCheck.
	FlowID string
	// TurnID is "turn-<n>" of the running turn, or "" for BuildPurposeCheck.
	TurnID string
	// Attempt is the one-based Step attempt, or 0 for BuildPurposeCheck.
	Attempt int64
	// SessionSettings are the settings the turn started with, such as {"model": "gpt-5-mini"}.
	// Only BuildPurposeModelCall receives them; the map is empty, never nil, otherwise.
	// JSON numbers decode as float64. Treat the map as read-only.
	SessionSettings map[string]any
	// ToolCallID is the call a BuildPurposeToolCall build runs, or "".
	ToolCallID string
}

// AgentConfigBuilder returns the Eino agent configuration of one Step.
//
// Use it instead of a fixed configuration when the model or the instruction
// follows the session settings. It runs once at construction with
// BuildPurposeCheck, so the configuration is checked before any session runs,
// then in every RunModel attempt and every tool Step attempt. Keep it fast and
// free of I/O: choose from models the Worker built at startup. The settings
// come from the application, so choose only from models the Worker allows.
// The tools must not depend on the session settings, because tool Steps do not
// receive them; a tool the tool Step's build lacks fails as an unknown tool.
// The adapter copies the returned configuration and never changes it. An
// error fails the Step attempt.
//
//	models := map[string]model.ToolCallingChatModel{"fast": fastModel, "smart": smartModel}
//	build := func(_ context.Context, build dexeino.AgentBuildContext) (*adk.ChatModelAgentConfig, error) {
//		chosen, isAllowed := models[fmt.Sprint(build.SessionSettings["model"])]
//		if !isAllowed {
//			chosen = models["fast"]
//		}
//		return &adk.ChatModelAgentConfig{Name: "support", Description: "Support agent.",
//			Instruction: "You help customers.", Model: chosen, ToolsConfig: toolsConfig}, nil
//	}
//
// Since 0.6.16.
type AgentConfigBuilder func(ctx context.Context, build AgentBuildContext) (*adk.ChatModelAgentConfig, error)

// Runner runs one Eino ChatModelAgent configuration in Dex Step mode.
//
// It implements dexagents.AgentFrameworkRunner and
// dexagents.UnfinishedTurnMessageBuilder. It keeps no state between calls:
// every RunModelStep builds a fresh Eino agent and adk.Runner over the
// session's stored messages, and every ExecuteDeferredToolCall builds a
// one-call compose.ToolsNode. Most applications call NewAgentSessionFlow
// instead of constructing it. Embed it to wrap one of its methods, so the
// session still finds BuildUnfinishedTurnMessages. Since 0.6.16.
type Runner struct {
	config  *adk.ChatModelAgentConfig
	build   AgentConfigBuilder
	options runnerOptions
}

// agentConfig is the adapter's own copy of one built configuration.
type agentConfig struct {
	agent adk.ChatModelAgentConfig
}

// runState is the FrameworkRunState: only the count of the turn's model calls.
type runState struct {
	Version    int  `json:"v"`
	ModelCalls *int `json:"model_calls"`
}

// NewRunner creates the runner of one Eino ChatModelAgent configuration.
//
// config is what you would pass to adk.NewChatModelAgent. The runner copies it
// and never changes it, but its tools, model, and Handlers are shared by every
// Step, so they must be safe for concurrent use. It returns an error when
// config is nil, when a tool has no Dex effect (see WithToolEffects), when two
// tools share a name, or when the configuration makes more than one model call
// in a Step or runs a tool inside RunModel: ModelRetryConfig,
// ModelFailoverConfig, Exit, ToolsConfig.ReturnDirectly, and the
// summarization middleware are refused. Since 0.6.16.
func NewRunner(config *adk.ChatModelAgentConfig, options ...Option) (*Runner, error) {
	if config == nil {
		return nil, errors.New("dexeino: NewRunner needs an Eino ChatModelAgentConfig")
	}
	copied := *config
	runner := &Runner{config: &copied, options: newRunnerOptions(options)}
	if err := runner.checkConfiguration(context.Background()); err != nil {
		return nil, err
	}
	return runner, nil
}

// NewRunnerWithBuilder creates the runner of an agent whose configuration build returns for each Step.
//
// It calls build once with BuildPurposeCheck and checks that configuration as
// NewRunner does. It returns an error when build is nil, fails, or returns a
// configuration NewRunner would refuse. See AgentConfigBuilder. Since 0.6.16.
func NewRunnerWithBuilder(build AgentConfigBuilder, options ...Option) (*Runner, error) {
	if build == nil {
		return nil, errors.New("dexeino: NewRunnerWithBuilder needs an AgentConfigBuilder")
	}
	runner := &Runner{build: build, options: newRunnerOptions(options)}
	if err := runner.checkConfiguration(context.Background()); err != nil {
		return nil, err
	}
	return runner, nil
}

// NewAgentSessionFlow builds the durable session Flow of one Eino ChatModelAgent configuration.
//
// Register the Flow in a dex.Registry and serve it with a Worker. Call it
// through dexagents.AgentSessionClient, as for any runner. flowType is the
// protocol Flow type, unique within the Registry; keep it stable, because
// running sessions resume by it. The options configure the adapter and are
// forwarded to dexagents.NewAgentSessionFlow. It returns the errors of
// NewRunner and of dexagents.NewAgentSessionFlow.
//
//	flow, err := dexeino.NewAgentSessionFlow(config, "SupportAgent",
//		dexeino.WithToolEffects(map[string]dexagents.ToolEffect{"lookup_order": dexagents.ToolEffectRead}),
//		dexeino.WithInterruptDeadline(24*time.Hour))
//
// Since 0.6.16.
func NewAgentSessionFlow(config *adk.ChatModelAgentConfig, flowType string, options ...Option) (*dexagents.AgentSessionFlow, error) {
	runner, err := NewRunner(config, options...)
	if err != nil {
		return nil, err
	}
	return dexagents.NewAgentSessionFlow(runner, flowType, runner.SessionOptions()...)
}

// NewAgentSessionFlowWithBuilder builds the session Flow of an agent whose configuration build returns per Step.
//
// It is NewAgentSessionFlow for an AgentConfigBuilder, such as one that picks
// the model from the session settings. It returns the errors of
// NewRunnerWithBuilder and of dexagents.NewAgentSessionFlow. Since 0.6.16.
func NewAgentSessionFlowWithBuilder(build AgentConfigBuilder, flowType string, options ...Option) (*dexagents.AgentSessionFlow, error) {
	runner, err := NewRunnerWithBuilder(build, options...)
	if err != nil {
		return nil, err
	}
	return dexagents.NewAgentSessionFlow(runner, flowType, runner.SessionOptions()...)
}

// SessionOptions returns the dexagents session options the runner's options forward, in order.
//
// Pass them to dexagents.NewAgentSessionFlow when you construct the Flow
// around a Runner yourself, so tool policies and the default effect reach the
// session. Since 0.6.16.
func (runner *Runner) SessionOptions() []dexagents.AgentSessionOption {
	return runner.options.orderedSessionOptions()
}

// BuildUserMessage stores user text, and Dex notes such as the turn-failure note, as an Eino user message.
//
// It returns the EncodeMessage result of schema.UserMessage(text). Since 0.6.16.
func (runner *Runner) BuildUserMessage(text string) (dexagents.FrameworkMessage, error) {
	return EncodeMessage(schema.UserMessage(text))
}

// RunModelStep makes one model call of the turn with a fresh Eino agent over the stored messages.
//
// The input is every stored message, then a tool result for each of the
// turn's calls whose result is not stored yet. It returns
// *dexagents.ToolCallsRequested when the model asked for tools and
// *dexagents.TurnCompleted otherwise; both carry those tool results and the
// model's message. Model errors come back with their HTTP status exposed (see
// ModelAPIError), so the session backs off a transient error and ends the
// turn on another 4xx. Since 0.6.16.
func (runner *Runner) RunModelStep(ctx context.Context, turn dexagents.AgentTurnContext) (dexagents.AgentTurnOutcome, error) {
	modelCalls, err := decodeRunState(turn.FrameworkRunState)
	if err != nil {
		return nil, err
	}
	sessionSettings := turn.SessionSettings
	if sessionSettings == nil {
		sessionSettings = map[string]any{}
	}
	config, err := runner.buildAgentConfig(ctx, AgentBuildContext{
		Purpose: BuildPurposeModelCall, FlowID: turn.FlowID, TurnID: turn.TurnID, Attempt: turn.Attempt,
		SessionSettings: sessionSettings,
	})
	if err != nil {
		return nil, err
	}
	if err := refuseUnsupportedConfig(config); err != nil {
		return runner.endTurnAsFailed(err.Error(), dexagents.AgentUsage{})
	}
	if limit := maxModelCallsPerTurn(config); modelCalls >= limit {
		return runner.endTurnAsFailed(fmt.Sprintf(
			"the turn reached the Eino agent's MaxIterations of %d model calls", limit), dexagents.AgentUsage{})
	}
	tools, err := runner.assembleAgentTools(ctx, config)
	if err != nil {
		return nil, err
	}
	unstoredResults, err := unstoredToolResults(turn)
	if err != nil {
		return nil, err
	}
	input, err := modelInputMessages(turn.Messages, unstoredResults)
	if err != nil {
		return nil, err
	}
	run := newModelCallRun(turn)
	agent, err := adk.NewChatModelAgent(ctx, modelCallAgentConfig(config, run))
	if err != nil {
		return nil, fmt.Errorf("dexeino: build the Eino agent: %w", err)
	}
	einoRunner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent, EnableStreaming: runner.options.isStreaming})
	drained := drainModelRun(einoRunner.Run(ctx, input))
	return runner.modelCallOutcome(turn, tools, run, drained, modelCalls, unstoredResults)
}

func (runner *Runner) modelCallOutcome(
	turn dexagents.AgentTurnContext,
	tools agentTools,
	run *modelCallRun,
	drained drainedModelRun,
	modelCalls int,
	unstoredResults []*schema.Message,
) (dexagents.AgentTurnOutcome, error) {
	usage := run.modelCallUsage()
	run.mutex.Lock()
	isSecondCallTried := run.isSecondCallTried
	offered := maps.Clone(run.offeredTools)
	run.mutex.Unlock()
	if isSecondCallTried || errors.Is(drained.err, errSecondModelCall) {
		return runner.endTurnAsFailed("the Eino agent tried a second model call in one RunModel Step; "+
			"a Handler or model wrapper that calls the model again is not supported", usage)
	}
	if drained.err != nil {
		return nil, fmt.Errorf("the Eino agent run failed: %w", drained.err)
	}
	if len(drained.foreignInterrupts) > 0 {
		return runner.endTurnAsFailed(fmt.Sprintf("an Eino interrupt that the adapter did not raise stopped the run (%s); "+
			"Dex approvals replace Eino human-in-the-loop interrupts", strings.Join(drained.foreignInterrupts, ", ")), usage)
	}
	message := run.capturedMessage()
	if message == nil {
		return nil, errors.New("dexeino: the Eino agent run ended without a model message")
	}
	newMessages, err := encodeMessages(append(append([]*schema.Message{}, unstoredResults...), message))
	if err != nil {
		return nil, err
	}
	requested := toolCallIDSet(message.ToolCalls)
	if len(message.ToolCalls) == 0 {
		if len(drained.gatedCalls) > 0 {
			return runner.endTurnAsFailed("the Eino agent stopped at tool calls its model message does not contain", usage)
		}
		return &dexagents.TurnCompleted{NewMessages: newMessages, Usage: usage}, nil
	}
	if !sameToolCallIDs(requested, drained.gatedCalls) && !(len(drained.gatedCalls) == 0 && len(tools.ordered) == 0) {
		return runner.endTurnAsFailed("the Eino agent's tool calls changed after the model call; "+
			"a Handler that rewrites the model message's tool calls is not supported", usage)
	}
	encodedState, err := json.Marshal(runState{Version: runStateVersion, ModelCalls: ptr(modelCalls + 1)})
	if err != nil {
		return nil, err
	}
	toolCalls := make([]dexagents.DeferredToolCall, 0, len(message.ToolCalls))
	for _, call := range message.ToolCalls {
		arguments := call.Function.Arguments
		if strings.TrimSpace(arguments) == "" {
			arguments = "{}"
		}
		toolCalls = append(toolCalls, dexagents.DeferredToolCall{
			ToolCallID: call.ID,
			ToolName:   call.Function.Name,
			Arguments:  arguments,
			Policy:     runner.modelCallToolPolicy(turn, tools, offered, call.Function.Name),
		})
	}
	return &dexagents.ToolCallsRequested{
		FrameworkRunState: string(encodedState),
		ToolCalls:         toolCalls,
		Usage:             usage,
		NewMessages:       newMessages,
	}, nil
}

// endTurnAsFailed ends the turn without a final answer, for a reason a retry would repeat.
//
// The Go core has no TurnCompleted.TurnError yet, so this returns an error,
// and RunModel ends the turn after its attempts.
func (runner *Runner) endTurnAsFailed(reason string, usage dexagents.AgentUsage) (dexagents.AgentTurnOutcome, error) {
	return nil, &turnEndingError{reason: reason, usage: usage}
}

// turnEndingError is the error endTurnAsFailed returns until the core can end a turn without retries.
type turnEndingError struct {
	reason string
	usage  dexagents.AgentUsage
}

func (failure *turnEndingError) Error() string {
	return "dexeino: " + failure.reason
}

// ExecuteDeferredToolCall runs one Eino tool call through a one-call compose.ToolsNode.
//
// The ToolsNode has the agent's tools, ToolAliases, ToolArgumentsHandler, and
// tool middlewares, plus the Handlers' tool wrappers, in Eino's order, so
// compose.GetToolCallID and ToolCallContextFrom work inside the tool. A tool
// the deploy no longer has, or that the model invented, returns a
// *dexagents.ToolExecutionError "unknown tool <name>", so the model sees why.
// A *dexagents.ToolExecutionError anywhere in the tool's error chain is the
// call's definite failure. Any other error fails the attempt, and the session
// decides the outcome from the call's effect. Since 0.6.16.
func (runner *Runner) ExecuteDeferredToolCall(ctx context.Context, call dexagents.DeferredToolExecution) (dexagents.DeferredToolOutcome, error) {
	record := call.ToolCall
	config, err := runner.buildAgentConfig(ctx, AgentBuildContext{
		Purpose: BuildPurposeToolCall, FlowID: call.FlowID, TurnID: record.TurnID, Attempt: call.Attempt,
		SessionSettings: map[string]any{}, ToolCallID: record.ToolCallID,
	})
	if err != nil {
		return nil, err
	}
	tools, err := runner.assembleAgentTools(ctx, config)
	if err != nil {
		return nil, err
	}
	nodeTools := tools.baseTools()
	entry, isKnown := tools.lookup(record.ToolName)
	if !isKnown {
		if nodeTools, entry, isKnown, err = runner.findHandlerAddedTool(ctx, config, nodeTools, record.ToolName); err != nil {
			return nil, err
		}
	}
	if !isKnown {
		return nil, &dexagents.ToolExecutionError{Message: "unknown tool " + record.ToolName}
	}
	if isStricterThanRecord(runner.toolStepPolicy(record.ToolName, entry), record) {
		return nil, &dexagents.ToolExecutionError{Message: fmt.Sprintf("tool %s now has a stricter effect than when the "+
			"model called it, so it did not run; call it again to run it under its current policy", record.ToolName)}
	}
	node, err := compose.NewToolNode(ctx, toolStepNodeConfig(config, nodeTools))
	if err != nil {
		return nil, fmt.Errorf("dexeino: build the tool Step's ToolsNode: %w", err)
	}
	ctx = context.WithValue(ctx, toolCallContextKey{}, ToolCallContext{
		FlowID: call.FlowID, TurnID: record.TurnID, ToolCallID: record.ToolCallID, ToolName: record.ToolName,
		IdempotencyKey: call.IdempotencyKey, Attempt: call.Attempt, Effect: record.Effect, Events: call.Events,
		Heartbeat: call.Heartbeat, HeartbeatCheckpoint: call.HeartbeatCheckpoint, SaveHeartbeatCheckpoint: call.SaveHeartbeatCheckpoint,
	})
	output, err := node.Invoke(ctx, schema.AssistantMessage("", []schema.ToolCall{
		functionCall(record.ToolCallID, record.ToolName, record.Arguments),
	}))
	if err != nil {
		return nil, classifyToolStepError(record.ToolName, err)
	}
	text, err := toolResultText(output)
	if err != nil {
		return nil, err
	}
	return &dexagents.ToolExecutionResult{Output: text}, nil
}

// findHandlerAddedTool looks for a tool that only a Handler's BeforeAgent adds.
func (runner *Runner) findHandlerAddedTool(
	ctx context.Context,
	config *agentConfig,
	baseTools []tool.BaseTool,
	toolName string,
) ([]tool.BaseTool, agentTool, bool, error) {
	if len(config.agent.Handlers) == 0 {
		return baseTools, agentTool{}, false, nil
	}
	extended, err := handlerAddedTools(ctx, config, baseTools)
	if err != nil {
		return nil, agentTool{}, false, err
	}
	for _, candidate := range extended {
		entry, err := runner.describeAgentTool(ctx, candidate)
		if err == nil && entry.name == toolName {
			return extended, entry, true, nil
		}
	}
	return extended, agentTool{}, false, nil
}

// BuildUnfinishedTurnMessages returns the results of the turn's tool calls that are not stored yet.
//
// The session calls it when a turn ends without a final answer, before the
// turn-failure note, so the stored conversation pairs every tool call with its
// result. It makes no model call. Since 0.6.16.
func (runner *Runner) BuildUnfinishedTurnMessages(_ context.Context, turn dexagents.AgentTurnContext) ([]dexagents.FrameworkMessage, error) {
	results, err := unstoredToolResults(turn)
	if err != nil {
		return nil, err
	}
	return encodeMessages(results)
}

func (runner *Runner) buildAgentConfig(ctx context.Context, build AgentBuildContext) (*agentConfig, error) {
	source := runner.config
	if runner.build != nil {
		built, err := runner.build(ctx, build)
		if err != nil {
			return nil, fmt.Errorf("dexeino: the agent config builder failed: %w", err)
		}
		source = built
	}
	if source == nil {
		return nil, errors.New("dexeino: the agent config builder returned no configuration")
	}
	return &agentConfig{agent: *source}, nil
}

// checkConfiguration builds the configuration once and refuses one the adapter cannot run in Step mode.
func (runner *Runner) checkConfiguration(ctx context.Context) error {
	config, err := runner.buildAgentConfig(ctx, AgentBuildContext{Purpose: BuildPurposeCheck, SessionSettings: map[string]any{}})
	if err != nil {
		return err
	}
	if err := refuseUnsupportedConfig(config); err != nil {
		return fmt.Errorf("dexeino: %w", err)
	}
	tools, err := runner.assembleAgentTools(ctx, config)
	if err != nil {
		return fmt.Errorf("dexeino: %w", err)
	}
	if err := runner.checkToolEffects(tools); err != nil {
		return fmt.Errorf("dexeino: %w", err)
	}
	if _, err := compose.NewToolNode(ctx, toolStepNodeConfig(config, tools.baseTools())); err != nil {
		return fmt.Errorf("dexeino: the agent's tools do not form a ToolsNode: %w", err)
	}
	if _, err := adk.NewChatModelAgent(ctx, modelCallAgentConfig(config, newModelCallRun(dexagents.AgentTurnContext{}))); err != nil {
		return fmt.Errorf("dexeino: Eino refuses the agent configuration: %w", err)
	}
	return nil
}

// refuseUnsupportedConfig returns an error for a configuration that would make several model calls in a Step or run a tool there.
func refuseUnsupportedConfig(config *agentConfig) error {
	agent := config.agent
	switch {
	case agent.Model == nil:
		return errors.New("the Eino agent configuration has no Model")
	case agent.ModelRetryConfig != nil:
		return errors.New("ModelRetryConfig retries the model inside one RunModel Step; remove it, " +
			"because RunModel retries the model call itself (see WithModelRetryPolicy)")
	case agent.ModelFailoverConfig != nil:
		return errors.New("ModelFailoverConfig calls other models inside one RunModel Step; remove it, " +
			"or choose the model per call with an AgentConfigBuilder")
	case agent.Exit != nil:
		return errors.New("an Exit tool ends the agent without a model answer; Dex Step mode does not support it")
	}
	for toolName, isReturnDirectly := range agent.ToolsConfig.ReturnDirectly {
		if isReturnDirectly {
			return fmt.Errorf("ToolsConfig.ReturnDirectly[%q] ends the turn on a tool result; Dex Step mode does not support it", toolName)
		}
	}
	for index, handler := range agent.Handlers {
		if handler == nil {
			return fmt.Errorf("handler %d is nil", index)
		}
		handlerType := reflect.TypeOf(handler)
		for handlerType.Kind() == reflect.Pointer {
			handlerType = handlerType.Elem()
		}
		if handlerType.PkgPath() == summarizationPackagePath {
			return errors.New("the summarization middleware makes its own model call before a model call; " +
				"Dex Step mode makes one model call per RunModel Step")
		}
	}
	return nil
}

func maxModelCallsPerTurn(config *agentConfig) int {
	if config.agent.MaxIterations > 0 {
		return config.agent.MaxIterations
	}
	return defaultMaxIterations
}

func decodeRunState(frameworkRunState *string) (int, error) {
	if frameworkRunState == nil {
		return 0, nil
	}
	var state runState
	if err := json.Unmarshal([]byte(*frameworkRunState), &state); err != nil || state.ModelCalls == nil ||
		(state.Version != 0 && state.Version != runStateVersion) {
		return 0, fmt.Errorf("dexeino: framework run state %q is not one this version reads: %v", *frameworkRunState, err)
	}
	return *state.ModelCalls, nil
}
