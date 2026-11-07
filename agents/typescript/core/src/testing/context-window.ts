// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { appendFileSync, existsSync, readFileSync } from "node:fs";
import { join } from "node:path";

import {
  contextSummaryInstructions,
  contextSummaryNoteHeader,
  type CompactionPolicy,
} from "../agent-context-compaction.js";
import type { AgentMessage, AgentSnapshot } from "../agent-session-values.js";
import { AgentSessionTestClient } from "./agent-session-test-client.js";
import { KillableWorkerProcess } from "./agent-worker-process.js";
import { FileGate } from "./coordination.js";
import { pollUntil } from "./polling.js";
import { executeCompletedEvent, readStepHistory } from "./step-history.js";

/** Environment variable holding the JSON of {@link ContextWindowSettings}. */
export const contextWindowSettingsVariable = "DEX_AGENTS_CONTEXT_WINDOW_SETTINGS";
/** Tool that the scripted model calls once per turn. */
export const contextWindowToolName = "lookup";
/** Start of the error that the scripted model throws for an oversized request. */
export const contextWindowExceededError = "scripted model: input exceeds the context window";
/** Gate that holds a summary call of a range that names `gate-summary`. */
export const contextWindowSummaryGateName = "summary";

const requestLogName = "model-requests.jsonl";
const summaryGateTag = "gate-summary";
const turnCommandPattern = /turn ([A-Za-z0-9-]+)/gu;
const providerCharactersPerToken = 4;
const summaryTagLimit = 10;

/**
 * Sizes of the scripted model and the session of one context window test. A Worker child process reads them from
 * {@link contextWindowSettingsVariable}.
 */
export interface ContextWindowSettings {
  /** Context window of the scripted model, at four characters per token. */
  readonly windowTokens: number;
  /** Whether the session gets a compaction policy. */
  readonly isCompactionEnabled: boolean;
  /** Size of each tool result in characters. */
  readonly toolOutputCharacters: number;
  /** Size of each model answer in characters. */
  readonly answerCharacters: number;
  /** `AgentSessionOptions.messageWindowSize`. */
  readonly messageWindowSize: number;
  /** `AgentSessionOptions.messageChunkSize`. */
  readonly messageChunkSize: number;
  /** `AgentSessionOptions.retainedMessageLimit`; `null` keeps every message. */
  readonly retainedMessageLimit: number | null;
}

/** The settings of a test that changes nothing. */
export const defaultContextWindowSettings: ContextWindowSettings = Object.freeze({
  windowTokens: 6000,
  isCompactionEnabled: true,
  toolOutputCharacters: 2000,
  answerCharacters: 1000,
  messageWindowSize: 8,
  messageChunkSize: 4,
  retainedMessageLimit: 1000,
});

/**
 * Returns the Worker environment that carries the settings.
 * @param settings - The test's settings.
 * @returns One entry under {@link contextWindowSettingsVariable}.
 */
export function contextWindowSettingsEnvironment(settings: ContextWindowSettings): Record<string, string> {
  return { [contextWindowSettingsVariable]: JSON.stringify(settings) };
}

/**
 * Reads the settings a test gave this Worker.
 * @returns The test's settings, or the defaults outside a Worker child.
 */
export function readContextWindowSettings(): ContextWindowSettings {
  const value = process.env[contextWindowSettingsVariable];
  return value === undefined ? defaultContextWindowSettings : (JSON.parse(value) as ContextWindowSettings);
}

/**
 * Returns the `AgentSessionOptions` fields for the settings.
 * @param settings - The test's settings.
 * @returns The compaction policy and the message storage options.
 */
export function contextWindowSessionOptions(settings: ContextWindowSettings): {
  readonly compaction?: CompactionPolicy;
  readonly messageWindowSize: number;
  readonly messageChunkSize: number;
  readonly retainedMessageLimit: number | null;
} {
  return {
    ...(settings.isCompactionEnabled ? { compaction: { contextWindowTokens: settings.windowTokens } } : {}),
    messageWindowSize: settings.messageWindowSize,
    messageChunkSize: settings.messageChunkSize,
    retainedMessageLimit: settings.retainedMessageLimit,
  };
}

