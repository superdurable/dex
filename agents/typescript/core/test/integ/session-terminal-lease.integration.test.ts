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
import { setTimeout as delay } from "node:timers/promises";
import { after, before, describe, test } from "node:test";

import { startAgentSession, type AcquireSessionTerminalLeaseRequest } from "../../src/index.js";
import { AgentSessionTestClient, AgentTestEnvironment, PrivateDexEngine } from "../../src/testing/index.js";
import { createScriptedAgentFlow } from "../fixtures/scripted-agent-runner.js";

describe("session terminal lease", () => {
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

  async function startedSession(): Promise<string> {
    const sessionId = `lease-${randomUUID()}`;
    await startAgentSession(environment.client, flow, sessionId);
    await sessions.waitForCompletedTurns(sessionId, 0);
    return sessionId;
  }

  function request(holderId: string, ttlSeconds = 30): AcquireSessionTerminalLeaseRequest {
    return { holderId, hostName: "host-a", processId: 4242, ttlSeconds };
  }

  test("the first holder gets the lease, a second waits, and a renewal keeps opened_at", async () => {
    const sessionId = await startedSession();
    const first = await environment.client.invokeRPC(flow.acquireSessionTerminalLease, sessionId, request("holder-a"));
    assert.equal(first.isAcquired, true);
    assert.equal(first.lease?.holderId, "holder-a");
    assert.equal(first.lease?.hostName, "host-a");
    assert.equal(first.lease?.processId, 4242);
    assert.ok(Date.parse(first.lease?.expiresAt ?? "") > Date.now());

    const refused = await environment.client.invokeRPC(flow.acquireSessionTerminalLease, sessionId, {
      ...request("holder-b"),
      hostName: "host-b",
    });
    assert.deepEqual(refused, { isAcquired: false, lease: first.lease });

    await delay(20);
    const renewed = await environment.client.invokeRPC(flow.acquireSessionTerminalLease, sessionId, request("holder-a"));
    assert.equal(renewed.isAcquired, true);
    assert.equal(renewed.lease?.openedAt, first.lease?.openedAt);
    assert.ok(Date.parse(renewed.lease?.expiresAt ?? "") > Date.parse(first.lease?.expiresAt ?? ""));
    const [stored] = await environment.client.getAttributes(sessionId, flow.sessionTerminalLease);
    assert.deepEqual(stored, renewed.lease);
  });

  test("a release frees the lease only for its holder", async () => {
    const sessionId = await startedSession();
    await environment.client.invokeRPC(flow.acquireSessionTerminalLease, sessionId, request("holder-a"));

    assert.equal(
      await environment.client.invokeRPC(flow.releaseSessionTerminalLease, sessionId, { holderId: "holder-b" }),
      false,
    );
    assert.equal(
      await environment.client.invokeRPC(flow.releaseSessionTerminalLease, sessionId, { holderId: "holder-a" }),
      true,
    );
    const [stored] = await environment.client.getAttributes(sessionId, flow.sessionTerminalLease);
    assert.equal(stored, undefined);

    const next = await environment.client.invokeRPC(flow.acquireSessionTerminalLease, sessionId, request("holder-b"));
    assert.equal(next.isAcquired, true);
    assert.equal(next.lease?.holderId, "holder-b");
    assert.equal(
      await environment.client.invokeRPC(flow.releaseSessionTerminalLease, sessionId, { holderId: "holder-a" }),
      false,
    );
  });

  test("an expired lease goes to the next holder with a new opened_at", async () => {
    const sessionId = await startedSession();
    const first = await environment.client.invokeRPC(flow.acquireSessionTerminalLease, sessionId, request("holder-a", 1));
    await delay(1_100);

    const next = await environment.client.invokeRPC(flow.acquireSessionTerminalLease, sessionId, request("holder-b"));
    assert.equal(next.isAcquired, true);
    assert.equal(next.lease?.holderId, "holder-b");
    assert.notEqual(next.lease?.openedAt, first.lease?.openedAt);
  });

  test("a new holder's environment stays as the last terminal environment after its release", async () => {
    const sessionId = await startedSession();
    const environmentA = { host_name: "host-a", workspace: "/work/a" };
    const environmentB = { host_name: "host-b", workspace: "/work/b" };
    const readLastEnvironment = async (): Promise<Readonly<Record<string, string>> | undefined> =>
      (await environment.client.getAttributes(sessionId, flow.sessionLastTerminalEnvironment))[0];

    await environment.client.invokeRPC(flow.acquireSessionTerminalLease, sessionId, request("holder-a"));
    assert.equal(await readLastEnvironment(), undefined);
    await environment.client.invokeRPC(flow.releaseSessionTerminalLease, sessionId, { holderId: "holder-a" });

    await environment.client.invokeRPC(flow.acquireSessionTerminalLease, sessionId, {
      ...request("holder-a"),
      environment: environmentA,
    });
    assert.deepEqual(await readLastEnvironment(), environmentA);
    await environment.client.invokeRPC(flow.acquireSessionTerminalLease, sessionId, {
      ...request("holder-a"),
      environment: environmentB,
    });
    assert.deepEqual(await readLastEnvironment(), environmentA, "a renewal keeps the stored environment");
    const refused = await environment.client.invokeRPC(flow.acquireSessionTerminalLease, sessionId, {
      ...request("holder-b"),
      environment: environmentB,
    });
    assert.equal(refused.isAcquired, false);
    assert.deepEqual(await readLastEnvironment(), environmentA, "a refused call keeps the stored environment");

    await environment.client.invokeRPC(flow.releaseSessionTerminalLease, sessionId, { holderId: "holder-a" });
    assert.deepEqual(await readLastEnvironment(), environmentA, "a release keeps the stored environment");
    await environment.client.invokeRPC(flow.acquireSessionTerminalLease, sessionId, {
      ...request("holder-b"),
      environment: environmentB,
    });
    assert.deepEqual(await readLastEnvironment(), environmentB);
  });

  test("an invalid request returns no lease and writes nothing", async () => {
    const sessionId = await startedSession();
    for (const invalid of [
      request(" "),
      request("holder-a", 0),
      request("holder-a", 3_601),
      { ...request("holder-a"), processId: -1 },
      { ...request("holder-a"), hostName: "h".repeat(256) },
    ]) {
      assert.deepEqual(
        await environment.client.invokeRPC(flow.acquireSessionTerminalLease, sessionId, invalid),
        { isAcquired: false, lease: null },
      );
    }
    const [stored] = await environment.client.getAttributes(sessionId, flow.sessionTerminalLease);
    assert.equal(stored, undefined);
  });
});
