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
import { after, before, describe, test } from "node:test";

import { ActiveStepSearchMode } from "@superdurable/dex";

import { agentSessionActiveStepSearchMode, startAgentSession } from "../../src/index.js";
import {
  AgentSessionTestClient,
  AgentTestEnvironment,
  FileGate,
  PrivateDexEngine,
  pollUntil,
} from "../../src/testing/index.js";
import { createScriptedAgentFlow } from "../fixtures/scripted-agent-runner.js";

const approvalQuery = 'ActiveStepTypes = "AwaitToolApproval"';
const questionQuery = 'ActiveStepTypes = "AwaitQuestionAnswer"';
const idleQuery = 'ActiveStepTypes = "AwaitUserMessage"';

describe("active Step search on session Flows", () => {
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  let sessions: AgentSessionTestClient;
  let flow: ReturnType<typeof createScriptedAgentFlow>;
  let markerDirectory: string;

  before(async () => {
    engine = await PrivateDexEngine.start();
    markerDirectory = mkdtempSync(join(engine.stateDirectory, "markers-"));
    flow = createScriptedAgentFlow(markerDirectory);
    environment = await AgentTestEnvironment.start({
      serverAddress: engine.serverAddress,
      flows: [flow],
      isWorkerInProcess: true,
    });
    sessions = new AgentSessionTestClient(environment.client, flow);
  });

  after(async () => {
    await environment?.close();
    await engine?.stop();
  });

  test("a session is found while it waits for approval and leaves when it moves on", async () => {
    const sessionId = newSessionId();
    await startAgentSession(environment.client, flow, sessionId, { initialMessage: "call send_email" });
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "send_email");

    await waitForSearchMatch(approvalQuery, sessionId, true);
    assert.equal(await isFound(questionQuery, sessionId), false);

    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    await sessions.waitForCompletedTurns(sessionId, 1);
    await waitForSearchMatch(approvalQuery, sessionId, false);
    await waitForSearchMatch(idleQuery, sessionId, true);
  });

  test("a session is found while it waits for a question and leaves when answered", async () => {
    new FileGate(markerDirectory, "confirm_shipment").release();
    const sessionId = newSessionId();
    await sessions.startSession(sessionId, "call confirm_shipment");
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "confirm_shipment");

    await waitForSearchMatch(questionQuery, sessionId, true);
    assert.equal(await isFound(approvalQuery, sessionId), false);

    assert.equal(await sessions.answerQuestion(sessionId, interrupt.interruptId, "yes"), true);
    await sessions.waitForCompletedTurns(sessionId, 1);
    await waitForSearchMatch(questionQuery, sessionId, false);
  });

  test("an application chooses the mode, and other config keeps the default mode", async () => {
    const allStepsId = newSessionId();
    await startAgentSession(environment.client, flow, allStepsId, { initialMessage: "call send_email" }, {
      configOverride: { activeStepSearchMode: ActiveStepSearchMode.ALL },
    });
    const keptId = newSessionId();
    await startAgentSession(environment.client, flow, keptId, { initialMessage: "call send_email" }, {
      configOverride: { stepDurability: "sync" },
    });
    await sessions.waitForPendingInterrupt(allStepsId, "send_email");
    await sessions.waitForPendingInterrupt(keptId, "send_email");

    await waitForSearchMatch(approvalQuery, allStepsId, true);
    await waitForSearchMatch(approvalQuery, keptId, true);
  });

  test("a plain startFlow stays out of the search", async () => {
    const sessionId = newSessionId();
    await environment.client.startFlow(flow, sessionId, { initialMessage: "call send_email", sessionSettings: null });
    await sessions.waitForPendingInterrupt(sessionId, "send_email");

    assert.equal(await isFound(approvalQuery, sessionId), false);
  });

  test("updateFlowConfig turns the search on for a session already waiting", async () => {
    const sessionId = newSessionId();
    // As a session started on 0.2.0: a plain start keeps the server's disabled default.
    await environment.client.startFlow(flow, sessionId, { initialMessage: "call send_email", sessionSettings: null });
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "send_email");
    assert.equal(await isFound(approvalQuery, sessionId), false);

    await environment.client.updateFlowConfig(sessionId, { activeStepSearchMode: agentSessionActiveStepSearchMode });

    await waitForSearchMatch(approvalQuery, sessionId, true);
    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    await sessions.waitForCompletedTurns(sessionId, 1);
    await waitForSearchMatch(approvalQuery, sessionId, false);
  });

  async function waitForSearchMatch(query: string, sessionId: string, isExpected: boolean): Promise<void> {
    await pollUntil(
      async () => ((await isFound(query, sessionId)) === isExpected ? true : undefined),
      { description: `${query} to ${isExpected ? "find" : "leave"} ${sessionId}` },
    );
  }

  async function isFound(query: string, sessionId: string): Promise<boolean> {
    const page = await environment.client.searchFlows(query, 100);
    return page.flows.some((entry) => entry.flowId === sessionId);
  }
});

function newSessionId(): string {
  return `search-${randomUUID()}`;
}
