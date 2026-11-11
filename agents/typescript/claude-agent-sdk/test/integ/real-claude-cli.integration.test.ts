// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { existsSync, mkdirSync } from "node:fs";
import { createRequire } from "node:module";
import { dirname, join } from "node:path";
import { after, afterEach, before, describe, test } from "node:test";

import type { PermissionMode } from "@anthropic-ai/claude-agent-sdk";
import { PrivateDexEngine } from "@superdurable/dex-agents/testing";

import { claudeTranscriptItemKind } from "../../src/index.js";
import { ClaudeAgentTestHarness } from "../fixtures/claude-agent-test-harness.js";
import {
  claudeTestAgentCliVariable,
  claudeTestAgentMediaStoreVariable,
  conformanceImage,
  createMediaStore,
  type ClaudeTestAgentCli,
} from "../fixtures/claude-test-agent.js";
import { FakeLedgerProvider } from "../fixtures/fake-ledger.js";
import {
  mockScriptMarker,
  MockAnthropicApi,
  RefusingProxy,
  type MockApiRequest,
} from "../fixtures/mock-anthropic-api.js";

/** Set by ci.sh so a missing native binary fails the suite instead of skipping it. */
const isRealCliRequiredVariable = "CLAUDE_AGENT_SDK_REAL_CLI_REQUIRED";
const resumePrompt = "Continue from where you left off.";
const realCliPath = findBundledClaudeCli();
const skipReason =
  realCliPath === undefined && process.env[isRealCliRequiredVariable] !== "1"
    ? "the native Claude Code binary is not installed (npm ci --omit=optional)"
    : false;

