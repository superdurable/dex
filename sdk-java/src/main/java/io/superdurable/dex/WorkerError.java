/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

package io.superdurable.dex;

import java.time.Duration;

/**
 * Describes the final failure that caused a Step method recovery.
 *
 * <p>Read this value from {@link Context#getRecoveryError()}. Errors returned by a Worker preserve
 * their original Java type name, detail, and stack trace. Backend failures such as timeouts use the
 * Temporal or Cadence failure type and may not contain a stack trace. Retry-after is present only
 * when the failing Worker supplied an explicit retry delay.
 *
 * <pre>{@code
 * WorkerError error = context.getRecoveryError();
 * if (error != null) {
 *     logger.warn("Recovering from {}: {}", error.getErrorType(), error.getDetail());
 * }
 * }</pre>
 */
public final class WorkerError {
    private final String detail;
    private final String errorType;
    private final String stackTrace;
    private final Duration retryAfter;

    private WorkerError(
            final String detail,
            final String errorType,
            final String stackTrace,
            final Duration retryAfter) {
        this.detail = detail;
        this.errorType = errorType;
        this.stackTrace = stackTrace;
        this.retryAfter = retryAfter;
    }

    static WorkerError fromProto(final io.superdurable.gen.WorkerErrorResponse error) {
        final Duration retryAfter = error.getRetryAfterSeconds() > 0
                ? Duration.ofSeconds(error.getRetryAfterSeconds())
                : null;
        return new WorkerError(
                error.getDetail(),
                error.getErrorType(),
                error.getStackTrace(),
                retryAfter);
    }

    /**
     * Returns the failure detail supplied by the Worker or backend.
     *
     * @return the failure detail, or an empty string when unavailable
     */
    public String getDetail() {
        return detail;
    }

    /**
     * Returns the original Worker exception type or backend failure type.
     *
     * @return the error type, or an empty string when unavailable
     */
    public String getErrorType() {
        return errorType;
    }

    /**
     * Returns the original Worker-side stack trace.
     *
     * @return the stack trace, or an empty string for backend failures and unavailable traces
     */
    public String getStackTrace() {
        return stackTrace;
    }

    /**
     * Returns the retry delay requested by the failing Worker attempt.
     *
     * @return the requested retry delay, or {@code null} when none was supplied
     */
    public Duration getRetryAfter() {
        return retryAfter;
    }
}
