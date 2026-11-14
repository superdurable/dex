// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// One terminal per session. The lines match the Python CLI core; see agents/conformance/cli_session_lease.json.

import { randomBytes } from "node:crypto";
import { hostname } from "node:os";
import type { Writable } from "node:stream";
import { setTimeout as delay } from "node:timers/promises";

import { RpcLockConflictError, type Client } from "@superdurable/dex";
import type {
  AgentFrameworkRunner,
  AgentSessionFlow,
  DeferredToolOutcome,
  SessionTerminalLease,
  SessionTerminalLeaseResult,
} from "@superdurable/dex-agents";

/** How long a lease lasts unless its holder renews it. */
export const defaultSessionLeaseTtlSeconds = 15;
/** The spinner of the animated waiting line, one frame per {@link waitingFrameIntervalMs}. */
export const waitingSpinnerFrames: readonly string[] = [..."⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏"];
/** How often the animated waiting line is redrawn. */
export const waitingFrameIntervalMs = 100;
/** How often a waiting command asks for the lease again. */
export const waitingRetryIntervalMs = 1_000;
/** What redraws, or clears, the animated line in place. */
export const waitingRedrawPrefix = "\r\u001b[2K";
/** The terminal width when the terminal does not say. */
export const defaultWaitingColumns = 80;
/** How long one lease RPC may take before the attempt counts as failed. */
const leaseCallTimeoutMs = 5_000;
/** How long a release may take at exit; expiry frees the lease otherwise. */
const leaseReleaseTimeoutMs = 2_000;

type SessionFlow = AgentSessionFlow<AgentFrameworkRunner<DeferredToolOutcome>>;

/** Who takes a lease: one random holder ID per process, with what a waiting terminal shows. */
export interface SessionLeaseHolder {
  readonly holderId: string;
  readonly hostName: string;
  /** The process ID of the command the user ran. */
  readonly processId: number;
}

/** One attempt to take or renew a session's lease. */
export type SessionLeaseAttempt = () => Promise<SessionTerminalLeaseResult>;

/**
 * Returns a new holder for this process.
 * @param processId - The process ID to show, the launcher's in the terminal UI path.
 */
export function newSessionLeaseHolder(processId: number): SessionLeaseHolder {
  return { holderId: randomBytes(16).toString("hex"), hostName: hostname(), processId };
}

/**
 * Returns the TTL that `DEX_AGENTS_SESSION_LEASE_TTL_SECONDS` sets, an integer from 1 to 3600, else the default.
 * @param text - The variable's value.
 */
export function readSessionLeaseTtlSeconds(text: string | undefined): number {
  const trimmed = (text ?? "").trim();
  const seconds = Number(trimmed);
  return /^\d+$/.test(trimmed) && seconds >= 1 && seconds <= 3_600 ? seconds : defaultSessionLeaseTtlSeconds;
}

/** Returns the local time, HH:MM, of an RFC 3339 time; `timeZone` overrides the local zone. */
export function formatLeaseSince(openedAt: string, timeZone?: string): string {
  const moment = new Date(openedAt);
  if (Number.isNaN(moment.getTime())) {
    return "--:--";
  }
  return new Intl.DateTimeFormat("en-GB", {
    hour: "2-digit",
    minute: "2-digit",
    hourCycle: "h23",
    ...(timeZone === undefined ? {} : { timeZone }),
  }).format(moment);
}

/** Returns whole minutes, a colon, and two-digit seconds. */
export function formatWaitElapsed(seconds: number): string {
  const whole = Math.max(0, Math.floor(seconds));
  return `${Math.floor(whole / 60)}:${String(whole % 60).padStart(2, "0")}`;
}

/** Returns the holder text: `<host>, pid <pid>, since <HH:MM>`. */
export function formatLeaseHolder(hostName: string, processId: number, since: string): string {
  return `${hostName}, pid ${processId}, since ${since}`;
}

