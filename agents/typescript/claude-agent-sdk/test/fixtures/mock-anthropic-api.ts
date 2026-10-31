// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { randomUUID } from "node:crypto";
import { createServer, type IncomingMessage, type Server, type ServerResponse } from "node:http";
import type { AddressInfo, Socket } from "node:net";

/** Prefix that marks a scripted prompt; the real CLI adds its own text around user prompts. */
export const mockScriptMarker = "dex-script: ";

const callAloneMarker = "again by itself";

type JsonObject = Record<string, unknown>;

/** One request the real CLI sent to the mock Messages API. */
export interface MockApiRequest {
  readonly path: string;
  readonly isStreaming: boolean;
  readonly toolNames: readonly string[];
  /** The scripted command the request answered, or `undefined` for a side request. */
  readonly command: string | undefined;
  /** Every user text of the request after the last scripted prompt, such as a resume prompt. */
  readonly textsAfterCommand: readonly string[];
  /** The response's content block types, such as `["tool_use", "tool_use"]`. */
  readonly responseBlockTypes: readonly string[];
}

interface ScriptedToolRequest {
  readonly requestIndex: number;
  readonly toolName: string;
  readonly toolInput: JsonObject;
}

type ResponseBlock =
  | { readonly type: "text"; readonly text: string }
  | { readonly type: "tool_use"; readonly id: string; readonly name: string; readonly input: JsonObject };

/** A local scripted Messages API that streams SSE, so the real CLI runs offline; see the README. */
export class MockAnthropicApi {
  /** Every Messages API request, in arrival order. */
  public readonly requests: MockApiRequest[] = [];
  /** Requests for paths the mock does not serve. */
  public readonly unservedPaths: string[] = [];
  private readonly sockets = new Set<Socket>();

  private constructor(private readonly server: Server) {}

  public static async start(): Promise<MockAnthropicApi> {
    let api: MockAnthropicApi | undefined;
    const server = createServer((request, response) => {
      void api?.handle(request, response);
    });
    api = new MockAnthropicApi(server);
    server.on("connection", (socket) => {
      api?.sockets.add(socket);
      socket.on("close", () => api?.sockets.delete(socket));
    });
    await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
    return api;
  }

  /** Base URL for `ANTHROPIC_BASE_URL`. */
  public get baseUrl(): string {
    return `http://127.0.0.1:${(this.server.address() as AddressInfo).port}`;
  }

  /** Requests that answered a scripted command, in order. */
  public scriptedRequests(): MockApiRequest[] {
    return this.requests.filter((request) => request.command !== undefined);
  }

  public async stop(): Promise<void> {
    for (const socket of this.sockets) {
      socket.destroy();
    }
    await new Promise<void>((resolve) => this.server.close(() => resolve()));
  }

  private async handle(request: IncomingMessage, response: ServerResponse): Promise<void> {
    const body = await readBody(request);
    const path = (request.url ?? "").split("?")[0] ?? "";
    if (request.method !== "POST" || !path.endsWith("/v1/messages")) {
      if (path.endsWith("/v1/messages/count_tokens")) {
        sendJson(response, { input_tokens: 10 });
        return;
      }
      this.unservedPaths.push(`${request.method ?? "?"} ${path}`);
      response.writeHead(404, { "content-type": "application/json" });
      response.end(JSON.stringify({ type: "error", error: { type: "not_found_error", message: path } }));
      return;
    }
    const payload = JSON.parse(body) as JsonObject;
    const messages = (payload["messages"] as JsonObject[] | undefined) ?? [];
    const tools = ((payload["tools"] as JsonObject[] | undefined) ?? []).map((tool) => String(tool["name"]));
    const script = findScript(messages);
    const blocks = script === undefined ? [{ type: "text" as const, text: "ok" }] : decideBlocks(script, tools);
    this.requests.push({
      path,
      isStreaming: payload["stream"] === true,
      toolNames: tools,
      command: script?.command,
      textsAfterCommand: script?.textsAfterCommand ?? [],
      responseBlockTypes: blocks.map((block) => block.type),
    });
    const model = String(payload["model"] ?? "claude-scripted");
    if (payload["stream"] === true) {
      streamMessage(response, model, blocks);
    } else {
      sendJson(response, messageObject(model, blocks, true));
    }
  }
}

