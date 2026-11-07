// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { setTimeout as delay } from "node:timers/promises";

import { FlowNotFoundError, type AttributeRead, type Client } from "@superdurable/dex";

import type { AgentFrameworkRunner, DeferredToolOutcome } from "../agent-framework-runner.js";
import { readSessionFailure } from "../agent-session-failure.js";
import type { AgentSessionFlow } from "../agent-session-flow.js";
import { messageSequenceKey } from "../agent-session-persistence.js";
import { findMessageChunkRange } from "../agent-session-transitions.js";
import type {
  AgentMessage,
  AgentMessageChunk,
  AgentMessageIndex,
  AgentState,
  AgentToolCallRecord,
} from "../agent-session-values.js";
import { parseTurnNumber } from "./agent-event-stream-positions.js";

const committedRecordWaitMs = 3_000;
const committedRecordPollIntervalMs = 25;
const storedMessagePageSize = 50;
const recentStoredMessageCount = 200;
const maximumStoredMessageReadAttempts = 3;

/** Reads committed session state for the UI streams; Attribute reads need no running Worker. */
export class AgentSessionRecordReader {
  public constructor(
    private readonly client: Client,
    private readonly flow: AgentSessionFlow<AgentFrameworkRunner<DeferredToolOutcome>>,
    private readonly flowId: string,
  ) {}

  public async readAgentState(): Promise<AgentState | undefined> {
    const [state] = await this.client.getAttributes(this.flowId, this.flow.agentState);
    return state;
  }

  /**
   * Waits until a turn's end committed, because its end event reaches the Stream before the commit.
   * @returns `true` once the turn is no longer active; `false` after 3 seconds without that commit.
   */
  public async waitForTurnToEnd(turnId: string): Promise<boolean> {
    const deadline = Date.now() + committedRecordWaitMs;
    for (;;) {
      const state = await this.readAgentState();
      if (state?.activeTurnId !== turnId) {
        return true;
      }
      if (Date.now() >= deadline) {
        return false;
      }
      await delay(committedRecordPollIntervalMs);
    }
  }

  /**
   * Reads the IDs of the tool calls whose approval `resolve_interrupt` still accepts.
   * @returns The tool call IDs of the pending `tool_approval` interrupts.
   */
  public async readPendingApprovalToolCallIds(): Promise<ReadonlySet<string>> {
    const [interrupts] = await this.client.getAttributes(this.flowId, this.flow.pendingInterrupts);
    return new Set(
      (interrupts ?? [])
        .filter((interrupt) => interrupt.kind === "tool_approval")
        .map((interrupt) => interrupt.toolCallId),
    );
  }

  public async isFlowRunning(): Promise<boolean> {
    try {
      return (await this.client.describeFlow(this.flowId)).status === "running";
    } catch (error: unknown) {
      if (error instanceof FlowNotFoundError) {
        return false;
      }
      throw error;
    }
  }

  /** Returns why the session's Flow failed, or `undefined` when it did not. */
  public readFlowFailure(): Promise<string | undefined> {
    return readSessionFailure(this.client, this.flowId);
  }

  /**
   * Reads what the session holds about one tool call of a turn.
   *
   * Events reach the Stream before their Step commits, so it polls the record until `isReady` holds, for
   * up to 3 seconds. A turn that ended deleted its records; then it reads the turn's stored messages.
   * @returns The record when it exists, plus the tool name, arguments, and stored result text from the
   * record or the stored messages; each is `undefined` when neither holds it.
   */
  public async readToolCallDetails(
    turnId: string,
    toolCallId: string,
    isReady: (toolCall: AgentToolCallRecord) => boolean,
  ): Promise<ToolCallDetails> {
    const record = await this.waitForToolCall(turnId, toolCallId, isReady);
    if (record !== undefined) {
      return { record, toolName: record.toolName, toolArguments: record.arguments, storedToolOutput: undefined };
    }
    const storedMessages = await this.readStoredToolMessages(turnId, toolCallId);
    return {
      record: undefined,
      toolName: storedMessages.toolCall?.toolName ?? storedMessages.toolResult?.toolName ?? undefined,
      toolArguments: storedMessages.toolCall?.toolArguments ?? undefined,
      storedToolOutput: storedMessages.toolResult?.toolOutput ?? undefined,
    };
  }

