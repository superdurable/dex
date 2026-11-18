// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { debuglog } from "node:util";

import { status } from "@grpc/grpc-js";

import { PollWorkerSlotKind } from "./gen/dex.js";

// NODE_DEBUG=dex prints the grow and shrink decisions.
const debugLog = debuglog("dex");

/**
 * An independent snapshot of a pull Worker's slot streams, returned by `Worker.pullSlotStreamState`.
 *
 * Every count is a number of slot streams or handlers, in the same unit as
 * `PullWorkerOptions.maxConcurrentTasks`. Changing the object does not change the Worker.
 *
 * `open`, `registering`, `busy`, and `idle` count Step-and-RPC streams only: the streams that
 * accept both Steps and RPCs. `rpcOnlyOpen` counts the reserved RPC-only streams. While every
 * stream is registered and no handler outlives its stream, an adaptive Worker, whose
 * `PullWorkerOptions.minOpenSlots` is below `maxConcurrentTasks`, keeps `open` between `floor`
 * and `effectiveCeiling`. A fixed Worker keeps `open` equal to `effectiveCeiling`. Reconnects,
 * orphaned handlers, and a lowered Server cap move `open` outside that range until the Worker
 * converges again.
 *
 * An `open` of zero with `orphanedHandlers` above zero means that the Worker holds no live
 * Step-and-RPC stream. It waits for running handlers to return, for example after a Server
 * restart. `pullRegistrationState` does not report that gap, because readiness stays latched
 * across reconnects.
 *
 * @example
 * ```ts
 * const state = worker.pullSlotStreamState();
 * console.log(`open=${state.open} busy=${state.busy} idle=${state.idle} `
 *   + `orphaned=${state.orphanedHandlers} capacity=${state.totalCapacity}`);
 * ```
 * @since 1.45.7
 */
export interface PullSlotStreamState {
  /** Whether the Worker varies its open Step-and-RPC streams, which is when `minOpenSlots` is below `maxConcurrentTasks`. */
  readonly isAdaptive: boolean;
  /** Step-and-RPC streams that are registered or registering and not closing. */
  readonly open: number;
  /** Step-and-RPC streams that wait for the Server's acknowledgement, including a reconnect that waits for its backoff. */
  readonly registering: number;
  /** Step-and-RPC streams that run a task. */
  readonly busy: number;
  /** Registered Step-and-RPC streams that wait for a task. */
  readonly idle: number;
  /**
   * Step-and-RPC handlers that still run after their stream ended.
   * Each one keeps its slot, so the Worker registers no stream for it until it returns.
   */
  readonly orphanedHandlers: number;
  /** RPC-only handlers that still run after their stream ended. */
  readonly orphanedRpcOnlyHandlers: number;
  /**
   * Step-and-RPC streams the Worker keeps open at rest.
   * It is `minOpenSlots` minus the reserved RPC slots, limited by `effectiveCeiling`.
   */
  readonly floor: number;
  /**
   * Most Step-and-RPC streams and orphaned handlers the Worker may hold.
   * It is `totalCapacity` minus the RPC-only slots, or minus the RPC-only streams and handlers
   * when they exceed those slots.
   */
  readonly effectiveCeiling: number;
  /**
   * Step-and-RPC stream count that growth may reach.
   * It equals `effectiveCeiling`, or less for the rest of a shrink window after the Server
   * rejected a growth stream at its stream limit.
   */
  readonly growthTarget: number;
  /**
   * The Worker's slot count.
   * It is `maxConcurrentTasks`, limited by the per-Worker cap that the Server reported in its
   * last acknowledgement.
   */
  readonly totalCapacity: number;
  /** RPC-only streams that are registered or registering and not closing. */
  readonly rpcOnlyOpen: number;
  /**
   * RPC-only streams the Worker keeps.
   * It is `reservedRpcTasks`, or fewer when the Server cap leaves no room for them next to one
   * Step-and-RPC stream.
   */
  readonly rpcOnlySlots: number;
  /** Step-and-RPC streams that growth opened since the Worker started. */
  readonly streamsGrown: number;
  /** Streams that the Worker half-closed because it had more than it needed. */
  readonly streamsShrunk: number;
  /**
   * Registrations that the adaptive Worker treated as a soft limit.
   * The Server rejected each with `RESOURCE_EXHAUSTED`, and the Worker did not record it as a
   * permanent rejection.
   */
  readonly growRejectedAtCap: number;
  /**
   * Tasks that arrived on a stream after the Worker decided to close it.
   * The Server returns each such task as not delivered and dispatches it again without spending a retry.
   */
  readonly shrinkRaceDrops: number;
}

