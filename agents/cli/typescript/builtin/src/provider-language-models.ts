// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { anthropic } from "@ai-sdk/anthropic";
import { openai } from "@ai-sdk/openai";
import type { LanguageModelV4 } from "@ai-sdk/provider";

/**
 * The model providers the command supports, as the prefix of a `<provider>/<model>` model ID. Each provider
 * package reads its standard environment variables: `OPENAI_API_KEY` and `OPENAI_BASE_URL`, or
 * `ANTHROPIC_API_KEY` and `ANTHROPIC_BASE_URL`.
 *
 * @since 0.5.0
 */
export const supportedModelProviders: readonly string[] = Object.freeze(["openai", "anthropic"]);

/**
 * Returns the AI SDK language model of a `<provider>/<model>` model ID, such as `openai/gpt-5.2` or
 * `anthropic/claude-sonnet-4-5`. OpenAI models use the Responses API.
 *
 * A model ID string is never passed to the AI SDK itself, which would send it to the Vercel AI Gateway.
 *
 * @param modelId - The model ID.
 * @returns The model.
 * @throws {Error} When the prefix is not one of {@link supportedModelProviders}. The message leaves the model ID
 * out, because session settings may hold it.
 * @since 0.5.0
 */
export function createProviderLanguageModel(modelId: string): LanguageModelV4 {
  const separator = modelId.indexOf("/");
  const provider = separator < 0 ? "" : modelId.slice(0, separator);
  const providerModelId = modelId.slice(separator + 1);
  if (providerModelId !== "") {
    if (provider === "openai") {
      return openai(providerModelId);
    }
    if (provider === "anthropic") {
      return anthropic(providerModelId);
    }
  }
  throw new Error("the model must be openai/<model> or anthropic/<model>");
}
