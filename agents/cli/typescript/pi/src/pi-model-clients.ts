// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { Api, Model } from "@earendil-works/pi-ai";
import { getEnvApiKey, getModel } from "@earendil-works/pi-ai/compat";
import type { ModelClient } from "@superdurable/dex-agents/builtin-loop";
import { createPiAiModelClient } from "@superdurable/dex-agents-pi";

/** What pi-ai's `getEnvApiKey` returns for ambient credentials, such as an AWS profile. */
const ambientCredentialsMarker = "<authenticated>";

// Errors never echo the model ID: a turn's error reaches snapshots, and the ID may come from session settings.

/** The API key variables of common providers, named in the missing-key error. */
const apiKeyVariablesByProvider: Readonly<Record<string, string>> = Object.freeze({
  anthropic: "ANTHROPIC_API_KEY",
  openai: "OPENAI_API_KEY",
  google: "GEMINI_API_KEY",
  mistral: "MISTRAL_API_KEY",
  groq: "GROQ_API_KEY",
  xai: "XAI_API_KEY",
  openrouter: "OPENROUTER_API_KEY",
  deepseek: "DEEPSEEK_API_KEY",
});

/**
 * One parsed model ID of the `dex-agents-pi` command: a pi-ai provider and the provider's model ID.
 *
 * @since 0.5.0
 */
export interface PiModelId {
  /** The pi-ai provider, such as `anthropic` or `openai`. */
  readonly provider: string;
  /** The model ID within the provider, such as `claude-opus-5-5`. It may contain `/`, as OpenRouter IDs do. */
  readonly modelId: string;
}

/**
 * Splits a `<provider>/<model>` model ID at its first `/`.
 *
 * @example
 * ```ts
 * parsePiModelId("openrouter/anthropic/claude-sonnet-5"); // { provider: "openrouter", modelId: "anthropic/claude-sonnet-5" }
 * ```
 * @param model - The model ID from the configuration, `--model`, or the session settings.
 * @returns The provider and the model ID.
 * @throws {Error} When the text has no provider or no model part.
 * @since 0.5.0
 */
export function parsePiModelId(model: string): PiModelId {
  const separator = model.indexOf("/");
  if (separator <= 0 || separator === model.length - 1) {
    throw new Error("the model is not <provider>/<model>; use a pi-ai provider and model, such as anthropic/claude-opus-5-5");
  }
  return { provider: model.slice(0, separator), modelId: model.slice(separator + 1) };
}

/**
 * Returns the API key that the command sends to a provider, read from the environment as pi-ai names it.
 *
 * Only API key variables count. For `anthropic` that is `ANTHROPIC_API_KEY`; the OAuth token variables and Pi's
 * stored logins are never read. Other providers use pi-ai's `getEnvApiKey`, such as `OPENAI_API_KEY` for `openai`.
 * Providers that pi-ai authenticates with ambient credentials, such as an AWS profile for Bedrock, need no key.
 *
 * @param provider - The pi-ai provider.
 * @param environment - The environment to read.
 * @returns The key, or `undefined` when ambient credentials authenticate the provider.
 * @throws {Error} When the provider has no key in the environment; the message names the variable.
 * @since 0.5.0
 */
export function resolvePiProviderApiKey(provider: string, environment: NodeJS.ProcessEnv): string | undefined {
  const apiKey =
    provider === "anthropic" ? environment["ANTHROPIC_API_KEY"] : getEnvApiKey(provider, environment as Record<string, string>);
  if (apiKey === ambientCredentialsMarker) {
    return undefined;
  }
  if (apiKey === undefined || apiKey.trim() === "") {
    const variable = apiKeyVariablesByProvider[provider];
    throw new Error(
      variable === undefined
        ? "no API key for the model's provider: set the API key environment variable that pi-ai reads for it"
        : `no API key for the model's provider: set ${variable}`,
    );
  }
  return apiKey;
}

/**
 * Builds and caches one pi-ai model client per model ID, for the session Flow's `model` and `resolveModel`.
 *
 * @since 0.5.0
 */
export class PiModelClients {
  private readonly clients = new Map<string, ModelClient>();

  /**
   * Creates the cache.
   *
   * @param providerBaseUrls - Base URLs that replace the catalog's, by provider.
   * @param environment - The environment the API keys come from.
   */
  public constructor(
    private readonly providerBaseUrls: Readonly<Record<string, string>>,
    private readonly environment: NodeJS.ProcessEnv,
  ) {}

  /**
   * Returns the model client of a model ID, building it on first use. It reads only pi-ai's model catalog and the
   * environment, so it is cheap enough for every `RunModel` attempt.
   *
   * @param model - The `<provider>/<model>` model ID.
   * @returns The client.
   * @throws {Error} When the ID is malformed, pi-ai does not know the model, or the provider's API key is missing.
   */
  public clientFor(model: string): ModelClient {
    const cached = this.clients.get(model);
    if (cached !== undefined) {
      return cached;
    }
    const { provider, modelId } = parsePiModelId(model);
    const catalogModel = (getModel as (provider: string, modelId: string) => Model<Api> | undefined)(provider, modelId);
    if (catalogModel === undefined) {
      throw new Error("pi-ai's model catalog does not have the model");
    }
    const baseUrl = this.providerBaseUrls[provider];
    const apiKey = resolvePiProviderApiKey(provider, this.environment);
    const client = createPiAiModelClient(
      baseUrl === undefined ? catalogModel : { ...catalogModel, baseUrl },
      apiKey === undefined ? {} : { apiKey },
    );
    this.clients.set(model, client);
    return client;
  }
}
