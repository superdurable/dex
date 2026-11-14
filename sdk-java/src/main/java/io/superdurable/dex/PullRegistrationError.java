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
 * Identifies a permanent pull rejection leaving a slot kind without a registered stream.
 *
 * <p>Thrown by {@link Worker#waitPullRegistered(java.time.Duration)} before initial readiness,
 * or by {@link Worker#start()} when stopping on permanent rejection is enabled.
 * Permanent codes are InvalidArgument, ResourceExhausted, Unimplemented, PermissionDenied,
 * and Unauthenticated. The cause is the original transport error. See the wait method for a startup example.
 *
 * <p>Changed in 1.45.1: the error carries the Worker's task queue in {@link #getTaskQueue()}. A
 * stream whose registration the Server confirms for a different task queue is also a permanent
 * rejection. Its cause then has the gRPC code FailedPrecondition.
 */
public final class PullRegistrationError extends IllegalStateException {
    private static final long serialVersionUID = 1L;
    private final String queue;
    private final String taskQueue;

    /**
     * Creates a rejection on the default task queue retaining its original transport cause.
     *
     * @param queue the rejected slot kind, POLL_WORKER_SLOT_KIND_STEP_AND_RPC or
     *     POLL_WORKER_SLOT_KIND_RPC_ONLY
     * @param cause the original permanent transport error
     */
    public PullRegistrationError(final String queue, final Throwable cause) {
        this(queue, "", cause);
    }

    /**
     * Creates a rejection of a stream that tried to register a task queue.
     *
     * <p>The message names the task queue when it is not the default task queue.
     *
     * @param queue the rejected slot kind, POLL_WORKER_SLOT_KIND_STEP_AND_RPC or
     *     POLL_WORKER_SLOT_KIND_RPC_ONLY
     * @param taskQueue the task queue the stream tried to register, or empty for the default
     *     task queue; null is treated as empty
     * @param cause the original permanent transport error
     * @since 1.45.1
     */
    public PullRegistrationError(final String queue, final String taskQueue, final Throwable cause) {
        super("pull Worker slot kind " + queue
                + (taskQueue == null || taskQueue.isEmpty() ? "" : " on task queue " + taskQueue)
                + " registration rejected", cause);
        this.queue = queue;
        this.taskQueue = taskQueue == null ? "" : taskQueue;
    }

    /**
     * Returns the rejected slot kind through the legacy queue accessor.
     *
     * <p>Changed in 1.35.0: this returns a slot kind instead of a Flow type or app Worker ID.
     *
     * @return the slot kind name, rather than a single routing queue
     */
    public String getQueue() {
        return queue;
    }

    /**
     * Returns the task queue the rejected stream tried to register.
     *
     * <p>Every stream of one Worker registers the task queue set by
     * {@link PullWorkerOptions.Builder#taskQueue(String)}.
     *
     * @return the task queue name, or empty for the default task queue
     * @since 1.45.1
     */
    public String getTaskQueue() {
        return taskQueue;
    }
}