/** What the scripted model does for one request: call the tool with a tag, or answer with a text. */
export type ContextWindowReply =
  | { readonly kind: "tool_call"; readonly tag: string }
  | { readonly kind: "text"; readonly text: string };

/** One request the scripted model saw. */
export interface ContextWindowRequest {
  /** `"model"` for an agent's request, `"summary"` for a summary call. */
  readonly kind: "model" | "summary";
  /** Characters of the request, instructions included. */
  readonly characters: number;
  /** The whole request as text, in the framework's format. */
  readonly input: string;
}

/**
 * Answers model requests of a framework adapter and logs every request, like a provider with a small context
 * window. Each user message is `turn <tag>`: the model calls {@link contextWindowToolName} once with the tag, then
 * answers {@link answerText}. It holds no state between calls, so it survives a Worker restart.
 */
export class ContextWindowModelScript {
  /**
   * @param stateDirectory - Directory shared with the test.
   * @param settings - Sizes of the test.
   */
  public constructor(
    private readonly stateDirectory: string,
    private readonly settings: ContextWindowSettings,
  ) {}

  /**
   * Returns what the tool answers.
   * @param tag - The tool call's `tag` argument.
   * @returns Text of `toolOutputCharacters` that names the tag.
   */
  public toolOutput(tag: string): string {
    return `lookup-body-${tag}:${"y".repeat(this.settings.toolOutputCharacters)}`;
  }

  /**
   * Returns the answer of the turn with this tag.
   * @param tag - The tag of the user message.
   * @returns Text of `answerCharacters` that names the tag.
   */
  public answerText(tag: string): string {
    return `answer-body-${tag}:${"x".repeat(this.settings.answerCharacters)}`;
  }

  /**
   * Answers one model request.
   * @param request - The system prompt, the whole request as text, and whether the running turn has its result.
   * @returns The tool call or the answer.
   * @throws {Error} When the request exceeds the window or names no turn.
   */
  public async reply(request: {
    readonly instructions: string | undefined;
    readonly inputText: string;
    readonly hasToolResult: boolean;
  }): Promise<ContextWindowReply> {
    const instructions = request.instructions ?? "";
    const isSummary = instructions.includes(contextSummaryInstructions);
    const characters = request.inputText.length + instructions.length;
    const entry: ContextWindowRequest = { kind: isSummary ? "summary" : "model", characters, input: request.inputText };
    appendFileSync(join(this.stateDirectory, requestLogName), `${JSON.stringify(entry)}\n`);
    const windowCharacters = this.settings.windowTokens * providerCharactersPerToken;
    if (characters > windowCharacters) {
      throw new Error(`${contextWindowExceededError}: ${characters} characters, limit ${windowCharacters}`);
    }
    const tags = [...request.inputText.matchAll(turnCommandPattern)].map((match) => match[1] ?? "");
    if (isSummary) {
      if (tags.includes(summaryGateTag)) {
        const gate = new FileGate(this.stateDirectory, contextWindowSummaryGateName);
        gate.markReached();
        await gate.waitForRelease();
      }
      return { kind: "text", text: `summary of turns: ${[...new Set(tags)].slice(-summaryTagLimit).join(" ")}` };
    }
    const tag = tags.at(-1);
    if (tag === undefined) {
      throw new Error("the scripted model needs a turn command");
    }
    return request.hasToolResult ? { kind: "text", text: this.answerText(tag) } : { kind: "tool_call", tag };
  }
}

/**
 * Returns every request the scripted model saw, in order.
 * @param stateDirectory - The test's state directory.
 * @returns The logged requests.
 */
