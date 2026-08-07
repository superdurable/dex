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

public final class LongPollTimeoutException extends RuntimeException {
    private final String flowId;

    LongPollTimeoutException(final String flowId, final Throwable cause) {
        super("Flow is still running: " + flowId, cause);
        this.flowId = flowId;
    }

    public String getFlowId() {
        return flowId;
    }
}
