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
 * Reports a Dex API failure translated from a gRPC response.
 *
 * <p>Inspect the standard gRPC {@link #getCode()} first and the optional Dex
 * {@link #getSubStatus()} when application recovery needs a more precise category. Prefer a
 * concrete subclass such as {@link FlowNotFoundException} for expected outcomes and use this base
 * type for diagnostics or uncategorized service failures.
 *
 * <pre>{@code
 * try {
 *     client.describeFlow("order-123");
 * } catch (FlowNotFoundException missing) {
 *     createOrder();
 * } catch (DexServiceException serviceFailure) {
 *     log.error("Dex {}: {}", serviceFailure.getCode(), serviceFailure.getDetail());
 * }
 * }</pre>
 */
public class DexServiceException extends RuntimeException {
    private final Status.Code code;
    private final ErrorSubStatus subStatus;
    private final String detail;

    /**
     * Creates a service exception with transport and Dex-specific status metadata.
     *
     * @param code the nonnull gRPC status code
     * @param subStatus the Dex-specific category, or {@code null} when unavailable
     * @param detail the server-provided error detail, which may be {@code null}
     * @param cause the original transport failure, which may be {@code null}
     */
    public DexServiceException(
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
