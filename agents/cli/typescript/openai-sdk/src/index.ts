// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

/**
 * The `dex-agents-openai-sdk` command: an OpenAI Agents SDK agent in the terminal. It builds the agent session Flow
 * with the OpenAI Agents SDK adapter and runs it with the shared Dex Agents CLI core.
 *
 * @packageDocumentation
 */

export {
  TurnModelProvider,
  createOpenAISdkSessionFlow,
  defaultInstructions,
  defaultOpenAIModel,
  openaiSdkCliFlowType,
  openaiSdkHarness,
  validateOpenAISection,
} from "./openai-sdk-harness.js";