/** Returns one frame of the animated waiting line, before it is cut to the terminal width. */
export function animatedWaitingLine(frameIndex: number, sessionId: string, holder: string, elapsedSeconds: number): string {
  const frame = waitingSpinnerFrames[frameIndex % waitingSpinnerFrames.length] ?? "";
  return `${frame} Session ${sessionId} is open in another terminal (${holder}) · waiting ${formatWaitElapsed(elapsedSeconds)} · Ctrl-C to quit`;
}

/** Returns the line a waiting command prints once when its output is not an animated terminal. */
export function plainWaitingLine(sessionId: string, holder: string): string {
  return `Session ${sessionId} is open in another terminal (${holder}) · waiting · Ctrl-C to quit`;
}

/** Returns the line printed once a waiting command got the session. */
export function tookOverLine(sessionId: string): string {
  return `Took over session ${sessionId}.`;
}

/** Returns the line that ends a chat whose lease another terminal took. */
export function leaseLostLine(sessionId: string, holder: string): string {
  return `Session ${sessionId} is now open in another terminal (${holder}); this chat stopped.`;
}

/** Cuts a line longer than `columns - 1` code points to `columns - 2` code points and an ellipsis. */
export function cutToColumns(line: string, columns: number): string {
  const codePoints = [...line];
  const width = columns - 1;
  return codePoints.length <= width ? line : `${codePoints.slice(0, Math.max(0, width - 1)).join("")}…`;
}

/** Returns the holder text of a lease, with its local opening time. */
export function describeLeaseHolder(lease: SessionTerminalLease): string {
  return formatLeaseHolder(lease.hostName, lease.processId, formatLeaseSince(lease.openedAt));
}

/**
 * Returns whether the waiting line animates: standard error is a terminal and TERM is set and not `dumb`.
 * @param stream - Standard error.
 * @param term - `TERM`.
 */
export function isAnimatedWaitingOutput(stream: Writable, term: string | undefined): boolean {
  return (stream as { isTTY?: boolean }).isTTY === true && term !== undefined && term !== "" && term !== "dumb";
}

/** What {@link waitForSessionLease} needs. */
export interface SessionLeaseWait {
  readonly sessionId: string;
  readonly attempt: SessionLeaseAttempt;
  /** Standard error. */
  readonly output: Writable;
  readonly isAnimated: boolean;
  /** Ctrl-C ends the wait. */
  readonly interruptSignal: AbortSignal;
  /** Returns whether an attempt's error ends the wait instead of being tried again; none does by default. */
  readonly isFinalFailure?: (error: unknown) => boolean;
}

/**
 * Takes the session's lease, waiting with one status line while another terminal holds it.
 *
 * A failed attempt is tried again a second later; only a refusal that names a holder starts the display.
 * @returns `acquired`, or `interrupted` after Ctrl-C.
 * @throws The error of an attempt that {@link SessionLeaseWait.isFinalFailure} accepts.
 */
export async function waitForSessionLease(wait: SessionLeaseWait): Promise<"acquired" | "interrupted"> {
  const display = new SessionLeaseWaitingDisplay(wait.sessionId, wait.output, wait.isAnimated);
  const attempt = async (): Promise<SessionTerminalLeaseResult | undefined> => {
    try {
      return await wait.attempt();
    } catch (error: unknown) {
      if (wait.isFinalFailure?.(error) === true) {
        display.clear();
        throw error;
      }
      return undefined;
    }
  };
  try {
    for (;;) {
      const result = await abortable(attempt(), wait.interruptSignal);
      if (result === aborted) {
        display.clear();
        return "interrupted";
      }
      if (result?.isAcquired === true) {
        display.finish();
        return "acquired";
      }
      if (result?.lease !== null && result?.lease !== undefined) {
        display.showHolder(result.lease);
      }
      try {
        await delay(waitingRetryIntervalMs, undefined, { signal: wait.interruptSignal });
      } catch {
        display.clear();
        return "interrupted";
      }
    }
  } finally {
    display.stop();
  }
}

