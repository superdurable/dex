/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Sustainable Use License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0
 */

package io.superdurable.dex;

/**
 * Selects how {@link Client#stopFlow} ends a running Flow.
 *
 * <p>Pass the value through {@link StopFlowOptions}. The selected mode determines the terminal
 * status that clients observe when they later describe or wait for the Flow.
 */
public enum StopType {
    /** Requests cooperative cancellation. */
    CANCEL,

    /** Terminates the Flow immediately. */
    TERMINATE,

    /** Ends the Flow as failed. */
    FAIL,

    /**
     * Completes the Flow once no Step is active, like a Step that returns
     * {@link StepDecision#gracefulComplete(Object)}.
     *
     * <p>Use it to end a Flow successfully from outside its Steps, for example from a Step that
     * ends its own Flow through a {@link Client}. Active Steps keep running. Their decisions still
     * apply, so a Step that moves to another Step keeps the Flow running. The Flow closes with
     * {@link FlowStatus#COMPLETED} when the last active Step finishes, or at once when no Step is
     * active. The output of {@link StopFlowOptions} becomes one of the Flow results. A Step waiting
     * for Channel messages or a Timer stays active until its wait ends.
     *
     * <p>A later stop of any type still applies while the Flow waits. Another graceful completion
     * adds its output to the results. The server ignores the reason.
     *
     * <pre>{@code
     * client.stopFlow(
     *         "session-42",
     *         new StopFlowOptions(
     *                 StopType.GRACEFUL_COMPLETE,
     *                 null,
     *                 Map.of("summary", "user ended the session")));
     * }</pre>
     *
     * <p>It requires Dex Server 0.1.34 or newer. Older servers reject it with an invalid argument
     * error.
     *
     * @since 1.45.3
     */
    GRACEFUL_COMPLETE,

    /**
     * Completes the Flow at once, like a Step that returns
     * {@link StepDecision#forceComplete(Object)}.
     *
     * <p>Use it to end a Flow successfully without waiting for its Steps. The server cancels every
     * active Step and discards their results. Results that Steps already returned through graceful
     * completion stay, and the output of {@link StopFlowOptions} is added after them. The Flow
     * closes with {@link FlowStatus#COMPLETED}. Like the Step decision, the close waits until
     * pending Attribute Sync writes and transactional RPCs finish. The server ignores the reason.
     *
     * <pre>{@code
     * client.stopFlow(
     *         "session-42",
     *         new StopFlowOptions(StopType.FORCE_COMPLETE, null, "closed by operator"));
     * }</pre>
     *
     * <p>It requires Dex Server 0.1.34 or newer. Older servers reject it with an invalid argument
     * error.
     *
     * @since 1.45.3
     */
    FORCE_COMPLETE
}
