// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { createHash } from "node:crypto";
import { join } from "node:path";

import {
  LocalDirectoryMediaStore,
  createAgentSessionFlow,
  describeToolCallOutcome,
  loadImages,
  type AgentFrameworkRunner,
  type AgentSessionFlow,
  type AgentTurnContext,
  type AgentTurnOutcome,
  type DeferredToolExecution,
  type DeferredToolOutcome,
  type FrameworkMessage,
  type MediaStore,
  type ToolTaskCancellation,
  type ToolTaskPoll,
  type ToolTaskPollOutcome,
} from "../../src/index.js";
import { McpToolSource } from "../../src/mcp/index.js";
import { fakeMcpServerModulePath, fakeMcpStateDirectoryVariable } from "./fake-mcp-server-names.js";

/** Flow type of the MCP scripted agent. */
export const mcpScriptedAgentFlowType = "McpScriptedAgent";
/** Environment variable carrying the Streamable HTTP MCP server URL to the Worker child. */
export const mcpAgentHttpUrlVariable = "MCP_AGENT_HTTP_URL";
/** Environment variable that, when set, adds a `gateway` server with this URL. */
export const mcpAgentGatewayUrlVariable = "MCP_AGENT_GATEWAY_URL";
/** Environment variable that, when set, adds a `sessions` server, one that keeps protocol sessions. */
export const mcpAgentSessionsUrlVariable = "MCP_AGENT_SESSIONS_URL";
/**
 * A policy wins over the tool's readOnlyHint annotation, and raises the effect a tool declares in the Dex
 * `_meta` keys.
 */
export const mcpAgentToolPolicies = {
  local__audit_lookup: { effect: "non_idempotent_write" },
  remote__get_balance: { effect: "non_idempotent_write" },
} as const;

const usage = { modelCalls: 1, inputTokens: 10, outputTokens: 5, cachedInputTokens: 0, reasoningTokens: 0 };

type RequestedCall = readonly [toolCallId: string, toolName: string, toolArguments: string];

/**
 * A scripted runner whose tools all come from an {@link McpToolSource}.
 *
 * The model reads `call <tool> <json>` from the user message, joined with ` && call ` for parallel calls,
 * and asks for those MCP tools. The second model call answers `<tool>: <result>` per call, joined with
 * `; `. The `local` server runs the fake MCP server over stdio; `remote` is the Streamable HTTP URL. Tasks of
 * a server without protocol sessions are polled through `pollToolTask`. Images of MCP results go to a
 * `LocalDirectoryMediaStore` under the state directory; the second model call loads them and adds
 * `image <sha256> <size>` per image, computed from the loaded bytes, to that call's summary.
 */
export class McpScriptedAgentRunner implements AgentFrameworkRunner<DeferredToolOutcome> {
  public constructor(
    private readonly tools: McpToolSource,
    private readonly mediaStore: MediaStore,
  ) {}

  public buildUserMessage(text: string): FrameworkMessage {
    return message("user", text);
  }

  public async runModelStep(turn: AgentTurnContext): Promise<AgentTurnOutcome> {
    if (turn.frameworkRunState !== null) {
      return this.answerWithResults(turn, JSON.parse(turn.frameworkRunState) as RequestedCall[]);
    }
    const command = (JSON.parse(turn.messages.at(-1)?.frameworkItem ?? "{}") as { text: string }).text;
    const offered = new Set((await this.tools.listTools()).map((tool) => tool.name));
    const calls = command
      .replace(/^call /, "")
      .split(" && call ")
      .map((request, index): RequestedCall => {
        const separator = request.indexOf(" ");
        const toolName = request.slice(0, separator);
        if (!offered.has(toolName)) {
          throw new Error(`the model was not offered ${toolName}`);
        }
        return [`${turn.turnId}-call-${index}`, toolName, request.slice(separator + 1)];
      });
    return {
      kind: "tool_calls_requested",
      frameworkRunState: JSON.stringify(calls),
      toolCalls: calls.map(([toolCallId, toolName, toolArguments]) =>
        this.tools.deferredToolCall(turn, { toolCallId, toolName, arguments: toolArguments }),
      ),
      usage,
    };
  }

