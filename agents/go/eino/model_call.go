// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dexeino

import (
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"sync"
	"unicode"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	dexagents "github.com/superdurable/dex/agents/go/core"
)

// toolCallGateInfo is the Info of the Eino interrupt the gate raises for one tool call.
type toolCallGateInfo struct {
	ToolCallID string
	ToolName   string
}

func init() {
	schema.RegisterName[*toolCallGateInfo]("dex_agents_eino_tool_call_gate")
	// ADK gob-encodes the run on an interrupt; replayed Extra holds JSON-decoded values.
	registerGobType([]any{})
	registerGobType(map[string]any{})
}

func registerGobType(value any) {
	defer func() {
		// Another package registered the type under its own name, which serves as well.
		_ = recover()
	}()
	gob.Register(value)
}

// errSecondModelCall is the error the innermost model wrapper returns instead of a second model call.
var errSecondModelCall = errors.New("dexeino: the Eino agent tried a second model call in one RunModel Step")

// errModelNotCalled stops a run whose model message did not come through the agent's model.
var errModelNotCalled = errors.New("dexeino: a Handler's WrapModel answered without calling the model it wraps")

// modelCallRun holds what one RunModel's Eino run observed. Each RunModel makes a new one.
type modelCallRun struct {
	events          *dexagents.AgentEventWriter
	stepExecutionID string
	usedToolCallIDs map[string]bool

	mutex             sync.Mutex
	captured          *schema.Message
	modelCalls        int64
	modelCallFailure  error
	usage             dexagents.AgentUsage
	offeredTools      map[string]bool
	isSecondCallTried bool
	// isModelNotCalled is set when a Handler's WrapModel answered from a model the counter does not wrap.
	isModelNotCalled bool
	// handedMessage and finalMessage encode the message Eino got from the gate and the one it kept after its hooks.
	handedMessage []byte
	finalMessage  []byte
}

func newModelCallRun(turn dexagents.AgentTurnContext) *modelCallRun {
	run := &modelCallRun{
		events:          turn.Events,
		stepExecutionID: "RunModel",
		usedToolCallIDs: map[string]bool{},
		offeredTools:    map[string]bool{},
	}
	if turn.Events != nil && turn.Events.StepExecutionID() != "" {
		run.stepExecutionID = turn.Events.StepExecutionID()
	}
	for toolCallID := range turn.ToolCalls {
		run.usedToolCallIDs[toolCallID] = true
	}
	return run
}

// capturedMessage returns the model's message, or nil when no model call returned one.
func (run *modelCallRun) capturedMessage() *schema.Message {
	run.mutex.Lock()
	defer run.mutex.Unlock()
	return run.captured
}

// modelCallUsage returns the usage of the model call; a call that failed is not counted.
//
// A message from a model the counter does not wrap counts as one call with the usage it reports.
func (run *modelCallRun) modelCallUsage() dexagents.AgentUsage {
	run.mutex.Lock()
	defer run.mutex.Unlock()
	usage := run.usage
	if run.modelCallFailure == nil {
		usage.ModelCalls = run.modelCalls
	}
	if run.isModelNotCalled {
		usage.ModelCalls = 1
	}
	return usage
}

// failedModelCall returns the error of the one model call, or nil when it did not fail.
func (run *modelCallRun) failedModelCall() error {
	run.mutex.Lock()
	defer run.mutex.Unlock()
	return run.modelCallFailure
}

func (run *modelCallRun) recordModelCallFailure(err error) error {
	run.mutex.Lock()
	defer run.mutex.Unlock()
	if run.modelCallFailure == nil {
		run.modelCallFailure = err
	}
	return err
}

// beginModelCall counts one model call and reports whether it may run.
func (run *modelCallRun) beginModelCall(options []model.Option) bool {
	run.mutex.Lock()
	defer run.mutex.Unlock()
	if run.modelCalls > 0 {
		run.isSecondCallTried = true
		return false
	}
	run.modelCalls++
	common := model.GetCommonOptions(&model.Options{}, options...)
	// The tool search middleware offers tools as deferred tools and as the tool search tool, too.
	offered := append(slices.Clone(common.Tools), common.DeferredTools...)
	for _, info := range append(offered, common.ToolSearchTool) {
		if info != nil {
			run.offeredTools[info.Name] = true
		}
	}
	return true
}

