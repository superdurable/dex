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

import { agentMessageChunkCodec, agentMessageCodec } from "../../src/agent-session-codecs.js";
import type { Attribute } from "@superdurable/dex";

import { messageSequenceKey, type AgentMessage, type AgentMessageIndex, type AgentSnapshot } from "../../src/index.js";
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

/** Stored message bytes of a session after a number of turns. */
export interface StoredMessageMeasurement {
  readonly turns: number;
  /** Encoded size of every `AgentMessages` window instance plus every `AgentMessageChunks` instance. */
  readonly storedBytes: number;
  /**
   * Encoded size of every Attribute value and map instance the session holds: the messages above plus the
   * index, state, usage, summary, plan, interrupts, run state, settings, and trace context. Superseded values
   * that the history window keeps are not included.
   */
  readonly liveStateBytes: number;
  /** Messages the session still stores. */
  readonly keptMessages: number;
}

/** What one storage session left behind: its measurements, last snapshot, messages, and Flow status. */
export interface PaddedSession {
  readonly measurements: readonly StoredMessageMeasurement[];
  readonly snapshot: AgentSnapshot;
  readonly messages: readonly AgentMessage[];
  /** The session's `AgentMessageIndex` at the end. */
  readonly messageIndex: AgentMessageIndex | undefined;
  readonly flowStatus: string;
  readonly elapsedSeconds: number;
}

/**
 * Runs turns of a 100-character question and a 600-character answer on a private engine with the given
 * `dex-server-zd` configuration, or its defaults. It queues the turns up to each milestone, waits for them,
 * and measures the stored message bytes, until the last milestone or a closed session.
 */
export async function runPaddedSession(
  milestones: readonly number[],
  serverZdConfig: string | undefined,
  timeoutMs: number,
  extraWorkerEnvironment: Readonly<Record<string, string>> = {},
): Promise<PaddedSession> {
  const configDirectory = mkdtempSync(join(tmpdir(), "dex-agents-storage-session-"));
  const configPath = join(configDirectory, "server-zd.yaml");
  if (serverZdConfig !== undefined) {
    // A config of its own replaces the test engine's process-crash-safe durability.
    writeFileSync(configPath, `${serverZdConfig}dev:\n  durabilityMode: processCrashSafe\n`);
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
      ...extraWorkerEnvironment,
    });
    const sessions = connection.sessions;
    const client = connection.environment.client;
    const sessionId = `compaction-storage-${newToken()}`;
    const startedAt = Date.now();
    const filler = "q".repeat(80);
    const turnCommand = (turn: number) => `pad 600 turn-${String(turn).padStart(4, "0")}-${filler}`;
    await sessions.startSession(sessionId, turnCommand(1));
    const measurements: StoredMessageMeasurement[] = [];
    let queuedTurns = 1;
    let flowStatus = "running";
    for (const milestone of milestones) {
      for (; queuedTurns < milestone; queuedTurns += 1) {
        assert.equal(await sessions.sendUserMessage(sessionId, turnCommand(queuedTurns + 1)), true);
      }
      flowStatus = await pollUntil(
        async () => {
          const status = (await client.describeFlow(sessionId)).status;
          if (status !== "running") {
            return status;
          }
          // AgentState is small; the snapshot RPC would load every message on each poll.
          const state = await sessions.readAgentState(sessionId);
          return state !== undefined && state.activeTurnId === null && state.turnCount >= milestone ? status : undefined;
        },
        { description: `${milestone} finished turns or a closed session ${sessionId}`, timeoutMs, intervalMs: 1_000 },
      );
      if (flowStatus !== "running") {
        break;
      }
      measurements.push({ turns: milestone, ...(await measureStoredMessages(connection, sessionId)) });
    }
    const elapsedSeconds = (Date.now() - startedAt) / 1_000;
    const [messageIndex] = await client.getAttributes(sessionId, sessions.flow.agentMessageIndex);
    return {
      measurements,
      messageIndex,
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

/** Sums the encoded size of every message instance, and of every Attribute value, the session holds. */
async function measureStoredMessages(
  connection: CompactionTestConnection,
  sessionId: string,
): Promise<Omit<StoredMessageMeasurement, "turns">> {
  const client = connection.environment.client;
  const flow = connection.sessions.flow;
  const [[index], [state]] = await Promise.all([
    client.getAttributes(sessionId, flow.agentMessageIndex),
    client.getAttributes(sessionId, flow.agentState),
  ]);
  assert.ok(index !== undefined && state !== undefined);
  let storedBytes = 0;
  for (const range of index.chunks) {
    const [chunk] = await client.getAttributes(
      sessionId,
      flow.agentMessageChunks.instance(messageSequenceKey(range.firstSequence)),
    );
    assert.ok(chunk !== undefined, `chunk ${range.firstSequence}`);
    storedBytes += encodedBytes(agentMessageChunkCodec.encode(chunk).data);
  }
  for (let sequence = index.windowFirstSequence; sequence < state.nextMessageSequence; sequence += 1) {
    const [message] = await client.getAttributes(sessionId, flow.agentMessages.instance(messageSequenceKey(sequence)));
    assert.ok(message !== undefined, `window message ${sequence}`);
    storedBytes += encodedBytes(agentMessageCodec.encode(message).data);
  }
  let liveStateBytes = storedBytes;
  const attributes = [
    flow.agentState,
    flow.agentMessageIndex,
    flow.agentUsage,
    flow.contextSummary,
    flow.agentPlan,
    flow.pendingInterrupts,
    flow.frameworkRunState,
    flow.sessionSettings,
    flow.turnSessionSettings,
    flow.turnCancellationRequest,
    flow.agentTraceContext,
  ] as const;
  for (const attribute of attributes) {
    const [value] = await client.getAttributes(sessionId, attribute as Attribute<unknown>);
    if (value !== undefined) {
      liveStateBytes += encodedBytes((attribute as Attribute<unknown>).codec.encode(value).data);
    }
  }
  return { storedBytes, liveStateBytes, keptMessages: state.nextMessageSequence - index.firstSequence };
}

function encodedBytes(data: unknown): number {
  return Buffer.byteLength(String(data), "utf8");
}

// An 8,000-token window compacts at 6,800 tokens; a turn is about 175 tokens.
export const guardMaxUnsummarizedMessages = 80;

/** The mean encoded size of the kept messages. */
export function storedBytesPerMessage(messages: readonly AgentMessage[]): number {
  const storedBytes = messages.reduce(
    (total, message) => total + String(agentMessageCodec.encode(message).data).length,
    0,
  );
  return storedBytes / messages.length;
}

