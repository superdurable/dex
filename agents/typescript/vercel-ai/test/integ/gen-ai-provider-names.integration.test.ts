// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { describe, test } from "node:test";

import { toGenAIProviderName } from "../../src/gen-ai-provider-names.js";

// Provider IDs of the AI SDK provider packages and of Mastra's model router.
const genAIProviderNamesByProviderId: readonly (readonly [string, string])[] = [
  ["openai.responses", "openai"],
  ["openai.chat", "openai"],
  ["openai.completion", "openai"],
  ["anthropic.messages", "anthropic"],
  ["google.generative-ai", "gcp.gemini"],
  ["google", "gcp.gemini"],
  ["google.vertex.chat", "gcp.vertex_ai"],
  ["googleVertex.anthropic.messages", "gcp.vertex_ai"],
  ["googleVertex.xai", "gcp.vertex_ai"],
  ["vertex.maas", "gcp.vertex_ai"],
  ["amazon-bedrock", "aws.bedrock"],
  ["bedrock.anthropic.messages", "aws.bedrock"],
  ["azure.chat", "azure.ai.openai"],
  ["azure.responses", "azure.ai.openai"],
  ["mistral.chat", "mistral"],
  ["cohere.chat", "cohere"],
  ["groq.chat", "groq"],
  ["deepseek.chat", "deepseek"],
  ["perplexity", "perplexity"],
  ["perplexity-agent", "perplexity"],
  ["xai.responses", "x_ai"],
  ["xai.chat", "x_ai"],
  ["moonshotai.chat", "moonshot_ai"],
  ["moonshotai-cn", "moonshot_ai"],
];

const unknownProviderIds = ["gateway", "openai-compatible", "openai-compatible.chat", "my-proxy.chat", "dex-test"];

describe("gen_ai.provider.name of an AI SDK provider ID", () => {
  for (const [providerId, genAIProviderName] of genAIProviderNamesByProviderId) {
    test(`${providerId} is ${genAIProviderName}`, () => {
      assert.equal(toGenAIProviderName(providerId), genAIProviderName);
    });
  }

  test("a provider ID without a well-known value passes through unchanged", () => {
    assert.deepEqual(unknownProviderIds.map(toGenAIProviderName), unknownProviderIds);
  });
});
