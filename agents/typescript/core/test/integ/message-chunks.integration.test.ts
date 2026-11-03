// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { existsSync, mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { after, afterEach, before, describe, test } from "node:test";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";

import { agentMessageChunkCodec } from "../../src/agent-session-codecs.js";
import {
  createAgentSessionFlow,
  messageSequenceKey,
  type AgentFrameworkRunner,
  type AgentMessage,
  type AgentMessageChunk,
  type AgentMessageChunkRange,
  type AgentMessageIndex,
  type AgentSessionFlow,
  type DeferredToolOutcome,
} from "../../src/index.js";
import {
  AgentSessionTestClient,
  AgentTestEnvironment,
  KillableWorkerProcess,
  PrivateDexEngine,
  executeCompletedEvent,
  pollUntil,
  readStepHistory,
} from "../../src/testing/index.js";
import {
  compactionMessageChunkSizeVariable,
  compactionMessageWindowSizeVariable,
  compactionRetainedMessageLimitVariable,
  createCompactionTestAgentFlow,
  readLoggedCompactionModelCalls,
} from "../fixtures/compaction-test-agent.js";
import { listAllMessages, newMarkerDirectory, newToken, startCompactionTestWorker } from "../fixtures/compaction-test-session.js";
import {
  EchoRunner,
  messageChunkMaxBytesVariable,
  messageChunkSizeVariable,
  messageChunksAgentFlowType,
  messageChunksMarkerDirectoryVariable,
  messageWindowSizeVariable,
  modelGateReachedFileName,
  modelGateReleasedFileName,
} from "../fixtures/message-chunks-runner.js";

const execFileAsync = promisify(execFile);
const echoWorkerModulePath = fileURLToPath(new URL("../fixtures/message-chunks-agent-worker.js", import.meta.url));
const echoRunnerModulePath = fileURLToPath(new URL("../fixtures/message-chunks-runner.js", import.meta.url));
const releasedAgentsPackage = "@superdurable/dex-agents@0.1.0";
const releasedDexPackage = "@superdurable/dex@1.30.0";

/** What a session stores: its index and every chunk and window instance up to the next sequence. */
interface StoredLayout {
  readonly index: AgentMessageIndex | undefined;
  readonly chunks: ReadonlyMap<number, AgentMessageChunk>;
  readonly windowSequences: readonly number[];
}

