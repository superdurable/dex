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
  type SessionTerminalLease,
  type SessionTerminalLeaseResult,
  type ToolTaskCancellation,
  type ToolTaskPoll,
  type ToolTaskPollOutcome,
} from "@superdurable/dex-agents";

import { describeFailureWithoutDexDetails } from "./dex-connection.js";
import {
  acquireSessionLease,
  isLeaseExpiredHere,
  monotonicNowMs,
  releaseSessionLease,
  renewalSafetyMarginRatio,
  withinTimeout,
  type SessionLeaseHolder,
} from "./session-terminal-lease.js";
import {
  cancelledTurnLine,
  describeSessionEnvironmentIdentity,
  foreignTaskQueueSessionError,
  isLegacySessionFlowType,
  isOtherSessionEnvironment,
  otherEnvironmentLegacySessionError,
  otherHarnessSessionError,
  refusedModelCallError,
  refusedToolCallError,
  sessionTaskQueue,
  takenOverTurnReason,
  takeoverLine,
  turnNotCancelledLine,
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
/** How long the read of a stored lease may take after a renewal could not confirm it. */
const storedLeaseReadTimeoutMs = 2_000;
/** How long the read of a stored lease before pulling may take. */
const leaseReadTimeoutMs = 5_000;

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
 * The session's pull permit expired before an acquire or renewal confirmed the lease, so the session Worker stopped
 * pulling. Waiting for the session goes on: the next attempt pulls again and needs a new confirmation.
 */
export class PullPermitExpiredError extends Error {
  public constructor(sessionId: string) {
    super(`the pull permit of session ${sessionId} expired before its lease was confirmed`);
  }
}

/**
 * The time a session Worker may pull a session's task queue without a confirmed lease: from the start of pulling,
 * then from the send of each acquire or renewal that the session confirmed, plus the TTL minus the safety margin.
 *
 * When the permit expires, it stops pulling first, then tells its listeners. Nothing extends an expired permit.
 */
export class SessionPullPermit {
  private deadlineMs: number;
  private timer: NodeJS.Timeout | undefined;
  private isExpiredState = false;
  private readonly expiryListeners: (() => void)[] = [];

  /**
   * Arms the permit.
   * @param ttlMs - The lease's TTL.
   * @param startedAtMs - The {@link monotonicNowMs} time pulling started, or the first acquire was sent.
   * @param stopPulling - Stops the session Worker, when it still pulls this session's task queue.
   */
  public constructor(
    private readonly ttlMs: number,
    startedAtMs: number,
    private readonly stopPulling: () => void,
  ) {
    this.deadlineMs = startedAtMs + this.validMs();
    this.schedule();
  }

  /** Whether the permit expired, so the Worker no longer pulls and no answer grants the session. */
  public get isExpired(): boolean {
    return this.isExpiredState;
  }

  /** Milliseconds left before the permit expires, at least 0. */
  public remainingMs(): number {
    return Math.max(0, this.deadlineMs - monotonicNowMs());
  }

  /**
   * Extends the permit to the TTL minus the safety margin after a request that the session confirmed.
   * @param sentAtMs - The {@link monotonicNowMs} time the confirmed acquire or renewal was sent.
   * @returns Whether the permit was still valid, so the confirmation counts.
   */
  public confirm(sentAtMs: number): boolean {
    if (this.isExpiredState) {
      return false;
    }
    this.deadlineMs = Math.max(this.deadlineMs, sentAtMs + this.validMs());
    this.schedule();
    return true;
  }

  /**
   * Calls `listener` once when the permit expires; at once when it expired already.
   * @param listener - Ends the chat, or tells the launcher.
   */
  public whenExpired(listener: () => void): void {
    if (this.isExpiredState) {
      listener();
      return;
    }
    this.expiryListeners.push(listener);
  }

  /** Disarms the permit without stopping pulling or telling its listeners; the caller stops the Worker. */
  public cancel(): void {
    clearTimeout(this.timer);
    this.timer = undefined;
    this.expiryListeners.length = 0;
  }

  private validMs(): number {
    return this.ttlMs * (1 - renewalSafetyMarginRatio);
  }

  private schedule(): void {
    clearTimeout(this.timer);
    this.timer = setTimeout(() => this.expire(), this.remainingMs());
    this.timer.unref();
  }

  private expire(): void {
    if (this.isExpiredState) {
      return;
    }
    this.isExpiredState = true;
    this.timer = undefined;
    // Pulling stops before anything else, so no Step of the session runs here past the lease.
    this.stopPulling();
    for (const listener of this.expiryListeners.splice(0)) {
      listener();
    }
  }
}

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
   * @param sessionId - The session, for the error when the Worker stopped while it started.
   * @throws {SessionWorkerStartError} When the Worker did not start.
   * @throws {PullPermitExpiredError} When the Worker was stopped while it started.
   */
  public async pull(taskQueue: string, sessionId: string): Promise<void> {
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
      if (this.current !== current) {
        throw new PullPermitExpiredError(sessionId);
      }
      this.stop();
      if (error instanceof SessionWorkerStartError) {
        throw error;
      }
      throw new SessionWorkerStartError(describeFailureWithoutDexDetails(error), { cause: error });
    }
    if (this.current !== current) {
      throw new PullPermitExpiredError(sessionId);
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
  /** Appends one line to the command's log file. */
  readonly writeLogLine: (line: string) => void;
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
  /** The pull permit of each session this command pulls or holds; see {@link SessionPullPermit}. */
  private readonly permits = new Map<string, SessionPullPermit>();

  public constructor(private readonly options: SessionOpenerOptions) {}

  /** This process's lease holder ID. */
  public get holderId(): string {
    return this.options.holder.holderId;
  }

  /**
   * Returns the session to open, after `DescribeFlow` showed that it runs with a Flow type this command serves.
   * @param sessionId - The session.
   * @throws {UnopenableSessionError} When the session does not exist, does not run, or belongs to another harness or
   * to another execution environment's environment-hashed Flow type, or runs on another task queue than its own.
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
    // Another client may start a session of this Flow type without its queue; no Worker of a command serves it.
    if (session.hasSessionTaskQueue && (await client.getFlowState(sessionId)).taskQueue !== sessionTaskQueue(sessionId)) {
      throw new UnopenableSessionError(foreignTaskQueueSessionError(sessionId, commandName));
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
   * expired, it returns that lease and pulls nothing. Otherwise the session Worker pulls the session's task queue
   * under a pull permit armed at the start of pulling, and the acquire RPC runs on it; a refused acquire stops the
   * pulling. When the acquire's answer never arrives, the stored lease decides: this command's holder counts as
   * the acquire's confirmation. Only a confirmation within the permit grants the session.
   * @param session - The session.
   * @returns The lease RPC's result, or a refusal that names the stored holder.
   * @throws {SessionWorkerStartError} When the session Worker did not start.
   * @throws {PullPermitExpiredError} When the permit expired before a confirmation; the next attempt pulls again.
   */
  public async attempt(session: OpenableSession): Promise<SessionTerminalLeaseResult> {
    const { client, sessionFlow, holder, ttlSeconds, environment, sessionWorker, turnRefusal } = this.options;
    const { sessionId } = session;
    this.openedSessions.set(sessionId, session);
    if (!session.hasSessionTaskQueue) {
      // The shared Worker of earlier sessions keeps pulling; the permit only ends the chat.
      const permit = this.activePermit(sessionId) ?? this.armPermit(sessionId, monotonicNowMs());
      const legacySentAtMs = monotonicNowMs();
      const legacyResult = await withinTimeout(
        acquireSessionLease(client, session.flow, sessionId, holder, ttlSeconds, environment),
        permit.remainingMs(),
      );
      return this.grantedWithin(sessionId, permit, legacyResult, legacySentAtMs);
    }
    const [lease, lastEnvironment, state] = await withinTimeout(
      client.getAttributes(
        sessionId,
        sessionFlow.sessionTerminalLease,
        sessionFlow.sessionLastTerminalEnvironment,
        sessionFlow.agentState,
      ),
      this.activePermit(sessionId)?.remainingMs() ?? leaseReadTimeoutMs,
    );
    const isHeldHere = lease?.holderId === holder.holderId;
    if (lease !== undefined && !isHeldHere && !isLeaseExpiredHere(lease, new Date())) {
      // A Worker that an attempt with an unknown outcome kept stops here.
      this.leave(sessionId);
      return { isAcquired: false, lease };
    }
    const isTakeover =
      lastEnvironment !== undefined && !isHeldHere && isOtherSessionEnvironment(lastEnvironment, environment);
    const takenOverTurnId = isTakeover ? (state?.activeTurnId ?? null) : null;
    if (takenOverTurnId !== null) {
      // Refused before the Worker pulls, so none of that turn's calls runs here.
      turnRefusal.refuseTurn(sessionId, takenOverTurnId);
    }
    // Armed before the Worker starts, so every moment of pulling counts against the permit.
    const permit = this.activePermit(sessionId) ?? this.armPermit(sessionId, monotonicNowMs());
    await sessionWorker.pull(sessionTaskQueue(sessionId), sessionId);
    const sentAtMs = monotonicNowMs();
    let result: SessionTerminalLeaseResult;
    try {
      result = await withinTimeout(
        acquireSessionLease(client, sessionFlow, sessionId, holder, ttlSeconds, environment),
        permit.remainingMs(),
      );
    } catch (error: unknown) {
      result = await this.resolveUnknownAcquisition(sessionId, error, permit);
    }
    const granted = this.grantedWithin(sessionId, permit, result, sentAtMs);
    if (granted.isAcquired && isTakeover) {
      this.pendingTakeovers.set(sessionId, { previousEnvironment: lastEnvironment, takenOverTurnId });
    }
    return granted;
  }

  /**
   * Calls `listener` once when the pull permit of a session this command holds expires: the session Worker
   * stopped pulling first. At once when it expired already.
   * @param sessionId - A session that {@link attempt} acquired.
   * @param listener - Ends the chat, or tells the launcher.
   */
  public whenPermitExpires(sessionId: string, listener: () => void): void {
    const permit = this.permits.get(sessionId);
    if (permit === undefined) {
      listener();
      return;
    }
    permit.whenExpired(listener);
  }

  /** Returns the result of an attempt, or throws when its permit expired before the confirmation. */
  private grantedWithin(
    sessionId: string,
    permit: SessionPullPermit,
    result: SessionTerminalLeaseResult,
    sentAtMs: number,
  ): SessionTerminalLeaseResult {
    if (!result.isAcquired) {
      this.leave(sessionId);
      return result;
    }
    if (!permit.confirm(sentAtMs)) {
      // A late answer grants nothing: the Worker stopped, and the next attempt pulls and confirms again.
      throw new PullPermitExpiredError(sessionId);
    }
    return result;
  }

  /**
   * Decides an acquire whose answer never arrived, such as after a timeout: the RPC may have taken the lease, and
   * only this command's session Worker serves its release. The read is bounded by the permit, which stops the
   * Worker on its own when no confirmation comes.
   * @param sessionId - The session.
   * @param error - The acquire's failure.
   * @param permit - The session's pull permit.
   * @returns The stored lease as acquired when this command holds it.
   * @throws The acquire's failure otherwise: after stopping the Worker when the read shows another holder or no
   * lease, and keeping it, within its permit, when the read fails too, so the next attempt decides.
   */
  private async resolveUnknownAcquisition(
    sessionId: string,
    error: unknown,
    permit: SessionPullPermit,
  ): Promise<SessionTerminalLeaseResult> {
    const { client, sessionFlow, holder } = this.options;
    let stored;
    try {
      [stored] = await withinTimeout(client.getAttributes(sessionId, sessionFlow.sessionTerminalLease), permit.remainingMs());
    } catch {
      throw error;
    }
    if (stored?.holderId === holder.holderId) {
      return { isAcquired: true, lease: stored };
    }
    this.leave(sessionId);
    throw error;
  }

  /**
   * Renews the lease of a session that {@link attempt} acquired, within its permit, and extends the permit when the
   * session confirms it.
   * @param sessionId - The session.
   * @param timeoutMs - The longest the call may take; the permit's remaining time bounds it too.
   * @returns The lease RPC's result.
   * @throws {PullPermitExpiredError} When the permit expired already.
   */
  public async renew(sessionId: string, timeoutMs: number): Promise<SessionTerminalLeaseResult> {
    const { client, holder, ttlSeconds, environment } = this.options;
    const permit = this.activePermit(sessionId);
    if (permit === undefined) {
      throw new PullPermitExpiredError(sessionId);
    }
    const sentAtMs = monotonicNowMs();
    const result = await withinTimeout(
      acquireSessionLease(client, this.flowOf(sessionId), sessionId, holder, ttlSeconds, environment),
      Math.min(timeoutMs, permit.remainingMs()),
    );
    if (result.isAcquired) {
      permit.confirm(sentAtMs);
    }
    return result;
  }

  /** Returns a session's pull permit while it has not expired. */
  private activePermit(sessionId: string): SessionPullPermit | undefined {
    const permit = this.permits.get(sessionId);
    return permit?.isExpired === false ? permit : undefined;
  }

  /** Arms a new pull permit for a session, which stops its pulling and logs when it expires. */
  private armPermit(sessionId: string, startedAtMs: number): SessionPullPermit {
    const { ttlSeconds, sessionWorker, writeLogLine } = this.options;
    this.permits.get(sessionId)?.cancel();
    const permit = new SessionPullPermit(ttlSeconds * 1_000, startedAtMs, () => {
      if (sessionWorker.taskQueue === sessionTaskQueue(sessionId)) {
        sessionWorker.stop();
      }
      writeLogLine(`session pull permit expired; stopped pulling its task queue: session=${sessionId}`);
    });
    this.permits.set(sessionId, permit);
    return permit;
  }

  /**
   * Finishes a takeover from another environment once the lease is held: cancels the turn that was in progress
   * there. The turn stays refused in this process either way; the lines say whether it ended.
   * @param sessionId - The session whose lease this command now holds.
   * @returns The lines to print, none when the session was last open in this environment.
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
        describeSessionEnvironmentIdentity(takeover.previousEnvironment),
        describeSessionEnvironmentIdentity(this.options.environment),
      ),
    ];
    const turnId = takeover.takenOverTurnId;
    if (turnId !== null) {
      lines.push((await this.isTurnCancelled(sessionId, turnId)) ? cancelledTurnLine : turnNotCancelledLine);
    }
    return lines;
  }

  /**
   * Cancels a turn and waits for it to end.
   * @returns Whether the turn is no longer active: ended before, cancelled, or ended within the wait.
   */
  private async isTurnCancelled(sessionId: string, turnId: string): Promise<boolean> {
    try {
      if ((await this.readActiveTurnId(sessionId)) !== turnId) {
        return true;
      }
      if (!(await this.cancelTurn(sessionId, turnId))) {
        return (await this.readActiveTurnId(sessionId)) !== turnId;
      }
      // The chat must not show the cancelled turn's approvals, which stay pending until the turn ends.
      return await this.waitForTurnEnd(sessionId, turnId);
    } catch {
      return false;
    }
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
   * Reads a session's stored lease without a Worker, within 2 s.
   * @param sessionId - The session.
   * @returns The lease, or `undefined` when none is stored or the read failed.
   */
  public async readStoredLease(sessionId: string): Promise<SessionTerminalLease | undefined> {
    const { client, sessionFlow } = this.options;
    let timer: NodeJS.Timeout | undefined;
    const timeout = new Promise<undefined>((resolve) => {
      timer = setTimeout(() => resolve(undefined), storedLeaseReadTimeoutMs);
    });
    try {
      const read = client.getAttributes(sessionId, sessionFlow.sessionTerminalLease).then(([lease]) => lease);
      return await Promise.race([read, timeout]);
    } catch {
      return undefined;
    } finally {
      clearTimeout(timer);
    }
  }

  /**
   * Stops pulling a session's task queue without releasing its lease, which another terminal took.
   * @param sessionId - The session.
   */
  public leave(sessionId: string): void {
    const { sessionWorker } = this.options;
    // The permit is disarmed without its listeners: the caller is ending the session on purpose.
    this.permits.get(sessionId)?.cancel();
    this.permits.delete(sessionId);
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

  /**
   * Waits a bounded time for a turn to end; a tool call that runs on keeps it going, and the chat then waits.
   * @returns Whether the turn ended within the wait.
   */
  private async waitForTurnEnd(sessionId: string, turnId: string): Promise<boolean> {
    const deadline = monotonicNowMs() + turnEndTimeoutMs;
    for (;;) {
      if ((await this.readActiveTurnId(sessionId)) !== turnId) {
        return true;
      }
      if (monotonicNowMs() >= deadline) {
        return false;
      }
      await delay(turnEndPollIntervalMs);
    }
  }

  /**
   * Cancels one turn, trying again while a short Step holds the RPC's lock.
   * @returns The RPC's result, or `false` when the lock stayed held until the deadline.
   */
  private async cancelTurn(sessionId: string, turnId: string): Promise<boolean> {
    const deadline = monotonicNowMs() + turnCancellationTimeoutMs;
    while (monotonicNowMs() < deadline) {
      try {
        return await this.options.client.invokeRPC(this.options.sessionFlow.cancelTurn, sessionId, { turnId });
      } catch (error: unknown) {
        if (!(error instanceof RpcLockConflictError)) {
          throw error;
        }
      }
      await delay(50);
    }
    return false;
  }
}
