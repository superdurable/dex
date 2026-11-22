// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package builtinloop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/agents/go/core/internal/pythonjson"
)

// DefaultMaxModelCallsPerTurn is the default limit of model calls in one turn.
const DefaultMaxModelCallsPerTurn = 100

// ToolCallLimitReachedResultFormat is the result of a tool call made after the turn's model call
// limit; format it with the limit.
const ToolCallLimitReachedResultFormat = "The tool call was not run: this turn reached its limit of %d model calls."

// ToolSourceEnvelopeKey is the key of the source name in the envelope the built-in loop stores as
// AgentToolCall.ToolSourceReference for a call that a tool source built.
//
// It is the key the Python and TypeScript built-in loops store, so a stored
// call routes to the same source on a Worker of any core.
//
// Since 0.6.16.
const ToolSourceEnvelopeKey = "stepwise_tool_source"

// UnknownToolErrorFormat is the error of a call to a tool that no function tool or source listing
// offers; format it with the tool name. The model sees it after "The tool call failed: ".
//
// Since 0.6.16.
const UnknownToolErrorFormat = "unknown tool %s"

// InstructionContext tells an instructions function which model call it serves.
type InstructionContext struct {
	// FlowID is the session ID, which is the Flow ID.
	FlowID string
	// TurnID is "turn-<n>" of the running turn.
	TurnID string
	// Attempt is the one-based RunModel attempt.
	Attempt int64
	// InteractionMode is the turn's mode.
	InteractionMode dexagents.InteractionMode
	// SessionSettings are the settings the turn started with, such as a persona or prompt name.
	// The map is empty, never nil, when the session has none.
	SessionSettings map[string]any
}

// ModelResolver maps the turn's AgentTurnContext.SessionSettings to the model client of one
// model call.
//
// It runs in every RunModel attempt, so keep it fast and free of I/O: look
// the model up in clients the Worker built at startup. The settings come from
// the application, so choose only from models the Worker allows. The map is
// empty when the session has no settings. An error fails the attempt like a
// model error, and its text becomes the turn's LastTurnError, which snapshots
// show, so never put the settings in an error message. Only model calls see
// the settings: tool calls do not receive them.
//
//	models := map[string]builtinloop.ModelClient{
//		"gpt-5-mini": openaimodel.NewResponsesModelClient(client, "gpt-5-mini"),
//		"gpt-5":      openaimodel.NewResponsesModelClient(client, "gpt-5"),
//	}
//	resolveModel := func(sessionSettings map[string]any) (builtinloop.ModelClient, error) {
//		if model, isAllowed := models[fmt.Sprint(sessionSettings["model"])]; isAllowed {
//			return model, nil
//		}
//		return models["gpt-5-mini"], nil
//	}
type ModelResolver func(sessionSettings map[string]any) (ModelClient, error)

// InstructionsFunction returns the system instructions of one model call.
//
// It runs in every RunModel attempt, so a deploy changes the next model call.
// An error fails the attempt.
type InstructionsFunction func(instructionContext InstructionContext) (string, error)

// AgentDefinitionOption configures one AgentDefinition.
type AgentDefinitionOption interface {
	applyAgentDefinition(definition *AgentDefinition)
}

type agentDefinitionOptionFunc func(definition *AgentDefinition)

func (option agentDefinitionOptionFunc) applyAgentDefinition(definition *AgentDefinition) {
	option(definition)
}

// WithInstructionsFunction computes the instructions in every RunModel instead of the fixed text.
func WithInstructionsFunction(instructions InstructionsFunction) AgentDefinitionOption {
	return agentDefinitionOptionFunc(func(definition *AgentDefinition) {
		definition.instructionsFunction = instructions
	})
}

// WithModelResolver chooses the model client of each model call from the session settings.
//
// Without it, the model passed to NewAgentDefinition serves every call. See
// ModelResolver.
func WithModelResolver(resolveModel ModelResolver) AgentDefinitionOption {
	return agentDefinitionOptionFunc(func(definition *AgentDefinition) {
		definition.resolveModel = resolveModel
	})
}

