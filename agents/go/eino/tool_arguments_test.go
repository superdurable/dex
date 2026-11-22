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
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
	"github.com/stretchr/testify/require"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/agents/go/core/builtinloop"
	dexeino "github.com/superdurable/dex/agents/go/eino"
)

// schemaToolBase is the Info of a test tool with a given parameters schema, and the arguments it ran with.
type schemaToolBase struct {
	name       string
	parameters *schema.ParamsOneOf
	ran        *[]string
}

func (base schemaToolBase) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: base.name, Desc: "Test tool " + base.name, ParamsOneOf: base.parameters}, nil
}

func (base schemaToolBase) run(arguments string) string {
	*base.ran = append(*base.ran, arguments)
	return "ran with " + arguments
}

type invokableSchemaTool struct{ schemaToolBase }

func (checked invokableSchemaTool) InvokableRun(_ context.Context, arguments string, _ ...tool.Option) (string, error) {
	return checked.run(arguments), nil
}

type streamableSchemaTool struct{ schemaToolBase }

func (checked streamableSchemaTool) StreamableRun(_ context.Context, arguments string, _ ...tool.Option) (*schema.StreamReader[string], error) {
	return schema.StreamReaderFromArray([]string{checked.run(arguments)}), nil
}

type enhancedInvokableSchemaTool struct{ schemaToolBase }

func (checked enhancedInvokableSchemaTool) InvokableRun(_ context.Context, argument *schema.ToolArgument, _ ...tool.Option) (*schema.ToolResult, error) {
	return textToolResult(checked.run(argument.Text)), nil
}

type enhancedStreamableSchemaTool struct{ schemaToolBase }

func (checked enhancedStreamableSchemaTool) StreamableRun(_ context.Context, argument *schema.ToolArgument, _ ...tool.Option) (*schema.StreamReader[*schema.ToolResult], error) {
	return schema.StreamReaderFromArray([]*schema.ToolResult{textToolResult(checked.run(argument.Text))}), nil
}

func textToolResult(text string) *schema.ToolResult {
	return &schema.ToolResult{Parts: []schema.ToolOutputPart{{Type: schema.ToolPartTypeText, Text: text}}}
}

func jsonSchemaParameters(t *testing.T, text string) *schema.ParamsOneOf {
	t.Helper()
	parsed := &jsonschema.Schema{}
	require.NoError(t, json.Unmarshal([]byte(text), parsed))
	return schema.NewParamsOneOfByJSONSchema(parsed)
}

// newWriteToolRunner returns a runner whose tools are idempotent_writes, as a stored call records them.
func newWriteToolRunner(t *testing.T, toolsConfig compose.ToolsNodeConfig, handlers ...adk.ChatModelAgentMiddleware) *dexeino.Runner {
	t.Helper()
	runner, err := dexeino.NewRunner(&adk.ChatModelAgentConfig{Name: "agent", Description: "Test agent.", Model: &scriptedChatModel{},
		ToolsConfig: adk.ToolsConfig{ToolsNodeConfig: toolsConfig}, Handlers: handlers},
		dexeino.WithDefaultToolEffect(dexagents.ToolEffectIdempotentWrite))
	require.NoError(t, err)
	return runner
}

// runStoredCall runs one stored idempotent_write call, as Dex re-runs it on a later attempt.
func runStoredCall(runner *dexeino.Runner, toolName, arguments string, attempt int64) (string, error) {
	outcome, err := runner.ExecuteDeferredToolCall(context.Background(), dexagents.DeferredToolExecution{
		FlowID: "session", Attempt: attempt, IdempotencyKey: "session/turn-1/call_1",
		ToolCall: dexagents.AgentToolCall{ToolCallID: "call_1", TurnID: "turn-1", ToolName: toolName, Arguments: arguments,
			Effect: dexagents.ToolEffectIdempotentWrite, Status: dexagents.ToolCallStatusApproved},
	})
	if err != nil {
		return "", err
	}
	return outcome.(*dexagents.ToolExecutionResult).Output, nil
}

// builtInLoopRefusal returns what the Go built-in loop's FunctionTool answers for the same schema and arguments.
func builtInLoopRefusal(t *testing.T, toolName, parameters, arguments string) error {
	t.Helper()
	builtin, err := builtinloop.NewFunctionTool(toolName, "", json.RawMessage(parameters), dexagents.ToolEffectIdempotentWrite,
		func(context.Context, json.RawMessage, builtinloop.ToolCallContext) (string, error) { return "ran", nil })
	require.NoError(t, err)
	_, err = builtin.Invoke(context.Background(), arguments, builtinloop.ToolCallContext{})
	return err
}

