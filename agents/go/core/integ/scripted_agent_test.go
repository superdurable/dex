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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/agents/go/core/agentstest"
	"github.com/superdurable/dex/agents/go/core/stepwise"
	"github.com/superdurable/dex/sdk-go/dex"
)

// The Flow types the scripted Worker serves.
const (
	scriptedAgentFlowType     = "ScriptedAgent"
	deadlineAgentFlowType     = "DeadlineAgent"
	checkpointAgentFlowType   = "CheckpointAgent"
	settingsAgentFlowType     = "SettingsAgent"
	chunkedAgentFlowType      = "ChunkedAgent"
	chunkedWindowSize         = 5
	chunkedChunkSize          = 2
	chunkedBytesAgentFlowType = "ChunkedBytesAgent"
	chunkedBytesChunkSize     = 4
	chunkedBytesMaxBytes      = 2000
	approvalDeadline          = 2 * time.Second
)

type scriptedAgentFlows map[string]*dexagents.AgentSessionFlow

func scriptedAgentFlowsByType(flows []dex.Flow) scriptedAgentFlows {
	byType := scriptedAgentFlows{}
	for _, flow := range flows {
		byType[flow.GetFlowType()] = flow.(*dexagents.AgentSessionFlow)
	}
	return byType
}

// newScriptedAgentFlows builds the Stepwise agents of the tests around one coordination directory.
func newScriptedAgentFlows(coordinationDir string) ([]dex.Flow, error) {
	tools := scriptedTools{coordinationDir: coordinationDir}
	lookupOrder, err := stepwise.NewFunctionTool("lookup_order", "Look up an order by its ID.",
		json.RawMessage(`{"type":"object","properties":{"order_id":{"type":"string"}},"required":["order_id"]}`),
		dexagents.ToolEffectRead, tools.lookupOrder)
	if err != nil {
		return nil, err
	}
	refundOrder, err := stepwise.NewFunctionTool("refund_order", "Refund an order.",
		json.RawMessage(`{"type":"object","properties":{"order_id":{"type":"string"}},"required":["order_id"]}`),
		dexagents.ToolEffectNonIdempotentWrite, tools.refundOrder)
	if err != nil {
		return nil, err
	}
	chargeCard, err := stepwise.NewFunctionTool("charge_card", "Charge the card on file.",
		json.RawMessage(`{"type":"object","properties":{"amount":{"type":"integer","minimum":1},"block":{"type":"boolean"}},"required":["amount"],"additionalProperties":false}`),
		dexagents.ToolEffectNonIdempotentWrite, tools.chargeCard, stepwise.WithRequiresApproval(false))
	if err != nil {
		return nil, err
	}
	script := scriptedConversation{coordinationDir: coordinationDir}
	newFlow := func(
		flowType string,
		sessionOptions []dexagents.AgentSessionOption,
		options ...stepwise.AgentDefinitionOption,
	) (dex.Flow, error) {
		definition, err := stepwise.NewAgentDefinition("scripted", "You are a scripted test agent.",
			slowFirstAttemptModelClient{scripted: agentstest.NewScriptedModelClient(script.respond), coordinationDir: coordinationDir},
			append([]stepwise.AgentDefinitionOption{stepwise.WithFunctionTools(lookupOrder, refundOrder, chargeCard)}, options...)...)
		if err != nil {
			return nil, err
		}
		return stepwise.NewStepwiseAgentSessionFlow(definition, flowType, sessionOptions...)
	}
	scripted, err := newFlow(scriptedAgentFlowType, nil)
	if err != nil {
		return nil, err
	}
	deadline, err := newFlow(deadlineAgentFlowType, nil, stepwise.WithInterruptDeadline(approvalDeadline))
	if err != nil {
		return nil, err
	}
	chunked, err := newFlow(chunkedAgentFlowType, []dexagents.AgentSessionOption{
		dexagents.WithMessageWindowSize(chunkedWindowSize), dexagents.WithMessageChunkSize(chunkedChunkSize),
	})
	if err != nil {
		return nil, err
	}
	chunkedBytes, err := newFlow(chunkedBytesAgentFlowType, []dexagents.AgentSessionOption{
		dexagents.WithMessageWindowSize(chunkedWindowSize), dexagents.WithMessageChunkSize(chunkedBytesChunkSize),
		dexagents.WithMessageChunkMaxBytes(chunkedBytesMaxBytes),
	})
	if err != nil {
		return nil, err
	}
	checkpointTool, err := stepwise.NewFunctionTool("checkpointed_job", "Run a job that resumes from a checkpoint.",
		json.RawMessage(`{"type":"object"}`), dexagents.ToolEffectRead,
		func(context.Context, json.RawMessage, stepwise.ToolCallContext) (string, error) {
			return "", errors.New("checkpointRunner runs this tool")
		})
	if err != nil {
		return nil, err
	}
	checkpointDefinition, err := stepwise.NewAgentDefinition("checkpoint", "You run jobs.",
		agentstest.NewScriptedModelClient(script.respond), stepwise.WithFunctionTools(checkpointTool))
	if err != nil {
		return nil, err
	}
	checkpoint, err := dexagents.NewAgentSessionFlow(
		checkpointRunner{StepwiseAgentRunner: stepwise.NewStepwiseAgentRunner(checkpointDefinition), coordinationDir: coordinationDir},
		checkpointAgentFlowType)
	if err != nil {
		return nil, err
	}
	settings, err := newSettingsAgentFlow(script, coordinationDir)
	if err != nil {
		return nil, err
	}
	return []dex.Flow{scripted, deadline, checkpoint, chunked, chunkedBytes, settings}, nil
}

