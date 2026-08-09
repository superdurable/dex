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
 * Reports that a Flow start request conflicts with an existing Flow ID.
 *
 * <p>Catch this exception when duplicate starts are an expected idempotency outcome. The applicable
 * {@link io.superdurable.dex.IdReusePolicy} and the state of the existing execution determine
 * whether Dex rejects the request.
 */
public final class FlowAlreadyStartedException extends DexServiceException {
    /**
     * Creates a duplicate-Flow exception from a Dex service response.
     *
     * @param code the gRPC status code returned by Dex
     * @param detail the server-provided conflict detail
     * @param cause the original transport failure
     */
    public FlowAlreadyStartedException(
            final Status.Code code,
            final String detail,
            final Throwable cause) {
        super(code, ErrorSubStatus.FLOW_ALREADY_STARTED, detail, cause);
    }
}
