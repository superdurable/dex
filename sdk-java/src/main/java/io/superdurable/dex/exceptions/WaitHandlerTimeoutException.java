/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Sustainable Use License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0
 */

package io.superdurable.dex.exceptions;

import io.grpc.Status;

/** Thrown when a caller-defined wait budget expires. */
public final class WaitHandlerTimeoutException extends DexServiceException {
    /**
     * Creates a typed durable wait timeout.
     *
     * @param code the outer gRPC deadline status
     * @param detail the service-provided timeout detail
     * @param cause the original transport failure, when present
     */
    public WaitHandlerTimeoutException(
            final Status.Code code,
            final String detail,
            final Throwable cause) {
        super(code, ErrorSubStatus.WAIT_HANDLER_TIMEOUT, detail, cause);
    }
}
