// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

/**
 * Requests a delay before the next retry while preserving the current failure.
 *
 * Throw the value returned by {@link retryAfter} from `waitFor` or `execute`. Dex
 * schedules the next retry after the requested whole-second delay while keeping
 * the wrapped failure as the reported Worker error.
 */
export class RetryAfterError extends Error {
  /**
   * Creates a retry-after error.
   * @param afterSeconds - Positive whole-second delay before the next attempt.
   * @param cause - Current Step method failure reported to Dex.
   */
  public constructor(
    public readonly afterSeconds: number,
    cause: Error,
  ) {
    super(cause.message, { cause });
    this.name = "RetryAfterError";
  }
}

/**
 * Creates a retry request while preserving the current attempt failure.
 * @param afterSeconds - Positive whole-second delay before the next attempt.
 * @param cause - Current Step method failure reported to Dex.
 * @returns An error to throw from the Step method.
 */
export function retryAfter(afterSeconds: number, cause: Error): RetryAfterError {
  if (!Number.isInteger(afterSeconds) || afterSeconds <= 0) {
    throw new RangeError("afterSeconds must be a positive whole number of seconds");
  }
  if (!(cause instanceof Error)) {
    throw new TypeError("cause must be an Error");
  }
  return new RetryAfterError(afterSeconds, cause);
}