  public async executeDeferredToolCall(call: DeferredToolExecution): Promise<DeferredToolOutcome> {
    const handler = (await this.tools.deferredToolHandlers())[call.toolCall.toolName];
    if (handler === undefined) {
      throw new Error(`no handler for ${call.toolCall.toolName}`);
    }
    return handler(call);
  }

  public pollToolTask(poll: ToolTaskPoll): Promise<ToolTaskPollOutcome> {
    return this.tools.pollToolTask(poll);
  }

  public cancelToolTask(cancellation: ToolTaskCancellation): Promise<void> {
    return this.tools.cancelToolTask(cancellation);
  }

  private async answerWithResults(turn: AgentTurnContext, calls: readonly RequestedCall[]): Promise<AgentTurnOutcome> {
    const newMessages: FrameworkMessage[] = [];
    const summaries: string[] = [];
    for (const [toolCallId, toolName, toolArguments] of calls) {
      const record = turn.recordedToolCall(toolCallId);
      if (record === undefined) {
        throw new Error(`no record for ${toolCallId}`);
      }
      const result = describeToolCallOutcome(record);
      const images = record.images ?? [];
      newMessages.push({
        role: "tool",
        text: null,
        toolCallId,
        toolName,
        toolArguments,
        toolOutput: result,
        frameworkItem: JSON.stringify(
          images.length === 0 ? { toolCallId, result } : { toolCallId, result, images: images.map((image) => image.uri) },
        ),
        ...(images.length === 0 ? {} : { images }),
      });
      const loaded = await loadImages(this.mediaStore, images);
      const seen = images.map((image) => {
        const data = loaded.get(image.sha256) ?? new Uint8Array();
        return `image ${createHash("sha256").update(data).digest("hex")} ${data.length}`;
      });
      summaries.push([`${toolName}: ${result}`, ...seen].join(" "));
    }
    newMessages.push(message("assistant", summaries.join("; ")));
    return { kind: "completed", newMessages, usage };
  }
}

/** Builds the tool source with the `local` stdio server and the `remote` HTTP server. */
export function createMcpToolSource(
  stateDirectory: string,
  httpUrl: string,
  gatewayUrl?: string,
  sessionsUrl?: string,
): McpToolSource {
  return new McpToolSource([
    {
      transport: "stdio",
      name: "local",
      command: process.execPath,
      args: [fakeMcpServerModulePath, "stdio"],
      env: { [fakeMcpStateDirectoryVariable]: stateDirectory },
    },
    { transport: "streamable_http", name: "remote", url: httpUrl },
    ...(gatewayUrl === undefined ? [] : [{ transport: "streamable_http" as const, name: "gateway", url: gatewayUrl }]),
    ...(sessionsUrl === undefined ? [] : [{ transport: "streamable_http" as const, name: "sessions", url: sessionsUrl }]),
  ], { mediaStore: createMcpMediaStore(stateDirectory) });
}

/** Builds the store that holds the images of MCP results, under the state directory. */
export function createMcpMediaStore(stateDirectory: string): MediaStore {
  return new LocalDirectoryMediaStore(join(stateDirectory, "media"));
}

/** Builds the MCP scripted agent's session Flow. */
export function createMcpScriptedAgentFlow(
  stateDirectory: string,
  httpUrl: string,
  gatewayUrl?: string,
  sessionsUrl?: string,
): AgentSessionFlow<McpScriptedAgentRunner> {
  return createAgentSessionFlow(
    new McpScriptedAgentRunner(
      createMcpToolSource(stateDirectory, httpUrl, gatewayUrl, sessionsUrl),
      createMcpMediaStore(stateDirectory),
    ),
    mcpScriptedAgentFlowType,
    { toolPolicies: mcpAgentToolPolicies },
  );
}

function message(role: "user" | "assistant", text: string): FrameworkMessage {
  return {
    role,
    text,
    toolCallId: null,
    toolName: null,
    toolArguments: null,
    toolOutput: null,
    frameworkItem: JSON.stringify({ text }),
  };
}
