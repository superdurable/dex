// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { createServer, type IncomingMessage, type Server, type ServerResponse } from "node:http";
import type { AddressInfo } from "node:net";
import { setTimeout as delay } from "node:timers/promises";

import type { Client } from "@superdurable/dex";
import {
  AbstractChat,
  DefaultChatTransport,
  type ChatInit,
  type ChatState,
  type ChatStatus,
  type UIMessage,
} from "ai";

import type { AgentFrameworkRunner, DeferredToolOutcome } from "../../src/index.js";
import type { AgentSessionFlow } from "../../src/index.js";
import {
  agentResumeTokenHeaderName,
  convertAgentEventStreamSnapshotToJson,
  createAgentEventSseResponse,
  createAgentUIMessageStreamResponse,
  readAgentEventStreamSnapshot,
  readAgentSessionUIMessages,
  readAgentToolApprovalResponses,
  resolveAgentToolApprovalsWithUIMessageStream,
  sendAgentUserMessageWithUIMessageStream,
} from "../../src/ui-stream/index.js";

const pollTimeoutMs = 1_000;
/** Header the test chat sets while it holds the parts its resume token continues. */
const resumeContinuationHeaderName = "x-agent-resume-continue";

/**
 * An HTTP server that mounts the UI stream helpers the way an application does:
 * `POST /api/chat`, `GET /api/chat/:id/stream`, `GET /api/sessions/:id/messages`,
 * `GET /api/sessions/:id/snapshot`, and `GET /api/sessions/:id/events`.
 */
export class AgentUIStreamTestServer {
  private constructor(
    private readonly server: Server,
    /** Base URL, such as `http://127.0.0.1:41234`. */
    public readonly baseUrl: string,
  ) {}

  public static async start(client: Client, flow: AgentSessionFlow<AgentFrameworkRunner<DeferredToolOutcome>>): Promise<AgentUIStreamTestServer> {
    const server = createServer((request, response) => {
      handleRequest(client, flow, request, response).catch((error: unknown) => {
        if (!response.headersSent) {
          response.writeHead(500, { "content-type": "text/plain" });
        }
        response.end(String(error));
      });
    });
    await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
    const address = server.address() as AddressInfo;
    return new AgentUIStreamTestServer(server, `http://127.0.0.1:${address.port}`);
  }

  public async close(): Promise<void> {
    this.server.closeAllConnections();
    await new Promise<void>((resolve, reject) =>
      this.server.close((error) => (error === undefined ? resolve() : reject(error))),
    );
  }
}