// recordUsage merges one message's usage into the call's, keeping each field's largest value as Eino merges chunks.
func (run *modelCallRun) recordUsage(usage *schema.TokenUsage) {
	run.mutex.Lock()
	defer run.mutex.Unlock()
	run.mergeUsageLocked(usage)
}

func (run *modelCallRun) mergeUsageLocked(usage *schema.TokenUsage) {
	if usage == nil {
		return
	}
	run.usage.InputTokens = max(run.usage.InputTokens, int64(usage.PromptTokens))
	run.usage.OutputTokens = max(run.usage.OutputTokens, int64(usage.CompletionTokens))
	run.usage.CachedInputTokens = max(run.usage.CachedInputTokens, int64(usage.PromptTokenDetails.CachedTokens))
	run.usage.ReasoningTokens = max(run.usage.ReasoningTokens, int64(usage.CompletionTokensDetails.ReasoningTokens))
}

// captureModelMessage stores the model's message with valid, unique tool call IDs and returns it for Eino.
func (run *modelCallRun) captureModelMessage(message *schema.Message) (*schema.Message, error) {
	if message == nil {
		return nil, errors.New("dexeino: the model returned no message")
	}
	run.mutex.Lock()
	defer run.mutex.Unlock()
	if run.captured != nil {
		return nil, errSecondModelCall
	}
	if run.modelCalls == 0 {
		// The counter never saw this call, so its tools, its usage, and a second call went unseen.
		run.isModelNotCalled = true
		if message.ResponseMeta != nil {
			run.mergeUsageLocked(message.ResponseMeta.Usage)
		}
		return nil, errModelNotCalled
	}
	fixed := *message
	fixed.Role = schema.Assistant
	fixed.ToolCalls = slices.Clone(message.ToolCalls)
	for index := range fixed.ToolCalls {
		fixed.ToolCalls[index].ID = run.validToolCallID(fixed.ToolCalls[index].ID, index)
	}
	// Eino's interrupt copies its state with a serializer that refuses unregistered Extra types.
	forEino, err := jsonDecodedCopy(&fixed)
	if err != nil {
		return nil, err
	}
	handed, err := encodeComparableMessage(forEino)
	if err != nil {
		return nil, err
	}
	run.captured = withoutEinoInternalExtra(&fixed)
	run.handedMessage = handed
	return forEino, nil
}

// recordFinalMessage is the last AfterChatModel hook: it records the message Eino goes on with.
func (run *modelCallRun) recordFinalMessage(_ context.Context, state *adk.ChatModelAgentState) error {
	var final []byte
	if len(state.Messages) > 0 {
		encoded, err := encodeComparableMessage(state.Messages[len(state.Messages)-1])
		if err != nil {
			return err
		}
		final = encoded
	}
	run.mutex.Lock()
	defer run.mutex.Unlock()
	run.finalMessage = final
	return nil
}

// encodeComparableMessage encodes a message without the Extra keys Eino ADK sets for itself.
func encodeComparableMessage(message *schema.Message) ([]byte, error) {
	if message == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(withoutEinoInternalExtra(message))
	if err != nil {
		return nil, fmt.Errorf("dexeino: the model message does not encode as JSON: %w", err)
	}
	return encoded, nil
}

// toolCallsChangedReason is the TurnError of a model message whose tool calls changed after the model call.
const toolCallsChangedReason = "the Eino agent's tool calls changed after the model call; a Handler's " +
	"AfterModelRewriteState or an AgentMiddleware's AfterChatModel that rewrites them is not supported, " +
	"because Dex stores and runs the message the model call returned; change the message in a Handler's WrapModel instead"