/** Phase of one slot attempt; an untracked attempt counts in no total. */
export enum PullSlotPhase {
  Untracked,
  Registering,
  Idle,
  Busy,
  // A stream draining after the Server's Reconnect; it takes no task.
  Reconnecting,
}

const PULL_SLOT_PHASE_COUNT = 5;

/** One registration attempt of a slot stream. */
export interface PullSlotAttempt {
  phase: PullSlotPhase;
  idleSequence: number;
  isGrowth: boolean;
  isOpenedAfterReadiness: boolean;
  hasEnded: boolean;
  hasReceivedRegistered: boolean;
  // Set in the same synchronous block that writes the Register; until then closeSurplus may revoke it.
  isRegisterSent: boolean;
  isOrphaned: boolean;
  isClosedOnPurpose: boolean;
  isSoftRejected: boolean;
  isReopenGranted: boolean;
}

/** The part of a pull stream that the slot state machine reads and updates. */
export interface PullSlotStream {
  readonly slotKind: PollWorkerSlotKind;
  slot: PullSlotAttempt;
}

/** Slot stream timings and test hooks; only tests replace the defaults. */
export interface PullSlotSettings {
  readonly shrinkWindowMs: number;
  /** Runs after a slot stream opens and before its Register is checked and written. */
  readonly beforeRegisterSendForTest?: (stream: PullSlotStream) => Promise<void>;
}

/** The pull Worker operations that the slot state machine drives. */
export interface PullSlotStreamHost {
  isWorkerStopping(): boolean;
  hasRegisteredAllSlotKinds(): boolean;
  listSlotStreams(): Iterable<PullSlotStream>;
  /** Creates a stream runner for the attempt; the runner starts after the current synchronous block. */
  openSlotRunner(slotKind: PollWorkerSlotKind, attempt: PullSlotAttempt): PullSlotStream;
  /** Marks the stream draining and, when shouldCloseNow is set, half-closes it at once. */
  halfCloseSlotStream(stream: PullSlotStream, shouldCloseNow: boolean): void;
}

/** Pull Worker slot counts from the options, before the first acknowledgement. */
export interface PullSlotConfiguration {
  readonly maxConcurrentTasks: number;
  readonly minOpenSlots: number;
  readonly reservedRpcTasks: number;
  readonly isMaxConcurrentTasksExplicit: boolean;
  readonly isMinOpenSlotsExplicit: boolean;
  readonly isReservedRpcTasksExplicit: boolean;
}

// W, the shrink window of decision D6.
const DEFAULT_PULL_SHRINK_WINDOW_MS = 30_000;

export function defaultPullSlotSettings(): PullSlotSettings {
  return { shrinkWindowMs: DEFAULT_PULL_SHRINK_WINDOW_MS };
}

/**
 * Keeps one RPC-only slot when the ceiling and the floor both leave room for it.
 * @returns The default reserved RPC slot count.
 */
export function defaultReservedRpcTasks(maxConcurrentTasks: number, minOpenSlots: number): number {
  return maxConcurrentTasks >= 2 && minOpenSlots >= 2 ? 1 : 0;
}

export function newPullSlotAttempt(isGrowth: boolean, isOpenedAfterReadiness: boolean): PullSlotAttempt {
  return {
    phase: PullSlotPhase.Untracked, idleSequence: 0, isGrowth, isOpenedAfterReadiness, hasEnded: false,
    hasReceivedRegistered: false, isRegisterSent: false, isOrphaned: false, isClosedOnPurpose: false,
    isSoftRejected: false, isReopenGranted: false,
  };
}

