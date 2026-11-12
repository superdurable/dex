// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { AgentSessionFlow } from "@superdurable/dex-agents";
import { createAiSdkModelClient } from "@superdurable/dex-agents/ai-sdk";
import {
  createBuiltinAgentSessionFlow,
  type BuiltinLoopAgentRunner,
  type ModelClient,
} from "@superdurable/dex-agents/builtin-loop";
import { ConfigError, type AgentHarness, type HarnessContext } from "@superdurable/dex-agents-cli-core";

import { createProviderLanguageModel } from "./provider-language-models.js";

/**
 * The base Flow type of the command's sessions, before the CLI core namespaces it.
 *
 * @since 0.5.0
 */
export const builtinCliFlowType = "DexAgentsBuiltinCliSession";

/**
 * The model the command uses when the configuration file sets none.
 *
 * @since 0.5.0
 */
export const defaultBuiltinModel = "openai/gpt-5.2";

/**
 * The instructions the agent gets when the configuration sets none.
 *
 * @since 0.5.0
 */
export const defaultInstructions =
  "You are a helpful assistant. Use your tools to work with the files and commands in your workspace and with " +
  "the connected services.";

/**
 * The `dex-agents-builtin` command: an agent run by the Dex Agents core's built-in agent loop, with no agent
 * framework, in the terminal.
 *
 * Every model call is one `RunModel` Step through the core's AI SDK model client, and every tool call is its own
 * tool Step. The agent offers the CLI core's connector and local tools and two built-in tools: `ask_user` asks the
 * user a question, and `write_todos` keeps the session's plan. Plan mode is on, so a message with `planMode` drafts a plan
 * with `write_todos` and `execute_plan` runs it. Both built-in names are reserved: no connector or local tool may use
 * them. The model ID is `openai/<model>` or `anthropic/<model>`; each turn uses the model and instructions its
 * session settings resolve to. The `builtin` section has no options yet; any key in it is a configuration error.
 *
 * Changed in 0.6.0: plan mode is on, and the agent has the `write_todos` built-in tool.
 *
 * @example
 * ```ts
 * import { processAgentCliIo, runAgentCli } from "@superdurable/dex-agents-cli-core";
 * import { builtinHarness } from "@superdurable/dex-agents-builtin-cli";
 *
 * process.exit(await runAgentCli(builtinHarness, processAgentCliIo()));
 * ```
 * @since 0.5.0
 */
export const builtinHarness: AgentHarness = {
  name: "builtin",
  configSection: "builtin",
  flowType: builtinCliFlowType,
  defaultModel: defaultBuiltinModel,
  createSessionFlow: (context) => createBuiltinSessionFlow(context),
};

/**
 * Builds the built-in loop agent session Flow from the effective configuration.
 *
 * @param context - The configuration, workspace, and tool sources.
 * @returns The Flow, with `context.flowType` as its type.
 * @throws {ConfigError} When the `builtin` section has a key.
 * @throws {Error} When the file's model is not `openai/<model>` or `anthropic/<model>`.
 * @since 0.5.0
 */
export function createBuiltinSessionFlow(context: HarnessContext): AgentSessionFlow<BuiltinLoopAgentRunner> {
  validateBuiltinSection(context.harnessOptions);
  const modelClients = new Map<string, ModelClient>();
  const modelClientFor = (modelId: string): ModelClient => {
    let client = modelClients.get(modelId);
    if (client === undefined) {
      client = createAiSdkModelClient(createProviderLanguageModel(modelId));
      modelClients.set(modelId, client);
    }
    return client;
  };
  return createBuiltinAgentSessionFlow(
    {
      name: "assistant",
      instructions: (turn) => context.resolveTurn(turn.sessionSettings ?? {}).instructions ?? defaultInstructions,
      model: modelClientFor(context.config.model),
      resolveModel: (settings) => modelClientFor(context.resolveTurn(settings).model),
      toolSources: context.toolSources,
      builtInTools: ["ask_user", "write_todos"],
      planModeEnabled: true,
    },
    context.flowType,
    {
      ...(context.mediaStore === undefined ? {} : { mediaStore: context.mediaStore }),
      summaryAttributesIndexed: context.isSessionSummaryIndexed === true,
    },
  );
}

/**
 * Rejects every key of the `builtin` section, which has no options yet.
 *
 * @param options - The section, `{}` when the file omits it.
 * @throws {ConfigError} A `schema` error with the first key's JSON Pointer.
 * @since 0.5.0
 */
export function validateBuiltinSection(options: Readonly<Record<string, unknown>>): void {
  const [firstKey] = Object.keys(options).sort();
  if (firstKey !== undefined) {
    const escaped = firstKey.replaceAll("~", "~0").replaceAll("/", "~1");
    throw new ConfigError("schema", `/builtin/${escaped}`, "additionalProperties");
  }
}
