// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// The rules match the Python core; see agents/conformance/session_fork.json.

import {
  DexServiceError,
  IdReusePolicy,
  InitialAttribute,
  StepList,
  StopType,
  TimeTravelStepMethod,
  TimeTravelType,
  type Attribute,
  type Client,
  type CloneForkFlowOptions,
  type CloneForkFlowResult,
  type Flow,
  type PersistenceSchema,
  type Stream,
} from "@superdurable/dex";

import {
  createAgentSessionPersistence,
  defaultAgentEventsCapacityBytes,
} from "./agent-session-persistence.js";
import type {
  AgentEvent,
  AgentSessionStartInput,
  AgentSessionStatus,
  AgentState,
  SessionTerminalLease,
} from "./agent-session-values.js";

/**
 * The Step type whose WaitFor starts each wait for the next user message.
 *
 * @since 0.6.13
 */
export const sessionForkTurnStartStepType = "AwaitUserMessage";

const eventPageSize = 200;
// Bounds the backward read of AgentEvents; the Stream's capacity bounds it too.
const maximumEventPages = 100;
// gRPC FAILED_PRECONDITION.
const failedPreconditionCode = 9;

/**
 * The rule that refused a session fork, as `agents/conformance/session_fork.json` names it.
 *
 * @since 0.6.13
 */
export type AgentSessionForkErrorCode =
  | "not_started"
  | "session_failed"
  | "turn_in_progress"
  | "no_messages"
  | "message_out_of_range"
  | "inside_turn"
  | "steered_turn"
  | "point_unavailable"
  | "point_refused"
  | "session_changed";

/**
 * A session fork was refused; nothing was created, or the created fork was stopped.
 *
 * The message is one sentence for the user.
 *
 * @since 0.6.13
 */
export class AgentSessionForkError extends Error {
  /**
   * @param code - The rule that refused the fork.
   * @param message - The sentence the user sees.
   * @param options - Standard Error options, such as the cause.
   */
  public constructor(
    public readonly code: AgentSessionForkErrorCode,
    message: string,
    options?: ErrorOptions,
  ) {
    super(message, options);
    this.name = "AgentSessionForkError";
  }
}

/**
 * Where one turn started, as its `turn_started` event on `AgentEvents` says.
 *
 * @since 0.6.13
 */
export interface AgentSessionTurnStart {
  /** The sequence of the turn's first user message. */
  readonly sequence: number;
  /** The Step execution that started the turn, such as `AwaitUserMessage-3`. */
  readonly stepExecutionId: string;
}

/**
 * The point of the source session that a fork copies.
 *
 * @since 0.6.13
 */
export interface AgentSessionForkPoint {
  /** The last message sequence the fork keeps; `0` keeps none. */
  readonly afterMessage: number;
  /**
   * The `AwaitUserMessage` execution whose WaitFor starts right after that message's turn, or `null` for the
   * source's current state.
   */
  readonly stepExecutionId: string | null;
}

/**
 * A session that {@link forkAgentSession} created.
 *
 * @since 0.6.13
 */
export interface AgentSessionFork {
  /** The new session's Flow ID. */
  readonly sessionId: string;
  /** The new session's run ID. */
  readonly runId: string;
  /** The last message sequence the new session has. */
  readonly afterMessage: number;
  /** The run of the source session that was copied. */
  readonly sourceRunId: string;
  /** The last history event of the source run that the copy includes. */
  readonly sourceEventId: number;
}

/**
 * The definitions a fork reads and writes: a session Flow, or one from {@link createAgentSessionForkingFlow}.
 *
 * @since 0.6.13
 */
export interface AgentSessionForkSource extends Flow<AgentSessionStartInput> {
  /** The `AgentState` Attribute. */
  readonly agentState: Attribute<AgentState>;
  /** The `SessionTerminalLease` Attribute. */
  readonly sessionTerminalLease: Attribute<SessionTerminalLease>;
  /** The `SessionLastTerminalEnvironment` Attribute. */
  readonly sessionLastTerminalEnvironment: Attribute<Readonly<Record<string, string>>>;
  /** The `AgentEvents` Stream. */
  readonly agentEvents: Stream<AgentEvent>;
}

/** Options of {@link forkAgentSession}. @since 0.6.13 */
export interface AgentSessionForkOptions {
  /** The last message sequence the new session keeps; omitted or `null` forks the source's current state. */
  readonly afterMessage?: number | null;
  /** The new session's task queue; omitted or `""` keeps the source's. Workers pull a named task queue. */
  readonly taskQueue?: string;
  /** The clone fork request ID; omitted generates one. */
  readonly requestId?: string;
}