interface FoundScript {
  readonly command: string;
  readonly earlierCommands: readonly string[];
  readonly tail: readonly JsonObject[];
  readonly textsAfterCommand: readonly string[];
}

function findScript(messages: readonly JsonObject[]): FoundScript | undefined {
  const commands: { readonly index: number; readonly command: string }[] = [];
  messages.forEach((message, index) => {
    if (message["role"] !== "user") {
      return;
    }
    for (const text of userTextsOf(message)) {
      const markerIndex = text.lastIndexOf(mockScriptMarker);
      if (markerIndex >= 0) {
        commands.push({ index, command: text.slice(markerIndex + mockScriptMarker.length).split("\n")[0]?.trim() ?? "" });
      }
    }
  });
  const last = commands.at(-1);
  if (last === undefined) {
    return undefined;
  }
  const tail = messages.slice(last.index + 1);
  return {
    command: last.command,
    earlierCommands: commands.slice(0, -1).map((entry) => entry.command),
    tail,
    textsAfterCommand: tail.filter((message) => message["role"] === "user").flatMap(userTextsOf),
  };
}

function decideBlocks(script: FoundScript, toolNames: readonly string[]): ResponseBlock[] {
  const command = script.command;
  if (command.startsWith("say ")) {
    return [{ type: "text", text: `said: ${command.slice("say ".length)}` }];
  }
  if (command === "recall") {
    return [{ type: "text", text: `history: ${script.earlierCommands.join(" | ")}` }];
  }
  if (!command.startsWith("call ")) {
    return [{ type: "text", text: `unknown command ${command}` }];
  }
  const requests = parseToolRequests(command);
  const results = toolResultsByRequest(script.tail);
  let pending: readonly ScriptedToolRequest[] = [];
  if (results.size === 0) {
    pending = requests;
  } else {
    const refused = requests.find((request) => results.get(request.requestIndex)?.includes(callAloneMarker) === true);
    pending = refused === undefined ? [] : [refused];
  }
  if (pending.length > 0) {
    return pending.map((request) => ({
      type: "tool_use",
      id: `toolu_${request.requestIndex}_${randomUUID().replaceAll("-", "").slice(0, 12)}`,
      name: toolNames.find((name) => name === request.toolName || name.endsWith(`__${request.toolName}`)) ?? request.toolName,
      input: request.toolInput,
    }));
  }
  const summary = requests
    .map((request) => `${request.toolName}: ${results.get(request.requestIndex) ?? "no result"}`)
    .join("; ");
  return [{ type: "text", text: summary }];
}

function parseToolRequests(command: string): ScriptedToolRequest[] {
  return command.split(" && ").map((call, requestIndex) => {
    const [, toolName = "", ...inputParts] = call.split(" ");
    return { requestIndex, toolName, toolInput: JSON.parse(inputParts.join(" ")) as JsonObject };
  });
}

function toolResultsByRequest(tail: readonly JsonObject[]): Map<number, string> {
  const resultsByToolUseId = new Map<string, string>();
  for (const block of tail.flatMap(contentBlocksOf)) {
    if (block["type"] === "tool_result") {
      resultsByToolUseId.set(String(block["tool_use_id"]), toolResultContentText(block["content"]));
    }
  }
  const latest = new Map<number, string>();
  for (const block of tail.flatMap(contentBlocksOf)) {
    const result = block["type"] === "tool_use" ? resultsByToolUseId.get(String(block["id"])) : undefined;
    if (result !== undefined) {
      latest.set(Number(String(block["id"]).split("_")[1]), result);
    }
  }
  return latest;
}

