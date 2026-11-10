// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { McpSdkServerConfigWithInstance } from "@anthropic-ai/claude-agent-sdk";
import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import {
  getParseErrorMessage,
  normalizeObjectSchema,
  safeParseAsync,
  type AnySchema,
  type ZodRawShapeCompat,
} from "@modelcontextprotocol/sdk/server/zod-compat.js";
import { toJsonSchemaCompat } from "@modelcontextprotocol/sdk/server/zod-json-schema-compat.js";
import {
  CallToolRequestSchema,
  ErrorCode,
  ListToolsRequestSchema,
  McpError,
  type CallToolResult,
  type Tool,
} from "@modelcontextprotocol/sdk/types.js";

import type { ClaudeCustomTool, ToolCallGate, ToolCatalog } from "./claude-tool-call-gate.js";

const replayOnlyToolDescription = "Not available now: its tool source does not offer it.";
const emptyObjectJsonSchema: Tool["inputSchema"] = { type: "object", properties: {} };

/**
 * Builds the in-process MCP server that serves custom tools, source tools, and replay-only tools.
 *
 * `createSdkMcpServer` takes only zod schemas, so a source's JSON Schema needs these low-level handlers.
 */
export function createClaudeToolServer(
  toolCatalog: ToolCatalog,
  toolCallGate: ToolCallGate,
): McpSdkServerConfigWithInstance {
  const serverName = toolCatalog.customToolServerName;
  const server = new McpServer({ name: serverName, version: "1.0.0" }, { capabilities: { tools: {} } });
  const listedTools: Tool[] = [
    ...toolCatalog.customTools.flatMap((customTool) => listCustomTool(customTool)),
    // No annotations: the CLI runs unannotated MCP tools one at a time, in the model's order.
    ...toolCatalog.sourceTools.map((sourceTool) => ({
      name: sourceTool.definition.name,
      description: sourceTool.definition.description,
      inputSchema: sourceTool.definition.inputSchema as Tool["inputSchema"],
    })),
    // Served so the CLI can resume a recorded call; the gate refuses new calls.
    ...toolCatalog.replayOnlyToolNames.map((toolName) => ({
      name: toolName,
      description: replayOnlyToolDescription,
      inputSchema: { type: "object" as const },
    })),
  ];
  server.server.setRequestHandler(ListToolsRequestSchema, () => ({ tools: listedTools }));
  server.server.setRequestHandler(CallToolRequestSchema, async (request, extra) => {
    const toolName = request.params.name;
    if (!toolCatalog.isServedToolName(toolName)) {
      return toolErrorResult(new McpError(ErrorCode.InvalidParams, `Tool ${toolName} not found`).message);
    }
    const customTool = toolCatalog.customTool(toolName);
    const runCustomTool =
      customTool === undefined
        ? undefined
        : async (): Promise<CallToolResult> => {
            const toolArguments = await parseInlineArguments(customTool, request.params.arguments);
            return customTool.handler(toolArguments, extra);
          };
    try {
      return await toolCallGate.callServedTool(toolName, cliToolUseIdOf(extra._meta), runCustomTool);
    } catch (error: unknown) {
      return toolErrorResult(error instanceof Error ? error.message : String(error));
    }
  });
  return { type: "sdk", name: serverName, instance: server };
}

// As McpServer lists a tool; a schema it cannot convert hides the tool, as createSdkMcpServer does.
function listCustomTool(customTool: ClaudeCustomTool): Tool[] {
  let inputSchema: Tool["inputSchema"];
  try {
    const objectSchema = normalizeObjectSchema(customTool.inputSchema as ZodRawShapeCompat);
    inputSchema =
      objectSchema === undefined
        ? emptyObjectJsonSchema
        : (toJsonSchemaCompat(objectSchema, { strictUnions: true, pipeStrategy: "input" }) as Tool["inputSchema"]);
  } catch (error: unknown) {
    console.warn(`custom tool ${customTool.name} is hidden: its input schema has no JSON Schema: ${String(error)}`);
    return [];
  }
  return [
    {
      name: customTool.name,
      description: customTool.description,
      inputSchema,
      ...(customTool.annotations === undefined ? {} : { annotations: customTool.annotations }),
      execution: { taskSupport: "forbidden" },
      ...(customTool._meta === undefined ? {} : { _meta: customTool._meta }),
    },
  ];
}

// The checks and error text of McpServer, so an inline custom tool behaves as under createSdkMcpServer.
async function parseInlineArguments(customTool: ClaudeCustomTool, toolArguments: unknown): Promise<unknown> {
  const objectSchema = normalizeObjectSchema(customTool.inputSchema as ZodRawShapeCompat);
  const schemaToParse = (objectSchema ?? customTool.inputSchema) as AnySchema;
  const parsed = await safeParseAsync(schemaToParse, toolArguments ?? {});
  if (!parsed.success) {
    throw new McpError(
      ErrorCode.InvalidParams,
      `Input validation error: Invalid arguments for tool ${customTool.name}: ${getParseErrorMessage(parsed.error)}`,
    );
  }
  return parsed.data;
}

function toolErrorResult(text: string): CallToolResult {
  return { content: [{ type: "text", text }], isError: true };
}

function cliToolUseIdOf(meta: unknown): string | undefined {
  if (typeof meta !== "object" || meta === null) {
    return undefined;
  }
  const toolUseId = (meta as Record<string, unknown>)["claudecode/toolUseId"];
  return typeof toolUseId === "string" && toolUseId !== "" ? toolUseId : undefined;
}
