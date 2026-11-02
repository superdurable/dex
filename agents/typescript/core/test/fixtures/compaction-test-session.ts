// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

import type { AgentMessage, AgentSnapshot } from "../../src/index.js";
import {
  AgentSessionTestClient,
  AgentTestEnvironment,
  KillableWorkerProcess,
  PrivateDexEngine,
  pollUntil,
} from "../../src/testing/index.js";
import {
  compactionMarkerDirectoryVariable,
  compactionSummaryPadVariable,
  compactionWindowTokensVariable,
  createCompactionTestAgentFlow,
} from "./compaction-test-agent.js";

const workerEntryModulePath = fileURLToPath(new URL("./compaction-test-agent-worker.js", import.meta.url));

/** A test-side Client for the compaction test agent on one engine. */
export interface CompactionTestConnection {
  readonly environment: AgentTestEnvironment;
  readonly sessions: AgentSessionTestClient;
}

/** Opens a Client whose Registry holds the compaction test agent; Workers run in child processes. */
export async function connectCompactionTestClient(engine: PrivateDexEngine): Promise<CompactionTestConnection> {
  const flow = createCompactionTestAgentFlow(join(engine.stateDirectory, "client-unused-markers"));
  const environment = await AgentTestEnvironment.start({
    serverAddress: engine.serverAddress,
    flows: [flow],
    isWorkerInProcess: false,
  });
  return { environment, sessions: new AgentSessionTestClient(environment.client, flow) };
}

/** Starts a compaction test agent Worker child with its marker directory and extra environment. */
export function startCompactionTestWorker(
  engine: PrivateDexEngine,
  markerDirectory: string,
  extraEnvironment: Readonly<Record<string, string>> = {},
): Promise<KillableWorkerProcess> {
  return KillableWorkerProcess.start({
    entryModulePath: workerEntryModulePath,
    serverAddress: engine.serverAddress,
    logPath: join(markerDirectory, "worker.log"),
    environment: { [compactionMarkerDirectoryVariable]: markerDirectory, ...extraEnvironment },
  });
}

/** Reads every stored message of a session through `list_agent_messages`, oldest first. */
export async function listAllMessages(sessions: AgentSessionTestClient, sessionId: string): Promise<AgentMessage[]> {
  const messages: AgentMessage[] = [];
  let beforeSequence: number | null = null;
  for (;;) {
    const page = await sessions.listAgentMessages(sessionId, beforeSequence, 200);
    messages.unshift(...page.messages);
    if (page.nextBeforeSequence === null) {
      return messages;
    }
    beforeSequence = page.nextBeforeSequence;
  }
}

/** Creates a fresh marker directory inside the engine's state directory. */
export function newMarkerDirectory(engine: PrivateDexEngine): string {
  return mkdtempSync(join(engine.stateDirectory, "markers-"));
}

/** Returns a short random token for session IDs. */
export function newToken(): string {
  return randomUUID().slice(0, 8);
}

/** What one storage session left behind: its last snapshot, messages, and Flow status. */
export interface PaddedSession {
  readonly snapshot: AgentSnapshot;
  readonly messages: readonly AgentMessage[];
  readonly flowStatus: string;
  readonly elapsedSeconds: number;
}

/**
 * Queues `turns` turns of a 100-character question and a 600-character answer on a private engine with
 * the given `dex-server-zd` configuration, or its defaults, then waits for the last turn or a closed
 * session.
 */
export async function runPaddedSession(
  turns: number,
  serverZdConfig: string | undefined,
  timeoutMs: number,
): Promise<PaddedSession> {
  const configDirectory = mkdtempSync(join(tmpdir(), "dex-agents-storage-session-"));
  const configPath = join(configDirectory, "server-zd.yaml");
  if (serverZdConfig !== undefined) {
    writeFileSync(configPath, serverZdConfig);
  }
  const engine = await PrivateDexEngine.start(
    serverZdConfig === undefined ? {} : { serverZdConfigPath: configPath },
  );
  const connection = await connectCompactionTestClient(engine);
  let worker: KillableWorkerProcess | undefined;
  try {
    worker = await startCompactionTestWorker(engine, newMarkerDirectory(engine), {
      [compactionWindowTokensVariable]: "8000",
      // A real summary of about 1,000 tokens, so superseded summaries count as they would.
      [compactionSummaryPadVariable]: "4000",
    });
    const sessions = connection.sessions;
    const client = connection.environment.client;
    const sessionId = `compaction-storage-${newToken()}`;
    const startedAt = Date.now();
    const filler = "q".repeat(80);
    const turnCommand = (turn: number) => `pad 600 turn-${String(turn).padStart(4, "0")}-${filler}`;
    await sessions.startSession(sessionId, turnCommand(1));
    for (let turn = 2; turn <= turns; turn += 1) {
      assert.equal(await sessions.sendUserMessage(sessionId, turnCommand(turn)), true);
    }
    const flowStatus = await pollUntil(
      async () => {
        const status = (await client.describeFlow(sessionId)).status;
        if (status !== "running") {
          return status;
        }
        // AgentState is small; the snapshot RPC would load every message on each poll.
        const state = await sessions.readAgentState(sessionId);
        return state !== undefined && state.activeTurnId === null && state.turnCount >= turns ? status : undefined;
      },
      { description: `${turns} finished turns or a closed session ${sessionId}`, timeoutMs, intervalMs: 1_000 },
    );
    const elapsedSeconds = (Date.now() - startedAt) / 1_000;
    return {
      snapshot: await sessions.getAgentSnapshot(sessionId),
      messages: await listAllMessages(sessions, sessionId),
      flowStatus,
      elapsedSeconds,
    };
  } finally {
    await worker?.kill();
    await connection.environment.close();
    await engine.stop();
    rmSync(configDirectory, { recursive: true, force: true });
  }
}
