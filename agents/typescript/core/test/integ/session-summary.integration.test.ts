// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { randomUUID } from "node:crypto";
import { mkdtempSync } from "node:fs";
import { join } from "node:path";
import { after, before, describe, test } from "node:test";
import { promisify } from "node:util";

import type { Client } from "@superdurable/dex";

import {
  SessionTitleSearchUnavailableError,
  listAgentSessions,
  sortAgentSessionsByLastActivity,
  withSummaryAttributesIndexed,
  type AgentSessionSummary,
} from "../../src/index.js";
import {
  AgentSessionTestClient,
  AgentTestEnvironment,
  PrivateDexEngine,
  pollUntil,
} from "../../src/testing/index.js";
import { createScriptedAgentFlow } from "../fixtures/scripted-agent-runner.js";

const execFileAsync = promisify(execFile);

describe("session summaries", () => {
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  let sessions: AgentSessionTestClient;
  let flow: ReturnType<typeof createScriptedAgentFlow>;
  let flowType: string;

  before(async () => {
    engine = await PrivateDexEngine.start();
    flow = withSummaryAttributesIndexed(createScriptedAgentFlow(mkdtempSync(join(engine.stateDirectory, "markers-"))));
    flowType = flow.getFlowType();
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

  test("the summary follows each turn", async () => {
    const sessionId = newSessionId();
    await sessions.startSession(sessionId, "Fix the\n\tflaky   retry test");
    await sessions.waitForCompletedTurns(sessionId, 1);
    const first = await waitForSummary(sessionId, (session) => session.turnCount === 1);

    assert.equal(first.title, "Fix the flaky retry test");
    assert.equal(first.status, "running");
    assert.ok(first.lastActivityAt !== null);
    assert.ok(first.lastActivityAt.getTime() >= Math.floor(first.startedAt.getTime() / 1_000) * 1_000);
    const [title, lastActivityAt, turnCount] = await environment.client.getAttributes(
      sessionId,
      flow.agentSessionTitle,
      flow.agentSessionLastActivityAt,
      flow.agentSessionTurnCount,
    );
    assert.deepEqual([title, lastActivityAt, turnCount], [first.title, first.lastActivityAt.toISOString(), 1]);

    await waitPast(first.lastActivityAt);
    assert.equal(await sessions.sendUserMessage(sessionId, "a different second message"), true);
    await sessions.waitForCompletedTurns(sessionId, 2);
    const second = await waitForSummary(sessionId, (session) => session.turnCount === 2);
    assert.equal(second.title, first.title);
    assert.ok(second.lastActivityAt !== null && second.lastActivityAt > first.lastActivityAt);

    // A session without a first message has a time and zero turns, but no title.
    const idleId = newSessionId();
    await sessions.startSession(idleId);
    const idle = await waitForSummary(idleId, (session) => session.turnCount === 0);
    assert.equal(idle.title, null);
    assert.ok(idle.lastActivityAt !== null);
  });

  test("sessions page, sort by last activity, and search by title", async () => {
    const refundOrder = newSessionId();
    const flakyTest = newSessionId();
    const refundInvoice = newSessionId();
    for (const [sessionId, message] of [
      [refundOrder, "Refund order 7"],
      [flakyTest, "Fix the flaky test"],
      [refundInvoice, 'refund the "duplicate" invoice'],
    ] as const) {
      await sessions.startSession(sessionId, message);
    }
    const created = [refundOrder, flakyTest, refundInvoice];
    for (const sessionId of created) {
      await sessions.waitForCompletedTurns(sessionId, 1);
      await waitForSummary(sessionId, (session) => session.turnCount === 1);
    }

    const everySession = (await listEveryPage(environment.client, flowType, 2)).flat();
    const listed = everySession.filter((session) => created.includes(session.sessionId));
    assert.deepEqual(listed.map((session) => session.sessionId).sort(), [...created].sort());

    // The oldest of the three becomes the most recently active session.
    const latest = Math.max(...everySession.map((session) => session.lastActivityAt?.getTime() ?? 0));
    await waitPast(new Date(latest));
    assert.equal(await sessions.sendUserMessage(refundOrder, "once more"), true);
    await sessions.waitForCompletedTurns(refundOrder, 2);
    await waitForSummary(refundOrder, (session) => session.turnCount === 2);
    const page = await listAgentSessions(environment.client, flowType, { pageSize: 100 });
    const ordered = sortAgentSessionsByLastActivity(page.sessions);
    assert.equal(ordered[0]?.sessionId, refundOrder);
    const times = ordered.map((session) => (session.lastActivityAt ?? session.startedAt).getTime());
    assert.deepEqual(times, [...times].sort((left, right) => right - left));

    assert.deepEqual(await search("refund"), [refundOrder, refundInvoice].sort());
    assert.deepEqual(await search("REFUND order"), [refundOrder]);
    assert.deepEqual(await search('duplicate" \\'), [refundInvoice]);
    assert.deepEqual(await search("flak"), []);
    const pages = await listEveryPage(environment.client, flowType, 1, "refund");
    assert.deepEqual(
      pages.map((sessionsOfPage) => sessionsOfPage.length),
      [1, 1],
    );
  });

  test("a session without the summary lists with nulls and gains no title", async () => {
    const sessionId = newSessionId();
    await sessions.startSession(sessionId, "refund the old order");
    await sessions.waitForCompletedTurns(sessionId, 1);
    await waitForSummary(sessionId, (session) => session.turnCount === 1);
    // An earlier release writes none of the three Attributes.
    await deleteSummaryAttributes(sessionId);

    const old = await waitForSummary(sessionId, (session) => session.title === null);
    assert.deepEqual([old.title, old.lastActivityAt, old.turnCount], [null, null, null]);
    assert.deepEqual(await search("old order"), []);

    assert.equal(await sessions.sendUserMessage(sessionId, "refund it again"), true);
    await sessions.waitForCompletedTurns(sessionId, 2);
    const upgraded = await waitForSummary(sessionId, (session) => session.turnCount === 2);
    assert.equal(upgraded.title, null);
    assert.ok(upgraded.lastActivityAt !== null);
    assert.deepEqual(await search("old order"), []);
  });

  async function waitForSummary(
    sessionId: string,
    isExpected: (session: AgentSessionSummary) => boolean,
  ): Promise<AgentSessionSummary> {
    return pollUntil(
      async () => {
        const page = await listAgentSessions(environment.client, flowType, { pageSize: 100 });
        return page.sessions.find((session) => session.sessionId === sessionId && isExpected(session));
      },
      { description: `${sessionId} in the session list` },
    );
  }

  async function search(titleSearch: string): Promise<string[]> {
    const page = await listAgentSessions(environment.client, flowType, { titleSearch, pageSize: 100 });
    return page.sessions.map((session) => session.sessionId).sort();
  }

  async function deleteSummaryAttributes(sessionId: string): Promise<void> {
    const deletions = [
      ["AgentSessionTitle", "INDEX_TYPE_TEXT", "text1"],
      ["AgentSessionLastActivityAt", "INDEX_TYPE_DATETIME", "datetime1"],
      ["AgentSessionTurnCount", "INDEX_TYPE_INT", "int1"],
    ].map(([key, type, indexKey]) => ({
      key,
      value: { nullValue: "NULL_VALUE" },
      indexConfig: { enable: true, type, indexKey },
    }));
    await execFileAsync(process.env.DEXCLI ?? "dexcli", [
      "api",
      "call",
      "SetAttributes",
      "--server",
      engine.serverAddress,
      "--yes",
      "--data",
      JSON.stringify({ flowId: sessionId, attributes: deletions, requestId: randomUUID() }),
    ]);
  }
});

describe("session summaries without summaryAttributesIndexed", () => {
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  let sessions: AgentSessionTestClient;
  let flow: ReturnType<typeof createScriptedAgentFlow>;

  before(async () => {
    engine = await PrivateDexEngine.start();
    flow = createScriptedAgentFlow(mkdtempSync(join(engine.stateDirectory, "markers-")));
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

  test("sessions list without a summary, and a title search says it is unavailable", async () => {
    const schema = flow.getPersistenceSchema?.() ?? {};
    assert.equal(
      (schema.attributes ?? []).some((definition) => definition.name.startsWith("AgentSession")),
      false,
    );
    const sessionId = newSessionId();
    await sessions.startSession(sessionId, "refund order 7");
    await sessions.waitForCompletedTurns(sessionId, 1);

    const page = await listAgentSessions(environment.client, flow.getFlowType());
    assert.deepEqual(
      page.sessions.map((session) => [session.sessionId, session.title, session.lastActivityAt, session.turnCount]),
      [[sessionId, null, null, null]],
    );
    // No Worker registered the title index on this server.
    await assert.rejects(
      listAgentSessions(environment.client, flow.getFlowType(), { titleSearch: "refund" }),
      SessionTitleSearchUnavailableError,
    );
  });
});

async function listEveryPage(
  client: Client,
  flowType: string,
  pageSize: number,
  titleSearch?: string,
): Promise<AgentSessionSummary[][]> {
  const pages: AgentSessionSummary[][] = [];
  let nextPageToken = "";
  do {
    const page = await listAgentSessions(client, flowType, {
      pageSize,
      nextPageToken,
      ...(titleSearch === undefined ? {} : { titleSearch }),
    });
    pages.push([...page.sessions]);
    nextPageToken = page.nextPageToken;
  } while (nextPageToken !== "");
  return pages;
}

// Activity times have one-second precision, so a later turn needs a later second.
async function waitPast(moment: Date): Promise<void> {
  await pollUntil(() => (Date.now() >= moment.getTime() + 1_000 ? true : undefined), {
    description: `the clock to pass ${moment.toISOString()}`,
  });
}

function newSessionId(): string {
  return `summary-${randomUUID()}`;
}
