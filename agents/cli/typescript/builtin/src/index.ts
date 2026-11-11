// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

/**
 * The `dex-agents-builtin` command: an agent run by the Dex Agents core's built-in agent loop in the terminal. It
 * builds the agent session Flow with the core's AI SDK model client and runs it with the shared Dex Agents CLI core.
 *
 * @packageDocumentation
 */

export {
  builtinCliFlowType,
  builtinHarness,
  createBuiltinSessionFlow,
  defaultBuiltinModel,
  defaultInstructions,
  validateBuiltinSection,
} from "./builtin-harness.js";
export { createProviderLanguageModel, supportedModelProviders } from "./provider-language-models.js";
