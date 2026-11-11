// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { after, afterEach, before, describe, test } from "node:test";

import { PrivateDexEngine } from "@superdurable/dex-agents/testing";

import { ClaudeAgentTestHarness } from "../fixtures/claude-agent-test-harness.js";
import { conformanceImage, createMediaStore } from "../fixtures/claude-test-agent.js";

// The real-CLI suite covers images that reach the model; this one covers a runner without a store.
describe("Claude agent images without a media store", () => {
  let engine: PrivateDexEngine;
  let harness: ClaudeAgentTestHarness | undefined;

  before(async () => {
    engine = await PrivateDexEngine.start();
  });

  afterEach(async () => {
    await harness?.close();
    harness = undefined;
  });

  after(async () => {
    await engine?.stop();
  });

  test("a runner without a media store refuses a user message with images", async () => {
    harness = await ClaudeAgentTestHarness.start(engine, true);
    const agent = harness;
    const image = await createMediaStore(agent.stateDirectory).putImage(
      Buffer.from(conformanceImage("png").base64, "base64"),
    );
    const sessionId = agent.newSessionId("no-media-store");
    await agent.sessions.startSession(sessionId);

    assert.equal(await agent.sessions.sendUserMessage(sessionId, "say hi", false, [image]), false);
    assert.equal(await agent.sessions.sendUserMessage(sessionId, "say hi"), true);
    const snapshot = await agent.waitForTurns(sessionId, 1);
    assert.equal(snapshot.messages.at(-1)?.text, "said: hi");
    assert.deepEqual(
      snapshot.messages.map((message) => message.images ?? []),
      [[], []],
    );
  });
});
