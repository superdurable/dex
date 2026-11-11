// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Images through a real session: stored as references, replayed, and uploaded once.

import assert from "node:assert/strict";
import { createHash, randomUUID } from "node:crypto";
import { existsSync, mkdtempSync, readdirSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { after, afterEach, before, describe, test } from "node:test";

import {
  ImageValidationError,
  InMemoryMediaStore,
  LocalDirectoryMediaStore,
  imageUri,
  outcomeUnknownToolResult,
  sendAgentUserMessage,
  turnFailureNoteHeader,
  type AgentImageReference,
} from "../../src/index.js";
import {
  AgentSessionTestClient,
  AgentTestEnvironment,
  FileGate,
  InvocationLog,
  KillableWorkerProcess,
  PrivateDexEngine,
} from "../../src/testing/index.js";
import { readConformanceJson } from "../fixtures/conformance-suite.js";
import {
  createImageTestAgentFlow,
  imageLoadsLogName,
  imageTestAgentStateDirectoryVariable,
  mediaDirectoryName,
  screenshotPng,
  uploadsLogName,
} from "../fixtures/image-test-agent.js";
import { createScriptedAgentFlow } from "../fixtures/scripted-agent-runner.js";

const workerEntryModulePath = fileURLToPath(new URL("../fixtures/image-test-agent-worker.js", import.meta.url));
const images = new Map(
  readConformanceJson<{ readonly images: readonly { readonly name: string; readonly base64: string }[] }>(
    "images.json",
  ).images.map((image) => [image.name, new Uint8Array(Buffer.from(image.base64, "base64"))]),
);
const png = images.get("png") as Uint8Array;
const jpeg = images.get("jpeg") as Uint8Array;

function loaded(reference: AgentImageReference): string {
  return `${reference.sha256}:${reference.sizeBytes}`;
}

function newSessionId(label: string): string {
  return `image-${label}-${randomUUID()}`;
}

/** Drops one object from an in-memory store, as an expired bucket object would be gone. */
function forgetImage(store: InMemoryMediaStore, sha256: string): void {
  (store as unknown as { readonly images: Map<string, Uint8Array> }).images.delete(sha256);
}

/** Lists the image files of the Worker's local directory store, skipping temporary files. */
function imageFileNames(stateDirectory: string): string[] {
  const directory = join(stateDirectory, mediaDirectoryName, "sha256");
  return existsSync(directory)
    ? readdirSync(directory)
        .filter((name) => !name.startsWith("."))
        .sort()
    : [];
}

function sha256Of(data: Uint8Array): string {
  return createHash("sha256").update(data).digest("hex");
}

describe("images in a session with an in-process Worker and the in-memory store", () => {
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  let sessions: AgentSessionTestClient;
  let scriptedSessions: AgentSessionTestClient;
  let stateDirectory: string;
  let store: InMemoryMediaStore;

  before(async () => {
    engine = await PrivateDexEngine.start();
    stateDirectory = mkdtempSync(join(engine.stateDirectory, "images-"));
    store = new InMemoryMediaStore();
    const flow = createImageTestAgentFlow(stateDirectory, store);
    const scriptedFlow = createScriptedAgentFlow(join(stateDirectory, "scripted"));
    environment = await AgentTestEnvironment.start({
      serverAddress: engine.serverAddress,
      flows: [flow, scriptedFlow],
      isWorkerInProcess: true,
    });
    sessions = new AgentSessionTestClient(environment.client, flow);
    scriptedSessions = new AgentSessionTestClient(environment.client, scriptedFlow);
  });

  after(async () => {
    await environment?.close();
    await engine?.stop();
  });

  function imageLoads(): InvocationLog {
    return new InvocationLog(join(stateDirectory, imageLoadsLogName));
  }

  test("a user message with images is stored as references and replays", async () => {
    const pngReference = await store.putImage(png);
    const jpegReference = await store.putImage(jpeg);
    const sessionId = newSessionId("replay");
    await sessions.startSession(sessionId);
    const loadsBefore = imageLoads().names().length;

    assert.equal(await sessions.sendUserMessage(sessionId, "describe", false, [pngReference, jpegReference]), true);
    await sessions.waitForCompletedTurns(sessionId, 1);
    assert.equal(await sessions.sendUserMessage(sessionId, "describe"), true);
    await sessions.waitForCompletedTurns(sessionId, 2);
    const snapshot = await sessions.getAgentSnapshot(sessionId);

    const [userMessage] = snapshot.messages;
    assert.equal(userMessage?.role, "user");
    assert.deepEqual(userMessage?.images, [pngReference, jpegReference]);
    assert.deepEqual(
      (JSON.parse(userMessage?.frameworkItem ?? "{}") as { readonly images: readonly { readonly sha256: string }[] })
        .images.map((image) => image.sha256),
      [pngReference.sha256, jpegReference.sha256],
    );
    assert.equal(snapshot.messages[1]?.text, "saw 2 images");
    assert.equal(snapshot.messages[3]?.text, "saw 2 images");
    assert.equal(snapshot.messages[2]?.images, undefined);
    assert.deepEqual(imageLoads().names().slice(loadsBefore), [
      `turn-1:1:${loaded(pngReference)},${loaded(jpegReference)}`,
      `turn-2:1:${loaded(pngReference)},${loaded(jpegReference)}`,
    ]);
    const stored = JSON.stringify(snapshot.messages.map((message) => message.frameworkItem));
    assert.equal(stored.includes(Buffer.from(png).toString("base64")), false);
  });

  test("a queued message keeps its images when steered", async () => {
    const pngReference = await store.putImage(png);
    const sessionId = newSessionId("steer");
    const gateName = `model-gate-${randomUUID()}`;
    await sessions.startSession(sessionId, `stall ${gateName}`);
    const gate = new FileGate(stateDirectory, gateName);
    await gate.waitUntilReached();
    assert.equal(await sessions.sendUserMessage(sessionId, "describe", false, [pngReference]), true);
    const queued = await sessions.waitForAgentSnapshot(
      sessionId,
      (snapshot) => (snapshot.queuedMessages ?? []).length > 0,
      "a queued message",
    );
    const [pending] = queued.queuedMessages ?? [];
    assert.deepEqual(pending?.message.images, [pngReference]);

    assert.equal(await sessions.steerQueuedMessage(sessionId, pending?.messageId ?? ""), true);
    gate.release();
    await sessions.waitForCompletedTurns(sessionId, 2);
    const snapshot = await sessions.getAgentSnapshot(sessionId);

    const steered = snapshot.messages.filter((message) => message.text === "describe");
    assert.deepEqual(
      steered.map((message) => message.images),
      [[pngReference]],
    );
    assert.equal(snapshot.messages.at(-1)?.text, "saw 1 images");
  });

  test("a tool image is stored as a reference on the record and the message", async () => {
    const sessionId = newSessionId("tool");
    const gateName = `hold-gate-${randomUUID()}`;
    const loadsBefore = imageLoads().names().length;
    await sessions.startSession(sessionId, `screenshot-hold ${gateName}`);
    const gate = new FileGate(stateDirectory, gateName);
    await gate.waitUntilReached();
    const sha256 = sha256Of(screenshotPng());
    const expected: AgentImageReference = {
      type: "image",
      uri: imageUri(store.storeUri, sha256),
      mimeType: "image/png",
      sha256,
      sizeBytes: screenshotPng().length,
    };

    const held = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(
      held.toolCalls.map((record) => record.images),
      [[expected]],
    );
    gate.release();
    await sessions.waitForCompletedTurns(sessionId, 1);
    const snapshot = await sessions.getAgentSnapshot(sessionId);

    const toolMessages = snapshot.messages.filter((message) => message.role === "tool");
    assert.deepEqual(
      toolMessages.map((message) => message.images),
      [[expected]],
    );
    assert.deepEqual(imageLoads().names().slice(loadsBefore), ["turn-1:1:", `turn-1:2:${loaded(expected)}`]);
  });

  test("a tool result with an invalid image fails the attempt", async () => {
    const sessionId = newSessionId("bad-tool");
    await sessions.startSession(sessionId, "bad-screenshot");
    await sessions.waitForCompletedTurns(sessionId, 1);
    const snapshot = await sessions.getAgentSnapshot(sessionId);

    const toolMessages = snapshot.messages.filter((message) => message.role === "tool");
    assert.deepEqual(
      toolMessages.map((message) => [message.toolOutput, message.images]),
      [[outcomeUnknownToolResult, undefined]],
    );
  });

  test("invalid images are refused before and by the session", async () => {
    const pngReference = await store.putImage(png);
    const sessionId = newSessionId("refused");
    await sessions.startSession(sessionId);
    const oversized = { ...pngReference, sizeBytes: 3_750_001 };

    await assert.rejects(
      sendAgentUserMessage(environment.client, sessions.flow, sessionId, { text: "describe", images: [oversized] }),
      { name: "ImageValidationError", message: "images[0]: image reference size_bytes must be from 1 to 3750000" },
    );
    await assert.rejects(
      sendAgentUserMessage(environment.client, sessions.flow, sessionId, {
        text: "describe",
        images: Array.from({ length: 21 }, () => pngReference),
      }),
      ImageValidationError,
    );
    for (const refused of [
      [oversized],
      [{ ...pngReference, mimeType: "image/svg+xml" }],
      [{ ...pngReference, uri: imageUri("s3://elsewhere", "0".repeat(64)) }],
      Array.from({ length: 21 }, () => pngReference),
    ]) {
      assert.equal(await sessions.sendUserMessage(sessionId, "describe", false, refused), false);
    }
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(snapshot.queuedMessages ?? [], []);
    assert.deepEqual(snapshot.messages, []);
  });

  test("a model answer with invalid images fails the turn and stores nothing", async () => {
    const sessionId = newSessionId("bad-answer");
    await sessions.startSession(sessionId, "bad-answer-images");
    await sessions.waitForCompletedTurns(sessionId, 1);
    const snapshot = await sessions.getAgentSnapshot(sessionId);

    assert.match(snapshot.state?.lastTurnError ?? "", /images\[0\]: image reference mime_type must be one of/);
    assert.deepEqual(
      snapshot.messages.map((message) => message.text),
      ["bad-answer-images"],
    );
  });

  test("an incremental item with invalid images fails the model call before any tool", async () => {
    const sessionId = newSessionId("bad-incremental");
    const uploadsBefore = new InvocationLog(join(stateDirectory, uploadsLogName)).names().length;
    await sessions.startSession(sessionId, "bad-incremental-images");
    await sessions.waitForCompletedTurns(sessionId, 1);
    const snapshot = await sessions.getAgentSnapshot(sessionId);

    assert.match(snapshot.state?.lastTurnError ?? "", /images\[0\]: image reference mime_type must be one of/);
    assert.deepEqual(
      snapshot.messages.map((message) => message.text),
      ["bad-incremental-images"],
    );
    assert.equal(new InvocationLog(join(stateDirectory, uploadsLogName)).names().length, uploadsBefore);
  });

  test("unfinished-turn items with invalid images leave only the failure note", async () => {
    const sessionId = newSessionId("bad-unfinished");
    await sessions.startSession(sessionId, "screenshot-then-fail");
    await sessions.waitForCompletedTurns(sessionId, 1);
    const snapshot = await sessions.getAgentSnapshot(sessionId);

    assert.match(snapshot.state?.lastTurnError ?? "", /scripted failure after the screenshot/);
    assert.deepEqual(
      snapshot.messages.map((message) => message.role),
      ["user", "user"],
    );
    assert.equal(snapshot.messages[1]?.text?.startsWith(turnFailureNoteHeader), true);
    assert.equal(
      snapshot.messages.every((message) => message.images === undefined),
      true,
    );
  });

  test("a replay whose image object is gone fails the turn cleanly", async () => {
    const pngReference = await store.putImage(png);
    const sessionId = newSessionId("missing-object");
    await sessions.startSession(sessionId);
    assert.equal(await sessions.sendUserMessage(sessionId, "describe", false, [pngReference]), true);
    await sessions.waitForCompletedTurns(sessionId, 1);

    // A lifecycle rule that expired the object before the session stopped replaying it.
    forgetImage(store, pngReference.sha256);
    assert.equal(await sessions.sendUserMessage(sessionId, "describe"), true);
    await sessions.waitForCompletedTurns(sessionId, 2);
    const snapshot = await sessions.getAgentSnapshot(sessionId);

    assert.match(snapshot.state?.lastTurnError ?? "", new RegExp(`image ${pngReference.uri} is not in the media store`));
    assert.deepEqual(
      snapshot.messages.map((message) => message.text),
      ["describe", "saw 1 images", "describe"],
    );
    assert.deepEqual(snapshot.messages[0]?.images, [pngReference]);
    assert.deepEqual(snapshot.toolCalls, []);
  });

  test("a runner without image support refuses images", async () => {
    const pngReference = await store.putImage(png);
    const sessionId = newSessionId("no-support");
    await scriptedSessions.startSession(sessionId);

    assert.equal(await scriptedSessions.sendUserMessage(sessionId, "say hi", false, [pngReference]), false);
    const snapshot = await scriptedSessions.getAgentSnapshot(sessionId);
    assert.deepEqual(snapshot.queuedMessages ?? [], []);
  });
});

describe("images in a session whose Worker process is killed", () => {
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  let sessions: AgentSessionTestClient;
  const workers: KillableWorkerProcess[] = [];

  before(async () => {
    engine = await PrivateDexEngine.start();
    const flow = createImageTestAgentFlow(
      join(engine.stateDirectory, "client-unused"),
      new InMemoryMediaStore("client-unused"),
    );
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

  async function startWorker(stateDirectory: string): Promise<KillableWorkerProcess> {
    const worker = await KillableWorkerProcess.start({
      entryModulePath: workerEntryModulePath,
      serverAddress: engine.serverAddress,
      logPath: join(stateDirectory, "worker.log"),
      environment: { [imageTestAgentStateDirectoryVariable]: stateDirectory },
    });
    workers.push(worker);
    return worker;
  }

  test("the retried tool uploads the same single object again", async () => {
    const stateDirectory = mkdtempSync(join(engine.stateDirectory, "kill-"));
    const worker = await startWorker(stateDirectory);
    const sessionId = newSessionId("kill");
    await sessions.startSession(sessionId, "screenshot-gate upload-gate");
    await new FileGate(stateDirectory, "upload-gate").waitUntilReached();

    await worker.kill();
    await startWorker(stateDirectory);
    await sessions.waitForCompletedTurns(sessionId, 1);
    const snapshot = await sessions.getAgentSnapshot(sessionId);

    const sha256 = sha256Of(screenshotPng());
    assert.deepEqual(new InvocationLog(join(stateDirectory, uploadsLogName)).names(), [
      `upload ${sha256} attempt 1`,
      `upload ${sha256} attempt 2`,
    ]);
    assert.deepEqual(imageFileNames(stateDirectory), [sha256]);
    const toolMessages = snapshot.messages.filter((message) => message.role === "tool");
    assert.deepEqual(
      toolMessages.map((message) => [message.toolOutput, (message.images ?? []).map((image) => image.sha256)]),
      [["took a screenshot", [sha256]]],
    );
  });

  test("an image replays from the local directory after a Worker restart", async () => {
    const stateDirectory = mkdtempSync(join(engine.stateDirectory, "restart-"));
    const store = new LocalDirectoryMediaStore(join(stateDirectory, mediaDirectoryName));
    const worker = await startWorker(stateDirectory);
    const pngReference = await store.putImage(png);
    const sessionId = newSessionId("restart");
    await sessions.startSession(sessionId);
    assert.equal(await sessions.sendUserMessage(sessionId, "describe", false, [pngReference]), true);
    await sessions.waitForCompletedTurns(sessionId, 1);

    await worker.kill();
    await startWorker(stateDirectory);
    assert.equal(await sessions.sendUserMessage(sessionId, "describe"), true);
    await sessions.waitForCompletedTurns(sessionId, 2);
    const snapshot = await sessions.getAgentSnapshot(sessionId);

    assert.ok(pngReference.uri.startsWith("file://"));
    assert.deepEqual(snapshot.messages[0]?.images, [pngReference]);
    assert.equal(snapshot.messages.at(-1)?.text, "saw 1 images");
    // The new Worker process read the bytes from the same directory.
    assert.deepEqual(new InvocationLog(join(stateDirectory, imageLoadsLogName)).names(), [
      `turn-1:1:${loaded(pngReference)}`,
      `turn-2:1:${loaded(pngReference)}`,
    ]);
    assert.deepEqual(imageFileNames(stateDirectory), [pngReference.sha256]);
  });
});
