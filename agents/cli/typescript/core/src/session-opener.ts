// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Opens and leaves sessions for the plain chat and the Worker host, in the order of the session task queue plan:
// read the lease without a Worker, pull the session's task queue only while the session is free, then acquire.

import { setTimeout as delay } from "node:timers/promises";

import { FlowNotFoundError, RpcLockConflictError, type Client, type Worker } from "@superdurable/dex";
import {
  ToolExecutionError,
  type AgentFrameworkRunner,
  type AgentSessionFlow,
  type AgentTurnContext,
  type AgentTurnOutcome,
  type ContextSummaryRequest,
  type ContextSummaryResult,
  type DeferredToolExecution,
  type DeferredToolOutcome,
  type SessionTerminalLeaseResult,
  type ToolTaskCancellation,
  type ToolTaskPoll,
  type ToolTaskPollOutcome,
} from "@superdurable/dex-agents";

import { describeFailureWithoutDexDetails } from "./dex-connection.js";
import {
  acquireSessionLease,
  isLeaseExpiredHere,
  releaseSessionLease,
  type SessionLeaseHolder,
} from "./session-terminal-lease.js";
import {
  cancelledTurnLine,
  describeSessionEnvironment,
  isLegacySessionFlowType,
  isOtherSessionPlace,
  otherEnvironmentLegacySessionError,
  otherHarnessSessionError,
  refusedModelCallError,
  refusedToolCallError,
  sessionTaskQueue,
  takenOverTurnReason,
  takeoverLine,
} from "./session-task-queues.js";

type SessionFlow = AgentSessionFlow<AgentFrameworkRunner<DeferredToolOutcome>>;

/** The longest app Worker ID the Dex server accepts. */
const maxAppWorkerIdLength = 63;
/** How long a takeover keeps trying `cancel_turn` while a short Step holds its lock. */
const turnCancellationTimeoutMs = 5_000;
/** How long a takeover waits for the cancelled turn to end before the chat starts. */
const turnEndTimeoutMs = 10_000;
/** How often a takeover reads whether the cancelled turn ended. */
const turnEndPollIntervalMs = 100;

/**
 * Returns `dex-agents-<harness>-<host>-<pid>`, plus `-<suffix>` when given, cutting the host name so the ID has at
 * most 63 characters.
 * @param harnessName - The harness name.
 * @param hostName - This host's name.
 * @param processId - This process's ID.
 * @param suffix - Tells apart the Workers of one process.
 */
export function cliAppWorkerId(harnessName: string, hostName: string, processId: number, suffix?: string): string {
  const head = `dex-agents-${harnessName}-`;
  const tail = `-${processId}${suffix === undefined ? "" : `-${suffix}`}`;
  return `${head}${hostName.slice(0, Math.max(0, maxAppWorkerIdLength - head.length - tail.length))}${tail}`;
}

/** A session this command can open. */
export interface OpenableSession {
  readonly sessionId: string;
  /** The session's Flow type. */
  readonly flowType: string;
  /**
   * Whether the session runs on its own task queue. `false` for an environment-hashed session from a release before
   * 0.6.7, which runs on the default task queue of this execution environment's Workers.
   */
  readonly hasSessionTaskQueue: boolean;
  /** The session Flow of {@link flowType}. */
  readonly flow: SessionFlow;
}

/** A session that does not exist, does not run, or belongs to another harness or execution environment. */
export class UnopenableSessionError extends Error {}

/** The session Worker could not start pulling a session's task queue, so waiting for the session ends. */
export class SessionWorkerStartError extends Error {}

/**
 * The Worker that pulls the task queue of the one session this command opens. One app Worker serves one task
 * queue, so each queue gets a new Worker with a new app Worker ID.
 */
export class SessionQueueWorker {
  private current: { readonly taskQueue: string; readonly worker: Worker; readonly started: Promise<void> } | undefined;
  private readonly closings: Promise<void>[] = [];
  private incarnation = 0;

