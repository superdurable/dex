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
 * Reports that a read or history operation cannot find a matching Flow execution.
 *
 * <p>Methods such as describe, Attribute read, Flow wait, search-adjacent history access, and reset
 * can inspect closed Flows, so this exception means no requested execution was found. Operations
 * that specifically require a running target use {@link FlowNotActiveException} instead.
 */
public final class FlowNotFoundException extends DexServiceException {
    /**
     * Creates a missing-Flow exception from a Dex service response.
     *
     * @param code the gRPC status code returned by Dex
     * @param detail the server-provided target detail
     * @param cause the original transport failure
     */
    public FlowNotFoundException(
            final Status.Code code,
            final String detail,
            final Throwable cause) {
        super(code, ErrorSubStatus.FLOW_NOT_EXISTS, detail, cause);
    }
}
