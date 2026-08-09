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

public final class LongPollTimeoutException extends DexException {
    private final String flowId;

    public LongPollTimeoutException(
            final Status.Code code,
            final String detail,
            final String flowId,
            final Throwable cause) {
        super(code, ErrorSubStatus.LONG_POLL_TIMEOUT, detail, cause);
        this.flowId = flowId;
    }

    public String getFlowId() {
        return flowId;
    }
}
