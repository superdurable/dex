// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

/**
 * The `dex-agents-vercel-ai` command: a Vercel AI SDK agent in the terminal. It builds the agent session Flow with
 * the Vercel AI SDK adapter and runs it with the shared Dex Agents CLI core.
 *
 * @packageDocumentation
 */

export { createProviderLanguageModel, supportedModelProviders } from "./provider-language-models.js";
export { TurnLanguageModel } from "./turn-language-model.js";
export {
  createVercelAiCliSessionFlow,
  defaultInstructions,
  defaultVercelAiModel,
  validateVercelAiSection,
  vercelAiCliFlowType,
  vercelAiHarness,
} from "./vercel-ai-harness.js";
