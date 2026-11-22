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

// modelCallingHandlerPackages are Eino middlewares that make their own model calls inside a Step, with the reason.
var modelCallingHandlerPackages = map[string]string{
	"github.com/cloudwego/eino/adk/middlewares/summarization": "the summarization middleware makes its own model call " +
		"before a model call",
	"github.com/cloudwego/eino/adk/middlewares/automemory": "the automemory middleware makes its own model calls " +
		"to select memory topics and extract memories",
	"github.com/cloudwego/eino/adk/middlewares/automemory/dream": "the dream middleware makes its own model calls " +
		"to consolidate memories",
}

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
// It implements dexagents.AgentFrameworkRunner,
// dexagents.UnfinishedTurnMessageBuilder, and
// dexagents.ToolCallStepOptionsProvider. It keeps no session state between
// calls: every RunModelStep builds a fresh Eino agent and adk.Runner over the
// session's stored messages, and every ExecuteDeferredToolCall builds a
// one-call compose.ToolsNode, or runs a tool source's call through its source.
// Most applications call NewAgentSessionFlow instead of constructing it. Embed
// it to wrap one of its methods, so the session still finds
// BuildUnfinishedTurnMessages and ToolCallStepOptions.
//
// Since 0.6.16.
type Runner struct {
	config  *adk.ChatModelAgentConfig
	build   AgentConfigBuilder
	options runnerOptions
	router  *dexagents.ToolSourceRouter
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
// tools share a name, when the configuration makes more than one model call
// in a Step or runs a tool inside RunModel (ModelRetryConfig,
// ModelFailoverConfig, Exit, ToolsConfig.ReturnDirectly, and the
// summarization, automemory, and dream middlewares are refused), when it sets
// ToolsConfig.ExecuteSequentially, which Dex cannot honor because it runs the
// tool calls of one model response as parallel Steps, when a tool is an Eino
// agent tool made with adk.WithFullChatHistoryAsInput, which reads the agent's
// history in its tool Step, where Dex has none, or when the WithToolSources
// sources are refused (see WithToolSources). Since 0.6.16.
func NewRunner(config *adk.ChatModelAgentConfig, options ...Option) (*Runner, error) {
	if config == nil {
		return nil, errors.New("dexeino: NewRunner needs an Eino ChatModelAgentConfig")
	}
	copied := *config
	runner := &Runner{config: &copied, options: newRunnerOptions(options)}
	router, err := runner.checkConfiguration(context.Background())
	if err != nil {
		return nil, err
	}
	runner.router = router
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
	router, err := runner.checkConfiguration(context.Background())
	if err != nil {
		return nil, err
	}
	runner.router = router
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
// turn's calls whose result is not stored yet. The model is offered the
// agent's tools and the tools the WithToolSources sources list now. It
// returns *dexagents.ToolCallsRequested when the model asked for tools and
// *dexagents.TurnCompleted otherwise; both carry those tool results and the
// model's message. Model errors come back with their HTTP status where the
// session reads it (see ModelAPIError), so the session backs off a transient
// error and ends the turn on another 4xx.
//
// A stop that a retry would repeat ends the turn as failed at once, with a
// *dexagents.TurnCompleted whose TurnError says why: a configuration from the
// AgentConfigBuilder that NewRunner would refuse (a refused setting or
// middleware, two tools of one name, an alias that names another tool, a
// tool without a Dex effect, or an agent tool that reads the agent's
// history), a turn at the agent's MaxIterations, a second
// model call in the Step, a Handler's WrapModel that answers without calling
// the model it wraps or after that call failed, an Eino interrupt the adapter
// did not raise, or a model message that changed after the model call, such
// as tool call arguments or text that a Handler's AfterModelRewriteState or an
// AgentMiddleware's AfterChatModel rewrote. Such a hook that changes the
// messages before the model's, or the state's tools, ends the turn the same
// way, because Dex replays the stored messages and offers the agent's tools at
// every model call. Its NewMessages hold only the unstored tool results,
// so every stored call keeps its result; the model message of a refused call
// is not stored. The usage of a model call that ran is recorded, and no retry
// pays for it again. When the one model call failed and a wrapper then tried
// a second, the failed call's error is returned instead, so the session
// handles it as any model error. A tool whose Info fails returns an error,
// so RunModel retries the attempt.
//
// Since 0.6.16.
func (runner *Runner) RunModelStep(ctx context.Context, turn dexagents.AgentTurnContext) (dexagents.AgentTurnOutcome, error) {
	modelCalls, err := decodeRunState(turn.FrameworkRunState)
	if err != nil {
		return nil, err
	}
	unstoredResults, err := unstoredToolResults(turn)
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
	tools, err := runner.checkModelCallConfiguration(ctx, config)
	var infoFailure *toolInfoError
	if errors.As(err, &infoFailure) {
		return nil, fmt.Errorf("dexeino: %w", err)
	}
	if err != nil {
		return endTurnAsFailed("the agent configuration cannot run in Dex Step mode: "+err.Error(),
			dexagents.AgentUsage{}, unstoredResults)
	}
	if limit := maxModelCallsPerTurn(config); modelCalls >= limit {
		return endTurnAsFailed(fmt.Sprintf(
			"the turn reached the Eino agent's MaxIterations of %d model calls", limit), dexagents.AgentUsage{}, unstoredResults)
	}
	sourceTools, err := runner.listSourceTools(ctx, turn, tools)
	if err != nil {
		return nil, err
	}
	input, err := modelInputMessages(turn.Messages, unstoredResults)
	if err != nil {
		return nil, err
	}
	run := newModelCallRun(turn)
	agent, err := adk.NewChatModelAgent(ctx, modelCallAgentConfig(config, run, sourceTools.stubs))
	if err != nil {
		return nil, fmt.Errorf("dexeino: build the Eino agent: %w", err)
	}
	einoRunner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent, EnableStreaming: runner.options.isStreaming})
	drained := drainModelRun(einoRunner.Run(ctx, input))
	return runner.modelCallOutcome(ctx, modelCallResult{
		turn: turn, config: config, tools: tools, sourceTools: sourceTools, offeredToolCount: len(tools.ordered) + len(sourceTools.stubs),
		run: run, drained: drained, modelCalls: modelCalls, unstoredResults: unstoredResults,
	})
}

