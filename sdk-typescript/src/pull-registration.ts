// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { ServiceError } from "@grpc/grpc-js";

/** Identifies a permanent rejection that leaves a required slot kind without a registered stream. */
export class PullRegistrationError extends Error {
  /** Rejected slot kind: STEP_AND_RPC or RPC_ONLY; retained field name for compatibility. */
  public readonly queue: string;

  /**
   * Creates a slot-kind rejection preserving the original gRPC error as `cause`.
   * @param queue - Rejected slot kind: STEP_AND_RPC or RPC_ONLY.
   * @param cause - Permanent gRPC stream error, including its status code and details.
   */
  public constructor(queue: string, cause: ServiceError) {
    super(`pull slot kind ${queue} rejected: ${cause.message}`, { cause });
    this.name = "PullRegistrationError";
    this.queue = queue;
  }
}

/** Independent snapshot of one slot kind; error objects must be treated as immutable. */
export interface PullQueueRegistrationState {
  /** Slot kind: STEP_AND_RPC or RPC_ONLY; retained field name for compatibility. */
  readonly queue: string;
  /** Whether this slot kind currently has an acknowledged stream. */
  readonly hasRegisteredStream: boolean;
  /** Last permanent rejection, cleared by successful registration; absent when none remains. */
  readonly lastPermanentError: ServiceError | undefined;
}

/** Independent readiness snapshot returned by Worker.pullRegistrationState. */
export interface PullRegistrationState {
  /** Latches after a STEP_AND_RPC stream and, when reserved, an RPC_ONLY stream register; false on a permanent error without a registered stream of that kind. */
  readonly isReady: boolean;
  /** Slot-kind snapshots in registration order: STEP_AND_RPC, then RPC_ONLY when configured. */
  readonly queues: readonly PullQueueRegistrationState[];
}

/** Optional cancellation and deadline for Worker.waitPullRegistered. */
export interface WaitPullRegisteredOptions {
  /** Cancels only this wait with the signal's reason; defaults to no cancellation. */
  readonly signal?: AbortSignal;
  /** Positive finite timeout in milliseconds; omitted means no deadline. */
  readonly timeoutMs?: number;
}

interface QueueRegistration {
  hasRegistered: boolean;
  registeredStreams: number;
  lastPermanentError: ServiceError | undefined;
}

export class PullRegistration {
  private readonly queues: Map<string, QueueRegistration>;
  private readonly listeners = new Set<RegistrationWaiter>();
  private hasRegisteredAllQueues = false;
  private initialError: unknown;
  private hasStopped = false;
  public permanentError: PullRegistrationError | undefined;

  public constructor(queues: readonly string[]) {
    this.queues = new Map(queues.map((queue) => [queue, {
      hasRegistered: false, registeredStreams: 0, lastPermanentError: undefined,
    }]));
  }

  public registered(queue: string): void {
    const registration = this.queues.get(queue)!;
    registration.hasRegistered = true;
    registration.registeredStreams++;
    registration.lastPermanentError = undefined;
    this.hasRegisteredAllQueues ||= [...this.queues.values()].every((entry) => entry.hasRegistered);
    this.notify();
  }

  public ended(queue: string, wasRegistered: boolean, failure?: ServiceError): boolean {
    const registration = this.queues.get(queue)!;
    if (wasRegistered) registration.registeredStreams--;
    if (failure !== undefined) {
      registration.lastPermanentError = failure;
      if (registration.registeredStreams === 0) {
        const rejection = new PullRegistrationError(queue, failure);
        if (!this.hasRegisteredAllQueues) this.initialError ??= rejection;
        this.permanentError ??= rejection;
        this.notify();
        return true;
      }
    }
    this.notify();
    return false;
  }

  public stopped(failure?: unknown): void {
    this.initialError ??= failure;
    this.hasStopped = true;
    this.notify();
  }

  public snapshot(): PullRegistrationState {
    const queues = [...this.queues].map(([queue, registration]) => ({
      queue, hasRegisteredStream: registration.registeredStreams > 0,
      lastPermanentError: registration.lastPermanentError,
    }));
    return {
      isReady: this.hasRegisteredAllQueues && !queues.some((queue) =>
        !queue.hasRegisteredStream && queue.lastPermanentError !== undefined),
      queues,
    };
  }

  public wait(options: WaitPullRegisteredOptions = {}): Promise<void> {
    const { signal, timeoutMs } = options;
    if (timeoutMs !== undefined && (!Number.isFinite(timeoutMs) || timeoutMs <= 0)) {
      return Promise.reject(new RangeError("timeoutMs must be positive and finite"));
    }
    return new RegistrationWaiter(this, options).wait();
  }

  public addWaiter(waiter: RegistrationWaiter): void {
    this.listeners.add(waiter);
  }

  public removeWaiter(waiter: RegistrationWaiter): void {
    this.listeners.delete(waiter);
  }

  public checkWaiter(waiter: RegistrationWaiter): void {
    if (this.hasRegisteredAllQueues) waiter.finish();
    else if (this.initialError !== undefined) waiter.finish(this.initialError);
    else if (this.hasStopped) waiter.finish(new Error("pull Worker stopped before registration"));
  }

  private notify(): void {
    for (const listener of this.listeners) this.checkWaiter(listener);
  }
}

class RegistrationWaiter {
  private timer: ReturnType<typeof setTimeout> | undefined;
  private resolve!: () => void;
  private reject!: (failure: unknown) => void;
  private hasFinished = false;
  private readonly abort = this.onAbort.bind(this);
  private readonly timeout = this.onTimeout.bind(this);
  private readonly deadlineMs: number | undefined;

  public constructor(
    private readonly registration: PullRegistration,
    private readonly options: WaitPullRegisteredOptions,
  ) {
    this.deadlineMs = options.timeoutMs === undefined ? undefined : Date.now() + options.timeoutMs;
  }

  public wait(): Promise<void> {
    return new Promise((resolve, reject) => {
      this.resolve = resolve;
      this.reject = reject;
      this.registration.addWaiter(this);
      this.options.signal?.addEventListener("abort", this.abort, { once: true });
      this.registration.checkWaiter(this);
      if (!this.hasFinished && this.options.signal?.aborted === true) this.onAbort();
      if (!this.hasFinished && this.deadlineMs !== undefined) this.onTimeout();
    });
  }

  public finish(failure?: unknown): void {
    if (this.hasFinished) return;
    this.hasFinished = true;
    this.registration.removeWaiter(this);
    this.options.signal?.removeEventListener("abort", this.abort);
    if (this.timer !== undefined) clearTimeout(this.timer);
    if (failure === undefined) this.resolve(); else this.reject(failure);
  }

  private onAbort(): void {
    this.finish(this.options.signal?.reason);
  }

  private onTimeout(): void {
    const remainingMs = this.deadlineMs! - Date.now();
    if (remainingMs <= 0) {
      this.finish(new DOMException("Pull registration timed out", "TimeoutError"));
    } else {
      this.timer = setTimeout(this.timeout, Math.min(remainingMs, 2_147_483_647));
    }
  }
}
