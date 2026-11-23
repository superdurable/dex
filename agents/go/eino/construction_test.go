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
	"errors"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/summarization"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"

	dexagents "github.com/superdurable/dex/agents/go/core"
	dexeino "github.com/superdurable/dex/agents/go/eino"
)

func echoModel() *scriptedChatModel {
	return &scriptedChatModel{respond: func(input []*schema.Message) (*schema.Message, error) {
		return schema.AssistantMessage("You said: "+input[len(input)-1].Content, nil), nil
	}}
}

func toolsConfig(tools ...tool.BaseTool) adk.ToolsConfig {
	return adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: tools}}
}

func noopTool(name string) funcTool {
	return funcTool{name: name, run: func(context.Context, string) (string, error) { return "ok", nil }}
}

// declaredWrite is a tool that declares its own Dex effect.
type declaredWrite struct{ funcTool }

func (declaredWrite) DexToolEffect() dexagents.ToolEffect {
	return dexagents.ToolEffectNonIdempotentWrite
}

// Construction refuses configurations that would make more than one model call per Step, run a tool
// inside RunModel, or leave a tool without a Dex effect.
func TestConstructionRefusesUnsupportedConfigurations(t *testing.T) {
	base := func() *adk.ChatModelAgentConfig {
		return &adk.ChatModelAgentConfig{Name: "agent", Description: "Test agent.", Model: echoModel(),
			ToolsConfig: toolsConfig(noopTool("lookup"))}
	}
	effects := dexeino.WithToolEffects(map[string]dexagents.ToolEffect{"lookup": dexagents.ToolEffectRead})
	for name, testCase := range map[string]struct {
		config   func() *adk.ChatModelAgentConfig
		options  []dexeino.Option
		expected string
	}{
		"no model": {func() *adk.ChatModelAgentConfig { config := base(); config.Model = nil; return config },
			[]dexeino.Option{effects}, "no Model"},
		"model retry": {func() *adk.ChatModelAgentConfig {
			config := base()
			config.ModelRetryConfig = &adk.ModelRetryConfig{MaxRetries: 2}
			return config
		}, []dexeino.Option{effects}, "ModelRetryConfig"},
		"model failover": {func() *adk.ChatModelAgentConfig {
			config := base()
			config.ModelFailoverConfig = &adk.ModelFailoverConfig[*schema.Message]{MaxRetries: 1}
			return config
		}, []dexeino.Option{effects}, "ModelFailoverConfig"},
		"exit tool": {func() *adk.ChatModelAgentConfig { config := base(); config.Exit = &adk.ExitTool{}; return config },
			[]dexeino.Option{effects}, "Exit tool"},
		"return directly": {func() *adk.ChatModelAgentConfig {
			config := base()
			config.ToolsConfig.ReturnDirectly = map[string]bool{"lookup": true}
			return config
		}, []dexeino.Option{effects}, "ReturnDirectly"},
		"execute sequentially": {func() *adk.ChatModelAgentConfig {
			config := base()
			config.ToolsConfig.ExecuteSequentially = true
			return config
		}, []dexeino.Option{effects}, "ExecuteSequentially runs the tool calls of one model response in order"},
		"summarization": {func() *adk.ChatModelAgentConfig {
			config := base()
			config.Handlers = []adk.ChatModelAgentMiddleware{&summarization.TypedMiddleware[*schema.Message]{}}
			return config
		}, []dexeino.Option{effects}, "summarization"},
		"nil handler": {func() *adk.ChatModelAgentConfig {
			config := base()
			config.Handlers = []adk.ChatModelAgentMiddleware{nil}
			return config
		}, []dexeino.Option{effects}, "handler 0 is nil"},
		"no effect": {base, nil, `tool "lookup" has no Dex effect`},
		"invalid effect": {base, []dexeino.Option{dexeino.WithToolEffects(map[string]dexagents.ToolEffect{"lookup": "sometimes"})},
			"not a tool effect"},
		"duplicate names": {func() *adk.ChatModelAgentConfig {
			config := base()
			config.ToolsConfig = toolsConfig(noopTool("lookup"), noopTool("lookup"))
			return config
		}, []dexeino.Option{effects}, `two tools are named "lookup"`},
		"alias of another tool": {func() *adk.ChatModelAgentConfig {
			config := base()
			config.ToolsConfig = toolsConfig(noopTool("lookup"), noopTool("search"))
			config.ToolsConfig.ToolAliases = map[string]compose.ToolAliasConfig{"lookup": {NameAliases: []string{"search"}}}
			return config
		}, []dexeino.Option{dexeino.WithDefaultToolEffect(dexagents.ToolEffectRead)}, "is the name of another tool"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := dexeino.NewAgentSessionFlow(testCase.config(), "Agent", testCase.options...)
			require.ErrorContains(t, err, testCase.expected)
		})
	}

	_, err := dexeino.NewRunner(nil)
	require.ErrorContains(t, err, "needs an Eino ChatModelAgentConfig")
	_, err = dexeino.NewRunnerWithBuilder(nil)
	require.ErrorContains(t, err, "needs an AgentConfigBuilder")
	_, err = dexeino.NewRunnerWithBuilder(func(context.Context, dexeino.AgentBuildContext) (*adk.ChatModelAgentConfig, error) {
		return nil, errors.New("no models configured")
	})
	require.ErrorContains(t, err, "no models configured")
}

