// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package aiagent

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/superdurable/dex-connectors-library/connectors/slack"
	"github.com/superdurable/dex-connectors-library/sdkgo"
	"github.com/superdurable/dex/sdk-go/dex"
)

const (
	StatusWaitingForUser     = "waiting_for_user"
	StatusWaitingForAnswer   = "waiting_for_answer"
	StatusCallingModel       = "calling_model"
	StatusWaitingForApproval = "waiting_for_approval"
	StatusWaitingForTimer    = "waiting_for_timer"
	StatusExecutingTool      = "executing_tool"

	ModelFailureProviderAPIError = "provider_api_error"
	ModelFailureTimeout          = "timeout"
	ModelFailureUnavailable      = "unavailable"

	maxRecoveryDetailLength = 300
	rpcTimeout              = 10 * time.Second
)

var (
	// The conversation lives in Dex; every model call replays it.
	Messages = dex.DefineAttribute[[]Message]("Messages")
	// Status commits with the Step that sets it, so it is set before moving into a long Step.
	Status = dex.DefineAttribute[string]("Status")
	// The tool calls of the latest model reply that have not finished, oldest first.
	PendingToolCalls = dex.DefineAttribute[[]ToolCall]("PendingToolCalls")
	PendingApproval  = dex.DefineAttribute[ToolCall]("PendingApproval")
	PendingTimer     = dex.DefineAttribute[TimerWait]("PendingTimer")
	PendingQuestion  = dex.DefineAttribute[UserQuestion]("PendingQuestion")
	LastModelFailure = dex.DefineAttribute[ModelFailure]("LastModelFailure")

	UserMessages = dex.DefineChannel[string]("UserMessages")
	// Keyed by tool call ID, so a decision only releases its own call.
	ToolApprovals = dex.DefineChannelMap[ToolApprovalDecision]("ToolApprovals")
)

var recoveryStepOptions = &dex.StepOptions{ExecuteRetry: &dex.RetryPolicy{MaximumInterval: 5 * time.Second}}

// TimerWait is a wait tool call whose durable Timer has not fired yet.
type TimerWait struct {
	CallID  string    `json:"callId"`
	Seconds int       `json:"seconds"`
	FiresAt time.Time `json:"firesAt"`
}

// UserQuestion is an ask_user call waiting for the user's answer.
type UserQuestion struct {
	Call     ToolCall `json:"call"`
	Question string   `json:"question"`
}

// ModelFailure describes the last model call that failed after its retries.
type ModelFailure struct {
	Kind               string    `json:"kind"`
	IsProviderAPIError bool      `json:"isProviderApiError"`
	Detail             string    `json:"detail"`
	FailedAt           time.Time `json:"failedAt"`
}

// ToolApprovalDecision is the user's answer to a pending tool approval.
type ToolApprovalDecision struct {
	CallID     string `json:"callId"`
	IsApproved bool   `json:"isApproved"`
}

// AgentSnapshot is everything the chat page shows, read by one RPC.
type AgentSnapshot struct {
	Model            string        `json:"model"`
	Status           string        `json:"status"`
	Messages         []Message     `json:"messages"`
	QueuedMessages   int           `json:"queuedMessages"`
	PendingToolCalls []ToolCall    `json:"pendingToolCalls,omitempty"`
	PendingApproval  *ToolCall     `json:"pendingApproval,omitempty"`
	PendingTimer     *TimerWait    `json:"pendingTimer,omitempty"`
	PendingQuestion  *UserQuestion `json:"pendingQuestion,omitempty"`
	LastModelFailure *ModelFailure `json:"lastModelFailure,omitempty"`
}

// AIAgentFlow is one durable agent conversation: it waits for the user, calls the model and runs tools.
type AIAgentFlow struct {
	dex.FlowDefaults
	model         ModelClient
	toolbox       *Toolbox
	slackSettings SlackSettings
	// modelToolDefinitions offers post_to_slack only when Slack is configured.
	modelToolDefinitions []ToolDefinition
}

