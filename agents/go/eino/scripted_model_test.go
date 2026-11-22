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
	"fmt"
	"sync"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"

	dexagents "github.com/superdurable/dex/agents/go/core"
	dexeino "github.com/superdurable/dex/agents/go/eino"
)

// scriptedChatModel is an Eino ToolCallingChatModel whose reply depends on its input only.
type scriptedChatModel struct {
	respond func(input []*schema.Message) (*schema.Message, error)
	chunks  func(message *schema.Message) []*schema.Message

	mutex     sync.Mutex
	inputs    [][]*schema.Message
	tools     [][]string
	toolInfos [][]*schema.ToolInfo
}

func (scripted *scriptedChatModel) record(input []*schema.Message, options []model.Option) {
	scripted.mutex.Lock()
	defer scripted.mutex.Unlock()
	scripted.inputs = append(scripted.inputs, input)
	var names []string
	infos := model.GetCommonOptions(&model.Options{}, options...).Tools
	for _, info := range infos {
		names = append(names, info.Name)
	}
	scripted.tools = append(scripted.tools, names)
	scripted.toolInfos = append(scripted.toolInfos, infos)
}

func (scripted *scriptedChatModel) calls() int {
	scripted.mutex.Lock()
	defer scripted.mutex.Unlock()
	return len(scripted.inputs)
}

func (scripted *scriptedChatModel) lastInput() []*schema.Message {
	scripted.mutex.Lock()
	defer scripted.mutex.Unlock()
	return scripted.inputs[len(scripted.inputs)-1]
}

func (scripted *scriptedChatModel) Generate(_ context.Context, input []*schema.Message, options ...model.Option) (*schema.Message, error) {
	scripted.record(input, options)
	return scripted.respond(input)
}

func (scripted *scriptedChatModel) Stream(_ context.Context, input []*schema.Message, options ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	scripted.record(input, options)
	message, err := scripted.respond(input)
	if err != nil {
		return nil, err
	}
	if scripted.chunks != nil {
		return schema.StreamReaderFromArray(scripted.chunks(message)), nil
	}
	return schema.StreamReaderFromArray([]*schema.Message{message}), nil
}

func (scripted *scriptedChatModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return scripted, nil
}

// funcTool is an invokable Eino tool backed by a function.
type funcTool struct {
	name string
	run  func(ctx context.Context, arguments string) (string, error)
}

func (function funcTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: function.name, Desc: "Test tool " + function.name,
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{"id": {Type: schema.String}})}, nil
}

func (function funcTool) InvokableRun(ctx context.Context, arguments string, _ ...tool.Option) (string, error) {
	return function.run(ctx, arguments)
}

func toolCallMessage(calls ...schema.ToolCall) *schema.Message {
	return schema.AssistantMessage("", calls)
}

func call(id, name, arguments string) schema.ToolCall {
	return schema.ToolCall{ID: id, Type: "function", Function: schema.FunctionCall{Name: name, Arguments: arguments}}
}

// turnState accumulates stored messages and records the way the session does, without a Dex Server.
type turnState struct {
	t        *testing.T
	turnID   string
	messages []dexagents.AgentMessage
	records  map[string]dexagents.AgentToolCall
	runState *string
	events   []dexagents.AgentEvent
	attempt  int64
}

func newTurnState(t *testing.T, runner *dexeino.Runner, userText string) *turnState {
	state := &turnState{t: t, turnID: "turn-1", records: map[string]dexagents.AgentToolCall{}, attempt: 1}
	user, err := runner.BuildUserMessage(userText)
	require.NoError(t, err)
	state.store(user)
	return state
}

func (state *turnState) store(messages ...dexagents.FrameworkMessage) {
	for _, message := range messages {
		state.messages = append(state.messages, dexagents.AgentMessage{
			Sequence: int64(len(state.messages) + 1), TurnID: state.turnID, Role: message.Role,
			Text: message.Text, ToolCallID: message.ToolCallID, ToolName: message.ToolName,
			ToolArguments: message.ToolArguments, ToolOutput: message.ToolOutput, FrameworkItem: message.FrameworkItem,
		})
	}
}

func (state *turnState) context() dexagents.AgentTurnContext {
	return dexagents.AgentTurnContext{
		FlowID: "session", TurnID: state.turnID, Attempt: state.attempt, Messages: state.messages,
		FrameworkRunState: state.runState, ToolCalls: state.records, ToolPolicies: map[string]dexagents.ToolPolicy{},
		DefaultToolEffect: dexagents.ToolEffectUnclassified, SessionSettings: map[string]any{},
		Events: dexagents.NewAgentEventWriter(func(event dexagents.AgentEvent) error {
			state.events = append(state.events, event)
			return nil
		}, &state.turnID, state.attempt, fmt.Sprintf("RunModel-%d", len(state.messages))),
	}
}

// runModel runs one RunModelStep and stores its outcome as the session would.
func (state *turnState) runModel(runner *dexeino.Runner) dexagents.AgentTurnOutcome {
	outcome, err := runner.RunModelStep(context.Background(), state.context())
	require.NoError(state.t, err)
	switch typed := outcome.(type) {
	case *dexagents.ToolCallsRequested:
		state.store(typed.NewMessages...)
		state.runState = &typed.FrameworkRunState
		for index, requested := range typed.ToolCalls {
			state.records[requested.ToolCallID] = dexagents.AgentToolCall{
				ToolCallID: requested.ToolCallID, TurnID: state.turnID, ToolName: requested.ToolName,
				Arguments: requested.Arguments, Effect: requested.Policy.Effect,
				RequiresApproval: requested.Policy.IsApprovalRequired(), Status: dexagents.ToolCallStatusApproved,
				DeferralOrder: int64(len(state.records) + index), ToolSourceReference: requested.ToolSourceReference,
			}
		}
	case *dexagents.TurnCompleted:
		state.store(typed.NewMessages...)
	}
	return outcome
}

// runTools runs every approved record through ExecuteDeferredToolCall and records its result.
func (state *turnState) runTools(runner *dexeino.Runner) map[string]error {
	failures := map[string]error{}
	for id, record := range state.records {
		if record.Status != dexagents.ToolCallStatusApproved {
			continue
		}
		outcome, err := runner.ExecuteDeferredToolCall(context.Background(), dexagents.DeferredToolExecution{
			FlowID: "session", ToolCall: record, IdempotencyKey: "session/" + state.turnID + "/" + id, Attempt: 1,
		})
		if err != nil {
			failures[id] = err
			record.Status = dexagents.ToolCallStatusFailed
			record.Error = ptr(err.Error())
		} else {
			record.Status = dexagents.ToolCallStatusSucceeded
			record.Output = ptr(outcome.(*dexagents.ToolExecutionResult).Output)
		}
		state.records[id] = record
	}
	return failures
}

func ptr[Value any](value Value) *Value {
	return &value
}

func decodeArguments(t *testing.T, arguments string) map[string]any {
	decoded := map[string]any{}
	require.NoError(t, json.Unmarshal([]byte(arguments), &decoded))
	return decoded
}