export function readContextWindowRequests(stateDirectory: string): ContextWindowRequest[] {
  const path = join(stateDirectory, requestLogName);
  if (!existsSync(path)) {
    return [];
  }
  return readFileSync(path, "utf8")
    .split("\n")
    .filter((line) => line !== "")
    .map((line) => JSON.parse(line) as ContextWindowRequest);
}

/** Options of {@link ContextWindowSessionScenario}. */
export interface ContextWindowSessionScenarioOptions {
  /** Dex Server address of the test's engine. */
  readonly serverAddress: string;
  /** Session client for the adapter's Flow. */
  readonly sessions: AgentSessionTestClient;
  /** Directory shared with the Worker child processes. */
  readonly stateDirectory: string;
  /** Sizes of the test. */
  readonly settings: ContextWindowSettings;
  /** Compiled module of the Worker child, which calls `runAgentWorkerProcess`. */
  readonly workerEntryModulePath: string;
  /** Environment that tells the Worker child where {@link stateDirectory} is. */
  readonly workerEnvironment: Readonly<Record<string, string>>;
}

/**
 * Drives an adapter's session past a context window in Worker child processes, and checks what happened. Every
 * adapter so proves one contract: the session keeps going, the model input shrinks, and the record stays readable.
 */
export class ContextWindowSessionScenario {
  /** The session ID of this scenario. */
  public readonly sessionId = `session-${randomUUID()}`;
  private readonly workers: KillableWorkerProcess[] = [];

  /** @param options - The engine, the session client, and the Worker child. */
  public constructor(private readonly options: ContextWindowSessionScenarioOptions) {}

  /**
   * Starts a Worker child with the scenario's settings, or with other settings.
   * @param settings - Settings of this Worker. Defaults to the scenario's.
   * @returns The Worker.
   */
  public async startWorker(settings = this.options.settings): Promise<KillableWorkerProcess> {
    const worker = await KillableWorkerProcess.start({
      entryModulePath: this.options.workerEntryModulePath,
      serverAddress: this.options.serverAddress,
      logPath: join(this.options.stateDirectory, "worker.log"),
      environment: { ...this.options.workerEnvironment, ...contextWindowSettingsEnvironment(settings) },
    });
    this.workers.push(worker);
    return worker;
  }

  /** Kills every Worker child the scenario started. */
  public async close(): Promise<void> {
    for (const worker of this.workers.splice(0)) {
      await worker.kill();
    }
  }

  /**
   * Returns the logged requests of one kind.
   * @param kind - `"model"` or `"summary"`.
   * @returns The requests in order.
   */
  public requests(kind: ContextWindowRequest["kind"]): ContextWindowRequest[] {
    return readContextWindowRequests(this.options.stateDirectory).filter((request) => request.kind === kind);
  }

  /**
   * Sends one turn per tag and waits until the session finished them.
   * @param tags - The turns' tags.
   * @param isFirst - Whether the first tag starts the session.
   * @returns The snapshot after the last turn.
   */
  public async runTurns(tags: readonly string[], isFirst = true): Promise<AgentSnapshot> {
    const { sessions } = this.options;
    let commands = tags.map((tag) => `turn ${tag}`);
    let before = 0;
    if (isFirst) {
      await sessions.startSession(this.sessionId, commands[0] ?? "");
      commands = commands.slice(1);
    } else {
      before = (await sessions.readAgentState(this.sessionId))?.turnCount ?? 0;
    }
    for (const command of commands) {
      assert.equal(await sessions.sendUserMessage(this.sessionId, command), true);
    }
    await sessions.waitForCompletedTurns(this.sessionId, before + tags.length, { timeoutMs: 180_000 });
    return sessions.getAgentSnapshot(this.sessionId);
  }

