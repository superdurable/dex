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
import { afterEach, describe, test } from "node:test";
import { fileURLToPath } from "node:url";

import type { AgentFrameworkRunner, AgentSessionFlow, DeferredToolOutcome } from "../../src/index.js";
import { outcomeUnknownToolResult } from "../../src/index.js";
import {
  AgentSessionTestClient,
  AgentTestEnvironment,
  FileGate,
  InvocationLog,
  KillableWorkerProcess,
  executeCompletedEvent,
  pollUntil,
  readStepHistory,
  waitForCompletedEvent,
} from "../../src/testing/index.js";
import {
  compactionMarkerDirectoryVariable,
  createCompactionTestAgentFlow,
  readLoggedCompactionModelCalls,
  summaryGateName,
} from "./compaction-test-agent.js";
import {
  startCrashableServer,
  type CrashableServer,
  type ServerFailureKind,
} from "./crashable-dex-server.js";
import {
  createScriptedAgentFlow,
  scriptedAgentBlockingMsVariable,
  scriptedAgentMarkerDirectoryVariable,
} from "./scripted-agent-runner.js";
import { createStepwiseTestAgentFlow, stepwiseMarkerDirectoryVariable } from "./stepwise-test-agent.js";

type AgentFlow = AgentSessionFlow<AgentFrameworkRunner<DeferredToolOutcome>>;

interface AgentFixture {
  readonly createFlow: (markerDirectory: string) => AgentFlow;
  readonly workerEntry: string;
  readonly markerVariable: string;
}

const scriptedAgent: AgentFixture = {
  createFlow: (markerDirectory) => createScriptedAgentFlow(markerDirectory) as AgentFlow,
  workerEntry: fixturePath("scripted-agent-worker.js"),
  markerVariable: scriptedAgentMarkerDirectoryVariable,
};
const stepwiseAgent: AgentFixture = {
  createFlow: (markerDirectory) => createStepwiseTestAgentFlow(markerDirectory) as unknown as AgentFlow,
  workerEntry: fixturePath("stepwise-test-agent-worker.js"),
  markerVariable: stepwiseMarkerDirectoryVariable,
};
const compactionAgent: AgentFixture = {
  createFlow: (markerDirectory) => createCompactionTestAgentFlow(markerDirectory) as unknown as AgentFlow,
  workerEntry: fixturePath("compaction-test-agent-worker.js"),
  markerVariable: compactionMarkerDirectoryVariable,
};
const turnTimeout = { timeoutMs: 120_000 };

function fixturePath(name: string): string {
  return fileURLToPath(new URL(`./${name}`, import.meta.url));
}

/** One server, one Client, and one running Worker that outlives the failure. */
class ServerFailureStack {
  private constructor(
    public readonly server: CrashableServer,
    public readonly sessions: AgentSessionTestClient,
    public readonly markerDirectory: string,
    private readonly environment: AgentTestEnvironment,
    private readonly worker: KillableWorkerProcess,
  ) {}

  public static async open(
    kind: ServerFailureKind,
    agent: AgentFixture,
    workerEnvironment: Readonly<Record<string, string>> = {},
  ): Promise<ServerFailureStack> {
    const server = await startCrashableServer(kind);
    const markerDirectory = mkdtempSync(join(server.stateDirectory, "markers-"));
    const flow = agent.createFlow(join(server.stateDirectory, "client-unused-markers"));
    const environment = await AgentTestEnvironment.start({
      serverAddress: server.serverAddress,
      flows: [flow],
      isWorkerInProcess: false,
    });
    const worker = await KillableWorkerProcess.start({
      entryModulePath: agent.workerEntry,
      serverAddress: server.serverAddress,
      logPath: join(markerDirectory, "worker.log"),
      environment: { [agent.markerVariable]: markerDirectory, ...workerEnvironment },
    });
    const sessions = new AgentSessionTestClient(environment.client, flow);
    return new ServerFailureStack(server, sessions, markerDirectory, environment, worker);
  }

  public gate(name: string): FileGate {
    return new FileGate(this.markerDirectory, name);
  }

  public invocations(name: string): string[] {
    return new InvocationLog(join(this.markerDirectory, `${name}.log`)).names();
  }

  /** Crashes the server, optionally lets the interrupted run finish meanwhile, and recovers. */
  public async crashAndRecover(gate?: FileGate): Promise<void> {
    await this.server.crash();
    gate?.release();
    await this.server.recover();
  }