describe("chunked AgentMessages", () => {
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  let echoFlow: AgentSessionFlow;
  let echoSessions: AgentSessionTestClient;
  let compactionSessions: AgentSessionTestClient;
  const workers: KillableWorkerProcess[] = [];

  before(async () => {
    engine = await PrivateDexEngine.start();
    echoFlow = createAgentSessionFlow(
      new EchoRunner(join(engine.stateDirectory, "client-unused-markers")) as unknown as AgentFrameworkRunner,
      messageChunksAgentFlowType,
    );
    const compactionFlow = createCompactionTestAgentFlow(join(engine.stateDirectory, "client-unused-markers"));
    environment = await AgentTestEnvironment.start({
      serverAddress: engine.serverAddress,
      flows: [echoFlow, compactionFlow],
      isWorkerInProcess: false,
    });
    echoSessions = new AgentSessionTestClient(environment.client, echoFlow);
    compactionSessions = new AgentSessionTestClient(environment.client, compactionFlow);
  });

  afterEach(async () => {
    for (const worker of workers.splice(0)) {
      await worker.kill();
    }
  });

  after(async () => {
    await environment?.close();
    await engine?.stop();
  });

  async function startEchoWorker(
    markerDirectory: string,
    extraEnvironment: Readonly<Record<string, string>>,
    entryModulePath = echoWorkerModulePath,
  ): Promise<KillableWorkerProcess> {
    const worker = await KillableWorkerProcess.start({
      entryModulePath,
      serverAddress: engine.serverAddress,
      logPath: join(markerDirectory, "worker.log"),
      environment: { [messageChunksMarkerDirectoryVariable]: markerDirectory, ...extraEnvironment },
    });
    workers.push(worker);
    return worker;
  }

  async function startCompactionWorker(
    markerDirectory: string,
    extraEnvironment: Readonly<Record<string, string>>,
  ): Promise<KillableWorkerProcess> {
    const worker = await startCompactionTestWorker(engine, markerDirectory, extraEnvironment);
    workers.push(worker);
    return worker;
  }

  async function runTurns(sessions: AgentSessionTestClient, sessionId: string, commands: readonly string[]): Promise<void> {
    await sessions.startSession(sessionId, commands[0] ?? "");
    for (const command of commands.slice(1)) {
      assert.equal(await sessions.sendUserMessage(sessionId, command), true);
    }
    await sessions.waitForCompletedTurns(sessionId, commands.length, { timeoutMs: 180_000 });
  }

  async function readStoredLayout(flow: AgentSessionFlow<AgentFrameworkRunner<DeferredToolOutcome>>, sessionId: string, endSequence: number): Promise<StoredLayout> {
    const client = environment.client;
    const [index] = await client.getAttributes(sessionId, flow.agentMessageIndex);
    const chunks = new Map<number, AgentMessageChunk>();
    const windowSequences: number[] = [];
    for (let sequence = 1; sequence < endSequence; sequence += 1) {
      const [chunk, message] = await client.getAttributes(
        sessionId,
        flow.agentMessageChunks.instance(messageSequenceKey(sequence)),
        flow.agentMessages.instance(messageSequenceKey(sequence)),
      );
      if (chunk !== undefined) {
        chunks.set(sequence, chunk);
      }
      if (message !== undefined) {
        windowSequences.push(sequence);
      }
    }
    return { index, chunks, windowSequences };
  }

  function sequencesOf(messages: readonly AgentMessage[]): number[] {
    return messages.map((message) => message.sequence);
  }

  function range(start: number, end: number): number[] {
    return Array.from({ length: Math.max(0, end - start) }, (_unused, offset) => start + offset);
  }

  function chunkStarts(start: number, end: number, chunkSize: number): number[] {
    const starts: number[] = [];
    for (let sequence = start; sequence < end; sequence += chunkSize) {
      starts.push(sequence);
    }
    return starts;
  }

  function chunkRanges(start: number, end: number, chunkSize: number): AgentMessageChunkRange[] {
    return chunkStarts(start, end, chunkSize).map((first) => ({ firstSequence: first, lastSequence: first + chunkSize - 1 }));
  }

  /**
   * Every listed chunk is stored and holds exactly its range. The archived ranges run from the oldest message to
   * the window; any later range holds one message stored in its own chunk, and the window holds the rest.
   */
  function assertConsistentLayout(layout: StoredLayout, nextSequence: number): AgentMessageIndex {
    const index = layout.index;
    assert.ok(index !== undefined);
    let expectedFirst = index.firstSequence;
    const directSequences = new Set<number>();
    for (const chunkRange of index.chunks) {
      assert.deepEqual(
        sequencesOf(layout.chunks.get(chunkRange.firstSequence)?.messages ?? []),
        range(chunkRange.firstSequence, chunkRange.lastSequence + 1),
      );
      if (chunkRange.firstSequence >= index.windowFirstSequence) {
        assert.equal(chunkRange.lastSequence, chunkRange.firstSequence);
        directSequences.add(chunkRange.firstSequence);
        continue;
      }
      assert.equal(chunkRange.firstSequence, expectedFirst);
      expectedFirst = chunkRange.lastSequence + 1;
    }
    assert.equal(expectedFirst, index.windowFirstSequence);
    assert.deepEqual([...layout.chunks.keys()], index.chunks.map((chunkRange) => chunkRange.firstSequence));
    assert.deepEqual(
      layout.windowSequences,
      range(index.windowFirstSequence, nextSequence).filter((sequence) => !directSequences.has(sequence)),
    );
    return index;
  }

  /** Returns the Attribute keys each Execute wrote, in history order, from the session's Flow history. */
  async function readExecuteWrites(sessionId: string): Promise<{ stepExecutionId: string; keys: string[] }[]> {
    const { stdout } = await execFileAsync(
      process.env.DEXCLI ?? "dexcli",
      ["flow", "history", sessionId, "--server", engine.serverAddress, "--all", "--output", "json"],
      { maxBuffer: 64 * 1024 * 1024 },
    );
    const history = JSON.parse(stdout) as {
      events: { type: string; payload: { context: { stepExecutionId: string }; output?: { upsertAttributes?: { key: string }[] } } }[];
    };
    return history.events
      .filter((event) => event.type === executeCompletedEvent)
      .map((event) => ({
        stepExecutionId: event.payload.context.stepExecutionId,
        keys: (event.payload.output?.upsertAttributes ?? []).map((write) => write.key),
      }));
  }

  async function eventPayloads(
    sessions: AgentSessionTestClient,
    sessionId: string,
    eventType: string,
  ): Promise<Record<string, unknown>[]> {
    return (await sessions.listAgentEvents(sessionId))
      .filter((event) => event.type === eventType)
      .map((event) => JSON.parse(event.payload) as Record<string, unknown>);
  }

  test("messages roll over into many chunks, and a Worker killed during a rollover leaves every message once", async () => {
    const markerDirectory = newMarkerDirectory(engine);
    const sizes = { [messageWindowSizeVariable]: "3", [messageChunkSizeVariable]: "2" };
    const firstWorker = await startEchoWorker(markerDirectory, sizes);
    const sessionId = `chunks-rollover-${newToken()}`;
    const commands = [...range(1, 10).map((turn) => `t${turn}`), "t10 gate"];
    await runTurns(echoSessions, sessionId, commands.slice(0, 9));

    // With a window of 3 and chunks of 2, the answer of turn 10, message 20, moves messages 17 and 18.
    assert.equal(await echoSessions.sendUserMessage(sessionId, "t10 gate"), true);
    await pollUntil(() => (existsSync(join(markerDirectory, modelGateReachedFileName)) ? true : undefined), {
      description: "the model call of turn 10 at its gate",
    });
    await firstWorker.kill();
    writeFileSync(join(markerDirectory, modelGateReleasedFileName), "");
    await startEchoWorker(markerDirectory, sizes);
    await echoSessions.waitForCompletedTurns(sessionId, 10, { timeoutMs: 120_000 });

    const messages = await listAllMessages(echoSessions, sessionId);
    assert.deepEqual(sequencesOf(messages), range(1, 21));
    assert.deepEqual(
      messages.map((message) => message.text),
      commands.flatMap((command, turn) => [command, `echo: ${command} | saw 1-${2 * turn + 1} (${2 * turn + 1})`]),
    );
    const layout = await readStoredLayout(echoFlow, sessionId, 21);
    assert.deepEqual(layout.index, {
      chunks: chunkRanges(1, 19, 2),
      firstSequence: 1,
      windowFirstSequence: 19,
    });
    assertConsistentLayout(layout, 21);
    assert.deepEqual(layout.windowSequences, [19, 20]);
    // The killed attempt committed nothing, so turn 10's model call ran twice and stored one answer.
    const runModelCompletions = (await readStepHistory(engine.serverAddress, sessionId)).filter(
      (entry) => entry.eventType === executeCompletedEvent && entry.stepType === "RunModel",
    );
    assert.equal(runModelCompletions.length, 10);
    assert.deepEqual(
      (await eventPayloads(echoSessions, sessionId, "turn_completed")).map((payload) => payload["message_count"]),
      range(1, 11).map(() => 1),
    );
  });

  test("with compaction, the model input after the summary comes from the window and the chunks after it", async () => {
    const markerDirectory = newMarkerDirectory(engine);
    await startCompactionWorker(markerDirectory, {
      [compactionMessageWindowSizeVariable]: "3",
      [compactionMessageChunkSizeVariable]: "2",
      [compactionRetainedMessageLimitVariable]: "none",
    });
    const sessionId = `chunks-compaction-${newToken()}`;
    // Each "pad" turn stores about 115 tokens, so the sixth model call crosses 85% of 600.
    const commands = [1, 2, 3, 4, 5, 6, 7].map((turn) => `pad 400 t${turn}`);
    await runTurns(compactionSessions, sessionId, commands);

    const snapshot = await compactionSessions.getAgentSnapshot(sessionId);
    const summary = snapshot.contextSummary;
    assert.ok(summary !== null && summary !== undefined);
    assert.ok(summary.lastSequence >= 8);
    // Retention is off, so every message stays.
    assert.deepEqual(sequencesOf(await listAllMessages(compactionSessions, sessionId)), range(1, 15));
    const layout = await readStoredLayout(compactionSessions.flow, sessionId, 15);
    assert.equal(layout.index?.firstSequence, 1);
    assert.ok((layout.index?.windowFirstSequence ?? 0) > summary.lastSequence - 4, "the input spans chunks");
    assert.deepEqual(assertConsistentLayout(layout, 15).chunks, chunkRanges(1, layout.index?.windowFirstSequence ?? 0, 2));
    // The last model call saw exactly the commands stored after the summary; turn k's command is message 2k - 1.
    const lastModelCall = readLoggedCompactionModelCalls(markerDirectory)
      .filter((call) => call.kind === "model")
      .at(-1);
    assert.deepEqual(
      lastModelCall?.items.filter((item) => item[0] === "user" && String(item[1]).startsWith("pad ")).map((item) => item[1]),
      commands.filter((_command, turn) => 2 * turn + 1 > summary.lastSequence),
    );
  });

  test("retention deletes only whole summarized chunks, and never leaves fewer than the limit", async () => {
    const markerDirectory = newMarkerDirectory(engine);
    const retainedMessageLimit = 4;
    const chunkSize = 2;
    await startCompactionWorker(markerDirectory, {
      [compactionMessageWindowSizeVariable]: "3",
      [compactionMessageChunkSizeVariable]: String(chunkSize),
      [compactionRetainedMessageLimitVariable]: String(retainedMessageLimit),
    });
    const sessionId = `chunks-retention-${newToken()}`;
    const commands = range(1, 13).map((turn) => `pad 400 t${turn}`);
    await runTurns(compactionSessions, sessionId, commands);

    const snapshot = await compactionSessions.getAgentSnapshot(sessionId);
    const summary = snapshot.contextSummary;
    assert.ok(summary !== null && summary !== undefined);
    const nextSequence = snapshot.state?.nextMessageSequence ?? 0;
    assert.equal(nextSequence, 25);
    const layout = await readStoredLayout(compactionSessions.flow, sessionId, nextSequence);
    const index = layout.index;
    assert.ok(index !== undefined);
    const firstSequence = index.firstSequence;
    assert.ok(firstSequence > 1, "retention deleted the oldest chunks");
    // Every deleted chunk was summarized whole.
    assert.ok(firstSequence - 1 <= summary.lastSequence, `deleted through ${firstSequence - 1}`);
    // The newest `retainedMessageLimit` messages always stay.
    assert.ok(nextSequence - firstSequence >= retainedMessageLimit, `${nextSequence - firstSequence} kept`);
    // The oldest kept chunk is not deletable: it is not summarized whole, or deleting it would go under the limit.
    const oldest = index.chunks[0];
    if (oldest !== undefined) {
      assert.ok(
        oldest.lastSequence > summary.lastSequence || nextSequence - (oldest.lastSequence + 1) < retainedMessageLimit,
      );
    }
    assert.deepEqual(assertConsistentLayout(layout, nextSequence).chunks, chunkRanges(firstSequence, index.windowFirstSequence, chunkSize));
    const stored = await listAllMessages(compactionSessions, sessionId);
    assert.deepEqual(sequencesOf(stored), range(firstSequence, nextSequence));
    assert.deepEqual(sequencesOf(snapshot.messages), range(firstSequence, nextSequence));
    const beforeTheOldest = await compactionSessions.listAgentMessages(sessionId, firstSequence, 50);
    assert.deepEqual(beforeTheOldest, { messages: [], nextBeforeSequence: null, deletedBeforeSequence: firstSequence });
    const newest = await compactionSessions.listAgentMessages(sessionId, null, 2);
    assert.equal(newest.deletedBeforeSequence, firstSequence);
    assert.equal(snapshot.state?.lastTurnError, null);
  });

  test("list_agent_messages pages and the snapshot's newest 200 cross window and chunk boundaries", async () => {
    const markerDirectory = newMarkerDirectory(engine);
    await startEchoWorker(markerDirectory, { [messageWindowSizeVariable]: "5", [messageChunkSizeVariable]: "3" });
    const sessionId = `chunks-paging-${newToken()}`;
    await runTurns(echoSessions, sessionId, range(1, 111).map((turn) => `p${turn}`));

    const layout = await readStoredLayout(echoFlow, sessionId, 221);
    assert.deepEqual(layout.index, {
      chunks: chunkRanges(1, 217, 3),
      firstSequence: 1,
      windowFirstSequence: 217,
    });
    assertConsistentLayout(layout, 221);
    const snapshot = await echoSessions.getAgentSnapshot(sessionId);
    assert.deepEqual(sequencesOf(snapshot.messages), range(21, 221));

    // Pages of 7 start and end inside chunks and cross into the window.
    const pages: number[][] = [];
    let beforeSequence: number | null = null;
    for (;;) {
      const page = await echoSessions.listAgentMessages(sessionId, beforeSequence, 7);
      pages.unshift(sequencesOf(page.messages));
      assert.equal(page.nextBeforeSequence, page.messages[0]?.sequence === 1 ? null : page.messages[0]?.sequence);
      assert.equal(page.deletedBeforeSequence, null);
      if (page.nextBeforeSequence === null) {
        break;
      }
      beforeSequence = page.nextBeforeSequence;
    }
    assert.deepEqual(pages.flat(), range(1, 221));
    assert.deepEqual(pages.at(-1), range(214, 221));
    assert.deepEqual(sequencesOf((await echoSessions.listAgentMessages(sessionId, 100, 4)).messages), [96, 97, 98, 99]);
    const middle = await echoSessions.listAgentMessages(sessionId, 219, 200);
    assert.deepEqual(sequencesOf(middle.messages), range(19, 219));
    assert.equal(middle.nextBeforeSequence, 19);
  });

  test("a session that a released 0.1.0 Worker wrote migrates into chunks a few at a time and keeps every message", async () => {
    const markerDirectory = newMarkerDirectory(engine);
    const releasedWorkerModulePath = await installReleasedWorker(join(engine.stateDirectory, "released-0.1.0"));
    const releasedWorker = await startEchoWorker(markerDirectory, {}, releasedWorkerModulePath);
    const sessionId = `chunks-upgrade-${newToken()}`;
    const commands = [...range(1, 26).map((turn) => `r${turn}`), "r26 gate"];
    await runTurns(echoSessions, sessionId, commands.slice(0, 25));
    // Turn 26 stops at its model call, so the upgraded Worker finishes a turn the released one started.
    assert.equal(await echoSessions.sendUserMessage(sessionId, "r26 gate"), true);
    await pollUntil(() => (existsSync(join(markerDirectory, modelGateReachedFileName)) ? true : undefined), {
      description: "the released Worker's model call of turn 26",
    });
    await releasedWorker.kill();
    const released = await readStoredLayout(echoFlow, sessionId, 52);
    assert.equal(released.index, undefined);
    assert.equal(released.chunks.size, 0);
    assert.deepEqual(released.windowSequences, range(1, 52));

    writeFileSync(join(markerDirectory, modelGateReleasedFileName), "");
    const sizes = { [messageWindowSizeVariable]: "3", [messageChunkSizeVariable]: "2" };
    await startEchoWorker(markerDirectory, sizes);
    await echoSessions.waitForCompletedTurns(sessionId, 26, { timeoutMs: 120_000 });
    // The first commit stored answer 52, created the index with turn 26's reply sequence, and moved only 4 chunks.
    const firstCommit = await readStoredLayout(echoFlow, sessionId, 53);
    assert.deepEqual(firstCommit.index, {
      chunks: chunkRanges(1, 9, 2),
      firstSequence: 1,
      windowFirstSequence: 9,
    });
    assertConsistentLayout(firstCommit, 53);

    // Each later commit moves 4 more chunks; a half-moved session still reads and replays every message.
    assert.equal(await echoSessions.sendUserMessage(sessionId, "r27"), true);
    await echoSessions.waitForCompletedTurns(sessionId, 27, { timeoutMs: 120_000 });
    const secondTurn = await readStoredLayout(echoFlow, sessionId, 55);
    assert.equal(secondTurn.index?.windowFirstSequence, 25);
    assertConsistentLayout(secondTurn, 55);
    const laterCommands = range(28, 32).map((turn) => `r${turn}`);
    for (const [offset, command] of laterCommands.entries()) {
      assert.equal(await echoSessions.sendUserMessage(sessionId, command), true);
      await echoSessions.waitForCompletedTurns(sessionId, 28 + offset, { timeoutMs: 120_000 });
    }
    const migrated = await readStoredLayout(echoFlow, sessionId, 63);
    const index = assertConsistentLayout(migrated, 63);
    assert.ok(63 - index.windowFirstSequence <= 3, `window starts at ${index.windowFirstSequence}`);
    assert.ok(index.chunks.every((chunkRange) => chunkRange.lastSequence - chunkRange.firstSequence === 1));

    assert.deepEqual(
      (await eventPayloads(echoSessions, sessionId, "turn_completed")).map((payload) => payload["message_count"]),
      range(1, 32).map(() => 1),
    );
    const messages = await listAllMessages(echoSessions, sessionId);
    assert.deepEqual(
      messages.map((message) => message.text),
      [...commands, "r27", ...laterCommands].flatMap((command, turn) => [
        command,
        `echo: ${command} | saw 1-${2 * turn + 1} (${2 * turn + 1})`,
      ]),
    );
  });

  test("AgentMessageIndex is written only by a Step that writes or deletes a chunk, not on every turn", async () => {
    const markerDirectory = newMarkerDirectory(engine);
    await startEchoWorker(markerDirectory, { [messageWindowSizeVariable]: "6", [messageChunkSizeVariable]: "6" });
    const sessionId = `chunks-index-writes-${newToken()}`;
    const commands = range(1, 13).map((turn) => `w${turn}`);
    await runTurns(echoSessions, sessionId, commands);

    const writes = await readExecuteWrites(sessionId);
    const indexWrites = writes.filter((write) => write.keys.includes("AgentMessageIndex"));
    for (const write of indexWrites) {
      assert.ok(
        write.keys.some((key) => key.startsWith("AgentMessageChunks/")),
        `${write.stepExecutionId} wrote the index without a chunk`,
      );
    }
    // 24 messages in chunks of six: three rollovers over 12 turns, and each turn rewrote TurnReplySequence.
    assert.equal(indexWrites.length, 3);
    assert.equal(writes.filter((write) => write.keys.includes("TurnReplySequence")).length, commands.length);
    const [replySequence] = await environment.client.getAttributes(sessionId, echoFlow.turnReplySequence);
    assert.deepEqual(replySequence, { turnId: "turn-12", sequence: 24 });
    assert.deepEqual(
      (await eventPayloads(echoSessions, sessionId, "turn_completed")).map((payload) => payload.message_count),
      commands.map(() => 1),
    );
  });

  test("a message too large for the Server's value limit fails the turn that stores it, and the next turn runs", async () => {
    const maxValueBytes = 8_192;
    const configPath = join(newMarkerDirectory(engine), "server-zd.yaml");
    writeFileSync(configPath, `values:\n  maxValueBytes: ${maxValueBytes}\n  blobDbMinValueBytes: 4096\nlimits:\n  maxFlowStateBytes: 67108864\n`);
    const limitedEngine = await PrivateDexEngine.start({ serverZdConfigPath: configPath });
    const limitedEnvironment = await AgentTestEnvironment.start({
      serverAddress: limitedEngine.serverAddress,
      flows: [echoFlow],
      isWorkerInProcess: false,
    });
    try {
      const sessions = new AgentSessionTestClient(limitedEnvironment.client, echoFlow);
      const markerDirectory = newMarkerDirectory(engine);
      const worker = await KillableWorkerProcess.start({
        entryModulePath: echoWorkerModulePath,
        serverAddress: limitedEngine.serverAddress,
        logPath: join(markerDirectory, "worker.log"),
        environment: {
          [messageChunksMarkerDirectoryVariable]: markerDirectory,
          [messageWindowSizeVariable]: "4",
          [messageChunkSizeVariable]: "4",
          [messageChunkMaxBytesVariable]: "4000",
        },
      });
      workers.push(worker);
      const sessionId = `chunks-edge-${newToken()}`;
      await sessions.startSession(sessionId, "say hello");
      await sessions.waitForCompletedTurns(sessionId, 1);
      // The answer is message 4. Its own chunk, 15 bytes more than the message, is 2 bytes over the limit:
      // the size a window instance fits but a chunk of one does not.
      const answerCharacters = sizeAnswerForChunkBytes(4, "turn-2", maxValueBytes + 2);
      assert.equal(await sessions.sendUserMessage(sessionId, `big ${answerCharacters}`), true);
      const failed = await pollUntil(
        async () => {
          const state = (await sessions.getAgentSnapshot(sessionId)).state;
          return state !== null && state.turnCount >= 2 ? state : undefined;
        },
        { description: "the second turn to end", timeoutMs: 120_000 },
      );
      assert.match(failed.lastTurnError ?? "", /exceeds/);
      assert.equal(await sessions.sendUserMessage(sessionId, "say again"), true);
      await sessions.waitForCompletedTurns(sessionId, 3);
      const snapshot = await sessions.getAgentSnapshot(sessionId);
      assert.equal(snapshot.state?.lastTurnError, null);
      assert.ok(snapshot.messages.at(-1)?.text?.startsWith("echo: say again"));
      assert.ok(!snapshot.messages.some((message) => message.text?.startsWith("yyy")));
    } finally {
      await limitedEnvironment.close();
      await limitedEngine.stop();
    }
  });

  test("a chunk closes at its byte cap, and a message larger than the cap is stored straight into its own chunk", async () => {
    const markerDirectory = newMarkerDirectory(engine);
    const chunkMaxBytes = 3_000;
    await startEchoWorker(markerDirectory, {
      [messageWindowSizeVariable]: "4",
      [messageChunkSizeVariable]: "4",
      [messageChunkMaxBytesVariable]: String(chunkMaxBytes),
    });
    const sessionId = `chunks-bytes-${newToken()}`;
    const sizes = [1, 1, 600, 1, 1_600, 1, 1, 600, 600, 1, 1, 1, 1_600, 1, 1, 1];
    const commands = sizes.map((size, turn) => `b${turn + 1} ${"x".repeat(size)}`);
    await runTurns(echoSessions, sessionId, commands);

    const nextSequence = 2 * commands.length + 1;
    const layout = await readStoredLayout(echoFlow, sessionId, nextSequence);
    const index = assertConsistentLayout(layout, nextSequence);
    const chunkBytes = index.chunks.map((chunkRange) =>
      Buffer.byteLength(String(agentMessageChunkCodec.encode(layout.chunks.get(chunkRange.firstSequence) as AgentMessageChunk).data)),
    );
    const lengths = index.chunks.map((chunkRange) => chunkRange.lastSequence - chunkRange.firstSequence + 1);
    for (const [offset, bytes] of chunkBytes.entries()) {
      assert.ok((lengths[offset] ?? 0) <= 4);
      // Over the cap only when it holds one message.
      assert.ok(bytes <= chunkMaxBytes || lengths[offset] === 1, `chunk ${offset}: ${bytes} bytes`);
    }
    assert.ok(chunkBytes.some((bytes, offset) => bytes > chunkMaxBytes && lengths[offset] === 1), "an oversized message alone");
    assert.ok(lengths.some((length) => length > 1 && length < 4), "a chunk closed by its byte cap");
    assert.ok(lengths.includes(4), "a chunk closed by its message count");
    // An oversized message was never a window instance: its first store wrote its own chunk.
    const oversized = index.chunks.filter((_chunkRange, offset) => (chunkBytes[offset] ?? 0) > chunkMaxBytes);
    const writes = await readExecuteWrites(sessionId);
    for (const chunkRange of oversized) {
      const key = messageSequenceKey(chunkRange.firstSequence);
      assert.ok(!writes.some((write) => write.keys.includes(`AgentMessages/${key}`)), `message ${key} entered the window`);
      assert.equal(writes.filter((write) => write.keys.includes(`AgentMessageChunks/${key}`)).length, 1);
    }
    const oversizedStarts = new Set(oversized.map((chunkRange) => chunkRange.firstSequence));
    // Each chunk closed for a reason: four messages, an oversized message next, or the next message would not fit.
    for (const [offset, chunkRange] of index.chunks.slice(0, -1).entries()) {
      if (lengths[offset] === 4 || oversizedStarts.has(chunkRange.firstSequence) || oversizedStarts.has(chunkRange.lastSequence + 1)) {
        continue;
      }
      const next = layout.chunks.get(chunkRange.lastSequence + 1)?.messages[0];
      assert.ok(next !== undefined);
      const withNext = String(
        agentMessageChunkCodec.encode({
          messages: [...(layout.chunks.get(chunkRange.firstSequence) as AgentMessageChunk).messages, next],
        }).data,
      );
      assert.ok(Buffer.byteLength(withNext) > chunkMaxBytes, `chunk ${offset} closed early`);
    }
    const messages = await listAllMessages(echoSessions, sessionId);
    assert.deepEqual(
      messages.map((message) => message.text),
      commands.flatMap((command, turn) => [command, `echo: ${command} | saw 1-${2 * turn + 1} (${2 * turn + 1})`]),
    );
    for (const limit of [1, 3, 5]) {
      const pages: number[] = [];
      let beforeSequence: number | null = null;
      for (;;) {
        const page = await echoSessions.listAgentMessages(sessionId, beforeSequence, limit);
        pages.unshift(...sequencesOf(page.messages));
        if (page.nextBeforeSequence === null) {
          break;
        }
        beforeSequence = page.nextBeforeSequence;
      }
      assert.deepEqual(pages, range(1, nextSequence));
    }
  });
});

