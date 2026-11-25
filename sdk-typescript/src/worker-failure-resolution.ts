// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { PayloadEncryptionError } from "./errors.js";
import { NonRetryableError } from "./non-retryable.js";
import { RetryAfterError } from "./retry-after.js";

/** Bounds the members read from one AggregateError; a hostile `length` must not stall the Worker. */
const maxAggregateMembersRead = 10_000;

/** What a handler failure tells the Worker, read once so that getters cannot change the answer. */
export interface WorkerFailureResolution {
  /** The first failure under the RetryAfterError and NonRetryableError wrappers. */
  readonly reported: unknown;
  /** The outermost RetryAfterError around `reported`, if any. */
  readonly retryAfterError: RetryAfterError | undefined;
  /** Whether a NonRetryableError appears anywhere in the `cause` chains or AggregateError members. */
  readonly isNonRetryable: boolean;
  /** The first PayloadEncryptionError anywhere in the same graph; its text must never be sent in clear. */
  readonly payloadEncryptionError: PayloadEncryptionError | undefined;
}

/**
 * Reads a handler failure once, without reading name, message, or stack.
 * Never throws: Proxy traps and throwing getters count as absent values, and a failure part way
 * keeps every finding made before it.
 * @returns The resolution of `failure`.
 */
export function resolveWorkerFailure(failure: unknown): WorkerFailureResolution {
  return new WorkerFailureGraph(failure).resolve();
}

/** One read of a failure graph; each `cause` and member is read at most once. */
class WorkerFailureGraph {
  private readonly causes = new Map<object, unknown>();
  private isNonRetryable = false;
  private payloadEncryptionError: PayloadEncryptionError | undefined;

  public constructor(private readonly failure: unknown) {}

  public resolve(): WorkerFailureResolution {
    let reported = this.failure;
    let retryAfterError: RetryAfterError | undefined;
    try {
      this.scanCausesAndMembers();
      ({ reported, retryAfterError } = this.unwrapControlWrappers());
    } catch {
      // Keep the findings made so far; the reported failure falls back to the original one.
    }
    return {
      reported,
      retryAfterError,
      isNonRetryable: this.isNonRetryable,
      payloadEncryptionError: this.payloadEncryptionError,
    };
  }

  private scanCausesAndMembers(): void {
    const pending: unknown[] = [this.failure];
    const visited = new Set<object>();
    while (pending.length > 0) {
      const current = pending.pop();
      if (typeof current !== "object" || current === null || visited.has(current)) continue;
      visited.add(current);
      if (isInstanceWithoutThrowing(current, PayloadEncryptionError)) {
        this.payloadEncryptionError ??= current as PayloadEncryptionError;
      }
      if (isInstanceWithoutThrowing(current, NonRetryableError)) {
        this.isNonRetryable = true;
      }
      pending.push(this.readCause(current));
      if (isInstanceWithoutThrowing(current, AggregateError)) {
        this.pushAggregateMembers(current, pending);
      }
    }
  }

  private pushAggregateMembers(group: object, pending: unknown[]): void {
    const members = readPropertyWithoutThrowing(group, "errors");
    const length = readPropertyWithoutThrowing(members, "length");
    if (typeof length !== "number" || !Number.isFinite(length)) return;
    const count = Math.min(length, maxAggregateMembersRead);
    for (let index = 0; index < count; index += 1) {
      pending.push(readPropertyWithoutThrowing(members, String(index)));
    }
  }

  private unwrapControlWrappers(): { reported: unknown; retryAfterError: RetryAfterError | undefined } {
    let reported = this.failure;
    let retryAfterError: RetryAfterError | undefined;
    const unwrapped = new Set<unknown>();
    while (!unwrapped.has(reported)) {
      unwrapped.add(reported);
      const isRetryAfter = isInstanceWithoutThrowing(reported, RetryAfterError);
      if (!isRetryAfter && !isInstanceWithoutThrowing(reported, NonRetryableError)) break;
      if (isRetryAfter) retryAfterError ??= reported as RetryAfterError;
      const cause = this.readCause(reported);
      if (!isInstanceWithoutThrowing(cause, Error)) break;
      reported = cause;
    }
    return { reported, retryAfterError };
  }

  private readCause(value: unknown): unknown {
    if (typeof value !== "object" || value === null) return undefined;
    if (!this.causes.has(value)) {
      this.causes.set(value, readPropertyWithoutThrowing(value, "cause"));
    }
    return this.causes.get(value);
  }
}

function isInstanceWithoutThrowing(value: unknown, type: abstract new (...args: never[]) => unknown): boolean {
  try {
    return value instanceof type;
  } catch {
    return false;
  }
}

function readPropertyWithoutThrowing(value: unknown, property: string): unknown {
  if (typeof value !== "object" || value === null) return undefined;
  try {
    return Reflect.get(value, property);
  } catch {
    return undefined;
  }
}