// requireRefusal checks that err is the tool Step's definite failure with the given text.
func requireRefusal(t *testing.T, err error, text string) {
	t.Helper()
	require.IsType(t, &dexagents.ToolExecutionError{}, err, "a refusal is a definite failure, not a failed attempt")
	require.Equal(t, text, err.Error())
}

type refundArguments struct {
	OrderID     string `json:"order_id" jsonschema:"required"`
	AmountCents int    `json:"amount_cents" jsonschema:"required"`
}

// inferredRefundTool is an Eino typed tool whose parameters a deploy renamed from orderId / amountCents.
func inferredRefundTool(t *testing.T, ran *[]refundArguments) tool.InvokableTool {
	t.Helper()
	refund, err := utils.InferTool("issue_refund", "Refund an order.", func(_ context.Context, arguments refundArguments) (string, error) {
		*ran = append(*ran, arguments)
		return `{"status":"succeeded"}`, nil
	})
	require.NoError(t, err)
	return refund
}

const preRenameRefundArguments = `{"orderId":"A-1001","amountCents":4999}`

// A stored call that a deploy's renamed parameters reject is refused on attempt 2 with the built-in
// loop's text, and the typed tool never runs with zero values; a call in the new shape runs.
func TestToolStepRefusesStoredArgumentsTheCurrentSchemaRejects(t *testing.T) {
	var ran []refundArguments
	refund := inferredRefundTool(t, &ran)
	runner := newWriteToolRunner(t, compose.ToolsNodeConfig{Tools: []tool.BaseTool{refund}})

	_, refusal := runStoredCall(runner, "issue_refund", preRenameRefundArguments, 2)
	requireRefusal(t, refusal, "invalid arguments for issue_refund: arguments.order_id is required")
	require.Empty(t, ran, "the tool must not run with arguments its schema rejects")
	info, err := refund.Info(context.Background())
	require.NoError(t, err)
	parameters, err := info.ToJSONSchema()
	require.NoError(t, err)
	encoded, err := json.Marshal(parameters)
	require.NoError(t, err)
	require.Equal(t, builtInLoopRefusal(t, "issue_refund", string(encoded), preRenameRefundArguments).Error(), refusal.Error(),
		"the same text as the Go built-in loop")

	output, err := runStoredCall(runner, "issue_refund", `{"order_id":"A-1001","amount_cents":4999}`, 2)
	require.NoError(t, err)
	require.Equal(t, `{"status":"succeeded"}`, output)
	require.Equal(t, []refundArguments{{OrderID: "A-1001", AmountCents: 4999}}, ran)
}

