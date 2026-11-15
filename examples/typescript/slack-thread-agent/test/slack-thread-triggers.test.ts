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

// Delivers Slack Trigger events to the thread session targets, with redeliveries and restarts.

import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { after, before, test } from "node:test";

import {
  isTriggerUndeliverable,
  newTriggerContext,
  type TriggerEvent,
  type TriggerTarget,
} from "@superdurable/connectors-sdk";
import {
  InMemoryObjectStore,
  TriggerInbox,
  newDurableTriggerTarget,
  parseTriggerPayloadJSON,
  type ObjectStore,
  type TriggerInboxKey,
} from "@superdurable/connectors-sdk/projectconfig";
import {
  ChannelThreadCreatedTriggerDefinition,
  ConnectorID,
  MessageEventSchema,
  ThreadReplyCreatedTriggerDefinition,
  type MessageEvent,
} from "@superdurable/connector-slack";
import { Client, Registry, Worker, openBlobCache, type BlobCache } from "@superdurable/dex";
import { readAgentSnapshot, type AgentSnapshot } from "@superdurable/dex-agents";
import { pollUntil } from "@superdurable/dex-agents/testing";

import { createThreadAgentFlow, echoReplyPrefix, type ThreadAgentFlow } from "../thread-agent-flow.ts";
import {
  newThreadReplyTarget,
  newThreadSessionStartTarget,
  resolveThreadSessionId,
  threadSessionReplyBinding,
  threadSessionStartBinding,
} from "../thread-session-triggers.ts";

const serverAddress = process.env.DEX_FLOW_SERVICE_ADDRESS ?? "127.0.0.1:8801";
const teamId = "T0TEAM";
const channelId = "C0CHANNEL";

let cacheDirectory: string;
let cache: BlobCache;
let flow: ThreadAgentFlow;
let worker: Worker;
let client: Client;

before(async () => {
  cacheDirectory = await mkdtemp(join(tmpdir(), "dex-slack-thread-agent-"));
  cache = openBlobCache({ directory: cacheDirectory, maxBytes: 64 * 1024 * 1024 });
  flow = createThreadAgentFlow();
  const registry = new Registry([flow]);
  worker = new Worker(registry, cache, { serverAddress });
  client = new Client(registry, cache, { serverAddress });
  await worker.start();
  await worker.waitPullRegistered({ timeoutMs: 30_000 });
});

after(async () => {
  await client.close();
  await worker.close();
  cache.close();
  await rm(cacheDirectory, { recursive: true, force: true });
});

test("a new thread starts one session, and a redelivered root starts no second one", async () => {
  const startTarget = newThreadSessionStartTarget(client, flow);
  const root = rootEvent("Where is order 42?");
  const sessionId = resolveThreadSessionId(root);

  await startTarget.handleTrigger(newTriggerContext(), root);
  await startTarget.handleTrigger(newTriggerContext(), root);

  const snapshot = await waitForReplies(sessionId, 1);
  assert.deepEqual(userTexts(snapshot), ["Where is order 42?"]);
  assert.deepEqual(assistantTexts(snapshot), [`${echoReplyPrefix}Where is order 42?`]);
});

test("a reply reaches its thread's session once, however often it is delivered", async () => {
  const startTarget = newThreadSessionStartTarget(client, flow);
  const replyTarget = newThreadReplyTarget(client, flow);
  const root = rootEvent("Hello");
  const sessionId = resolveThreadSessionId(root);
  await startTarget.handleTrigger(newTriggerContext(), root);
  await waitForReplies(sessionId, 1);

  const reply = replyEvent(root, "Any update?");
  assert.equal(resolveThreadSessionId(reply), sessionId);
  // Two concurrent deliveries and a later one share one request ID, so the session queues one message.
  await Promise.all([
    replyTarget.handleTrigger(newTriggerContext(), reply),
    replyTarget.handleTrigger(newTriggerContext(), reply),
  ]);
  await replyTarget.handleTrigger(newTriggerContext(), reply);
  await waitForReplies(sessionId, 2);

  await replyTarget.handleTrigger(newTriggerContext(), replyEvent(root, "Thanks"));
  const snapshot = await waitForReplies(sessionId, 3);
  assert.deepEqual(userTexts(snapshot), ["Hello", "Any update?", "Thanks"]);
  assert.equal(snapshot.queuedMessageCount, 0);
});

test("a reply in a thread without a session is undeliverable", async () => {
  const replyTarget = newThreadReplyTarget(client, flow);
  const reply = replyEvent(rootEvent("never delivered"), "Is anyone there?");

  await assert.rejects(replyTarget.handleTrigger(newTriggerContext(), reply), (error) => isTriggerUndeliverable(error));
});

