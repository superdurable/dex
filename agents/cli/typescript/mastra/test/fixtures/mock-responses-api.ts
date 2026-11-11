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

/** One request a model client sent, with its credentials. */
export interface ResponsesApiRequest {
  readonly path: string;
  readonly authorization: string | undefined;
  readonly body: JsonObject;
}

/**
 * A scripted local OpenAI Responses API for the command's real model client; every TypeScript CLI package has a
 * copy.
 *
 * The scripted model reads commands from the last user text of the conversation:
 *
 * - `say <text>` answers `said: <text>`.
 * - `recall` answers with every earlier user text.
 * - `call <tool> <json>` calls one tool. Once the call has a result, it answers `<tool>: <result>`.
 *
 * It keeps every output item it sent, so an `item_reference` input resolves.
 */
export class MockResponsesApi {
  /** Every request, in arrival order. */
  public readonly requests: ResponsesApiRequest[] = [];
  private readonly sockets = new Set<Socket>();
  private readonly sentItems = new Map<string, JsonObject>();

  private constructor(private readonly server: Server) {}

  /**
   * Starts the mock on a free loopback port.
   * @returns The running mock.
   */
  public static async start(): Promise<MockResponsesApi> {
    let api: MockResponsesApi | undefined;
    const server = createServer((request, response) => {
      void api?.handle(request, response);
    });
    api = new MockResponsesApi(server);
    server.on("connection", (socket) => {
      api?.sockets.add(socket);
      socket.on("close", () => api?.sockets.delete(socket));
    });
    await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
    return api;
  }

  /** Base URL for `OPENAI_BASE_URL`, ending in `/v1`. */
  public get baseUrl(): string {
    return `http://127.0.0.1:${(this.server.address() as AddressInfo).port}/v1`;
  }

  /** The bodies of every Responses API request. */
  public modelRequests(): JsonObject[] {
    return this.requests.filter((request) => request.body["input"] !== undefined).map((request) => request.body);
  }

  /** The tool names the last model request offered. */
  public lastOfferedToolNames(): string[] {
    const tools = (this.modelRequests().at(-1)?.["tools"] as JsonObject[] | undefined) ?? [];
    return tools.map((tool) => String(tool["name"]));
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
    const authorization = request.headers.authorization;
    this.requests.push({ path, authorization: Array.isArray(authorization) ? authorization[0] : authorization, body });
    if (request.method !== "POST" || !path.endsWith("/responses")) {
      sendJson(response, 404, { error: { type: "not_found", message: path } });
      return;
    }
    const output = this.scriptedOutput(body);
    for (const item of output) {
      this.sentItems.set(String(item["id"]), item);
    }
    const completed = {
      id: `resp_${randomUUID().replaceAll("-", "").slice(0, 20)}`,
      object: "response",
      created_at: Math.floor(Date.now() / 1000),
      status: "completed",
      model: String(body["model"] ?? "mock-model"),
      output,
      incomplete_details: null,
      error: null,
      usage: {
        input_tokens: 10,
        input_tokens_details: { cached_tokens: 3 },
        output_tokens: 5,
        output_tokens_details: { reasoning_tokens: 0 },
        total_tokens: 15,
      },
    };
    if (body["stream"] === true) {
      streamResponse(response, completed);
    } else {
      sendJson(response, 200, completed);
    }
  }

  private scriptedOutput(body: JsonObject): JsonObject[] {
    const input = this.resolveInput(body["input"]);
    const prompts = input.map(userText).filter((prompt): prompt is string => prompt !== undefined);
    const command = prompts.at(-1) ?? "";
    if (command.startsWith("say ")) {
      return [messageItem(`said: ${command.slice("say ".length)}`)];
    }
    if (command === "recall") {
      return [messageItem(`history: ${prompts.slice(0, -1).join(" | ")}`)];
    }
    if (command.startsWith("call ")) {
      const [, toolName = "", ...inputParts] = command.split(" ");
      const result = latestToolResult(input);
      if (result === undefined) {
        return [
          {
            type: "function_call",
            id: `fc_${randomUUID().replaceAll("-", "").slice(0, 16)}`,
            call_id: `call_${randomUUID().replaceAll("-", "").slice(0, 16)}`,
            name: toolName,
            arguments: JSON.stringify(JSON.parse(inputParts.join(" "))),
            status: "completed",
          },
        ];
      }
      return [messageItem(`${toolName}: ${result}`)];
    }
    return [messageItem(`unknown command: ${command.slice(0, 80)}`)];
  }

