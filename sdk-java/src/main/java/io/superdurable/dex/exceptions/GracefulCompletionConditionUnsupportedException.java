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

/**
 * Reports a Wait with a graceful completion condition for a Server that cannot evaluate it.
 *
 * <p>The condition that {@link io.superdurable.dex.GracefulCompletion#requested()} returns
 * requires Server protocol version 7 or later, which server-zd 0.1.34 and later releases provide.
 * An older Server would drop the condition, so a Wait with only that condition would not wait at
 * all. The SDK refuses to send the Wait instead. A Step whose waitFor returns such a Wait fails
 * with this exception, and Dex retries it according to its retry policy. Upgrade the Dex Server
 * to resolve it.
 *
 * <p>The Worker reports the exception as the Step's waitFor failure, so it usually appears in the
 * Flow's Step failure details rather than in application code. Code that inspects such a failure
 * can recognize it by type:
 *
 * <pre>{@code
 * if (failure instanceof GracefulCompletionConditionUnsupportedException) {
 *     GracefulCompletionConditionUnsupportedException unsupported =
 *             (GracefulCompletionConditionUnsupportedException) failure;
 *     log.warn("upgrade Dex: the graceful completion condition needs protocol "
 *             + unsupported.getRequiredServerProtocolVersion());
 * }
 * }</pre>
 *
 * @since 1.45.3
 */
public final class GracefulCompletionConditionUnsupportedException extends IllegalStateException {
    private static final long serialVersionUID = 1L;

    private final int negotiatedServerProtocolVersion;
    private final int requiredServerProtocolVersion;

    /**
     * Creates an exception for a negotiated protocol below the required one.
     *
     * @param negotiatedServerProtocolVersion the protocol version agreed with the Server, or
     *     {@code 0} when the Worker has not negotiated one yet
     * @param requiredServerProtocolVersion the minimum Server protocol version for the graceful
     *     completion condition
     * @since 1.45.3
     */
    public GracefulCompletionConditionUnsupportedException(
            final int negotiatedServerProtocolVersion,
            final int requiredServerProtocolVersion) {
        super("the graceful completion condition requires Server protocol "
                + requiredServerProtocolVersion + " or later, but the Server negotiated "
                + negotiatedServerProtocolVersion + "; upgrade the Dex Server");
        this.negotiatedServerProtocolVersion = negotiatedServerProtocolVersion;
        this.requiredServerProtocolVersion = requiredServerProtocolVersion;
    }

    /**
     * Returns the protocol version agreed with the Server.
     *
     * @return the negotiated Server protocol version, or {@code 0} when none was negotiated
     * @since 1.45.3
     */
    public int getNegotiatedServerProtocolVersion() {
        return negotiatedServerProtocolVersion;
    }

    /**
     * Returns the minimum Server protocol version for the graceful completion condition.
     *
     * @return the required Server protocol version
     * @since 1.45.3
     */
    public int getRequiredServerProtocolVersion() {
        return requiredServerProtocolVersion;
    }
}
