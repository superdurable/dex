// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

/**
 * The `dex-agents-claude-sdk` command: a Claude Agent SDK agent in the terminal. It builds the agent session Flow
 * with the Claude Agent SDK adapter and runs it with the shared Dex Agents CLI runtime.
 *
 * @packageDocumentation
 */

export {
  ClaudeCodeLocalTools,
  claudeCodeLocalTools,
  claudeSdkCliFlowType,
  claudeSdkHarness,
  createClaudeSdkSessionFlow,
  defaultClaudeModel,
  defaultInstructions,
  validateClaudeSection,
  type ClaudeCodeLocalTool,
} from "./claude-sdk-harness.js";