// newSettingsAgentFlow builds an agent whose model resolver picks the model the session settings name.
//
// The models are "default", "alpha", and "beta". "which-model" answers "model <name>: <instructions>",
// and the first attempt of "think slowly" blocks after it wrote marker "settings-model-attempt-1".
func newSettingsAgentFlow(script scriptedConversation, coordinationDir string) (dex.Flow, error) {
	models := map[string]stepwise.ModelClient{}
	for _, name := range []string{"default", "alpha", "beta"} {
		models[name] = namedModelClient{
			name: name, scripted: agentstest.NewScriptedModelClient(script.respond), coordinationDir: coordinationDir,
		}
	}
	resolveModel := func(sessionSettings map[string]any) (stepwise.ModelClient, error) {
		name, hasName := sessionSettings["model"].(string)
		if !hasName {
			name = "default"
		}
		model, isAllowed := models[name]
		if !isAllowed {
			return nil, fmt.Errorf("model %q is not allowed", name)
		}
		return model, nil
	}
	instructions := func(instructionContext stepwise.InstructionContext) (string, error) {
		if persona, hasPersona := instructionContext.SessionSettings["persona"]; hasPersona {
			return fmt.Sprintf("You are the settings agent. Persona: %v.", persona), nil
		}
		return "You are the settings agent.", nil
	}
	definition, err := stepwise.NewAgentDefinition("settings", "unused", models["default"],
		stepwise.WithModelResolver(resolveModel), stepwise.WithInstructionsFunction(instructions))
	if err != nil {
		return nil, err
	}
	return stepwise.NewStepwiseAgentSessionFlow(definition, settingsAgentFlowType)
}

// namedModelClient answers "which-model" and "think slowly" with its own name.
type namedModelClient struct {
	name            string
	scripted        *agentstest.ScriptedModelClient
	coordinationDir string
}

func (client namedModelClient) CallModel(
	ctx context.Context,
	request stepwise.ModelRequest,
	output stepwise.ModelOutputWriter,
) (stepwise.ModelResponse, error) {
	userTexts := agentstest.UserTexts(request)
	switch lastText := userTexts[len(userTexts)-1]; {
	case lastText == "which-model":
		return client.reply(ctx, request, output, fmt.Sprintf("model %s: %s", client.name, request.Instructions))
	case lastText == "think slowly" && request.Metadata.Attempt == 1:
		if err := output.WriteTextDelta("thinking"); err != nil {
			return stepwise.ModelResponse{}, err
		}
		writeMarker(client.coordinationDir, "settings-model-attempt-1")
		// The test SIGKILLs this Worker while the model call runs.
		<-ctx.Done()
		return stepwise.ModelResponse{}, ctx.Err()
	case lastText == "think slowly":
		return client.reply(ctx, request, output,
			fmt.Sprintf("answered on attempt %d by %s", request.Metadata.Attempt, client.name))
	default:
		return client.scripted.CallModel(ctx, request, output)
	}
}

func (client namedModelClient) reply(
	ctx context.Context,
	request stepwise.ModelRequest,
	output stepwise.ModelOutputWriter,
	text string,
) (stepwise.ModelResponse, error) {
	answer := agentstest.NewScriptedModelClient(func(stepwise.ModelRequest) (agentstest.ScriptedModelReply, error) {
		return agentstest.ScriptedModelReply{Text: text}, nil
	})
	return answer.CallModel(ctx, request, output)
}

func (client namedModelClient) EstimateInputTokens(request stepwise.ModelRequest) int64 {
	return client.scripted.EstimateInputTokens(request)
}