// WithModelSettings sets the settings of every model call. The default is ModelSettings{}.
func WithModelSettings(settings ModelSettings) AgentDefinitionOption {
	return agentDefinitionOptionFunc(func(definition *AgentDefinition) {
		definition.modelSettings = settings
	})
}

// WithFunctionTools adds function tools. Their names must be unique.
func WithFunctionTools(tools ...*FunctionTool) AgentDefinitionOption {
	return agentDefinitionOptionFunc(func(definition *AgentDefinition) {
		definition.tools = append(definition.tools, tools...)
	})
}

// WithToolSources adds tool sources, such as a connectortools.ConnectorToolSource, whose tools the
// agent lists at run time.
//
// In every RunModel that offers tools, the runner lists each source, waiting
// at most the listing timeout, and offers the listed tools after the function
// tools. A source whose listing fails or takes longer offers no tools in that
// model call. A call of a listed tool is built by its source, which picks the
// effect, the approval, and the ordering, and runs in its own tool Step
// through that source, on any Worker that has a source of the same
// SourceName. A name that no listing offered fails as an unknown tool without
// an approval. NewAgentDefinition returns an error when two sources share a
// SourceName or a declared source tool name repeats a function tool or another
// source's tool.
//
// Since 0.6.16.
func WithToolSources(sources ...dexagents.ToolSource) AgentDefinitionOption {
	copied := slices.Clone(sources)
	return agentDefinitionOptionFunc(func(definition *AgentDefinition) {
		definition.toolSources = append(definition.toolSources, copied...)
	})
}

// WithToolSourceListingTimeout sets how long one model call waits for each tool source's listing.
//
// The default is dexagents.DefaultToolSourceListingTimeout, 10 seconds. It
// must be positive: NewAgentDefinition returns an error for zero or a
// negative timeout, as the other cores do.
//
// Since 0.6.16.
func WithToolSourceListingTimeout(timeout time.Duration) AgentDefinitionOption {
	return agentDefinitionOptionFunc(func(definition *AgentDefinition) {
		definition.toolSourceListingTimeout = &timeout
	})
}

// WithToolPolicies sets policies by tool name; they override a function tool's own effect and approval.
func WithToolPolicies(toolPolicies map[string]dexagents.ToolPolicy) AgentDefinitionOption {
	copied := maps.Clone(toolPolicies)
	return agentDefinitionOptionFunc(func(definition *AgentDefinition) {
		definition.toolPolicies = copied
	})
}

// WithDefaultToolEffect sets the effect of a tool without a policy. The default is unclassified.
func WithDefaultToolEffect(effect dexagents.ToolEffect) AgentDefinitionOption {
	return agentDefinitionOptionFunc(func(definition *AgentDefinition) {
		definition.defaultToolEffect = effect
	})
}

// WithMaxModelCallsPerTurn sets after how many model calls the next call offers no tools.
//
// If the model asks for tools anyway, the turn ends with that response and
// each requested call gets the result ToolCallLimitReachedResultFormat. The
// default is DefaultMaxModelCallsPerTurn. It must be positive.
func WithMaxModelCallsPerTurn(maxModelCallsPerTurn int) AgentDefinitionOption {
	return agentDefinitionOptionFunc(func(definition *AgentDefinition) {
		definition.maxModelCallsPerTurn = maxModelCallsPerTurn
	})
}

// WithInterruptDeadline sets how long an approval may stay pending. The default waits indefinitely.
func WithInterruptDeadline(deadline time.Duration) AgentDefinitionOption {
	return agentDefinitionOptionFunc(func(definition *AgentDefinition) {
		definition.interruptDeadline = deadline
	})
}

// WithEventStreamCapacityBytes sets the AgentEvents capacity. The default is 10 MiB.
func WithEventStreamCapacityBytes(capacityBytes int64) AgentDefinitionOption {
	return agentDefinitionOptionFunc(func(definition *AgentDefinition) {
		definition.eventStreamCapacityBytes = capacityBytes
	})
}

