/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

package io.superdurable.dex.exceptions;

import java.time.Duration;

/**
 * Requests a specific delay before retrying a failed Step method.
 *
 * <p>Throw the value returned by {@link #after(Duration, Throwable)} from wait-for or execute. On
 * Temporal, the delay overrides only the next retry interval; retry limits and timeouts remain
 * unchanged, and the current attempt failure remains the reported Worker error. Cadence rejects
 * dynamic retry intervals with a Flow validation error.
 *
 * <pre>{@code
 * throw RetryAfterException.after(Duration.ofSeconds(30), currentFailure);
 * }</pre>
 */
public final class RetryAfterException extends RuntimeException {
    private final Duration retryAfter;

    private RetryAfterException(final Duration retryAfter, final Throwable currentCause) {
        super(currentCause.getMessage(), currentCause);
        this.retryAfter = retryAfter;
    }

    /**
     * Creates a retry request while preserving the current attempt failure.
     *
     * @param retryAfter the positive whole-second delay before the next attempt
     * @param currentCause the current Step method attempt failure reported to Dex
     * @return an exception to throw from the Step method
     * @throws IllegalArgumentException if the delay is null, nonpositive, fractional, or exceeds
     *         the signed 32-bit seconds range, or if the current cause is null
     */
    public static RetryAfterException after(
            final Duration retryAfter,
            final Throwable currentCause) {
        if (retryAfter == null
                || retryAfter.isZero()
                || retryAfter.isNegative()
                || retryAfter.getNano() != 0
                || retryAfter.getSeconds() > Integer.MAX_VALUE) {
            throw new IllegalArgumentException(
                    "retryAfter must be positive whole seconds within int32");
        }
        if (currentCause == null) {
            throw new IllegalArgumentException("currentCause is required");
        }
        return new RetryAfterException(retryAfter, currentCause);
    }

    /**
     * Returns the requested delay before the next retry.
     *
     * @return the positive whole-second retry delay
     */
    public Duration getRetryAfter() {
        return retryAfter;
    }
}
