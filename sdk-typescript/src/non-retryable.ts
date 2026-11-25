// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

/**
 * Fails the current Step method attempt without further retries.
 *
 * Throw it from `waitFor` or `execute` when a later attempt cannot succeed, such as
 * rejected input or a request that a provider reports as permanent. Dex skips the
 * remaining attempts of the method's retry policy and applies the Step's failure
 * policy at once. By default the Flow fails. A `waitFor` configured to proceed on
 * failure runs `execute`, and an `execute` configured to proceed to a recovery Step
 * starts that Step.
 *
 * Pass the original `Error` to report it as the Worker error, so the recovery
 * Step's `recoveryError` shows its name, message, and stack. Pass a message instead
 * to report this error itself. Subclasses are also non-retryable.
 *
 * It may wrap or be wrapped by a {@link RetryAfterError}; the non-retryable request
 * wins and no retry runs. Thrown from an RPC handler, it has no effect, because RPC
 * handlers never retry.
 *
 * It needs server-zd 0.1.38 or later. An older server ignores the request and
 * retries the attempt under the retry policy.
 *
 * @example
 * ```ts
 * public async execute(context: Context, order: Order): Promise<StepDecision> {
 *   try {
 *     return gracefulComplete(await payments.charge(order));
 *   } catch (error) {
 *     if (error instanceof CardRejectedError) {
 *       throw new NonRetryableError(error);
 *     }
 *     throw error;
 *   }
 * }
 * ```
 *
 * @since 1.45.17
 */
export class NonRetryableError extends Error {
  /**
   * Creates a non-retryable failure.
   * @param causeOrMessage - The failure to report to Dex, or a message that describes it.
   * @throws TypeError when `causeOrMessage` is neither an `Error` nor a string.
   */
  public constructor(causeOrMessage: Error | string) {
    if (causeOrMessage instanceof Error) {
      super(causeOrMessage.message, { cause: causeOrMessage });
    } else if (typeof causeOrMessage === "string") {
      super(causeOrMessage);
    } else {
      throw new TypeError("causeOrMessage must be an Error or a string");
    }
    this.name = "NonRetryableError";
  }
}