/**
 * Returns how many "y" characters make the `big` answer at `sequence` of `turnId` encode, alone in a chunk, to
 * `chunkBytes` bytes. Each character adds two bytes: one in `text` and one in `frameworkItem`.
 */
function sizeAnswerForChunkBytes(sequence: number, turnId: string, chunkBytes: number): number {
  const encodedBytes = (characters: number): number =>
    Buffer.byteLength(
      String(
        agentMessageChunkCodec.encode({
          messages: [
            {
              sequence,
              turnId,
              role: "assistant",
              text: "y".repeat(characters),
              toolCallId: null,
              toolName: null,
              toolArguments: null,
              toolOutput: null,
              frameworkItem: JSON.stringify({ role: "assistant", text: "y".repeat(characters) }),
              createdAt: new Date(0).toISOString(),
            },
          ],
        }).data,
      ),
    );
  const characters = Math.floor((chunkBytes - encodedBytes(0)) / 2);
  assert.ok(Math.abs(encodedBytes(characters) - chunkBytes) <= 1);
  return characters;
}

/**
 * Installs the released core from npm next to the Dex SDK it pins and writes a Worker entry module that
 * serves the echo agent with it.
 * @returns The entry module path.
 */
async function installReleasedWorker(directory: string): Promise<string> {
  mkdirSync(directory, { recursive: true });
  writeFileSync(join(directory, "package.json"), JSON.stringify({ name: "released-worker", private: true, type: "module" }));
  await execFileAsync(
    "npm",
    ["install", "--no-audit", "--no-fund", "--ignore-scripts", releasedAgentsPackage, releasedDexPackage],
    { cwd: directory, timeout: 180_000 },
  );
  const entryModulePath = join(directory, "released-worker.mjs");
  writeFileSync(
    entryModulePath,
    [
      'import { createAgentSessionFlow } from "@superdurable/dex-agents";',
      'import { runAgentWorkerProcess } from "@superdurable/dex-agents/testing";',
      `import { EchoRunner, messageChunksAgentFlowType, messageChunksMarkerDirectoryVariable } from ${JSON.stringify(echoRunnerModulePath)};`,
      "const runner = new EchoRunner(process.env[messageChunksMarkerDirectoryVariable]);",
      "await runAgentWorkerProcess([createAgentSessionFlow(runner, messageChunksAgentFlowType)]);",
      "",
    ].join("\n"),
  );
  return entryModulePath;
}
