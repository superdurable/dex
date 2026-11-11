// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { createServer, type IncomingMessage, type Server, type ServerResponse } from "node:http";
import type { AddressInfo } from "node:net";

import type { Api, Model } from "@earendil-works/pi-ai";

/** Wire format of a fake provider endpoint. */
export type FakeModelFormat = "openai" | "anthropic";

/** One tool call a fake reply streams; `arguments` is the raw JSON text. */
export interface FakeToolCall {
  readonly id: string;
  readonly name: string;
  readonly arguments: string;
}

/** What the fake provider streams for one request. */
export interface FakeModelReply {
  readonly thinking?: { readonly text: string; readonly signature: string };
  readonly text?: string;
  readonly toolCalls?: readonly FakeToolCall[];
  readonly usage?: { readonly input: number; readonly output: number; readonly cacheRead: number };
  /** Answers HTTP 500 instead of a stream. */
  readonly isServerError?: boolean;
  /** Breaks the connection after the last streamed block, before the stop event. */
  readonly isCutBeforeStop?: boolean;
  /** Awaited after the text streams and before the rest, to hold the stream open. */
  readonly pauseAfterText?: () => Promise<void>;
}

/** One request the fake provider received. */
export interface RecordedModelRequest {
  readonly format: FakeModelFormat;
  readonly body: Record<string, any>;
  /** Whether the client closed the connection before the reply finished. */
  isAbortedByClient: boolean;
}

/** Picks the reply to one request of a session; `index` counts that session's requests from zero. */
export type FakeModelResponder = (request: RecordedModelRequest, index: number) => FakeModelReply | Promise<FakeModelReply>;

/** A local OpenAI Chat Completions and Anthropic Messages provider; responders are keyed by a session token. */
export class FakeModelServer {
  private readonly responders = new Map<string, FakeModelResponder>();
  private readonly requestsByToken = new Map<string, RecordedModelRequest[]>();

  private constructor(
    private readonly server: Server,
    /** Base URL, such as `http://127.0.0.1:1234`. */
    public readonly baseUrl: string,
  ) {}

  public static async start(): Promise<FakeModelServer> {
    let fakeServer: FakeModelServer | undefined;
    const server = createServer((request, response) => {
      void fakeServer?.handleRequest(request, response);
    });
    await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
    const port = (server.address() as AddressInfo).port;
    fakeServer = new FakeModelServer(server, `http://127.0.0.1:${port}`);
    return fakeServer;
  }

  /** Registers the responder for requests whose body contains `token`. */
  public script(token: string, responder: FakeModelResponder): void {
    this.responders.set(token, responder);
    this.requestsByToken.set(token, []);
  }

  /** Requests whose body contained `token`, oldest first. */
  public requests(token: string): readonly RecordedModelRequest[] {
    return this.requestsByToken.get(token) ?? [];
  }

  public async close(): Promise<void> {
    this.server.closeAllConnections();
    await new Promise<void>((resolve) => this.server.close(() => resolve()));
  }

  private async handleRequest(request: IncomingMessage, response: ServerResponse): Promise<void> {
    const chunks: Buffer[] = [];
    for await (const chunk of request) {
      chunks.push(chunk as Buffer);
    }
    const bodyText = Buffer.concat(chunks).toString("utf8");
    const path = new URL(request.url ?? "/", "http://fake").pathname;
    const format: FakeModelFormat | undefined = path.endsWith("/chat/completions")
      ? "openai"
      : path.endsWith("/messages")
        ? "anthropic"
        : undefined;
    const token = [...this.responders.keys()].find((candidate) => bodyText.includes(candidate));
    if (format === undefined || token === undefined) {
      response.writeHead(404, { "content-type": "application/json" });
      response.end(JSON.stringify({ error: { message: `no fake reply for ${request.url}` } }));
      return;
    }
    const recorded: RecordedModelRequest = {
      format,
      body: JSON.parse(bodyText) as Record<string, any>,
      isAbortedByClient: false,
    };
    const requests = this.requestsByToken.get(token) ?? [];
    requests.push(recorded);
    let isFinished = false;
    response.on("close", () => {
      if (!isFinished) {
        recorded.isAbortedByClient = true;
      }
    });
    const reply = await (this.responders.get(token) as FakeModelResponder)(recorded, requests.length - 1);
    if (reply.isServerError === true) {
      isFinished = true;
      response.writeHead(500, { "content-type": "application/json" });
      response.end(JSON.stringify({ error: { message: "fake provider failure", type: "server_error" } }));
      return;
    }
    response.writeHead(200, { "content-type": "text/event-stream", "cache-control": "no-cache" });
    const writer = format === "openai" ? new OpenAiStreamWriter(response) : new AnthropicStreamWriter(response);
    await writer.writeReply(reply, () => {
      isFinished = true;
    });
  }
}

