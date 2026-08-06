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

import io.grpc.Status;

public final class DexException extends RuntimeException {
    private final Status.Code code;
    private final ErrorSubStatus subStatus;
    private final String detail;
    private final String workerErrorType;
    private final String workerErrorDetail;

    DexException(
            final Status.Code code,
            final ErrorSubStatus subStatus,
            final String detail,
            final String workerErrorType,
            final String workerErrorDetail,
            final Throwable cause) {
        super(detail, cause);
        this.code = code;
        this.subStatus = subStatus;
        this.detail = detail;
        this.workerErrorType = workerErrorType;
        this.workerErrorDetail = workerErrorDetail;
    }

    public Status.Code getCode() {
        return code;
    }

    public ErrorSubStatus getSubStatus() {
        return subStatus;
    }

    public String getDetail() {
        return detail;
    }

    public String getWorkerErrorType() {
        return workerErrorType;
    }

    public String getWorkerErrorDetail() {
        return workerErrorDetail;
    }
}