async function handleRequest(
  client: Client,
  flow: AgentSessionFlow<AgentFrameworkRunner<DeferredToolOutcome>>,
  request: IncomingMessage,
  response: ServerResponse,
): Promise<void> {
  const url = new URL(request.url ?? "/", "http://localhost");
  const abortController = new AbortController();
  response.on("close", () => abortController.abort());
  const signal = abortController.signal;
  const segments = url.pathname.split("/").filter((segment) => segment !== "");
  let fetchResponse: Response;
  if (request.method === "POST" && url.pathname === "/api/chat") {
    const body = JSON.parse(await readRequestBody(request)) as {
      readonly id: string;
      readonly messages: readonly UIMessage[];
      readonly resumeToken?: string;
    };
    const lastMessage = body.messages.at(-1);
    if (lastMessage?.role === "assistant") {
      fetchResponse = await resolveAgentToolApprovalsWithUIMessageStream({
        client,
        flow,
        flowId: body.id,
        approvals: readAgentToolApprovalResponses(lastMessage),
        resumeToken: body.resumeToken,
        pollTimeoutMs,
        signal,
      });
    } else {
      const text = (lastMessage?.parts ?? [])
        .flatMap((part) => (part.type === "text" ? [part.text] : []))
        .join("");
      fetchResponse = await sendAgentUserMessageWithUIMessageStream({
        client,
        flow,
        flowId: body.id,
        text,
        pollTimeoutMs,
        signal,
      });
    }
  } else if (segments[0] === "api" && segments[1] === "chat" && segments[3] === "stream") {
    const resumeToken = request.headers[agentResumeTokenHeaderName];
    fetchResponse = await createAgentUIMessageStreamResponse({
      client,
      flow,
      flowId: decodeURIComponent(segments[2] ?? ""),
      resumeToken: typeof resumeToken === "string" ? resumeToken : undefined,
      continueAfterResumeToken: request.headers[resumeContinuationHeaderName] === "true",
      pollTimeoutMs,
      signal,
    });
  } else if (segments[0] === "api" && segments[1] === "sessions" && segments[3] === "messages") {
    fetchResponse = Response.json(
      await readAgentSessionUIMessages({ client, flow, flowId: decodeURIComponent(segments[2] ?? "") }),
    );
  } else if (segments[0] === "api" && segments[1] === "sessions" && segments[3] === "snapshot") {
    const snapshot = await readAgentEventStreamSnapshot({
      client,
      flow,
      flowId: decodeURIComponent(segments[2] ?? ""),
    });
    fetchResponse = Response.json(convertAgentEventStreamSnapshotToJson(snapshot));
  } else if (segments[0] === "api" && segments[1] === "sessions" && segments[3] === "events") {
    const lastEventId = request.headers["last-event-id"];
    fetchResponse = await createAgentEventSseResponse({
      client,
      flow,
      flowId: decodeURIComponent(segments[2] ?? ""),
      resumeToken:
        typeof lastEventId === "string" ? lastEventId : (url.searchParams.get("resumeToken") ?? ""),
      pollTimeoutMs,
      signal,
    });
  } else {
    fetchResponse = new Response("not found", { status: 404 });
  }
  await writeFetchResponse(fetchResponse, response);
}

async function readRequestBody(request: IncomingMessage): Promise<string> {
  const chunks: Buffer[] = [];
  for await (const chunk of request) {
    chunks.push(chunk as Buffer);
  }
  return Buffer.concat(chunks).toString("utf8");
}

async function writeFetchResponse(fetchResponse: Response, response: ServerResponse): Promise<void> {
  response.writeHead(fetchResponse.status, Object.fromEntries(fetchResponse.headers.entries()));
  if (fetchResponse.body === null) {
    response.end();
    return;
  }
  const reader = fetchResponse.body.getReader();
  response.on("close", () => {
    reader.cancel().catch(() => undefined);
  });
  for (;;) {
    const { done, value } = await reader.read();
    if (done) {
      break;
    }
    response.write(value);
  }
  response.end();
}

/** One response that went through a {@link RecordingFetch}. */
export interface RecordedResponse {
  readonly method: string;
  readonly url: string;
  readonly status: number;
  readonly headers: Headers;
  /** The request body text, or `undefined` for a request without one. */
  readonly requestBody: string | undefined;
  /** The JSON of every complete `data:` frame, in arrival order; `[DONE]` excluded. */
  readonly chunks: unknown[];
}

const cutDelayMs = 100;

/** How {@link RecordingFetch} ends a response it cuts. */
export type ResponseCut = "network-error" | "clean-close";

/**
 * A `fetch` for the AI SDK transport that records every UI message stream part and can cut the open
 * response with the `TypeError` a browser raises when the network drops, or close it cleanly.
 */
export class RecordingFetch {
  /** Every response, in request order. */
  public readonly responses: RecordedResponse[] = [];
  private cutActiveResponse: ((cut: ResponseCut) => void) | undefined;
  private pendingCut:
    | {
        readonly isCutPoint: (chunk: unknown) => boolean;
        readonly cut: ResponseCut;
        readonly isBeforeChunk: boolean;
      }
    | undefined;

