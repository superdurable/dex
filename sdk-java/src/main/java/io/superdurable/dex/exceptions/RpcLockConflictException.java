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
 * Reports that a locking RPC could not acquire its requested Attribute locks.
 *
 * <p>The exception uses gRPC {@link Status.Code#ABORTED}. Callers may retry after contention clears,
 * ideally using application-appropriate backoff and idempotent RPC input.
 */
public final class RpcLockConflictException extends DexServiceException {
    /**
     * Creates an RPC lock-conflict exception.
     *
     * @param detail the server-provided lock conflict detail
     * @param cause the original transport failure
     */
    public RpcLockConflictException(final String detail, final Throwable cause) {
        super(Status.Code.ABORTED, ErrorSubStatus.WORKER_API_ERROR, detail, cause);
    }
}
