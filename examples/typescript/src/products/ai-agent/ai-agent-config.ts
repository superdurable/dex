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

import { randomBytes } from "node:crypto";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

import type { McpServerConfig } from "@superdurable/dex-agents/mcp";
import { parse as parseYaml } from "yaml";

import { environmentOr } from "../../config/env.js";

/** The local demo model, which needs no provider key; any `mock/<name>` model is a demo model. */
export const demoModel = "mock/dex";
const demoMiniModel = "mock/dex-mini";

/** Worker configuration of the AI Agent, shared by every session. Sessions never choose these values. */
export interface AiAgentConfig {
  /**
   * Models a session may pick when it starts; the first is the default. Each is `mock/<name>` or
   * `openai/<model>`, which reads `OPENAI_API_KEY` from the environment.
   */
  readonly models: readonly string[];
  readonly systemPrompt: string;
  readonly contextWindowTokens: number;
  /** Trusted MCP servers; a stdio command runs with the Worker's privileges. */
  readonly mcpServers: readonly McpServerConfig[];
}

const defaultSystemPrompt =
  "You are a helpful durable AI agent. Use tools when they help, explain important actions, " +
  "and never claim a tool succeeded unless its result says so.";

/** The demo MCP server shipped with the example, run over stdio when no MCP configuration is given. */
export const notesMcpServer: McpServerConfig = {
  transport: "stdio",
  name: "notes",
  command: process.execPath,
  args: [fileURLToPath(new URL("./notes-mcp-server.js", import.meta.url))],
  trustsToolAnnotations: true,
};

export function loadAiAgentConfig(): AiAgentConfig {
  const contextWindowTokens = Number(environmentOr("DEX_AGENT_CONTEXT_TOKENS", "32000"));
  if (!Number.isInteger(contextWindowTokens) || contextWindowTokens <= 0) {
    throw new RangeError("DEX_AGENT_CONTEXT_TOKENS must be a positive integer");
  }
  const mcpConfigPath = process.env.DEX_AGENT_MCP_CONFIG;
  return {
    models: listAllowedModels(environmentOr("DEX_AGENT_MODEL", demoModel), process.env.DEX_AGENT_MODELS ?? ""),
    systemPrompt: environmentOr("DEX_AGENT_SYSTEM_PROMPT", defaultSystemPrompt),
    contextWindowTokens,
    mcpServers: mcpConfigPath ? loadMcpServers(readFileSync(mcpConfigPath, "utf8")) : [notesMcpServer],
  };
}

/** Returns the default model first, then the comma-separated others, without repeats. */
function listAllowedModels(defaultModel: string, otherModels: string): string[] {
  const others = otherModels.split(",").map((model) => model.trim()).filter((model) => model !== "");
  if (others.length === 0 && defaultModel === demoModel) {
    others.push(demoMiniModel);
  }
  return [...new Set([defaultModel, ...others])];
}

/** Signs session tokens. A random key ends every token when the process restarts. */
export function loadAiAgentSessionKey(): string {
  return process.env.DEX_AGENT_SESSION_KEY || randomBytes(32).toString("hex");
}

const mcpServerKeys = new Set(["name", "transport", "command", "args", "cwd", "env", "url", "headers", "trust_read_only_annotations"]);

/** Reads the same YAML as the Python example; `env` and `headers` values name environment variables. */
export function loadMcpServers(yamlText: string): McpServerConfig[] {
  const entries = ((parseYaml(yamlText) ?? {}) as { servers?: Record<string, unknown>[] }).servers ?? [];
  return entries.map((entry) => {
    const name = String(entry.name);
    const unknownKeys = Object.keys(entry).filter((key) => !mcpServerKeys.has(key));
    if (unknownKeys.length > 0) {
      throw new TypeError(`MCP server ${name} has unsupported keys ${unknownKeys.sort().join(", ")}`);
    }
    const trustsToolAnnotations = entry.trust_read_only_annotations === true;
    if (entry.transport === "stdio") {
      const args = ((entry.args ?? []) as unknown[]).map(String);
      const env = readEnvironmentValues(entry.env);
      const cwd = entry.cwd === undefined ? {} : { cwd: String(entry.cwd) };
      return { transport: "stdio", name, command: String(entry.command), args, env, ...cwd, trustsToolAnnotations };
    }
    if (entry.transport === "streamable_http") {
      const headers = readEnvironmentValues(entry.headers);
      return { transport: "streamable_http", name, url: String(entry.url), headers, trustsToolAnnotations };
    }
    throw new TypeError(`MCP server ${name} needs transport stdio or streamable_http`);
  });
}

function readEnvironmentValues(variableNames: unknown): Record<string, string> {
  const entries = Object.entries((variableNames ?? {}) as Record<string, string>);
  const missing = entries.filter(([, variable]) => process.env[variable] === undefined).map(([, variable]) => variable);
  if (missing.length > 0) {
    throw new TypeError(`MCP configuration needs the environment variables ${missing.sort().join(", ")}`);
  }
  return Object.fromEntries(entries.map(([target, variable]) => [target, process.env[variable] ?? ""]));
}