func NewAIAgentFlow(model ModelClient, toolbox *Toolbox, slackSettings SlackSettings) *AIAgentFlow {
	if model == nil || toolbox == nil {
		panic("aiagent: the model client and toolbox are required")
	}
	modelToolDefinitions := toolDefinitions
	if slackSettings.IsConfigured() {
		modelToolDefinitions = append(append([]ToolDefinition(nil), toolDefinitions...), postToSlackToolDefinition)
	}
	return &AIAgentFlow{model: model, toolbox: toolbox, slackSettings: slackSettings, modelToolDefinitions: modelToolDefinitions}
}

func (flow *AIAgentFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(AwaitUser{}),
		dex.DefineStep(CallModel{flow: flow}),
		dex.DefineStep(RecordModelFailure{}),
		dex.DefineStep(RouteTool{}),
		dex.DefineStep(AwaitToolApproval{}),
		dex.DefineStep(AwaitTimer{}),
		dex.DefineStep(ExecuteTool{flow: flow}),
		dex.DefineStep(RecordToolFailure{}),
		dex.DefineStep(slack.NewPostChannelMessageStep(slack.PostChannelMessageStepConfig[ToolCall]{
			StepType: postToSlackStepType,
			Annotations: sdkgo.StepAnnotations{
				GroupID:     "tools",
				GroupLabel:  "Tools",
				Explanation: "Post an approved message to Slack through the Dex Slack connector.",
			},
			ConfigurationUI: sdkgo.ConnectorConfigurationUI{Units: []sdkgo.ConnectorUIUnit{{
				ID: "channel", UnitID: slack.UIUnitChannelPicker, Label: "Channel", Required: true,
				Description: "Select the joined Slack channel that post_to_slack posts to; the picker stores its stable channel ID.",
				Bindings:    []sdkgo.ConnectorUIBinding{{Port: slack.UIChannelPickerPortChannelID, JSONPointer: "/channelId"}},
			}}},
			Connection:          flow.slackSettings.Connection,
			ConnectionName:      slackConnectionName,
			MapToOperationInput: flow.slackSettings.mapToolCallToSlackMessage,
			Sent:                sdkgo.GoTo(RecordSlackPost{}),
			ProviderRejected:    sdkgo.GoTo(RecordSlackPost{}),
			Uncertain:           sdkgo.GoTo(RecordSlackPost{}),
			Defect:              sdkgo.GoTo(RecordSlackPost{}),
			// After a crash, the connector's dispatch fence turns the retry into Uncertain instead of a second post.
			StepOptionsOverride: &dex.StepOptions{
				ExecuteFailure: dex.ProceedToOnExecuteFailure(RecordToolFailure{}, recoveryStepOptions),
			},
		})),
		dex.DefineStep(RecordSlackPost{}),
	}
}

func (flow *AIAgentFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.SendUserMessage, &dex.RPCOptions{Timeout: rpcTimeout}),
		dex.DefineRPC(flow.DecideToolApproval, &dex.RPCOptions{
			Timeout:        rpcTimeout,
			LockAttributes: []dex.AttributeLock{dex.LockAttribute(PendingApproval)},
		}),
		dex.DefineRPC(flow.GetAgentSnapshot, &dex.RPCOptions{Timeout: rpcTimeout}),
		dex.DefineRPC(flow.GetDexSummary, nil),
		dex.DefineRPC(flow.GetDexDisplay, nil),
	}
}

func (*AIAgentFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{
			Messages, Status, PendingToolCalls, PendingApproval, PendingTimer, PendingQuestion, LastModelFailure,
		},
		Channels: []dex.ChannelDef{UserMessages, ToolApprovals},
	}
}

// SendUserMessage queues a message. AwaitUser takes it the next time the agent waits for the user.
func (*AIAgentFlow) SendUserMessage(ctx dex.Context, text string) (*dex.RPCResult[bool], error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return &dex.RPCResult[bool]{Output: false}, nil
	}
	if err := UserMessages.Publish(ctx, text); err != nil {
		return nil, err
	}
	return &dex.RPCResult[bool]{Output: true}, nil
}

