// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { randomUUID } from "node:crypto";
import { mkdtempSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

import type { AgentEvent, AgentInterrupt, AgentSessionFlow, AgentSnapshot } from "@superdurable/dex-agents";
import {
  AgentSessionTestClient,
  AgentTestEnvironment,
  FileGate,
  InvocationLog,
  KillableWorkerProcess,
  pollUntil,
  type PrivateDexEngine,
} from "@superdurable/dex-agents/testing";

import { createClaudeTestAgentFlow, type ClaudeTestAgentCli } from "./claude-test-agent.js";
import {
  fakeClaudeInvocationLogName,
  fakeClaudeStateDirectoryVariable,
} from "./fake-claude-cli-environment.js";

const workerEntryModulePath = fileURLToPath(new URL("./claude-test-worker.js", import.meta.url));
const turnTimeoutMs = 120_000;

/** One test's Claude agent: its state directory, Flow, Client, and in-process or child Workers. */
export class ClaudeAgentTestHarness {
  private readonly workers: KillableWorkerProcess[] = [];

  private constructor(
    private readonly engine: PrivateDexEngine,
    /** Directory of this test's gates, invocation log, workspace, and Claude config. */
    public readonly stateDirectory: string,
    /** The Claude test agent Flow. */
    public readonly flow: AgentSessionFlow,
    private readonly environment: AgentTestEnvironment,
    /** Typed session RPCs and waits. */
    public readonly sessions: AgentSessionTestClient,
  ) {}

  /** Creates a fresh state directory, Flow, and Client, plus an in-process Worker when asked. */
  public static async start(
    engine: PrivateDexEngine,
    isWorkerInProcess: boolean,
    cli?: (stateDirectory: string) => ClaudeTestAgentCli,
  ): Promise<ClaudeAgentTestHarness> {
    const stateDirectory = mkdtempSync(join(engine.stateDirectory, "claude-agent-"));
    const flow = createClaudeTestAgentFlow(stateDirectory, cli?.(stateDirectory));
    const environment = await AgentTestEnvironment.start({
      serverAddress: engine.serverAddress,
      flows: [flow],
      isWorkerInProcess,
    });
    return new ClaudeAgentTestHarness(
      engine,
      stateDirectory,
      flow,
      environment,
      new AgentSessionTestClient(environment.client, flow),
    );
  }

  /** Every invocation the fake CLI and the test tools recorded, in order. */
  public get invocations(): InvocationLog {
    return new InvocationLog(join(this.stateDirectory, fakeClaudeInvocationLogName));
  }

  public gate(name: string): FileGate {
    return new FileGate(this.stateDirectory, name);
  }

  public newSessionId(prefix: string): string {
    return `${prefix}-${randomUUID()}`;
  }

  /** Starts a Worker child that a test can kill with SIGKILL. */
  public async startWorker(): Promise<KillableWorkerProcess> {
    const worker = await KillableWorkerProcess.start({
      entryModulePath: workerEntryModulePath,
      serverAddress: this.engine.serverAddress,
      logPath: join(this.stateDirectory, "worker.log"),
      environment: { [fakeClaudeStateDirectoryVariable]: this.stateDirectory },
    });
    this.workers.push(worker);
    return worker;
  }

  /** Waits until the session finished `turnCount` turns and is idle, then returns its snapshot. */
  public async waitForTurns(sessionId: string, turnCount: number): Promise<AgentSnapshot> {
    await this.sessions.waitForCompletedTurns(sessionId, turnCount, { timeoutMs: turnTimeoutMs });
    return this.sessions.getAgentSnapshot(sessionId);
  }

  /** Waits for a pending approval of `toolName` other than the already handled interrupts. */
  public waitForNewInterrupt(
    sessionId: string,
    toolName: string,
    handledInterruptIds: readonly string[] = [],
  ): Promise<AgentInterrupt> {
    return pollUntil(
      async () =>
        (await this.sessions.readPendingInterrupts(sessionId)).find(
          (interrupt) => interrupt.toolName === toolName && !handledInterruptIds.includes(interrupt.interruptId),
        ),
      { description: `a new ${toolName} interrupt in session ${sessionId}`, timeoutMs: turnTimeoutMs },
    );
  }

  public listEvents(sessionId: string): Promise<AgentEvent[]> {
    return this.sessions.listAgentEvents(sessionId);
  }

  /** Waits until the session's event Stream holds an event of `eventType`. */
  public async waitForEvent(sessionId: string, eventType: string): Promise<void> {
    await pollUntil(
      async () => ((await this.listEvents(sessionId)).some((event) => event.type === eventType) ? true : undefined),
      { description: `${eventType} event in session ${sessionId}`, timeoutMs: turnTimeoutMs },
    );
  }

  /** Returns the concatenated streamed text of each attempt, in attempt order. */
  public async textByAttempt(sessionId: string): Promise<[number, string][]> {
    const textByAttempt = new Map<number, string>();
    for (const event of await this.listEvents(sessionId)) {
      if (event.type === "text_delta") {
        const text = (JSON.parse(event.payload) as { readonly text: string }).text;
        textByAttempt.set(event.attempt, (textByAttempt.get(event.attempt) ?? "") + text);
      }
    }
    return [...textByAttempt.entries()].sort(([left], [right]) => left - right);
  }

  /** Kills every Worker child, then closes the Client and the in-process Worker. */
  public async close(): Promise<void> {
    for (const worker of this.workers.splice(0)) {
      await worker.kill();
    }
    await this.environment.close();
  }
}