// modelCallResult is what modelCallOutcome turns into the Step's outcome.
type modelCallResult struct {
	turn             dexagents.AgentTurnContext
	config           *agentConfig
	tools            agentTools
	sourceTools      offeredSourceTools
	offeredToolCount int
	run              *modelCallRun
	drained          drainedModelRun
	modelCalls       int
	unstoredResults  []*schema.Message
}

func (runner *Runner) modelCallOutcome(ctx context.Context, result modelCallResult) (dexagents.AgentTurnOutcome, error) {
	run, drained := result.run, result.drained
	usage := run.modelCallUsage()
	run.mutex.Lock()
	isSecondCallTried := run.isSecondCallTried
	isModelNotCalled := run.isModelNotCalled
	offered := maps.Clone(run.offeredTools)
	run.mutex.Unlock()
	if isModelNotCalled {
		if failure := run.failedModelCall(); failure != nil {
			runner.options.logger.Warn("dexeino: a Handler's WrapModel answered from another model after the model call failed",
				"turn_id", result.turn.TurnID, "error", failure)
			return endTurnAsFailed("a Handler's WrapModel answered from another model after the call of the model it wraps "+
				"failed, so two model calls ran in one RunModel Step; return the model's error, which the session "+
				"retries or fails by its HTTP status, and choose the model with an AgentConfigBuilder instead",
				usage, result.unstoredResults)
		}
		return endTurnAsFailed("a Handler's WrapModel answered without calling the model it wraps, so Dex cannot count "+
			"the Step's model calls or see the tools that model was offered; choose the model with an AgentConfigBuilder instead",
			usage, result.unstoredResults)
	}
	if isSecondCallTried || errors.Is(drained.err, errSecondModelCall) {
		if failure := run.failedModelCall(); failure != nil {
			// The call that ran failed, so its error, not the refused retry, decides what the session does.
			return nil, fmt.Errorf("the Eino agent's model call failed: %w", exposeModelHTTPStatus(failure))
		}
		return endTurnAsFailed("the Eino agent tried a second model call in one RunModel Step; "+
			"a Handler or model wrapper that calls the model again is not supported", usage, result.unstoredResults)
	}
	if drained.err != nil {
		return nil, fmt.Errorf("the Eino agent run failed: %w", drained.err)
	}
	if len(drained.foreignInterrupts) > 0 {
		return endTurnAsFailed(fmt.Sprintf("an Eino interrupt that the adapter did not raise stopped the run (%s); "+
			"Dex approvals replace Eino human-in-the-loop interrupts", strings.Join(drained.foreignInterrupts, ", ")),
			usage, result.unstoredResults)
	}
	message := run.capturedMessage()
	if message == nil {
		return nil, errors.New("dexeino: the Eino agent run ended without a model message")
	}
	newMessages, err := encodeMessages(append(append([]*schema.Message{}, result.unstoredResults...), message))
	if err != nil {
		return nil, err
	}
	requested := toolCallIDSet(message.ToolCalls)
	if len(message.ToolCalls) == 0 {
		if len(drained.gatedCalls) > 0 {
			return endTurnAsFailed("the Eino agent stopped at tool calls its model message does not contain",
				usage, result.unstoredResults)
		}
		if reason := run.changeAfterModelCall(); reason != "" {
			return endTurnAsFailed(reason, usage, result.unstoredResults)
		}
		return &dexagents.TurnCompleted{NewMessages: newMessages, Usage: usage}, nil
	}
	if !sameToolCallIDs(requested, drained.gatedCalls) && !(len(drained.gatedCalls) == 0 && result.offeredToolCount == 0) {
		return endTurnAsFailed(toolCallsChangedReason, usage, result.unstoredResults)
	}
	if reason := run.changeAfterModelCall(); reason != "" {
		return endTurnAsFailed(reason, usage, result.unstoredResults)
	}
	encodedState, err := json.Marshal(runState{Version: runStateVersion, ModelCalls: ptr(result.modelCalls + 1)})
	if err != nil {
		return nil, err
	}
	handlerAdded := runner.handlerAddedToolEffectsOfCalls(ctx, result, offered, message.ToolCalls)
	toolCalls := make([]dexagents.DeferredToolCall, 0, len(message.ToolCalls))
	for _, call := range message.ToolCalls {
		deferred, err := runner.deferredToolCall(result, offered, handlerAdded, call)
		if err != nil {
			return nil, err
		}
		toolCalls = append(toolCalls, deferred)
	}
	return &dexagents.ToolCallsRequested{
		FrameworkRunState: string(encodedState),
		ToolCalls:         toolCalls,
		Usage:             usage,
		NewMessages:       newMessages,
	}, nil
}

