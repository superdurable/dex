// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

/**
 * The `dex-agents-pi` command: a Pi coding agent in the terminal. Dex runs the agent loop with the built-in agent
 * loop session Flow, pi-ai makes each model call, and pi-coding-agent's tools run as Dex tool Steps, on the shared
 * Dex Agents CLI runtime.
 *
 * @packageDocumentation
 */

export {
  createPiSessionFlow,
  defaultInstructions,
  defaultPiModel,
  piCliFlowType,
  piHarness,
  readPiSection,
  type PiSection,
} from "./pi-harness.js";
export {
  PiCodingToolSource,
  defaultBashTimeoutSeconds,
  maximumBashTimeoutSeconds,
  piCodingLocalTools,
  type PiCodingLocalTool,
} from "./pi-coding-tool-source.js";
export { PiModelClients, parsePiModelId, resolvePiProviderApiKey, type PiModelId } from "./pi-model-clients.js";
export { ProjectContextFiles, type ProjectContextFile } from "./project-context-files.js";
export { WorkspaceGrep, type WorkspaceGrepArguments } from "./workspace-grep.js";
