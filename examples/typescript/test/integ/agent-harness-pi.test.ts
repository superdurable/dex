/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

import assert from "node:assert/strict";

import type { Api, Model } from "@earendil-works/pi-ai";

import { createPiOrderSupportFlow } from "../../src/products/agent-harnesses/pi/pi-order-support-flow.js";
import { registerOrderSupportScenarios } from "./agent-harnesses/order-support-scenarios.js";
import { ScriptedOrderSupportMessagesApi } from "./agent-harnesses/scripted-order-support-messages-api.js";

let messagesApi: ScriptedOrderSupportMessagesApi;
const savedAnthropicApiKey = process.env.ANTHROPIC_API_KEY;

registerOrderSupportScenarios({
  harnessName: "Pi",
  createFlow: async (orderDesk, slack) => {
    messagesApi = await ScriptedOrderSupportMessagesApi.start();
    // pi-ai reads the provider key from the environment, as the example does.
    process.env.ANTHROPIC_API_KEY = "sk-ant-test-not-a-real-key";
    return createPiOrderSupportFlow(orderDesk, createScriptedAnthropicModel(messagesApi.baseUrl), slack);
  },
  close: async () => {
    if (savedAnthropicApiKey === undefined) {
      delete process.env.ANTHROPIC_API_KEY;
    } else {
      process.env.ANTHROPIC_API_KEY = savedAnthropicApiKey;
    }
    assert.deepEqual(messagesApi.unservedRequests, []);
    await messagesApi.stop();
  },
});

/** A pi-ai Anthropic Messages model whose requests go to the scripted Messages API. */
function createScriptedAnthropicModel(baseUrl: string): Model<Api> {
  return {
    id: "claude-scripted",
    name: "claude-scripted",
    api: "anthropic-messages",
    provider: "anthropic",
    baseUrl,
    reasoning: false,
    input: ["text"],
    cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
    contextWindow: 200_000,
    maxTokens: 4_096,
  };
}