// DecideToolApproval answers the pending approval. It returns false when that call is no longer waiting.
func (*AIAgentFlow) DecideToolApproval(ctx dex.Context, decision ToolApprovalDecision) (*dex.RPCResult[bool], error) {
	pending, isPending, err := getOptionalAttribute(ctx, PendingApproval)
	if err != nil {
		return nil, err
	}
	if !isPending || pending.ID != decision.CallID {
		return &dex.RPCResult[bool]{Output: false}, nil
	}
	if err := PendingApproval.Delete(ctx); err != nil {
		return nil, err
	}
	if err := ToolApprovals.Publish(ctx, decision.CallID, decision); err != nil {
		return nil, err
	}
	return &dex.RPCResult[bool]{Output: true}, nil
}

func (flow *AIAgentFlow) GetAgentSnapshot(ctx dex.Context, _ dex.None) (*dex.RPCResult[AgentSnapshot], error) {
	snapshot := AgentSnapshot{Model: flow.model.Name(), QueuedMessages: UserMessages.Size(ctx)}
	var err error
	if snapshot.Status, _, err = getOptionalAttribute(ctx, Status); err != nil {
		return nil, err
	}
	if snapshot.Messages, _, err = getOptionalAttribute(ctx, Messages); err != nil {
		return nil, err
	}
	if snapshot.PendingToolCalls, _, err = getOptionalAttribute(ctx, PendingToolCalls); err != nil {
		return nil, err
	}
	if snapshot.PendingApproval, err = getAttributePointer(ctx, PendingApproval); err != nil {
		return nil, err
	}
	if snapshot.PendingTimer, err = getAttributePointer(ctx, PendingTimer); err != nil {
		return nil, err
	}
	if snapshot.PendingQuestion, err = getAttributePointer(ctx, PendingQuestion); err != nil {
		return nil, err
	}
	if snapshot.LastModelFailure, err = getAttributePointer(ctx, LastModelFailure); err != nil {
		return nil, err
	}
	return &dex.RPCResult[AgentSnapshot]{Output: snapshot}, nil
}

// dex:field attribute-key:Status value-type:string editable:false description:"Agent status" ui-slot:status
func (*AIAgentFlow) GetDexSummary(ctx dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	status, err := getAttributePointer(ctx, Status)
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[map[string]any]{Output: map[string]any{"Status": status}}, nil
}

// dex:field attribute-key:Status value-type:string editable:false description:"Agent status" ui-slot:status
// dex:field attribute-key:PendingApproval value-type:json editable:false description:"Tool call waiting for approval"
// dex:field attribute-key:PendingTimer value-type:json editable:false description:"Wait call on a durable Timer"
// dex:field attribute-key:PendingQuestion value-type:json editable:false description:"Question waiting for the user"
// dex:field attribute-key:LastModelFailure value-type:json editable:false description:"Last failed model call"
// dex:field attribute-key:Messages value-type:array editable:false description:"Conversation"
func (*AIAgentFlow) GetDexDisplay(ctx dex.Context, _ dex.None) (*dex.RPCResult[map[string]any], error) {
	status, err := getAttributePointer(ctx, Status)
	if err != nil {
		return nil, err
	}
	pendingApproval, err := getAttributePointer(ctx, PendingApproval)
	if err != nil {
		return nil, err
	}
	pendingTimer, err := getAttributePointer(ctx, PendingTimer)
	if err != nil {
		return nil, err
	}
	pendingQuestion, err := getAttributePointer(ctx, PendingQuestion)
	if err != nil {
		return nil, err
	}
	lastModelFailure, err := getAttributePointer(ctx, LastModelFailure)
	if err != nil {
		return nil, err
	}
	messages, err := getAttributePointer(ctx, Messages)
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[map[string]any]{Output: map[string]any{
		"Status":           status,
		"PendingApproval":  pendingApproval,
		"PendingTimer":     pendingTimer,
		"PendingQuestion":  pendingQuestion,
		"LastModelFailure": lastModelFailure,
		"Messages":         messages,
	}}, nil
}

