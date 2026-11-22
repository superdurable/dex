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
	"strings"
	"time"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	dexagents "github.com/superdurable/dex/agents/go/core"
	dexeino "github.com/superdurable/dex/agents/go/eino"
	"github.com/superdurable/dex/sdk-go/dex"
)

// The Flow types the Eino test Worker serves.
const (
	einoAgentFlowType          = "EinoAgent"
	einoStreamingAgentFlowType = "EinoStreamingAgent"
	einoDeadlineAgentFlowType  = "EinoDeadlineAgent"
	einoSettingsAgentFlowType  = "EinoSettingsAgent"
	einoOpenAIAgentFlowType    = "EinoOpenAIAgent"
	einoConnectorAgentFlowType = "EinoConnectorAgent"
	einoGuardedAgentFlowType   = "EinoGuardedAgent"
	approvalDeadline           = 2 * time.Second
	instructionV1              = "You are the Eino test agent."
	instructionV2              = "You are the Eino test agent, version 2."
)

// agentDeploy is what a Worker's deploy changed: the idempotent_write tool and the instruction.
type agentDeploy string

const (
	// deployOriginal registers issue_refund and the first instruction.
	deployOriginal agentDeploy = ""
	// deployRenamed registers issue_refund as refund_payment.
	deployRenamed agentDeploy = "renamed"
	// deployRemoved drops issue_refund.
	deployRemoved agentDeploy = "removed"
	// deployInstructionV2 changes the instruction.
	deployInstructionV2 agentDeploy = "instruction_v2"
	// deployParametersRenamed makes issue_refund an Eino typed tool whose required parameter is order_number.
	deployParametersRenamed agentDeploy = "parameters_renamed"
)

// newEinoAgentFlows builds every test Flow around one coordination directory.
func newEinoAgentFlows(coordinationDir string, deploy agentDeploy, openAIBaseURL string) ([]dex.Flow, error) {
	coordination := coordinationFiles{dir: coordinationDir}
	instruction := instructionV1
	if deploy == deployInstructionV2 {
		instruction = instructionV2
	}
	tools, err := scriptedTools(coordination, deploy)
	if err != nil {
		return nil, err
	}
	conversation := &scriptedChatModel{coordination: coordination}
	newConfig := func(chatModel model.ToolCallingChatModel) *adk.ChatModelAgentConfig {
		return &adk.ChatModelAgentConfig{
			Name: "eino_test_agent", Description: "Answers order questions.", Instruction: instruction, Model: chatModel,
			ToolsConfig: adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools:               tools,
				ToolCallMiddlewares: []compose.ToolMiddleware{coordination.countingMiddleware("middleware")},
			}},
			Handlers: []adk.ChatModelAgentMiddleware{&countingHandler{BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{},
				coordination: coordination}},
		}
	}
	options := []dexeino.Option{
		dexeino.WithToolEffects(map[string]dexagents.ToolEffect{
			"lookup_order": dexagents.ToolEffectRead, "refund_order": dexagents.ToolEffectNonIdempotentWrite,
			"issue_refund": dexagents.ToolEffectIdempotentWrite, "refund_payment": dexagents.ToolEffectIdempotentWrite,
			"slow_read": dexagents.ToolEffectRead, "blocking_read": dexagents.ToolEffectRead, "echo_call": dexagents.ToolEffectRead,
			"typed_failure": dexagents.ToolEffectRead,
		}),
		dexeino.WithToolPolicies(map[string]dexagents.ToolPolicy{
			"charge_card":   {Effect: dexagents.ToolEffectNonIdempotentWrite, RequiresApproval: ptr(false)},
			"plain_failure": {Effect: dexagents.ToolEffectNonIdempotentWrite, RequiresApproval: ptr(false)},
		}),
	}
	scripted, err := dexeino.NewAgentSessionFlow(newConfig(conversation), einoAgentFlowType, options...)
	if err != nil {
		return nil, err
	}
	streaming, err := dexeino.NewAgentSessionFlow(newConfig(conversation), einoStreamingAgentFlowType,
		append(options, dexeino.WithStreaming(true))...)
	if err != nil {
		return nil, err
	}
	deadline, err := dexeino.NewAgentSessionFlow(newConfig(conversation), einoDeadlineAgentFlowType,
		append(options, dexeino.WithInterruptDeadline(approvalDeadline))...)
	if err != nil {
		return nil, err
	}
	settings, err := newSettingsAgentFlow()
	if err != nil {
		return nil, err
	}
	openAI, err := newOpenAIAgentFlow(openAIBaseURL)
	if err != nil {
		return nil, err
	}
	ledger, err := newLedgerToolSource(coordination)
	if err != nil {
		return nil, err
	}
	connector, err := dexeino.NewAgentSessionFlow(newConfig(conversation), einoConnectorAgentFlowType,
		append(options, dexeino.WithToolSources(ledger))...)
	if err != nil {
		return nil, err
	}
	guardedConfig := newConfig(conversation)
	guardedConfig.MaxIterations = 1
	guardedConfig.Handlers = append(guardedConfig.Handlers, &doubleCallingHandler{BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{}})
	guarded, err := dexeino.NewAgentSessionFlow(guardedConfig, einoGuardedAgentFlowType, options...)
	if err != nil {
		return nil, err
	}
	return []dex.Flow{scripted, streaming, deadline, settings, openAI, connector, guarded}, nil
}

