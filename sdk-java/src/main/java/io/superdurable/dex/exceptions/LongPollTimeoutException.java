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
 * Thrown by {@link Client#waitForFlow(String, Class, java.time.Duration)} when the timeout expires
 * before the Flow reaches a terminal status.
 *
 * <p>This timeout does not indicate a Flow failure. The Flow is still running, so callers may catch
 * the exception and call {@code waitForFlow} again with the same Flow ID.
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