// Each keyword of the built-in loop's JSON Schema subset refuses as the built-in loop does, and
// arguments that pass reach the tool byte for byte.
func TestToolStepChecksEachSchemaKeywordLikeTheBuiltInLoop(t *testing.T) {
	cases := []struct {
		name, parameters, refused, problem, accepted string
	}{
		{"type", `{"type":"object","properties":{"order_id":{"type":"string"}}}`,
			`{"order_id":5}`, "arguments.order_id must be string", `{"order_id": "A-1"}`},
		{"type list", `{"type":"object","properties":{"note":{"type":["string","null"]}}}`,
			`{"note":1}`, "arguments.note must be string or null", `{"note":null}`},
		{"integer", `{"type":"object","properties":{"count":{"type":"integer"}}}`,
			`{"count":1.5}`, "arguments.count must be integer", `{"count":2.0}`},
		{"enum", `{"type":"object","properties":{"status":{"type":"string","enum":["open","closed"]}}}`,
			`{"status":"lost"}`, `arguments.status must be one of ["open", "closed"]`, `{"status":"open"}`},
		{"anyOf", `{"type":"object","properties":{"id":{"anyOf":[{"type":"string"},{"type":"integer"}]}}}`,
			`{"id":true}`, "arguments.id matches none of the allowed types", `{"id":7}`},
		{"nested properties", `{"type":"object","properties":{"address":{"type":"object","properties":{"zip":{"type":"string"}},"required":["zip"]}}}`,
			`{"address":{}}`, "arguments.address.zip is required", `{"address":{"zip":"94107"}}`},
		{"required", `{"type":"object","properties":{"order_id":{"type":"string"}},"required":["order_id"]}`,
			`{"orderId":"A-1"}`, "arguments.order_id is required", `{"order_id":"A-1"}`},
		{"additionalProperties false", `{"type":"object","properties":{"order_id":{"type":"string"}},"additionalProperties":false}`,
			`{"order_id":"A-1","orderId":"A-1"}`, "arguments.orderId is not allowed", `{"order_id":"A-1"}`},
		{"additionalProperties schema", `{"type":"object","additionalProperties":{"type":"integer"}}`,
			`{"apples":"three"}`, "arguments.apples must be integer", `{"apples":3}`},
		{"items", `{"type":"object","properties":{"tags":{"type":"array","items":{"type":"string"}}}}`,
			`{"tags":["a",2]}`, "arguments.tags[1] must be string", `{"tags":["a","b"]}`},
		{"minimum", `{"type":"object","properties":{"amount":{"type":"integer","minimum":1}}}`,
			`{"amount":0}`, "arguments.amount must be at least 1", `{"amount":1}`},
		{"maximum", `{"type":"object","properties":{"amount":{"type":"number","maximum":99.5}}}`,
			`{"amount":100}`, "arguments.amount must be at most 99.5", `{"amount":99.5}`},
		{"not JSON", `{"type":"object","properties":{"order_id":{"type":"string"}}}`,
			`{"order_id":`, "arguments are not valid JSON", `{}`},
		{"not an object", `{"type":"object","properties":{"order_id":{"type":"string"}}}`,
			`["A-1"]`, "expected a JSON object", `{"order_id":"A-1"}`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var ran []string
			checked := invokableSchemaTool{schemaToolBase{name: "update_order", parameters: jsonSchemaParameters(t, testCase.parameters), ran: &ran}}
			runner := newWriteToolRunner(t, compose.ToolsNodeConfig{Tools: []tool.BaseTool{checked}})

			_, err := runStoredCall(runner, "update_order", testCase.refused, 1)
			requireRefusal(t, err, "invalid arguments for update_order: "+testCase.problem)
			require.Equal(t, builtInLoopRefusal(t, "update_order", testCase.parameters, testCase.refused).Error(), err.Error())
			require.Empty(t, ran)

			require.NoError(t, builtInLoopRefusal(t, "update_order", testCase.parameters, testCase.accepted))
			output, err := runStoredCall(runner, "update_order", testCase.accepted, 1)
			require.NoError(t, err)
			require.Equal(t, []string{testCase.accepted}, ran, "arguments that pass reach the tool unchanged")
			require.Equal(t, "ran with "+testCase.accepted, output)
		})
	}
}

// Keywords outside the subset, such as pattern and minLength, refuse nothing, as in the built-in loop.
func TestToolStepIgnoresKeywordsOutsideTheSubset(t *testing.T) {
	var ran []string
	parameters := `{"type":"object","properties":{"code":{"type":"string","pattern":"^[A-Z]+$","minLength":3,"format":"email"}}}`
	checked := invokableSchemaTool{schemaToolBase{name: "set_code", parameters: jsonSchemaParameters(t, parameters), ran: &ran}}
	runner := newWriteToolRunner(t, compose.ToolsNodeConfig{Tools: []tool.BaseTool{checked}})

	require.NoError(t, builtInLoopRefusal(t, "set_code", parameters, `{"code":"a"}`))
	_, err := runStoredCall(runner, "set_code", `{"code":"a"}`, 1)
	require.NoError(t, err)
	require.Equal(t, []string{`{"code":"a"}`}, ran)
}

// A schema built from Eino ParameterInfo values is checked the same way.
func TestToolStepChecksParameterInfoSchemas(t *testing.T) {
	var ran []string
	checked := invokableSchemaTool{schemaToolBase{name: "return_order", ran: &ran,
		parameters: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"order_id": {Type: schema.String, Required: true},
			"reason":   {Type: schema.String, Enum: []string{"damaged", "late"}},
			"items": {Type: schema.Array, ElemInfo: &schema.ParameterInfo{Type: schema.Object, SubParams: map[string]*schema.ParameterInfo{
				"sku": {Type: schema.String, Required: true},
			}}},
		})}}
	runner := newWriteToolRunner(t, compose.ToolsNodeConfig{Tools: []tool.BaseTool{checked}})

	for arguments, problem := range map[string]string{
		`{"reason":"late"}`:                    "arguments.order_id is required",
		`{"order_id":"A-1","reason":"lost"}`:   `arguments.reason must be one of ["damaged", "late"]`,
		`{"order_id":"A-1","items":[{"q":1}]}`: "arguments.items[0].sku is required",
	} {
		_, err := runStoredCall(runner, "return_order", arguments, 1)
		requireRefusal(t, err, "invalid arguments for return_order: "+problem)
	}
	require.Empty(t, ran)
	_, err := runStoredCall(runner, "return_order", `{"order_id":"A-1","reason":"late","items":[{"sku":"S-1"}]}`, 1)
	require.NoError(t, err)
	require.Len(t, ran, 1)
}