  public readonly fetch = async (input: string | URL | Request, init?: RequestInit): Promise<Response> => {
    const request = new Request(input, init);
    const requestBody = request.body === null ? undefined : await request.clone().text();
    const response = await globalThis.fetch(request);
    const recorded: RecordedResponse = {
      method: request.method,
      url: request.url,
      status: response.status,
      headers: response.headers,
      requestBody,
      chunks: [],
    };
    this.responses.push(recorded);
    if (response.body === null) {
      return response;
    }
    const decoder = new TextDecoder();
    const encoder = new TextEncoder();
    let pendingText = "";
    let isCut = false;
    let streamController: TransformStreamDefaultController<Uint8Array> | undefined;
    let cutDone: Promise<void> | undefined;
    const cutResponse = (cut: ResponseCut): void => {
      isCut = true;
      // Erroring a stream discards its queued frames; a network drop delivers what already arrived.
      cutDone = delay(cutDelayMs).then(() => {
        if (cut === "network-error") {
          streamController?.error(new TypeError("network error"));
        } else {
          streamController?.terminate();
        }
      });
    };
    // Whole frames pass through one by one, so a cut lands exactly between two parts.
    const recordingStream = new TransformStream<Uint8Array, Uint8Array>({
      start: (controller) => {
        streamController = controller;
      },
      // A cut response ends with the cut, never with the server's own end.
      flush: async () => {
        await cutDone;
      },
      transform: (chunk, controller) => {
        pendingText += decoder.decode(chunk, { stream: true });
        let frameEnd = pendingText.indexOf("\n\n");
        while (frameEnd >= 0 && !isCut) {
          const frame = pendingText.slice(0, frameEnd + 2);
          pendingText = pendingText.slice(frameEnd + 2);
          frameEnd = pendingText.indexOf("\n\n");
          const dataLine = frame.split("\n").find((line) => line.startsWith("data: ") && line !== "data: [DONE]");
          const parsedChunk: unknown = dataLine === undefined ? undefined : JSON.parse(dataLine.slice("data: ".length));
          const pendingCut = this.pendingCut;
          const isCutPoint = parsedChunk !== undefined && pendingCut?.isCutPoint(parsedChunk) === true;
          if (isCutPoint && pendingCut?.isBeforeChunk === true) {
            this.pendingCut = undefined;
            cutResponse(pendingCut.cut);
            break;
          }
          controller.enqueue(encoder.encode(frame));
          if (parsedChunk !== undefined) {
            recorded.chunks.push(parsedChunk);
          }
          if (isCutPoint && pendingCut !== undefined) {
            this.pendingCut = undefined;
            cutResponse(pendingCut.cut);
          }
        }
      },
    });
    this.cutActiveResponse = cutResponse;
    return new Response(response.body.pipeThrough(recordingStream), {
      status: response.status,
      headers: response.headers,
    });
  };

  /** Ends the latest response now: by default as a dropped network connection would. */
  public disconnect(cut: ResponseCut = "network-error"): void {
    if (this.cutActiveResponse === undefined) {
      throw new Error("no response to disconnect");
    }
    this.cutActiveResponse(cut);
  }

  /** Cuts the response that delivers the next part matching `isCutPoint`, right after that part. */
  public cutAfterChunk(isCutPoint: (chunk: unknown) => boolean, cut: ResponseCut): void {
    this.pendingCut = { isCutPoint, cut, isBeforeChunk: false };
  }

  /** Cuts the response that delivers the next part matching `isCutPoint`, right before that part. */
  public cutBeforeChunk(isCutPoint: (chunk: unknown) => boolean, cut: ResponseCut): void {
    this.pendingCut = { isCutPoint, cut, isBeforeChunk: true };
  }
}

/** Options of {@link AgentUIStreamTestChat}. */
export interface AgentUIStreamTestChatOptions
  extends Omit<ChatInit<UIMessage>, "messages" | "transport" | "onData" | "id"> {
  readonly sessionId: string;
  readonly baseUrl: string;
  readonly recordingFetch: RecordingFetch;
  readonly messages?: UIMessage[];
  readonly resumeToken?: string | undefined;
  readonly isContinuationEnabled?: boolean;
  /** Whether a request that answers approvals carries the latest resume token in its body. */
  readonly isResumeTokenSentWithApprovals?: boolean;
}