  /**
   * Reads every message history still holds, through the public RPC.
   * @returns The messages in sequence order.
   */
  public async readStoredMessages(): Promise<AgentMessage[]> {
    const messages: AgentMessage[] = [];
    let beforeSequence: number | null = null;
    for (;;) {
      const page = await this.options.sessions.listAgentMessages(this.sessionId, beforeSequence, 200);
      messages.unshift(...page.messages);
      if (page.nextBeforeSequence === null) {
        return messages;
      }
      beforeSequence = page.nextBeforeSequence;
    }
  }

  /**
   * Counts the finished `CompactContext` Steps of the session.
   * @returns The count.
   */
  public async countCompactContextSteps(): Promise<number> {
    const history = await readStepHistory(this.options.serverAddress, this.sessionId);
    return history.filter((entry) => entry.stepType === "CompactContext" && entry.eventType === executeCompletedEvent)
      .length;
  }

  /**
   * Checks the whole contract on a session longer than the window.
   * @param turnCount - Turns to run.
   * @returns The final snapshot.
   */
  public async assertOutlivesTheWindow(turnCount = 12): Promise<AgentSnapshot> {
    await this.startWorker();
    const snapshot = await this.runTurns(numberedTags(turnCount));

    assert.equal(snapshot.state?.lastTurnError ?? null, null);
    assert.equal(snapshot.state?.status, "waiting_for_user_message");
    assert.equal(snapshot.state?.turnCount, turnCount);
    const summary = snapshot.contextSummary;
    assert.ok(summary);
    assert.match(summary.text, /^summary of turns:/u);
    assert.ok((await this.countCompactContextSteps()) >= 1);

    const limit = this.options.settings.windowTokens * providerCharactersPerToken;
    const modelRequests = this.requests("model");
    assert.ok(modelRequests.every((request) => request.characters <= limit));
    const allRequests = readContextWindowRequests(this.options.stateDirectory);
    allRequests.forEach((request, index) => {
      if (request.kind === "summary") {
        assert.ok((allRequests[index + 1]?.characters ?? 0) < (allRequests[index - 1]?.characters ?? 0));
      }
    });
    const lastInput = modelRequests.at(-1)?.input ?? "";
    assert.ok(lastInput.includes(contextSummaryNoteHeader));
    assert.ok(lastInput.includes(summary.text.split("\n")[0] ?? ""));
    // Turn 1 is behind the summary, so its bodies left the model input.
    assert.ok(!lastInput.includes("answer-body-t1:"));
    assert.ok(!lastInput.includes("lookup-body-t1:"));

    const messages = await this.readStoredMessages();
    assert.deepEqual(
      messages.map((message) => message.sequence),
      messages.map((_, index) => index + 1),
    );
    assert.equal(messages[0]?.text, "turn t1");
    assert.ok(!messages.some((message) => message.frameworkItem.includes(contextSummaryNoteHeader)));
    assert.ok(messages.some((message) => (message.text ?? "").includes("answer-body-t1:")));
    const summaryCalls = this.requests("summary").length;
    assert.equal(summaryCalls, await this.countCompactContextSteps());
    assert.equal(snapshot.usage.modelCalls, modelRequests.length + summaryCalls);
    return snapshot;
  }

  /**
   * Checks that retention deletes only chunks behind the summary. The settings need a small `retainedMessageLimit`.
   * @param turnCount - Turns to run.
   */
  public async assertRetentionFollowsTheSummary(turnCount = 24): Promise<void> {
    await this.startWorker();
    const snapshot = await this.runTurns(numberedTags(turnCount));
    assert.equal(snapshot.state?.lastTurnError ?? null, null);
    const summary = snapshot.contextSummary;
    assert.ok(summary);
    const page = await this.options.sessions.listAgentMessages(this.sessionId, null, 200);
    const deletedBefore = page.deletedBeforeSequence ?? null;
    assert.ok(deletedBefore !== null && deletedBefore > 1);
    assert.ok(deletedBefore - 1 <= summary.lastSequence);
    const messages = await this.readStoredMessages();
    assert.equal(messages[0]?.sequence, deletedBefore);
    assert.deepEqual(
      messages.map((message) => message.sequence),
      messages.map((_, index) => deletedBefore + index),
    );
    const limit = this.options.settings.retainedMessageLimit;
    assert.ok(limit !== null && messages.length >= limit);
    assert.ok((messages.at(-1)?.text ?? "").startsWith(`answer-body-t${turnCount}:`));
  }