// A tool without parameters, or with an empty schema, accepts any JSON object and refuses anything else.
func TestToolStepChecksToolsWithoutParametersForAnObject(t *testing.T) {
	for name, parameters := range map[string]*schema.ParamsOneOf{
		"no ParamsOneOf": nil,
		"empty schema":   schema.NewParamsOneOfByJSONSchema(&jsonschema.Schema{}),
	} {
		t.Run(name, func(t *testing.T) {
			var ran []string
			checked := invokableSchemaTool{schemaToolBase{name: "ping", parameters: parameters, ran: &ran}}
			runner := newWriteToolRunner(t, compose.ToolsNodeConfig{Tools: []tool.BaseTool{checked}})

			for _, arguments := range []string{`{}`, `{"anything":1}`, ``} {
				_, err := runStoredCall(runner, "ping", arguments, 1)
				require.NoError(t, err, arguments)
			}
			require.Equal(t, []string{`{}`, `{"anything":1}`, ``}, ran)
			_, err := runStoredCall(runner, "ping", `[1]`, 1)
			requireRefusal(t, err, "invalid arguments for ping: expected a JSON object")
			_, err = runStoredCall(runner, "ping", `{`, 1)
			requireRefusal(t, err, "invalid arguments for ping: arguments are not valid JSON")
			require.Len(t, ran, 3)
		})
	}
}

// Invokable, streamable, and both enhanced tool kinds are all checked.
func TestToolStepChecksEveryToolKind(t *testing.T) {
	parameters := `{"type":"object","properties":{"order_id":{"type":"string"}},"required":["order_id"]}`
	newTools := map[string]func(base schemaToolBase) tool.BaseTool{
		"invokable":           func(base schemaToolBase) tool.BaseTool { return invokableSchemaTool{base} },
		"streamable":          func(base schemaToolBase) tool.BaseTool { return streamableSchemaTool{base} },
		"enhanced invokable":  func(base schemaToolBase) tool.BaseTool { return enhancedInvokableSchemaTool{base} },
		"enhanced streamable": func(base schemaToolBase) tool.BaseTool { return enhancedStreamableSchemaTool{base} },
	}
	for name, newTool := range newTools {
		t.Run(name, func(t *testing.T) {
			var ran []string
			checked := newTool(schemaToolBase{name: "cancel_order", parameters: jsonSchemaParameters(t, parameters), ran: &ran})
			runner := newWriteToolRunner(t, compose.ToolsNodeConfig{Tools: []tool.BaseTool{checked}})

			_, err := runStoredCall(runner, "cancel_order", `{"orderId":"A-1"}`, 2)
			requireRefusal(t, err, "invalid arguments for cancel_order: arguments.order_id is required")
			require.Empty(t, ran)
			output, err := runStoredCall(runner, "cancel_order", `{"order_id":"A-1"}`, 2)
			require.NoError(t, err)
			require.Equal(t, `ran with {"order_id":"A-1"}`, output)
		})
	}
}