/** The adaptive slot stream state machine of one pull Worker. */
export class PullSlotStreams {
  /** Slot timings and test hooks; tests replace it before `start`. */
  public settings: PullSlotSettings;
  public readonly isAdaptive: boolean;
  public readonly isMaxConcurrentTasksExplicit: boolean;
  public hasCheckedRegistrationLimit = false;
  /** T: the ceiling, clamped at the first acknowledgement in adaptive mode or with an unset fixed ceiling. */
  public maxConcurrentTasks: number;
  public reservedRpcTasks: number;
  private minOpenSlots: number;
  private readonly configuredMaxConcurrentTasks: number;
  private readonly isMinOpenSlotsExplicit: boolean;
  private readonly isReservedRpcTasksExplicit: boolean;
  // C, from the last acknowledgement.
  private totalCapacity: number;
  // R_eff.
  private rpcOnlySlots: number;
  // L, minOpenSlots minus R.
  private stepFloor: number;
  // L_eff.
  private effectiveFloor = 0;
  // E.
  private effectiveCeiling = 0;
  // G, never above effectiveCeiling.
  private growthTarget = 0;
  private readonly stepPhaseCounts = new Array<number>(PULL_SLOT_PHASE_COUNT).fill(0);
  private readonly rpcOnlyPhaseCounts = new Array<number>(PULL_SLOT_PHASE_COUNT).fill(0);
  private orphanedStepHandlers = 0;
  private orphanedRpcOnlyHandlers = 0;
  private isGrowthTargetClamped = false;
  private hasGrownThisWindow = false;
  // The retry latch that a soft rejection sets.
  private isReopenDeferred = false;
  private hasBatchRejection = false;
  private peakBusy = 0;
  private nextIdleSequence = 0;
  private shrinkWindowTimer: ReturnType<typeof setTimeout> | undefined;
  private shrinkWindowGeneration = 0;
  private streamsGrown = 0;
  private streamsShrunk = 0;
  private growRejectedAtCap = 0;
  private shrinkRaceDrops = 0;

  public constructor(
    configuration: PullSlotConfiguration,
    private readonly host: PullSlotStreamHost,
    settings: PullSlotSettings = defaultPullSlotSettings(),
  ) {
    this.settings = settings;
    this.maxConcurrentTasks = configuration.maxConcurrentTasks;
    this.configuredMaxConcurrentTasks = configuration.maxConcurrentTasks;
    this.minOpenSlots = configuration.minOpenSlots;
    this.reservedRpcTasks = configuration.reservedRpcTasks;
    this.isMaxConcurrentTasksExplicit = configuration.isMaxConcurrentTasksExplicit;
    this.isMinOpenSlotsExplicit = configuration.isMinOpenSlotsExplicit;
    this.isReservedRpcTasksExplicit = configuration.isReservedRpcTasksExplicit;
    this.isAdaptive = configuration.minOpenSlots < configuration.maxConcurrentTasks;
    this.totalCapacity = configuration.maxConcurrentTasks;
    this.rpcOnlySlots = configuration.reservedRpcTasks;
    this.stepFloor = configuration.minOpenSlots - configuration.reservedRpcTasks;
    this.recomputeCapacity();
  }

  /** Opens the startup streams and starts the shrink window of an adaptive Worker. */
  public start(): void {
    if (this.host.isWorkerStopping()) return;
    this.restartShrinkWindow();
    this.maybeReopen(undefined);
  }

  /** Re-evaluates the reopen rule; it opens nothing that the rule does not allow. */
  public reconcile(): void {
    if (this.host.isWorkerStopping()) return;
    this.maybeReopen(undefined);
  }

  /** Stops the shrink window timer at stop. */
  public stop(): void {
    this.shrinkWindowGeneration++;
    if (this.shrinkWindowTimer !== undefined) clearTimeout(this.shrinkWindowTimer);
    this.shrinkWindowTimer = undefined;
  }

  /**
   * Applies D4 and F6 at the first acknowledgement.
   * @returns The start failure, or undefined when the Worker may run.
   */
  public resolveCeilingAtFirstAcknowledgement(limit: number): Error | undefined {
    this.hasCheckedRegistrationLimit = true;
    const hasLimit = limit !== 0;
    if (!this.isAdaptive && this.isMaxConcurrentTasksExplicit) {
      if (hasLimit && this.maxConcurrentTasks > limit) {
        return new Error(`maxConcurrentTasks ${this.maxConcurrentTasks} exceeds Server matching.maxConcurrentTasksCap ${limit}`);
      }
      return undefined;
    }
    let ceiling = this.configuredMaxConcurrentTasks;
    if (hasLimit && ceiling > limit) {
      ceiling = limit;
      const source = this.isMaxConcurrentTasksExplicit ? "" : ", the CPU-count default,";
      console.warn(`clamped maxConcurrentTasks ${this.configuredMaxConcurrentTasks}${source} to the Server `
        + `matching.maxConcurrentTasksCap ${limit}`);
    }
    const minOpenSlots = this.isMinOpenSlotsExplicit ? this.minOpenSlots : ceiling;
    const reservedRpcTasks = this.isReservedRpcTasksExplicit
      ? this.reservedRpcTasks : defaultReservedRpcTasks(ceiling, minOpenSlots);
    if (reservedRpcTasks >= ceiling) {
      return new Error(`reservedRpcTasks ${reservedRpcTasks} must be less than the slot ceiling ${ceiling}, which is `
        + `maxConcurrentTasks ${this.configuredMaxConcurrentTasks} limited by Server matching.maxConcurrentTasksCap ${limit}`);
    }
    if (minOpenSlots > ceiling) {
      return new Error(`minOpenSlots ${minOpenSlots} exceeds the slot ceiling ${ceiling}, which is `
        + `maxConcurrentTasks ${this.configuredMaxConcurrentTasks} limited by Server matching.maxConcurrentTasksCap ${limit}`);
    }
    this.maxConcurrentTasks = ceiling;
    this.minOpenSlots = minOpenSlots;
    this.reservedRpcTasks = reservedRpcTasks;
    this.stepFloor = minOpenSlots - reservedRpcTasks;
    this.totalCapacity = ceiling;
    return undefined;
  }