// dex:group group-id:conversation group-label:"Conversation"
// dex:explanation text:"Wait for the user's next message, which may answer the agent's question."
type AwaitUser struct {
	dex.StepDefaults
}

func (AwaitUser) WaitFor(ctx dex.Context, _ dex.None) (*dex.Wait, error) {
	_, isAsking, err := getOptionalAttribute(ctx, PendingQuestion)
	if err != nil {
		return nil, err
	}
	status := StatusWaitingForUser
	if isAsking {
		status = StatusWaitingForAnswer
	}
	if err := Status.Set(ctx, status); err != nil {
		return nil, err
	}
	return dex.Until(UserMessages.ForOne()), nil
}

func (AwaitUser) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	texts, err := UserMessages.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	if len(texts) != 1 {
		return nil, fmt.Errorf("expected one user message, got %d", len(texts))
	}
	question, isAsking, err := getOptionalAttribute(ctx, PendingQuestion)
	if err != nil {
		return nil, err
	}
	if isAsking {
		if err := PendingQuestion.Delete(ctx); err != nil {
			return nil, err
		}
		if err := finishToolCall(ctx, question.Call, OutcomeDone, "The user answered: "+texts[0]); err != nil {
			return nil, err
		}
		return dex.GoTo(RouteTool{}, nil), nil
	}
	if err := appendMessages(ctx, Message{Role: RoleUser, Content: texts[0], CreatedAt: time.Now().UTC()}); err != nil {
		return nil, err
	}
	if err := Status.Set(ctx, StatusCallingModel); err != nil {
		return nil, err
	}
	return dex.GoTo(CallModel{}, nil), nil
}

// dex:group group-id:model group-label:"Model"
// dex:explanation text:"Call the model with the stored conversation and record its reply."
type CallModel struct {
	dex.StepDefaultsNoWaitFor[dex.None]
	flow *AIAgentFlow
}

// A model call is silent until it returns. Dex still notices a killed Worker quickly.
func (CallModel) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteMethodTimeout: 2 * time.Minute,
		HeartbeatTimeout:     2 * time.Minute,
		ExecuteDurability:    dex.StepDurabilitySync,
		ExecuteRetry:         &dex.RetryPolicy{MaximumAttempts: 3, MaximumInterval: 5 * time.Second},
		ExecuteFailure:       dex.ProceedToOnExecuteFailure(RecordModelFailure{}, recoveryStepOptions),
	}
}

func (step CallModel) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	messages, _, err := getOptionalAttribute(ctx, Messages)
	if err != nil {
		return nil, err
	}
	reply, err := step.flow.model.Complete(ctx, ModelRequest{SystemPrompt: systemPrompt, Messages: messages, Tools: step.flow.modelToolDefinitions})
	if err != nil {
		return nil, err
	}
	reply.Role, reply.CreatedAt = RoleAssistant, time.Now().UTC()
	if err := appendMessages(ctx, reply); err != nil {
		return nil, err
	}
	if err := LastModelFailure.Delete(ctx); err != nil {
		return nil, err
	}
	if len(reply.ToolCalls) == 0 {
		return dex.GoTo(AwaitUser{}, nil), nil
	}
	if err := PendingToolCalls.Set(ctx, reply.ToolCalls); err != nil {
		return nil, err
	}
	return dex.GoTo(RouteTool{}, nil), nil
}

// dex:group group-id:model group-label:"Model"
// dex:explanation text:"Record a model call that failed after its retries, flag provider API errors, and wait for the user."
type RecordModelFailure struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (RecordModelFailure) GetStepOptions() *dex.StepOptions { return recoveryStepOptions }

func (RecordModelFailure) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	if err := LastModelFailure.Set(ctx, describeModelFailure(ctx.RecoveryError())); err != nil {
		return nil, err
	}
	return dex.GoTo(AwaitUser{}, nil), nil
}

// dex:group group-id:tools group-label:"Tools"
// dex:explanation text:"Send the next pending tool call to its wait, its approval, or its execution."
type RouteTool struct {
	dex.StepDefaultsNoWaitFor[dex.None]
}