/**
 * Creates a Flow with no Steps that declares what a fork of one session Flow type reads and writes.
 *
 * Register it in a Client to fork sessions of `flowType` without building that type's session Flow, whose runner
 * may need a model API key. It declares `AgentState`, `SessionTerminalLease`, `SessionLastTerminalEnvironment`, and
 * the `AgentEvents` Stream. It never runs: a Worker never registers it.
 *
 * @example
 * ```ts
 * const forking = createAgentSessionForkingFlow("support-agent");
 * const client = new Client(new Registry([forking]), clientOptions);
 * const fork = await forkAgentSession(client, forking, "session-42", "session-42-b");
 * ```
 * @param flowType - The session Flow type, the same as the sessions' Flow type.
 * @returns The Flow.
 * @since 0.6.13
 */
export function createAgentSessionForkingFlow(flowType: string): AgentSessionForkSource {
  const persistence = createAgentSessionPersistence(defaultAgentEventsCapacityBytes);
  const schema: PersistenceSchema = {
    attributes: [persistence.agentState, persistence.sessionTerminalLease, persistence.sessionLastTerminalEnvironment],
    streams: [persistence.agentEvents],
  };
  return {
    agentState: persistence.agentState,
    sessionTerminalLease: persistence.sessionTerminalLease,
    sessionLastTerminalEnvironment: persistence.sessionLastTerminalEnvironment,
    agentEvents: persistence.agentEvents,
    getFlowType: () => flowType,
    getSteps: () => StepList.empty(),
    getPersistenceSchema: () => schema,
  };
}

/**
 * Creates a new session from another session's conversation, between two turns.
 *
 * The new session copies the source session's state at one point between turns: its messages, settings, usage, and
 * plan. It has no Flow history and no `AgentEvents` of the source. The source session never changes and keeps
 * running. Dex deletes two Attributes in the same change that creates the new session: `SessionTerminalLease`, so
 * no terminal holds the new session, and `SessionLastTerminalEnvironment`, so opening it is no takeover. The new
 * Flow ID must be unused: the fork never reuses an ID.
 *
 * The point is the source's current state when `afterMessage` is omitted or names the last message. The session
 * must then wait for a user message, with no turn running. For an earlier message, it is the point right after the
 * turn that the message ends: the WaitFor of the `AwaitUserMessage` execution that started the next turn, read from
 * that turn's `turn_started` event. That event must still be on `AgentEvents`, and the point must still be in the
 * source run's history window. A message inside a turn, or one whose next turn steering started, has no such point.
 *
 * After the copy, the new session's `AgentState` must show no running turn and exactly the kept messages. Otherwise
 * the new session is stopped and the call throws `session_changed`.
 *
 * Messages queued before the point and not yet taken by a turn are copied too, so the new session runs them as
 * soon as a Worker serves it. A point between turns has no SubFlow wait, so the SubFlow reset policy does not apply;
 * the server default is kept.
 *
 * @example
 * ```ts
 * const fork = await forkAgentSession(client, flow, "session-42", "session-42-b", { afterMessage: 6 });
 * await client.invokeRPC(flow.sendUserMessage, fork.sessionId, { text: "Try the other plan" });
 * ```
 * @param client - An open Client whose Registry contains `flow`.
 * @param flow - The source session's Flow type: its session Flow, or one from {@link createAgentSessionForkingFlow}.
 * @param sourceSessionId - The session to fork.
 * @param sessionId - The new session's Flow ID. It must differ from the source.
 * @param options - The point, the task queue, and the request ID.
 * @returns The new session and the copied point.
 * @throws {AgentSessionForkError} When the point is not between turns, cannot be found, or Dex refuses it, or the
 * session changed during the fork.
 * @throws {Error} When the server is older than server-zd 0.1.33, which has no clone fork. Nothing was created.
 * @throws {FlowAlreadyStartedError} When `sessionId` is in use.
 * @throws {FlowNotFoundError} When the source session does not exist.
 * @since 0.6.13
 */