  /**
   * Checks that a session without a policy is unchanged and reaches the window.
   * @param turnCount - Turns to run; the window must end the session first.
   */
  public async assertFailsAtTheWindowWithoutAPolicy(turnCount = 12): Promise<void> {
    await this.startWorker();
    const snapshot = await this.runTurns(numberedTags(turnCount));
    assert.ok((snapshot.state?.lastTurnError ?? "").includes(contextWindowExceededError));
    assert.equal(snapshot.contextSummary ?? null, null);
    assert.equal(await this.countCompactContextSteps(), 0);
    assert.equal(this.requests("summary").length, 0);
    assert.ok(!this.requests("model").some((request) => request.input.includes(contextSummaryNoteHeader)));
  }

  /** Checks that a session started without a policy compacts once a Worker has one. */
  public async assertStartsCompactingAfterAnUpgrade(): Promise<void> {
    const worker = await this.startWorker({ ...this.options.settings, isCompactionEnabled: false });
    await this.runTurns(["t1", "t2", "t3"]);
    assert.equal(this.requests("summary").length, 0);
    await worker.kill();

    await this.startWorker();
    const snapshot = await this.runTurns(numberedTags(12).slice(3), false);
    assert.equal(snapshot.state?.lastTurnError ?? null, null);
    assert.ok(snapshot.contextSummary);
    assert.equal((await this.readStoredMessages())[0]?.text, "turn t1");
  }

  /** Checks SIGKILL during `CompactContext`: one more summary call, one stored summary, no repeated model call. */
  public async assertWorkerKillRerunsOnlyTheSummary(): Promise<void> {
    const { sessions, stateDirectory } = this.options;
    const worker = await this.startWorker();
    const commands = ["gate-summary", ...numberedTags(12).slice(1)].map((tag) => `turn ${tag}`);
    await sessions.startSession(this.sessionId, commands[0] ?? "");
    for (const command of commands.slice(1)) {
      assert.equal(await sessions.sendUserMessage(this.sessionId, command), true);
    }
    const gate = new FileGate(stateDirectory, contextWindowSummaryGateName);
    await gate.waitUntilReached();
    const compacting = await pollUntil(async () => {
      const snapshot = await sessions.getAgentSnapshot(this.sessionId);
      return snapshot.state?.status === "compacting_context" ? snapshot : undefined;
    }, { description: "the session compacting" });
    assert.equal(compacting.contextSummary ?? null, null);
    const modelCallsBeforeKill = this.requests("model").length;

    await worker.kill();
    gate.release();
    await this.startWorker();
    await sessions.waitForCompletedTurns(this.sessionId, commands.length, { timeoutMs: 180_000 });
    const snapshot = await sessions.getAgentSnapshot(this.sessionId);

    assert.equal(gate.reachedCount(), 2);
    assert.equal(snapshot.state?.lastTurnError ?? null, null);
    assert.ok(snapshot.contextSummary);
    const modelRequests = this.requests("model");
    // The model call that started compaction never ran, so the kill repeats no model call.
    assert.ok(modelCallsBeforeKill < modelRequests.length);
    const summaryCalls = this.requests("summary").length;
    assert.equal(await this.countCompactContextSteps(), summaryCalls - 1);
    assert.equal(snapshot.usage.modelCalls, modelRequests.length + summaryCalls - 1);
  }
}

function numberedTags(count: number): string[] {
  return Array.from({ length: count }, (_, index) => `t${index + 1}`);
}