  /** The registered event of the state machine. */
  public recordRegistered(stream: PullSlotStream, limit: number): void {
    stream.slot.hasReceivedRegistered = true;
    if (stream.slot.phase === PullSlotPhase.Registering) this.setPhase(stream, PullSlotPhase.Idle);
    if (this.isAdaptive) {
      this.totalCapacity = limit !== 0 && limit < this.maxConcurrentTasks ? limit : this.maxConcurrentTasks;
    }
    let rpcOnlySlots = this.reservedRpcTasks;
    if (rpcOnlySlots + 1 > this.totalCapacity) rpcOnlySlots = Math.max(0, this.totalCapacity - 1);
    if (rpcOnlySlots < this.rpcOnlySlots) {
      console.warn(`Server matching.maxConcurrentTasksCap ${limit} leaves ${rpcOnlySlots} RPC-only slots, `
        + `fewer than reservedRpcTasks ${this.reservedRpcTasks}`);
    }
    this.rpcOnlySlots = rpcOnlySlots;
    this.closeSurplus();
    this.recomputeCapacity();
    this.maybeReopen(undefined);
    if (this.stepPhaseCounts[PullSlotPhase.Registering] === 0) this.settleGrowthBatch();
  }

  /** The task_frame event. */
  public recordTaskStarted(stream: PullSlotStream): void {
    if (stream.slot.phase === PullSlotPhase.Untracked) return;
    this.setPhase(stream, PullSlotPhase.Busy);
    if (stream.slotKind === PollWorkerSlotKind.POLL_WORKER_SLOT_KIND_RPC_ONLY) return;
    this.peakBusy = Math.max(this.peakBusy, this.phaseCount(this.stepPhaseCounts, PullSlotPhase.Busy));
    this.maybeGrow();
  }

  /** Counts a task dropped on a stream that the Worker chose to close. */
  public recordTaskDropped(stream: PullSlotStream): void {
    if (stream.slot.isClosedOnPurpose && !this.host.isWorkerStopping()) this.shrinkRaceDrops++;
  }

  /** Keeps a stream that the Server asked to reconnect out of the idle set. */
  public recordReconnectRequested(stream: PullSlotStream): void {
    if (stream.slot.phase === PullSlotPhase.Idle) this.setPhase(stream, PullSlotPhase.Reconnecting);
  }

  /**
   * The final_frame_sent event, or orphaned_handler_returned after the stream ended.
   * @returns Whether the stream must half-close because it is surplus.
   */
  public recordTaskCompleted(stream: PullSlotStream, isDraining: boolean): boolean {
    if (stream.slot.isOrphaned) {
      stream.slot.isOrphaned = false;
      if (stream.slotKind === PollWorkerSlotKind.POLL_WORKER_SLOT_KIND_RPC_ONLY) this.orphanedRpcOnlyHandlers--;
      else this.orphanedStepHandlers--;
      this.recomputeCapacity();
      this.maybeReopen(this.reopenCandidate(stream));
      return false;
    }
    if (stream.slot.phase !== PullSlotPhase.Busy) return false;
    if (isDraining) {
      this.setPhase(stream, PullSlotPhase.Reconnecting);
      return false;
    }
    this.setPhase(stream, PullSlotPhase.Idle);
    const isSurplus = stream.slotKind === PollWorkerSlotKind.POLL_WORKER_SLOT_KIND_RPC_ONLY
      ? this.rpcOnlySlotUsage() > this.rpcOnlySlots
      : this.stepSlotUsage() > Math.max(0, this.totalCapacity - this.rpcOnlySlots);
    if (!isSurplus) return false;
    this.halfCloseSlotStream(stream, false);
    this.streamsShrunk++;
    this.recomputeCapacity();
    this.maybeReopen(undefined);
    return true;
  }