function streamMessage(response: ServerResponse, model: string, blocks: readonly ResponseBlock[]): void {
  response.writeHead(200, { "content-type": "text/event-stream", "cache-control": "no-cache" });
  const send = (event: string, data: JsonObject): void => {
    response.write(`event: ${event}\ndata: ${JSON.stringify(data)}\n\n`);
  };
  send("message_start", { type: "message_start", message: messageObject(model, [], false) });
  blocks.forEach((block, index) => {
    if (block.type === "text") {
      send("content_block_start", { type: "content_block_start", index, content_block: { type: "text", text: "" } });
      const half = Math.floor(block.text.length / 2);
      for (const chunk of [block.text.slice(0, half), block.text.slice(half)]) {
        if (chunk !== "") {
          send("content_block_delta", { type: "content_block_delta", index, delta: { type: "text_delta", text: chunk } });
        }
      }
    } else {
      send("content_block_start", {
        type: "content_block_start",
        index,
        content_block: { type: "tool_use", id: block.id, name: block.name, input: {} },
      });
      send("content_block_delta", {
        type: "content_block_delta",
        index,
        delta: { type: "input_json_delta", partial_json: JSON.stringify(block.input) },
      });
    }
    send("content_block_stop", { type: "content_block_stop", index });
  });
  const stopReason = blocks.some((block) => block.type === "tool_use") ? "tool_use" : "end_turn";
  send("message_delta", {
    type: "message_delta",
    delta: { stop_reason: stopReason, stop_sequence: null },
    usage: { output_tokens: 5 },
  });
  send("message_stop", { type: "message_stop" });
  response.end();
}

function messageObject(model: string, blocks: readonly ResponseBlock[], isComplete: boolean): JsonObject {
  return {
    id: `msg_${randomUUID().replaceAll("-", "").slice(0, 20)}`,
    type: "message",
    role: "assistant",
    model,
    content: blocks,
    stop_reason: isComplete ? (blocks.some((block) => block.type === "tool_use") ? "tool_use" : "end_turn") : null,
    stop_sequence: null,
    usage: { input_tokens: 10, output_tokens: isComplete ? 5 : 1, cache_read_input_tokens: 3, cache_creation_input_tokens: 0 },
  };
}

function userTextsOf(message: JsonObject): string[] {
  const content = message["content"];
  if (typeof content === "string") {
    return [content];
  }
  return Array.isArray(content)
    ? content.filter((block: JsonObject) => block?.["type"] === "text").map((block: JsonObject) => String(block["text"]))
    : [];
}

function contentBlocksOf(message: JsonObject): JsonObject[] {
  const content = message["content"];
  return Array.isArray(content) ? content.filter((block): block is JsonObject => typeof block === "object" && block !== null) : [];
}

function toolResultContentText(content: unknown): string {
  const text =
    typeof content === "string"
      ? content
      : Array.isArray(content)
        ? content.map((block: JsonObject) => (block?.["type"] === "text" ? String(block["text"]) : "")).join("\n")
        : "";
  // The real CLI appends reminders to tool results it sends to the model.
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

/** An HTTP proxy that refuses every connection and records where the CLI tried to go. */
export class RefusingProxy {
  /** Hosts the CLI tried to reach through the proxy. */
  public readonly refusedTargets: string[] = [];

  private constructor(private readonly server: Server) {}

  public static async start(): Promise<RefusingProxy> {
    let proxy: RefusingProxy | undefined;
    const server = createServer((request, response) => {
      proxy?.refusedTargets.push(request.url ?? "?");
      response.writeHead(403);
      response.end();
    });
    proxy = new RefusingProxy(server);
    server.on("connect", (request: IncomingMessage, socket: Socket) => {
      proxy?.refusedTargets.push(request.url ?? "?");
      socket.end("HTTP/1.1 403 Forbidden\r\n\r\n");
    });
    await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
    return proxy;
  }

  public get url(): string {
    return `http://127.0.0.1:${(this.server.address() as AddressInfo).port}`;
  }

  public async stop(): Promise<void> {
    this.server.closeAllConnections();
    await new Promise<void>((resolve) => this.server.close(() => resolve()));
  }
}