  /**
   * Creates the holder; {@link pull} starts a Worker.
   * @param createWorker - Builds a Worker that pulls a task queue with an app Worker ID.
   * @param appWorkerIdFor - Returns the app Worker ID of one incarnation.
   * @param startWorker - Starts a Worker and waits until it pulls.
   */
  public constructor(
    private readonly createWorker: (taskQueue: string, appWorkerId: string) => Worker,
    private readonly appWorkerIdFor: (incarnation: number) => string,
    private readonly startWorker: (worker: Worker) => Promise<void>,
  ) {}

  /** The task queue the current Worker pulls, or `undefined`. */
  public get taskQueue(): string | undefined {
    return this.current?.taskQueue;
  }

  /**
   * Pulls a task queue from now on, instead of any other one.
   * @param taskQueue - The session's task queue.
   * @throws {SessionWorkerStartError} When the Worker did not start.
   */
  public async pull(taskQueue: string): Promise<void> {
    if (this.current?.taskQueue !== taskQueue) {
      this.stop();
      this.incarnation += 1;
      const worker = this.createWorker(taskQueue, this.appWorkerIdFor(this.incarnation));
      this.current = { taskQueue, worker, started: this.startWorker(worker) };
    }
    const current = this.current;
    try {
      await current?.started;
    } catch (error: unknown) {
      if (this.current === current) {
        this.stop();
      }
      if (error instanceof SessionWorkerStartError) {
        throw error;
      }
      throw new SessionWorkerStartError(describeFailureWithoutDexDetails(error), { cause: error });
    }
  }

  /** Stops pulling; the Worker drains its running tasks in the background, and {@link close} waits for that. */
  public stop(): void {
    const current = this.current;
    this.current = undefined;
    if (current !== undefined) {
      this.closings.push(current.worker.close().catch(() => undefined));
    }
  }

  /** Stops pulling and waits until every Worker this holder started has closed. */
  public async close(): Promise<void> {
    this.stop();
    await Promise.all(this.closings);
  }
}

/**
 * Refuses, in this process, the model and tool calls of a turn that was in progress in another place when this
 * command took its session over, so nothing approved there runs here.
 */
export class TakenOverTurnRefusal {
  private readonly refusedTurnIds = new Map<string, Set<string>>();

  /**
   * Refuses every model and tool call of one turn of a session from now on.
   * @param sessionId - The session.
   * @param turnId - The turn.
   */
  public refuseTurn(sessionId: string, turnId: string): void {
    const turnIds = this.refusedTurnIds.get(sessionId) ?? new Set<string>();
    turnIds.add(turnId);
    this.refusedTurnIds.set(sessionId, turnIds);
  }

  /**
   * Puts the refusal in front of the runner's model calls, tool calls, summaries, and tool task polls and
   * cancellations. The session Flow calls them through the runner object, so wrapping its own runner covers them.
   * @param runner - The session Flow's runner.
   */
  public installOn(runner: AgentFrameworkRunner<DeferredToolOutcome>): void {
    const runModelStep = runner.runModelStep.bind(runner);
    runner.runModelStep = async (turn: AgentTurnContext): Promise<AgentTurnOutcome> => {
      this.requireModelCall(turn.flowId, turn.turnId);
      return runModelStep(turn);
    };
    const executeDeferredToolCall = runner.executeDeferredToolCall.bind(runner);
    runner.executeDeferredToolCall = async (call: DeferredToolExecution) => {
      this.requireToolCall(call.flowId, call.toolCall.turnId, call.toolCall.toolName);
      return executeDeferredToolCall(call);
    };
    // Optional methods stay absent when the runner has none, because the session Flow checks their presence.
    const summarizeContext = runner.summarizeContext?.bind(runner);
    if (summarizeContext !== undefined) {
      runner.summarizeContext = async (request: ContextSummaryRequest): Promise<ContextSummaryResult> => {
        this.requireModelCall(request.flowId, request.turnId);
        return summarizeContext(request);
      };
    }
    const pollToolTask = runner.pollToolTask?.bind(runner);
    if (pollToolTask !== undefined) {
      runner.pollToolTask = async (poll: ToolTaskPoll): Promise<ToolTaskPollOutcome> => {
        this.requireToolCall(poll.flowId, poll.toolCall.turnId, poll.toolCall.toolName);
        return pollToolTask(poll);
      };
    }
    const cancelToolTask = runner.cancelToolTask?.bind(runner);
    if (cancelToolTask !== undefined) {
      runner.cancelToolTask = async (cancellation: ToolTaskCancellation): Promise<void> => {
        this.requireToolCall(cancellation.flowId, cancellation.toolCall.turnId, cancellation.toolCall.toolName);
        return cancelToolTask(cancellation);
      };
    }
  }