// checkpointRunner saves a heartbeat checkpoint on attempt 1, fails attempts 1 and 2, and records
// what each later attempt read, so a test sees whether attempt 2 carried the checkpoint forward.
type checkpointRunner struct {
	*stepwise.StepwiseAgentRunner
	coordinationDir string
}

func (runner checkpointRunner) ExecuteDeferredToolCall(
	ctx context.Context,
	call dexagents.DeferredToolExecution,
) (dexagents.DeferredToolOutcome, error) {
	if call.Attempt == 1 {
		if err := call.SaveHeartbeatCheckpoint("job-1"); err != nil {
			return nil, err
		}
		return nil, errors.New("the first attempt stops after it saved its checkpoint")
	}
	seen := "none"
	if call.HeartbeatCheckpoint != nil {
		seen = *call.HeartbeatCheckpoint
	}
	if err := (scriptedTools{coordinationDir: runner.coordinationDir}).recordCall("checkpoint-" + seen); err != nil {
		return nil, err
	}
	if call.Attempt == 2 {
		if err := call.Heartbeat(); err != nil {
			return nil, err
		}
		return nil, errors.New("the second attempt stops after a heartbeat")
	}
	return &dexagents.ToolExecutionResult{Output: "resumed " + seen}, nil
}

// scriptedConversation answers by the last user text, so a reply depends on committed state only.
type scriptedConversation struct {
	coordinationDir string
}

func (script scriptedConversation) respond(request stepwise.ModelRequest) (agentstest.ScriptedModelReply, error) {
	userTexts := agentstest.UserTexts(request)
	lastText := userTexts[len(userTexts)-1]
	results := agentstest.ToolResults(request)
	outputs := make([]string, 0, len(results))
	for _, result := range results {
		outputs = append(outputs, result.Output)
	}
	switch {
	case lastText == "lookup and refund":
		if len(results) == 0 {
			return toolCalls(
				agentstest.ScriptedToolCall{ToolName: "lookup_order", Arguments: `{"order_id": "1"}`},
				agentstest.ScriptedToolCall{ToolName: "refund_order", Arguments: `{"order_id": "1"}`},
			), nil
		}
		return agentstest.ScriptedModelReply{Text: "Done: " + strings.Join(outputs, "; ")}, nil
	case strings.HasPrefix(lastText, "lookup "):
		if len(results) == 0 {
			return toolCalls(agentstest.ScriptedToolCall{
				ToolName: "lookup_order", Arguments: fmt.Sprintf(`{"order_id": %q}`, strings.TrimPrefix(lastText, "lookup ")),
			}), nil
		}
		return agentstest.ScriptedModelReply{Text: "Order status: " + outputs[0]}, nil
	case strings.HasPrefix(lastText, "refund "):
		if len(results) == 0 {
			return toolCalls(agentstest.ScriptedToolCall{
				ToolName: "refund_order", Arguments: fmt.Sprintf(`{"order_id": %q}`, strings.TrimPrefix(lastText, "refund ")),
			}), nil
		}
		return agentstest.ScriptedModelReply{Text: "Refund result: " + outputs[0]}, nil
	case lastText == "run the job":
		if len(results) == 0 {
			return toolCalls(agentstest.ScriptedToolCall{ToolName: "checkpointed_job"}), nil
		}
		return agentstest.ScriptedModelReply{Text: "Job: " + outputs[0]}, nil
	case lastText == "charge and fail":
		if len(results) == 0 {
			return toolCalls(agentstest.ScriptedToolCall{ToolName: "charge_card", Arguments: `{"amount": 5}`}), nil
		}
		return agentstest.ScriptedModelReply{}, errors.New("the scripted model is down")
	case lastText == "charge slowly":
		if len(results) == 0 {
			return toolCalls(agentstest.ScriptedToolCall{ToolName: "charge_card", Arguments: `{"amount": 7, "block": true}`}), nil
		}
		return agentstest.ScriptedModelReply{Text: "Charge result: " + outputs[0]}, nil
	case strings.HasPrefix(lastText, "pad "):
		characters, err := strconv.Atoi(strings.TrimPrefix(lastText, "pad "))
		if err != nil {
			return agentstest.ScriptedModelReply{}, err
		}
		return agentstest.ScriptedModelReply{Text: strings.Repeat("x", characters)}, nil
	case lastText == "count user texts":
		return agentstest.ScriptedModelReply{Text: fmt.Sprintf("I see %d user texts", len(userTexts))}, nil
	case lastText == "think slowly":
		return agentstest.ScriptedModelReply{Text: fmt.Sprintf("answered on attempt %d", request.Metadata.Attempt)}, nil
	default:
		for _, text := range userTexts {
			if strings.HasPrefix(text, dexagents.TurnFailureNoteHeader) {
				return agentstest.ScriptedModelReply{Text: "I see the failure note"}, nil
			}
		}
		return agentstest.ScriptedModelReply{Text: "You said: " + lastText}, nil
	}
}