/** The waiting line: animated in place on a terminal, or one plain line otherwise. */
class SessionLeaseWaitingDisplay {
  private holder: string | undefined;
  private startedAt = 0;
  private frameIndex = 0;
  private timer: NodeJS.Timeout | undefined;

  public constructor(
    private readonly sessionId: string,
    private readonly output: Writable,
    private readonly isAnimated: boolean,
  ) {}

  public showHolder(lease: SessionTerminalLease): void {
    const isFirst = this.holder === undefined;
    if (!this.isAnimated && !isFirst) {
      return;
    }
    this.holder = describeLeaseHolder(lease);
    if (!isFirst) {
      return;
    }
    this.startedAt = Date.now();
    if (!this.isAnimated) {
      this.output.write(`${plainWaitingLine(this.sessionId, this.holder)}\n`);
      return;
    }
    this.draw();
    this.timer = setInterval(() => this.draw(), waitingFrameIntervalMs);
  }

  /** Clears the line and says the session was taken over, when the wait showed anything. */
  public finish(): void {
    if (this.holder === undefined) {
      return;
    }
    this.clear();
    this.output.write(`${tookOverLine(this.sessionId)}\n`);
  }

  public clear(): void {
    this.stop();
    if (this.isAnimated && this.holder !== undefined) {
      this.output.write(waitingRedrawPrefix);
    }
  }

  public stop(): void {
    clearInterval(this.timer);
    this.timer = undefined;
  }

  private draw(): void {
    if (this.holder === undefined) {
      return;
    }
    const reported = (this.output as { columns?: number }).columns;
    // A pseudo-terminal without a window size reports 0 columns.
    const columns = reported !== undefined && reported > 0 ? reported : defaultWaitingColumns;
    const elapsedSeconds = (Date.now() - this.startedAt) / 1_000;
    const line = animatedWaitingLine(this.frameIndex, this.sessionId, this.holder, elapsedSeconds);
    this.frameIndex += 1;
    this.output.write(`${waitingRedrawPrefix}${cutToColumns(line, columns)}`);
  }
}

/**
 * Renews the lease of the current session every third of its TTL. Only a refusal that names another holder
 * ends it; failed renewals are tried again at the next tick.
 */
export class SessionLeaseRenewal {
  private sessionId: string | undefined;
  private timer: NodeJS.Timeout | undefined;
  private isRenewing = false;

  /**
   * Creates the renewal; {@link renew} starts it.
   * @param holderId - This process's holder ID.
   * @param ttlSeconds - The lease's TTL.
   * @param attemptFor - Takes or renews the lease of one session.
   * @param onLost - Called once when another holder took the lease.
   */
  public constructor(
    private readonly holderId: string,
    ttlSeconds: number,
    private readonly attemptFor: (sessionId: string) => Promise<SessionTerminalLeaseResult>,
    private readonly onLost: (sessionId: string, lease: SessionTerminalLease) => void,
    private readonly intervalMs = (ttlSeconds * 1_000) / 3,
  ) {}

  /** The session whose lease this process holds, or `undefined`. */
  public get currentSessionId(): string | undefined {
    return this.sessionId;
  }

  /**
   * Renews the lease of `sessionId` from now on, instead of any other session's.
   * @param sessionId - The session whose lease was just acquired.
   */
  public renew(sessionId: string): void {
    this.sessionId = sessionId;
    this.timer ??= setInterval(() => void this.tick(), this.intervalMs);
    this.timer.unref();
  }

  /** Stops renewing; the caller releases the lease. */
  public stop(): void {
    clearInterval(this.timer);
    this.timer = undefined;
    this.sessionId = undefined;
  }

