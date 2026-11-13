// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Sessions that move between execution environments. The lines match the Python CLI core; see
// agents/conformance/cli_session_environments.json.

import { setTimeout as delay } from "node:timers/promises";

import { RpcLockConflictError, type Client } from "@superdurable/dex";
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
  handBackSessionLease,
  isLeaseExpiredHere,
  isSessionCallAllowedHere,
  readStoredSessionLease,
  releaseSessionLease,
  sessionOwnerEnvironment,
  type SessionLeaseHolder,
} from "./session-terminal-lease.js";

type SessionFlow = AgentSessionFlow<AgentFrameworkRunner<DeferredToolOutcome>>;

/** The suffix of a session Flow type: `-` and 6 lowercase base36 characters. */
const sessionFlowTypeSuffix = /^-[0-9a-z]{6}$/;
/** How long a takeover keeps trying `cancel_turn` while a short Step holds its lock. */
const turnCancellationTimeoutMs = 5_000;
/** How long a takeover waits for the cancelled turn to end before the chat starts. */
const turnEndTimeoutMs = 10_000;
/** How often a takeover reads whether the cancelled turn ended. */
const turnEndPollIntervalMs = 100;

/**
 * Returns whether a session Flow type belongs to a harness: its base Flow type, `-`, and 6 base36 characters.
 * @param harnessFlowType - The harness's base Flow type.
 * @param flowType - A session's Flow type.
 */
export function isHarnessSessionFlowType(harnessFlowType: string, flowType: string): boolean {
  return flowType.startsWith(harnessFlowType) && sessionFlowTypeSuffix.test(flowType.slice(harnessFlowType.length));
}

/** Returns the error for `--resume` of a session that another harness's command runs. */
export function otherHarnessSessionError(sessionId: string, flowType: string, commandName: string): string {
  return `session ${sessionId} is a ${flowType} session, which ${commandName} does not run; resume it with the command that started it`;
}

/** Returns the line printed when a command takes a session over from another execution environment. */
export function takeoverLine(sessionId: string, previousEnvironment: string, thisEnvironment: string, workspace: string): string {
  return `Session ${sessionId} was last open in execution environment ${previousEnvironment}; its model and tool calls now run in this one (${thisEnvironment}), in workspace ${workspace}.`;
}

/** The line printed when a takeover cancelled the turn in progress. */
export const cancelledTurnLine = "Cancelled the turn that was in progress there; none of its model or tool calls run here.";

/** Returns why a Worker outside the environment that holds a session refuses the session's call. */
export function sessionElsewhereReason(sessionId: string): string {
  return `session ${sessionId} is now open in another execution environment`;
}

/** Returns why the holder refuses a call of the turn that was in progress when it took the session over. */
export function turnElsewhereReason(sessionId: string): string {
  return `its turn started in another execution environment before this command took session ${sessionId} over`;
}

/** Returns the error of a refused tool call. */
export function refusedToolCallError(toolName: string, reason: string): string {
  return `tool ${toolName} did not run: ${reason}`;
}

/** Returns the error of a refused model call. */
export function refusedModelCallError(reason: string): string {
  return `the model call did not run: ${reason}`;
}

/**
 * Refuses a session's model and tool calls on a Worker outside the execution environment that holds the session.
 *
 * Dex routes Steps by Flow type alone, so every Worker that polls a session's Flow type may receive its calls. Each
 * call first reads the stored lease from Dex and runs only when {@link isSessionCallAllowedHere} allows it. The
 * holder also refuses the turn that was in progress when it took the session over from another environment.
 */
export class SessionEnvironmentFence {
  private readonly refusedTurnIds = new Map<string, Set<string>>();

  /**
   * Creates the fence; {@link installOn} puts it in front of a runner.
   * @param client - The command's Client.
   * @param flow - The session Flow, whose Flow type the Worker polls.
   * @param homeFlowType - The session Flow type of this command's own execution environment.
   * @param holderId - This process's lease holder ID.
   */
  public constructor(
    private readonly client: Client,
    private readonly flow: SessionFlow,
    private readonly homeFlowType: string,
    private readonly holderId: string,
  ) {}

  /**
   * Refuses, in this process, every model and tool call of one turn of a session.
   * @param sessionId - The session.
   * @param turnId - The turn.
   */
  public refuseTurn(sessionId: string, turnId: string): void {
    const turnIds = this.refusedTurnIds.get(sessionId) ?? new Set<string>();
    turnIds.add(turnId);
    this.refusedTurnIds.set(sessionId, turnIds);
  }