// messageChangedAfterModelCall returns the TurnError for a message the after-model hooks changed, or "".
//
// A final message the last hook never recorded counts as changed.
func (run *modelCallRun) messageChangedAfterModelCall() string {
	run.mutex.Lock()
	defer run.mutex.Unlock()
	if bytes.Equal(run.handedMessage, run.finalMessage) {
		return ""
	}
	var handed, final schema.Message
	if json.Unmarshal(run.handedMessage, &handed) == nil && json.Unmarshal(run.finalMessage, &final) == nil {
		handedCalls, handedErr := json.Marshal(handed.ToolCalls)
		finalCalls, finalErr := json.Marshal(final.ToolCalls)
		if handedErr == nil && finalErr == nil && !bytes.Equal(handedCalls, finalCalls) {
			return toolCallsChangedReason
		}
	}
	return "the Eino agent's model message changed after the model call; a Handler's AfterModelRewriteState or an " +
		"AgentMiddleware's AfterChatModel that changes it is not supported, because Dex stores the message the model " +
		"call returned; change the message in a Handler's WrapModel instead"
}

// jsonDecodedCopy returns message as encoding/json decodes it, so Extra holds only JSON values.
func jsonDecodedCopy(message *schema.Message) (*schema.Message, error) {
	encoded, err := json.Marshal(message)
	if err != nil {
		return nil, fmt.Errorf("dexeino: the model message does not encode as JSON: %w", err)
	}
	decoded := &schema.Message{}
	if err := json.Unmarshal(encoded, decoded); err != nil {
		return nil, fmt.Errorf("dexeino: decode the model message: %w", err)
	}
	return decoded, nil
}

// validToolCallID keeps a usable ID and otherwise synthesizes "<StepExecutionID>-<index>", which a retry repeats.
func (run *modelCallRun) validToolCallID(toolCallID string, index int) string {
	if !isBlankToolCallID(toolCallID) && !strings.Contains(toolCallID, "/") && !run.usedToolCallIDs[toolCallID] {
		run.usedToolCallIDs[toolCallID] = true
		return toolCallID
	}
	base := strings.ReplaceAll(run.stepExecutionID, "/", "-")
	candidate := fmt.Sprintf("%s-%d", base, index)
	for suffix := 1; run.usedToolCallIDs[candidate]; suffix++ {
		candidate = fmt.Sprintf("%s-%d-%d", base, index, suffix)
	}
	run.usedToolCallIDs[candidate] = true
	return candidate
}

// isBlankToolCallID applies the session's blank rule: only Unicode spaces and the separators U+001C to U+001F.
func isBlankToolCallID(toolCallID string) bool {
	return strings.TrimFunc(toolCallID, func(character rune) bool {
		return unicode.IsSpace(character) || (character >= 0x1C && character <= 0x1F)
	}) == ""
}

func (run *modelCallRun) writeDeltas(message *schema.Message) error {
	if run.events == nil || message == nil {
		return nil
	}
	if message.ReasoningContent != "" {
		if err := run.events.WriteReasoningDelta(message.ReasoningContent); err != nil {
			return err
		}
	}
	if message.Content != "" {
		return run.events.WriteTextDelta(message.Content)
	}
	return nil
}

// gateToolCall stops the Eino run at a tool call; the session runs the call in its own Step.
func (run *modelCallRun) gateToolCall(ctx context.Context, toolCallID string, toolName string) error {
	return tool.Interrupt(ctx, &toolCallGateInfo{ToolCallID: toolCallID, ToolName: toolName})
}

// gateUnknownToolCall is the UnknownToolsHandler of a model call; Handlers do not wrap unknown tools.
func (run *modelCallRun) gateUnknownToolCall(ctx context.Context, toolName string, _ string) (string, error) {
	return "", run.gateToolCall(ctx, compose.GetToolCallID(ctx), toolName)
}

// toolCallGate is the first Handler of a model call's agent: the outermost model and tool wrapper.
type toolCallGate struct {
	*adk.BaseChatModelAgentMiddleware
	run *modelCallRun
}

func (gate *toolCallGate) WrapModel(_ context.Context, inner model.BaseModel[*schema.Message], _ *adk.ModelContext) (model.BaseModel[*schema.Message], error) {
	return &capturingModel{inner: inner, run: gate.run}, nil
}

func (gate *toolCallGate) WrapInvokableToolCall(_ context.Context, _ adk.InvokableToolCallEndpoint, toolContext *adk.ToolContext) (adk.InvokableToolCallEndpoint, error) {
	return func(ctx context.Context, _ string, _ ...tool.Option) (string, error) {
		return "", gate.run.gateToolCall(ctx, toolContext.CallID, toolContext.Name)
	}, nil
}