  private async waitForToolCall(
    turnId: string,
    toolCallId: string,
    isReady: (toolCall: AgentToolCallRecord) => boolean,
  ): Promise<AgentToolCallRecord | undefined> {
    const deadline = Date.now() + committedRecordWaitMs;
    for (;;) {
      const [record] = await this.client.getAttributes(
        this.flowId,
        this.flow.agentToolCalls.instance(toolCallId),
      );
      const turnRecord = record?.turnId === turnId ? record : undefined;
      if ((turnRecord !== undefined && isReady(turnRecord)) || Date.now() >= deadline) {
        return turnRecord;
      }
      if (turnRecord === undefined && (await this.readAgentState())?.activeTurnId !== turnId) {
        return undefined;
      }
      await delay(committedRecordPollIntervalMs);
    }
  }

  /**
   * Waits up to 3 seconds for a tool call record to be committed, because events precede their commit.
   * @returns The record, or `undefined` when it never appeared or the turn is no longer active.
   */
  public async readCommittedToolCall(turnId: string, toolCallId: string): Promise<AgentToolCallRecord | undefined> {
    return this.waitForToolCall(turnId, toolCallId, () => true);
  }

  /**
   * Reads one tool call record once, without waiting for a commit.
   * @returns The record when it exists and belongs to `turnId`, otherwise `undefined`.
   */
  public async readToolCall(turnId: string, toolCallId: string): Promise<AgentToolCallRecord | undefined> {
    const [record] = await this.client.getAttributes(this.flowId, this.flow.agentToolCalls.instance(toolCallId));
    return record?.turnId === turnId ? record : undefined;
  }

  /**
   * Reads the stored tool results among the last 200 stored messages of any turn.
   * @returns The results in sequence order, each with its turn, tool call ID, and the text the model saw.
   */
  public async readRecentStoredToolResults(): Promise<readonly StoredToolResult[]> {
    const state = await this.readAgentState();
    const endSequence = state?.nextMessageSequence ?? 1;
    const startSequence = Math.max(1, endSequence - recentStoredMessageCount);
    if (startSequence >= endSequence) {
      return [];
    }
    const results: StoredToolResult[] = [];
    for (const message of (await this.readStoredMessages(startSequence, endSequence)).messages) {
      const toolOutput = message?.toolOutput ?? message?.text;
      if (message?.role === "tool" && message.toolCallId !== null && toolOutput != null) {
        results.push({ turnId: message.turnId, toolCallId: message.toolCallId, toolOutput });
      }
    }
    return results;
  }

  /** Finds a tool call's stored request and result messages, newest pages first, within its turn. */
  private async readStoredToolMessages(
    turnId: string,
    toolCallId: string,
  ): Promise<{ toolCall?: AgentMessage; toolResult?: AgentMessage }> {
    const state = await this.readAgentState();
    const turnNumber = parseTurnNumber(turnId);
    const found: { toolCall?: AgentMessage; toolResult?: AgentMessage } = {};
    let endSequence = state?.nextMessageSequence ?? 1;
    let firstSequence = 1;
    while (endSequence > firstSequence) {
      const startSequence = Math.max(firstSequence, endSequence - storedMessagePageSize);
      const page = await this.readStoredMessages(startSequence, endSequence);
      const messages = page.messages;
      firstSequence = page.firstSequence;
      let hasEarlierTurn = false;
      for (const message of messages) {
        if (message === undefined) {
          continue;
        }
        if (message.turnId !== turnId) {
          hasEarlierTurn ||= (parseTurnNumber(message.turnId) ?? 0) < (turnNumber ?? 0);
          continue;
        }
        if (message.toolCallId === toolCallId) {
          if (message.role === "tool") {
            found.toolResult = message;
          } else {
            found.toolCall = message;
          }
        }
      }
      if (hasEarlierTurn || (found.toolCall !== undefined && found.toolResult !== undefined)) {
        return found;
      }
      endSequence = startSequence;
    }
    return found;
  }

