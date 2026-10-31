// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { setTimeout as delay } from "node:timers/promises";

import { RpcLockConflictError, type Client } from "@superdurable/dex";

import type { AgentSessionFlow } from "../agent-session-flow.js";
import type {
  AgentEvent,
  AgentInterrupt,
  AgentMessagesPage,
  AgentSnapshot,
  AgentState,
  AgentToolCall,
} from "../agent-session-values.js";
import { pollUntil } from "./polling.js";

const lockConflictRetryTimeoutMs = 30_000;
const lockConflictRetryIntervalMs = 50;
const streamPageSize = 500;

/** Options of the `waitFor*` methods of {@link AgentSessionTestClient}. */
export interface AgentSessionWaitOptions {
  /** Total milliseconds to wait. Defaults to 60,000. */
  readonly timeoutMs?: number;
}

/**
 * Typed Client calls for one agent session Flow, with waits suited to integration tests.
 *
 * RPC methods need a running Worker. The `read*` and `waitFor*` methods use `Client.getAttributes`,
 * which reads committed Attributes without a Worker, so they also work while a killed Worker is down.
 *
 * @example
 * ```ts
 * const sessions = new AgentSessionTestClient(environment.client, flow);
 * await sessions.startSession(sessionId, "send the report");
 * const interrupt = await sessions.waitForPendingInterrupt(sessionId, "send_email");
 * assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
 * const state = await sessions.waitForCompletedTurns(sessionId, 1);
 * const snapshot = await sessions.getAgentSnapshot(sessionId);
 * ```
 */
export class AgentSessionTestClient {
  /**
   * Creates the helper.
   * @param client - Client whose Registry contains `flow`.
   * @param flow - The agent session Flow, the same object registered in the Client's Registry.
   */
  public constructor(
    /** Client used for every call. */
    public readonly client: Client,
    /** Flow whose sessions this helper drives. */
    public readonly flow: AgentSessionFlow,
  ) {}

  /**
   * Starts a session.
   * @param sessionId - Flow ID of the new session.
   * @param initialMessage - First user message, or `null` to start idle. Defaults to `null`.
   * @returns A Promise that resolves after Dex accepted the start.
   */
  public async startSession(sessionId: string, initialMessage: string | null = null): Promise<void> {
    await this.client.startFlow(this.flow, sessionId, { initialMessage });
  }

  /**
   * Calls `send_user_message`.
   * @param sessionId - Session Flow ID.
   * @param text - Message text.
   * @returns The RPC result: `false` for blank text.
   */
  public sendUserMessage(sessionId: string, text: string): Promise<boolean> {
    return this.client.invokeRPC(this.flow.sendUserMessage, sessionId, { text });
  }

  /**
   * Calls `resolve_interrupt`, retrying an `RpcLockConflictError` for up to 30 seconds.
   * @param sessionId - Session Flow ID.
   * @param interruptId - Interrupt to resolve.
   * @param approved - Whether the tool call may run.
   * @param rejectionMessage - Text shown to the model on rejection. Defaults to `null`.
   * @returns The RPC result: `false` when the interrupt was not pending.
   */
  public async resolveInterrupt(
    sessionId: string,
    interruptId: string,
    approved: boolean,
    rejectionMessage: string | null = null,
  ): Promise<boolean> {
    const deadline = Date.now() + lockConflictRetryTimeoutMs;
    for (;;) {
      try {
        return await this.client.invokeRPC(this.flow.resolveInterrupt, sessionId, {
          interruptId,
          approved,
          rejectionMessage,
        });
      } catch (error: unknown) {
        if (!(error instanceof RpcLockConflictError) || Date.now() >= deadline) {
          throw error;
        }
      }
      await delay(lockConflictRetryIntervalMs);
    }
  }

  /**
   * Calls `get_agent_snapshot`.
   * @param sessionId - Session Flow ID.
   * @returns The snapshot.
   */
  public getAgentSnapshot(sessionId: string): Promise<AgentSnapshot> {
    return this.client.invokeRPC(this.flow.getAgentSnapshot, sessionId);
  }

  /**
   * Calls `list_agent_messages`.
   * @param sessionId - Session Flow ID.
   * @param beforeSequence - Exclusive upper bound, or `null` for the newest page.
   * @param limit - Page size, clamped by the session to 1..200.
   * @returns The page.
   */
  public listAgentMessages(
    sessionId: string,
    beforeSequence: number | null,
    limit: number,
  ): Promise<AgentMessagesPage> {
    return this.client.invokeRPC(this.flow.listAgentMessages, sessionId, { beforeSequence, limit });
  }