  /**
   * Applies F5: only an adaptive Worker softens RESOURCE_EXHAUSTED, and only for a growth stream
   * or a reconnect after initial readiness that never registered.
   * @returns Whether the rejection is soft.
   */
  public isSoftRejection(stream: PullSlotStream, code: number): boolean {
    if (!this.isAdaptive || code !== status.RESOURCE_EXHAUSTED || stream.slot.hasReceivedRegistered) return false;
    if (stream.slot.phase !== PullSlotPhase.Registering) return false;
    return stream.slot.isGrowth || stream.slot.isOpenedAfterReadiness;
  }

  /** The soft registration_rejected event. */
  public recordRegistrationRejected(stream: PullSlotStream): void {
    stream.slot.isSoftRejected = true;
    this.setPhase(stream, PullSlotPhase.Untracked);
    if (stream.slot.isGrowth) this.hasBatchRejection = true;
    this.growRejectedAtCap++;
    this.isReopenDeferred = true;
    this.recomputeCapacity();
    if (this.stepPhaseCounts[PullSlotPhase.Registering] === 0) this.settleGrowthBatch();
  }

  /** The stream_ended event; every end of an attempt passes here once. */
  public recordStreamEnded(stream: PullSlotStream): void {
    if (stream.slot.hasEnded) return;
    stream.slot.hasEnded = true;
    if (stream.slot.phase !== PullSlotPhase.Untracked) {
      if (stream.slot.phase === PullSlotPhase.Busy) {
        stream.slot.isOrphaned = true;
        if (stream.slotKind === PollWorkerSlotKind.POLL_WORKER_SLOT_KIND_RPC_ONLY) this.orphanedRpcOnlyHandlers++;
        else this.orphanedStepHandlers++;
      }
      this.setPhase(stream, PullSlotPhase.Untracked);
    }
    if (stream.slot.hasReceivedRegistered) this.isReopenDeferred = false;
    this.recomputeCapacity();
    this.maybeReopen(stream.slot.isOrphaned ? undefined : this.reopenCandidate(stream));
    this.maybeGrow();
    if (this.stepPhaseCounts[PullSlotPhase.Registering] === 0) this.settleGrowthBatch();
  }

  /** Ends an attempt whose runner exits before the attempt ends. */
  public abandonAttempt(stream: PullSlotStream): void {
    if (stream.slot.phase === PullSlotPhase.Untracked || stream.slot.hasEnded) return;
    stream.slot.isClosedOnPurpose = true;
    this.recordStreamEnded(stream);
  }

  /**
   * Checks an attempt before its runner opens a stream, and again in the synchronous block that
   * writes its Register, because a lower cap may arrive in between. A cap drop after this check
   * reaches the Server, which enforces the cap; an adaptive Worker treats that rejection as soft.
   * @returns Whether the attempt may register; a revoked attempt has already ended.
   */
  public admitRegistration(stream: PullSlotStream): boolean {
    if (this.host.isWorkerStopping() || stream.slot.phase !== PullSlotPhase.Registering || stream.slot.hasEnded) return false;
    if (!this.isAttemptOverCapacity(stream)) return true;
    this.revokeAttempt(stream);
    this.recomputeCapacity();
    this.maybeReopen(undefined);
    if (this.stepPhaseCounts[PullSlotPhase.Registering] === 0) this.settleGrowthBatch();
    return false;
  }

  /**
   * Re-checks the attempt and marks it as sending; the caller writes the Register in the same synchronous block.
   * @returns Whether the Register may be written.
   */
  public admitRegistrationForSend(stream: PullSlotStream): boolean {
    if (!this.admitRegistration(stream)) return false;
    stream.slot.isRegisterSent = true;
    return true;
  }

  /** The every-W step of the state machine for one window generation. */
  public expireShrinkWindow(generation: number): void {
    if (this.host.isWorkerStopping() || generation !== this.shrinkWindowGeneration) return;
    this.restartShrinkWindow();
    this.isGrowthTargetClamped = false;
    this.hasGrownThisWindow = false;
    this.isReopenDeferred = false;
    this.recomputeCapacity();
    this.maybeReopen(undefined);
    this.shrink();
    this.peakBusy = this.phaseCount(this.stepPhaseCounts, PullSlotPhase.Busy);
    this.maybeGrow();
  }

