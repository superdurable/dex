// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

/**
 * The shared runtime of the Dex Agents CLI commands. A harness package implements {@link AgentHarness} and calls
 * {@link runAgentCli}.
 *
 * @packageDocumentation
 */

export {
  isLocalToolGroupEnabled,
  isShellCommandApprovalRequired,
  localToolGroups,
  type AgentHarness,
  type HarnessContext,
  type LocalToolGroup,
  type ResolvedTurn,
} from "./agent-harness.js";
export { namespaceSessionFlowType, processAgentCliIo, runAgentCli, type AgentCliIo } from "./agent-cli.js";
export {
  ConfigError,
  canonicalOperationName,
  compareCodePoints,
  defaultConnectorsMcp,
  defaultDexServer,
  defaultWorkspace,
  isCoveredByConnectorEntry,
  isShellCommandAllowed,
  parseConfigYaml,
  readSchema,
  resolveConfig,
  resolveTurn,
  type ConfigErrorCode,
  type ConnectorEntry,
  type EffectiveConfig,
  type LocalToolsConfig,
  type TurnConfig,
} from "./cli-config.js";
export {
  defaultDexcliDevDexPort,
  defaultDexcliDevWebPort,
  defaultDexcliDownloadUrl,
  readCliEnvironment,
  type CliEnvironment,
} from "./cli-environment.js";
export {
  ConnectorsMcpToolSource,
  connectorToolMetaKeys,
  isConnectorToolEnabledBy,
  listConnectorToolIdentities,
  probeConnectorsMcp,
  type ConnectorToolIdentity,
  type ConnectorsMcpAvailability,
} from "./connector-tool-source.js";
export { pinnedDexcliVersion, resolveDexcliExecutable } from "./dexcli-binary.js";
export {
  readStartedDexcliDev,
  resolveDexServer,
  type DexServerConnection,
  type StartedDexcliDev,
} from "./dex-server.js";
export { LocalToolSource, localToolNames } from "./local-tools/local-tool-source.js";
export { TurnConfigResolver } from "./turn-config-resolver.js";
export { resolveTurnToolPolicy } from "./tool-policy.js";
