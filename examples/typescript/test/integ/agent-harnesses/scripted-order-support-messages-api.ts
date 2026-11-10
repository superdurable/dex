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

import { randomUUID } from "node:crypto";
import { createServer, type IncomingMessage, type Server, type ServerResponse } from "node:http";
import type { AddressInfo } from "node:net";

import { decideOrderSupportReply } from "./order-support-script.js";

type JsonObject = Record<string, unknown>;

type ContentBlock =
  | { readonly type: "text"; readonly text: string }
  | { readonly type: "tool_use"; readonly id: string; readonly name: string; readonly input: JsonObject };

/** Text the Claude Code CLI adds when it resumes a deferred tool call; it is not a user command. */
const resumePromptText = "Continue from where you left off.";

/**
 * A local Messages API that follows the order-support script, so the Claude Agent SDK example runs the real bundled
 * Claude Code CLI without a model key. Point `ANTHROPIC_BASE_URL` at {@link baseUrl}.
 */
export class ScriptedOrderSupportMessagesApi {
  private constructor(
    private readonly server: Server,
    /** Requests for paths this API does not serve. */
    public readonly unservedRequests: readonly string[],
  ) {}

  public static async start(): Promise<ScriptedOrderSupportMessagesApi> {
    const unservedRequests: string[] = [];
    const server = createServer((request, response) => {
      void answerRequest(request, response, unservedRequests);
    });
    await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
    return new ScriptedOrderSupportMessagesApi(server, unservedRequests);
  }

  public get baseUrl(): string {
    return `http://127.0.0.1:${(this.server.address() as AddressInfo).port}`;
  }

  public async stop(): Promise<void> {
    this.server.closeAllConnections();
    await new Promise<void>((resolve) => this.server.close(() => resolve()));
  }
}

async function answerRequest(request: IncomingMessage, response: ServerResponse, unservedRequests: string[]): Promise<void> {
  const body = await readBody(request);
  const path = (request.url ?? "").split("?")[0] ?? "";
  if (request.method === "POST" && path.endsWith("/v1/messages/count_tokens")) {
    sendJson(response, { input_tokens: 10 });
    return;
  }
  if (request.method !== "POST" || !path.endsWith("/v1/messages")) {
    unservedRequests.push(`${request.method ?? "?"} ${path}`);
    response.writeHead(404, { "content-type": "application/json" });
    response.end(JSON.stringify({ type: "error", error: { type: "not_found_error", message: path } }));
    return;
  }
  const payload = JSON.parse(body) as JsonObject;
  const messages = (payload.messages as JsonObject[] | undefined) ?? [];
  const toolNames = ((payload.tools as JsonObject[] | undefined) ?? []).map((tool) => String(tool.name));
  const blocks = replyToMessages(messages, toolNames);
  const model = String(payload.model ?? "claude-scripted");
  if (payload.stream === true) {
    streamMessage(response, model, blocks);
  } else {
    sendJson(response, messageObject(model, blocks, true));
  }
}

function replyToMessages(messages: readonly JsonObject[], toolNames: readonly string[]): ContentBlock[] {
  let commandIndex = -1;
  let userText = "";
  messages.forEach((message, index) => {
    if (message.role !== "user") {
      return;
    }
    const texts = contentBlocksOf(message)
      .filter((block) => block.type === "text")
      .map((block) => stripReminders(String(block.text)))
      .filter((text) => text !== "" && text !== resumePromptText);
    if (texts.length > 0) {
      commandIndex = index;
      userText = texts.at(-1) ?? "";
    }
  });
  if (commandIndex < 0) {
    // A side request, such as a title, that carries no user command.
    return [{ type: "text", text: "ok" }];
  }
  const toolNamesById = new Map<string, string>();
  const toolResults = new Map<string, string>();
  for (const block of messages.slice(commandIndex + 1).flatMap(contentBlocksOf)) {
    if (block.type === "tool_use") {
      toolNamesById.set(String(block.id), bareToolName(String(block.name)));
    } else if (block.type === "tool_result") {
      const toolName = toolNamesById.get(String(block.tool_use_id));
      if (toolName !== undefined) {
        toolResults.set(toolName, toolResultText(block.content));
      }
    }
  }
  const reply = decideOrderSupportReply(userText, toolResults);
  if (reply.kind === "answer") {
    return [{ type: "text", text: reply.text }];
  }
  // The CLI offers custom tools as mcp__<server>__<tool>.
  const fullToolName = toolNames.find((name) => bareToolName(name) === reply.toolName) ?? reply.toolName;
  const id = `toolu_${randomUUID().replaceAll("-", "").slice(0, 20)}`;
  return [{ type: "tool_use", id, name: fullToolName, input: { orderId: reply.orderId } }];
}

