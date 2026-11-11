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

type JsonObject = Record<string, unknown>;

/** The hidden message a resumed Claude Code CLI sends after a deferred call; never a command. */
const continuePrompt = "Continue from where you left off.";
const callAloneMarker = "again by itself";

/** One request the Claude Code CLI sent, with its credentials. */
export interface MessagesApiRequest {
  readonly path: string;
  readonly apiKey: string | undefined;
  readonly authorization: string | undefined;
  readonly body: JsonObject;
}

type ResponseBlock =
  | { readonly type: "text"; readonly text: string }
  | { readonly type: "tool_use"; readonly id: string; readonly name: string; readonly input: JsonObject };

/**
 * A scripted local Messages API for the real Claude Code CLI; the Python package's test double does the same.
 *
 * The scripted model reads commands from the last prompt text of the conversation:
 *
 * - `say <text>` answers `said: <text>`.
 * - `recall` answers with every earlier prompt.
 * - `call <tool> <json>` calls one tool; join calls with ` && ` for one reply with several tool uses. A tool name
 *   matches the request's tool list exactly or as the `mcp__<server>__<name>` suffix. Once every call has a
 *   result, it answers `<tool>: <result>` joined with `; `.
 */
export class MockMessagesApi {
  /** Every request, in arrival order. */
  public readonly requests: MessagesApiRequest[] = [];
  private readonly sockets = new Set<Socket>();

  private constructor(private readonly server: Server) {}

