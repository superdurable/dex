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
import { test } from "node:test";

import { DexServiceError, ErrorSubStatus } from "@superdurable/dex";
import type { AgentSnapshot, PendingUserMessage } from "@superdurable/dex-agents";

import {
  sendUserMessageOnce,
  type MessageSessions,
} from "../../src/products/agent-harnesses/quick-start/quick-start-safe-send.js";

type Failure = "unavailable" | "queued-then-unavailable" | "queued-twice" | "invalid";

class FakeSessions implements MessageSessions {
  public queued: PendingUserMessage[] = [];
  public sendCalls = 0;

  public constructor(private readonly failures: Failure[]) {}

  public async getAgentSnapshot(_sessionId: string): Promise<AgentSnapshot> {
    return { messages: [], queuedMessages: [...this.queued] } as unknown as AgentSnapshot;
  }

  public async sendUserMessage(_sessionId: string, text: string): Promise<boolean> {
    this.sendCalls++;
    const failure = this.failures.shift();
    if (failure === undefined || failure === "queued-then-unavailable" || failure === "queued-twice") {
      this.queue(text);
    }
    if (failure === "queued-twice") {
      this.queue(text);
    }
    if (failure === "unavailable" || failure === "queued-then-unavailable") {
      throw serviceError(14);
    }
    if (failure === "invalid") {
      throw serviceError(3);
    }
    return true;
  }

  public async deleteQueuedMessage(_sessionId: string, messageId: string): Promise<boolean> {
    const before = this.queued.length;
    this.queued = this.queued.filter((item) => item.messageId !== messageId);
    return this.queued.length < before;
  }

  public queuedTexts(): string[] {
    return this.queued.map((item) => item.message.text);
  }

  private queue(text: string): void {
    this.queued.push({ messageId: `m-${this.queued.length + 1}`, message: { text } } as PendingUserMessage);
  }
}

function serviceError(code: number): DexServiceError {
  return new DexServiceError(code as never, ErrorSubStatus.UNCATEGORIZED, "test", "sendUserMessage", "session-1");
}

test("a lost response does not queue the message twice", async () => {
  const sessions = new FakeSessions(["queued-then-unavailable"]);
  await sendUserMessageOnce(sessions, "session-1", "Cancel order 1002", { retryDelayMs: 0 });
  assert.deepEqual(sessions.queuedTexts(), ["Cancel order 1002"]);
  assert.equal(sessions.sendCalls, 1);
});

test("a send that reached no Worker is retried", async () => {
  const sessions = new FakeSessions(["unavailable", "unavailable"]);
  await sendUserMessageOnce(sessions, "session-1", "Cancel order 1002", { retryDelayMs: 0 });
  assert.deepEqual(sessions.queuedTexts(), ["Cancel order 1002"]);
  assert.equal(sessions.sendCalls, 3);
});

test("an extra queued copy is deleted", async () => {
  const sessions = new FakeSessions(["queued-twice"]);
  await sendUserMessageOnce(sessions, "session-1", "Cancel order 1002", { retryDelayMs: 0 });
  assert.deepEqual(sessions.queuedTexts(), ["Cancel order 1002"]);
});

test("other errors are raised", async () => {
  const sessions = new FakeSessions(["invalid"]);
  await assert.rejects(sendUserMessageOnce(sessions, "session-1", "hi", { retryDelayMs: 0 }), DexServiceError);
});