// AgentDefinition declares one built-in loop agent in Dex terms: instructions, model, and tools.
//
// Pass it to NewBuiltinAgentSessionFlow, which builds the session Flow and
// derives its session options, so tool policies and the interrupt deadline
// are declared once. Every model call runs in its own RunModel Step and every
// tool call in its own tool Step.
//
//	definition, err := builtinloop.NewAgentDefinition("support", "You help customers with their orders.",
//		openaimodel.NewResponsesModelClient(openai.NewClient(), "gpt-5-mini"),
//		builtinloop.WithFunctionTools(lookupOrder, refundOrder),
//		builtinloop.WithInterruptDeadline(24*time.Hour))
//	if err != nil {
//		return err
//	}
//	flow, err := builtinloop.NewBuiltinAgentSessionFlow(definition, "SupportAgent")
type AgentDefinition struct {
	name                     string
	instructions             string
	instructionsFunction     InstructionsFunction
	model                    ModelClient
	resolveModel             ModelResolver
	modelSettings            ModelSettings
	tools                    []*FunctionTool
	toolPolicies             map[string]dexagents.ToolPolicy
	defaultToolEffect        dexagents.ToolEffect
	maxModelCallsPerTurn     int
	interruptDeadline        time.Duration
	eventStreamCapacityBytes int64
	toolSources              []dexagents.ToolSource
	toolSourceListingTimeout *time.Duration
	toolSourceRouter         *dexagents.ToolSourceRouter
}

// NewAgentDefinition declares one built-in loop agent.
//
// name is used in events and must not be blank. instructions are the system
// instructions unless WithInstructionsFunction computes them. model must not
// be nil. It returns an error for a blank name, two tools with one name, an
// unknown default effect, a non-positive model call limit, or tool sources
// that dexagents.NewToolSourceRouter refuses.
func NewAgentDefinition(name string, instructions string, model ModelClient, options ...AgentDefinitionOption) (*AgentDefinition, error) {
	if model == nil {
		panic("builtinloop: NewAgentDefinition needs a model client")
	}
	definition := &AgentDefinition{
		name:                 name,
		instructions:         instructions,
		model:                model,
		toolPolicies:         map[string]dexagents.ToolPolicy{},
		defaultToolEffect:    dexagents.ToolEffectUnclassified,
		maxModelCallsPerTurn: DefaultMaxModelCallsPerTurn,
	}
	for _, option := range options {
		option.applyAgentDefinition(definition)
	}
	if isBlank(name) {
		return nil, errors.New("AgentDefinition name must not be blank")
	}
	if definition.maxModelCallsPerTurn <= 0 {
		return nil, errors.New("max model calls per turn must be positive")
	}
	if !definition.defaultToolEffect.IsValid() {
		return nil, fmt.Errorf("default tool effect %q is not a tool effect", definition.defaultToolEffect)
	}
	names := make([]string, 0, len(definition.tools))
	for _, tool := range definition.tools {
		if tool == nil {
			return nil, errors.New("function tools must not be nil")
		}
		if slices.Contains(names, tool.name) {
			return nil, fmt.Errorf("two tools are named %q", tool.name)
		}
		names = append(names, tool.name)
	}
	routerOptions := []dexagents.ToolSourceRouterOption{
		dexagents.WithReservedToolNames(names...),
		dexagents.WithToolSourceEnvelopeKey(ToolSourceEnvelopeKey),
	}
	if definition.toolSourceListingTimeout != nil {
		routerOptions = append(routerOptions, dexagents.WithToolSourceListingTimeout(*definition.toolSourceListingTimeout))
	}
	router, err := dexagents.NewToolSourceRouter(definition.toolSources, routerOptions...)
	if err != nil {
		return nil, err
	}
	definition.toolSourceRouter = router
	return definition, nil
}

// Name returns the agent name.
func (definition *AgentDefinition) Name() string {
	return definition.name
}

