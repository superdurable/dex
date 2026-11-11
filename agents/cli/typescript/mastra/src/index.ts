// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

/**
 * The `dex-agents-mastra` command: a Mastra agent in the terminal. It builds the agent session Flow with the Mastra
 * adapter and runs it with the shared Dex Agents CLI runtime.
 *
 * @packageDocumentation
 */

export {
  createMastraCliSessionFlow,
  defaultInstructions,
  defaultMastraModel,
  mastraCliFlowType,
  mastraHarness,
  validateMastraSection,
} from "./mastra-harness.js";
