// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import {
  Agent,
  OpenAIProvider,
  type Model,
  type ModelProvider,
  type ModelRequest,
  type ModelSettings,
} from "@openai/agents";
import { ConfigError, type AgentHarness, type HarnessContext, type ResolvedTurn } from "@superdurable/dex-agents-cli-core";
import { createOpenAIAgentsSessionFlow } from "@superdurable/dex-agents-openai-agents";

/**
 * The base Flow type of the command's sessions, before the CLI core namespaces it.
 *
 * @since 0.5.0
 */
export const openaiSdkCliFlowType = "DexAgentsOpenAISdkCliSession";

/**
 * The model the command uses when the configuration file sets none.
 *
 * @since 0.5.0
 */
export const defaultOpenAIModel = "gpt-5.2";

/**
 * The instructions the agent gets when the configuration sets none.
 *
 * @since 0.5.0
 */
export const defaultInstructions =
  "You are a helpful assistant. Use your tools to work with the files and commands in your workspace and with " +
  "the connected services.";

const openaiModelPrefix = "openai/";

type ReasoningEffort = NonNullable<NonNullable<ModelSettings["reasoning"]>["effort"]>;

/**
 * The `dex-agents-openai-sdk` command: an OpenAI Agents SDK agent in the terminal.
 *
 * The agent offers the CLI core's connector and local tools and calls the OpenAI Responses API through the SDK's
 * `OpenAIProvider`, which reads `OPENAI_API_KEY` and `OPENAI_BASE_URL`. The model is an OpenAI model name, such as
 * `gpt-5.2`; an `openai/` prefix is accepted and dropped. Each turn uses the model and instructions its session
 * settings resolve to. The SDK's trace upload is off, because the Dex session already records every model and tool
 * call. The `openai` section has no options yet; any key in it is a configuration error.
 *
 * The `reasoning_effort` session setting takes `low`, `medium`, or `high`, and becomes the SDK's
 * `modelSettings.reasoning.effort` of every model call in the turn. The terminal UI's `/model` suggests `gpt-5.2`,
 * `gpt-5-mini`, and `gpt-5-nano`.
 *
 * Changed in 0.7.0: maps the `reasoning_effort` session setting and suggests models.
 *
 * @example
 * ```ts
 * import { processAgentCliIo, runAgentCli } from "@superdurable/dex-agents-cli-core";
 * import { openaiSdkHarness } from "@superdurable/dex-agents-openai-sdk-cli";
 *
 * process.exit(await runAgentCli(openaiSdkHarness, processAgentCliIo()));
 * ```
 * @since 0.5.0
 */
export const openaiSdkHarness: AgentHarness = {
  name: "openai-sdk",
  configSection: "openai",
  flowType: openaiSdkCliFlowType,
  defaultModel: defaultOpenAIModel,
  supportedReasoningEfforts: ["low", "medium", "high"],
  suggestedModels: ["gpt-5.2", "gpt-5-mini", "gpt-5-nano"],
  createSessionFlow: (context) => createOpenAISdkSessionFlow(context),
};

/**
 * Builds the OpenAI Agents SDK agent session Flow from the effective configuration.
 *
 * @param context - The configuration, workspace, and tool sources.
 * @returns The Flow, with `context.flowType` as its type.
 * @throws {ConfigError} When the `openai` section has a key.
 * @since 0.5.0
 */
export function createOpenAISdkSessionFlow(context: HarnessContext): ReturnType<typeof createOpenAIAgentsSessionFlow> {
  validateOpenAISection(context.harnessOptions);
  const agent = new Agent({
    name: "Assistant",
    instructions: () => context.currentTurn().instructions ?? defaultInstructions,
  });
  return createOpenAIAgentsSessionFlow(agent, context.flowType, {
    runConfig: { modelProvider: new TurnModelProvider(() => context.currentTurn()), tracingDisabled: true },
    toolSources: context.toolSources,
    ...(context.mediaStore === undefined ? {} : { mediaStore: context.mediaStore }),
    summaryAttributesIndexed: context.isSessionSummaryIndexed === true,
  });
}

/**
 * Rejects every key of the `openai` section, which has no options yet.
 *
 * @param options - The section, `{}` when the file omits it.
 * @throws {ConfigError} A `schema` error with the first key's JSON Pointer.
 * @since 0.5.0
 */
export function validateOpenAISection(options: Readonly<Record<string, unknown>>): void {
  const [firstKey] = Object.keys(options).sort();
  if (firstKey !== undefined) {
    const escaped = firstKey.replaceAll("~", "~0").replaceAll("/", "~1");
    throw new ConfigError("schema", `/openai/${escaped}`, "additionalProperties");
  }
}

/**
 * An OpenAI Agents SDK model provider that serves every model call with the model of the turn whose model call is
 * running.
 *
 * The agent sets no model, so the SDK asks the provider in each model call, and the provider ignores the name it
 * gets.
 *
 * @since 0.5.0
 */
export class TurnModelProvider implements ModelProvider {
  private readonly openaiProvider = new OpenAIProvider();

  /**
   * Creates the provider.
   *
   * @param currentTurn - Returns the running turn's configuration, such as `HarnessContext.currentTurn`.
   */
  public constructor(private readonly currentTurn: () => ResolvedTurn) {}

  /**
   * Returns the running turn's model.
   *
   * Changed in 0.7.0: when the turn has a reasoning effort, the model sets it as `modelSettings.reasoning.effort`
   * of every request, over the agent's own reasoning settings.
   *
   * @returns The SDK's OpenAI model for the turn's model name, without an `openai/` prefix, with the turn's
   * reasoning effort when it has one.
   */
  public async getModel(): Promise<Model> {
    const turn = this.currentTurn();
    const model = await this.openaiProvider.getModel(
      turn.model.startsWith(openaiModelPrefix) ? turn.model.slice(openaiModelPrefix.length) : turn.model,
    );
    return turn.reasoningEffort === undefined ? model : withReasoningEffort(model, turn.reasoningEffort);
  }
}

/** Returns the model with every request's reasoning effort set; its other members pass through. */
function withReasoningEffort(model: Model, effort: string): Model {
  const withEffort = (request: ModelRequest): ModelRequest => ({
    ...request,
    modelSettings: {
      ...request.modelSettings,
      // The CLI core accepts only the declared efforts, which the SDK's effort type includes.
      reasoning: { ...request.modelSettings.reasoning, effort: effort as ReasoningEffort },
    },
  });
  // A Proxy keeps optional members, such as getRetryAdvice, that the Runner finds by presence.
  return new Proxy(model, {
    get: (target, key) => {
      if (key === "getResponse") {
        return (request: ModelRequest) => target.getResponse(withEffort(request));
      }
      if (key === "getStreamedResponse") {
        return (request: ModelRequest) => target.getStreamedResponse(withEffort(request));
      }
      const value: unknown = Reflect.get(target, key, target);
      return typeof value === "function" ? (value as (...values: unknown[]) => unknown).bind(target) : value;
    },
  });
}
