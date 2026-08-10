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

/**
 * Reports that a Java worker failed while executing a Step or RPC invocation.
 *
 * <p>The exception preserves both the outer Dex service status and, when supplied, the original
 * worker exception type, detail, and gRPC status. These fields are intended for diagnostics; user
 * code should not depend on Java exception class names as a durable protocol.
 */
public final class WorkerInvocationException extends DexServiceException {
    private final String workerErrorType;
    private final String workerErrorDetail;
    private final Status.Code workerCode;

    /**
     * Creates a worker failure with its original worker metadata.
     *
     * @param code the outer Dex service gRPC status code
     * @param detail the outer Dex service error detail
     * @param workerErrorType the original worker exception type, or an empty string
     * @param workerErrorDetail the original worker exception detail, or an empty string
     * @param workerCode the original worker gRPC status code, or {@code null} when unavailable
     * @param cause the original client transport failure
     */
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

    /**
     * Returns the original worker exception type.
     *
     * @return the worker error type, or an empty string when unavailable
     */
    public String getWorkerErrorType() {
        return workerErrorType;
    }

    /**
     * Returns the original worker exception detail.
     *
     * @return the worker error detail, or an empty string when unavailable
     */
    public String getWorkerErrorDetail() {
        return workerErrorDetail;
    }

    /**
     * Returns the original worker-side gRPC status code.
     *
     * @return the worker status code, or {@code null} when unavailable
     */
    public Status.Code getWorkerCode() {
        return workerCode;
    }
}
