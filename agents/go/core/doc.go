// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Package dexagents runs durable agent sessions on Dex with the session Flow that the Python and
// TypeScript Dex Agents cores share.
//
// One Flow execution is one agent session, and the Flow ID is the session ID.
// Every model call is one RunModel Step, and every tool call is its own Step:
// CallTool for read and idempotent_write tools, with up to three attempts and
// a stable idempotency key, and CallSingleAttemptTool for every other tool,
// after AwaitToolApproval when the call needs approval. The tool calls of one
// model response run as parallel Steps, and ResumeTurn waits for all of their
// results before the next RunModel. A crash repeats at most the one call that
// was running.
//
// Step types, persistence names, RPC names, event types, and wire values are
// the contract in docs/design/plan/agent-framework-adapters.md, so a Client
// in any core drives a session that Go Workers serve. The Go core supports a
// subset of the contract, without questions, durable waits, steering, plans,
// compaction, or tool tasks, so serve each agent Flow type with Workers of one
// language: a Go Step that finds state from those parts fails the session with
// a reason instead of acting on it. Package
// builtinloop is the built-in agent loop on this Flow; packages openaimodel and
// anthropicmodel are its model clients, and agentstest has a scripted model
// client and a private engine for tests.
//
//	flow, err := builtinloop.NewBuiltinAgentSessionFlow(definition, "SupportAgent")
//	if err != nil {
//		return err
//	}
//	registry, err := dex.NewRegistry([]dex.Flow{flow})
//	if err != nil {
//		return err
//	}
//	sessions := dexagents.NewAgentSessionClient(client, flow)
package dexagents