  /**
   * Starts the mock on a free loopback port.
   * @returns The running mock.
   */
  public static async start(): Promise<MockMessagesApi> {
    let api: MockMessagesApi | undefined;
    const server = createServer((request, response) => {
      void api?.handle(request, response);
    });
    api = new MockMessagesApi(server);
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

  /** The bodies of every Messages API request. */
  public modelRequests(): JsonObject[] {
    return this.requests.filter((request) => Array.isArray(request.body["messages"])).map((request) => request.body);
  }

  /**
   * Stops the mock.
   * @returns A Promise that resolves after it closed.
   */
  public async stop(): Promise<void> {
    for (const socket of this.sockets) {
      socket.destroy();
    }
    await new Promise<void>((resolve) => this.server.close(() => resolve()));
  }

  private async handle(request: IncomingMessage, response: ServerResponse): Promise<void> {
    const chunks: Buffer[] = [];
    for await (const chunk of request) {
      chunks.push(chunk as Buffer);
    }
    const text = Buffer.concat(chunks).toString("utf8");
    const body = (text === "" ? {} : JSON.parse(text)) as JsonObject;
    const path = (request.url ?? "").split("?")[0] ?? "";
    this.requests.push({
      path,
      apiKey: headerText(request.headers["x-api-key"]),
      authorization: headerText(request.headers.authorization),
      body,
    });
    if (request.method !== "POST" || !path.endsWith("/v1/messages")) {
      if (path.endsWith("/v1/messages/count_tokens")) {
        sendJson(response, 200, { input_tokens: 10 });
        return;
      }
      sendJson(response, 404, { type: "error", error: { type: "not_found_error", message: path } });
      return;
    }
    const message = scriptedReply(body);
    if (body["stream"] === true) {
      streamMessage(response, message);
    } else {
      sendJson(response, 200, message);
    }
  }
}

function scriptedReply(body: JsonObject): JsonObject {
  const toolNames = ((body["tools"] as JsonObject[] | undefined) ?? []).map((tool) => String(tool["name"]));
  const messages = (body["messages"] as JsonObject[] | undefined) ?? [];
  const prompts = messages.map(promptText).filter((prompt): prompt is string => prompt !== undefined);
  const command = prompts.at(-1) ?? "";
  let content: ResponseBlock[];
  if (command.startsWith("say ")) {
    content = [{ type: "text", text: `said: ${command.slice("say ".length)}` }];
  } else if (command === "recall") {
    content = [{ type: "text", text: `history: ${prompts.slice(0, -1).join(" | ")}` }];
  } else if (command.startsWith("call ")) {
    content = nextToolContent(command, messages, toolNames);
  } else {
    content = [{ type: "text", text: `unknown command: ${command.slice(0, 80)}` }];
  }
  return {
    id: `msg_${randomUUID().replaceAll("-", "").slice(0, 20)}`,
    type: "message",
    role: "assistant",
    model: "claude-mock",
    content,
    stop_reason: content.some((block) => block.type === "tool_use") ? "tool_use" : "end_turn",
    stop_sequence: null,
    usage: { input_tokens: 10, output_tokens: 5, cache_read_input_tokens: 3, cache_creation_input_tokens: 0 },
  };
}

function nextToolContent(command: string, messages: readonly JsonObject[], toolNames: readonly string[]): ResponseBlock[] {
  const requests = command.split(" && ").map((call, requestIndex) => {
    const [, requestedName = "", ...inputParts] = call.split(" ");
    const toolName =
      toolNames.find((name) => name === requestedName || name.endsWith(`__${requestedName}`)) ?? requestedName;
    return { requestIndex, toolName, toolInput: JSON.parse(inputParts.join(" ")) as JsonObject };
  });
  const toolUse = (request: (typeof requests)[number]): ResponseBlock => ({
    type: "tool_use",
    id: `toolu_${request.requestIndex}_${randomUUID().replaceAll("-", "").slice(0, 12)}`,
    name: request.toolName,
    input: request.toolInput,
  });
  const results = latestResultsByRequest(messages);
  if (results.size === 0) {
    return requests.map(toolUse);
  }
  const callAgain = requests.find((request) => (results.get(request.requestIndex) ?? "").includes(callAloneMarker));
  if (callAgain !== undefined) {
    return [toolUse(callAgain)];
  }
  const summary = requests
    .map((request) => `${request.toolName.split("__").at(-1) ?? request.toolName}: ${results.get(request.requestIndex) ?? ""}`)
    .join("; ");
  return [{ type: "text", text: summary }];
}

function latestResultsByRequest(messages: readonly JsonObject[]): Map<number, string> {
  let lastPromptIndex = -1;
  messages.forEach((message, index) => {
    if (promptText(message) !== undefined) {
      lastPromptIndex = index;
    }
  });
  const results = new Map<number, string>();
  for (const message of messages.slice(lastPromptIndex + 1)) {
    const content = message["content"];
    if (!Array.isArray(content)) {
      continue;
    }
    for (const block of content as JsonObject[]) {
      const toolUseId = String(block["tool_use_id"] ?? "");
      if (block["type"] === "tool_result" && toolUseId.startsWith("toolu_")) {
        results.set(Number(toolUseId.split("_")[1]), blockText(block["content"]));
      }
    }
  }
  return results;
}

function promptText(message: JsonObject): string | undefined {
  if (message["role"] !== "user") {
    return undefined;
  }
  const content = message["content"];
  if (typeof content === "string") {
    return content.trim() === "" ? undefined : content.trim();
  }
  if (!Array.isArray(content)) {
    return undefined;
  }
  const blocks = content as JsonObject[];
  if (blocks.some((block) => block["type"] === "tool_result")) {
    return undefined;
  }
  const commands = blocks
    .filter((block) => block["type"] === "text")
    .map((block) => String(block["text"] ?? "").trim())
    .filter((text) => text !== "" && !text.startsWith("<") && text !== continuePrompt);
  return commands.at(-1);
}

function blockText(content: unknown): string {
  if (typeof content === "string") {
    return content;
  }
  if (Array.isArray(content)) {
    return (content as JsonObject[])
      .filter((block) => block["type"] === "text")
      .map((block) => String(block["text"] ?? ""))
      .join("\n");
  }
  return "";
}

function streamMessage(response: ServerResponse, message: JsonObject): void {
  response.writeHead(200, { "content-type": "text/event-stream", "cache-control": "no-cache" });
  const send = (event: JsonObject): void => {
    response.write(`event: ${String(event["type"])}\ndata: ${JSON.stringify(event)}\n\n`);
  };
  send({ type: "message_start", message: { ...message, content: [], stop_reason: null } });
  (message["content"] as ResponseBlock[]).forEach((block, index) => {
    if (block.type === "text") {
      send({ type: "content_block_start", index, content_block: { type: "text", text: "" } });
      send({ type: "content_block_delta", index, delta: { type: "text_delta", text: block.text } });
    } else {
      send({ type: "content_block_start", index, content_block: { ...block, input: {} } });
      send({ type: "content_block_delta", index, delta: { type: "input_json_delta", partial_json: JSON.stringify(block.input) } });
    }
    send({ type: "content_block_stop", index });
  });
  send({
    type: "message_delta",
    delta: { stop_reason: message["stop_reason"], stop_sequence: null },
    usage: { output_tokens: 5 },
  });
  send({ type: "message_stop" });
  response.end();
}

function sendJson(response: ServerResponse, status: number, payload: JsonObject): void {
  const data = JSON.stringify(payload);
  response.writeHead(status, { "content-type": "application/json", "content-length": Buffer.byteLength(data) });
  response.end(data);
}

function headerText(value: string | string[] | undefined): string | undefined {
  return Array.isArray(value) ? value[0] : value;
}