  private isRefused(sessionId: string, turnId: string): boolean {
    return this.refusedTurnIds.get(sessionId)?.has(turnId) === true;
  }

  private requireModelCall(sessionId: string, turnId: string): void {
    if (this.isRefused(sessionId, turnId)) {
      throw new Error(refusedModelCallError(takenOverTurnReason(sessionId)));
    }
  }

  private requireToolCall(sessionId: string, turnId: string, toolName: string): void {
    if (this.isRefused(sessionId, turnId)) {
      // The tool has not run, so the call records failed rather than an unknown outcome.
      throw new ToolExecutionError(refusedToolCallError(toolName, takenOverTurnReason(sessionId)));
    }
  }
}

/** What {@link SessionOpener} needs. */
export interface SessionOpenerOptions {
  readonly client: Client;
  /** The harness's Flow type: the Flow type of every session this release starts. */
  readonly harnessFlowType: string;
  /** The session Flow of {@link harnessFlowType}. */
  readonly sessionFlow: SessionFlow;
  /** The session Flow of this execution environment's environment-hashed Flow type, from before 0.6.7. */
  readonly legacySessionFlow: SessionFlow;
  /** `dex-agents-<harness>`, for errors. */
  readonly commandName: string;
  readonly holder: SessionLeaseHolder;
  readonly ttlSeconds: number;
  /** This command's environment, which the session keeps as where it was last open. */
  readonly environment: Readonly<Record<string, string>>;
  readonly sessionWorker: SessionQueueWorker;
  readonly turnRefusal: TakenOverTurnRefusal;
}

/** A takeover from another place, which {@link SessionOpener.completeTakeover} finishes. */
interface PendingTakeover {
  readonly previousEnvironment: Readonly<Record<string, string>>;
  /** The turn that was in progress there, which this command refuses and cancels. */
  readonly takenOverTurnId: string | null;
}

/**
 * Opens and leaves one session at a time for a command.
 *
 * A session of the harness's Flow type runs on its own task queue. Its lease is read first, without a Worker; only
 * when no other terminal holds it does the session Worker pull the session's task queue and acquire the lease
 * through the RPC that Worker serves. A session from before 0.6.7 keeps its environment-hashed Flow type and the
 * default task queue, which the command's other Worker always pulls.
 */
export class SessionOpener {
  private readonly openedSessions = new Map<string, OpenableSession>();
  private readonly pendingTakeovers = new Map<string, PendingTakeover>();

  public constructor(private readonly options: SessionOpenerOptions) {}

  /** This process's lease holder ID. */
  public get holderId(): string {
    return this.options.holder.holderId;
  }

