// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package stepwise_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/agents/go/core/agentstest"
	"github.com/superdurable/dex/agents/go/core/builtinloop"
	"github.com/superdurable/dex/agents/go/core/stepwise"
)

type lookupArguments struct {
	OrderID string `json:"order_id"`
}

func TestDeprecatedPackageBuildsTheSameAgent(t *testing.T) {
	lookup, err := stepwise.NewTypedFunctionTool(
		"lookup_order",
		"Look up an order.",
		json.RawMessage(`{"type":"object","properties":{"order_id":{"type":"string"}}}`),
		dexagents.ToolEffectRead,
		func(ctx context.Context, arguments lookupArguments, call stepwise.ToolCallContext) (string, error) {
			return "order " + arguments.OrderID, nil
		},
		stepwise.WithRequiresApproval(false),
	)
	require.NoError(t, err)
	model := agentstest.NewScriptedModelClient(func(request builtinloop.ModelRequest) (agentstest.ScriptedModelReply, error) {
		return agentstest.ScriptedModelReply{Text: "Done."}, nil
	})
	definition, err := stepwise.NewAgentDefinition("support", "Help.", model,
		stepwise.WithFunctionTools(lookup),
		stepwise.WithMaxModelCallsPerTurn(stepwise.DefaultMaxModelCallsPerTurn),
	)
	require.NoError(t, err)

	var newDefinition *builtinloop.AgentDefinition = definition
	var runner *builtinloop.BuiltinLoopAgentRunner = stepwise.NewStepwiseAgentRunner(newDefinition)
	require.NotNil(t, runner)
	flow, err := stepwise.NewStepwiseAgentSessionFlow(definition, "DeprecatedStepwiseAgent")
	require.NoError(t, err)
	_, isBuiltinLoopRunner := flow.Runner().(*builtinloop.BuiltinLoopAgentRunner)
	require.True(t, isBuiltinLoopRunner)

	result, err := lookup.Invoke(context.Background(), `{"order_id":"7"}`, stepwise.ToolCallContext{})
	require.NoError(t, err)
	require.Equal(t, "order 7", result)
}

func TestDeprecatedMessageCodecMatchesTheBuiltinLoop(t *testing.T) {
	item := stepwise.UserTextItem{Text: "Where is order 7?"}

	oldMessage, err := stepwise.EncodeStepwiseMessage(item)
	require.NoError(t, err)
	newMessage, err := builtinloop.EncodeBuiltinLoopMessage(item)
	require.NoError(t, err)
	require.Equal(t, newMessage, oldMessage)

	decoded, err := stepwise.DecodeStepwiseMessage(dexagents.AgentMessage{FrameworkItem: oldMessage.FrameworkItem})
	require.NoError(t, err)
	require.Equal(t, builtinloop.ModelInputItem(item), decoded)
	require.Equal(t, builtinloop.UserTextItemType, stepwise.UserTextItemType)
}
