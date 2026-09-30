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

/**
 * Reports a missing Flow or an operation that cannot use a closed Flow.
 *
 * <p>RPC paths that require Update or Signal, Channel publish, Attribute mutation, stop, timer,
 * configuration, and Step-wait operations use this exception when the Flow never existed or is
 * already closed. All RPC paths also use this exception for missing targets. A query-only RPC
 * without locks, transactionality, durable effects, or Server-forced Update routing can read a
 * retained terminal execution. At that confirmed read boundary, handle this exception as not found
 * without a lifecycle probe. Preserve other service and Worker failures. The exception does not
 * distinguish missing from closed targets or prove the requested action succeeded. Use {@link
 * FlowNotFoundException} for other read and history operations that can target closed Flows.
 *
 * <pre>{@code
 * try {
 *     client.invokeRPC(stub::updateOrder, input);
 * } catch (FlowNotActiveOrNotFoundException unavailable) {
 *     throw new OrderUnavailableException(flowId, unavailable);
 * }
 * }</pre>
 */
public final class FlowNotActiveOrNotFoundException extends DexServiceException {
    /**
     * Creates a missing-or-inactive-Flow exception from a Dex service response.
     *
     * @param code the gRPC status code returned by Dex
     * @param detail the server-provided target detail
     * @param cause the original transport failure
     */
    public FlowNotActiveOrNotFoundException(
            final Status.Code code,
            final String detail,
            final Throwable cause) {
        super(code, ErrorSubStatus.FLOW_NOT_EXISTS, detail, cause);
    }
}
