// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { ErrorCode, McpError } from "@modelcontextprotocol/sdk/types.js";

import { ToolExecutionError } from "../agent-framework-runner.js";

/** Name of the read tool that lists a server's resources, resource templates, and prompts. */
export const mcpListResourcesAndPromptsToolName = "mcp_list_resources_and_prompts";
/** Name of the read tool that reads one resource of a server. */
export const mcpReadResourceToolName = "mcp_read_resource";
/** Name of the read tool that gets one prompt of a server. */
export const mcpGetPromptToolName = "mcp_get_prompt";

export const resourceAndPromptToolNames: ReadonlySet<string> = new Set([
  mcpListResourcesAndPromptsToolName,
  mcpReadResourceToolName,
  mcpGetPromptToolName,
]);

const listingPageLimit = 100;

/** A read tool's definition, before it becomes an `McpTool`. */
export interface ResourceAndPromptToolDefinition {
  readonly name: string;
  readonly description: string;
  readonly inputSchema: Readonly<Record<string, unknown>>;
}

/** Returns the three read tools; the `server` argument names one of `serverNames`. */
export function buildResourceAndPromptToolDefinitions(
  serverNames: readonly string[],
): ResourceAndPromptToolDefinition[] {
  const server = { type: "string", enum: [...serverNames], description: "Name of a configured MCP server." };
  const definitions: [string, string, Record<string, unknown>][] = [
    [
      mcpListResourcesAndPromptsToolName,
      "List the resources, resource templates, and prompts of a configured MCP server.",
      { server },
    ],
    [
      mcpReadResourceToolName,
      "Read one resource of a configured MCP server by its URI.",
      { server, uri: { type: "string", description: "URI of the resource, as the server lists it." } },
    ],
    [
      mcpGetPromptToolName,
      "Get one prompt of a configured MCP server, rendered with its arguments.",
      {
        server,
        name: { type: "string", description: "Name of the prompt, as the server lists it." },
        arguments: {
          type: "object",
          additionalProperties: { type: "string" },
          description: "Prompt arguments by name. Defaults to none.",
        },
      },
    ],
  ];
  return definitions.map(([name, description, properties]) => ({
    name,
    description,
    inputSchema: {
      type: "object",
      properties,
      required: Object.keys(properties).filter((key) => key !== "arguments"),
      additionalProperties: false,
    },
  }));
}

/** Returns the `server` argument, or throws the {@link ToolExecutionError} the model sees. */
export function readServerName(toolArguments: Readonly<Record<string, unknown>>): string {
  const serverName = toolArguments["server"];
  if (typeof serverName !== "string") {
    throw new ToolExecutionError("the server argument must be a string");
  }
  return serverName;
}

/** Sends one read tool's requests on an open connection and returns the output JSON text. */
export async function runResourceAndPromptTool(
  client: Client,
  toolName: string,
  toolArguments: Readonly<Record<string, unknown>>,
  requestOptions: { readonly timeout: number; readonly signal?: AbortSignal },
): Promise<string> {
  let output: Record<string, unknown>;
  try {
    if (toolName === mcpListResourcesAndPromptsToolName) {
      output = {
        resources: await listAll((cursor) => client.listResources(cursorParams(cursor), requestOptions), "resources"),
        resource_templates: await listAll(
          (cursor) => client.listResourceTemplates(cursorParams(cursor), requestOptions),
          "resourceTemplates",
        ),
        prompts: await listAll((cursor) => client.listPrompts(cursorParams(cursor), requestOptions), "prompts"),
      };
    } else if (toolName === mcpReadResourceToolName) {
      const uri = toolArguments["uri"];
      if (typeof uri !== "string") {
        throw new ToolExecutionError("the uri argument must be a string");
      }
      const read = await client.readResource({ uri }, requestOptions);
      output = { contents: read.contents };
    } else {
      const name = toolArguments["name"];
      if (typeof name !== "string") {
        throw new ToolExecutionError("the name argument must be a string");
      }
      const promptArguments = toolArguments["arguments"];
      if (
        promptArguments !== undefined &&
        promptArguments !== null &&
        (typeof promptArguments !== "object" ||
          Array.isArray(promptArguments) ||
          !Object.values(promptArguments).every((value) => typeof value === "string"))
      ) {
        throw new ToolExecutionError("the arguments argument must map names to strings");
      }
      const prompt = await client.getPrompt(
        {
          name,
          ...(promptArguments === undefined || promptArguments === null
            ? {}
            : { arguments: promptArguments as Record<string, string> }),
        },
        requestOptions,
      );
      output = { messages: prompt.messages };
      if (prompt.description !== undefined) {
        output["description"] = prompt.description;
      }
    }
  } catch (error: unknown) {
    if (error instanceof McpError) {
      throw new ToolExecutionError(`MCP error ${error.code}: ${readMcpErrorMessage(error)}`, { cause: error });
    }
    throw error;
  }
  return serializeToolOutputJson(output);
}

