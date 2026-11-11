// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Images through a built-in loop session on the pi-ai client: references in state, base64 in each request.

import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { after, before, describe, test } from "node:test";

import {
  InMemoryMediaStore,
  maxImagesPerMessage,
  type AgentImageReference,
  type AgentSnapshot,
  type AgentTurnContext,
  type DeferredToolCall,
  type DeferredToolExecution,
  type MediaStore,
  type ToolExecutionResult,
  type ToolTaskPoll,
  type ToolTaskPollOutcome,
} from "@superdurable/dex-agents";
import {
  createBuiltinAgentSessionFlow,
  type ModelClient,
  type ModelRequest,
  type SourceToolDefinition,
  type ToolSource,
} from "@superdurable/dex-agents/builtin-loop";
import { AgentSessionTestClient, AgentTestEnvironment, PrivateDexEngine } from "@superdurable/dex-agents/testing";

import {
  createPiAiModelClient,
  piAgentFunctionTool,
  piAiEstimatedImageInputTokens,
  piDurableFunctionTool,
  type PiAgentToolLike,
  type PiDurableToolLike,
} from "../../src/index.js";
import { FakeModelServer, createFakeAnthropicModel, createFakeOpenAiModel } from "../fixtures/fake-model-server.js";

const conformanceImages = new Map(
  (
    JSON.parse(
      readFileSync(fileURLToPath(new URL("../../../../../conformance/images.json", import.meta.url)), "utf8"),
    ) as { readonly images: readonly { readonly name: string; readonly base64: string }[] }
  ).images.map((image) => [image.name, new Uint8Array(Buffer.from(image.base64, "base64"))]),
);
const png = conformanceImages.get("png") as Uint8Array;
const jpeg = conformanceImages.get("jpeg") as Uint8Array;
const gif = conformanceImages.get("gif") as Uint8Array;
const screenshotToolName = "take_screenshot";
const piScreenshotToolName = "pi_screenshot";
const piDurablePhotoToolName = "pi_durable_photo";
// Not PNG, JPEG, GIF, or WebP, so the media store refuses it.
const svg = new Uint8Array(Buffer.from('<svg xmlns="http://www.w3.org/2000/svg"/>'));

const piScreenshotTool: PiAgentToolLike = {
  name: piScreenshotToolName,
  description: "Take a screenshot with a Pi tool.",
  parameters: { type: "object", properties: {} },
  replay: "safe",
  execute: async () => ({
    content: [
      { type: "text", text: "Took a Pi screenshot." },
      { type: "image", data: base64(png), mimeType: "image/png" },
      { type: "image", data: base64(svg), mimeType: "image/svg+xml" },
    ],
  }),
};

const piManyScreenshotsToolName = "pi_many_screenshots";

const piManyScreenshotsTool: PiAgentToolLike = {
  name: piManyScreenshotsToolName,
  description: "Take more screenshots than one tool result may carry.",
  parameters: { type: "object", properties: {} },
  replay: "safe",
  execute: async () => ({
    content: [
      { type: "text", text: "Took 21 screenshots." },
      ...Array.from({ length: maxImagesPerMessage + 1 }, () => ({
        type: "image" as const,
        data: base64(png),
        mimeType: "image/png",
      })),
    ],
  }),
};

const piDurablePhotoTool: PiDurableToolLike = {
  name: piDurablePhotoToolName,
  description: "Take a photo with a pi-durable tool.",
  parameters: { type: "object", properties: {} },
  replay: "safe",
  execute: async () => ({
    output: [
      { type: "text", text: "Took a photo." },
      { type: "image", data: base64(jpeg), mimeType: "image/jpeg" },
    ],
  }),
};