  /**
   * Returns the session to open, after `DescribeFlow` showed that it runs with a Flow type this command serves.
   * @param sessionId - The session.
   * @throws {UnopenableSessionError} When the session does not exist, does not run, or belongs to another harness or
   * to another execution environment's environment-hashed Flow type.
   */
  public async describeOpenableSession(sessionId: string): Promise<OpenableSession> {
    const { client, harnessFlowType, sessionFlow, legacySessionFlow, commandName } = this.options;
    let info;
    try {
      info = await client.describeFlow(sessionId);
    } catch (error: unknown) {
      if (error instanceof FlowNotFoundError) {
        throw new UnopenableSessionError(`session ${sessionId} does not exist; list sessions with ${commandName} sessions`);
      }
      throw error;
    }
    let session: OpenableSession;
    if (info.flowType === harnessFlowType) {
      session = { sessionId, flowType: info.flowType, hasSessionTaskQueue: true, flow: sessionFlow };
    } else if (info.flowType === legacySessionFlow.getFlowType()) {
      session = { sessionId, flowType: info.flowType, hasSessionTaskQueue: false, flow: legacySessionFlow };
    } else if (isLegacySessionFlowType(harnessFlowType, info.flowType)) {
      throw new UnopenableSessionError(otherEnvironmentLegacySessionError(sessionId, info.flowType));
    } else {
      throw new UnopenableSessionError(otherHarnessSessionError(sessionId, info.flowType, commandName));
    }
    if (info.status !== "running") {
      throw new UnopenableSessionError(`session ${sessionId} is ${info.status}, so it cannot continue`);
    }
    return session;
  }

  /**
   * Returns a session this command just started on its own task queue.
   * @param sessionId - The session.
   */
  public startedSession(sessionId: string): OpenableSession {
    const { harnessFlowType, sessionFlow } = this.options;
    return { sessionId, flowType: harnessFlowType, hasSessionTaskQueue: true, flow: sessionFlow };
  }

  /**
   * Tries once to take a session's lease, as `waitForSessionLease` asks.
   *
   * For a session on its own task queue, it reads the stored lease first. While another terminal's lease has not
   * expired, it returns that lease and pulls nothing. Otherwise the session Worker pulls the session's task queue,
   * and the acquire RPC runs on it; a refused or failed acquire stops the pulling again.
   * @param session - The session.
   * @returns The lease RPC's result, or a refusal that names the stored holder.
   * @throws {SessionWorkerStartError} When the session Worker did not start.
   */
  public async attempt(session: OpenableSession): Promise<SessionTerminalLeaseResult> {
    const { client, sessionFlow, holder, ttlSeconds, environment, sessionWorker, turnRefusal } = this.options;
    this.openedSessions.set(session.sessionId, session);
    if (!session.hasSessionTaskQueue) {
      return acquireSessionLease(client, session.flow, session.sessionId, holder, ttlSeconds, environment);
    }
    const [lease, lastEnvironment, state] = await client.getAttributes(
      session.sessionId,
      sessionFlow.sessionTerminalLease,
      sessionFlow.sessionLastTerminalEnvironment,
      sessionFlow.agentState,
    );
    const isHeldHere = lease?.holderId === holder.holderId;
    if (lease !== undefined && !isHeldHere && !isLeaseExpiredHere(lease, new Date())) {
      return { isAcquired: false, lease };
    }
    const isTakeover = lastEnvironment !== undefined && !isHeldHere && isOtherSessionPlace(lastEnvironment, environment);
    const takenOverTurnId = isTakeover ? (state?.activeTurnId ?? null) : null;
    if (takenOverTurnId !== null) {
      // Refused before the Worker pulls, so none of that turn's calls runs here.
      turnRefusal.refuseTurn(session.sessionId, takenOverTurnId);
    }
    await sessionWorker.pull(sessionTaskQueue(session.sessionId));
    let result: SessionTerminalLeaseResult;
    try {
      result = await acquireSessionLease(client, sessionFlow, session.sessionId, holder, ttlSeconds, environment);
    } catch (error: unknown) {
      sessionWorker.stop();
      throw error;
    }
    if (!result.isAcquired) {
      sessionWorker.stop();
      return result;
    }
    if (isTakeover) {
      this.pendingTakeovers.set(session.sessionId, { previousEnvironment: lastEnvironment, takenOverTurnId });
    }
    return result;
  }

