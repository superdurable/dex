/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

import assert from "node:assert/strict";
import type { Server } from "node:http";
import type { AddressInfo } from "node:net";
import test from "node:test";

import express from "express";

import type { Connection } from "@superdurable/connector-slack";
import { GeneratedMock, postThreadReplyReplySent } from "@superdurable/connector-slack/mock";
import type { AgentSnapshot } from "@superdurable/dex-agents";
import { AgentSessionTestClient, AgentTestEnvironment, pollUntil } from "@superdurable/dex-agents/testing";

import { OrderDesk } from "../../../src/products/agent-harnesses/order-support/order-desk.js";
import {
  createOrderSupportRouter,
  orderSupportSessionTokenHeaderName,
  type OrderSupportSessionFlow,
} from "../../../src/products/agent-harnesses/order-support/order-support-routes.js";
import { slackConnectionName } from "../../../src/shared/slack-connector-tools.js";
import { scriptedCancelReason, scriptedReplyPrefix, slackPostThreadReplyToolName } from "./order-support-script.js";

const serverAddress = process.env.DEX_FLOW_SERVICE_ADDRESS ?? "127.0.0.1:8801";
const waitOptions = { timeoutMs: 90_000 };

/** Counts every `cancelOrder` call, so a test sees a second run even when it changes nothing. */
class CancelCountingOrderDesk extends OrderDesk {
  public cancelCallCount = 0;

  public override cancelOrder(orderId: string, reason: string): string {
    this.cancelCallCount += 1;
    return super.cancelOrder(orderId, reason);
  }
}

/** How a harness test builds its Flow from the order desk and the Slack connection its tools use. */
export interface OrderSupportScenarioSetup {
  readonly harnessName: string;
  createFlow(orderDesk: OrderDesk, slack: Connection): OrderSupportSessionFlow | Promise<OrderSupportSessionFlow>;
  close?(): Promise<void>;
}

interface RouteSession {
  readonly sessionId: string;
  readonly sessionToken: string;
}

/**
 * Registers the scenarios every harness passes through its HTTP routes: a read tool turn, and a
 * cancellation that waits for approval and is rejected and never runs, or is approved and runs once, and a Slack
 * thread reply through the connector mock that waits for approval and posts once.
 */
