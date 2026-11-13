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
// The smallest host for a durable agent: one Worker, one Client, one turn.

import { Client, Registry, Worker, openBlobCache } from "@superdurable/dex";
import {
  readAgentSnapshot,
  sendAgentUserMessage,
  startAgentSession,
  type AgentFrameworkRunner,
  type AgentSessionFlow,
  type AgentSnapshot,
  type DeferredToolOutcome,
} from "@superdurable/dex-agents";

/** Starts a session, sends one message, and returns the session after the turn. */
export async function askAgent(
  flow: AgentSessionFlow<AgentFrameworkRunner<DeferredToolOutcome>>,
  sessionId: string,
  message: string,
  serverAddress = "127.0.0.1:8801",
  blobCacheDir = ".dex-blob-cache",
): Promise<AgentSnapshot> {
  const registry = new Registry([flow]);
  const blobCache = openBlobCache({ directory: blobCacheDir, maxBytes: 1 << 30 });
  const worker = new Worker(registry, blobCache, { serverAddress });
  const client = new Client(registry, blobCache, { serverAddress });
  try {
    await worker.start();
    await worker.waitPullRegistered({ timeoutMs: 30_000 });
    await startAgentSession(client, flow, sessionId, {});
    await sendAgentUserMessage(client, flow, sessionId, { text: message });
    for (;;) {
      const snapshot = await readAgentSnapshot(client, flow, sessionId);
      const state = snapshot.state;
      if (state?.status === "failed") {
        throw new Error(`the session failed: ${state.lastTurnError ?? "unknown error"}`);
      }
      if (state !== null && state.turnCount >= 1 && state.status === "waiting_for_user_message") {
        return snapshot;
      }
      await new Promise((resolve) => setTimeout(resolve, 500));
    }
  } finally {
    await client.close();
    await worker.close();
    blobCache.close();
  }
}
