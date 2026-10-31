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
import { mockScriptMarker, MockAnthropicApi, RefusingProxy } from "../fixtures/mock-anthropic-api.js";

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

  async function startHarness(permissionMode?: PermissionMode): Promise<ClaudeAgentTestHarness> {
    harness = await ClaudeAgentTestHarness.start(engine, true, (stateDirectory) => {
      mkdirSync(join(stateDirectory, "home"), { recursive: true });
      return {
        flowType: permissionMode === undefined ? "RealClaudeCliAgent" : `RealClaudeCliAgent-${permissionMode}`,
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
    });
    return harness;
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
