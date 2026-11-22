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
