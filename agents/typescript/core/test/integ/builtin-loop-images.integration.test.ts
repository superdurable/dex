// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Images through a built-in loop session on the AI SDK client: references in state, bytes in each prompt.

import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { mkdtempSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { after, afterEach, before, describe, test } from "node:test";

import { InMemoryMediaStore, LocalDirectoryMediaStore, type AgentSnapshot } from "../../src/index.js";
import {
  AgentSessionTestClient,
  AgentTestEnvironment,
  FileGate,
  KillableWorkerProcess,
  PrivateDexEngine,
} from "../../src/testing/index.js";
import {
  builtinImageAgentStateDirectoryVariable,
  createBuiltinImageAgentFlows,
  readLoggedPrompts,
  seenImage,
  type SeenMessage,
} from "../fixtures/builtin-image-agent.js";
import { readConformanceJson } from "../fixtures/conformance-suite.js";
import { mediaDirectoryName, screenshotPng } from "../fixtures/image-test-agent.js";

const workerEntryModulePath = fileURLToPath(new URL("../fixtures/builtin-image-agent-worker.js", import.meta.url));
const images = new Map(
  readConformanceJson<{ readonly images: readonly { readonly name: string; readonly base64: string }[] }>(
    "images.json",
  ).images.map((image) => [image.name, new Uint8Array(Buffer.from(image.base64, "base64"))]),
);
const png = images.get("png") as Uint8Array;
const jpeg = images.get("jpeg") as Uint8Array;

function assertNoImageBytes(snapshot: AgentSnapshot, ...contents: Uint8Array[]): void {
  const stored = JSON.stringify([
    ...snapshot.messages.map((message) => message.frameworkItem),
    ...snapshot.toolCalls.map((record) => record.output),
  ]);
  for (const data of contents) {
    assert.equal(stored.includes(Buffer.from(data).toString("base64")), false);
  }
}

function lastMessage(prompt: readonly SeenMessage[] | undefined): SeenMessage | undefined {
  return prompt?.at(-1);
}

describe("images through a built-in loop session", () => {
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  let sessions: AgentSessionTestClient;
  let noStoreSessions: AgentSessionTestClient;
  const workers: KillableWorkerProcess[] = [];

  before(async () => {
    engine = await PrivateDexEngine.start();
    const [flow, noStoreFlow] = createBuiltinImageAgentFlows(
      join(engine.stateDirectory, "client-unused"),
      new InMemoryMediaStore("client-unused"),
    );
    environment = await AgentTestEnvironment.start({
      serverAddress: engine.serverAddress,
      flows: [flow!, noStoreFlow!],
      isWorkerInProcess: false,
    });
    sessions = new AgentSessionTestClient(environment.client, flow!);
    noStoreSessions = new AgentSessionTestClient(environment.client, noStoreFlow!);
  });

  afterEach(async () => {
    for (const worker of workers.splice(0)) {
      await worker.kill();
    }
  });

  after(async () => {
    await environment?.close();
    await engine?.stop();
  });

  async function startWorker(stateDirectory: string): Promise<KillableWorkerProcess> {
    const worker = await KillableWorkerProcess.start({
      entryModulePath: workerEntryModulePath,
      serverAddress: engine.serverAddress,
      logPath: join(stateDirectory, "worker.log"),
      environment: { [builtinImageAgentStateDirectoryVariable]: stateDirectory },
    });
    workers.push(worker);
    return worker;
  }

  test("user and tool images reach the prompt as file parts and are stored as references", async () => {
    const stateDirectory = mkdtempSync(join(engine.stateDirectory, "images-"));
    const store = new LocalDirectoryMediaStore(join(stateDirectory, mediaDirectoryName));
    await startWorker(stateDirectory);
    const pngReference = await store.putImage(png);
    const jpegReference = await store.putImage(jpeg);
    const sessionId = `builtin-images-${randomUUID()}`;
    await sessions.startSession(sessionId);

    assert.equal(await sessions.sendUserMessage(sessionId, "describe", false, [pngReference, jpegReference]), true);
    await sessions.waitForCompletedTurns(sessionId, 1);
    assert.equal(await sessions.sendUserMessage(sessionId, "screenshot"), true);
    await sessions.waitForCompletedTurns(sessionId, 2);
    const snapshot = await sessions.getAgentSnapshot(sessionId);

    const prompts = readLoggedPrompts(stateDirectory);
    const userSeen: SeenMessage = {
      role: "user",
      parts: [{ type: "text", text: "describe" }, seenImage(png, "image/png"), seenImage(jpeg, "image/jpeg")],
    };
    assert.deepEqual(prompts[0]?.slice(1), [userSeen]);
    // The second turn replays the first turn's images, then shows the tool's image in its result content.
    assert.deepEqual(prompts.at(-1)?.[1], userSeen);
    assert.deepEqual(lastMessage(prompts.at(-1)), {
      role: "tool",
      parts: [
        {
          type: "tool-result",
          toolName: "take_screenshot",
          output: [{ type: "text", text: "Took a screenshot." }, seenImage(screenshotPng(), "image/png")],
        },
      ],
    });
    assert.equal(snapshot.messages.at(-1)?.text, "screenshot taken");

    const [userMessage] = snapshot.messages;
    assert.deepEqual(userMessage?.images, [pngReference, jpegReference]);
    assert.deepEqual(
      (JSON.parse(userMessage?.frameworkItem ?? "{}") as { readonly images: readonly { readonly sha256: string }[] })
        .images.map((image) => image.sha256),
      [pngReference.sha256, jpegReference.sha256],
    );
    const toolMessage = snapshot.messages.find((message) => message.role === "tool");
    const screenshotReference = await store.putImage(screenshotPng());
    assert.deepEqual(toolMessage?.images, [screenshotReference]);
    assertNoImageBytes(snapshot, png, jpeg, screenshotPng());
  });

  test("a Worker killed in the model call after a tool image rebuilds the same prompt", async () => {
    const stateDirectory = mkdtempSync(join(engine.stateDirectory, "kill-"));
    const store = new LocalDirectoryMediaStore(join(stateDirectory, mediaDirectoryName));
    const worker = await startWorker(stateDirectory);
    const pngReference = await store.putImage(png);
    const sessionId = `builtin-images-kill-${randomUUID()}`;
    await sessions.startSession(sessionId);
    assert.equal(await sessions.sendUserMessage(sessionId, "screenshot-hold model-gate", false, [pngReference]), true);
    const gate = new FileGate(stateDirectory, "model-gate");
    await gate.waitUntilReached();
    const before = readLoggedPrompts(stateDirectory);

    await worker.kill();
    await startWorker(stateDirectory);
    await gate.waitUntilReached(2);
    gate.release();
    await sessions.waitForCompletedTurns(sessionId, 1);
    const snapshot = await sessions.getAgentSnapshot(sessionId);

    const after = readLoggedPrompts(stateDirectory);
    // The new Worker read the same bytes from the store and built the identical prompt.
    assert.equal(before.length, 2);
    assert.deepEqual(after[2], before[1]);
    const resumedPrompt = JSON.stringify(after[2]);
    assert.ok(resumedPrompt.includes(JSON.stringify(seenImage(png, "image/png"))));
    assert.ok(resumedPrompt.includes(JSON.stringify(seenImage(screenshotPng(), "image/png"))));
    assert.equal(snapshot.messages.at(-1)?.text, "screenshot taken");
    assertNoImageBytes(snapshot, png, screenshotPng());
  });

  test("a runner without a media store refuses images and hides tool images", async () => {
    const stateDirectory = mkdtempSync(join(engine.stateDirectory, "no-store-"));
    const store = new LocalDirectoryMediaStore(join(stateDirectory, mediaDirectoryName));
    await startWorker(stateDirectory);
    const pngReference = await store.putImage(png);
    const sessionId = `builtin-images-no-store-${randomUUID()}`;
    await noStoreSessions.startSession(sessionId);

    assert.equal(await noStoreSessions.sendUserMessage(sessionId, "describe", false, [pngReference]), false);
    assert.equal(await noStoreSessions.sendUserMessage(sessionId, "screenshot"), true);
    await noStoreSessions.waitForCompletedTurns(sessionId, 1);
    const snapshot = await noStoreSessions.getAgentSnapshot(sessionId);

    assert.deepEqual(
      snapshot.messages.filter((message) => message.role === "user").map((message) => message.text),
      ["screenshot"],
    );
    // The tool's reference is stored for display, but the model saw only the text.
    const toolMessage = snapshot.messages.find((message) => message.role === "tool");
    assert.equal(toolMessage?.images?.length, 1);
    assert.deepEqual(lastMessage(readLoggedPrompts(stateDirectory).at(-1)), {
      role: "tool",
      parts: [
        {
          type: "tool-result",
          toolName: "take_screenshot",
          output: JSON.stringify({ type: "text", value: "Took a screenshot." }),
        },
      ],
    });
  });
});