// newSettingsAgentFlow builds an agent whose builder picks the model the session settings name.
func newSettingsAgentFlow() (*dexagents.AgentSessionFlow, error) {
	models := map[string]model.ToolCallingChatModel{}
	for _, name := range []string{"default", "alpha", "beta"} {
		modelName := name
		models[name] = &namedChatModel{name: modelName}
	}
	return dexeino.NewAgentSessionFlowWithBuilder(func(_ context.Context, build dexeino.AgentBuildContext) (*adk.ChatModelAgentConfig, error) {
		name, hasName := build.SessionSettings["model"].(string)
		if !hasName {
			name = "default"
		}
		chosen, isAllowed := models[name]
		if !isAllowed {
			return nil, fmt.Errorf("model %q is not allowed", name)
		}
		return &adk.ChatModelAgentConfig{Name: "settings", Description: "Names its model.",
			Instruction: "You are the settings agent.", Model: chosen}, nil
	}, einoSettingsAgentFlowType)
}

// newOpenAIAgentFlow builds an agent on the eino-ext OpenAI ChatModel, against the test's fake server.
func newOpenAIAgentFlow(baseURL string) (*dexagents.AgentSessionFlow, error) {
	if baseURL == "" {
		baseURL = "http://127.0.0.1:1/v1"
	}
	chatModel, err := openai.NewChatModel(context.Background(), &openai.ChatModelConfig{
		BaseURL: baseURL, APIKey: "test-key", Model: "gpt-test", Timeout: 10 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	return dexeino.NewAgentSessionFlow(&adk.ChatModelAgentConfig{Name: "openai_agent", Description: "Talks to OpenAI.",
		Instruction: "You are the OpenAI test agent.", Model: chatModel}, einoOpenAIAgentFlowType,
		dexeino.WithModelRetryPolicy(dexagents.ModelRetryPolicy{
			InitialInterval: time.Second, MaximumInterval: time.Second, TotalDuration: 10 * time.Second,
		}))
}

// scriptedTools returns the tools of the test agent as deploy leaves them.
func scriptedTools(coordination coordinationFiles, deploy agentDeploy) ([]tool.BaseTool, error) {
	tools := []tool.BaseTool{
		coordination.tool("lookup_order", func(ctx context.Context, arguments map[string]any) (string, error) {
			return fmt.Sprintf("order %v is shipped", arguments["order_id"]), nil
		}),
		coordination.tool("refund_order", func(ctx context.Context, arguments map[string]any) (string, error) {
			toolCall, _ := dexeino.ToolCallContextFrom(ctx)
			return fmt.Sprintf("refunded %v with key %s", arguments["order_id"], toolCall.IdempotencyKey), nil
		}),
		coordination.tool("charge_card", func(ctx context.Context, arguments map[string]any) (string, error) {
			if arguments["block"] == true {
				coordination.writeMarker("charge_card.started")
				<-ctx.Done()
				return "", ctx.Err()
			}
			return fmt.Sprintf("charged %v", arguments["amount"]), nil
		}),
		coordination.tool("slow_read", func(ctx context.Context, arguments map[string]any) (string, error) {
			name := fmt.Sprint(arguments["name"])
			coordination.writeMarker(name + ".started")
			if err := coordination.waitForRelease(ctx, name); err != nil {
				return "", err
			}
			return "read " + name, nil
		}),
		coordination.tool("blocking_read", func(ctx context.Context, _ map[string]any) (string, error) {
			if !coordination.hasMarker("blocking_read.started") {
				coordination.writeMarker("blocking_read.started")
				<-ctx.Done()
				return "", ctx.Err()
			}
			return "read after the restart", nil
		}),
		coordination.tool("echo_call", func(ctx context.Context, _ map[string]any) (string, error) {
			toolCall, isToolStep := dexeino.ToolCallContextFrom(ctx)
			if !isToolStep {
				return "", errors.New("echo_call ran outside a tool Step")
			}
			return fmt.Sprintf("id=%s key=%s", compose.GetToolCallID(ctx), toolCall.IdempotencyKey), nil
		}),
		coordination.tool("typed_failure", func(context.Context, map[string]any) (string, error) {
			return "", fmt.Errorf("order service: %w", &dexagents.ToolExecutionError{Message: "order 9 is already shipped"})
		}),
		coordination.tool("plain_failure", func(context.Context, map[string]any) (string, error) {
			return "", errors.New("connection reset by peer")
		}),
	}
	refund := coordination.tool("issue_refund", func(ctx context.Context, _ map[string]any) (string, error) {
		coordination.writeMarker("issue_refund.started")
		<-ctx.Done()
		return "", ctx.Err()
	})
	switch deploy {
	case deployOriginal, deployInstructionV2:
		tools = append(tools, refund)
	case deployRenamed:
		refund.name = "refund_payment"
		tools = append(tools, refund)
	case deployParametersRenamed:
		renamed, err := utils.InferTool("issue_refund", "Refunds an order.", func(_ context.Context, arguments renamedRefundArguments) (string, error) {
			coordination.recordCall("issue_refund")
			return fmt.Sprintf("refunded order %q", arguments.OrderNumber), nil
		})
		if err != nil {
			return nil, err
		}
		tools = append(tools, renamed)
	case deployRemoved:
	default:
		return nil, fmt.Errorf("unknown deploy %q", deploy)
	}
	return tools, nil
}

// renamedRefundArguments are issue_refund's arguments after deployParametersRenamed.
type renamedRefundArguments struct {
	OrderNumber string `json:"order_number" jsonschema:"required"`
}

// coordinationFiles are the files a test and its Worker children share.
type coordinationFiles struct {
	dir string
}

func (coordination coordinationFiles) tool(name string, run func(ctx context.Context, arguments map[string]any) (string, error)) *recordedTool {
	return &recordedTool{name: name, run: run, coordination: coordination}
}

func (coordination coordinationFiles) recordCall(name string) {
	file, err := os.OpenFile(filepath.Join(coordination.dir, name+".calls"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		panic(err)
	}
	_, _ = file.WriteString(name + "\n")
	_ = file.Close()
}

func (coordination coordinationFiles) writeMarker(name string) {
	if err := os.WriteFile(filepath.Join(coordination.dir, name), nil, 0o644); err != nil {
		panic(err)
	}
}

func (coordination coordinationFiles) hasMarker(name string) bool {
	_, err := os.Stat(filepath.Join(coordination.dir, name))
	return err == nil
}

func (coordination coordinationFiles) waitForRelease(ctx context.Context, name string) error {
	deadline := time.Now().Add(coordinationTimeout)
	for time.Now().Before(deadline) {
		if coordination.hasMarker(name + ".release") {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(coordinationPollInterval):
		}
	}
	return fmt.Errorf("the test never released %s", name)
}

func (coordination coordinationFiles) countingMiddleware(name string) compose.ToolMiddleware {
	return compose.ToolMiddleware{Invokable: func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
		return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
			coordination.recordCall(name)
			return next(ctx, input)
		}
	}}
}

// recordedTool is an invokable Eino tool that counts its runs in "<name>.calls".
type recordedTool struct {
	name         string
	run          func(ctx context.Context, arguments map[string]any) (string, error)
	coordination coordinationFiles
}

func (recorded *recordedTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: recorded.name, Desc: "Test tool " + recorded.name,
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{"order_id": {Type: schema.String}})}, nil
}