func (gate *toolCallGate) WrapStreamableToolCall(_ context.Context, _ adk.StreamableToolCallEndpoint, toolContext *adk.ToolContext) (adk.StreamableToolCallEndpoint, error) {
	return func(ctx context.Context, _ string, _ ...tool.Option) (*schema.StreamReader[string], error) {
		return nil, gate.run.gateToolCall(ctx, toolContext.CallID, toolContext.Name)
	}, nil
}

func (gate *toolCallGate) WrapEnhancedInvokableToolCall(_ context.Context, _ adk.EnhancedInvokableToolCallEndpoint, toolContext *adk.ToolContext) (adk.EnhancedInvokableToolCallEndpoint, error) {
	return func(ctx context.Context, _ *schema.ToolArgument, _ ...tool.Option) (*schema.ToolResult, error) {
		return nil, gate.run.gateToolCall(ctx, toolContext.CallID, toolContext.Name)
	}, nil
}

func (gate *toolCallGate) WrapEnhancedStreamableToolCall(_ context.Context, _ adk.EnhancedStreamableToolCallEndpoint, toolContext *adk.ToolContext) (adk.EnhancedStreamableToolCallEndpoint, error) {
	return func(ctx context.Context, _ *schema.ToolArgument, _ ...tool.Option) (*schema.StreamReader[*schema.ToolResult], error) {
		return nil, gate.run.gateToolCall(ctx, toolContext.CallID, toolContext.Name)
	}, nil
}

// capturingModel stores the one model message, fixes its tool call IDs, streams deltas, and exposes HTTP statuses.
type capturingModel struct {
	inner model.BaseModel[*schema.Message]
	run   *modelCallRun
}

func (capturing *capturingModel) Generate(ctx context.Context, input []*schema.Message, options ...model.Option) (*schema.Message, error) {
	message, err := capturing.inner.Generate(ctx, input, options...)
	if err != nil {
		return nil, exposeModelHTTPStatus(err)
	}
	if err := capturing.run.writeDeltas(message); err != nil {
		return nil, err
	}
	return capturing.run.captureModelMessage(message)
}

func (capturing *capturingModel) Stream(ctx context.Context, input []*schema.Message, options ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	stream, err := capturing.inner.Stream(ctx, input, options...)
	if err != nil {
		return nil, exposeModelHTTPStatus(err)
	}
	defer stream.Close()
	var chunks []*schema.Message
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, exposeModelHTTPStatus(err)
		}
		if chunk == nil {
			continue
		}
		if err := capturing.run.writeDeltas(chunk); err != nil {
			return nil, err
		}
		chunks = append(chunks, chunk)
	}
	if len(chunks) == 0 {
		return nil, errors.New("dexeino: the model stream ended without a message")
	}
	message, err := schema.ConcatMessages(chunks)
	if err != nil {
		return nil, fmt.Errorf("dexeino: concatenate the model stream: %w", err)
	}
	captured, err := capturing.run.captureModelMessage(message)
	if err != nil {
		return nil, err
	}
	// The whole message is one chunk, so Eino state and events carry the fixed tool call IDs.
	return schema.StreamReaderFromArray([]*schema.Message{captured}), nil
}

// modelCallCounter is the last Handler of a model call's agent: the innermost model wrapper.
type modelCallCounter struct {
	*adk.BaseChatModelAgentMiddleware
	run *modelCallRun
}

func (counter *modelCallCounter) WrapModel(_ context.Context, inner model.BaseModel[*schema.Message], _ *adk.ModelContext) (model.BaseModel[*schema.Message], error) {
	return &countingModel{inner: inner, run: counter.run}, nil
}

// countingModel lets exactly one model call reach the model and records its usage.
type countingModel struct {
	inner model.BaseModel[*schema.Message]
	run   *modelCallRun
}

func (counting *countingModel) Generate(ctx context.Context, input []*schema.Message, options ...model.Option) (*schema.Message, error) {
	if !counting.run.beginModelCall(options) {
		return nil, errSecondModelCall
	}
	message, err := counting.inner.Generate(ctx, input, options...)
	if err != nil {
		return nil, counting.run.recordModelCallFailure(err)
	}
	if message != nil && message.ResponseMeta != nil {
		counting.run.recordUsage(message.ResponseMeta.Usage)
	}
	return message, nil
}

