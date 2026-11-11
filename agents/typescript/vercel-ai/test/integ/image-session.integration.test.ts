// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Images through Vercel AI SDK sessions: file parts in the model prompt, references in stored state.

import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { mkdtempSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { after, afterEach, before, describe, test } from "node:test";

import { LocalDirectoryMediaStore, type AgentImageReference } from "@superdurable/dex-agents";
import {
  AgentSessionTestClient,
  AgentTestEnvironment,
  FileGate,
  KillableWorkerProcess,
  PrivateDexEngine,
} from "@superdurable/dex-agents/testing";

import { readRecordedModelPrompts, type RecordedModelPrompt } from "../fixtures/scripted-language-model.js";
import {
  createScriptedVercelAiAgentFlow,
  scriptedAgentMarkerDirectoryVariable,
  scriptedAgentMediaDirectoryVariable,
} from "../fixtures/scripted-vercel-ai-agent.js";
import { screenshotPng, screenshotToolOutput, userImagePng } from "../fixtures/screenshot-tool-source.js";

const workerEntryModulePath = fileURLToPath(
  new URL("../fixtures/scripted-vercel-ai-agent-worker.js", import.meta.url),
);
const userImageBase64 = Buffer.from(userImagePng).toString("base64");
const screenshotBase64 = Buffer.from(screenshotPng).toString("base64");

interface PromptMessage {
  readonly role: string;
  readonly content: unknown;
}

/** The file part the AI SDK passes to the model for one PNG. */
function promptFilePart(base64: string): unknown {
  return { type: "file", mediaType: "image/png", data: { type: "data", data: { base64 } } };
}

function promptMessages(recorded: RecordedModelPrompt | undefined): PromptMessage[] {
  return (recorded?.prompt ?? []) as PromptMessage[];
}

/** The user message and tool results of a prompt that this test's command produced. */
function findUserContent(messages: readonly PromptMessage[], text: string): unknown {
  return messages.find(
    (message) =>
      message.role === "user" &&
      Array.isArray(message.content) &&
      (message.content as { readonly text?: string }[]).some((part) => part.text === text),
  )?.content;
}

function findToolResultOutput(messages: readonly PromptMessage[], toolName: string): unknown {
  for (const message of messages) {
    if (message.role !== "tool" || !Array.isArray(message.content)) {
      continue;
    }
    for (const part of message.content as { readonly toolName?: string; readonly output?: unknown }[]) {
      if (part.toolName === toolName) {
        return part.output;
      }
    }
  }
  return undefined;
}

function assertNoImageBytes(text: string): void {
  assert.equal(text.includes(userImageBase64), false);
  assert.equal(text.includes(screenshotBase64), false);
}

describe("Vercel AI SDK sessions show images with an in-process Worker", () => {
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  let imageSessions: AgentSessionTestClient;
  let plainSessions: AgentSessionTestClient;
  let markerDirectory: string;
  let store: LocalDirectoryMediaStore;

  before(async () => {
    engine = await PrivateDexEngine.start();
    markerDirectory = mkdtempSync(join(engine.stateDirectory, "markers-"));
    store = new LocalDirectoryMediaStore(mkdtempSync(join(engine.stateDirectory, "media-")));
    const imageFlow = createScriptedVercelAiAgentFlow(markerDirectory, {
      flowType: "ScriptedVercelAiAgentWithImages",
      mediaStore: store,
    });
    const plainFlow = createScriptedVercelAiAgentFlow(markerDirectory);
    environment = await AgentTestEnvironment.start({
      serverAddress: engine.serverAddress,
      flows: [imageFlow, plainFlow],
      isWorkerInProcess: true,
    });
    imageSessions = new AgentSessionTestClient(environment.client, imageFlow);
    plainSessions = new AgentSessionTestClient(environment.client, plainFlow);
  });

  after(async () => {
    await environment?.close();
    await engine?.stop();
  });

  test("a user image and a tool image reach the model as file parts, and stored state holds only references", async () => {
    const userImage = await store.putImage(userImagePng);
    const screenshot = await store.putImage(screenshotPng);
    const sessionId = `images-${randomUUID()}`;
    const command = "call take_screenshot then send_email";
    await imageSessions.startSession(sessionId);
    assert.equal(await imageSessions.sendUserMessage(sessionId, command, false, [userImage]), true);

    const interrupt = await imageSessions.waitForPendingInterrupt(sessionId, "send_email");
    const [runState] = await environment.client.getAttributes(sessionId, imageSessions.flow.frameworkRunState);
    assert.ok(runState?.includes(`[image ${screenshot.uri}]`));
    assertNoImageBytes(runState ?? "");
    const pendingSnapshot = await imageSessions.getAgentSnapshot(sessionId);
    assert.deepEqual(
      pendingSnapshot.toolCalls.find((record) => record.toolName === "take_screenshot")?.images,
      [screenshot] satisfies AgentImageReference[],
    );
    assert.equal(await imageSessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    await imageSessions.waitForCompletedTurns(sessionId, 1);
    assert.equal(await imageSessions.sendUserMessage(sessionId, "history"), true);
    const state = await imageSessions.waitForCompletedTurns(sessionId, 2);
    assert.equal(state.lastTurnError, null);

    const prompts = readRecordedModelPrompts(markerDirectory, sessionId);
    assert.equal(prompts.length, 4);
    const expectedUserContent = [{ type: "text", text: command }, promptFilePart(userImageBase64)];
    const expectedScreenshotOutput = {
      type: "content",
      value: [{ type: "text", text: screenshotToolOutput }, promptFilePart(screenshotBase64)],
    };
    for (const recorded of prompts) {
      assert.deepEqual(findUserContent(promptMessages(recorded), command), expectedUserContent);
    }
    // The last two model calls replay the screenshot: once from the run state, once from stored history.
    for (const recorded of prompts.slice(2)) {
      assert.deepEqual(findToolResultOutput(promptMessages(recorded), "take_screenshot"), expectedScreenshotOutput);
    }

    const snapshot = await imageSessions.getAgentSnapshot(sessionId);
    const userMessage = snapshot.messages.find((message) => message.text === command);
    assert.deepEqual(userMessage?.images, [userImage]);
    const screenshotMessage = snapshot.messages.find(
      (message) => message.role === "tool" && message.toolName === "take_screenshot",
    );
    assert.deepEqual(screenshotMessage?.images, [screenshot]);
    assert.equal(screenshotMessage?.toolOutput, screenshotToolOutput);
    assert.equal(snapshot.messages.at(-1)?.text?.startsWith("history | user: call take_screenshot then send_email"), true);
    assertNoImageBytes(JSON.stringify(snapshot.messages.map((message) => message.frameworkItem)));
  });

  test("a session whose runner has no media store refuses images", async () => {
    const userImage = await store.putImage(userImagePng);
    const sessionId = `no-store-${randomUUID()}`;
    await plainSessions.startSession(sessionId);
    assert.equal(await plainSessions.sendUserMessage(sessionId, "look at this", false, [userImage]), false);
    assert.equal(await plainSessions.sendUserMessage(sessionId, "look at this"), true);
    await plainSessions.waitForCompletedTurns(sessionId, 1);
    const snapshot = await plainSessions.getAgentSnapshot(sessionId);
    assert.deepEqual(
      snapshot.messages.map((message) => message.text),
      ["look at this", "echo: look at this"],
    );
  });
});

describe("Vercel AI SDK sessions replay images after a Worker killed with SIGKILL", () => {
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  let sessions: AgentSessionTestClient;
  let mediaDirectory: string;
  const workers: KillableWorkerProcess[] = [];

  before(async () => {
    engine = await PrivateDexEngine.start();
    mediaDirectory = mkdtempSync(join(engine.stateDirectory, "media-"));
    const flow = createScriptedVercelAiAgentFlow(join(engine.stateDirectory, "client-unused-markers"));
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
      environment: {
        [scriptedAgentMarkerDirectoryVariable]: markerDirectory,
        [scriptedAgentMediaDirectoryVariable]: mediaDirectory,
      },
    });
    workers.push(worker);
    return worker;
  }

  test("the model call rerun after a Worker restart sends the same prompt, image bytes included", async () => {
    const markerDirectory = mkdtempSync(join(engine.stateDirectory, "markers-"));
    const store = new LocalDirectoryMediaStore(mediaDirectory);
    const userImage = await store.putImage(userImagePng);
    const firstWorker = await startWorker(markerDirectory);
    const sessionId = `image-restart-${randomUUID()}`;
    const command = "call take_screenshot then pause";
    await sessions.startSession(sessionId);
    assert.equal(await sessions.sendUserMessage(sessionId, command, false, [userImage]), true);
    const turnGate = new FileGate(markerDirectory, "turn");
    await turnGate.waitUntilReached();

    await firstWorker.kill();
    turnGate.release();
    await startWorker(markerDirectory);

    const state = await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });
    assert.equal(state.lastTurnError, null);
    const prompts = readRecordedModelPrompts(markerDirectory, sessionId);
    assert.deepEqual(
      prompts.map((recorded) => [recorded.turnId, recorded.attempt]),
      [
        ["turn-1", 1],
        ["turn-1", 1],
        ["turn-1", 2],
      ],
    );
    const [, beforeRestart, afterRestart] = prompts.map(promptMessages);
    assert.deepEqual(afterRestart, beforeRestart);
    assert.deepEqual(findUserContent(afterRestart ?? [], command), [
      { type: "text", text: command },
      promptFilePart(userImageBase64),
    ]);
    assert.deepEqual(findToolResultOutput(afterRestart ?? [], "take_screenshot"), {
      type: "content",
      value: [{ type: "text", text: screenshotToolOutput }, promptFilePart(screenshotBase64)],
    });
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.equal(snapshot.messages.at(-1)?.text, `take_screenshot -> ${screenshotToolOutput}`);
    assertNoImageBytes(JSON.stringify(snapshot.messages.map((message) => message.frameworkItem)));
  });
});
