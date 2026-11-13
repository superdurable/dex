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

// Runs one turn of an Agent Quick Start session from the command line:
//   npm run agent-quick-start -- openai-agents "Cancel order 1002"
//   npm run agent-quick-start -- openai-agents --session-id ID
// The second form resumes a session, for example after you stopped this program at the approval prompt.
// Pass --approve to approve each pending call without asking, as a coding agent does. Without a terminal
// and without --approve, the program leaves the call pending in Dex and exits, as if stopped at the prompt.

import { openai } from "@ai-sdk/openai";
import { randomUUID } from "node:crypto";
import { createInterface } from "node:readline/promises";
import { parseArgs } from "node:util";

import { startAgentSession, type AgentInterrupt } from "@superdurable/dex-agents";

import { environmentOr } from "../../../config/env.js";
import { buildClaudeCliEnvironment } from "../claude-agent-sdk/claude-agent-sdk-order-support-flow.js";
import { OrderDesk } from "../order-support/order-desk.js";
import type { OrderSupportSessionFlow } from "../order-support/order-support-routes.js";
import { loadPiOrderSupportModel } from "../pi/pi-order-support-flow.js";
import * as claudeAgentSdk from "./claude-agent-sdk-quick-start.js";
import * as mastra from "./mastra-quick-start.js";
import * as openAIAgents from "./openai-agents-quick-start.js";
import * as pi from "./pi-quick-start.js";
import { QuickStartApp, runTurn } from "./quick-start-session.js";
import * as vercelAi from "./vercel-ai-quick-start.js";

const frameworks = ["claude-agent-sdk", "mastra", "openai-agents", "pi", "vercel-ai"] as const;

function buildQuickStartAgent(framework: string, orderDesk: OrderDesk): OrderSupportSessionFlow {
  switch (framework) {
    case "claude-agent-sdk":
      return claudeAgentSdk.buildDurableAgent(
        orderDesk,
        environmentOr("DEX_CLAUDE_AGENT_SDK_MODEL", "claude-opus-5-5"),
        buildClaudeCliEnvironment(),
      );
    case "mastra":
      return mastra.buildDurableAgent(orderDesk, environmentOr("DEX_MASTRA_MODEL", "openai/gpt-5-mini"));
    case "openai-agents":
      return openAIAgents.buildDurableAgent(orderDesk, environmentOr("DEX_OPENAI_AGENTS_MODEL", "gpt-5-mini"));
    case "pi":
      return pi.buildDurableAgent(orderDesk, loadPiOrderSupportModel());
    case "vercel-ai":
      return vercelAi.buildDurableAgent(orderDesk, openai(environmentOr("DEX_VERCEL_AI_MODEL", "gpt-5-mini")));
    default:
      throw new Error(`unknown framework "${framework}"; use one of ${frameworks.join(", ")}`);
  }
}

/** No one can answer the prompt, so the call keeps waiting in Dex. */
class ApprovalLeftPending extends Error {
  constructor(readonly interrupt: AgentInterrupt) {
    super(interrupt.toolName);
  }
}

async function askForApproval(interrupt: AgentInterrupt): Promise<boolean> {
  if (!process.stdin.isTTY) {
    throw new ApprovalLeftPending(interrupt);
  }
  const prompt = createInterface({ input: process.stdin, output: process.stdout });
  try {
    const answer = await prompt.question(`${interrupt.toolName} wants to run with ${interrupt.arguments}. Approve? [y/N] `);
    return ["y", "yes"].includes(answer.trim().toLowerCase());
  } finally {
    prompt.close();
  }
}

async function approveWithoutAsking(interrupt: AgentInterrupt): Promise<boolean> {
  console.log(`Approved ${interrupt.toolName} with ${interrupt.arguments}.`);
  return true;
}

async function main(): Promise<void> {
  const { positionals, values } = parseArgs({
    allowPositionals: true,
    options: { "session-id": { type: "string" }, approve: { type: "boolean" } },
  });
  const [framework, message] = positionals;
  if (framework === undefined || (message === undefined && values["session-id"] === undefined)) {
    throw new Error(`usage: agent-quick-start <${frameworks.join("|")}> "<message>" [--session-id ID] [--approve]`);
  }
  const orderDesk = new OrderDesk(Number(environmentOr("DEX_ORDER_DESK_TOOL_DELAY_SECONDS", "0")) * 1000);
  const app = await QuickStartApp.start(
    buildQuickStartAgent(framework, orderDesk),
    environmentOr("DEX_FLOW_SERVICE_ADDRESS", "127.0.0.1:8801"),
    environmentOr("DEX_BLOB_CACHE_DIR", "/tmp/dex-agent-quick-start-blob-cache"),
  );
  try {
    let sessionId = values["session-id"];
    if (sessionId === undefined) {
      sessionId = `${framework}-quick-start-${randomUUID().slice(0, 8)}`;
      await startAgentSession(app.client, app.flow, sessionId, {});
    }
    console.log(`Session ${sessionId}`);
    let replies: string[];
    try {
      replies = await runTurn(app, sessionId, message, values.approve ? approveWithoutAsking : askForApproval);
    } catch (error) {
      if (!(error instanceof ApprovalLeftPending)) {
        throw error;
      }
      console.log(
        `${error.interrupt.toolName} waits for approval. Run again with --session-id ${sessionId} --approve to approve it.`,
      );
      return;
    }
    for (const reply of replies) {
      console.log(`Agent: ${reply}`);
    }
  } finally {
    await app.close();
  }
}

await main();
