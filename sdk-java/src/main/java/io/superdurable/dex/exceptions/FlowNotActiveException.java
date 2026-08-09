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
 * Reports that an operation requiring a running Flow has no active target.
 *
 * <p>RPC, Channel publish, Attribute mutation, stop, timer, configuration, and Step-wait operations
 * use this exception when the Flow never existed or is already closed. Use
 * {@link FlowNotFoundException} for read and history operations that can target closed Flows.
 */
public final class FlowNotActiveException extends DexServiceException {
    /**
     * Creates an inactive-Flow exception from a Dex service response.
     *
     * @param code the gRPC status code returned by Dex
     * @param detail the server-provided target detail
     * @param cause the original transport failure
     */
    public FlowNotActiveException(
            final Status.Code code,
            final String detail,
            final Throwable cause) {
        super(code, ErrorSubStatus.FLOW_NOT_EXISTS, detail, cause);
    }
}
