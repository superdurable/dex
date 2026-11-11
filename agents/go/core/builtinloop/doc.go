// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Package builtinloop is the built-in agent loop: it runs an agent without an
// agent framework.
//
// Dex owns the conversation and builds every model input from durable state,
// so the agent does not depend on a model provider's conversation API. Declare
// an AgentDefinition with instructions, a ModelClient, and function tools, then
// build its session Flow with NewBuiltinAgentSessionFlow. The runner makes at
// most one model call per RunModel Step. The session Flow, its Steps,
// approvals, and events are the ones every framework adapter uses.
//
//	definition, err := builtinloop.NewAgentDefinition(
//		"support",
//		"You help customers with their orders.",
//		openaimodel.NewResponsesModelClient(client, "gpt-5-mini"),
//		builtinloop.WithFunctionTools(lookupOrder),
//	)
//	if err != nil {
//		return err
//	}
//	flow, err := builtinloop.NewBuiltinAgentSessionFlow(definition, "SupportAgent")
//
// Since 0.5.0. It replaces package stepwise, which stays as a deprecated alias.
package builtinloop