/**
 * The AI SDK `Chat` state machine that `useChat` drives, with in-memory state instead of React.
 *
 * It tracks the latest `data-agent-resume-token` and reconnects with it, as an application does.
 */
export class AgentUIStreamTestChat extends AbstractChat<UIMessage> {
  /** The latest resume token from a `data-agent-resume-token` part. */
  public resumeToken: string | undefined;
  /** Whether a reconnect after a dropped connection asks to continue exactly instead of replaying. */
  public readonly isContinuationEnabled: boolean;

  public constructor(options: AgentUIStreamTestChatOptions) {
    const {
      sessionId,
      baseUrl,
      recordingFetch,
      messages,
      resumeToken,
      isContinuationEnabled,
      isResumeTokenSentWithApprovals,
      ...chatInit
    } = options;
    let chat: AgentUIStreamTestChat | undefined;
    super({
      ...chatInit,
      id: sessionId,
      state: new InMemoryChatState(messages ?? []),
      onData: (part) => {
        if (part.type === "data-agent-resume-token" && chat !== undefined) {
          chat.resumeToken = part.data as string;
        }
      },
      transport: new DefaultChatTransport({
        api: `${baseUrl}/api/chat`,
        fetch: recordingFetch.fetch,
        prepareSendMessagesRequest: ({ id, messages: requestMessages, body, trigger, messageId }) => ({
          body: {
            ...body,
            id,
            messages: requestMessages,
            trigger,
            messageId,
            ...(isResumeTokenSentWithApprovals === true && requestMessages.at(-1)?.role === "assistant"
              ? { resumeToken: chat?.resumeToken }
              : {}),
          },
        }),
        prepareReconnectToStreamRequest: ({ id }) => ({
          api: `${baseUrl}/api/chat/${encodeURIComponent(id)}/stream`,
          headers:
            chat?.resumeToken === undefined
              ? {}
              : {
                  [agentResumeTokenHeaderName]: chat.resumeToken,
                  // After a dropped connection the chat keeps its partial message, so it can continue it.
                  [resumeContinuationHeaderName]: String(chat.isContinuationEnabled && chat.status === "error"),
                },
        }),
      }),
    });
    chat = this;
    this.resumeToken = resumeToken;
    this.isContinuationEnabled = isContinuationEnabled ?? false;
  }
}

class InMemoryChatState implements ChatState<UIMessage> {
  public status: ChatStatus = "ready";
  public error: Error | undefined = undefined;

  public constructor(public messages: UIMessage[]) {}

  public pushMessage(message: UIMessage): void {
    this.messages = [...this.messages, message];
  }

  public popMessage(): void {
    this.messages = this.messages.slice(0, -1);
  }

  public replaceMessage(index: number, message: UIMessage): void {
    this.messages = this.messages.map((existing, existingIndex) => (existingIndex === index ? message : existing));
  }

  public snapshot<T>(thing: T): T {
    return structuredClone(thing);
  }
}

/** Describes message parts by the fields the UI renders, so live and stored messages compare. */
export function describeUIMessageParts(message: UIMessage | { readonly parts: readonly unknown[] } | undefined): string[] {
  return (message?.parts ?? []).map((part) => {
    const uiPart = part as Record<string, unknown>;
    switch (uiPart["type"]) {
      case "step-start":
        return "step-start";
      case "text":
        return `text ${String(uiPart["text"])}`;
      case "reasoning":
        return `reasoning ${String(uiPart["text"])}`;
      case "dynamic-tool": {
        const result =
          uiPart["state"] === "output-available"
            ? ` -> ${String(uiPart["output"])}`
            : uiPart["state"] === "output-error"
              ? ` !! ${String(uiPart["errorText"])}`
              : "";
        return `tool ${String(uiPart["toolName"])} ${String(uiPart["state"])} ${JSON.stringify(uiPart["input"])}${result}`;
      }
      default:
        return String(uiPart["type"]);
    }
  });
}