func toolCalls(calls ...agentstest.ScriptedToolCall) agentstest.ScriptedModelReply {
	return agentstest.ScriptedModelReply{ToolCalls: calls}
}

// scriptedTools record every call in "<tool>.calls" so tests count how often a tool ran.
type scriptedTools struct {
	coordinationDir string
}

func (tools scriptedTools) lookupOrder(_ context.Context, arguments json.RawMessage, _ stepwise.ToolCallContext) (string, error) {
	var input struct {
		OrderID string `json:"order_id"`
	}
	if err := json.Unmarshal(arguments, &input); err != nil {
		return "", err
	}
	if err := tools.recordCall("lookup_order"); err != nil {
		return "", err
	}
	return fmt.Sprintf("order %s is shipped", input.OrderID), nil
}

func (tools scriptedTools) refundOrder(_ context.Context, arguments json.RawMessage, call stepwise.ToolCallContext) (string, error) {
	var input struct {
		OrderID string `json:"order_id"`
	}
	if err := json.Unmarshal(arguments, &input); err != nil {
		return "", err
	}
	if err := tools.recordCall("refund_order"); err != nil {
		return "", err
	}
	return fmt.Sprintf("refunded %s with key %s", input.OrderID, call.IdempotencyKey), nil
}

func (tools scriptedTools) chargeCard(ctx context.Context, arguments json.RawMessage, _ stepwise.ToolCallContext) (string, error) {
	var input struct {
		Amount  int  `json:"amount"`
		IsBlock bool `json:"block"`
	}
	if err := json.Unmarshal(arguments, &input); err != nil {
		return "", err
	}
	if err := tools.recordCall("charge_card"); err != nil {
		return "", err
	}
	if input.IsBlock {
		writeMarker(tools.coordinationDir, "charge_card.started")
		// The test SIGKILLs this Worker while the charge runs.
		<-ctx.Done()
		return "", ctx.Err()
	}
	return fmt.Sprintf("charged %d", input.Amount), nil
}

func (tools scriptedTools) recordCall(toolName string) error {
	file, err := os.OpenFile(filepath.Join(tools.coordinationDir, toolName+".calls"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	if _, err := file.WriteString(toolName + "\n"); err != nil {
		return errors.Join(err, file.Close())
	}
	return file.Close()
}

func writeMarker(coordinationDir string, name string) {
	if err := os.WriteFile(filepath.Join(coordinationDir, name), nil, 0o644); err != nil {
		panic(fmt.Sprintf("write coordination marker %s: %v", name, err))
	}
}

// slowFirstAttemptModelClient blocks the first "think slowly" attempt after it streamed output.
//
// Output first matters: Dex redelivers an attempt that wrote nothing at the same attempt number.
type slowFirstAttemptModelClient struct {
	scripted        *agentstest.ScriptedModelClient
	coordinationDir string
}

func (client slowFirstAttemptModelClient) CallModel(
	ctx context.Context,
	request stepwise.ModelRequest,
	output stepwise.ModelOutputWriter,
) (stepwise.ModelResponse, error) {
	userTexts := agentstest.UserTexts(request)
	if userTexts[len(userTexts)-1] == "think slowly" && request.Metadata.Attempt == 1 {
		if err := output.WriteTextDelta("thinking"); err != nil {
			return stepwise.ModelResponse{}, err
		}
		writeMarker(client.coordinationDir, "model-attempt-1")
		// The test SIGKILLs this Worker while the model call runs.
		<-ctx.Done()
		return stepwise.ModelResponse{}, ctx.Err()
	}
	return client.scripted.CallModel(ctx, request, output)
}

func (client slowFirstAttemptModelClient) EstimateInputTokens(request stepwise.ModelRequest) int64 {
	return client.scripted.EstimateInputTokens(request)
}

// newTypingOnlyFlow builds a Flow of another core's Flow type; a Client uses it only to type calls.
func newTypingOnlyFlow(flowType string) (*dexagents.AgentSessionFlow, error) {
	definition, err := stepwise.NewAgentDefinition("typing-only", "unused",
		agentstest.NewScriptedModelClient(func(stepwise.ModelRequest) (agentstest.ScriptedModelReply, error) {
			return agentstest.ScriptedModelReply{}, nil
		}))
	if err != nil {
		return nil, err
	}
	return stepwise.NewStepwiseAgentSessionFlow(definition, flowType)
}