describe("the adapter with the real bundled Claude Code CLI against a scripted Messages API", { skip: skipReason }, () => {
  let engine: PrivateDexEngine;
  let api: MockAnthropicApi;
  let proxy: RefusingProxy;
  let harness: ClaudeAgentTestHarness | undefined;

  before(async () => {
    assert.ok(realCliPath !== undefined, "the native Claude Code binary of @anthropic-ai/claude-agent-sdk is missing");
    engine = await PrivateDexEngine.start();
    api = await MockAnthropicApi.start();
    proxy = await RefusingProxy.start();
  });

  afterEach(async () => {
    await harness?.close();
    harness = undefined;
  });

  after(async () => {
    // Every request went to the mock: nothing reached the network or an unserved endpoint.
    assert.deepEqual(proxy?.refusedTargets ?? [], []);
    assert.deepEqual(api?.unservedPaths ?? [], []);
    await proxy?.stop();
    await api?.stop();
    await engine?.stop();
  });

  function realCli(stateDirectory: string, flowType: string, permissionMode?: PermissionMode): ClaudeTestAgentCli {
    mkdirSync(join(stateDirectory, "home"), { recursive: true });
    return {
      flowType,
      executablePath: realCliPath as string,
      model: "claude-sonnet-4-5",
      ...(permissionMode === undefined ? {} : { permissionMode }),
      // A closed environment: never the developer's HOME, credentials, or the real API.
      environment: {
        PATH: "/usr/bin:/bin",
        HOME: join(stateDirectory, "home"),
        ANTHROPIC_BASE_URL: api.baseUrl,
        ANTHROPIC_API_KEY: "sk-ant-test-not-a-real-key",
        CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: "1",
        DISABLE_AUTOUPDATER: "1",
        HTTPS_PROXY: proxy.url,
        HTTP_PROXY: proxy.url,
        NO_PROXY: "127.0.0.1,localhost",
      },
    };
  }

  async function startHarness(permissionMode?: PermissionMode): Promise<ClaudeAgentTestHarness> {
    harness = await ClaudeAgentTestHarness.start(engine, true, (stateDirectory) =>
      realCli(
        stateDirectory,
        permissionMode === undefined ? "RealClaudeCliAgent" : `RealClaudeCliAgent-${permissionMode}`,
        permissionMode,
      ),
    );
    return harness;
  }

  // A child Worker builds the same Flow from these variables, media store included.
  async function startImageHarness(
    isWorkerInProcess: boolean,
  ): Promise<{ agent: ClaudeAgentTestHarness; workerEnvironment: Record<string, string> }> {
    let cli: ClaudeTestAgentCli | undefined;
    harness = await ClaudeAgentTestHarness.start(
      engine,
      isWorkerInProcess,
      (stateDirectory) => {
        cli = realCli(stateDirectory, "RealClaudeCliImageAgent");
        return cli;
      },
      undefined,
      true,
    );
    return {
      agent: harness,
      workerEnvironment: { [claudeTestAgentCliVariable]: JSON.stringify(cli), [claudeTestAgentMediaStoreVariable]: "1" },
    };
  }

  function scriptedRequestsOf(command: string): number {
    return api.scriptedRequests().filter((request) => request.command === command).length;
  }

  test("a read tool runs inline and the next turn resumes the stored Claude session", async () => {
    const agent = await startHarness();
    const sessionId = agent.newSessionId("real-read");
    await agent.sessions.startSession(sessionId, `${mockScriptMarker}call lookup {"query":"docs"}`);
    const first = await agent.waitForTurns(sessionId, 1);

    assert.equal(first.state?.lastTurnError, null);
    assert.deepEqual(
      first.messages.map((message) => [message.role, message.toolName]),
      [
        ["user", null],
        ["assistant", "lookup"],
        ["tool", "lookup"],
        ["assistant", null],
      ],
    );
    assert.equal(first.messages[2]?.toolOutput, "found docs");
    assert.equal(first.messages.at(-1)?.text, "lookup: found docs");
    for (const message of first.messages.slice(1)) {
      assert.equal((JSON.parse(message.frameworkItem) as { kind: string }).kind, claudeTranscriptItemKind);
    }

    assert.equal(await agent.sessions.sendUserMessage(sessionId, `${mockScriptMarker}recall`), true);
    const second = await agent.waitForTurns(sessionId, 2);
    assert.equal(second.messages.at(-1)?.text, 'history: call lookup {"query":"docs"}');
    assert.equal(agent.invocations.count("lookup"), 1);
    assert.equal(second.usage.modelCalls, 3);
  });

  test("an approved write runs once in its own Step and the CLI resumes it without a prompt", async () => {
    const agent = await startHarness();
    const sessionId = agent.newSessionId("real-approved");
    const command = 'call publish {"message":"hello"}';
    await agent.sessions.startSession(sessionId, `${mockScriptMarker}${command}`);
    const interrupt = await agent.waitForNewInterrupt(sessionId, "publish");
    assert.equal(agent.invocations.count("publish"), 0);
    assert.equal(await agent.sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    const snapshot = await agent.waitForTurns(sessionId, 1);

    assert.equal(snapshot.state?.lastTurnError, null);
    assert.equal(snapshot.messages.at(-1)?.text, "publish: published hello");
    assert.equal(agent.invocations.count("publish"), 1);
    // One model call per RunModel Step: the CLI resumed the deferred call by itself and sent its own prompt.
    const requests = api.scriptedRequests().filter((request) => request.command === command);
    assert.equal(requests.length, 2);
    assert.deepEqual(requests[0]?.textsAfterCommand, []);
    assert.equal(requests[1]?.textsAfterCommand.includes(resumePrompt), true);
  });

  test("an approved write defers and resumes the same way in dontAsk mode", async () => {
    const agent = await startHarness("dontAsk");
    const sessionId = agent.newSessionId("real-dont-ask");
    await agent.sessions.startSession(sessionId, `${mockScriptMarker}call publish {"message":"quiet"}`);
    const interrupt = await agent.waitForNewInterrupt(sessionId, "publish");
    assert.equal(await agent.sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    const snapshot = await agent.waitForTurns(sessionId, 1);

    assert.equal(snapshot.messages.at(-1)?.text, "publish: published quiet");
    assert.equal(agent.invocations.count("publish"), 1);
  });

  test("a rejected write never runs and the model sees the rejection message", async () => {
    const agent = await startHarness();
    const sessionId = agent.newSessionId("real-rejected");
    await agent.sessions.startSession(sessionId, `${mockScriptMarker}call publish {"message":"draft"}`);
    const interrupt = await agent.waitForNewInterrupt(sessionId, "publish");
    assert.equal(await agent.sessions.resolveInterrupt(sessionId, interrupt.interruptId, false, "not today"), true);
    const snapshot = await agent.waitForTurns(sessionId, 1);

    assert.equal(snapshot.messages.at(-1)?.text, "publish: not today");
    assert.equal(agent.invocations.count("publish"), 0);
  });

  test("a second write of one reply is refused, called again alone, and both run once", async () => {
    const agent = await startHarness();
    const sessionId = agent.newSessionId("real-two-writes");
    const command = 'call publish {"message":"b"} && call publish {"message":"c"}';
    await agent.sessions.startSession(sessionId, `${mockScriptMarker}${command}`);
    const firstInterrupt = await agent.waitForNewInterrupt(sessionId, "publish");
    assert.equal(firstInterrupt.arguments, '{"message":"b"}');
    assert.equal(await agent.sessions.resolveInterrupt(sessionId, firstInterrupt.interruptId, true), true);
    const secondInterrupt = await agent.waitForNewInterrupt(sessionId, "publish", [firstInterrupt.interruptId]);
    assert.equal(secondInterrupt.arguments, '{"message":"c"}');
    assert.equal(await agent.sessions.resolveInterrupt(sessionId, secondInterrupt.interruptId, true), true);
    const snapshot = await agent.waitForTurns(sessionId, 1);

    assert.equal(snapshot.messages.at(-1)?.text, "publish: published b; publish: published c");
    assert.equal(agent.invocations.count("publish"), 2);
    assert.equal(scriptedRequestsOf(command), 3);
  });

  test("two connector writes of one reply run one at a time in the model's order", async () => {
    const agent = await startHarness("dontAsk");
    const ledger = new FakeLedgerProvider(agent.stateDirectory);
    const sessionId = agent.newSessionId("real-connector-writes");
    const command =
      'call post_note {"accountId":"a-1","text":"first gate-after-accept:first-write"} && ' +
      'call create_payout {"accountId":"a-1","amount":5}';
    await agent.sessions.startSession(sessionId, `${mockScriptMarker}${command}`);
    const firstWrite = agent.gate("first-write");
    await firstWrite.waitUntilReached();
    const running = await agent.sessions.getAgentSnapshot(sessionId);
    // The CLI runs unannotated tools in order, so the second write never left the turn.
    assert.deepEqual(running.toolCalls.map((record) => record.toolName), ["post_note"]);
    assert.deepEqual(ledger.sends().map((entry) => entry.operation), ["postNote"]);
    firstWrite.release();
    const snapshot = await agent.waitForTurns(sessionId, 1);

    assert.equal(
      snapshot.messages.at(-1)?.text,
      'post_note: {"noteId":"postNote-1"}; create_payout: {"amount":5,"payoutId":"createPayout-2"}',
    );
    assert.deepEqual(ledger.writes().map((entry) => entry.operation), ["postNote", "createPayout"]);
    const payoutOutputs = snapshot.messages
      .filter((message) => message.role === "tool" && message.toolName === "create_payout")
      .map((message) => message.toolOutput ?? "");
    assert.match(
      payoutOutputs[0] ?? "",
      /^PreToolUse:mcp__tools__create_payout hook error: create_payout did not run\. Writes run one at a time in your order/,
    );
  });

  test("an application hook's updatedInput reaches the deferred Step", async () => {
    const agent = await startHarness();
    const sessionId = agent.newSessionId("real-rewritten");
    await agent.sessions.startSession(sessionId, `${mockScriptMarker}call publish {"message":"rewrite:hello"}`);
    const interrupt = await agent.waitForNewInterrupt(sessionId, "publish");
    assert.equal(interrupt.arguments, '{"message":"reviewed hello"}');
    assert.equal(await agent.sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    const snapshot = await agent.waitForTurns(sessionId, 1);

    assert.equal(snapshot.messages.at(-1)?.text, "publish: published reviewed hello");
    assert.equal(agent.invocations.count("publish"), 1);
  });

  test("Bash rewritten by an application hook resumes with the approved input", async () => {
    const agent = await startHarness();
    const sessionId = agent.newSessionId("real-bash-rewritten");
    await agent.sessions.startSession(sessionId, `${mockScriptMarker}call Bash {"command":"rewrite:hi"}`);
    const interrupt = await agent.waitForNewInterrupt(sessionId, "Bash");
    assert.equal(interrupt.arguments, '{"command":"echo reviewed hi"}');
    assert.equal(await agent.sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    const snapshot = await agent.waitForTurns(sessionId, 1);

    // The application hook is skipped on resume; the adapter's updatedInput carries the approved command.
    assert.equal(snapshot.messages.at(-1)?.text, "Bash: reviewed hi");
  });

  test("user and tool result images reach the model and replay byte for byte after Worker restarts", async () => {
    const { agent, workerEnvironment } = await startImageHarness(false);
    const media = createMediaStore(agent.stateDirectory);
    const userImageBase64 = conformanceImage("png").base64;
    const screenshotBase64 = conformanceImage("gif").base64;
    const userImage = await media.putImage(Buffer.from(userImageBase64, "base64"));
    let worker = await agent.startWorker(workerEnvironment);
    const sessionId = agent.newSessionId("real-images");
    const command = `call screenshot {"label":"${sessionId}"}`;
    await agent.sessions.startSession(sessionId);
    assert.equal(await agent.sessions.sendUserMessage(sessionId, `${mockScriptMarker}${command}`, false, [userImage]), true);
    const interrupt = await agent.waitForNewInterrupt(sessionId, "screenshot");
    // The suspended turn keeps its transcript, user image included, in the run state.
    const [runState] = await agent.sessions.client.getAttributes(sessionId, agent.flow.frameworkRunState);
    assert.ok(runState?.includes(userImage.uri));
    assert.equal(runState?.includes(userImageBase64), false);

    await worker.kill();
    worker = await agent.startWorker(workerEnvironment);
    assert.equal(await agent.sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    const first = await agent.waitForTurns(sessionId, 1);
    assert.equal(first.state?.lastTurnError, null);
    assert.equal(first.messages.at(-1)?.text, "screenshot: took a screenshot");
    await worker.kill();
    await agent.startWorker(workerEnvironment);
    assert.equal(await agent.sessions.sendUserMessage(sessionId, `${mockScriptMarker}recall`), true);
    const second = await agent.waitForTurns(sessionId, 2);
    assert.equal(second.messages.at(-1)?.text, `history: ${command}`);

    const [firstRequest, resumedRequest, recallRequest] = requestsOfSession(sessionId);
    assert.ok(firstRequest !== undefined && resumedRequest !== undefined && recallRequest !== undefined);
    assert.deepEqual(imageBlocksOf(contentOf(firstRequest.messages[0])), [
      { type: "image", source: { type: "base64", media_type: "image/png", data: userImageBase64 } },
    ]);
    const toolResult = contentOf(resumedRequest.messages.at(-1)).find((block) => block["type"] === "tool_result");
    assert.equal(toolResult?.["tool_use_id"], interrupt.toolCallId);
    assert.deepEqual(imageBlocksOf(toolResult?.["content"]), [
      { type: "image", source: { type: "base64", media_type: "image/gif", data: screenshotBase64 } },
    ]);
    // Each restarted Worker rebuilt the earlier conversation from references, byte for byte.
    const firstConversation = conversationOf(firstRequest);
    const resumedConversation = conversationOf(resumedRequest);
    assert.deepEqual(resumedConversation.slice(0, firstConversation.length), firstConversation);
    assert.deepEqual(conversationOf(recallRequest).slice(0, resumedConversation.length), resumedConversation);

    const screenshotImage = await media.putImage(Buffer.from(screenshotBase64, "base64"));
    assert.deepEqual(second.messages[0]?.images, [userImage]);
    const toolMessages = second.messages.filter((message) => message.role === "tool");
    assert.deepEqual(
      toolMessages.map((message) => [message.toolName, message.images]),
      [["screenshot", [screenshotImage]]],
    );
    const stored = JSON.stringify(second.messages.map((message) => message.frameworkItem));
    assert.equal(stored.includes(userImageBase64), false);
    assert.equal(stored.includes(screenshotBase64), false);
    assert.ok(stored.includes(screenshotImage.uri));
    assert.equal(agent.invocations.count("screenshot"), 1);
  });

  test("a deferred custom tool's MCP image is stored as a reference and reaches the model", async () => {
    const { agent } = await startImageHarness(true);
    const drawingBase64 = conformanceImage("jpeg").base64;
    const sessionId = agent.newSessionId("real-drawing");
    await agent.sessions.startSession(sessionId, `${mockScriptMarker}call draw {"subject":"${sessionId}"}`);
    const snapshot = await agent.waitForTurns(sessionId, 1);

    assert.equal(snapshot.state?.lastTurnError, null);
    assert.equal(snapshot.messages.at(-1)?.text, `draw: drew ${sessionId}`);
    const toolMessages = snapshot.messages.filter((message) => message.role === "tool");
    assert.deepEqual(
      toolMessages.map((message) => (message.images ?? []).map((image) => image.sha256)),
      [[conformanceImage("jpeg").sha256]],
    );
    const resumedRequest = requestsOfSession(sessionId).at(-1);
    const toolResult = (resumedRequest?.messages ?? [])
      .flatMap((message) => contentOf(message))
      .find((block) => block["type"] === "tool_result");
    assert.deepEqual(imageBlocksOf(toolResult?.["content"]), [
      { type: "image", source: { type: "base64", media_type: "image/jpeg", data: drawingBase64 } },
    ]);
    const stored = JSON.stringify(snapshot.messages.map((message) => message.frameworkItem));
    assert.equal(stored.includes(drawingBase64), false);
    assert.equal(agent.invocations.count("draw"), 1);
  });

  function requestsOfSession(sessionId: string): MockApiRequest[] {
    return api.scriptedRequests().filter((request) => JSON.stringify(request.messages).includes(sessionId));
  }

  test("Bash waits for approval and the resumed CLI runs it", async () => {
    const agent = await startHarness();
    const sessionId = agent.newSessionId("real-bash");
    await agent.sessions.startSession(sessionId, `${mockScriptMarker}call Bash {"command":"echo hi"}`);
    const interrupt = await agent.waitForNewInterrupt(sessionId, "Bash");
    assert.equal(await agent.sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    const snapshot = await agent.waitForTurns(sessionId, 1);

    assert.equal(snapshot.messages.at(-1)?.text, "Bash: hi");
  });
});

type JsonBlock = Readonly<Record<string, unknown>>;

function contentOf(message: unknown): JsonBlock[] {
  const content = (message as JsonBlock | undefined)?.["content"];
  return Array.isArray(content) ? (content as JsonBlock[]) : [];
}

function imageBlocksOf(content: unknown): JsonBlock[] {
  return Array.isArray(content) ? (content as JsonBlock[]).filter((block) => block["type"] === "image") : [];
}

// The CLI's system turns and cache breakpoints move between requests; neither carries images.
function conversationOf(request: MockApiRequest): unknown[] {
  return request.messages.filter((message) => message["role"] !== "system").map(withoutCacheControl);
}

function withoutCacheControl(value: unknown): unknown {
  if (Array.isArray(value)) {
    return value.map(withoutCacheControl);
  }
  if (typeof value === "object" && value !== null) {
    return Object.fromEntries(
      Object.entries(value)
        .filter(([key]) => key !== "cache_control")
        .map(([key, item]) => [key, withoutCacheControl(item)]),
    );
  }
  return value;
}

function findBundledClaudeCli(): string | undefined {
  const require = createRequire(import.meta.url);
  const binaryName = process.platform === "win32" ? "claude.exe" : "claude";
  for (const suffix of ["", "-musl"]) {
    try {
      const packageJson = require.resolve(
        `@anthropic-ai/claude-agent-sdk-${process.platform}-${process.arch}${suffix}/package.json`,
      );
      const binaryPath = join(dirname(packageJson), binaryName);
      if (existsSync(binaryPath)) {
        return binaryPath;
      }
    } catch {
      // This platform package is not installed; try the next variant.
    }
  }
  return undefined;
}