  /**
   * Builds the public snapshot.
   * @returns An independent copy of the counts.
   */
  public snapshot(): PullSlotStreamState {
    return {
      isAdaptive: this.isAdaptive,
      open: this.stepSlotOpen(),
      registering: this.phaseCount(this.stepPhaseCounts, PullSlotPhase.Registering),
      busy: this.phaseCount(this.stepPhaseCounts, PullSlotPhase.Busy),
      idle: this.phaseCount(this.stepPhaseCounts, PullSlotPhase.Idle),
      orphanedHandlers: this.orphanedStepHandlers,
      orphanedRpcOnlyHandlers: this.orphanedRpcOnlyHandlers,
      floor: this.effectiveFloor,
      effectiveCeiling: this.effectiveCeiling,
      growthTarget: this.growthTarget,
      totalCapacity: this.totalCapacity,
      rpcOnlyOpen: this.rpcOnlySlotOpen(),
      rpcOnlySlots: this.rpcOnlySlots,
      streamsGrown: this.streamsGrown,
      streamsShrunk: this.streamsShrunk,
      growRejectedAtCap: this.growRejectedAtCap,
      shrinkRaceDrops: this.shrinkRaceDrops,
    };
  }

  /** Closes half the idle surplus above the window's peak plus headroom. */
  private shrink(): void {
    const peakBusy = this.peakBusy;
    const headroom = Math.max(1, Math.ceil(peakBusy / 4));
    const target = Math.min(this.effectiveCeiling, Math.max(this.effectiveFloor, peakBusy + headroom));
    const open = this.stepSlotOpen();
    if (open <= target) return;
    const idleStreams = this.idleStepStreamsNewestFirst();
    // The last idle stream stays open, and the floor stays open.
    const closeCount = Math.min(Math.ceil((open - target) / 2), idleStreams.length - 1, open - this.effectiveFloor);
    if (closeCount <= 0) return;
    for (const stream of idleStreams.slice(0, closeCount)) this.halfCloseSlotStream(stream, true);
    this.streamsShrunk += closeCount;
    debugLog("shrink pull slot streams from=%d to=%d peakBusy=%d", open, open - closeCount, peakBusy);
    this.recomputeCapacity();
  }

  /**
   * Releases capacity above a lowered cap; RPC-only surplus goes first.
   * An attempt whose Register has not left yet is revoked first, because it costs the Server nothing.
   */
  private closeSurplus(): void {
    for (const slotKind of [PollWorkerSlotKind.POLL_WORKER_SLOT_KIND_RPC_ONLY, PollWorkerSlotKind.POLL_WORKER_SLOT_KIND_STEP_AND_RPC]) {
      while (this.isSlotKindOverQuota(slotKind)) {
        const pending = this.unsentAttempt(slotKind);
        if (pending !== undefined) {
          this.revokeAttempt(pending);
          continue;
        }
        const stream = this.newestIdleStream(slotKind);
        if (stream === undefined) break;
        this.halfCloseSlotStream(stream, true);
        this.streamsShrunk++;
      }
    }
  }

  /** Checks one registering attempt, which the totals already count. */
  private isAttemptOverCapacity(stream: PullSlotStream): boolean {
    if (this.stepSlotUsage() + this.rpcOnlySlotUsage() > this.totalCapacity) return true;
    if (stream.slotKind === PollWorkerSlotKind.POLL_WORKER_SLOT_KIND_RPC_ONLY) return this.rpcOnlySlotUsage() > this.rpcOnlySlots;
    // F5 gives the first Step-and-RPC stream any free slot, so only later ones need the Step quota.
    return this.stepSlotOpen() > 1 && this.stepSlotUsage() > this.effectiveCeiling;
  }

  /** Reports surplus of a kind after a lowered cap: RPC-only above R_eff, or Step-and-RPC above C - R_eff. */
  private isSlotKindOverQuota(slotKind: PollWorkerSlotKind): boolean {
    if (slotKind === PollWorkerSlotKind.POLL_WORKER_SLOT_KIND_RPC_ONLY) return this.rpcOnlySlotUsage() > this.rpcOnlySlots;
    return this.stepSlotUsage() > Math.max(0, this.totalCapacity - this.rpcOnlySlots);
  }

  /** Takes back an attempt whose Register has not left; its runner then exits. */
  private revokeAttempt(stream: PullSlotStream): void {
    stream.slot.isClosedOnPurpose = true;
    stream.slot.hasEnded = true;
    this.setPhase(stream, PullSlotPhase.Untracked);
  }