func (counting *countingModel) Stream(ctx context.Context, input []*schema.Message, options ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	if !counting.run.beginModelCall(options) {
		return nil, errSecondModelCall
	}
	stream, err := counting.inner.Stream(ctx, input, options...)
	if err != nil {
		return nil, counting.run.recordModelCallFailure(err)
	}
	return schema.StreamReaderWithConvert(stream, func(chunk *schema.Message) (*schema.Message, error) {
		if chunk != nil && chunk.ResponseMeta != nil {
			counting.run.recordUsage(chunk.ResponseMeta.Usage)
		}
		return chunk, nil
	}, schema.WithErrWrapper(counting.run.recordModelCallFailure)), nil
}

// modelCallAgentConfig returns the agent config of one model call: the user's, with the gate and counter around it.
//
// Tool middlewares, aliases, and the arguments handler run in the tool Step,
// so the model call's ToolsNode only gates calls.
func modelCallAgentConfig(config *agentConfig, run *modelCallRun, sourceStubs []tool.BaseTool) *adk.ChatModelAgentConfig {
	agent := config.agent
	agent.ToolsConfig = adk.ToolsConfig{
		ToolsNodeConfig: compose.ToolsNodeConfig{
			Tools:               append(slices.Clone(config.agent.ToolsConfig.Tools), sourceStubs...),
			UnknownToolsHandler: run.gateUnknownToolCall,
			ExecuteSequentially: true,
		},
		EmitInternalEvents: config.agent.ToolsConfig.EmitInternalEvents,
	}
	agent.Middlewares = make([]adk.AgentMiddleware, 0, len(config.agent.Middlewares)+1)
	for _, middleware := range config.agent.Middlewares {
		middleware.WrapToolCall = compose.ToolMiddleware{}
		agent.Middlewares = append(agent.Middlewares, middleware)
	}
	// AfterChatModel hooks run after every Handler's AfterModelRewriteState, so the last one sees Eino's message.
	agent.Middlewares = append(agent.Middlewares, adk.AgentMiddleware{AfterChatModel: run.recordFinalMessage})
	agent.Handlers = make([]adk.ChatModelAgentMiddleware, 0, len(config.agent.Handlers)+2)
	agent.Handlers = append(agent.Handlers, &toolCallGate{BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{}, run: run})
	agent.Handlers = append(agent.Handlers, config.agent.Handlers...)
	agent.Handlers = append(agent.Handlers, &modelCallCounter{BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{}, run: run})
	return &agent
}

// drainedModelRun is what the Eino event stream of one model call reported.
type drainedModelRun struct {
	err               error
	gatedCalls        map[string]string
	foreignInterrupts []string
}

func drainModelRun(iterator *adk.AsyncIterator[*adk.AgentEvent]) drainedModelRun {
	drained := drainedModelRun{gatedCalls: map[string]string{}}
	for {
		event, isOpen := iterator.Next()
		if !isOpen {
			return drained
		}
		if event == nil {
			continue
		}
		if event.Err != nil && drained.err == nil {
			drained.err = event.Err
		}
		if event.Action == nil || event.Action.Interrupted == nil {
			continue
		}
		for _, interrupt := range event.Action.Interrupted.InterruptContexts {
			if interrupt == nil || !interrupt.IsRootCause {
				continue
			}
			if gated, isGate := interrupt.Info.(*toolCallGateInfo); isGate {
				drained.gatedCalls[gated.ToolCallID] = gated.ToolName
				continue
			}
			drained.foreignInterrupts = append(drained.foreignInterrupts, fmt.Sprintf("%v (%T)", interrupt.ID, interrupt.Info))
		}
	}
}

func toolCallIDSet(calls []schema.ToolCall) map[string]string {
	set := make(map[string]string, len(calls))
	for _, call := range calls {
		set[call.ID] = call.Function.Name
	}
	return set
}

func sameToolCallIDs(left map[string]string, right map[string]string) bool {
	return maps.EqualFunc(left, right, func(string, string) bool { return true })
}