// BuiltinLoopAgentRunner runs an AgentDefinition as the native Dex Agents loop, with no framework.
//
// It implements dexagents.AgentFrameworkRunner,
// dexagents.UnfinishedTurnMessageBuilder, and
// dexagents.ToolCallStepOptionsProvider. RunModelStep makes at most one
// model call, from committed state only. It stores every item as it appears:
// the assistant item that asks for tools commits with the tool call records,
// and the tool results commit with the next model call. A tool call is never
// sent to a provider without a result. Most applications use
// NewBuiltinAgentSessionFlow instead of constructing it.
//
// Since 0.5.0. It replaces stepwise.StepwiseAgentRunner.
type BuiltinLoopAgentRunner struct {
	definition  *AgentDefinition
	toolsByName map[string]*FunctionTool
}

// NewBuiltinLoopAgentRunner creates the runner for one agent. definition must not be nil.
//
// Since 0.5.0. It replaces stepwise.NewStepwiseAgentRunner.
func NewBuiltinLoopAgentRunner(definition *AgentDefinition) *BuiltinLoopAgentRunner {
	if definition == nil {
		panic("builtinloop: NewBuiltinLoopAgentRunner needs a definition")
	}
	toolsByName := make(map[string]*FunctionTool, len(definition.tools))
	for _, tool := range definition.tools {
		toolsByName[tool.name] = tool
	}
	if definition.toolSourceRouter == nil {
		panic("builtinloop: NewBuiltinLoopAgentRunner needs a definition from NewAgentDefinition")
	}
	return &BuiltinLoopAgentRunner{definition: definition, toolsByName: toolsByName}
}

// NewBuiltinAgentSessionFlow builds the durable session Flow of one built-in loop agent.
//
// Register the Flow in a Registry and serve it with a Worker. Call it through
// dexagents.AgentSessionClient, as for any runner. flowType is the protocol
// Flow type, unique within the Registry; keep it stable, because running
// sessions resume by it. sessionOptions apply after the options the
// definition derives, such as dexagents.WithMessageWindowSize. It returns the
// errors of dexagents.NewAgentSessionFlow.
//
// Since 0.5.0. It replaces stepwise.NewStepwiseAgentSessionFlow.
func NewBuiltinAgentSessionFlow(
	definition *AgentDefinition,
	flowType string,
	sessionOptions ...dexagents.AgentSessionOption,
) (*dexagents.AgentSessionFlow, error) {
	options := []dexagents.AgentSessionOption{
		dexagents.WithToolPolicies(definition.toolPolicies),
		dexagents.WithDefaultToolEffect(definition.defaultToolEffect),
	}
	if definition.interruptDeadline != 0 {
		options = append(options, dexagents.WithInterruptDeadline(definition.interruptDeadline))
	}
	if definition.eventStreamCapacityBytes != 0 {
		options = append(options, dexagents.WithEventStreamCapacityBytes(definition.eventStreamCapacityBytes))
	}
	options = append(options, sessionOptions...)
	return dexagents.NewAgentSessionFlow(NewBuiltinLoopAgentRunner(definition), flowType, options...)
}

// BuildUserMessage stores user text, and Dex notes, as a provider-neutral user_text item.
func (runner *BuiltinLoopAgentRunner) BuildUserMessage(text string) (dexagents.FrameworkMessage, error) {
	return EncodeBuiltinLoopMessage(UserTextItem{Text: text})
}

