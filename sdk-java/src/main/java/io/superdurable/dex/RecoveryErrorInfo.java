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

/**
 * Describes the final failure that caused a Step method recovery.
 *
 * <p>Read this value from {@link Context#getRecoveryError()}. Errors returned by a Worker preserve
 * their original Java type name and detail. Backend failures such as timeouts use the Temporal or
 * Cadence failure type.
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

    private RecoveryErrorInfo(final String detail, final String errorType) {
        this.detail = detail;
        this.errorType = errorType;
    }

    static RecoveryErrorInfo fromProto(final io.superdurable.gen.RecoveryErrorInfo error) {
        return new RecoveryErrorInfo(error.getDetail(), error.getErrorType());
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
