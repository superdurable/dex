// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Package dexeino runs CloudWeGo Eino ADK agents as durable Dex agent sessions.
//
// Give it the *adk.ChatModelAgentConfig you would pass to
// adk.NewChatModelAgent, or a builder that returns one, and it builds the Go
// core's session Flow (package dexagents) around it. Every model call runs in
// its own RunModel Step and every tool call in its own tool Step, so a crash
// repeats at most the one call that was running.
//
// Each RunModel builds a fresh Eino ChatModelAgent and adk.Runner from the
// current configuration and runs it over the session's stored messages. A
// handler that the adapter puts first in Handlers stops the run after exactly
// one model call: it turns every tool call into an Eino interrupt instead of
// running the tool. The adapter stores the model's message, and the session
// runs each tool call in its own Step through a one-call compose.ToolsNode
// built from the agent's ToolsNodeConfig and the Handlers' tool wrappers. The
// next RunModel replays the stored messages plus the tool results, so it uses
// the instruction and tools of the current deploy. No Eino checkpoint is kept.
//
//	config := &adk.ChatModelAgentConfig{
//		Name:        "support",
//		Description: "Answers order questions.",
//		Instruction: "You help customers with their orders.",
//		Model:       chatModel,
//		ToolsConfig: adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{
//			Tools: []tool.BaseTool{lookupOrder, refundOrder},
//		}},
//	}
//	flow, err := dexeino.NewAgentSessionFlow(config, "SupportAgent",
//		dexeino.WithToolEffects(map[string]dexagents.ToolEffect{
//			"lookup_order": dexagents.ToolEffectRead,
//			"refund_order": dexagents.ToolEffectNonIdempotentWrite,
//		}))
//	if err != nil {
//		return err
//	}
//	registry, err := dex.NewRegistry([]dex.Flow{flow})
//
// Every tool needs a Dex effect: from WithToolPolicies, WithToolEffects, a
// tool that implements ToolEffectDeclarer, or an explicit
// WithDefaultToolEffect. Construction fails for a tool without one, and for
// configurations that would make more than one model call in a Step or run a
// tool inside RunModel: ModelRetryConfig, ModelFailoverConfig, Exit,
// ToolsConfig.ReturnDirectly, and the summarization, automemory, and dream
// middlewares. It also refuses ToolsConfig.ExecuteSequentially, because Dex
// runs the tool calls of one model response as parallel Steps. A stop that
// a retry would repeat, such as a second model call in one Step, ends the turn
// as failed with dexagents.TurnCompleted.TurnError.
//
// WithToolSources offers the tools of a dexagents.ToolSource, such as the
// Connector operations of a connectortools.ConnectorToolSource, beside the
// agent's own. Their calls run through the source in their tool Steps,
// outside Eino's tool node and tool middlewares.
//
// Since 0.6.16.
package dexeino