  /**
   * Renews the lease of a session that {@link attempt} acquired.
   * @param sessionId - The session.
   * @returns The lease RPC's result.
   */
  public renew(sessionId: string): Promise<SessionTerminalLeaseResult> {
    const { client, holder, ttlSeconds, environment } = this.options;
    return acquireSessionLease(client, this.flowOf(sessionId), sessionId, holder, ttlSeconds, environment);
  }

  /**
   * Finishes a takeover from another place once the lease is held: cancels the turn that was in progress there.
   * A failed cancellation only leaves that turn refused in this process.
   * @param sessionId - The session whose lease this command now holds.
   * @returns The lines to print, none when the session was last open in this place.
   */
  public async completeTakeover(sessionId: string): Promise<readonly string[]> {
    const takeover = this.pendingTakeovers.get(sessionId);
    if (takeover === undefined) {
      return [];
    }
    this.pendingTakeovers.delete(sessionId);
    const lines = [
      takeoverLine(
        sessionId,
        describeSessionEnvironment(takeover.previousEnvironment),
        describeSessionEnvironment(this.options.environment),
      ),
    ];
    const turnId = takeover.takenOverTurnId;
    try {
      if (turnId !== null && (await this.readActiveTurnId(sessionId)) === turnId) {
        await this.cancelTurn(sessionId, turnId);
        // The chat must not show the cancelled turn's approvals, which stay pending until the turn ends.
        await this.waitForTurnEnd(sessionId, turnId);
        lines.push(cancelledTurnLine);
      }
    } catch {
      // The turn stays refused in this process, so none of its calls runs here.
    }
    return lines;
  }

  /**
   * Leaves a session: releases its lease while the session Worker still serves the RPC, then stops pulling.
   * @param sessionId - The session.
   */
  public async release(sessionId: string): Promise<void> {
    const { client, holder } = this.options;
    await releaseSessionLease(client, this.flowOf(sessionId), sessionId, holder.holderId);
    this.leave(sessionId);
  }

  /**
   * Stops pulling a session's task queue without releasing its lease, which another terminal took.
   * @param sessionId - The session.
   */
  public leave(sessionId: string): void {
    const { sessionWorker } = this.options;
    const session = this.openedSessions.get(sessionId);
    if (session?.hasSessionTaskQueue === true && sessionWorker.taskQueue === sessionTaskQueue(sessionId)) {
      sessionWorker.stop();
    }
  }

  private flowOf(sessionId: string): SessionFlow {
    return this.openedSessions.get(sessionId)?.flow ?? this.options.sessionFlow;
  }

  private async readActiveTurnId(sessionId: string): Promise<string | null> {
    const [state] = await this.options.client.getAttributes(sessionId, this.options.sessionFlow.agentState);
    return state?.activeTurnId ?? null;
  }

  /** Waits a bounded time for a turn to end; a tool call that runs on keeps it going, and the chat then waits. */
  private async waitForTurnEnd(sessionId: string, turnId: string): Promise<void> {
    const deadline = Date.now() + turnEndTimeoutMs;
    while (Date.now() < deadline && (await this.readActiveTurnId(sessionId)) === turnId) {
      await delay(turnEndPollIntervalMs);
    }
  }

  /** Cancels one turn, trying again while a short Step holds the RPC's lock. */
  private async cancelTurn(sessionId: string, turnId: string): Promise<void> {
    const deadline = Date.now() + turnCancellationTimeoutMs;
    while (Date.now() < deadline) {
      try {
        await this.options.client.invokeRPC(this.options.sessionFlow.cancelTurn, sessionId, { turnId });
        return;
      } catch (error: unknown) {
        if (!(error instanceof RpcLockConflictError)) {
          throw error;
        }
      }
      await delay(50);
    }
  }
}