test("a restarted runner replays events that already reached Dex, and the session gets each once", async () => {
  const objects = new InMemoryObjectStore();
  const startKey = inboxKey(ChannelThreadCreatedTriggerDefinition.trigger.triggerName, threadSessionStartBinding);
  const replyKey = inboxKey(ThreadReplyCreatedTriggerDefinition.trigger.triggerName, threadSessionReplyBinding);
  const root = rootEvent("Start from the inbox");
  const reply = replyEvent(root, "Reply from the inbox");
  const sessionId = resolveThreadSessionId(root);

  // The runner records and delivers both events, then stops before removing them from the inbox.
  const stoppedStart = durableTarget(objects, startKey, newThreadSessionStartTarget(client, flow));
  const stoppedReply = durableTarget(objects, replyKey, newThreadReplyTarget(client, flow));
  await stoppedStart.prepareTrigger?.(newTriggerContext(), root);
  await stoppedReply.prepareTrigger?.(newTriggerContext(), reply);
  assert.equal((await new TriggerInbox(objects, inboxScope, startKey).pending()).length, 1);
  assert.equal((await new TriggerInbox(objects, inboxScope, replyKey).pending()).length, 1);
  await newThreadSessionStartTarget(client, flow).handleTrigger(newTriggerContext(), root);
  await newThreadReplyTarget(client, flow).handleTrigger(newTriggerContext(), reply);
  await waitForReplies(sessionId, 2);

  const restartedStart = durableTarget(objects, startKey, newThreadSessionStartTarget(client, flow));
  const restartedReply = durableTarget(objects, replyKey, newThreadReplyTarget(client, flow));
  await restartedStart.replayTriggerDeliveries?.(newTriggerContext());
  await restartedReply.replayTriggerDeliveries?.(newTriggerContext());

  const snapshot = await waitForReplies(sessionId, 2);
  assert.deepEqual(userTexts(snapshot), ["Start from the inbox", "Reply from the inbox"]);
  assert.deepEqual(await new TriggerInbox(objects, inboxScope, startKey).pending(), []);
  assert.deepEqual(await new TriggerInbox(objects, inboxScope, replyKey).pending(), []);
});

test("a restarted runner delivers an event that was recorded but never reached Dex", async () => {
  const objects = new InMemoryObjectStore();
  const startKey = inboxKey(ChannelThreadCreatedTriggerDefinition.trigger.triggerName, threadSessionStartBinding);
  const root = rootEvent("Recorded before the stop");
  const sessionId = resolveThreadSessionId(root);
  await durableTarget(objects, startKey, newThreadSessionStartTarget(client, flow)).prepareTrigger?.(
    newTriggerContext(),
    root,
  );
  assert.equal((await new TriggerInbox(objects, inboxScope, startKey).pending()).length, 1);

  const restartedStart = durableTarget(objects, startKey, newThreadSessionStartTarget(client, flow));
  await restartedStart.replayTriggerDeliveries?.(newTriggerContext());

  const snapshot = await waitForReplies(sessionId, 1);
  assert.deepEqual(userTexts(snapshot), ["Recorded before the stop"]);
  assert.deepEqual(await new TriggerInbox(objects, inboxScope, startKey).pending(), []);
});

const inboxScope = { projectId: "slack-thread-agent-test", kind: "live" } as const;

function inboxKey(triggerName: string, bindingName: string): TriggerInboxKey {
  return { connectorId: ConnectorID, connectionName: "slack-workspace", triggerName, bindingName };
}

function durableTarget(
  objects: ObjectStore,
  key: TriggerInboxKey,
  target: TriggerTarget<MessageEvent>,
): TriggerTarget<MessageEvent> {
  return newDurableTriggerTarget(new TriggerInbox(objects, inboxScope, key), key, target, {
    decodePayload: (_payloadJSON, payloadBytes) => MessageEventSchema.parse(parseTriggerPayloadJSON(payloadBytes)),
  });
}

function rootEvent(text: string): TriggerEvent<MessageEvent> {
  const timestamp = uniqueSlackTimestamp();
  return slackEvent({ teamId, channelId, timestamp, threadTimestamp: timestamp, userId: "U0MEMBER", text });
}

function replyEvent(root: TriggerEvent<MessageEvent>, text: string): TriggerEvent<MessageEvent> {
  return slackEvent({ ...root.payload, timestamp: uniqueSlackTimestamp(), text });
}

function slackEvent(payload: MessageEvent): TriggerEvent<MessageEvent> {
  return { id: `Ev${randomUUID()}`, occurredAt: new Date(), payload };
}

function uniqueSlackTimestamp(): string {
  return `${Math.floor(Date.now() / 1000)}.${randomUUID().slice(0, 6)}`;
}

async function waitForReplies(sessionId: string, count: number): Promise<AgentSnapshot> {
  return pollUntil(
    async () => {
      const snapshot = await readAgentSnapshot(client, flow, sessionId);
      const isIdle = snapshot.state?.status === "waiting_for_user_message" && snapshot.queuedMessageCount === 0;
      return isIdle && assistantTexts(snapshot).length >= count ? snapshot : undefined;
    },
    { description: `${count} replies in session ${sessionId}`, timeoutMs: 60_000 },
  );
}

function userTexts(snapshot: AgentSnapshot): (string | null)[] {
  return snapshot.messages.filter((message) => message.role === "user").map((message) => message.text);
}

function assistantTexts(snapshot: AgentSnapshot): (string | null)[] {
  return snapshot.messages.filter((message) => message.role === "assistant").map((message) => message.text);
}
