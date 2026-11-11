// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { Agent } from "@mastra/core/agent";
import { ConfigError, type AgentHarness, type HarnessContext } from "@superdurable/dex-agents-cli";
import { createMastraAgentSessionFlow } from "@superdurable/dex-agents-mastra";

/**
 * The base Flow type of the command's sessions, before the runtime namespaces it.
 *
 * @since 0.5.0
 */
export const mastraCliFlowType = "DexAgentsMastraCliSession";

/**
 * The model the command uses when the configuration file sets none.
 *
 * @since 0.5.0
 */
export const defaultMastraModel = "openai/gpt-5.2";

/**
 * The instructions the agent gets when the configuration sets none.
 *
 * @since 0.5.0
 */
export const defaultInstructions =
  "You are a helpful assistant. Use your tools to work with the files and commands in your workspace and with " +
  "the connected services.";

/**
 * The `dex-agents-mastra` command: a Mastra agent in the terminal.
 *
 * The agent offers the runtime's connector and local tools. The model ID is any Mastra model router ID, such as
 * `openai/gpt-5.2` or `anthropic/claude-sonnet-4-5`, and Mastra reads the provider's standard API key variable.
 * Each turn uses the model and instructions its session settings resolve to. The `mastra` section has no options
 * yet; any key in it is a configuration error.
 *
 * @example
 * ```ts
 * import { processAgentCliIo, runAgentCli } from "@superdurable/dex-agents-cli";
 * import { mastraHarness } from "@superdurable/dex-agents-mastra-cli";
 *
 * process.exit(await runAgentCli(mastraHarness, processAgentCliIo()));
 * ```
 * @since 0.5.0
 */
export const mastraHarness: AgentHarness = {
  name: "mastra",
  configSection: "mastra",
  flowType: mastraCliFlowType,
  defaultModel: defaultMastraModel,
  createSessionFlow: (context) => createMastraCliSessionFlow(context),
};

/**
 * Builds the Mastra agent session Flow from the effective configuration.
 *
 * The agent's model and instructions are functions, which Mastra calls in every model call, so each turn gets its
 * own.
 *
 * @param context - The configuration, workspace, and tool sources.
 * @returns The Flow, with `context.flowType` as its type.
 * @throws {ConfigError} When the `mastra` section has a key.
 * @since 0.5.0
 */
export function createMastraCliSessionFlow(context: HarnessContext): ReturnType<typeof createMastraAgentSessionFlow> {
  validateMastraSection(context.harnessOptions);
  const agent = new Agent({
    id: "assistant",
    name: "Assistant",
    instructions: () => context.currentTurn().instructions ?? defaultInstructions,
    model: () => context.currentTurn().model,
  });
  return createMastraAgentSessionFlow(agent, context.flowType, {
    toolSources: context.toolSources,
    ...(context.mediaStore === undefined ? {} : { mediaStore: context.mediaStore }),
  });
}

/**
 * Rejects every key of the `mastra` section, which has no options yet.
 *
 * @param options - The section, `{}` when the file omits it.
 * @throws {ConfigError} A `schema` error with the first key's JSON Pointer.
 * @since 0.5.0
 */
export function validateMastraSection(options: Readonly<Record<string, unknown>>): void {
  const [firstKey] = Object.keys(options).sort();
  if (firstKey !== undefined) {
    const escaped = firstKey.replaceAll("~", "~0").replaceAll("/", "~1");
    throw new ConfigError("schema", `/mastra/${escaped}`, "additionalProperties");
  }
}