// deferredToolCall builds a call through the source that listed its tool, or as a call of an agent tool.
func (runner *Runner) deferredToolCall(
	result modelCallResult,
	offered map[string]bool,
	handlerAdded handlerAddedToolEffects,
	call schema.ToolCall,
) (dexagents.DeferredToolCall, error) {
	arguments := call.Function.Arguments
	if strings.TrimSpace(arguments) == "" {
		arguments = "{}"
	}
	if result.sourceTools.isOffered(call.Function.Name) {
		built, err := result.sourceTools.listing.DeferredToolCall(result.turn, call.ID, call.Function.Name, arguments)
		if err != nil {
			return dexagents.DeferredToolCall{}, err
		}
		if built != nil {
			return *built, nil
		}
	}
	return dexagents.DeferredToolCall{
		ToolCallID: call.ID,
		ToolName:   call.Function.Name,
		Arguments:  arguments,
		Policy:     runner.modelCallToolPolicy(result.turn, result.tools, offered, handlerAdded, call.Function.Name),
	}, nil
}

// handlerAddedToolEffectsOfCalls reads the effects of Handler-added tools, only when a call names one.
func (runner *Runner) handlerAddedToolEffectsOfCalls(
	ctx context.Context,
	result modelCallResult,
	offered map[string]bool,
	calls []schema.ToolCall,
) handlerAddedToolEffects {
	for _, call := range calls {
		name := call.Function.Name
		_, isAgentTool := result.tools.lookup(name)
		isSourceTool := result.sourceTools.isOffered(name)
		if offered[name] && !isAgentTool && !isSourceTool {
			return runner.findHandlerAddedToolEffects(ctx, result.config, result.tools)
		}
	}
	return handlerAddedToolEffects{}
}

