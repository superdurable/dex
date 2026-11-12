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

// Hosts one Agent Quick Start agent and runs one turn of its session.

import { Client, Registry, Worker, openBlobCache, type BlobCache } from "@superdurable/dex";
import {
  readAgentSnapshot,
  sendAgentUserMessage,
  sessionFlowDefinitionDocument,
  type AgentFrameworkRunner,
  type AgentInterrupt,
  type AgentSessionFlow,
  type DeferredToolOutcome,
} from "@superdurable/dex-agents";

const pullRegistrationTimeoutMs = 30_000;
const pollIntervalMs = 500;
const actionPermission = "agent-quick-start.operate";

/** Decides whether one tool call that waits for approval may run. */
/** A session Flow of any harness, including the built-in agent loop's. */
export type QuickStartSessionFlow = AgentSessionFlow<AgentFrameworkRunner<DeferredToolOutcome>>;

export type ApprovalDecider = (interrupt: AgentInterrupt) => Promise<boolean>;

/** One Registry, Worker, and Client for one durable agent, in this process. */
export class QuickStartApp {
  private constructor(
    public readonly flow: QuickStartSessionFlow,
    public readonly client: Client,
    private readonly worker: Worker,
    private readonly blobCache: BlobCache,
  ) {}

  public static async start(flow: QuickStartSessionFlow, serverAddress: string, blobCacheDir: string): Promise<QuickStartApp> {
    const registry = new Registry([flow]);
    const blobCache = openBlobCache({ directory: blobCacheDir, maxBytes: 1 << 30 });
    const worker = new Worker(registry, blobCache, {
      serverAddress,
      pull: { maxConcurrentTasks: 16 },
      // Dex Web draws the session and offers Approve from this graph.
      flowDefinitions: [sessionFlowDefinitionDocument({ flowType: flow.getFlowType(), actionPermission })],
    });
    const client = new Client(registry, blobCache, { serverAddress });
    const app = new QuickStartApp(flow, client, worker, blobCache);
    try {
      await worker.start();
      await worker.waitPullRegistered({ timeoutMs: pullRegistrationTimeoutMs });
    } catch (failure) {
      await app.close();
      throw failure;
    }
    return app;
  }

  /** Closes the Client, the Worker, and the BlobCache. */
  public async close(): Promise<void> {
    try {
      await this.client.close();
    } finally {
      try {
        await this.worker.close();
      } finally {
        this.blobCache.close();
      }
    }
  }
}

/**
 * Sends `message`, or resumes the active turn when it is undefined, and returns the agent's replies.
 * Each tool call that waits for approval goes to `decideApproval` once.
 */
export async function runTurn(
  app: QuickStartApp,
  sessionId: string,
  message: string | undefined,
  decideApproval: ApprovalDecider,
): Promise<string[]> {
  const before = await readAgentSnapshot(app.client, app.flow, sessionId);
  const finishedTurns = before.state?.turnCount ?? 0;
  let targetTurn = finishedTurns;
  if (message !== undefined) {
    await sendAgentUserMessage(app.client, app.flow, sessionId, { text: message });
    targetTurn = finishedTurns + 1;
  } else if (before.state?.activeTurnId) {
    targetTurn = finishedTurns + 1;
  }
  const answered = new Set<string>();
  for (;;) {
    const snapshot = await readAgentSnapshot(app.client, app.flow, sessionId);
    for (const interrupt of snapshot.pendingInterrupts) {
      if (answered.has(interrupt.interruptId)) {
        continue;
      }
      answered.add(interrupt.interruptId);
      const approved = await decideApproval(interrupt);
      const resolution = { interruptId: interrupt.interruptId, approved, rejectionMessage: null };
      await app.client.invokeRPC(app.flow.resolveInterrupt, sessionId, resolution);
    }
    const state = snapshot.state;
    if (state?.status === "failed") {
      throw new Error(`the session failed: ${state.lastTurnError ?? "unknown error"}`);
    }
    if (state !== null && state.turnCount >= targetTurn && state.status === "waiting_for_user_message") {
      const turnId = `turn-${targetTurn}`;
      return snapshot.messages
        .filter((item) => item.turnId === turnId && item.role === "assistant" && item.text)
        .map((item) => item.text ?? "");
    }
    await new Promise((resolve) => setTimeout(resolve, pollIntervalMs));
  }
}
