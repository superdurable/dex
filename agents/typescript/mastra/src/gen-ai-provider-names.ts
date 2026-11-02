// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// AI SDK and Mastra router provider IDs whose provider the pinned GenAI conventions name.
const genAIProviderNamesByProviderIdPrefix: ReadonlyMap<string, string> = new Map([
  ["openai", "openai"],
  ["anthropic", "anthropic"],
  ["google", "gcp.gemini"],
  ["google.generative-ai", "gcp.gemini"],
  ["google.vertex", "gcp.vertex_ai"],
  ["google-vertex", "gcp.vertex_ai"],
  ["googlevertex", "gcp.vertex_ai"],
  ["vertex", "gcp.vertex_ai"],
  ["amazon-bedrock", "aws.bedrock"],
  ["bedrock", "aws.bedrock"],
  ["azure", "azure.ai.openai"],
  ["mistral", "mistral"],
  ["cohere", "cohere"],
  ["groq", "groq"],
  ["deepseek", "deepseek"],
  ["perplexity", "perplexity"],
  ["perplexity-agent", "perplexity"],
  ["xai", "x_ai"],
  ["moonshotai", "moonshot_ai"],
  ["moonshotai-cn", "moonshot_ai"],
]);

/**
 * Maps a provider ID such as `openai.responses` to its well-known `gen_ai.provider.name`.
 * @returns The value for the provider ID's longest known dot-separated prefix, or the provider ID unchanged.
 */
export function toGenAIProviderName(providerId: string): string {
  const segments = providerId.toLowerCase().split(".");
  for (let segmentCount = segments.length; segmentCount > 0; segmentCount -= 1) {
    const genAIProviderName = genAIProviderNamesByProviderIdPrefix.get(segments.slice(0, segmentCount).join("."));
    if (genAIProviderName !== undefined) {
      return genAIProviderName;
    }
  }
  return providerId;
}
