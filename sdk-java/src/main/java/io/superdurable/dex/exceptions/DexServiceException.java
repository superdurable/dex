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

import io.grpc.Status;

class DexServiceException extends RuntimeException {
    private final Status.Code code;
    private final ErrorSubStatus subStatus;
    private final String detail;

    DexServiceException(
            final Status.Code code,
            final ErrorSubStatus subStatus,
            final String detail,
            final Throwable cause) {
        super(detail, cause);
        this.code = code;
        this.subStatus = subStatus;
        this.detail = detail;
    }

    /**
     * Returns the standard gRPC status code.
     *
     * @return the nonnull gRPC code
     */
    public Status.Code getCode() {
        return code;
    }

    /**
     * Returns the Dex-specific error category.
     *
     * @return the category, or {@code null} when none was supplied or recognized
     */
    public ErrorSubStatus getSubStatus() {
        return subStatus;
    }

    /**
     * Returns the server-provided error detail.
     *
     * @return the detail, which may be {@code null}
     */
    public String getDetail() {
        return detail;
    }
}
