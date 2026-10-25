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
 * Reports a request rejected while Dex closes a Flow.
 *
 * <p>Catch this exception when an update-class Client call races with Flow close cleanup.
 * Re-read the retained terminal Flow state before deciding whether another action is needed.
 * For example:
 *
 * <pre>{@code
 * io.superdurable.dex.WaitForStepCompletionOptions options =
 *         io.superdurable.dex.WaitForStepCompletionOptions.newBuilder()
 *                 .requestTimeout(timeout)
 *                 .build();
 * try {
 *     client.waitForStepCompletion(flowId, stepExecutionId, options);
 * } catch (FlowClosingException closing) {
 *     client.waitForFlow(flowId, timeout);
 * }
 * }</pre>
 */
public final class FlowClosingException extends DexServiceException {
    /**
     * Creates a Flow-closing exception from a Dex service response.
     *
     * @param code the gRPC status code returned by Dex
     * @param detail the server-provided close-cleanup detail
     * @param cause the original transport failure
     */
    public FlowClosingException(
            final Status.Code code,
            final String detail,
            final Throwable cause) {
        super(code, ErrorSubStatus.FLOW_CLOSING, detail, cause);
    }
}