// endTurnAsFailed ends the turn without a final answer, for a reason a retry would repeat.
//
// It returns a TurnCompleted with TurnError, so RunModel ends the turn without
// retries; its NewMessages hold only the turn's unstored tool results.
func endTurnAsFailed(reason string, usage dexagents.AgentUsage, unstoredResults []*schema.Message) (dexagents.AgentTurnOutcome, error) {
	newMessages, err := encodeMessages(unstoredResults)
	if err != nil {
		return nil, err
	}
	return &dexagents.TurnCompleted{NewMessages: newMessages, Usage: usage, TurnError: &reason}, nil
}

// ExecuteDeferredToolCall runs one tool call: an Eino tool's through a one-call compose.ToolsNode,
// a tool source's through its source.
//
// A call that a WithToolSources source built runs first and alone through
// dexagents.ToolSourceRouter.ExecuteDeferredToolCall, with ctx and call
// unchanged, and returns what the source returns: the agent's tool
// middlewares and Handler tool wrappers never wrap it, and the
// AgentConfigBuilder is not called. When this Worker has no source of that
// name, the error fails the attempt, so the call's effect decides its
// outcome.
//
// For an Eino tool, the ToolsNode has the agent's tools, ToolAliases,
// ToolArgumentsHandler, and tool middlewares, plus the Handlers' tool wrappers, in Eino's order, so
// compose.GetToolCallID and ToolCallContextFrom work inside the tool. A tool
// the deploy no longer has, or that the model invented, returns a
// *dexagents.ToolExecutionError "unknown tool <name>", so the model sees why.
// A call whose record has a weaker effect than this deploy gives its tool, or
// no approval where this deploy's policy needs one, returns a
// *dexagents.ToolExecutionError without running, so the model may call it
// again under the current policy. A *dexagents.ToolExecutionError anywhere in the tool's error chain is the
// call's definite failure. Any other error fails the attempt, and the session
// decides the outcome from the call's effect. After Eino's argument conversion
// (ArgumentsAliases, then ToolArgumentsHandler) and before the tool
// middlewares, the arguments are checked against the schema the tool's Info
// reports now, with the Go built-in loop's JSON Schema subset and texts: a
// call they break, such as a stored call after a deploy renamed a required
// parameter, returns the *dexagents.ToolExecutionError "invalid arguments for
// <name>: <problem>" without running. A tool Step has no Eino agent state, so
// a call of an agent tool made with adk.WithFullChatHistoryAsInput, which a
// Handler added, and a call of the skill middleware's tool that runs a skill
// with context fork_with_context return a *dexagents.ToolExecutionError
// before anything runs. Since 0.6.16.
func (runner *Runner) ExecuteDeferredToolCall(ctx context.Context, call dexagents.DeferredToolExecution) (dexagents.DeferredToolOutcome, error) {
	record := call.ToolCall
	if runner.router.IsSourceToolCall(record) {
		return runner.router.ExecuteDeferredToolCall(ctx, call)
	}
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
	current := runner.toolStepPolicy(record.ToolName, entry)
	if isStricterThanRecord(current, record) {
		return nil, &dexagents.ToolExecutionError{Message: fmt.Sprintf("tool %s now has a stricter effect than when the "+
			"model called it, so it did not run; call it again to run it under its current policy", record.ToolName)}
	}
	if needsApprovalTheRecordLacks(current, record) {
		return nil, &dexagents.ToolExecutionError{Message: fmt.Sprintf("tool %s now needs an approval it did not have when the "+
			"model called it, so it did not run; call it again to run it under its current policy", record.ToolName)}
	}
	if isHistoryReadingAgentTool(entry.tool) {
		return nil, &dexagents.ToolExecutionError{Message: historyReadingAgentToolReason(record.ToolName)}
	}
	checkArguments, err := argumentsCheck(ctx, record.ToolName, entry.tool)
	if err != nil {
		return nil, err
	}
	node, err := compose.NewToolNode(ctx, toolStepNodeConfig(config, nodeTools, checkArguments))
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
		if failure := agentStateFailure(record.ToolName, entry.tool, err); failure != nil {
			return nil, failure
		}
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

// checkConfiguration refuses a configuration the adapter cannot run in Step mode and returns its tool source router.
func (runner *Runner) checkConfiguration(ctx context.Context) (*dexagents.ToolSourceRouter, error) {
	config, err := runner.buildAgentConfig(ctx, AgentBuildContext{Purpose: BuildPurposeCheck, SessionSettings: map[string]any{}})
	if err != nil {
		return nil, err
	}
	tools, err := runner.checkModelCallConfiguration(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("dexeino: %w", err)
	}
	router, err := newToolSourceRouter(runner.options, reservedToolNames(tools))
	if err != nil {
		return nil, fmt.Errorf("dexeino: %w", err)
	}
	sourceStubs, err := cachedSourceToolStubs(router)
	if err != nil {
		return nil, fmt.Errorf("dexeino: %w", err)
	}
	if _, err := compose.NewToolNode(ctx, toolStepNodeConfig(config, tools.baseTools())); err != nil {
		return nil, fmt.Errorf("dexeino: the agent's tools do not form a ToolsNode: %w", err)
	}
	if _, err := adk.NewChatModelAgent(ctx, modelCallAgentConfig(config, newModelCallRun(dexagents.AgentTurnContext{}), sourceStubs)); err != nil {
		return nil, fmt.Errorf("dexeino: Eino refuses the agent configuration: %w", err)
	}
	return router, nil
}

// checkModelCallConfiguration applies the construction checks to one model call's configuration and returns its tools.
func (runner *Runner) checkModelCallConfiguration(ctx context.Context, config *agentConfig) (agentTools, error) {
	if err := refuseUnsupportedConfig(config); err != nil {
		return agentTools{}, err
	}
	tools, err := runner.assembleAgentTools(ctx, config)
	if err != nil {
		return agentTools{}, err
	}
	if err := runner.checkToolEffects(tools); err != nil {
		return agentTools{}, err
	}
	if err := refuseHistoryReadingAgentTools(tools); err != nil {
		return agentTools{}, err
	}
	return tools, nil
}

// refuseUnsupportedConfig returns an error for a configuration that would make several model calls in a Step or run a tool there,
// or that asks for a tool call order Dex does not keep.
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
	case agent.ToolsConfig.ExecuteSequentially:
		return errors.New("ToolsConfig.ExecuteSequentially runs the tool calls of one model response in order, " +
			"but Dex runs them as parallel Steps; remove it, and keep tools safe to run in any order")
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
		if reason, isRefused := modelCallingHandlerPackages[handlerType.PkgPath()]; isRefused {
			return errors.New(reason + "; Dex Step mode makes one model call per RunModel Step")
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