func (recorded *recordedTool) InvokableRun(ctx context.Context, arguments string, _ ...tool.Option) (string, error) {
	decoded := map[string]any{}
	if err := json.Unmarshal([]byte(arguments), &decoded); err != nil {
		return "", err
	}
	recorded.coordination.recordCall(recorded.name)
	return recorded.run(ctx, decoded)
}

// countingHandler counts the Handler tool wrapper runs, which belong in the tool Step.
type countingHandler struct {
	*adk.BaseChatModelAgentMiddleware
	coordination coordinationFiles
}

func (handler *countingHandler) WrapInvokableToolCall(_ context.Context, endpoint adk.InvokableToolCallEndpoint, _ *adk.ToolContext) (adk.InvokableToolCallEndpoint, error) {
	return func(ctx context.Context, arguments string, options ...tool.Option) (string, error) {
		handler.coordination.recordCall("handler")
		return endpoint(ctx, arguments, options...)
	}, nil
}

// scriptedChatModel answers by the last user text and the results after it, so a reply depends on its input only.
type scriptedChatModel struct {
	coordination coordinationFiles
}

func (scripted *scriptedChatModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return scripted, nil
}

func (scripted *scriptedChatModel) Generate(ctx context.Context, input []*schema.Message, options ...model.Option) (*schema.Message, error) {
	scripted.coordination.recordCall("model")
	scripted.recordOfferedTools(options)
	return scripted.respond(ctx, input)
}