function streamMessage(response: ServerResponse, model: string, blocks: readonly ContentBlock[]): void {
  response.writeHead(200, { "content-type": "text/event-stream", "cache-control": "no-cache" });
  const send = (event: string, data: JsonObject): void => {
    response.write(`event: ${event}\ndata: ${JSON.stringify({ type: event, ...data })}\n\n`);
  };
  send("message_start", { message: messageObject(model, [], false) });
  blocks.forEach((block, index) => {
    if (block.type === "text") {
      send("content_block_start", { index, content_block: { type: "text", text: "" } });
      send("content_block_delta", { index, delta: { type: "text_delta", text: block.text } });
    } else {
      send("content_block_start", { index, content_block: { ...block, input: {} } });
      send("content_block_delta", { index, delta: { type: "input_json_delta", partial_json: JSON.stringify(block.input) } });
    }
    send("content_block_stop", { index });
  });
  send("message_delta", { delta: { stop_reason: stopReasonOf(blocks), stop_sequence: null }, usage: { output_tokens: 5 } });
  send("message_stop", {});
  response.end();
}

function messageObject(model: string, blocks: readonly ContentBlock[], isComplete: boolean): JsonObject {
  return {
    id: `msg_${randomUUID().replaceAll("-", "").slice(0, 20)}`,
    type: "message",
    role: "assistant",
    model,
    content: blocks,
    stop_reason: isComplete ? stopReasonOf(blocks) : null,
    stop_sequence: null,
    usage: { input_tokens: 10, output_tokens: isComplete ? 5 : 1 },
  };
}

function stopReasonOf(blocks: readonly ContentBlock[]): string {
  return blocks.some((block) => block.type === "tool_use") ? "tool_use" : "end_turn";
}

function bareToolName(name: string): string {
  return name.split("__").at(-1) ?? name;
}

function contentBlocksOf(message: JsonObject): JsonObject[] {
  const content = message.content;
  if (typeof content === "string") {
    return [{ type: "text", text: content }];
  }
  return Array.isArray(content) ? content.filter((block): block is JsonObject => typeof block === "object" && block !== null) : [];
}

function toolResultText(content: unknown): string {
  const text =
    typeof content === "string"
      ? content
      : Array.isArray(content)
        ? content.map((block: JsonObject) => (block.type === "text" ? String(block.text) : "")).join("\n")
        : "";
  return stripReminders(text);
}

/** The CLI adds `<system-reminder>` blocks to prompts and tool results; they are not part of the script. */
function stripReminders(text: string): string {
  return text.replace(/<system-reminder>[\s\S]*?<\/system-reminder>/g, "").trim();
}

function sendJson(response: ServerResponse, payload: JsonObject): void {
  response.writeHead(200, { "content-type": "application/json" });
  response.end(JSON.stringify(payload));
}

function readBody(request: IncomingMessage): Promise<string> {
  return new Promise((resolve, reject) => {
    const chunks: Buffer[] = [];
    request.on("data", (chunk: Buffer) => chunks.push(chunk));
    request.on("end", () => resolve(Buffer.concat(chunks).toString("utf8")));
    request.on("error", reject);
  });
}