// Every tool gets its effect from a policy, WithToolEffects, a ToolEffectDeclarer, or the default effect.
func TestConstructionAcceptsEveryEffectSource(t *testing.T) {
	config := &adk.ChatModelAgentConfig{Name: "agent", Description: "Test agent.", Model: echoModel(),
		ToolsConfig: toolsConfig(noopTool("by_policy"), noopTool("by_effects"), declaredWrite{noopTool("declared")})}
	_, err := dexeino.NewAgentSessionFlow(config, "Agent",
		dexeino.WithToolPolicies(map[string]dexagents.ToolPolicy{"by_policy": {Effect: dexagents.ToolEffectRead}}),
		dexeino.WithToolEffects(map[string]dexagents.ToolEffect{"by_effects": dexagents.ToolEffectIdempotentWrite}))
	require.NoError(t, err)

	undeclared := &adk.ChatModelAgentConfig{Name: "agent", Description: "Test agent.", Model: echoModel(),
		ToolsConfig: toolsConfig(noopTool("anything"))}
	_, err = dexeino.NewAgentSessionFlow(undeclared, "Agent", dexeino.WithDefaultToolEffect(dexagents.ToolEffectRead))
	require.NoError(t, err)
}

// Every option that mirrors a session option reaches dexagents.NewAgentSessionFlow, as its validation shows.
func TestSessionOptionsReachTheSession(t *testing.T) {
	config := &adk.ChatModelAgentConfig{Name: "agent", Description: "Test agent.", Model: echoModel()}
	for name, testCase := range map[string]struct {
		option   dexeino.Option
		expected string
	}{
		"interrupt deadline":    {dexeino.WithInterruptDeadline(-time.Second), "interrupt deadline must be positive"},
		"event stream capacity": {dexeino.WithEventStreamCapacityBytes(-1), "event stream capacity bytes must be positive"},
		"message window size":   {dexeino.WithMessageWindowSize(1), "message window size must be at least message chunk size"},
		"message chunk size":    {dexeino.WithMessageChunkSize(0), "message chunk size must be positive"},
		"message chunk bytes":   {dexeino.WithMessageChunkMaxBytes(0), "message chunk max bytes must be between"},
		"model retry policy": {dexeino.WithModelRetryPolicy(dexagents.ModelRetryPolicy{InitialInterval: time.Millisecond}),
			"model retry intervals must be whole seconds"},
		"tool policies": {dexeino.WithToolPolicies(map[string]dexagents.ToolPolicy{"later": {Effect: "often"}}),
			`tool policy for "later" has effect "often"`},
		"default tool effect": {dexeino.WithDefaultToolEffect("often"), `default tool effect "often" is not a tool effect`},
		"forwarded session option": {dexeino.WithSessionOptions(dexagents.WithMessageChunkSize(-1)),
			"message chunk size must be positive"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := dexeino.NewAgentSessionFlow(config, "Agent", testCase.option)
			require.ErrorContains(t, err, testCase.expected)
		})
	}

	runner, err := dexeino.NewRunner(config, dexeino.WithLogger(nil), dexeino.WithInterruptDeadline(time.Hour),
		dexeino.WithSessionOptions(dexagents.WithMessageWindowSize(80)))
	require.NoError(t, err)
	require.Len(t, runner.SessionOptions(), 3, "the logger, the deadline, then the forwarded option")
	flow, err := dexagents.NewAgentSessionFlow(runner, "Agent", runner.SessionOptions()...)
	require.NoError(t, err)
	_, isIncremental := flow.Runner().(dexagents.UnfinishedTurnMessageBuilder)
	require.True(t, isIncremental)
}
