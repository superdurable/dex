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
// Queues one user message without queuing it twice when a send is retried.

import { DexServiceError, type Client } from "@superdurable/dex";
import {
  readAgentSnapshot,
  sendAgentUserMessage,
  type AgentFrameworkRunner,
  type AgentSessionFlow,
  type AgentSnapshot,
  type DeferredToolOutcome,
} from "@superdurable/dex-agents";

// gRPC codes UNAVAILABLE and DEADLINE_EXCEEDED: the send may or may not have arrived.
const UNCERTAIN_CODES: readonly number[] = [14, 4];

export interface MessageSessions {
  getAgentSnapshot(sessionId: string): Promise<AgentSnapshot>;
  sendUserMessage(sessionId: string, text: string): Promise<boolean>;
  deleteQueuedMessage(sessionId: string, messageId: string): Promise<boolean>;
}

export function messageSessions(
  client: Client,
  flow: AgentSessionFlow<AgentFrameworkRunner<DeferredToolOutcome>>,
): MessageSessions {
  return {
    getAgentSnapshot: (sessionId) => readAgentSnapshot(client, flow, sessionId),
    sendUserMessage: (sessionId, text) => sendAgentUserMessage(client, flow, sessionId, { text }),
    deleteQueuedMessage: (sessionId, messageId) =>
      client.invokeRPC(flow.deleteQueuedMessage, sessionId, { messageId }),
  };
}

/**
 * Queues one user message, retrying a failed send without queuing it twice.
 * Before each retry it reads the session and stops when a new copy of the text is there.
 * At the end it deletes any extra queued copy. It tells messages apart by their text,
 * so call it once for each message a user sends.
 */
export async function sendUserMessageOnce(
  sessions: MessageSessions,
  sessionId: string,
  text: string,
  { attempts = 5, retryDelayMs = 1_000 }: { attempts?: number; retryDelayMs?: number } = {},
): Promise<void> {
  const copiesBefore = countCopies(await sessions.getAgentSnapshot(sessionId), text);
  for (let attempt = 1; attempt <= attempts; attempt++) {
    try {
      await sessions.sendUserMessage(sessionId, text);
      break;
    } catch (error) {
      const isUncertain = error instanceof DexServiceError && UNCERTAIN_CODES.includes(error.code);
      if (!isUncertain || attempt === attempts) {
        throw error;
      }
    }
    await new Promise((resolve) => setTimeout(resolve, retryDelayMs));
    if (countCopies(await sessions.getAgentSnapshot(sessionId), text) > copiesBefore) {
      break;
    }
  }
  await deleteExtraCopies(sessions, sessionId, text, copiesBefore + 1);
}

async function deleteExtraCopies(
  sessions: MessageSessions,
  sessionId: string,
  text: string,
  wanted: number,
): Promise<void> {
  const snapshot = await sessions.getAgentSnapshot(sessionId);
  const extra = countCopies(snapshot, text) - wanted;
  if (extra <= 0) {
    return;
  }
  const queued = (snapshot.queuedMessages ?? []).filter((item) => item.message.text === text);
  for (const item of queued.slice(Math.max(0, queued.length - extra)).reverse()) {
    await sessions.deleteQueuedMessage(sessionId, item.messageId);
  }
}

function countCopies(snapshot: AgentSnapshot, text: string): number {
  const delivered = snapshot.messages.filter((message) => message.role === "user" && message.text === text).length;
  const queued = (snapshot.queuedMessages ?? []).filter((item) => item.message.text === text).length;
  return delivered + queued;
}