// RunModelStep makes one model call of the turn.
//
// It returns *dexagents.ToolCallsRequested when the model asked for tools,
// otherwise *dexagents.TurnCompleted. Both carry the previous round's tool
// results and the new output items. Errors are the model client's.
func (runner *BuiltinLoopAgentRunner) RunModelStep(
	ctx context.Context,
	turn dexagents.AgentTurnContext,
) (dexagents.AgentTurnOutcome, error) {
	modelCalls, err := modelCallCount(turn.FrameworkRunState)
	if err != nil {
		return nil, err
	}
	unstoredResults, err := unstoredToolResults(turn)
	if err != nil {
		return nil, err
	}
	isToolBudgetSpent := modelCalls >= runner.definition.maxModelCallsPerTurn
	var sourceTools *dexagents.SourceToolListing
	if !isToolBudgetSpent {
		sourceTools, err = runner.definition.toolSourceRouter.ListSourceTools(ctx, turn)
		if err != nil {
			return nil, err
		}
	}
	request, err := runner.modelRequest(turn, unstoredResults, isToolBudgetSpent, sourceTools)
	if err != nil {
		return nil, err
	}
	model, err := runner.resolveModel(turn)
	if err != nil {
		return nil, err
	}
	response, err := model.CallModel(ctx, request, turn.Events)
	if err != nil {
		return nil, err
	}
	usage := response.Usage
	usage.ModelCalls = 1
	newMessages := make([]dexagents.FrameworkMessage, 0, len(unstoredResults)+len(response.OutputItems))
	for _, result := range unstoredResults {
		message, err := EncodeBuiltinLoopMessage(result)
		if err != nil {
			return nil, err
		}
		newMessages = append(newMessages, message)
	}
	for _, item := range response.OutputItems {
		message, err := EncodeBuiltinLoopMessage(item)
		if err != nil {
			return nil, err
		}
		newMessages = append(newMessages, message)
	}
	if len(response.ToolCalls) == 0 {
		return &dexagents.TurnCompleted{NewMessages: newMessages, Usage: usage}, nil
	}
	if isToolBudgetSpent {
		// The model ignored that no tools were offered; answer each call instead of running it.
		notRun := fmt.Sprintf(ToolCallLimitReachedResultFormat, runner.definition.maxModelCallsPerTurn)
		for _, call := range response.ToolCalls {
			message, err := EncodeBuiltinLoopMessage(ToolResultItem{ToolCallID: call.ToolCallID, ToolName: call.ToolName, Output: notRun})
			if err != nil {
				return nil, err
			}
			newMessages = append(newMessages, message)
		}
		return &dexagents.TurnCompleted{NewMessages: newMessages, Usage: usage}, nil
	}
	runState, err := pythonjson.Marshal(pythonjson.Object{{Key: "model_calls", Value: modelCalls + 1}}, pythonjson.Options{})
	if err != nil {
		return nil, err
	}
	toolCalls := make([]dexagents.DeferredToolCall, 0, len(response.ToolCalls))
	for _, call := range response.ToolCalls {
		deferred, err := runner.deferredToolCall(turn, call, sourceTools)
		if err != nil {
			return nil, err
		}
		toolCalls = append(toolCalls, deferred)
	}
	return &dexagents.ToolCallsRequested{
		FrameworkRunState: runState,
		ToolCalls:         toolCalls,
		Usage:             usage,
		NewMessages:       newMessages,
	}, nil
}

// ExecuteDeferredToolCall runs one function tool call, or one call that a tool source built, in its own Step.
//
// It returns the tool output, or a *dexagents.ToolExecutionError when the tool
// is unknown, the arguments break its schema, or the tool returned one. A call
// that a tool source built runs through that source, with ctx and call
// unchanged, and returns what the source returns; when this Worker
// has no source of that name, the error fails the attempt, so the call's
// effect class decides its outcome.
//
// Changed in 0.6.16: a call that a tool source built runs through that source.
func (runner *BuiltinLoopAgentRunner) ExecuteDeferredToolCall(
	ctx context.Context,
	call dexagents.DeferredToolExecution,
) (dexagents.DeferredToolOutcome, error) {
	record := call.ToolCall
	if runner.definition.toolSourceRouter.IsSourceToolCall(record) {
		return runner.definition.toolSourceRouter.ExecuteDeferredToolCall(ctx, call)
	}
	tool, isKnown := runner.toolsByName[record.ToolName]
	if !isKnown {
		return nil, &dexagents.ToolExecutionError{Message: fmt.Sprintf(UnknownToolErrorFormat, record.ToolName)}
	}
	output, err := tool.Invoke(ctx, record.Arguments, ToolCallContext{
		FlowID:         call.FlowID,
		TurnID:         record.TurnID,
		ToolCallID:     record.ToolCallID,
		IdempotencyKey: call.IdempotencyKey,
		Attempt:        call.Attempt,
		Events:         call.Events,
		Heartbeat:      call.Heartbeat,
	})
	if err != nil {
		return nil, err
	}
	return &dexagents.ToolExecutionResult{Output: output}, nil
}

