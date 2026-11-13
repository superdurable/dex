// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type {
  LanguageModelV4,
  LanguageModelV4CallOptions,
  LanguageModelV4GenerateResult,
  LanguageModelV4StreamResult,
} from "@ai-sdk/provider";
import type { ResolvedTurn } from "@superdurable/dex-agents-cli-core";

/**
 * An AI SDK language model that serves each call with the model and instructions of the turn whose model call is
 * running.
 *
 * The Vercel AI SDK adapter takes one model and fixed instructions for the whole Flow, while a CLI session may
 * change both per turn through its session settings. This model reads the running turn on every call, delegates
 * to that turn's model, and adds the turn's instructions as the system message when the prompt has none.
 *
 * When the turn has a reasoning effort, each call sets it as the `reasoning` call option. Otherwise the call keeps
 * its own `reasoning`, so the model's default applies.
 *
 * Changed in 0.6.2: sets the turn's reasoning effort.
 *
 * @example
 * ```ts
 * const model = new TurnLanguageModel(() => context.currentTurn(), createProviderLanguageModel, "Help the user.");
 * const flow = createVercelAiAgentSessionFlow({ model, toolSources: context.toolSources }, context.flowType);
 * ```
 * @since 0.5.0
 */
export class TurnLanguageModel implements LanguageModelV4 {
  /** The AI SDK specification version. */
  public readonly specificationVersion = "v4";
  private readonly models = new Map<string, LanguageModelV4>();

  /**
   * Creates the model.
   *
   * @param currentTurn - Returns the running turn's configuration, such as `HarnessContext.currentTurn`.
   * @param createModel - Returns the language model of a model ID; its result is cached per ID.
   * @param defaultInstructions - The instructions of a turn that sets none.
   */
  public constructor(
    private readonly currentTurn: () => ResolvedTurn,
    private readonly createModel: (modelId: string) => LanguageModelV4,
    private readonly defaultInstructions: string,
  ) {}

  /** The running turn's model provider. */
  public get provider(): string {
    return this.turnModel().provider;
  }

  /** The running turn's provider model ID. */
  public get modelId(): string {
    return this.turnModel().modelId;
  }

  /** The URLs the running turn's model accepts. */
  public get supportedUrls(): LanguageModelV4["supportedUrls"] {
    return this.turnModel().supportedUrls;
  }

  /**
   * Generates with the running turn's model.
   *
   * @param options - The call options.
   * @returns The model's result.
   */
  public doGenerate(options: LanguageModelV4CallOptions): PromiseLike<LanguageModelV4GenerateResult> {
    return this.turnModel().doGenerate(this.withTurnOptions(options));
  }

  /**
   * Streams with the running turn's model.
   *
   * @param options - The call options.
   * @returns The model's stream.
   */
  public doStream(options: LanguageModelV4CallOptions): PromiseLike<LanguageModelV4StreamResult> {
    return this.turnModel().doStream(this.withTurnOptions(options));
  }

  private turnModel(): LanguageModelV4 {
    const modelId = this.currentTurn().model;
    let model = this.models.get(modelId);
    if (model === undefined) {
      model = this.createModel(modelId);
      this.models.set(modelId, model);
    }
    return model;
  }

  private withTurnOptions(options: LanguageModelV4CallOptions): LanguageModelV4CallOptions {
    const reasoningEffort = this.currentTurn().reasoningEffort;
    const withInstructions = this.withInstructions(options);
    return reasoningEffort === undefined
      ? withInstructions
      : // The CLI core accepts only the declared efforts, which the call option's type includes.
        { ...withInstructions, reasoning: reasoningEffort as NonNullable<LanguageModelV4CallOptions["reasoning"]> };
  }

  private withInstructions(options: LanguageModelV4CallOptions): LanguageModelV4CallOptions {
    if (options.prompt.some((message) => message.role === "system")) {
      return options;
    }
    const instructions = this.currentTurn().instructions ?? this.defaultInstructions;
    return { ...options, prompt: [{ role: "system", content: instructions }, ...options.prompt] };
  }
}