// Eino converts first (ArgumentsAliases, then ToolArgumentsHandler); the check sees the converted
// arguments, and the tool middlewares and Handler wrappers run only for arguments that pass.
func TestToolStepChecksArgumentsAfterEinoConvertsThem(t *testing.T) {
	renameKeys := func(arguments string) string {
		return strings.NewReplacer(`"orderId"`, `"order_id"`, `"amountCents"`, `"amount_cents"`).Replace(arguments)
	}
	t.Run("ArgumentsAliases", func(t *testing.T) {
		var ran []refundArguments
		runner := newWriteToolRunner(t, compose.ToolsNodeConfig{Tools: []tool.BaseTool{inferredRefundTool(t, &ran)},
			ToolAliases: map[string]compose.ToolAliasConfig{"issue_refund": {ArgumentsAliases: map[string][]string{
				"order_id": {"orderId"}, "amount_cents": {"amountCents"},
			}}}})
		_, err := runStoredCall(runner, "issue_refund", preRenameRefundArguments, 2)
		require.NoError(t, err)
		require.Equal(t, []refundArguments{{OrderID: "A-1001", AmountCents: 4999}}, ran)
	})
	t.Run("ToolArgumentsHandler", func(t *testing.T) {
		var ran []refundArguments
		runner := newWriteToolRunner(t, compose.ToolsNodeConfig{Tools: []tool.BaseTool{inferredRefundTool(t, &ran)},
			ToolArgumentsHandler: func(_ context.Context, _ string, arguments string) (string, error) {
				return renameKeys(arguments), nil
			}})
		_, err := runStoredCall(runner, "issue_refund", preRenameRefundArguments, 2)
		require.NoError(t, err)
		require.Equal(t, []refundArguments{{OrderID: "A-1001", AmountCents: 4999}}, ran)
	})
	t.Run("ToolArgumentsHandler output is checked", func(t *testing.T) {
		var ran []refundArguments
		runner := newWriteToolRunner(t, compose.ToolsNodeConfig{Tools: []tool.BaseTool{inferredRefundTool(t, &ran)},
			ToolArgumentsHandler: func(context.Context, string, string) (string, error) {
				return `{"order_id":"A-1001","amount_cents":"4999"}`, nil
			}})
		_, err := runStoredCall(runner, "issue_refund", `{"order_id":"A-1001","amount_cents":4999}`, 2)
		requireRefusal(t, err, "invalid arguments for issue_refund: arguments.amount_cents must be integer")
		require.Empty(t, ran)
	})
	t.Run("wrappers run only for arguments that pass", func(t *testing.T) {
		var ran []refundArguments
		var middlewareRuns, agentMiddlewareRuns, handlerWraps, conversions atomic.Int64
		counting := func(counter *atomic.Int64) compose.ToolMiddleware {
			return compose.ToolMiddleware{Invokable: func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
				return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
					counter.Add(1)
					return next(ctx, input)
				}
			}}
		}
		runner, err := dexeino.NewRunner(&adk.ChatModelAgentConfig{Name: "agent", Description: "Test agent.", Model: &scriptedChatModel{},
			ToolsConfig: adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools:               []tool.BaseTool{inferredRefundTool(t, &ran)},
				ToolCallMiddlewares: []compose.ToolMiddleware{counting(&middlewareRuns)},
				ToolArgumentsHandler: func(_ context.Context, _ string, arguments string) (string, error) {
					conversions.Add(1)
					return arguments, nil
				},
			}},
			Middlewares: []adk.AgentMiddleware{{WrapToolCall: counting(&agentMiddlewareRuns)}},
			Handlers:    []adk.ChatModelAgentMiddleware{countingToolHandler{&adk.BaseChatModelAgentMiddleware{}, &handlerWraps, nil}},
		}, dexeino.WithDefaultToolEffect(dexagents.ToolEffectIdempotentWrite))
		require.NoError(t, err)

		_, err = runStoredCall(runner, "issue_refund", preRenameRefundArguments, 2)
		requireRefusal(t, err, "invalid arguments for issue_refund: arguments.order_id is required")
		require.Equal(t, int64(1), conversions.Load(), "the conversion runs before the check")
		require.Zero(t, middlewareRuns.Load()+agentMiddlewareRuns.Load()+handlerWraps.Load(), "no wrapper runs for refused arguments")
		require.Empty(t, ran)

		_, err = runStoredCall(runner, "issue_refund", `{"order_id":"A-1001","amount_cents":4999}`, 2)
		require.NoError(t, err)
		require.Equal(t, []int64{1, 1, 1}, []int64{middlewareRuns.Load(), agentMiddlewareRuns.Load(), handlerWraps.Load()})
		require.Len(t, ran, 1)
	})
}

// A tool that only a Handler's BeforeAgent adds is checked against its own schema too.
func TestToolStepChecksAHandlerAddedTool(t *testing.T) {
	var ran []string
	var handlerWraps atomic.Int64
	added := invokableSchemaTool{schemaToolBase{name: "write_file", ran: &ran,
		parameters: jsonSchemaParameters(t, `{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)}}
	runner := newWriteToolRunner(t, compose.ToolsNodeConfig{},
		countingToolHandler{&adk.BaseChatModelAgentMiddleware{}, &handlerWraps, added})

	_, err := runStoredCall(runner, "write_file", `{"file_path":"a.txt"}`, 2)
	requireRefusal(t, err, "invalid arguments for write_file: arguments.path is required")
	require.Empty(t, ran)
	_, err = runStoredCall(runner, "write_file", `{"path":"a.txt"}`, 2)
	require.NoError(t, err)
	require.Equal(t, []string{`{"path":"a.txt"}`}, ran)
}