func (RouteTool) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	calls, _, err := getOptionalAttribute(ctx, PendingToolCalls)
	if err != nil {
		return nil, err
	}
	if len(calls) == 0 {
		if err := Status.Set(ctx, StatusCallingModel); err != nil {
			return nil, err
		}
		return dex.GoTo(CallModel{}, nil), nil
	}
	call := calls[0]
	policy, isKnown := toolPolicies[call.Name]
	switch {
	case !isKnown:
		if err := finishToolCall(ctx, call, OutcomeFailed, fmt.Sprintf("There is no tool named %q.", call.Name)); err != nil {
			return nil, err
		}
		return dex.GoTo(RouteTool{}, nil), nil
	case call.Name == ToolAskUser:
		question, err := decodeToolArgument[string](call, "question")
		if err != nil {
			if err := finishToolCall(ctx, call, OutcomeFailed, err.Error()); err != nil {
				return nil, err
			}
			return dex.GoTo(RouteTool{}, nil), nil
		}
		if err := PendingQuestion.Set(ctx, UserQuestion{Call: call, Question: question}); err != nil {
			return nil, err
		}
		return dex.GoTo(AwaitUser{}, nil), nil
	case call.Name == ToolWait:
		seconds, err := decodeToolArgument[int](call, "seconds")
		if err == nil && (seconds < 1 || seconds > maxWaitSeconds) {
			err = fmt.Errorf("seconds must be between 1 and %d", maxWaitSeconds)
		}
		if err != nil {
			if err := finishToolCall(ctx, call, OutcomeFailed, err.Error()); err != nil {
				return nil, err
			}
			return dex.GoTo(RouteTool{}, nil), nil
		}
		firesAt := time.Now().UTC().Add(time.Duration(seconds) * time.Second)
		if err := PendingTimer.Set(ctx, TimerWait{CallID: call.ID, Seconds: seconds, FiresAt: firesAt}); err != nil {
			return nil, err
		}
		return dex.GoTo(AwaitTimer{}, call), nil
	case call.Name == ToolPostToSlack:
		if err := Status.Set(ctx, StatusExecutingTool); err != nil {
			return nil, err
		}
		return dex.GoTo(sdkgo.StepRef[ToolCall](postToSlackStepType), call), nil
	case policy.needsApproval:
		if err := PendingApproval.Set(ctx, call); err != nil {
			return nil, err
		}
		return dex.GoTo(AwaitToolApproval{}, call), nil
	}
	if err := Status.Set(ctx, StatusExecutingTool); err != nil {
		return nil, err
	}
	return dex.GoTo(ExecuteTool{}, call, dex.WithStepOptions(executeToolStepOptions(call))), nil
}

// dex:group group-id:tools group-label:"Tools"
// dex:explanation text:"Wait for the user to approve or decline a tool call that changes data."
type AwaitToolApproval struct {
	dex.StepDefaults
}

func (AwaitToolApproval) WaitFor(ctx dex.Context, call ToolCall) (*dex.Wait, error) {
	if err := Status.Set(ctx, StatusWaitingForApproval); err != nil {
		return nil, err
	}
	return dex.Until(ToolApprovals.ForOne(call.ID)), nil
}

func (AwaitToolApproval) Execute(ctx dex.Context, call ToolCall) (*dex.StepDecision, error) {
	decisions, err := ToolApprovals.GetConditionResults(ctx, call.ID)
	if err != nil {
		return nil, err
	}
	if len(decisions) != 1 {
		return nil, fmt.Errorf("expected one decision for call %s, got %d", call.ID, len(decisions))
	}
	if !decisions[0].IsApproved {
		if err := finishToolCall(ctx, call, OutcomeDeclined, "The user declined this call."); err != nil {
			return nil, err
		}
		return dex.GoTo(RouteTool{}, nil), nil
	}
	if err := Status.Set(ctx, StatusExecutingTool); err != nil {
		return nil, err
	}
	return dex.GoTo(ExecuteTool{}, call, dex.WithStepOptions(executeToolStepOptions(call))), nil
}