describe("images through a Pi agent session", () => {
  let engine: PrivateDexEngine;
  let modelServer: FakeModelServer;
  let environment: AgentTestEnvironment;
  let sessions: AgentSessionTestClient;
  const store = new InMemoryMediaStore("pi-images");

  before(async () => {
    engine = await PrivateDexEngine.start();
    modelServer = await FakeModelServer.start();
    const clientOptions = { apiKey: "test-key" };
    const openAiModel = createPiAiModelClient(
      createFakeOpenAiModel(modelServer.baseUrl, "fake-gpt", ["text", "image"]),
      clientOptions,
    );
    const modelsBySetting = new Map<string, ModelClient>([
      [
        "anthropic",
        createPiAiModelClient(createFakeAnthropicModel(modelServer.baseUrl, "claude-fake-1", ["text", "image"]), clientOptions),
      ],
      ["text-only", createPiAiModelClient(createFakeOpenAiModel(modelServer.baseUrl), clientOptions)],
    ]);
    const flow = createBuiltinAgentSessionFlow(
      {
        name: "pi-images",
        instructions: "You look at images.",
        model: openAiModel,
        resolveModel: (settings) => modelsBySetting.get(String(settings["model"])) ?? openAiModel,
        tools: [
          piAgentFunctionTool(piScreenshotTool, { effect: "read" }),
          piAgentFunctionTool(piManyScreenshotsTool, { effect: "read" }),
          piDurableFunctionTool(piDurablePhotoTool, { effect: "read" }),
        ],
        toolSources: [new ScreenshotToolSource(store)],
      },
      "PiImageAgent",
      { mediaStore: store },
    );
    environment = await AgentTestEnvironment.start({
      serverAddress: engine.serverAddress,
      flows: [flow],
      isWorkerInProcess: true,
    });
    sessions = new AgentSessionTestClient(environment.client, flow);
  });

  after(async () => {
    await environment?.close();
    await modelServer?.close();
    await engine?.stop();
  });

  test("user images reach OpenAI Chat Completions as image_url parts after the text, and are stored as references", async () => {
    const token = newToken();
    const sessionId = `pi-images-openai-${token}`;
    modelServer.script(token, () => ({ text: "two images" }));
    const references = [await store.putImage(png), await store.putImage(jpeg)];
    await sessions.startSession(sessionId);

    assert.equal(await sessions.sendUserMessage(sessionId, `describe ${token}`, false, references), true);
    await sessions.waitForCompletedTurns(sessionId, 1);

    const [request] = modelServer.requests(token);
    assert.deepEqual(request?.body["messages"].at(-1), {
      role: "user",
      content: [
        { type: "text", text: `describe ${token}` },
        { type: "image_url", image_url: { url: dataUrl(png, "image/png") } },
        { type: "image_url", image_url: { url: dataUrl(jpeg, "image/jpeg") } },
      ],
    });
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    const [userMessage] = snapshot.messages;
    assert.deepEqual(userMessage?.images, references);
    assert.deepEqual(
      (JSON.parse(userMessage?.frameworkItem ?? "{}") as { readonly images: readonly { readonly sha256: string }[] })
        .images.map((image) => image.sha256),
      references.map((reference) => reference.sha256),
    );
    assert.equal(snapshot.messages.at(-1)?.text, "two images");
    assertNoImageBytes(snapshot, png, jpeg);
  });

  test("user images reach Anthropic Messages as base64 image blocks after the text", async () => {
    const token = newToken();
    const sessionId = `pi-images-anthropic-${token}`;
    modelServer.script(token, () => ({ text: "one image" }));
    const reference = await store.putImage(gif);
    await sessions.startSession(sessionId, null, {}, { model: "anthropic" });

    assert.equal(await sessions.sendUserMessage(sessionId, `describe ${token}`, false, [reference]), true);
    await sessions.waitForCompletedTurns(sessionId, 1);

    const [request] = modelServer.requests(token);
    assert.equal(request?.format, "anthropic");
    const content = request?.body["messages"].at(-1).content as readonly Record<string, unknown>[];
    // pi-ai adds cache_control to the last block, so compare the fields that carry the message.
    assert.deepEqual(
      content.map(({ cache_control: _cacheControl, ...block }) => block),
      [
        { type: "text", text: `describe ${token}` },
        { type: "image", source: { type: "base64", media_type: "image/gif", data: base64(gif) } },
      ],
    );
    assertNoImageBytes(await sessions.getAgentSnapshot(sessionId), gif);
  });

  test("a tool source's image reaches the next OpenAI request in a user message after the tool message", async () => {
    const token = newToken();
    const sessionId = `pi-images-tool-openai-${token}`;
    modelServer.script(token, (_request, index) =>
      index === 0
        ? { toolCalls: [{ id: "call_screenshot", name: screenshotToolName, arguments: "{}" }] }
        : { text: "screenshot taken" },
    );
    await sessions.startSession(sessionId, `screenshot ${token}`);
    await sessions.waitForCompletedTurns(sessionId, 1);

    const requests = modelServer.requests(token);
    assert.equal(requests.length, 2);
    assert.deepEqual(requests[1]?.body["messages"].slice(-2), [
      { role: "tool", content: "Took a screenshot.", tool_call_id: "call_screenshot" },
      {
        role: "user",
        content: [
          { type: "text", text: "Attached image(s) from tool result:" },
          { type: "image_url", image_url: { url: dataUrl(png, "image/png") } },
        ],
      },
    ]);
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    const toolMessage = snapshot.messages.find((message) => message.role === "tool");
    assert.deepEqual(toolMessage?.images, [await store.putImage(png)]);
    assertNoImageBytes(snapshot, png);
  });

  test("a tool source's image reaches the next Anthropic request inside the tool_result block", async () => {
    const token = newToken();
    const sessionId = `pi-images-tool-anthropic-${token}`;
    modelServer.script(token, (_request, index) =>
      index === 0
        ? { toolCalls: [{ id: "toolu_screenshot", name: screenshotToolName, arguments: "{}" }] }
        : { text: "screenshot taken" },
    );
    await sessions.startSession(sessionId, `screenshot ${token}`, {}, { model: "anthropic" });
    await sessions.waitForCompletedTurns(sessionId, 1);

    const requests = modelServer.requests(token);
    assert.equal(requests.length, 2);
    const toolResults = (requests[1]?.body["messages"].at(-1).content as readonly Record<string, any>[]).filter(
      (block) => block["type"] === "tool_result",
    );
    assert.equal(toolResults.length, 1);
    assert.equal(toolResults[0]?.["tool_use_id"], "toolu_screenshot");
    assert.deepEqual(
      (toolResults[0]?.["content"] as readonly Record<string, unknown>[]).map(
        ({ cache_control: _cacheControl, ...block }) => block,
      ),
      [
        { type: "text", text: "Took a screenshot." },
        { type: "image", source: { type: "base64", media_type: "image/png", data: base64(png) } },
      ],
    );
  });

  test("a Pi tool's image reaches the model through the media store, and a refused image stays a note", async () => {
    const token = newToken();
    const sessionId = `pi-images-pi-tool-${token}`;
    modelServer.script(token, (_request, index) =>
      index === 0
        ? { toolCalls: [{ id: "call_pi_screenshot", name: piScreenshotToolName, arguments: "{}" }] }
        : { text: "Pi screenshot taken" },
    );
    await sessions.startSession(sessionId, `pi screenshot ${token}`);
    await sessions.waitForCompletedTurns(sessionId, 1);

    const requests = modelServer.requests(token);
    assert.equal(requests.length, 2);
    assert.deepEqual(requests[1]?.body["messages"].slice(-2), [
      {
        role: "tool",
        content: "Took a Pi screenshot.\n[image omitted: image/svg+xml]",
        tool_call_id: "call_pi_screenshot",
      },
      {
        role: "user",
        content: [
          { type: "text", text: "Attached image(s) from tool result:" },
          { type: "image_url", image_url: { url: dataUrl(png, "image/png") } },
        ],
      },
    ]);
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    const toolMessage = snapshot.messages.find((message) => message.role === "tool");
    assert.deepEqual(toolMessage?.images, [await store.putImage(png)]);
    assertNoImageBytes(snapshot, png);
  });

  test("a Pi tool with more images than one result may carry delivers the first ones and notes the rest", async () => {
    const token = newToken();
    const sessionId = `pi-images-pi-tool-many-${token}`;
    modelServer.script(token, (_request, index) =>
      index === 0
        ? { toolCalls: [{ id: "call_pi_many", name: piManyScreenshotsToolName, arguments: "{}" }] }
        : { text: "many screenshots taken" },
    );
    await sessions.startSession(sessionId, `many screenshots ${token}`);
    await sessions.waitForCompletedTurns(sessionId, 1);

    const requests = modelServer.requests(token);
    assert.equal(requests.length, 2);
    const [toolMessage, attachedMessage] = requests[1]?.body["messages"].slice(-2);
    assert.deepEqual(toolMessage, {
      role: "tool",
      content:
        "Took 21 screenshots.\n[image omitted: image/png]\n" +
        `(more than ${maxImagesPerMessage} images; the rest were omitted)`,
      tool_call_id: "call_pi_many",
    });
    const attachedImages = (attachedMessage.content as readonly Record<string, unknown>[]).filter(
      (part) => part["type"] === "image_url",
    );
    assert.equal(attachedImages.length, maxImagesPerMessage);
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    const storedToolMessage = snapshot.messages.find((message) => message.role === "tool");
    assert.equal(storedToolMessage?.images?.length, maxImagesPerMessage);
  });

  test("a pi-durable tool's image reaches the next Anthropic request inside the tool_result block", async () => {
    const token = newToken();
    const sessionId = `pi-images-pi-durable-tool-${token}`;
    modelServer.script(token, (_request, index) =>
      index === 0
        ? { toolCalls: [{ id: "toolu_photo", name: piDurablePhotoToolName, arguments: "{}" }] }
        : { text: "photo taken" },
    );
    await sessions.startSession(sessionId, `photo ${token}`, {}, { model: "anthropic" });
    await sessions.waitForCompletedTurns(sessionId, 1);

    const requests = modelServer.requests(token);
    assert.equal(requests.length, 2);
    const toolResults = (requests[1]?.body["messages"].at(-1).content as readonly Record<string, any>[]).filter(
      (block) => block["type"] === "tool_result",
    );
    assert.equal(toolResults.length, 1);
    assert.deepEqual(
      (toolResults[0]?.["content"] as readonly Record<string, unknown>[]).map(
        ({ cache_control: _cacheControl, ...block }) => block,
      ),
      [
        { type: "text", text: "Took a photo." },
        { type: "image", source: { type: "base64", media_type: "image/jpeg", data: base64(jpeg) } },
      ],
    );
    assertNoImageBytes(await sessions.getAgentSnapshot(sessionId), jpeg);
  });

  test("a model whose input lacks image gets pi-ai's placeholder text instead of the image", async () => {
    const token = newToken();
    const sessionId = `pi-images-text-only-${token}`;
    modelServer.script(token, () => ({ text: "no image seen" }));
    const reference = await store.putImage(png);
    await sessions.startSession(sessionId, null, {}, { model: "text-only" });

    assert.equal(await sessions.sendUserMessage(sessionId, `describe ${token}`, false, [reference]), true);
    await sessions.waitForCompletedTurns(sessionId, 1);

    const [request] = modelServer.requests(token);
    const userMessage = request?.body["messages"].at(-1);
    assert.equal(JSON.stringify(userMessage).includes(base64(png)), false);
    assert.deepEqual(userMessage, {
      role: "user",
      content: [
        { type: "text", text: `describe ${token}` },
        { type: "text", text: "(image omitted: model does not support images)" },
      ],
    });
  });

  test("a request without the bytes of an image throws before pi-ai sends it", async () => {
    const token = newToken();
    const reference = await store.putImage(png);
    const model = createPiAiModelClient(createFakeOpenAiModel(modelServer.baseUrl, "fake-gpt", ["text", "image"]), {
      apiKey: "test-key",
    });
    modelServer.script(token, () => ({ text: "unreachable" }));
    const request = imageRequest(token, reference);

    await assert.rejects(
      model.callModel(request, { writeTextDelta: () => undefined, writeReasoningDelta: () => undefined }),
      { name: "TypeError", message: `the model request has no bytes for image ${reference.uri}` },
    );
    assert.equal(modelServer.requests(token).length, 0);
    const characters = JSON.stringify(request.inputItems).length + JSON.stringify(request.tools).length;
    assert.equal(model.estimateInputTokens(request), Math.ceil(characters / 4) + piAiEstimatedImageInputTokens);
  });
});

