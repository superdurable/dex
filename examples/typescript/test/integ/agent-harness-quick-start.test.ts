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

// The Agent Quick Start agents, each in its own Worker and Client, on a real Dex server.
// A cancellation waits for approval and runs once. A session whose Worker stops while the
// cancellation waits finishes on a new Worker, which runs the cancellation once.

import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { mkdirSync, mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { after, before, describe, test } from "node:test";

import { readAgentSnapshot, sendAgentUserMessage, startAgentSession, type AgentSessionFlow } from "@superdurable/dex-agents";
import { pollUntil } from "@superdurable/dex-agents/testing";

import { OrderDesk } from "../../src/products/agent-harnesses/order-support/order-desk.js";
import * as claudeAgentSdk from "../../src/products/agent-harnesses/quick-start/claude-agent-sdk-quick-start.js";
import * as mastra from "../../src/products/agent-harnesses/quick-start/mastra-quick-start.js";
import * as openAIAgents from "../../src/products/agent-harnesses/quick-start/openai-agents-quick-start.js";
import { QuickStartApp, runTurn } from "../../src/products/agent-harnesses/quick-start/quick-start-session.js";
import * as vercelAi from "../../src/products/agent-harnesses/quick-start/vercel-ai-quick-start.js";
import { scriptedCancelReason, scriptedReplyPrefix } from "./agent-harnesses/order-support-script.js";
import { createScriptedOrderSupportLanguageModel } from "./agent-harnesses/scripted-order-support-language-model.js";
import { ScriptedOrderSupportMessagesApi } from "./agent-harnesses/scripted-order-support-messages-api.js";
import { ScriptedOrderSupportOpenAIModel } from "./agent-harnesses/scripted-order-support-openai-model.js";

process.env.MASTRA_TELEMETRY_DISABLED = "1";
const serverAddress = process.env.DEX_FLOW_SERVICE_ADDRESS ?? "127.0.0.1:8801";
const stateDirectory = mkdtempSync(join(tmpdir(), "dex-typescript-agent-quick-start-"));
const cancelledReply = `${scriptedReplyPrefix}Order 1002 is cancelled: ${scriptedCancelReason}.`;
let messagesApi: ScriptedOrderSupportMessagesApi;

before(async () => {
  messagesApi = await ScriptedOrderSupportMessagesApi.start();
  mkdirSync(join(stateDirectory, "home"), { recursive: true });
});

after(async () => {
  await messagesApi.stop();
  rmSync(stateDirectory, { recursive: true, force: true });
});

const frameworks: Record<string, (orderDesk: OrderDesk) => AgentSessionFlow> = {
  "Claude Agent SDK": (orderDesk) =>
    claudeAgentSdk.buildDurableAgent(orderDesk, "claude-opus-5-5", {
      PATH: "/usr/bin:/bin",
      HOME: join(stateDirectory, "home"),
      CLAUDE_CONFIG_DIR: join(stateDirectory, "claude-config"),
      ANTHROPIC_BASE_URL: messagesApi.baseUrl,
      ANTHROPIC_API_KEY: "sk-ant-test-not-a-real-key",
      CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: "1",
      DISABLE_AUTOUPDATER: "1",
      HTTPS_PROXY: "http://127.0.0.1:9",
      HTTP_PROXY: "http://127.0.0.1:9",
      NO_PROXY: "127.0.0.1,localhost",
    }),
  Mastra: (orderDesk) => mastra.buildDurableAgent(orderDesk, createScriptedOrderSupportLanguageModel()),
  "OpenAI Agents SDK": (orderDesk) => openAIAgents.buildDurableAgent(orderDesk, new ScriptedOrderSupportOpenAIModel()),
  "Vercel AI SDK": (orderDesk) => vercelAi.buildDurableAgent(orderDesk, createScriptedOrderSupportLanguageModel()),
};

for (const [name, buildDurableAgent] of Object.entries(frameworks)) {
  describe(`Agent Quick Start: ${name}`, () => {
    test("a cancellation waits for one approval and runs once", async () => {
      const orderDesk = new OrderDesk();
      const app = await QuickStartApp.start(buildDurableAgent(orderDesk), serverAddress, join(stateDirectory, randomUUID()));
      const approvals: string[] = [];
      try {
        const sessionId = `quick-start-test-${randomUUID()}`;
        await startAgentSession(app.client, app.flow, sessionId, {});
        const lookUp = await runTurn(app, sessionId, "Look up order 1001", async () => true);
        assert.equal(lookUp.at(-1), `${scriptedReplyPrefix}Order 1001: 2 notebooks, shipped.`);
        const replies = await runTurn(app, sessionId, "Cancel order 1002", async (interrupt) => {
          approvals.push(interrupt.toolName);
          return true;
        });
        assert.equal(replies.at(-1), cancelledReply);
        assert.deepEqual(approvals, ["cancel_order"]);
        assert.deepEqual(orderDesk.cancelledOrderIds, ["1002"]);
      } finally {
        await app.close();
      }
    });

    test("a cancellation runs once after the Worker restarts while it waits for approval", async () => {
      const firstOrderDesk = new OrderDesk();
      const first = await QuickStartApp.start(buildDurableAgent(firstOrderDesk), serverAddress, join(stateDirectory, randomUUID()));
      const sessionId = `quick-start-test-${randomUUID()}`;
      try {
        await startAgentSession(first.client, first.flow, sessionId, {});
        await sendAgentUserMessage(first.client, first.flow, sessionId, { text: "Cancel order 1002" });
        await pollUntil(
          async () => (await readAgentSnapshot(first.client, first.flow, sessionId)).pendingInterrupts[0],
          { description: "the cancellation to wait for approval", timeoutMs: 90_000 },
        );
      } finally {
        await first.close();
      }
      const secondOrderDesk = new OrderDesk();
      const second = await QuickStartApp.start(buildDurableAgent(secondOrderDesk), serverAddress, join(stateDirectory, randomUUID()));
      try {
        const replies = await runTurn(second, sessionId, undefined, async () => true);
        assert.equal(replies.at(-1), cancelledReply);
        assert.deepEqual(firstOrderDesk.cancelledOrderIds, []);
        assert.deepEqual(secondOrderDesk.cancelledOrderIds, ["1002"]);
      } finally {
        await second.close();
      }
    });
  });
}