  public async lastMessage(sessionId: string): Promise<string | null | undefined> {
    return (await this.sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text;
  }

  public async executedStepTypes(sessionId: string): Promise<string[]> {
    return (await readStepHistory(this.server.serverAddress, sessionId))
      .filter((entry) => entry.eventType === executeCompletedEvent)
      .map((entry) => entry.stepType);
  }

  public async waitForStepMethods(sessionId: string, stepType: string, eventType: string, count: number): Promise<void> {
    await pollUntil(
      async () =>
        (await readStepHistory(this.server.serverAddress, sessionId)).filter(
          (entry) => entry.stepType === stepType && entry.eventType === eventType,
        ).length >= count
          ? true
          : undefined,
      { description: `${count} ${stepType} ${eventType} in session ${sessionId}` },
    );
  }

  public async finishedStatuses(sessionId: string): Promise<unknown[]> {
    return (await this.sessions.listAgentEvents(sessionId))
      .filter((event) => event.type === "tool_call_finished")
      .map((event) => (JSON.parse(event.payload) as { status: unknown }).status);
  }

  public async close(): Promise<void> {
    await this.worker.kill();
    await this.environment.close();
    await this.server.stop();
  }
}

/** Defines the crash tests of every agent Step for one kind of server failure. */
export function defineServerFailureSuite(kind: ServerFailureKind): void {
  describe(`agent sessions survive a server ${kind} while the Worker keeps running`, () => {
    let stack: ServerFailureStack | undefined;

    async function open(
      agent: AgentFixture,
      workerEnvironment?: Readonly<Record<string, string>>,
    ): Promise<ServerFailureStack> {
      stack = await ServerFailureStack.open(kind, agent, workerEnvironment);
      return stack;
    }

    afterEach(async () => {
      await stack?.close();
      stack = undefined;
    });


    test("a failure during RunModel reruns only that model call", async () => {
      const agent = await open(scriptedAgent);
      const sessionId = `server-model-${randomUUID()}`;
      await agent.sessions.startSession(sessionId, "call get_weather then pause");
      const gate = agent.gate("turn");
      await gate.waitUntilReached();

      await agent.crashAndRecover(gate);
      await agent.sessions.waitForCompletedTurns(sessionId, 1, turnTimeout);

      assert.equal(await agent.lastMessage(sessionId), "get_weather -> sunny in Paris");
      assert.equal(agent.invocations("get_weather").length, 1);
      assert.equal(gate.reachedCount(), 2);
    });

    test("a failure before any model output reruns the model call", async () => {
      const agent = await open(scriptedAgent, { [scriptedAgentBlockingMsVariable]: "2000" });
      const sessionId = `server-model-window-${randomUUID()}`;
      await agent.sessions.startSession(sessionId, "block");
      const gate = agent.gate("turn");
      await gate.waitUntilReached();

      await agent.crashAndRecover();
      await agent.sessions.waitForCompletedTurns(sessionId, 1, turnTimeout);

      assert.equal(await agent.lastMessage(sessionId), "echo: block");
      assert.equal(gate.reachedCount(), 2);
    });

    test("a failure before the first heartbeat retries an idempotent write", async () => {
      const agent = await open(scriptedAgent, { [scriptedAgentBlockingMsVariable]: "2000" });
      const sessionId = `server-tool-window-${randomUUID()}`;
      await agent.sessions.startSession(sessionId, "call blocking_refund");
      await agent.gate("blocking_refund").waitUntilReached();

      await agent.crashAndRecover();
      await agent.sessions.waitForCompletedTurns(sessionId, 1, turnTimeout);

      assert.deepEqual(agent.invocations("blocking_refund"), [
        `${sessionId} turn-1-blocking_refund attempt 1`,
        `${sessionId} turn-1-blocking_refund attempt 2`,
      ]);
    });

    test("a failure during CallTool retries with the same key", async () => {
      const agent = await open(scriptedAgent);
      const sessionId = `server-call-tool-${randomUUID()}`;
      await agent.sessions.startSession(sessionId, "call transfer_funds");
      const gate = agent.gate("transfer_funds");
      await gate.waitUntilReached();

      await agent.crashAndRecover(gate);
      await agent.sessions.waitForCompletedTurns(sessionId, 1, turnTimeout);

      assert.deepEqual(agent.invocations("transfer_funds"), [
        `${sessionId} turn-1-transfer_funds attempt 1`,
        `${sessionId} turn-1-transfer_funds attempt 2`,
      ]);
      assert.equal(gate.reachedCount(), 2);
    });

    test("a failure during a single-attempt write records outcome_unknown and never reruns it", async () => {
      const agent = await open(scriptedAgent);
      const sessionId = `server-write-${randomUUID()}`;
      await agent.sessions.startSession(sessionId, "call charge_card");
      const interrupt = await agent.sessions.waitForPendingInterrupt(sessionId, "charge_card");
      assert.equal(await agent.sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
      const gate = agent.gate("charge_card");
      await gate.waitUntilReached();

      await agent.crashAndRecover(gate);
      await agent.sessions.waitForCompletedTurns(sessionId, 1, turnTimeout);

      assert.equal(await agent.lastMessage(sessionId), `charge_card -> ${outcomeUnknownToolResult}`);
      assert.deepEqual(agent.invocations("charge_card"), [`${sessionId} turn-1-charge_card attempt 1`]);
      assert.equal(gate.reachedCount(), 1);
      assert.ok((await agent.executedStepTypes(sessionId)).includes("RecordToolOutcomeUnknown"));
    });

    test("a failure before the first heartbeat runs a single-attempt tool once", async () => {
      const agent = await open(scriptedAgent, { [scriptedAgentBlockingMsVariable]: "2000" });
      const sessionId = `server-window-${randomUUID()}`;
      await agent.sessions.startSession(sessionId, "call blocking_write");
      const gate = agent.gate("blocking_write");
      await gate.waitUntilReached();

      await agent.crashAndRecover();
      await agent.sessions.waitForCompletedTurns(sessionId, 1, turnTimeout);

      assert.equal(await agent.lastMessage(sessionId), `blocking_write -> ${outcomeUnknownToolResult}`);
      assert.deepEqual(agent.invocations("blocking_write"), [`${sessionId} turn-1-blocking_write attempt 1`]);
      assert.equal(gate.reachedCount(), 1);
    });

    test("an approval wait survives and the write runs once after approval", async () => {
      const agent = await open(scriptedAgent);
      const sessionId = `server-approval-${randomUUID()}`;
      await agent.sessions.startSession(sessionId, "call send_email");
      const interrupt = await agent.sessions.waitForPendingInterrupt(sessionId, "send_email");
      await agent.waitForStepMethods(sessionId, "AwaitToolApproval", waitForCompletedEvent, 1);

      await agent.crashAndRecover();

      assert.deepEqual(await agent.sessions.waitForPendingInterrupt(sessionId, "send_email"), interrupt);
      assert.equal(await agent.sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
      await agent.sessions.waitForCompletedTurns(sessionId, 1, turnTimeout);
      assert.equal(
        await agent.lastMessage(sessionId),
        `send_email -> email sent for ${sessionId}/turn-1/turn-1-send_email`,
      );
      assert.deepEqual(agent.invocations("send_email"), [`${sessionId} turn-1-send_email attempt 1`]);
    });

    test("a question wait survives and the answer resumes the turn", async () => {
      const agent = await open(stepwiseAgent);
      const token = randomUUID().slice(0, 8);
      const sessionId = `server-question-${token}`;
      await agent.sessions.startSession(sessionId, `ask ${token}`);
      const interrupt = await agent.sessions.waitForPendingInterrupt(sessionId, "ask_user");
      await agent.waitForStepMethods(sessionId, "AwaitQuestionAnswer", waitForCompletedEvent, 1);

      await agent.crashAndRecover();

      assert.deepEqual(await agent.sessions.waitForPendingInterrupt(sessionId, "ask_user"), interrupt);
      assert.equal(await agent.sessions.answerQuestion(sessionId, interrupt.interruptId, "teal"), true);
      await agent.sessions.waitForCompletedTurns(sessionId, 1, turnTimeout);
      assert.equal(await agent.lastMessage(sessionId), 'answer: {"status":"answered","answer":"teal"}');
    });

    test("a Timer wait survives and fires once", async () => {
      const agent = await open(stepwiseAgent);
      const token = randomUUID().slice(0, 8);
      const sessionId = `server-timer-${token}`;
      await agent.sessions.startSession(sessionId, `wait 3 ${token}`);
      await agent.waitForStepMethods(sessionId, "AwaitTimer", waitForCompletedEvent, 1);

      await agent.crashAndRecover();
      await agent.sessions.waitForCompletedTurns(sessionId, 1, turnTimeout);

      assert.equal(
        await agent.lastMessage(sessionId),
        `waited: {"status":"completed","waited_seconds":3,"reason":"cool down ${token}"}`,
      );
      assert.equal((await agent.executedStepTypes(sessionId)).filter((type) => type === "AwaitTimer").length, 1);
    });

    test("a poll wait survives and polling resumes", async () => {
      const agent = await open(scriptedAgent);
      const sessionId = `server-poll-wait-${randomUUID()}`;
      await agent.sessions.startSession(sessionId, "call gated_report_task");
      const gate = agent.gate("gated_report_task");
      await gate.waitUntilReached();
      // The second PollToolTask waits on its 3 second Timer when the server dies.
      await agent.waitForStepMethods(sessionId, "PollToolTask", waitForCompletedEvent, 2);

      await agent.crashAndRecover(gate);
      await agent.sessions.waitForCompletedTurns(sessionId, 1, turnTimeout);

      assert.equal(await agent.lastMessage(sessionId), "gated_report_task -> report ready");
      assert.deepEqual(agent.invocations("gated_report_task"), [`${sessionId} turn-1-gated_report_task attempt 1`]);
      assert.deepEqual(await agent.finishedStatuses(sessionId), ["succeeded"]);
    });

    test("a failure during a poll polls again and keeps one task", async () => {
      const agent = await open(scriptedAgent);
      const sessionId = `server-poll-${randomUUID()}`;
      await agent.sessions.startSession(sessionId, "call blocking_report_task");
      const gate = agent.gate("blocking_report_task");
      await gate.waitUntilReached();

      await agent.crashAndRecover(gate);
      await agent.sessions.waitForCompletedTurns(sessionId, 1, turnTimeout);

      assert.equal(await agent.lastMessage(sessionId), "blocking_report_task -> report ready");
      assert.equal(gate.reachedCount(), 2);
      assert.deepEqual(agent.invocations("blocking_report_task"), [
        `${sessionId} turn-1-blocking_report_task attempt 1`,
      ]);
      assert.deepEqual(await agent.finishedStatuses(sessionId), ["succeeded"]);
    });

    test("an idle session survives and the next message starts a turn", async () => {
      const agent = await open(scriptedAgent);
      const sessionId = `server-idle-${randomUUID()}`;
      await agent.sessions.startSession(sessionId, "first");
      await agent.sessions.waitForCompletedTurns(sessionId, 1, turnTimeout);

      await agent.crashAndRecover();

      await pollUntil(
        async () => ((await agent.sessions.sendUserMessage(sessionId, "second")) ? true : undefined),
        { description: `session ${sessionId} to take a message` },
      );
      await agent.sessions.waitForCompletedTurns(sessionId, 2, turnTimeout);
      assert.equal(await agent.lastMessage(sessionId), "echo: second");
    });

    test("a failure during CompactContext reruns only the summary call and commits one generation", async () => {
      const agent = await open(compactionAgent);
      const sessionId = `server-compaction-${randomUUID().slice(0, 8)}`;
      const commands = ["pad 400 gate-summary", ...[2, 3, 4, 5, 6].map((turn) => `pad 400 t${turn}`)];
      await agent.sessions.startSession(sessionId, commands[0] ?? "");
      for (const command of commands.slice(1)) {
        assert.equal(await agent.sessions.sendUserMessage(sessionId, command), true);
      }
      const gate = agent.gate(summaryGateName);
      await gate.waitUntilReached();

      await agent.crashAndRecover(gate);
      await agent.sessions.waitForCompletedTurns(sessionId, commands.length, turnTimeout);

      assert.equal(gate.reachedCount(), 2);
      const calls = readLoggedCompactionModelCalls(agent.markerDirectory);
      assert.equal(calls.filter((call) => call.kind === "summary").length, 2);
      assert.equal((await agent.sessions.getAgentSnapshot(sessionId)).contextSummary?.generation, 1);
      assert.equal(
        (await agent.executedStepTypes(sessionId)).filter((type) => type === "CompactContext").length,
        1,
      );
    });
  });
}