  /**
   * Reads the committed `AgentState` without a Worker.
   * @param sessionId - Session Flow ID.
   * @returns The state, or `undefined` before `StartAgentSession` committed.
   */
  public async readAgentState(sessionId: string): Promise<AgentState | undefined> {
    const [state] = await this.client.getAttributes(sessionId, this.flow.persistence.agentState);
    return state;
  }

  /**
   * Reads one committed tool call record without a Worker.
   * @param sessionId - Session Flow ID.
   * @param toolCallId - Tool call ID.
   * @returns The record, or `undefined` when it does not exist or its turn completed.
   */
  public async readToolCall(sessionId: string, toolCallId: string): Promise<AgentToolCall | undefined> {
    const [toolCall] = await this.client.getAttributes(
      sessionId,
      this.flow.persistence.agentToolCalls.instance(toolCallId),
    );
    return toolCall;
  }

  /**
   * Reads the committed `PendingInterrupts` without a Worker.
   * @param sessionId - Session Flow ID.
   * @returns The pending interrupts, or an empty array.
   */
  public async readPendingInterrupts(sessionId: string): Promise<readonly AgentInterrupt[]> {
    const [interrupts] = await this.client.getAttributes(
      sessionId,
      this.flow.persistence.pendingInterrupts,
    );
    return interrupts ?? [];
  }

  /**
   * Polls the committed `AgentState` until a predicate holds.
   * @param sessionId - Session Flow ID.
   * @param predicate - Returns `true` for the awaited state.
   * @param description - What is awaited, for the timeout error.
   * @param options - Timeout.
   * @returns The first matching state.
   * @throws {Error} When the timeout passes.
   */
  public waitForAgentState(
    sessionId: string,
    predicate: (state: AgentState) => boolean,
    description: string,
    options: AgentSessionWaitOptions = {},
  ): Promise<AgentState> {
    return pollUntil(
      async () => {
        const state = await this.readAgentState(sessionId);
        return state !== undefined && predicate(state) ? state : undefined;
      },
      { description: `${description} in session ${sessionId}`, ...options },
    );
  }

  /**
   * Waits until the session is idle after at least `turnCount` completed or failed turns.
   * @param sessionId - Session Flow ID.
   * @param turnCount - Minimum `turnCount`.
   * @param options - Timeout.
   * @returns The idle state.
   */
  public waitForCompletedTurns(
    sessionId: string,
    turnCount: number,
    options: AgentSessionWaitOptions = {},
  ): Promise<AgentState> {
    return this.waitForAgentState(
      sessionId,
      (state) => state.status === "waiting_for_user_message" && state.turnCount >= turnCount,
      `${turnCount} finished turns`,
      options,
    );
  }

  /**
   * Polls `PendingInterrupts` until an interrupt appears.
   * @param sessionId - Session Flow ID.
   * @param toolName - Tool whose interrupt is awaited, or `undefined` for any tool.
   * @param options - Timeout.
   * @returns The first matching pending interrupt.
   */
  public waitForPendingInterrupt(
    sessionId: string,
    toolName?: string,
    options: AgentSessionWaitOptions = {},
  ): Promise<AgentInterrupt> {
    return pollUntil(
      async () =>
        (await this.readPendingInterrupts(sessionId)).find(
          (interrupt) => toolName === undefined || interrupt.toolName === toolName,
        ),
      { description: `pending interrupt for ${toolName ?? "any tool"} in session ${sessionId}`, ...options },
    );
  }

  /**
   * Polls one tool call record until a predicate holds.
   * @param sessionId - Session Flow ID.
   * @param toolCallId - Tool call ID.
   * @param predicate - Returns `true` for the awaited record.
   * @param options - Timeout.
   * @returns The first matching record.
   */
  public waitForToolCall(
    sessionId: string,
    toolCallId: string,
    predicate: (toolCall: AgentToolCall) => boolean,
    options: AgentSessionWaitOptions = {},
  ): Promise<AgentToolCall> {
    return pollUntil(
      async () => {
        const toolCall = await this.readToolCall(sessionId, toolCallId);
        return toolCall !== undefined && predicate(toolCall) ? toolCall : undefined;
      },
      { description: `tool call ${toolCallId} in session ${sessionId}`, ...options },
    );
  }

  /**
   * Reads every retained `AgentEvents` message, oldest first.
   * @param sessionId - Session Flow ID.
   * @returns The retained events; trimming may have removed the oldest.
   */
  public async listAgentEvents(sessionId: string): Promise<AgentEvent[]> {
    const newestFirst: AgentEvent[] = [];
    let pageToken = "";
    do {
      const page = await this.client.listStreamMessages(
        sessionId,
        this.flow.persistence.agentEvents,
        streamPageSize,
        pageToken,
      );
      newestFirst.push(...page.messages.map((message) => message.value));
      pageToken = page.nextPageToken;
    } while (pageToken !== "");
    return newestFirst.reverse();
  }
}
