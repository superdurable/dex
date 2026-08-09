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
import io.superdurable.dex.Client;

/**
 * Reports that a blocking Flow wait ended while the Flow was still running.
 *
 * <p>This timeout is not a Flow failure. Callers may catch the exception and issue another
 * {@link Client#waitForFlow} request for the same Flow ID.
 */
public final class LongPollTimeoutException extends DexServiceException {
    private final String flowId;

    /**
     * Creates a long-poll timeout with the target Flow identity.
     *
     * @param code the gRPC status code returned by Dex
     * @param detail the server-provided timeout detail
     * @param flowId the Flow ID that remains active
     * @param cause the original transport failure
     */
    public LongPollTimeoutException(
            final Status.Code code,
            final String detail,
            final String flowId,
            final Throwable cause) {
        super(code, ErrorSubStatus.LONG_POLL_TIMEOUT, detail, cause);
        this.flowId = flowId;
    }

    /**
     * Returns the Flow ID whose long poll timed out.
     *
     * @return the Flow ID
     */
    public String getFlowId() {
        return flowId;
    }
}