  private resolveInput(input: unknown): JsonObject[] {
    if (typeof input === "string") {
      return [{ role: "user", content: input }];
    }
    if (!Array.isArray(input)) {
      return [];
    }
    return (input as JsonObject[]).map((item) =>
      item["type"] === "item_reference" ? (this.sentItems.get(String(item["id"])) ?? item) : item,
    );
  }
}

function messageItem(text: string): JsonObject {
  return {
    type: "message",
    id: `msg_${randomUUID().replaceAll("-", "").slice(0, 16)}`,
    role: "assistant",
    status: "completed",
    content: [{ type: "output_text", text, annotations: [] }],
  };
}

/** Returns the text of a user message, or `undefined` for any other item or a Dex note in angle brackets. */
function userText(item: JsonObject): string | undefined {
  if (item["role"] !== "user") {
    return undefined;
  }
  const content = item["content"];
  const texts =
    typeof content === "string"
      ? [content]
      : Array.isArray(content)
        ? (content as JsonObject[])
            .filter((part) => part["type"] === "input_text" || part["type"] === "text")
            .map((part) => String(part["text"] ?? ""))
        : [];
  const commands = texts.map((text) => text.trim()).filter((text) => text !== "" && !text.startsWith("<"));
  return commands.at(-1);
}

function latestToolResult(input: readonly JsonObject[]): string | undefined {
  let lastPromptIndex = -1;
  input.forEach((item, index) => {
    if (userText(item) !== undefined) {
      lastPromptIndex = index;
    }
  });
  let result: string | undefined;
  for (const item of input.slice(lastPromptIndex + 1)) {
    if (item["type"] === "function_call_output") {
      result = outputText(item["output"]);
    }
  }
  return result;
}

function outputText(output: unknown): string {
  if (typeof output === "string") {
    return output;
  }
  if (Array.isArray(output)) {
    return (output as JsonObject[])
      .filter((part) => part["type"] === "input_text" || part["type"] === "output_text" || part["type"] === "text")
      .map((part) => String(part["text"] ?? ""))
      .join("\n");
  }
  return JSON.stringify(output);
}

function streamResponse(response: ServerResponse, completed: JsonObject): void {
  response.writeHead(200, { "content-type": "text/event-stream", "cache-control": "no-cache" });
  let sequenceNumber = 0;
  const send = (event: JsonObject): void => {
    const data = { ...event, sequence_number: sequenceNumber++ };
    response.write(`event: ${String(event["type"])}\ndata: ${JSON.stringify(data)}\n\n`);
  };
  send({ type: "response.created", response: { ...completed, status: "in_progress", output: [], usage: null } });
  (completed["output"] as JsonObject[]).forEach((item, outputIndex) => {
    const itemId = String(item["id"]);
    if (item["type"] === "message") {
      const text = String(((item["content"] as JsonObject[])[0] ?? {})["text"] ?? "");
      send({
        type: "response.output_item.added",
        output_index: outputIndex,
        item: { ...item, status: "in_progress", content: [] },
      });
      const part = { type: "output_text", text: "", annotations: [] };
      send({ type: "response.content_part.added", item_id: itemId, output_index: outputIndex, content_index: 0, part });
      send({
        type: "response.output_text.delta",
        item_id: itemId,
        output_index: outputIndex,
        content_index: 0,
        delta: text,
        logprobs: [],
      });
      send({
        type: "response.output_text.done",
        item_id: itemId,
        output_index: outputIndex,
        content_index: 0,
        text,
        logprobs: [],
      });
      send({
        type: "response.content_part.done",
        item_id: itemId,
        output_index: outputIndex,
        content_index: 0,
        part: { ...part, text },
      });
    } else {
      const toolArguments = String(item["arguments"]);
      send({
        type: "response.output_item.added",
        output_index: outputIndex,
        item: { ...item, arguments: "", status: "in_progress" },
      });
      send({
        type: "response.function_call_arguments.delta",
        item_id: itemId,
        output_index: outputIndex,
        delta: toolArguments,
      });
      send({
        type: "response.function_call_arguments.done",
        item_id: itemId,
        output_index: outputIndex,
        arguments: toolArguments,
      });
    }
    send({ type: "response.output_item.done", output_index: outputIndex, item });
  });
  send({ type: "response.completed", response: completed });
  response.end();
}

function sendJson(response: ServerResponse, status: number, payload: JsonObject): void {
  const data = JSON.stringify(payload);
  response.writeHead(status, { "content-type": "application/json", "content-length": Buffer.byteLength(data) });
  response.end(data);
}