  private async tick(): Promise<void> {
    const sessionId = this.sessionId;
    if (sessionId === undefined || this.isRenewing) {
      return;
    }
    this.isRenewing = true;
    try {
      const result = await attemptQuietly(() => this.attemptFor(sessionId));
      const lease = result?.lease;
      if (
        this.sessionId === sessionId &&
        result?.isAcquired === false &&
        lease !== null &&
        lease !== undefined &&
        lease.holderId !== this.holderId
      ) {
        this.stop();
        this.onLost(sessionId, lease);
      }
    } finally {
      this.isRenewing = false;
    }
  }
}

/**
 * Takes or renews a session's lease through the session Flow's RPC, within 5 s.
 * @param environment - This command's environment, which the session keeps as where it was last open.
 * @returns The RPC's result.
 */
export function acquireSessionLease(
  client: Client,
  flow: SessionFlow,
  sessionId: string,
  holder: SessionLeaseHolder,
  ttlSeconds: number,
  environment: Readonly<Record<string, string>> | null = null,
): Promise<SessionTerminalLeaseResult> {
  return withinTimeout(
    client.invokeRPC(flow.acquireSessionTerminalLease, sessionId, {
      holderId: holder.holderId,
      hostName: holder.hostName,
      processId: holder.processId,
      ttlSeconds,
      environment,
    }),
    leaseCallTimeoutMs,
  );
}

/**
 * Returns whether a stored lease expired by this host's clock; an expiry that does not parse counts as expired.
 * @param lease - The stored lease.
 * @param now - This host's current time.
 */
export function isLeaseExpiredHere(lease: SessionTerminalLease, now: Date): boolean {
  const expiresAt = Date.parse(lease.expiresAt);
  return Number.isNaN(expiresAt) || expiresAt <= now.getTime();
}

/**
 * Releases a session's lease within 2 s, ignoring every failure: the lease then expires on its own.
 * @param client - The command's Client.
 * @param flow - The session Flow.
 * @param sessionId - The session.
 * @param holderId - This process's holder ID.
 */
export async function releaseSessionLease(
  client: Client,
  flow: SessionFlow,
  sessionId: string,
  holderId: string,
): Promise<void> {
  const deadline = Date.now() + leaseReleaseTimeoutMs;
  while (Date.now() < deadline) {
    try {
      await withinTimeout(client.invokeRPC(flow.releaseSessionTerminalLease, sessionId, { holderId }), deadline - Date.now());
      return;
    } catch (error: unknown) {
      if (!(error instanceof RpcLockConflictError)) {
        return;
      }
    }
    await delay(50);
  }
}

async function attemptQuietly(attempt: SessionLeaseAttempt): Promise<SessionTerminalLeaseResult | undefined> {
  try {
    return await attempt();
  } catch {
    // Unavailable Workers, lock conflicts, and timeouts all mean: ask again later.
    return undefined;
  }
}

const aborted = Symbol("aborted");

function abortable<T>(promise: Promise<T>, signal: AbortSignal): Promise<T | typeof aborted> {
  if (signal.aborted) {
    return Promise.resolve(aborted);
  }
  return new Promise((resolve, reject) => {
    const onAbort = (): void => resolve(aborted);
    signal.addEventListener("abort", onAbort, { once: true });
    promise.then(
      (value) => {
        signal.removeEventListener("abort", onAbort);
        resolve(value);
      },
      (error: unknown) => {
        signal.removeEventListener("abort", onAbort);
        reject(error instanceof Error ? error : new Error(String(error)));
      },
    );
  });
}

function withinTimeout<T>(promise: Promise<T>, timeoutMs: number): Promise<T> {
  let timer: NodeJS.Timeout | undefined;
  const timeout = new Promise<never>((_, reject) => {
    timer = setTimeout(() => reject(new Error(`no answer within ${timeoutMs} ms`)), Math.max(0, timeoutMs));
  });
  return Promise.race([promise, timeout]).finally(() => clearTimeout(timer));
}