// dex:group group-id:tools group-label:"Tools"
// dex:explanation text:"Wait on a durable Timer for the wait tool, then record that the time passed."
type AwaitTimer struct {
	dex.StepDefaults
}

func (AwaitTimer) WaitFor(ctx dex.Context, _ ToolCall) (*dex.Wait, error) {
	timer, err := PendingTimer.Get(ctx)
	if err != nil {
		return nil, err
	}
	if err := Status.Set(ctx, StatusWaitingForTimer); err != nil {
		return nil, err
	}
	return dex.Until(dex.Timer(time.Duration(timer.Seconds) * time.Second)), nil
}

func (AwaitTimer) Execute(ctx dex.Context, call ToolCall) (*dex.StepDecision, error) {
	timer, err := PendingTimer.Get(ctx)
	if err != nil {
		return nil, err
	}
	if err := PendingTimer.Delete(ctx); err != nil {
		return nil, err
	}
	if err := finishToolCall(ctx, call, OutcomeDone, fmt.Sprintf("Waited %d seconds.", timer.Seconds)); err != nil {
		return nil, err
	}
	return dex.GoTo(RouteTool{}, nil), nil
}

// dex:group group-id:tools group-label:"Tools"
// dex:explanation text:"Run one fetch_page or publish_note call and record its result."
type ExecuteTool struct {
	dex.StepDefaultsNoWaitFor[ToolCall]
	flow *AIAgentFlow
}

// GetStepOptions runs a write once: it may have taken effect, so Dex never repeats it silently.
func (ExecuteTool) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteMethodTimeout: 2 * time.Minute,
		HeartbeatTimeout:     30 * time.Second,
		ExecuteDurability:    dex.StepDurabilitySync,
		ExecuteRetry:         &dex.RetryPolicy{MaximumAttempts: 1},
		ExecuteFailure:       dex.ProceedToOnExecuteFailure(RecordToolFailure{}, recoveryStepOptions),
	}
}

func (step ExecuteTool) Execute(ctx dex.Context, call ToolCall) (*dex.StepDecision, error) {
	result, err := step.flow.toolbox.RunTool(ctx, call, func(progress string) error {
		return ctx.RecordHeartbeat(progress)
	})
	if err != nil {
		return nil, dex.ErrorWithStack(fmt.Errorf("run %s call %s: %w", call.Name, call.ID, err))
	}
	if ctx.Attempt() > 1 {
		result += fmt.Sprintf("\n(Dex ran this read-only call again. Attempt %d finished it.)", ctx.Attempt())
	}
	if err := finishToolCall(ctx, call, OutcomeDone, result); err != nil {
		return nil, err
	}
	return dex.GoTo(RouteTool{}, nil), nil
}

// dex:group group-id:tools group-label:"Tools"
// dex:explanation text:"Record a tool call that failed after its retries so the model can decide what to do."
type RecordToolFailure struct {
	dex.StepDefaultsNoWaitFor[ToolCall]
}

func (RecordToolFailure) GetStepOptions() *dex.StepOptions { return recoveryStepOptions }

func (RecordToolFailure) Execute(ctx dex.Context, call ToolCall) (*dex.StepDecision, error) {
	detail := "no failure detail"
	if failure := ctx.RecoveryError(); failure != nil {
		detail = truncate(failure.Detail, maxRecoveryDetailLength)
	}
	outcome, content := OutcomeOutcomeUnknown, fmt.Sprintf(
		"%s stopped before it reported a result (%s). It changes data and may already have taken effect, "+
			"so check before trying again.", call.Name, detail)
	if toolPolicies[call.Name].isReadOnly {
		outcome, content = OutcomeFailed, fmt.Sprintf("%s failed after its retries: %s. It only reads, so trying again is safe.", call.Name, detail)
	}
	if err := finishToolCall(ctx, call, outcome, content); err != nil {
		return nil, err
	}
	return dex.GoTo(RouteTool{}, nil), nil
}

