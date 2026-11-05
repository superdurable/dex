/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Sustainable Use License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0
 */

package io.superdurable.dex;

/**
 * Describes the final failure that caused a Step method recovery.
 *
 * <p>Read this value from {@link Context#getRecoveryError()}. Errors returned by a Worker preserve
 * their original Java type name and detail. Server failures retain their failure type.
 * Read {@link #getAttemptProgress()} before deciding whether side effects may have happened.
 *
 * <pre>{@code
 * RecoveryErrorInfo error = context.getRecoveryError();
 * if (error != null) {
 *     logger.warn("Recovering from {}: {}", error.getErrorType(), error.getDetail());
 * }
 * }</pre>
 */
public final class RecoveryErrorInfo {
    private final String detail;
    private final String errorType;
    private final StepAttemptProgress attemptProgress;

    private RecoveryErrorInfo(final String detail, final String errorType,
            final StepAttemptProgress attemptProgress) {
        this.detail = detail;
        this.errorType = errorType;
        this.attemptProgress = attemptProgress;
    }

    static RecoveryErrorInfo fromProto(final io.superdurable.gen.RecoveryErrorInfo error) {
        return new RecoveryErrorInfo(error.getDetail(), error.getErrorType(),
                StepAttemptProgress.fromProto(error.getAttemptProgress()));
    }

    /**
     * Returns the furthest progress across all attempts of the failed Step method.
     *
     * <p>{@link StepAttemptProgress#NOT_STARTED} means the handler did not run in any attempt.
     * Other values require recovery code to allow for side effects from an incomplete attempt.
     *
     * <pre>{@code
     * RecoveryErrorInfo error = context.getRecoveryError();
     * if (error != null && error.getAttemptProgress() == StepAttemptProgress.NOT_STARTED) {
     *     logger.info("No handler side effects need reconciliation");
     * }
     * }</pre>
     *
     * @return the progress reported by the Server; never null
     */
    public StepAttemptProgress getAttemptProgress() {
        return attemptProgress;
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
}