  /**
   * Reads stored messages with sequences in `[startSequence, endSequence)`, in sequence order, from the
   * `AgentMessages` window and the `AgentMessageChunks` instances that `AgentMessageIndex` names. A message
   * that retention deleted reads as `undefined`. A commit between the index read and the instance reads moves
   * messages, so the read repeats with the new index.
   * @returns The messages and the sequence of the oldest stored message.
   * @throws Error when the index keeps changing for every attempt, or a stored message is missing.
   */
  private async readStoredMessages(startSequence: number, endSequence: number): Promise<StoredMessagePage> {
    let [index] = await this.client.getAttributes(this.flowId, this.flow.agentMessageIndex);
    for (let attempt = 1; ; attempt += 1) {
      const firstSequence = index?.firstSequence ?? 1;
      const chunkStarts = new Set<number>();
      const windowSequences: number[] = [];
      for (let sequence = Math.max(firstSequence, startSequence); sequence < endSequence; sequence += 1) {
        const range = index === undefined ? undefined : findMessageChunkRange(index, sequence);
        if (range === undefined) {
          windowSequences.push(sequence);
        } else {
          chunkStarts.add(range.firstSequence);
        }
      }
      const chunkReads = [...chunkStarts].map((chunkStart) =>
        this.flow.agentMessageChunks.instance(messageSequenceKey(chunkStart)),
      );
      const windowReads = windowSequences.map((sequence) =>
        this.flow.agentMessages.instance(messageSequenceKey(sequence)),
      );
      const values = (await this.client.getAttributes(
        this.flowId,
        ...([this.flow.agentMessageIndex, ...chunkReads, ...windowReads] as [
          AttributeRead<unknown>,
          ...AttributeRead<unknown>[],
        ]),
      )) as readonly unknown[];
      const readIndex = values[0] as AgentMessageIndex | undefined;
      // A rollover or retention between the two reads moved messages, so read again with the new index.
      if (!isSameMessageLayout(index, readIndex)) {
        if (attempt >= maximumStoredMessageReadAttempts) {
          throw new Error(
            `the stored messages of session ${this.flowId} moved during ${maximumStoredMessageReadAttempts} reads`,
          );
        }
        index = readIndex;
        continue;
      }
      const bySequence = new Map<number, AgentMessage>();
      for (const value of values.slice(1, 1 + chunkReads.length)) {
        for (const message of (value as AgentMessageChunk | undefined)?.messages ?? []) {
          bySequence.set(message.sequence, message);
        }
      }
      for (const [offset, value] of values.slice(1 + chunkReads.length).entries()) {
        const sequence = windowSequences[offset];
        if (sequence !== undefined && value !== undefined) {
          bySequence.set(sequence, value as AgentMessage);
        }
      }
      const messages: (AgentMessage | undefined)[] = [];
      for (let sequence = startSequence; sequence < endSequence; sequence += 1) {
        const message = bySequence.get(sequence);
        if (message === undefined && sequence >= firstSequence) {
          throw new Error(`session ${this.flowId} has no stored message ${sequence}`);
        }
        messages.push(message);
      }
      return { messages, firstSequence };
    }
  }
}

interface StoredMessagePage {
  readonly messages: readonly (AgentMessage | undefined)[];
  readonly firstSequence: number;
}

function isSameMessageLayout(left: AgentMessageIndex | undefined, right: AgentMessageIndex | undefined): boolean {
  return (
    left?.firstSequence === right?.firstSequence &&
    left?.windowFirstSequence === right?.windowFirstSequence &&
    left?.chunks.length === right?.chunks.length
  );
}

/** What the session holds about one tool call; see {@link AgentSessionRecordReader.readToolCallDetails}. */
export interface ToolCallDetails {
  readonly record: AgentToolCallRecord | undefined;
  readonly toolName: string | undefined;
  readonly toolArguments: string | undefined;
  readonly storedToolOutput: string | undefined;
}

/** One stored tool result; see {@link AgentSessionRecordReader.readRecentStoredToolResults}. */
export interface StoredToolResult {
  readonly turnId: string;
  readonly toolCallId: string;
  readonly toolOutput: string;
}