export async function forkAgentSession(
  client: Client,
  flow: AgentSessionForkSource,
  sourceSessionId: string,
  sessionId: string,
  options: AgentSessionForkOptions = {},
): Promise<AgentSessionFork> {
  const afterMessage = options.afterMessage ?? null;
  const [state] = await client.getAttributes(sourceSessionId, flow.agentState);
  if (state === undefined) {
    throw new AgentSessionForkError(
      "not_started",
      `session ${sourceSessionId} has not started yet, so it cannot be forked`,
    );
  }
  const isEarlierMessage = afterMessage !== null && afterMessage >= 1 && afterMessage < state.nextMessageSequence - 1;
  const turnStarts = isEarlierMessage
    ? await readAgentSessionTurnStarts(client, flow.agentEvents, sourceSessionId, afterMessage + 1)
    : [];
  const point = planAgentSessionForkPoint(
    sourceSessionId,
    afterMessage,
    state.nextMessageSequence,
    state.activeTurnId,
    state.status,
    turnStarts,
  );
  const result = await cloneFork(client, flow, sourceSessionId, sessionId, point, options);
  await requireForkBetweenTurns(client, flow, sourceSessionId, sessionId, point);
  return {
    sessionId,
    runId: result.runId,
    afterMessage: point.afterMessage,
    sourceRunId: result.sourceRunId,
    sourceEventId: result.sourceEventId,
  };
}

/**
 * Returns the point a fork copies, from the source's state and its turn starts.
 *
 * @param sourceSessionId - The source session, for the error messages.
 * @param afterMessage - The last message the fork keeps, or `null` for the current state.
 * @param nextMessageSequence - `AgentState.nextMessageSequence` of the source.
 * @param activeTurnId - `AgentState.activeTurnId` of the source.
 * @param status - `AgentState.status` of the source.
 * @param turnStarts - The source's retained turn starts, in any order.
 * @returns The point.
 * @throws {AgentSessionForkError} When no point between turns follows the message.
 * @since 0.6.13
 */
export function planAgentSessionForkPoint(
  sourceSessionId: string,
  afterMessage: number | null,
  nextMessageSequence: number,
  activeTurnId: string | null,
  status: AgentSessionStatus,
  turnStarts: readonly AgentSessionTurnStart[],
): AgentSessionForkPoint {
  if (status === "failed") {
    throw new AgentSessionForkError("session_failed", `session ${sourceSessionId} failed, so it cannot be forked`);
  }
  const lastMessage = nextMessageSequence - 1;
  if (afterMessage === null || (afterMessage === lastMessage && lastMessage > 0)) {
    if (activeTurnId || status !== "waiting_for_user_message") {
      throw new AgentSessionForkError(
        "turn_in_progress",
        `session ${sourceSessionId} is running a turn; fork it once the turn ends, or after an earlier message`,
      );
    }
    return { afterMessage: lastMessage, stepExecutionId: null };
  }
  if (lastMessage === 0) {
    throw new AgentSessionForkError(
      "no_messages",
      `session ${sourceSessionId} has no messages yet; fork it without a message number`,
    );
  }
  if (!(afterMessage >= 1 && afterMessage < lastMessage)) {
    throw new AgentSessionForkError(
      "message_out_of_range",
      `session ${sourceSessionId} has messages 1 to ${lastMessage}; fork after one of them`,
    );
  }
  const following = afterMessage + 1;
  const started = turnStarts.find((start) => start.sequence === following);
  if (started !== undefined) {
    const stepType = started.stepExecutionId.slice(0, Math.max(0, started.stepExecutionId.lastIndexOf("-")));
    if (stepType === sessionForkTurnStartStepType) {
      return { afterMessage, stepExecutionId: started.stepExecutionId };
    }
    throw new AgentSessionForkError(
      "steered_turn",
      `steering ended the turn of message ${afterMessage} of session ${sourceSessionId}, so no point between turns ` +
        "follows it; fork after another message",
    );
  }
  if (turnStarts.some((start) => start.sequence <= afterMessage)) {
    throw new AgentSessionForkError(
      "inside_turn",
      `message ${afterMessage} of session ${sourceSessionId} is inside a turn; fork after the last message of a turn`,
    );
  }
  throw new AgentSessionForkError(
    "point_unavailable",
    `the events of session ${sourceSessionId} no longer show where the turn after message ${afterMessage} started; ` +
      "fork after a later message",
  );
}

