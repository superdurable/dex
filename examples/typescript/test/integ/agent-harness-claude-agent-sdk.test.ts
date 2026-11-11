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
import { mkdirSync, mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import {
  buildClaudeCliEnvironment,
  createClaudeAgentSdkOrderSupportFlow,
} from "../../src/products/agent-harnesses/claude-agent-sdk/claude-agent-sdk-order-support-flow.js";
import { registerOrderSupportScenarios } from "./agent-harnesses/order-support-scenarios.js";
import { ScriptedOrderSupportMessagesApi } from "./agent-harnesses/scripted-order-support-messages-api.js";

const stateDirectory = mkdtempSync(join(tmpdir(), "dex-typescript-examples-claude-agent-sdk-"));
let messagesApi: ScriptedOrderSupportMessagesApi;

registerOrderSupportScenarios({
  harnessName: "Claude Agent SDK",
  createFlow: async (orderDesk, slack) => {
    messagesApi = await ScriptedOrderSupportMessagesApi.start();
    mkdirSync(join(stateDirectory, "home"), { recursive: true });
    // The real bundled Claude Code CLI, in a closed environment that reaches only the scripted Messages API.
    return createClaudeAgentSdkOrderSupportFlow(orderDesk, "claude-opus-5-5", {
      PATH: "/usr/bin:/bin",
      HOME: join(stateDirectory, "home"),
      CLAUDE_CONFIG_DIR: join(stateDirectory, "claude-config"),
      ANTHROPIC_BASE_URL: messagesApi.baseUrl,
      ANTHROPIC_API_KEY: "sk-ant-test-not-a-real-key",
      CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: "1",
      DISABLE_AUTOUPDATER: "1",
      // A closed port: any request that bypasses ANTHROPIC_BASE_URL fails instead of reaching the network.
      HTTPS_PROXY: "http://127.0.0.1:9",
      HTTP_PROXY: "http://127.0.0.1:9",
      NO_PROXY: "127.0.0.1,localhost",
    }, slack);
  },
  close: async () => {
    // Other Claude credentials in the Worker's environment never reach the CLI.
    const savedEnvironment = { ...process.env };
    try {
      Object.assign(process.env, {
        ANTHROPIC_API_KEY: "sk-ant-test-worker-key",
        ANTHROPIC_BASE_URL: "http://127.0.0.1:9/anthropic",
        ANTHROPIC_AUTH_TOKEN: "another-credential",
        CLAUDE_CODE_USE_BEDROCK: "1",
        PATH: "/usr/bin:/bin",
      });
      assert.deepEqual(
        Object.keys(buildClaudeCliEnvironment()).filter((name) => name.startsWith("ANTHROPIC") || name.startsWith("CLAUDE")),
        ["ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL"],
      );
      assert.equal(buildClaudeCliEnvironment().ANTHROPIC_BASE_URL, "http://127.0.0.1:9/anthropic");
      assert.equal(buildClaudeCliEnvironment().PATH, "/usr/bin:/bin");
    } finally {
      process.env = savedEnvironment;
    }
    assert.deepEqual(messagesApi.unservedRequests, []);
    await messagesApi.stop();
    rmSync(stateDirectory, { recursive: true, force: true });
  },
});