// recordOfferedTools writes the names of the tools this model call offers to "offered-tools".
func (scripted *scriptedChatModel) recordOfferedTools(options []model.Option) {
	names := []string{}
	for _, info := range model.GetCommonOptions(&model.Options{}, options...).Tools {
		names = append(names, info.Name)
	}
	if err := os.WriteFile(filepath.Join(scripted.coordination.dir, "offered-tools"), []byte(strings.Join(names, ",")), 0o644); err != nil {
		panic(err)
	}
}

func (scripted *scriptedChatModel) Stream(ctx context.Context, input []*schema.Message, options ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	scripted.coordination.recordCall("model")
	scripted.recordOfferedTools(options)
	lastUser, _, _ := conversationTail(input)
	if lastUser == "think slowly" && !scripted.coordination.hasMarker("model-attempt-1") {
		reader, writer := schema.Pipe[*schema.Message](1)
		go func() {
			defer writer.Close()
			writer.Send(&schema.Message{Role: schema.Assistant, Content: "thinking"}, nil)
			scripted.coordination.writeMarker("model-attempt-1")
			// The test SIGKILLs this Worker while the model call streams.
			<-ctx.Done()
			writer.Send(nil, ctx.Err())
		}()
		return reader, nil
	}
	message, err := scripted.respond(ctx, input)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{message}), nil
}

// conversationTail returns the last user text, the tool results after it, and every user text.
func conversationTail(input []*schema.Message) (string, []*schema.Message, []string) {
	lastUser := ""
	var results []*schema.Message
	var userTexts []string
	for _, message := range input {
		switch message.Role {
		case schema.User:
			lastUser, results = message.Content, nil
			userTexts = append(userTexts, message.Content)
		case schema.Tool:
			results = append(results, message)
		}
	}
	return lastUser, results, userTexts
}