  /** Runs when no stream registers; a rejected batch clamps G until the window ends. */
  private settleGrowthBatch(): void {
    if (this.hasBatchRejection) {
      this.hasBatchRejection = false;
      this.growthTarget = Math.min(this.effectiveCeiling, this.stepSlotUsage());
      this.isGrowthTargetClamped = true;
      console.info(`the Server rejected pull slot stream growth at its stream limit; holding growth at `
        + `growthTarget ${this.growthTarget} of effectiveCeiling ${this.effectiveCeiling} until the next shrink window`);
    }
    this.maybeGrow();
  }

  /** Doubles the open streams when a hand-out took the last idle one. */
  private maybeGrow(): void {
    if (!this.isAdaptive || this.host.isWorkerStopping() || this.isReopenDeferred) return;
    if (this.isGrowthTargetClamped && this.hasGrownThisWindow) return;
    const open = this.stepSlotOpen();
    const usage = this.stepSlotUsage();
    if (this.stepPhaseCounts[PullSlotPhase.Idle] !== 0 || this.stepPhaseCounts[PullSlotPhase.Registering] !== 0
      || usage >= this.growthTarget) return;
    const count = Math.min(this.growthTarget - usage, Math.max(1, open));
    debugLog("grow pull slot streams from=%d to=%d reason=%s", open, open + count, "no idle stream");
    for (let index = 0; index < count; index++) {
      this.openSlotStream(PollWorkerSlotKind.POLL_WORKER_SLOT_KIND_STEP_AND_RPC, true, undefined);
    }
    this.streamsGrown += count;
    this.hasGrownThisWindow = true;
    this.restartShrinkWindow();
  }

  /**
   * Opens streams within free capacity in F5 order. A parked runner of the same kind is reused,
   * so it keeps its reconnect backoff.
   */
  private maybeReopen(parked: PullSlotStream | undefined): void {
    if (this.host.isWorkerStopping() || this.isReopenDeferred) return;
    let stepOpen = this.stepSlotOpen();
    let free = Math.max(0, this.totalCapacity - (this.stepSlotUsage() + this.rpcOnlySlotUsage()));
    if (stepOpen === 0 && free > 0) {
      this.openSlotStream(PollWorkerSlotKind.POLL_WORKER_SLOT_KIND_STEP_AND_RPC, false, parked);
      stepOpen++;
      free--;
    }
    if (this.isCapProbePending()) return;
    if (this.hasCheckedRegistrationLimit || !this.isAdaptive) {
      const count = Math.min(free, Math.max(0, this.rpcOnlySlots - this.rpcOnlySlotUsage()));
      for (let index = 0; index < count; index++) {
        this.openSlotStream(PollWorkerSlotKind.POLL_WORKER_SLOT_KIND_RPC_ONLY, false, parked);
      }
      free -= count;
    }
    const count = Math.min(free, Math.max(0,
      Math.min(this.effectiveFloor, this.effectiveCeiling - this.orphanedStepHandlers) - stepOpen));
    for (let index = 0; index < count; index++) {
      this.openSlotStream(PollWorkerSlotKind.POLL_WORKER_SLOT_KIND_STEP_AND_RPC, false, parked);
    }
  }

  /** Counts a new attempt as registering and grants it to parked when possible. */
  private openSlotStream(slotKind: PollWorkerSlotKind, isGrowth: boolean, parked: PullSlotStream | undefined): void {
    const attempt = newPullSlotAttempt(isGrowth, this.host.hasRegisteredAllSlotKinds());
    if (!isGrowth && parked !== undefined && parked.slotKind === slotKind && !parked.slot.isReopenGranted) {
      attempt.isReopenGranted = true;
      parked.slot = attempt;
      this.setPhase(parked, PullSlotPhase.Registering);
      return;
    }
    const stream = this.host.openSlotRunner(slotKind, attempt);
    this.setPhase(stream, PullSlotPhase.Registering);
  }

  /** Takes an idle stream out of the totals and half-closes it; its later end changes no count. */
  private halfCloseSlotStream(stream: PullSlotStream, shouldCloseNow: boolean): void {
    this.setPhase(stream, PullSlotPhase.Untracked);
    stream.slot.isClosedOnPurpose = true;
    this.host.halfCloseSlotStream(stream, shouldCloseNow);
  }

  /** Starts a new shrink window; an older window's timer then does nothing. */
  private restartShrinkWindow(): void {
    if (!this.isAdaptive || this.settings.shrinkWindowMs <= 0) return;
    const generation = ++this.shrinkWindowGeneration;
    if (this.shrinkWindowTimer !== undefined) clearTimeout(this.shrinkWindowTimer);
    this.shrinkWindowTimer = setTimeout(() => this.expireShrinkWindow(generation), this.settings.shrinkWindowMs);
    this.shrinkWindowTimer.unref();
  }