export function registerOrderSupportScenarios(setup: OrderSupportScenarioSetup): void {
  let orderDesk: CancelCountingOrderDesk;
  let slackMock: GeneratedMock;
  let environment: AgentTestEnvironment;
  let sessions: AgentSessionTestClient;
  let server: Server;
  let baseUrl: string;

  test.before(async () => {
    orderDesk = new CancelCountingOrderDesk();
    slackMock = new GeneratedMock(slackConnectionName);
    // Strict with one scripted answer: any other post fails the mock.
    slackMock.postThreadReply().strict().respond(postThreadReplyReplySent());
    const flow = await setup.createFlow(orderDesk, slackMock.connection());
    environment = await AgentTestEnvironment.start({ serverAddress, flows: [flow], isWorkerInProcess: true });
    sessions = new AgentSessionTestClient(environment.client, flow);
    const app = express().use(express.json()).use("/agent", createOrderSupportRouter(environment.client, flow, "test-key"));
    server = await new Promise<Server>((resolve) => {
      const listening = app.listen(0, "127.0.0.1", () => resolve(listening));
    });
    baseUrl = `http://127.0.0.1:${(server.address() as AddressInfo).port}/agent`;
  });

  test.after(async () => {
    server.closeAllConnections();
    await new Promise((resolve) => server.close(resolve));
    await environment.close();
    await setup.close?.();
    slackMock.assertNoFailures();
  });

  test(`${setup.harnessName}: a turn calls the read tool and answers from its result`, async () => {
    const session = await startSession();
    assert.equal((await post(session, "messages", { text: "Look up order 1001" })).status, 200);
    await sessions.waitForCompletedTurns(session.sessionId, 1, waitOptions);

    const snapshot = await sessions.getAgentSnapshot(session.sessionId);
    assert.equal(snapshot.state?.lastTurnError ?? null, null);
    assert.equal(lastAssistantText(snapshot), `${scriptedReplyPrefix}Order 1001: 2 notebooks, shipped.`);
    const json = await getSnapshotJson(session);
    assert.ok(Array.isArray(json.messages));
  });

  // Runs before the approved cancellation, while order 1002 is still processing.
  test(`${setup.harnessName}: a rejected cancellation never runs`, async () => {
    // An approval here would cancel the order, so the rejection is what keeps the count at 0.
    assert.equal(orderDesk.describeOrder("1002"), "Order 1002: 1 desk lamp, processing.");
    const session = await startSession("Please cancel order 1002");
    const interrupt = await waitForApproval(session);
    const rejection = await post(session, `interrupts/${interrupt.interrupt_id}/reject`, { reason: "keep the order" });
    assert.equal(rejection.status, 200);
    await sessions.waitForCompletedTurns(session.sessionId, 1, waitOptions);
    assert.equal(orderDesk.cancelCallCount, 0);
    assert.equal(orderDesk.describeOrder("1002"), "Order 1002: 1 desk lamp, processing.");
    // The model reads the rejection reason as the call's result.
    const reply = lastAssistantText(await sessions.getAgentSnapshot(session.sessionId));
    assert.ok(reply.startsWith(scriptedReplyPrefix) && reply.includes("keep the order"), reply);
  });

  test(`${setup.harnessName}: a cancellation waits for approval and runs once after it`, async () => {
    const session = await startSession("Please cancel order 1002");
    const interrupt = await waitForApproval(session);
    assert.equal(orderDesk.cancelCallCount, 0);

    const approval = await post(session, `interrupts/${interrupt.interrupt_id}/approve`, {});
    assert.equal(approval.status, 200);
    await sessions.waitForCompletedTurns(session.sessionId, 1, waitOptions);

    const snapshot = await sessions.getAgentSnapshot(session.sessionId);
    assert.equal(lastAssistantText(snapshot), `${scriptedReplyPrefix}Order 1002 is cancelled: ${scriptedCancelReason}.`);
    assert.equal(orderDesk.cancelCallCount, 1);
    assert.deepEqual(orderDesk.cancelledOrderIds, ["1002"]);
    // The interrupt is resolved, so a second approval is refused and the tool does not run again.
    assert.equal((await post(session, `interrupts/${interrupt.interrupt_id}/approve`, {})).status, 409);
    assert.equal(orderDesk.cancelCallCount, 1);
  });

  test(`${setup.harnessName}: a Slack thread reply waits for approval and posts once`, async () => {
    const replyInput = { channelId: "C0123456789", threadTimestamp: "1710000000.000100", text: "Order 1001 has shipped." };
    const session = await startSession(
      `Reply in Slack thread ${replyInput.channelId} ${replyInput.threadTimestamp}: ${replyInput.text}`,
    );
    const postThreadReply = slackMock.postThreadReply();
    const interrupt = await waitForApproval(session, slackPostThreadReplyToolName);
    assert.deepEqual(postThreadReply.calls(), []);
    // The active turn's record: a non-idempotent write runs once, in its own single-attempt tool Step.
    const pendingCall = (await sessions.getAgentSnapshot(session.sessionId)).toolCalls.find(
      (record) => record.toolName === slackPostThreadReplyToolName,
    );
    assert.equal(pendingCall?.effect, "non_idempotent_write");
    assert.equal(pendingCall?.requiresApproval, true);

    assert.equal((await post(session, `interrupts/${interrupt.interrupt_id}/approve`, {})).status, 200);
    await sessions.waitForCompletedTurns(session.sessionId, 1, waitOptions);

    const calls = postThreadReply.calls();
    assert.equal(calls.length, 1);
    const [providerCall] = calls;
    assert.equal(providerCall?.flowId, session.sessionId);
    assert.deepEqual(providerCall?.input, replyInput);
    assert.equal(providerCall?.branch, "sent");
    assert.equal(providerCall?.retry, undefined);
    assert.equal(providerCall?.replay, undefined);
    // Slack deduplicates on a key derived from the Call ID, which the tool Step keeps across attempts.
    assert.equal(providerCall?.idempotencyKey, postThreadReply.idempotencyKey(providerCall?.callId ?? ""));

    // The model read the sent message, so the call succeeded.
    const reply = lastAssistantText(await sessions.getAgentSnapshot(session.sessionId));
    assert.ok(reply.startsWith(scriptedReplyPrefix) && reply.includes('"timestamp":"1710000011.000600"'), reply);

    assert.equal((await post(session, `interrupts/${interrupt.interrupt_id}/approve`, {})).status, 409);
    assert.equal(postThreadReply.calls().length, 1);
  });

  test(`${setup.harnessName}: session routes refuse a missing or wrong token and an empty message`, async () => {
    const session = await startSession();
    const missingToken = await fetch(`${baseUrl}/sessions/${session.sessionId}/snapshot`);
    assert.equal(missingToken.status, 403);
    assert.equal((await request({ ...session, sessionToken: "0".repeat(64) }, "snapshot")).status, 403);
    // The token check comes first, so a stranger learns nothing from the body validation.
    assert.equal((await post({ ...session, sessionToken: "0".repeat(64) }, "messages", { text: "" })).status, 403);
    assert.equal((await post(session, "messages", { text: "" })).status, 400);
  });

  async function startSession(message?: string): Promise<RouteSession> {
    const body = message === undefined ? {} : { message };
    const response = await fetch(`${baseUrl}/sessions`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(body),
    });
    assert.equal(response.status, 200, await response.clone().text());
    const started = (await response.json()) as { session_id: string; session_token: string };
    return { sessionId: started.session_id, sessionToken: started.session_token };
  }

  function request(session: RouteSession, path: string, init: RequestInit = {}): Promise<Response> {
    const headers = { "content-type": "application/json", [orderSupportSessionTokenHeaderName]: session.sessionToken };
    return fetch(`${baseUrl}/sessions/${session.sessionId}/${path}`, { ...init, headers });
  }

  function post(session: RouteSession, path: string, body: unknown): Promise<Response> {
    return request(session, path, { method: "POST", body: JSON.stringify(body) });
  }

  async function getSnapshotJson(session: RouteSession): Promise<Record<string, unknown>> {
    const response = await request(session, "snapshot");
    assert.equal(response.status, 200, await response.clone().text());
    return (await response.json()) as Record<string, unknown>;
  }

  function waitForApproval(
    session: RouteSession,
    toolName = "cancel_order",
  ): Promise<{ interrupt_id: string; tool_name: string }> {
    return pollUntil(async () => {
      const pending = (await getSnapshotJson(session)).pending_interrupts as { interrupt_id: string; tool_name: string }[];
      return pending.find((interrupt) => interrupt.tool_name === toolName);
    }, { description: `a pending ${toolName} approval`, ...waitOptions });
  }
}

function lastAssistantText(snapshot: AgentSnapshot): string {
  return snapshot.messages.filter((message) => message.role === "assistant" && message.text !== null).at(-1)?.text ?? "";
}
