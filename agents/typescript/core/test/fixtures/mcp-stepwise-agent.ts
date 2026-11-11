// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0


import type { AgentSessionFlow } from "../../src/index.js";
import { McpToolSource } from "../../src/mcp/index.js";
import {
  ScriptedModelClient,
  createBuiltinAgentSessionFlow,
  functionTool,
  type AgentDefinition,
  type ModelRequest,
  type ScriptedModelReply,
  type BuiltinLoopAgentRunner,
} from "../../src/builtin-loop/index.js";
import { fakeMcpServerModulePath, fakeMcpStateDirectoryVariable } from "./fake-mcp-server-names.js";

/** Flow type of the Stepwise agent whose tools come from a function tool and an `McpToolSource`. */
export const mcpStepwiseAgentFlowType = "McpStepwiseAgent";

/** A function tool next to the MCP tools. */
export const takeNote = functionTool({
  name: "take_note",
  description: "Keep a note for later.",
  parameters: { type: "object", properties: { text: { type: "string" } }, required: ["text"] },
  effect: "read",
  execute: ({ text }) => `kept ${String(text)}`,
});

/**
 * The scripted demo model: `call <tool> <json>` calls that tool and answers `<tool>: <output>` once the result
 * arrives; any other text is answered `said: <text>`.
 */
export function replyFromCommands(request: ModelRequest): ScriptedModelReply {
  const results = ScriptedModelClient.toolResults(request);
  if (results.length > 0) {
    return { text: results.map((result) => `${result.toolName}: ${result.output}`).join("; ") };
  }
  const command = ScriptedModelClient.userTexts(request).at(-1) ?? "";
  if (!command.startsWith("call ")) {
    return { text: `said: ${command}` };
  }
  const request_ = command.slice("call ".length);
  const separator = request_.indexOf(" ");
  return { toolCalls: [{ toolName: request_.slice(0, separator), arguments: request_.slice(separator + 1) }] };
}

/** The agent: a function tool, an MCP source with the resource and prompt tools, and `ask_user`. */
export function createMcpStepwiseDefinition(stateDirectory: string, httpUrl: string): AgentDefinition {
  const tools = new McpToolSource(
    [
      {
        transport: "stdio",
        name: "local",
        command: process.execPath,
        args: [fakeMcpServerModulePath, "stdio"],
        env: { [fakeMcpStateDirectoryVariable]: stateDirectory },
      },
      { transport: "streamable_http", name: "remote", url: httpUrl },
    ],
    { resourceAndPromptToolsEnabled: true },
  );
  return {
    name: "mcp-stepwise",
    instructions: "You are a scripted test agent.",
    model: new ScriptedModelClient(replyFromCommands),
    tools: [takeNote],
    toolSources: [tools],
    builtInTools: ["ask_user"],
  };
}

/** Builds the agent's session Flow. */
export function createMcpStepwiseFlow(definition: AgentDefinition): AgentSessionFlow<BuiltinLoopAgentRunner> {
  return createBuiltinAgentSessionFlow(definition, mcpStepwiseAgentFlowType);
}