/** A tool source whose only tool uploads a PNG and returns its reference with the result. */
class ScreenshotToolSource implements ToolSource {
  public readonly sourceName = "screenshots";

  public constructor(private readonly store: MediaStore) {}

  public listTools(): Promise<readonly SourceToolDefinition[]> {
    return Promise.resolve([
      { name: screenshotToolName, description: "Take a screenshot.", inputSchema: { type: "object", properties: {} } },
    ]);
  }

  public deferredToolCall(
    turn: AgentTurnContext,
    call: { readonly toolCallId: string; readonly toolName: string; readonly arguments: string },
  ): DeferredToolCall {
    return {
      toolCallId: call.toolCallId,
      toolName: call.toolName,
      arguments: call.arguments,
      policy: turn.resolveToolPolicy(call.toolName, "read"),
    };
  }

  public async executeDeferredToolCall(_call: DeferredToolExecution): Promise<ToolExecutionResult> {
    const reference = await this.store.putImage(png);
    return { output: "Took a screenshot.", images: [reference] };
  }

  public pollToolTask(_poll: ToolTaskPoll): Promise<ToolTaskPollOutcome> {
    return Promise.reject(new RangeError(`${screenshotToolName} starts no tool task`));
  }
}

function imageRequest(token: string, reference: AgentImageReference): ModelRequest {
  return {
    instructions: "",
    inputItems: [{ type: "user_text", text: token, images: [reference] }],
    tools: [],
    toolChoice: "auto",
    settings: {},
    metadata: { flowId: `pi-images-${token}`, turnId: "turn-1", stepExecutionId: "step-1", attempt: 1 },
    cancellationSignal: new AbortController().signal,
  };
}

function assertNoImageBytes(snapshot: AgentSnapshot, ...contents: Uint8Array[]): void {
  const stored = JSON.stringify([
    ...snapshot.messages.map((message) => message.frameworkItem),
    ...snapshot.toolCalls.map((record) => record.output),
  ]);
  for (const data of contents) {
    assert.equal(stored.includes(base64(data)), false);
  }
}

function base64(data: Uint8Array): string {
  return Buffer.from(data).toString("base64");
}

function dataUrl(data: Uint8Array, mimeType: string): string {
  return `data:${mimeType};base64,${base64(data)}`;
}

function newToken(): string {
  return randomUUID().slice(0, 8);
}