func (scripted *scriptedChatModel) respond(ctx context.Context, input []*schema.Message) (*schema.Message, error) {
	lastUser, results, userTexts := conversationTail(input)
	outputs := make([]string, 0, len(results))
	for _, result := range results {
		outputs = append(outputs, result.Content)
	}
	calls := func(toolCalls ...schema.ToolCall) (*schema.Message, error) {
		return withUsage(schema.AssistantMessage("", toolCalls)), nil
	}
	answer := func(text string) (*schema.Message, error) {
		return withUsage(schema.AssistantMessage(text, nil)), nil
	}
	isFirst := len(results) == 0
	switch {
	case lastUser == "lookup and refund":
		if isFirst {
			return calls(toolCall("call_lookup", "lookup_order", `{"order_id":"1"}`), toolCall("call_refund", "refund_order", `{"order_id":"1"}`))
		}
		return answer("Done: " + strings.Join(outputs, "; "))
	case strings.HasPrefix(lastUser, "lookup "):
		if isFirst {
			return calls(toolCall("", "lookup_order", fmt.Sprintf(`{"order_id":%q}`, strings.TrimPrefix(lastUser, "lookup "))))
		}
		return answer("Order status: " + outputs[0])
	case strings.HasPrefix(lastUser, "refund "):
		if isFirst {
			return calls(toolCall("call_refund", "refund_order", fmt.Sprintf(`{"order_id":%q}`, strings.TrimPrefix(lastUser, "refund "))))
		}
		return answer("Refund result: " + outputs[0])
	case lastUser == "charge slowly":
		if isFirst {
			return calls(toolCall("call_charge", "charge_card", `{"amount":7,"block":true}`))
		}
		return answer("Charge result: " + outputs[0])
	case lastUser == "charge and fail":
		if isFirst {
			return calls(toolCall("call_charge", "charge_card", `{"amount":5}`))
		}
		return nil, errors.New("the scripted model is down")
	case strings.HasPrefix(lastUser, "idempotent refund "):
		gate, orderID, _ := strings.Cut(strings.TrimPrefix(lastUser, "idempotent refund "), " ")
		if isFirst {
			return calls(toolCall("call_issue", "issue_refund", fmt.Sprintf(`{"order_id":%q}`, orderID)))
		}
		if gate != "-" {
			scripted.coordination.writeMarker(gate + ".reached")
			if err := scripted.coordination.waitForRelease(ctx, gate); err != nil {
				return nil, err
			}
		}
		return answer("Refund result: " + outputs[0])
	case lastUser == "parallel reads":
		if isFirst {
			return calls(toolCall("call_a", "slow_read", `{"name":"read-a"}`), toolCall("call_b", "slow_read", `{"name":"read-b"}`))
		}
		return answer("Parallel: " + strings.Join(outputs, "; "))
	case lastUser == "instruction check":
		if isFirst {
			return calls(toolCall("call_blocking", "blocking_read", `{}`))
		}
		return answer("Instruction: " + input[0].Content + "; " + outputs[0])
	case lastUser == "numeric order id":
		if isFirst {
			return calls(toolCall("call_lookup", "lookup_order", `{"order_id":5}`))
		}
		return answer("Lookup: " + outputs[0])
	case lastUser == "hallucinate":
		if isFirst {
			return calls(toolCall("call_ghost", "ghost_tool", `{}`))
		}
		return answer("Ghost: " + outputs[0])
	case lastUser == "bad ids":
		if isFirst {
			return calls(toolCall("", "lookup_order", `{"order_id":"1"}`), toolCall("a/b", "lookup_order", `{"order_id":"2"}`),
				toolCall("dup", "lookup_order", `{"order_id":"3"}`), toolCall("dup", "lookup_order", `{"order_id":"4"}`))
		}
		ids := make([]string, 0, len(results))
		for _, result := range results {
			ids = append(ids, result.ToolCallID+"="+result.Content)
		}
		return answer("IDs: " + strings.Join(ids, "; "))
	case lastUser == "echo":
		if isFirst {
			return calls(toolCall("call_echo", "echo_call", `{}`))
		}
		return answer("Echo: " + outputs[0])
	case lastUser == "typed failure":
		if isFirst {
			return calls(toolCall("call_typed", "typed_failure", `{}`))
		}
		return answer("Typed: " + outputs[0])
	case lastUser == "plain failure":
		if isFirst {
			return calls(toolCall("call_plain", "plain_failure", `{}`))
		}
		return answer("Plain: " + outputs[0])
	case lastUser == "think slowly":
		return answer("answered after the restart")
	case lastUser == "ledger turn":
		if isFirst {
			return calls(toolCall("call_lookup", "lookup_order", `{"order_id":"1"}`),
				toolCall("call_balance", "fake-ledger_get_balance", `{"accountId":"acct-1"}`),
				toolCall("call_note", "fake-ledger_post_note", `{"accountId":"acct-1","text":"hello"}`))
		}
		scripted.coordination.writeMarker("ledger-final.reached")
		if err := scripted.coordination.waitForRelease(ctx, "ledger-final"); err != nil {
			return nil, err
		}
		return answer("Ledger: " + strings.Join(outputs, "; "))
	case lastUser == "rejected note":
		if isFirst {
			return calls(toolCall("call_reject", "fake-ledger_post_note", `{"accountId":"acct-1","text":"reject this"}`))
		}
		return answer("Note: " + outputs[0])
	case lastUser == "payout":
		if isFirst {
			return calls(toolCall("call_payout", "fake-ledger_create_payout", `{"accountId":"acct-2","amount":5}`))
		}
		return answer("Payout: " + outputs[0])
	default:
		for _, text := range userTexts {
			if strings.HasPrefix(text, dexagents.TurnFailureNoteHeader) {
				return answer("I see the failure note")
			}
		}
		return answer("You said: " + lastUser)
	}
}