/**
 * Reads the retained `turn_started` events, newest first, back to a message sequence.
 *
 * It stops at the first turn start at or before `throughSequence`, at the oldest retained event, or after 100 pages
 * of 200 events.
 *
 * @param client - An open Client whose Registry declares `agentEvents`.
 * @param agentEvents - The session Flow type's `AgentEvents` Stream.
 * @param sessionId - The session.
 * @param throughSequence - The message sequence the read reaches back to.
 * @returns The turn starts that carry a Step execution ID, newest first.
 * @since 0.6.13
 */
export async function readAgentSessionTurnStarts(
  client: Client,
  agentEvents: Stream<AgentEvent>,
  sessionId: string,
  throughSequence: number,
): Promise<AgentSessionTurnStart[]> {
  const starts: AgentSessionTurnStart[] = [];
  let pageToken = "";
  for (let pageIndex = 0; pageIndex < maximumEventPages; pageIndex += 1) {
    const page = await client.listStreamMessages(sessionId, agentEvents, eventPageSize, pageToken);
    for (const message of page.messages) {
      const start = turnStartOf(message.value);
      if (start === null) {
        continue;
      }
      starts.push(start);
      if (start.sequence <= throughSequence) {
        return starts;
      }
    }
    if (page.nextPageToken === "") {
      return starts;
    }
    pageToken = page.nextPageToken;
  }
  return starts;
}

function turnStartOf(event: AgentEvent): AgentSessionTurnStart | null {
  if (event.type !== "turn_started" || event.stepExecutionId === "") {
    return null;
  }
  let payload: unknown;
  try {
    payload = JSON.parse(event.payload);
  } catch {
    return null;
  }
  const sequence = typeof payload === "object" && payload !== null ? (payload as { sequence?: unknown }).sequence : null;
  if (typeof sequence !== "number" || !Number.isSafeInteger(sequence)) {
    return null;
  }
  return { sequence, stepExecutionId: event.stepExecutionId };
}

async function cloneFork(
  client: Client,
  flow: AgentSessionForkSource,
  sourceSessionId: string,
  sessionId: string,
  point: AgentSessionForkPoint,
  options: AgentSessionForkOptions,
): Promise<CloneForkFlowResult> {
  const pointOptions: CloneForkFlowOptions =
    point.stepExecutionId === null
      ? {}
      : {
          type: TimeTravelType.STEP_EXECUTION_ID,
          stepExecutionId: point.stepExecutionId,
          stepMethod: TimeTravelStepMethod.WAIT_FOR,
        };
  try {
    return await client.cloneForkFlow(flow, sourceSessionId, sessionId, {
      ...pointOptions,
      idReusePolicy: IdReusePolicy.DISALLOW,
      ...(options.taskQueue ? { taskQueue: options.taskQueue } : {}),
      ...(options.requestId === undefined ? {} : { requestId: options.requestId }),
      reason: `fork of agent session ${sourceSessionId} after message ${point.afterMessage}`,
      // A null write deletes each Attribute in the change that creates the fork.
      attributes: [
        InitialAttribute.of<SessionTerminalLease | null>(flow.sessionTerminalLease, null),
        InitialAttribute.of<Readonly<Record<string, string>> | null>(flow.sessionLastTerminalEnvironment, null),
      ],
    });
  } catch (failure) {
    if (failure instanceof DexServiceError && failure.code === failedPreconditionCode) {
      throw new AgentSessionForkError(
        "point_refused",
        `Dex cannot fork session ${sourceSessionId} after message ${point.afterMessage}: ${failure.detail}`,
        { cause: failure },
      );
    }
    throw failure;
  }
}

/** Stops the fork unless it shows no running turn and exactly the kept messages. */
async function requireForkBetweenTurns(
  client: Client,
  flow: AgentSessionForkSource,
  sourceSessionId: string,
  sessionId: string,
  point: AgentSessionForkPoint,
): Promise<void> {
  const [state] = await client.getAttributes(sessionId, flow.agentState);
  if (
    state !== undefined &&
    !state.activeTurnId &&
    state.status === "waiting_for_user_message" &&
    state.nextMessageSequence === point.afterMessage + 1
  ) {
    return;
  }
  try {
    await client.stopFlow(sessionId, { type: StopType.TERMINATE, reason: "the fork did not land between turns" });
  } catch (failure) {
    // The refusal below matters more; an unstopped fork waits on a task queue nobody may pull.
    console.warn(`dex-agents: cannot stop the fork ${sessionId}:`, failure);
  }
  throw new AgentSessionForkError(
    "session_changed",
    `session ${sourceSessionId} changed while it was forked, so the fork was stopped; try again`,
  );
}