/**
 * Serializes with sorted keys and spaced separators, as the Python core's `json.dumps(sort_keys=True)`.
 * Keys sort by code point while the text is written, never through an object, because JavaScript objects
 * put integer-like keys first.
 */
export function serializeToolOutputJson(value: unknown): string {
  if (Array.isArray(value)) {
    return `[${value.map((item) => serializeToolOutputJson(item === undefined ? null : item)).join(", ")}]`;
  }
  if (typeof value === "object" && value !== null) {
    const entries = Object.entries(value as Record<string, unknown>)
      .filter(([, nested]) => nested !== undefined)
      .sort(([left], [right]) => compareByCodePoint(left, right));
    return `{${entries.map(([key, nested]) => `${JSON.stringify(key)}: ${serializeToolOutputJson(nested)}`).join(", ")}}`;
  }
  const text = JSON.stringify(value);
  if (text === undefined) {
    throw new TypeError(`a ${typeof value} value cannot be serialized as JSON`);
  }
  return text;
}

/** JavaScript's `<` compares UTF-16 code units, which orders characters above U+FFFF before U+E000. */
function compareByCodePoint(left: string, right: string): number {
  const leftCodePoints = Array.from(left, (character) => character.codePointAt(0) ?? 0);
  const rightCodePoints = Array.from(right, (character) => character.codePointAt(0) ?? 0);
  for (let index = 0; index < Math.min(leftCodePoints.length, rightCodePoints.length); index += 1) {
    const difference = (leftCodePoints[index] ?? 0) - (rightCodePoints[index] ?? 0);
    if (difference !== 0) {
      return difference;
    }
  }
  return leftCodePoints.length - rightCodePoints.length;
}

async function listAll<Field extends string>(
  listPage: (cursor: string | undefined) => Promise<{ readonly nextCursor?: string | undefined } & Record<Field, unknown>>,
  fieldName: Field,
): Promise<unknown[]> {
  const items: unknown[] = [];
  let cursor: string | undefined;
  for (let page = 0; page < listingPageLimit; page += 1) {
    let result: { readonly nextCursor?: string | undefined } & Record<Field, unknown>;
    try {
      result = await listPage(cursor);
    } catch (error: unknown) {
      if (error instanceof McpError && error.code === ErrorCode.MethodNotFound) {
        // The server does not offer this kind of item.
        return items;
      }
      throw error;
    }
    items.push(...(result[fieldName] as unknown[]));
    cursor = result.nextCursor;
    if (cursor === undefined || cursor === "") {
      break;
    }
  }
  return items;
}

function cursorParams(cursor: string | undefined): { cursor?: string } {
  return cursor === undefined ? {} : { cursor };
}

/** The SDK prefixes `MCP error <code>: ` to the server's message; the Python core shows the bare message. */
function readMcpErrorMessage(error: McpError): string {
  const prefix = `MCP error ${error.code}: `;
  return error.message.startsWith(prefix) ? error.message.slice(prefix.length) : error.message;
}
