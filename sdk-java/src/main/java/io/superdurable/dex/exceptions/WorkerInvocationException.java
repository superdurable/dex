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

import io.grpc.Status;

public final class WorkerInvocationException extends DexException {
    private final String workerErrorType;
    private final String workerErrorDetail;
    private final Status.Code workerCode;

    public WorkerInvocationException(
            final Status.Code code,
            final String detail,
            final String workerErrorType,
            final String workerErrorDetail,
            final Status.Code workerCode,
            final Throwable cause) {
        super(code, ErrorSubStatus.WORKER_API_ERROR, detail, cause);
        this.workerErrorType = workerErrorType;
        this.workerErrorDetail = workerErrorDetail;
        this.workerCode = workerCode;
    }

    public String getWorkerErrorType() {
        return workerErrorType;
    }

    public String getWorkerErrorDetail() {
        return workerErrorDetail;
    }

    public Status.Code getWorkerCode() {
        return workerCode;
    }
}