  /** Moves an attempt between phases and keeps the per-phase totals. */
  private setPhase(stream: PullSlotStream, phase: PullSlotPhase): void {
    const counts = stream.slotKind === PollWorkerSlotKind.POLL_WORKER_SLOT_KIND_RPC_ONLY
      ? this.rpcOnlyPhaseCounts : this.stepPhaseCounts;
    if (stream.slot.phase !== PullSlotPhase.Untracked) counts[stream.slot.phase] = this.phaseCount(counts, stream.slot.phase) - 1;
    stream.slot.phase = phase;
    if (phase !== PullSlotPhase.Untracked) counts[phase] = this.phaseCount(counts, phase) + 1;
    if (phase === PullSlotPhase.Idle) stream.slot.idleSequence = ++this.nextIdleSequence;
  }

  /** Derives E, L_eff, and G; RPC-only overflow takes Step capacity. */
  private recomputeCapacity(): void {
    const rpcOnlyReserve = Math.max(this.rpcOnlySlots, this.rpcOnlySlotUsage());
    this.effectiveCeiling = Math.max(0, this.totalCapacity - rpcOnlyReserve);
    this.effectiveFloor = Math.max(0, Math.min(this.stepFloor, this.effectiveCeiling));
    this.growthTarget = this.isGrowthTargetClamped
      ? Math.min(this.growthTarget, this.effectiveCeiling) : this.effectiveCeiling;
  }

  /**
   * Checks whether the stream's runner may serve the next attempt of its kind.
   * @returns The stream, or undefined when its runner must exit.
   */
  private reopenCandidate(stream: PullSlotStream): PullSlotStream | undefined {
    if (stream.slot.isClosedOnPurpose || stream.slot.isSoftRejected || stream.slot.isReopenGranted) return undefined;
    return stream;
  }

  private idleStepStreamsNewestFirst(): PullSlotStream[] {
    const idleStreams: PullSlotStream[] = [];
    for (const stream of this.host.listSlotStreams()) {
      if (stream.slotKind !== PollWorkerSlotKind.POLL_WORKER_SLOT_KIND_RPC_ONLY && stream.slot.phase === PullSlotPhase.Idle) {
        idleStreams.push(stream);
      }
    }
    return idleStreams.sort((first, second) => second.slot.idleSequence - first.slot.idleSequence);
  }

  private unsentAttempt(slotKind: PollWorkerSlotKind): PullSlotStream | undefined {
    for (const stream of this.host.listSlotStreams()) {
      if (stream.slotKind === slotKind && stream.slot.phase === PullSlotPhase.Registering && !stream.slot.isRegisterSent) {
        return stream;
      }
    }
    return undefined;
  }

  private newestIdleStream(slotKind: PollWorkerSlotKind): PullSlotStream | undefined {
    let newest: PullSlotStream | undefined;
    for (const stream of this.host.listSlotStreams()) {
      if (stream.slotKind === slotKind && stream.slot.phase === PullSlotPhase.Idle
        && (newest === undefined || stream.slot.idleSequence > newest.slot.idleSequence)) {
        newest = stream;
      }
    }
    return newest;
  }

  /** F4: a fixed Worker with an unset ceiling opens one stream until the first acknowledgement. */
  private isCapProbePending(): boolean {
    return !this.isAdaptive && !this.isMaxConcurrentTasksExplicit && !this.hasCheckedRegistrationLimit;
  }

  /** S. */
  private stepSlotOpen(): number {
    return this.openCount(this.stepPhaseCounts);
  }

  /** S + H. */
  private stepSlotUsage(): number {
    return this.stepSlotOpen() + this.orphanedStepHandlers;
  }

  /** Sr. */
  private rpcOnlySlotOpen(): number {
    return this.openCount(this.rpcOnlyPhaseCounts);
  }

  /** Sr + Hr. */
  private rpcOnlySlotUsage(): number {
    return this.rpcOnlySlotOpen() + this.orphanedRpcOnlyHandlers;
  }

  private openCount(counts: readonly number[]): number {
    return this.phaseCount(counts, PullSlotPhase.Registering) + this.phaseCount(counts, PullSlotPhase.Idle)
      + this.phaseCount(counts, PullSlotPhase.Busy) + this.phaseCount(counts, PullSlotPhase.Reconnecting);
  }

  private phaseCount(counts: readonly number[], phase: PullSlotPhase): number {
    return counts[phase] ?? 0;
  }
}