/** Input kinds of a fake model; only a model with `image` gets image content from pi-ai. */
export type FakeModelInput = Model<Api>["input"];

/** A chat model of the fake OpenAI Chat Completions endpoint. */
export function createFakeOpenAiModel(baseUrl: string, id = "fake-gpt", input: FakeModelInput = ["text"]): Model<Api> {
  return {
    id,
    name: id,
    api: "openai-completions",
    provider: "fake-openai",
    baseUrl: `${baseUrl}/v1`,
    reasoning: false,
    input: [...input],
    cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
    contextWindow: 128_000,
    maxTokens: 4_096,
  };
}

/** A thinking chat model of the fake Anthropic Messages endpoint. */
export function createFakeAnthropicModel(
  baseUrl: string,
  id = "claude-fake-1",
  input: FakeModelInput = ["text"],
): Model<Api> {
  return {
    id,
    name: id,
    api: "anthropic-messages",
    provider: "anthropic",
    baseUrl,
    reasoning: true,
    input: [...input],
    cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
    contextWindow: 200_000,
    maxTokens: 16_000,
  };
}

abstract class StreamWriter {
  public constructor(protected readonly response: ServerResponse) {}

  public async writeReply(reply: FakeModelReply, markFinished: () => void): Promise<void> {
    this.writeStart(reply);
    if (reply.thinking !== undefined) {
      this.writeThinking(reply.thinking.text, reply.thinking.signature);
    }
    if (reply.text !== undefined) {
      this.writeText(reply.text);
    }
    if (reply.pauseAfterText !== undefined) {
      await reply.pauseAfterText();
    }
    for (const [index, toolCall] of (reply.toolCalls ?? []).entries()) {
      this.writeToolCall(index, toolCall);
    }
    if (reply.isCutBeforeStop === true) {
      markFinished();
      this.response.destroy();
      return;
    }
    this.writeStop(reply);
    markFinished();
    this.response.end();
  }

  protected writeData(data: unknown, event?: string): void {
    this.response.write(`${event === undefined ? "" : `event: ${event}\n`}data: ${JSON.stringify(data)}\n\n`);
  }

  protected abstract writeStart(reply: FakeModelReply): void;
  protected abstract writeThinking(text: string, signature: string): void;
  protected abstract writeText(text: string): void;
  protected abstract writeToolCall(index: number, toolCall: FakeToolCall): void;
  protected abstract writeStop(reply: FakeModelReply): void;
}

class OpenAiStreamWriter extends StreamWriter {
  protected writeStart(): void {
    this.writeChunk({ role: "assistant", content: "" });
  }

  protected writeThinking(text: string): void {
    this.writeChunk({ reasoning_content: text });
  }

  protected writeText(text: string): void {
    this.writeChunk({ content: text });
  }

  protected writeToolCall(index: number, toolCall: FakeToolCall): void {
    this.writeChunk({
      tool_calls: [{ index, id: toolCall.id, type: "function", function: { name: toolCall.name, arguments: "" } }],
    });
    this.writeChunk({ tool_calls: [{ index, function: { arguments: toolCall.arguments } }] });
  }

