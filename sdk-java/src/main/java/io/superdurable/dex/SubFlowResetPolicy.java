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
 * Decides what happens to a SubFlow condition that a restored Step still waits on.
 *
 * <p>{@link Client#timeTravel} and {@link Client#cloneForkFlow(String, String, CloneForkFlowOptions)}
 * restore Steps from an earlier state. A restored Step whose WaitFor waits on SubFlow conditions
 * names, in each condition, the child Flow that the Step started. The policy decides, per
 * condition, whether the restored wait attaches to that child, restarts the child, fails the
 * condition, or rejects the whole call.
 *
 * <p>Attaching keeps the condition on its original child. When the child run has ended, its result
 * completes the condition. When the child still runs, the condition keeps waiting for it. A child
 * whose run no longer exists cannot be attached; the {@code ATTACH_ELSE} policies then take their
 * second action. Under cloneForkFlow, the source run's children report only to the source run, so
 * a child that still runs cannot be attached either.
 *
 * <p>Restarting starts the child again with the start it originally received. Under timeTravel,
 * the child keeps its Flow ID and a still-running run of it is terminated first. Under
 * cloneForkFlow, the restart starts a new child Flow whose Flow ID derives from the new Flow ID,
 * and the source run's child never changes. A child whose run no longer exists cannot be
 * restarted, and the call fails.
 *
 * <p>Failing completes the condition with a failed child result whose error type is
 * {@link FlowErrorType#SUB_FLOW_ABANDONED}; the Step's Execute receives it like any failed child.
 * Under timeTravel, a child that still runs is terminated.
 *
 * <p>Rejecting fails the call with a
 * {@link io.superdurable.dex.exceptions.DexServiceException} whose gRPC code is
 * {@code FAILED_PRECONDITION}, and nothing changes. Its detail names each Step execution and child
 * Flow ID that caused it.
 *
 * <pre>{@code
 * client.timeTravel("order-123", TimeTravelOptions.newBuilder(TimeTravelType.STEP_TYPE)
 *         .stepType("ShipOrder")
 *         .subFlowResetPolicy(SubFlowResetPolicy.ATTACH_ELSE_FAIL)
 *         .build());
 * }</pre>
 *
 * @since 1.45.2
 */
public enum SubFlowResetPolicy {
    /** Leaves the choice to the Server, which uses {@link #ATTACH_ELSE_RESTART}. */
    DEFAULT,

    /** Restarts every child, whether it ended or still runs. */
    ALWAYS_RESTART,

    /** Fails every condition, whether its child ended or still runs. */
    ALWAYS_FAIL,

    /** Rejects the call when any restored Step waits on a SubFlow condition. */
    ALWAYS_REJECT,

    /** Attaches each condition to its child and restarts a child that cannot be attached. */
    ATTACH_ELSE_RESTART,

    /** Attaches each condition to its child and fails a condition whose child cannot be attached. */
    ATTACH_ELSE_FAIL,

    /** Attaches each condition to its child and rejects the call when a child cannot be attached. */
    ATTACH_ELSE_REJECT
}
