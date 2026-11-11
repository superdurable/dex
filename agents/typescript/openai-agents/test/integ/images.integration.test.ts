// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { mkdtempSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { after, afterEach, before, describe, test } from "node:test";

import type { AgentSessionFlow } from "@superdurable/dex-agents";
import {
  AgentSessionTestClient,
  AgentTestEnvironment,
  FileGate,
  InvocationLog,
  KillableWorkerProcess,
  PrivateDexEngine,
} from "@superdurable/dex-agents/testing";

import {
  createImageMediaStore,
  createImageOpenAIAgentFlow,
  imageAgentMarkerDirectoryVariable,
  inputFilterLogName,
  screenshotJpeg,
  userPng,
} from "../fixtures/image-openai-agent.js";

const workerEntryModulePath = fileURLToPath(new URL("../fixtures/image-openai-agent-worker.js", import.meta.url));
const userPngUrl = `data:image/png;base64,${Buffer.from(userPng).toString("base64")}`;
const screenshotJpegUrl = `data:image/jpeg;base64,${Buffer.from(screenshotJpeg).toString("base64")}`;

describe("OpenAI Agents SDK sessions show images to the model and store only references", () => {
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  let flow: AgentSessionFlow;
  let sessions: AgentSessionTestClient;
  const workers: KillableWorkerProcess[] = [];

  before(async () => {
    engine = await PrivateDexEngine.start();
    flow = createImageOpenAIAgentFlow(join(engine.stateDirectory, "client-unused-markers"));
    environment = await AgentTestEnvironment.start({
      serverAddress: engine.serverAddress,
      flows: [flow],
      isWorkerInProcess: false,
    });
    sessions = new AgentSessionTestClient(environment.client, flow);
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

  async function startWorker(markerDirectory: string): Promise<KillableWorkerProcess> {
    const worker = await KillableWorkerProcess.start({
      entryModulePath: workerEntryModulePath,
      serverAddress: engine.serverAddress,
      logPath: join(markerDirectory, "worker.log"),
      environment: { [imageAgentMarkerDirectoryVariable]: markerDirectory },
    });
    workers.push(worker);
    return worker;
  }

  test("user and tool images reach the model as bytes, while items and run state keep references", async () => {
    const markerDirectory = mkdtempSync(join(engine.stateDirectory, "markers-"));
    await startWorker(markerDirectory);
    const mediaStore = createImageMediaStore(markerDirectory);
    const png = await mediaStore.putImage(userPng);
    const sessionId = `images-${randomUUID()}`;
    await sessions.startSession(sessionId);

    assert.equal(
      await sessions.sendUserMessage(sessionId, "call take_screenshot then call lookup_a", false, [png]),
      true,
    );
    const lookupGate = new FileGate(markerDirectory, "lookups");
    await lookupGate.waitUntilReached();
    const [runState] = await environment.client.getAttributes(sessionId, flow.frameworkRunState);
    lookupGate.release();
    await sessions.waitForCompletedTurns(sessionId, 1);
    assert.equal(await sessions.sendUserMessage(sessionId, "again"), true);
    await sessions.waitForCompletedTurns(sessionId, 2);

    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.equal(snapshot.state?.lastTurnError, null);
    const answers = snapshot.messages.filter((message) => message.role === "assistant" && message.text !== null);
    assert.deepEqual(
      answers.map((message) => message.text),
      ["take_screenshot -> took a screenshot | lookup_a -> lookup_a found", "echo: again"],
    );
    const toolImage = `call_1_take_screenshot ${screenshotJpegUrl}`;
    assert.deepEqual(readRequestImages(markerDirectory), [
      `1 image user ${userPngUrl}`,
      `2 image user ${userPngUrl}`,
      `2 image ${toolImage}`,
      `3 image user ${userPngUrl}`,
      `3 image ${toolImage}`,
      // The second turn rebuilds its input from the stored items alone.
      `4 image user ${userPngUrl}`,
      `4 image ${toolImage}`,
    ]);
    // The application's own filter runs first and sees only placeholders.
    assert.deepEqual(new InvocationLog(join(markerDirectory, inputFilterLogName)).names(), [
      "images 1 bytes false",
      "images 2 bytes false",
      "images 2 bytes false",
      "images 2 bytes false",
    ]);

    // Uploading the same bytes again returns the reference the tool stored.
    const screenshot = await mediaStore.putImage(screenshotJpeg);
    const userMessage = snapshot.messages[0];
    assert.deepEqual(userMessage?.images, [png]);
    assert.deepEqual((JSON.parse(userMessage?.frameworkItem ?? "{}") as { content?: unknown }).content, [
      { type: "input_text", text: "call take_screenshot then call lookup_a" },
      { type: "input_image", image: png.uri, detail: "auto" },
    ]);
    const toolMessage = snapshot.messages.find(
      (message) => message.role === "tool" && message.toolName === "take_screenshot",
    );
    assert.equal(toolMessage?.toolOutput, "took a screenshot");
    assert.deepEqual(toolMessage?.images, [screenshot]);
    assert.deepEqual((JSON.parse(toolMessage?.frameworkItem ?? "{}") as { output?: unknown }).output, [
      { type: "input_text", text: "took a screenshot" },
      { type: "input_image", image: screenshot.uri, detail: "auto" },
    ]);
    assert.ok(typeof runState === "string" && runState.includes(png.uri) && runState.includes(screenshot.uri));
    const stored = JSON.stringify(snapshot.messages.map((message) => message.frameworkItem)) + runState;
    for (const image of [userPng, screenshotJpeg]) {
      assert.equal(stored.includes(Buffer.from(image).toString("base64")), false);
    }
  });

  test("a Worker killed in a resumed model call rebuilds the same model input", async () => {
    const markerDirectory = mkdtempSync(join(engine.stateDirectory, "markers-"));
    const firstWorker = await startWorker(markerDirectory);
    const png = await createImageMediaStore(markerDirectory).putImage(userPng);
    const sessionId = `images-kill-${randomUUID()}`;
    await sessions.startSession(sessionId);

    assert.equal(await sessions.sendUserMessage(sessionId, "call take_screenshot then pause", false, [png]), true);
    const turnGate = new FileGate(markerDirectory, "turn");
    await turnGate.waitUntilReached();
    await firstWorker.kill();
    turnGate.release();
    await startWorker(markerDirectory);
    await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });

    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.equal(snapshot.state?.lastTurnError, null);
    assert.equal(snapshot.messages.at(-1)?.text, "take_screenshot -> took a screenshot");
    assert.equal(turnGate.reachedCount(), 2);
    const requestLines = new InvocationLog(join(markerDirectory, "model-requests.log")).names();
    const digests = requestLines.filter((line) => line.includes(" input ")).map((line) => line.split(" input ")[1]);
    assert.equal(digests.length, 3);
    assert.equal(digests[1], digests[2]);
    const toolImage = `call_1_take_screenshot ${screenshotJpegUrl}`;
    assert.deepEqual(readRequestImages(markerDirectory).slice(1), [
      `2 image user ${userPngUrl}`,
      `2 image ${toolImage}`,
      `3 image user ${userPngUrl}`,
      `3 image ${toolImage}`,
    ]);
    assert.deepEqual(new InvocationLog(join(markerDirectory, "take_screenshot.log")).names(), ["take_screenshot"]);
  });
});

function readRequestImages(markerDirectory: string): string[] {
  return new InvocationLog(join(markerDirectory, "model-requests.log"))
    .names()
    .filter((line) => line.includes(" image "));
}
