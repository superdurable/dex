/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Sustainable Use License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0
 */

package io.superdurable.dex.exceptions;

/**
 * Fails the current Step method attempt without further retries.
 *
 * <p>Throw it from wait-for or execute when a later attempt cannot succeed, such as rejected
 * input or a request that a provider reports as permanent. Dex skips the remaining attempts of
 * the method's retry policy and applies the Step's failure policy at once. By default the Flow
 * fails. A wait-for configured to proceed on failure runs execute, and an execute configured to
 * proceed to a recovery Step starts that Step.
 *
 * <p>Create it with a cause to report that cause as the Worker error, so the recovery Step's
 * recovery error shows its class, message, and stack trace. Create it with a message instead to
 * report this exception itself. Subclasses are also non-retryable.
 *
 * <p>It may wrap or be wrapped by a {@link RetryAfterException}; the non-retryable request wins
 * and no retry runs. It also counts anywhere in the causes or suppressed exceptions of the
 * thrown exception, such as {@code new IllegalStateException("charge failed", marked)} or a
 * buffered Stream finalizer failure that Dex adds as suppressed; Dex then reports the outer
 * exception. Thrown from an RPC handler, it has no effect, because RPC handlers never retry.
 *
 * <p>It needs server-zd 0.1.38 or later. An older server ignores the request and retries the
 * attempt under the retry policy.
 *
 * <pre>{@code
 * public StepDecision execute(final Context context, final Order order) {
 *     try {
 *         return StepDecision.gracefulComplete(payments.charge(order));
 *     } catch (final CardRejectedException rejected) {
 *         throw new NonRetryableException(rejected);
 *     }
 * }
 * }</pre>
 *
 * @since 1.45.4
 */
public class NonRetryableException extends RuntimeException {
    private static final long serialVersionUID = 1L;

    /**
     * Creates a non-retryable failure that reports {@code cause} as the Worker error.
     *
     * @param cause the Step method failure reported to Dex
     * @throws IllegalArgumentException if {@code cause} is null
     */
    public NonRetryableException(final Throwable cause) {
        super(requireCause(cause).getMessage(), cause);
    }

    /**
     * Creates a non-retryable failure that reports this exception as the Worker error.
     *
     * @param message the failure message reported to Dex
     */
    public NonRetryableException(final String message) {
        super(message);
    }

    private static Throwable requireCause(final Throwable cause) {
        if (cause == null) {
            throw new IllegalArgumentException("cause is required");
        }
        return cause;
    }
}