// dex:group group-id:tools group-label:"Tools"
// dex:explanation text:"Record the Slack connector's outcome as the result of the post_to_slack call."
type RecordSlackPost struct {
	dex.StepDefaultsNoWaitFor[slack.PostChannelMessageResult]
}

func (RecordSlackPost) Execute(ctx dex.Context, result slack.PostChannelMessageResult) (*dex.StepDecision, error) {
	calls, _, err := getOptionalAttribute(ctx, PendingToolCalls)
	if err != nil {
		return nil, err
	}
	if len(calls) == 0 || calls[0].Name != ToolPostToSlack {
		return nil, fmt.Errorf("no %s call is pending", ToolPostToSlack)
	}
	outcome, content := describeSlackPost(result)
	if err := finishToolCall(ctx, calls[0], outcome, content); err != nil {
		return nil, err
	}
	return dex.GoTo(RouteTool{}, nil), nil
}

// executeToolStepOptions lets Dex repeat a read-only call, for example after a crash.
func executeToolStepOptions(call ToolCall) *dex.StepOptions {
	options := ExecuteTool{}.GetStepOptions()
	if toolPolicies[call.Name].isReadOnly {
		options.ExecuteRetry = &dex.RetryPolicy{MaximumAttempts: 3, MaximumInterval: 5 * time.Second}
	}
	return options
}

// finishToolCall records the call's result and removes it from PendingToolCalls.
func finishToolCall(ctx dex.Context, call ToolCall, outcome, content string) error {
	calls, _, err := getOptionalAttribute(ctx, PendingToolCalls)
	if err != nil {
		return err
	}
	if len(calls) > 0 && calls[0].ID == call.ID {
		calls = calls[1:]
	}
	if err := PendingToolCalls.Set(ctx, calls); err != nil {
		return err
	}
	return appendMessages(ctx, Message{
		Role: RoleTool, ToolCallID: call.ID, ToolName: call.Name, Outcome: outcome, Content: content, CreatedAt: time.Now().UTC(),
	})
}

func appendMessages(ctx dex.Context, messages ...Message) error {
	conversation, _, err := getOptionalAttribute(ctx, Messages)
	if err != nil {
		return err
	}
	return Messages.Set(ctx, append(conversation, messages...))
}

func describeModelFailure(failure *dex.RecoveryErrorInfo) ModelFailure {
	report := ModelFailure{Kind: ModelFailureUnavailable, Detail: "no failure detail", FailedAt: time.Now().UTC()}
	if failure == nil {
		return report
	}
	report.Detail = truncate(failure.Detail, maxRecoveryDetailLength)
	switch {
	// Go SDK v1.25.0 reports every Step error type as *errors.joinError, so match the message.
	case strings.HasPrefix(failure.Detail, modelProviderErrorPrefix):
		report.Kind, report.IsProviderAPIError = ModelFailureProviderAPIError, true
	case failure.IsMethodTimeout() || failure.IsHeartbeatTimeout():
		report.Kind = ModelFailureTimeout
	}
	return report
}

func decodeToolArgument[T any](call ToolCall, name string) (T, error) {
	var arguments map[string]json.RawMessage
	var value T
	if err := json.Unmarshal([]byte(call.Arguments), &arguments); err != nil {
		return value, fmt.Errorf("%s arguments are not a JSON object: %w", call.Name, err)
	}
	if err := json.Unmarshal(arguments[name], &value); err != nil {
		return value, fmt.Errorf("%s needs a valid %q argument: %w", call.Name, name, err)
	}
	return value, nil
}

func getOptionalAttribute[T any](ctx dex.Context, attribute dex.Attribute[T]) (T, bool, error) {
	value, err := attribute.Get(ctx)
	var notFound *dex.AttributeNotFoundError
	if errors.As(err, &notFound) {
		return value, false, nil
	}
	return value, err == nil, err
}

func getAttributePointer[T any](ctx dex.Context, attribute dex.Attribute[T]) (*T, error) {
	value, isSet, err := getOptionalAttribute(ctx, attribute)
	if err != nil || !isSet {
		return nil, err
	}
	return &value, nil
}

var _ dex.Flow = (*AIAgentFlow)(nil)