func toolCall(id, name, arguments string) schema.ToolCall {
	return schema.ToolCall{ID: id, Type: "function", Function: schema.FunctionCall{Name: name, Arguments: arguments}}
}

func withUsage(message *schema.Message) *schema.Message {
	message.ResponseMeta = &schema.ResponseMeta{Usage: &schema.TokenUsage{PromptTokens: 10, CompletionTokens: 5}}
	return message
}

// doubleCallingHandler calls the model twice when the user says "call twice", as a retrying wrapper would.
type doubleCallingHandler struct {
	*adk.BaseChatModelAgentMiddleware
}

func (handler *doubleCallingHandler) WrapModel(_ context.Context, inner model.BaseModel[*schema.Message], _ *adk.ModelContext) (model.BaseModel[*schema.Message], error) {
	return &doubleCallingModel{inner: inner}, nil
}

type doubleCallingModel struct {
	inner model.BaseModel[*schema.Message]
}

func (double *doubleCallingModel) Generate(ctx context.Context, input []*schema.Message, options ...model.Option) (*schema.Message, error) {
	message, err := double.inner.Generate(ctx, input, options...)
	if err != nil {
		return nil, err
	}
	if lastUser, _, _ := conversationTail(input); lastUser != "call twice" {
		return message, nil
	}
	return double.inner.Generate(ctx, input, options...)
}

func (double *doubleCallingModel) Stream(ctx context.Context, input []*schema.Message, options ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	message, err := double.Generate(ctx, input, options...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{message}), nil
}

// namedChatModel answers with its own name and the instruction it received.
type namedChatModel struct {
	name string
}

func (named *namedChatModel) Generate(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	return schema.AssistantMessage(fmt.Sprintf("model %s: %s", named.name, input[0].Content), nil), nil
}

func (named *namedChatModel) Stream(ctx context.Context, input []*schema.Message, options ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	message, err := named.Generate(ctx, input, options...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{message}), nil
}

func (named *namedChatModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return named, nil
}

func ptr[Value any](value Value) *Value {
	return &value
}