  /**
   * Returns why this Worker must not run a call of a session's turn, or `undefined` when it may.
   * @param sessionId - The session.
   * @param turnId - The call's turn.
   * @throws When Dex does not answer the read of the stored lease.
   */
  public async refusalReason(sessionId: string, turnId: string): Promise<string | undefined> {
    if (this.refusedTurnIds.get(sessionId)?.has(turnId) === true) {
      return turnElsewhereReason(sessionId);
    }
    const stored = await readStoredSessionLease(this.client, this.flow, sessionId);
    const isAllowed = isSessionCallAllowedHere({
      sessionFlowType: this.flow.getFlowType(),
      homeFlowType: this.homeFlowType,
      holderId: this.holderId,
      stored,
    });
    return isAllowed ? undefined : sessionElsewhereReason(sessionId);
  }

  /**
   * Puts the fence in front of the runner's model calls, tool calls, summaries, and tool task polls and
   * cancellations. The session Flow calls them through the runner object, so wrapping its own runner covers them.
   * @param runner - The session Flow's runner.
   */
  public installOn(runner: AgentFrameworkRunner<DeferredToolOutcome>): void {
    const runModelStep = runner.runModelStep.bind(runner);
    runner.runModelStep = async (turn: AgentTurnContext): Promise<AgentTurnOutcome> => {
      await this.requireModelCallHere(turn.flowId, turn.turnId);
      return runModelStep(turn);
    };
    const executeDeferredToolCall = runner.executeDeferredToolCall.bind(runner);
    runner.executeDeferredToolCall = async (call: DeferredToolExecution) => {
      await this.requireToolCallHere(call.flowId, call.toolCall.turnId, call.toolCall.toolName);
      return executeDeferredToolCall(call);
    };
    // Optional methods stay absent when the runner has none, because the session Flow checks their presence.
    const summarizeContext = runner.summarizeContext?.bind(runner);
    if (summarizeContext !== undefined) {
      runner.summarizeContext = async (request: ContextSummaryRequest): Promise<ContextSummaryResult> => {
        await this.requireModelCallHere(request.flowId, request.turnId);
        return summarizeContext(request);
      };
    }
    const pollToolTask = runner.pollToolTask?.bind(runner);
    if (pollToolTask !== undefined) {
      runner.pollToolTask = async (poll: ToolTaskPoll): Promise<ToolTaskPollOutcome> => {
        await this.requireToolCallHere(poll.flowId, poll.toolCall.turnId, poll.toolCall.toolName);
        return pollToolTask(poll);
      };
    }
    const cancelToolTask = runner.cancelToolTask?.bind(runner);
    if (cancelToolTask !== undefined) {
      runner.cancelToolTask = async (cancellation: ToolTaskCancellation): Promise<void> => {
        await this.requireToolCallHere(cancellation.flowId, cancellation.toolCall.turnId, cancellation.toolCall.toolName);
        return cancelToolTask(cancellation);
      };
    }
  }

  private async requireModelCallHere(sessionId: string, turnId: string): Promise<void> {
    const reason = await this.refusalReason(sessionId, turnId);
    if (reason !== undefined) {
      throw new Error(refusedModelCallError(reason));
    }
  }

  private async requireToolCallHere(sessionId: string, turnId: string, toolName: string): Promise<void> {
    let reason: string | undefined;
    try {
      reason = await this.refusalReason(sessionId, turnId);
    } catch (error: unknown) {
      // The tool has not run, so the call records failed rather than an unknown outcome.
      const detail = describeFailureWithoutDexDetails(error);
      throw new ToolExecutionError(refusedToolCallError(toolName, `its session's lease could not be read: ${detail}`), {
        cause: error,
      });
    }
    if (reason !== undefined) {
      throw new ToolExecutionError(refusedToolCallError(toolName, reason));
    }
  }
}

/** The deferred Worker of another environment's session could not start; waiting for the lease ends. */
export class SessionWorkerStartError extends Error {}

/** What {@link SessionTakeover} needs. */
export interface SessionTakeoverOptions {
  readonly client: Client;
  /** The session Flow, whose Flow type the Worker polls. */
  readonly flow: SessionFlow;
  readonly fence: SessionEnvironmentFence;
  readonly holder: SessionLeaseHolder;
  readonly ttlSeconds: number;
  /** The session Flow type of this command's own execution environment. */
  readonly homeFlowType: string;
  /** This command's workspace, which the takeover line names. */
  readonly workspace: string;
  /** Starts the Worker once; later calls wait for the first. */
  readonly startWorker: () => Promise<void>;
  /** Whether the Worker polls the session Flow type already. */
  readonly isWorkerStarted: () => boolean;
}

/**
 * Opens and leaves sessions for one command, including sessions of other execution environments.
 *
 * A Worker for another environment's session starts polling only once no other terminal's live lease is stored,
 * so it takes none of the session's Steps while another terminal has the session open. A takeover from another
 * environment refuses and cancels the turn in progress, so nothing approved there runs here.
 */
