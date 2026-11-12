// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { ConfigError, type AgentHarness, type HarnessContext } from "@superdurable/dex-agents-cli-core";
import { createVercelAiAgentSessionFlow } from "@superdurable/dex-agents-vercel-ai";

import { createProviderLanguageModel } from "./provider-language-models.js";
import { TurnLanguageModel } from "./turn-language-model.js";

/**
 * The base Flow type of the command's sessions, before the CLI core namespaces it.
 *
 * @since 0.5.0
 */
export const vercelAiCliFlowType = "DexAgentsVercelAiCliSession";

/**
 * The model the command uses when the configuration file sets none.
 *
 * @since 0.5.0
 */
export const defaultVercelAiModel = "openai/gpt-5.2";

/**
 * The instructions the agent gets when the configuration sets none.
 *
 * @since 0.5.0
 */
export const defaultInstructions =
  "You are a helpful assistant. Use your tools to work with the files and commands in your workspace and with " +
  "the connected services.";

/**
 * The `dex-agents-vercel-ai` command: a Vercel AI SDK agent in the terminal.
 *
 * The agent offers the CLI core's connector and local tools. The model ID is `openai/<model>` or
 * `anthropic/<model>`; each turn uses the model and instructions its session settings resolve to. The
 * `vercel_ai` section has no options yet; any key in it is a configuration error.
 *
 * The `reasoning_effort` session setting takes `low`, `medium`, or `high`, and becomes the AI SDK's `reasoning`
 * call option of every model call in the turn. The terminal UI's `/model` suggests `openai/gpt-5.2`,
 * `openai/gpt-5-mini`, `anthropic/claude-opus-5-5`, and `anthropic/claude-sonnet-5-5`.
 *
 * Changed in 0.7.0: maps the `reasoning_effort` session setting and suggests models.
 *
 * @example
 * ```ts
 * import { processAgentCliIo, runAgentCli } from "@superdurable/dex-agents-cli-core";
 * import { vercelAiHarness } from "@superdurable/dex-agents-vercel-ai-cli";
 *
 * process.exit(await runAgentCli(vercelAiHarness, processAgentCliIo()));
 * ```
 * @since 0.5.0
 */
export const vercelAiHarness: AgentHarness = {
  name: "vercel-ai",
  configSection: "vercel_ai",
  flowType: vercelAiCliFlowType,
  defaultModel: defaultVercelAiModel,
  supportedReasoningEfforts: ["low", "medium", "high"],
  suggestedModels: ["openai/gpt-5.2", "openai/gpt-5-mini", "anthropic/claude-opus-5-5", "anthropic/claude-sonnet-5-5"],
  createSessionFlow: (context) => createVercelAiCliSessionFlow(context),
};

/**
 * Builds the Vercel AI SDK agent session Flow from the effective configuration.
 *
 * @param context - The configuration, workspace, and tool sources.
 * @returns The Flow, with `context.flowType` as its type.
 * @throws {ConfigError} When the `vercel_ai` section has a key.
 * @throws {Error} When the file's model is not `openai/<model>` or `anthropic/<model>`.
 * @since 0.5.0
 */
export function createVercelAiCliSessionFlow(context: HarnessContext): ReturnType<typeof createVercelAiAgentSessionFlow> {
  validateVercelAiSection(context.harnessOptions);
  // Fail at startup, not at the first turn, when the file's model has no supported provider.
  createProviderLanguageModel(context.config.model);
  const model = new TurnLanguageModel(() => context.currentTurn(), createProviderLanguageModel, defaultInstructions);
  return createVercelAiAgentSessionFlow(
    { model, toolSources: context.toolSources },
    context.flowType,
    {
      ...(context.mediaStore === undefined ? {} : { mediaStore: context.mediaStore }),
      summaryAttributesIndexed: context.isSessionSummaryIndexed === true,
    },
  );
}

/**
 * Rejects every key of the `vercel_ai` section, which has no options yet.
 *
 * @param options - The section, `{}` when the file omits it.
 * @throws {ConfigError} A `schema` error with the first key's JSON Pointer.
 * @since 0.5.0
 */
export function validateVercelAiSection(options: Readonly<Record<string, unknown>>): void {
  const [firstKey] = Object.keys(options).sort();
  if (firstKey !== undefined) {
    const escaped = firstKey.replaceAll("~", "~0").replaceAll("/", "~1");
    throw new ConfigError("schema", `/vercel_ai/${escaped}`, "additionalProperties");
  }
}
