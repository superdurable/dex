// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//go:build !eino_v010

package dexeino_test

import (
	"context"

	"github.com/cloudwego/eino/adk"
)

// BeforeAgent adds the handler's tool; Eino v0.9 passes a plain ChatModelAgentContext.
func (handler countingToolHandler) BeforeAgent(ctx context.Context, agentContext *adk.ChatModelAgentContext) (context.Context, *adk.ChatModelAgentContext, error) {
	if handler.addedTool != nil {
		agentContext.Tools = append(agentContext.Tools, handler.addedTool)
	}
	return ctx, agentContext, nil
}
