// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//go:build eino_v010

package dexeino_test

import (
	"context"
	"errors"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

// BeforeAgent adds the handler's tool; Eino v0.10 passes a generic ChatModelAgentContext.
func (handler countingToolHandler) BeforeAgent(
	ctx context.Context,
	agentContext *adk.ChatModelAgentContext[*schema.Message],
) (context.Context, *adk.ChatModelAgentContext[*schema.Message], error) {
	if handler.addedTool != nil {
		agentContext.Tools = append(agentContext.Tools, handler.addedTool)
	}
	return ctx, agentContext, nil
}

// BeforeAgent adds the handler's tool on its first call and fails on every later one.
func (handler failingLookupHandler) BeforeAgent(
	ctx context.Context,
	agentContext *adk.ChatModelAgentContext[*schema.Message],
) (context.Context, *adk.ChatModelAgentContext[*schema.Message], error) {
	if handler.calls.Add(1) > 1 {
		return ctx, agentContext, errors.New("the tool registry is unreachable")
	}
	agentContext.Tools = append(agentContext.Tools, handler.addedTool)
	return ctx, agentContext, nil
}

// BeforeAgent runs the wrapped Handler's BeforeAgent and declares the effect of each tool it adds.
func (handler declaringHandler) BeforeAgent(
	ctx context.Context,
	agentContext *adk.ChatModelAgentContext[*schema.Message],
) (context.Context, *adk.ChatModelAgentContext[*schema.Message], error) {
	added := len(agentContext.Tools)
	ctx, agentContext, err := handler.ChatModelAgentMiddleware.BeforeAgent(ctx, agentContext)
	if err != nil {
		return ctx, agentContext, err
	}
	agentContext.Tools = handler.declareAddedTools(ctx, agentContext.Tools, added)
	return ctx, agentContext, nil
}