// ToolCallStepOptions returns the options of a tool source's call for its tool Step.
//
// It returns nil for a function tool call and for a source without options,
// or an error when the source that built the call is not configured on this
// Worker, so the session uses the defaults.
//
// Since 0.6.16.
func (runner *BuiltinLoopAgentRunner) ToolCallStepOptions(toolCall dexagents.AgentToolCall) (*dexagents.ToolCallStepOptions, error) {
	return runner.definition.toolSourceRouter.ToolCallStepOptions(toolCall)
}

// BuildUnfinishedTurnMessages returns the results of the turn's tool calls that are not stored yet.
//
// It returns one tool_result message per unstored result.
func (runner *BuiltinLoopAgentRunner) BuildUnfinishedTurnMessages(
	_ context.Context,
	turn dexagents.AgentTurnContext,
) ([]dexagents.FrameworkMessage, error) {
	results, err := unstoredToolResults(turn)
	if err != nil {
		return nil, err
	}
	messages := make([]dexagents.FrameworkMessage, 0, len(results))
	for _, result := range results {
		message, err := EncodeBuiltinLoopMessage(result)
		if err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, nil
}

func (runner *BuiltinLoopAgentRunner) modelRequest(
	turn dexagents.AgentTurnContext,
	unstoredResults []ModelInputItem,
	isToolBudgetSpent bool,
	sourceTools *dexagents.SourceToolListing,
) (ModelRequest, error) {
	instructions, err := runner.instructions(turn)
	if err != nil {
		return ModelRequest{}, err
	}
	items := make([]ModelInputItem, 0, len(turn.Messages)+len(unstoredResults))
	for _, message := range turn.Messages {
		item, err := DecodeBuiltinLoopMessage(message)
		if err != nil {
			return ModelRequest{}, err
		}
		items = append(items, item)
	}
	items = append(items, unstoredResults...)
	request := ModelRequest{
		Instructions: instructions,
		InputItems:   PairToolCallsWithResults(items),
		Tools:        []ModelToolDefinition{},
		ToolChoice:   ToolChoice{Mode: ToolChoiceModeAuto},
		Settings:     runner.definition.modelSettings,
		Metadata: ModelRequestMetadata{
			FlowID:          turn.FlowID,
			TurnID:          turn.TurnID,
			StepExecutionID: turn.Events.StepExecutionID(),
			Attempt:         turn.Attempt,
		},
	}
	if isToolBudgetSpent {
		request.ToolChoice = ToolChoice{Mode: ToolChoiceModeNone}
		return request, nil
	}
	for _, tool := range runner.definition.tools {
		request.Tools = append(request.Tools, tool.Definition())
	}
	if sourceTools != nil {
		for _, tool := range sourceTools.Tools() {
			request.Tools = append(request.Tools, ModelToolDefinition{
				Name: tool.Name, Description: tool.Description, Parameters: tool.InputSchema,
			})
		}
	}
	return request, nil
}

func (runner *BuiltinLoopAgentRunner) instructions(turn dexagents.AgentTurnContext) (string, error) {
	if runner.definition.instructionsFunction == nil {
		return runner.definition.instructions, nil
	}
	return runner.definition.instructionsFunction(InstructionContext{
		FlowID: turn.FlowID, TurnID: turn.TurnID, Attempt: turn.Attempt, InteractionMode: turn.InteractionMode,
		SessionSettings: turn.SessionSettings,
	})
}

func (runner *BuiltinLoopAgentRunner) resolveModel(turn dexagents.AgentTurnContext) (ModelClient, error) {
	if runner.definition.resolveModel == nil {
		return runner.definition.model, nil
	}
	model, err := runner.definition.resolveModel(turn.SessionSettings)
	if err != nil {
		return nil, err
	}
	if model == nil {
		return nil, fmt.Errorf("the model resolver of agent %s returned no model client", runner.definition.name)
	}
	return model, nil
}

// deferredToolCall builds a call through the source that listed its tool, or as a function tool call.
func (runner *BuiltinLoopAgentRunner) deferredToolCall(
	turn dexagents.AgentTurnContext,
	call ModelToolCall,
	sourceTools *dexagents.SourceToolListing,
) (dexagents.DeferredToolCall, error) {
	if sourceTools != nil && sourceTools.HasTool(call.ToolName) {
		built, err := sourceTools.DeferredToolCall(turn, call.ToolCallID, call.ToolName, call.Arguments)
		if err != nil {
			return dexagents.DeferredToolCall{}, err
		}
		if built != nil {
			return *built, nil
		}
	}
	_, isFunctionTool := runner.toolsByName[call.ToolName]
	policy := runner.toolPolicy(turn, call.ToolName)
	if len(runner.definition.toolSources) > 0 && !isFunctionTool {
		// An unlisted name never reaches a source, so it fails without asking anyone to approve it.
		isApprovalRequired := false
		policy = dexagents.ToolPolicy{Effect: dexagents.ToolEffectUnclassified, RequiresApproval: &isApprovalRequired}
	}
	return dexagents.DeferredToolCall{
		ToolCallID: call.ToolCallID,
		ToolName:   call.ToolName,
		Arguments:  call.Arguments,
		Policy:     policy,
	}, nil
}

func (runner *BuiltinLoopAgentRunner) toolPolicy(turn dexagents.AgentTurnContext, toolName string) dexagents.ToolPolicy {
	if _, hasPolicy := turn.ToolPolicies[toolName]; hasPolicy {
		return dexagents.ResolveToolPolicy(toolName, turn.ToolPolicies, turn.DefaultToolEffect, nil)
	}
	tool, isKnown := runner.toolsByName[toolName]
	if !isKnown {
		// An unknown tool fails fast in CallTool, so the model sees why.
		isApprovalRequired := false
		return dexagents.ToolPolicy{Effect: dexagents.ToolEffectRead, RequiresApproval: &isApprovalRequired}
	}
	return tool.Policy()
}

func modelCallCount(frameworkRunState *string) (int, error) {
	if frameworkRunState == nil {
		return 0, nil
	}
	var runState struct {
		ModelCalls *int `json:"model_calls"`
	}
	if err := json.Unmarshal([]byte(*frameworkRunState), &runState); err != nil || runState.ModelCalls == nil {
		return 0, fmt.Errorf("built-in loop framework run state %q has no model_calls: %v", *frameworkRunState, err)
	}
	return *runState.ModelCalls, nil
}

func unstoredToolResults(turn dexagents.AgentTurnContext) ([]ModelInputItem, error) {
	storedResultIDs := map[string]bool{}
	for _, message := range turn.Messages {
		if message.TurnID == turn.TurnID && message.Role == dexagents.MessageRoleTool && message.ToolCallID != nil {
			storedResultIDs[*message.ToolCallID] = true
		}
	}
	records := slices.Collect(maps.Values(turn.ToolCalls))
	slices.SortStableFunc(records, func(left, right dexagents.AgentToolCall) int {
		return int(left.DeferralOrder - right.DeferralOrder)
	})
	results := []ModelInputItem{}
	for _, record := range records {
		if storedResultIDs[record.ToolCallID] {
			continue
		}
		output, err := dexagents.DescribeToolCallOutcome(record)
		if err != nil {
			return nil, err
		}
		results = append(results, ToolResultItem{ToolCallID: record.ToolCallID, ToolName: record.ToolName, Output: output})
	}
	return results, nil
}

func isBlank(text string) bool {
	for _, character := range text {
		if !(character == ' ' || (character >= '\t' && character <= '\r') || (character >= 0x1C && character <= 0x1F) ||
			character == 0x85 || character == 0xA0 || character == 0x1680 || (character >= 0x2000 && character <= 0x200A) ||
			character == 0x2028 || character == 0x2029 || character == 0x202F || character == 0x205F || character == 0x3000) {
			return false
		}
	}
	return true
}
