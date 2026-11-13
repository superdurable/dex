// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

/**
 * The shared CLI core of the Dex Agents CLI commands. A harness package implements {@link AgentHarness} and calls
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
  type HarnessDefaultWorkspaceLocation,
  type LocalToolGroup,
  type ResolvedTurn,
} from "./agent-harness.js";
export {
  namespaceSessionFlowType,
  processAgentCliIo,
  runAgentCli,
  sessionActionPermission,
  type AgentCliIo,
  type SessionEnvironmentIdentity,
} from "./agent-cli.js";
export {
  ConfigError,
  canonicalOperationName,
  compareCodePoints,
  defaultConnectorsMcp,
  defaultDexServer,
  defaultMediaStore,
  defaultSessionSummary,
  defaultWorkspace,
  isCoveredByConnectorEntry,
  isSessionSummaryIndexed,
  isShellCommandAllowed,
  parseConfigYaml,
  readSchema,
  resolveConfig,
  resolveTurn,
  type ConfigErrorCode,
  type ConnectorEntry,
  type DexAuthConfig,
  type DexTlsConfig,
  type EffectiveConfig,
  type LocalToolsConfig,
  type MediaConfig,
  type RemoteMcpServerConfig,
  type SessionsConfig,
  type TurnConfig,
  type TurnResolution,
} from "./cli-config.js";
export {
  defaultDexcliDevDexPort,
  defaultDexcliDevWebPort,
  defaultDexcliDownloadUrl,
  readCliEnvironment,
  readDexConnectionVariables,
  removeDexSecretVariables,
  dexSecretVariableNames,
  type CliEnvironment,
  type DexConnectionVariables,
} from "./cli-environment.js";
export {
  DexConnectionSettingsError,
  resolveDexConnectionSettings,
  type DexAuthSettings,
  type DexConnectionPaths,
  type DexConnectionSettings,
  type DexTlsSettings,
} from "./dex-connection.js";
export {
  ConnectorsMcpToolSource,
  DisabledConnectorsToolSource,
  connectorToolMetaKeys,
  expectedIdentityMetaKeys,
  isConnectorToolEnabledBy,
  listConnectorToolIdentities,
  probeConnectorsMcp,
  type ConnectorToolIdentity,
  type ConnectorsMcpAvailability,
  type ConnectorsMcpToolSourceOptions,
  type ConnectorsMcpUnavailability,
} from "./connector-tool-source.js";
export { pinnedDexcliVersion, resolveDexcliExecutable } from "./dexcli-binary.js";
export {
  readStartedDexcliDev,
  resolveDexServer,
  type DexServerConnection,
  type StartedDexcliDev,
} from "./dex-server.js";
export { LocalToolSource, localToolNames } from "./local-tools/local-tool-source.js";
export {
  McpServerSettingsError,
  McpServersToolSource,
  mcpServersToolSourceName,
  resolveMcpServers,
  type McpServersToolSourceOptions,
} from "./mcp-servers-tool-source.js";
export {
  WorkspaceFileRefusedError,
  WorkspaceFiles,
  type WorkspaceDirectoryEntry,
  type WorkspaceFileVersion,
} from "./local-tools/workspace-files.js";
export {
  TurnConfigResolver,
  type ToolStepTurn,
  type TurnSessionSettingsReader,
} from "./turn-config-resolver.js";
export { resolveTurnToolPolicy } from "./tool-policy.js";