  protected writeStop(reply: FakeModelReply): void {
    const finishReason = (reply.toolCalls ?? []).length > 0 ? "tool_calls" : "stop";
    this.writeData({
      id: "chatcmpl-fake",
      object: "chat.completion.chunk",
      created: 0,
      model: "fake-gpt",
      choices: [{ index: 0, delta: {}, finish_reason: finishReason }],
    });
    const usage = reply.usage ?? { input: 10, output: 5, cacheRead: 0 };
    this.writeData({
      id: "chatcmpl-fake",
      object: "chat.completion.chunk",
      created: 0,
      model: "fake-gpt",
      choices: [],
      usage: {
        prompt_tokens: usage.input + usage.cacheRead,
        completion_tokens: usage.output,
        total_tokens: usage.input + usage.cacheRead + usage.output,
        prompt_tokens_details: { cached_tokens: usage.cacheRead },
      },
    });
    this.response.write("data: [DONE]\n\n");
  }

  private writeChunk(delta: Record<string, unknown>): void {
    this.writeData({
      id: "chatcmpl-fake",
      object: "chat.completion.chunk",
      created: 0,
      model: "fake-gpt",
      choices: [{ index: 0, delta, finish_reason: null }],
    });
  }
}

class AnthropicStreamWriter extends StreamWriter {
  private blockIndex = 0;

  protected writeStart(reply: FakeModelReply): void {
    const usage = reply.usage ?? { input: 10, output: 5, cacheRead: 0 };
    this.writeData(
      {
        type: "message_start",
        message: {
          id: "msg_fake",
          type: "message",
          role: "assistant",
          model: "claude-fake-1",
          content: [],
          stop_reason: null,
          stop_sequence: null,
          usage: {
            input_tokens: usage.input,
            output_tokens: 1,
            cache_read_input_tokens: usage.cacheRead,
            cache_creation_input_tokens: 0,
          },
        },
      },
      "message_start",
    );
  }

  protected writeThinking(text: string, signature: string): void {
    const index = this.blockIndex++;
    this.writeData(
      { type: "content_block_start", index, content_block: { type: "thinking", thinking: "", signature: "" } },
      "content_block_start",
    );
    this.writeData(
      { type: "content_block_delta", index, delta: { type: "thinking_delta", thinking: text } },
      "content_block_delta",
    );
    this.writeData(
      { type: "content_block_delta", index, delta: { type: "signature_delta", signature } },
      "content_block_delta",
    );
    this.writeData({ type: "content_block_stop", index }, "content_block_stop");
  }

  protected writeText(text: string): void {
    const index = this.blockIndex++;
    this.writeData(
      { type: "content_block_start", index, content_block: { type: "text", text: "" } },
      "content_block_start",
    );
    this.writeData({ type: "content_block_delta", index, delta: { type: "text_delta", text } }, "content_block_delta");
    this.writeData({ type: "content_block_stop", index }, "content_block_stop");
  }

  protected writeToolCall(_index: number, toolCall: FakeToolCall): void {
    const index = this.blockIndex++;
    this.writeData(
      {
        type: "content_block_start",
        index,
        content_block: { type: "tool_use", id: toolCall.id, name: toolCall.name, input: {} },
      },
      "content_block_start",
    );
    this.writeData(
      { type: "content_block_delta", index, delta: { type: "input_json_delta", partial_json: toolCall.arguments } },
      "content_block_delta",
    );
    this.writeData({ type: "content_block_stop", index }, "content_block_stop");
  }

  protected writeStop(reply: FakeModelReply): void {
    const usage = reply.usage ?? { input: 10, output: 5, cacheRead: 0 };
    const stopReason = (reply.toolCalls ?? []).length > 0 ? "tool_use" : "end_turn";
    this.writeData(
      {
        type: "message_delta",
        delta: { stop_reason: stopReason, stop_sequence: null },
        usage: { output_tokens: usage.output },
      },
      "message_delta",
    );
    this.writeData({ type: "message_stop" }, "message_stop");
  }
}