export class SessionTakeover {
  /** The sessions acquired from another environment, with that environment, until {@link completeTakeover}. */
  private readonly crossEnvironmentAcquisitions = new Map<string, string>();

  public constructor(private readonly options: SessionTakeoverOptions) {}

  /** Whether the Worker polls another execution environment's Flow type. */
  public get isOtherEnvironmentSession(): boolean {
    return this.options.flow.getFlowType() !== this.options.homeFlowType;
  }

  /**
   * Tries once to take a session's lease, as {@link waitForSessionLease} asks.
   * @param sessionId - The session.
   * @returns The lease RPC's result, or a refusal naming the stored holder while the Worker still waits to start.
   */
  public async attempt(sessionId: string): Promise<SessionTerminalLeaseResult> {
    const { client, flow, holder } = this.options;
    const stored = await readStoredSessionLease(client, flow, sessionId);
    const isHeldByOther = stored !== undefined && stored.holderId !== holder.holderId;
    if (!this.options.isWorkerStarted()) {
      if (isHeldByOther && !isLeaseExpiredHere(stored, new Date())) {
        return { isAcquired: false, lease: stored };
      }
      await this.options.startWorker();
    }
    const previousEnvironment = sessionOwnerEnvironment(flow.getFlowType(), stored);
    const isCrossEnvironment = stored?.holderId !== holder.holderId && previousEnvironment !== this.options.homeFlowType;
    if (isCrossEnvironment) {
      // Refused before the lease is taken, so this Worker never runs a call that was approved there.
      const activeTurnId = await this.readActiveTurnId(sessionId);
      if (activeTurnId !== null) {
        this.options.fence.refuseTurn(sessionId, activeTurnId);
      }
    }
    const result = await acquireSessionLease(client, flow, sessionId, holder, this.options.ttlSeconds);
    if (result.isAcquired && isCrossEnvironment) {
      this.crossEnvironmentAcquisitions.set(sessionId, previousEnvironment);
    }
    return result;
  }

  /**
   * Finishes a takeover from another environment once the lease is held: refuses and cancels the turn in progress.
   * @param sessionId - The session whose lease this command now holds.
   * @returns The lines to print, none when the session did not come from another environment.
   */
  public async completeTakeover(sessionId: string): Promise<readonly string[]> {
    const previousEnvironment = this.crossEnvironmentAcquisitions.get(sessionId);
    if (previousEnvironment === undefined) {
      return [];
    }
    const lines = [takeoverLine(sessionId, previousEnvironment, this.options.homeFlowType, this.options.workspace)];
    const activeTurnId = await this.readActiveTurnId(sessionId);
    if (activeTurnId !== null) {
      this.options.fence.refuseTurn(sessionId, activeTurnId);
      await this.cancelTurn(sessionId, activeTurnId);
      // The chat must not show the cancelled turn's approvals, which stay pending until the turn ends.
      await this.waitForTurnEnd(sessionId, activeTurnId);
      lines.push(cancelledTurnLine);
    }
    // Kept until here, so a failed read leaves the takeover to finish on the next try.
    this.crossEnvironmentAcquisitions.delete(sessionId);
    return lines;
  }

  /**
   * Leaves a session whose lease this process may hold. A session of this environment is released. Another
   * environment's session is handed back by expiry, so the holder keeps naming this environment.
   * @param sessionId - The session.
   */
  public async leave(sessionId: string): Promise<void> {
    const { client, flow, holder } = this.options;
    if (this.isOtherEnvironmentSession) {
      await handBackSessionLease(client, flow, sessionId, holder);
      return;
    }
    await releaseSessionLease(client, flow, sessionId, holder.holderId);
  }

  private async readActiveTurnId(sessionId: string): Promise<string | null> {
    const [state] = await this.options.client.getAttributes(sessionId, this.options.flow.agentState);
    return state?.activeTurnId ?? null;
  }

  /** Waits a bounded time for a turn to end; a tool call that runs on keeps it going, and the chat then waits. */
  private async waitForTurnEnd(sessionId: string, turnId: string): Promise<void> {
    const deadline = Date.now() + turnEndTimeoutMs;
    while (Date.now() < deadline && (await this.readActiveTurnId(sessionId)) === turnId) {
      await delay(turnEndPollIntervalMs);
    }
  }

  /** Cancels one turn, trying again while a short Step holds the RPC's lock; other failures are left to the fence. */
  private async cancelTurn(sessionId: string, turnId: string): Promise<void> {
    const deadline = Date.now() + turnCancellationTimeoutMs;
    while (Date.now() < deadline) {
      try {
        await this.options.client.invokeRPC(this.options.flow.cancelTurn, sessionId, { turnId });
        return;
      } catch (error: unknown) {
        if (!(error instanceof RpcLockConflictError)) {
          return;
        }
      }
      await delay(50);
    }
  }
}
