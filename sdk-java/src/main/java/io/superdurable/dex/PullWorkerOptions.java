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

import java.time.Duration;

/**
 * Configures a Worker that opens outbound task streams to Dex.
 *
 * <p>Pass these options to {@link WorkerOptions.Builder#pull(PullWorkerOptions)}. Each slot serves
 * the Worker identity and every registered Flow type. The worker does not open a listener.
 * Zero task count and a zero timeout select defaults. An empty Worker ID is generated per Worker.
 *
 * <p>Changed in 1.35.0: there are exactly maxConcurrentTasks long-lived streams, one per
 * execution slot, shared across all registered Flow types. Reserved RPC slots are RPC-only
 * streams. Other streams accept both Step and RPC tasks. Capacity no longer causes task declines.
 * Server protocol 4, provided by server-zd 0.1.17 or newer, is required.
 *
 * <p>Changed in 1.45.7: with {@link Builder#minOpenSlots(int)} below maxConcurrentTasks the Worker
 * is adaptive. It keeps between minOpenSlots and maxConcurrentTasks slot streams open, so idle
 * Workers hold fewer Server streams. With minOpenSlots unset or equal to maxConcurrentTasks the
 * Worker is fixed and keeps every slot open as before.
 *
 * <pre>{@code
 * PullWorkerOptions pull = PullWorkerOptions.newBuilder()
 *         .maxConcurrentTasks(8)
 *         .stopDrainTimeout(Duration.ofSeconds(45))
 *         .build();
 * WorkerOptions options = WorkerOptions.newBuilder().pull(pull).build();
 * }</pre>
 */
public final class PullWorkerOptions {
    private final int maxConcurrentTasks;
    private final int reservedRpcTasks;
    private final int minOpenSlots;
    private final boolean isMaxConcurrentTasksExplicit;
    private final boolean isReservedRpcTasksExplicit;
    private final boolean isMinOpenSlotsExplicit;
    private final boolean shouldWarnRpcCapacity;
    private final boolean shouldWarnFloorRpcCapacity;
    private final String appWorkerID;
    private final String taskQueue;
    private final Duration stopDrainTimeout;
    private final boolean stopOnPermanentStreamRejection;
    private final PullSlotSettings slotSettings;

    private PullWorkerOptions(final Builder builder) {
        this.stopOnPermanentStreamRejection = builder.stopOnPermanentStreamRejection;
        this.isMaxConcurrentTasksExplicit = builder.maxConcurrentTasks != 0;
        this.isReservedRpcTasksExplicit = builder.reservedRpcTasks != null;
        this.isMinOpenSlotsExplicit = builder.minOpenSlots != 0;
        this.maxConcurrentTasks = builder.maxConcurrentTasks == 0
                ? Runtime.getRuntime().availableProcessors() : builder.maxConcurrentTasks;
        this.minOpenSlots = builder.minOpenSlots == 0 ? maxConcurrentTasks : builder.minOpenSlots;
        if (minOpenSlots > maxConcurrentTasks) {
            throw new IllegalArgumentException("minOpenSlots " + minOpenSlots
                    + " must not exceed maxConcurrentTasks " + maxConcurrentTasks);
        }
        this.shouldWarnRpcCapacity = builder.reservedRpcTasks == null && maxConcurrentTasks == 1;
        this.shouldWarnFloorRpcCapacity = builder.reservedRpcTasks == null && maxConcurrentTasks >= 2
                && minOpenSlots == 1;
        this.reservedRpcTasks = builder.reservedRpcTasks == null
                ? defaultReservedRpcTasks(maxConcurrentTasks, minOpenSlots) : builder.reservedRpcTasks;
        if (reservedRpcTasks < 0 || reservedRpcTasks >= maxConcurrentTasks) {
            throw new IllegalArgumentException("reservedRpcTasks must be nonnegative and less than maxConcurrentTasks");
        }
        if (reservedRpcTasks + 1 > minOpenSlots) {
            throw new IllegalArgumentException("minOpenSlots " + minOpenSlots + " must be at least reservedRpcTasks "
                    + reservedRpcTasks + " plus one Step-and-RPC slot");
        }
        this.appWorkerID = builder.appWorkerID;
        this.taskQueue = builder.taskQueue;
        this.stopDrainTimeout = builder.stopDrainTimeout.isZero()
                ? Duration.ofSeconds(30) : builder.stopDrainTimeout;
        this.slotSettings = builder.slotSettings;
    }

    // Keeps one RPC-only slot when the ceiling and the floor both leave room for it.
    static int defaultReservedRpcTasks(final int maxConcurrentTasks, final int minOpenSlots) {
        return maxConcurrentTasks >= 2 && minOpenSlots >= 2 ? 1 : 0;
    }

    /**
     * Creates a builder with CPU-count execution slots and a 30-second drain.
     *
     * @return a mutable pull options builder
     */
    public static Builder newBuilder() {
        return new Builder();
    }

    int getMaxConcurrentTasks() {
        return maxConcurrentTasks;
    }

    boolean shouldWarnRpcCapacity() {
        return shouldWarnRpcCapacity;
    }

    boolean shouldWarnFloorRpcCapacity() {
        return shouldWarnFloorRpcCapacity;
    }

    int getReservedRpcTasks() {
        return reservedRpcTasks;
    }

    int getMinOpenSlots() {
        return minOpenSlots;
    }

    boolean isMaxConcurrentTasksExplicit() {
        return isMaxConcurrentTasksExplicit;
    }

    boolean isReservedRpcTasksExplicit() {
        return isReservedRpcTasksExplicit;
    }

    boolean isMinOpenSlotsExplicit() {
        return isMinOpenSlotsExplicit;
    }

    PullSlotSettings getSlotSettings() {
        return slotSettings;
    }

    String getAppWorkerID() {
        return appWorkerID;
    }

    String getTaskQueue() {
        return taskQueue;
    }

    boolean isStopOnPermanentStreamRejection() {
        return stopOnPermanentStreamRejection;
    }

    Duration getStopDrainTimeout() {
        return stopDrainTimeout;
    }

    /**
     * Builds immutable outbound polling options.
     *
     * <p>Obtain a builder from {@link PullWorkerOptions#newBuilder()}. Zero values use the
     * defaults documented by each setter. The enclosing type shows a complete Worker example.
     */
    public static final class Builder {
        private int maxConcurrentTasks;
        private int minOpenSlots;
        private Integer reservedRpcTasks;
        private String appWorkerID = "";
        private String taskQueue = "";
        private Duration stopDrainTimeout = Duration.ZERO;
        private boolean stopOnPermanentStreamRejection;
        private PullSlotSettings slotSettings = PullSlotSettings.defaults();

        private Builder() {
        }

        /**
         * Sets the number of long-lived execution-slot streams for Step and RPC tasks.
         *
         * <p>This also sizes the Worker's handler pool. Worker start fails when a fixed Worker with
         * an explicit maxConcurrentTasks exceeds the Server's per-Worker limit,
         * matching.maxConcurrentTasksCap. Every received task
         * executes without a capacity rejection. The server limit defaults to 256. Lower this
         * option or raise the server limit before starting the Worker; this failure is not retried
         * in the background. Budget server matching.maxStreams for the sum of Worker slots,
         * including overlap during deployments.
         *
         * <p>Changed in 1.35.0: this value is also the exact stream count, and exceeding the
         * server cap fails startup.
         *
         * <p>Changed in 1.45.7: this value is the ceiling of the slot streams, including the
         * reserved RPC-only slots. A fixed Worker, whose {@link #minOpenSlots(int)} is unset or
         * equal to this value, keeps this many streams open. An adaptive Worker, whose
         * minOpenSlots is below this value, opens streams up to it only while every open
         * Step-and-RPC stream is busy. Two cases now clamp the value to the Server cap with a
         * warning that names both values, instead of failing start:
         *
         * <ul>
         *   <li>a fixed Worker with this value unset, whose CPU-count default exceeds the cap;</li>
         *   <li>an adaptive Worker, with this value set or unset.</li>
         * </ul>
         *
         * <p>A fixed Worker with this value unset therefore opens one stream first, learns the cap
         * from its registration, and then opens the others. That adds one round trip to start. A
         * fixed Worker with an explicit value above the cap still fails start and opens all its
         * streams at once. After a clamp, the reservedRpcTasks and minOpenSlots defaults come from
         * the clamped value. In an adaptive Worker, a lower cap reported on a reconnect lowers its
         * capacity: it half-closes the surplus streams, RPC-only streams first, and keeps running.
         * A fixed Worker keeps the earlier behavior for a lower cap on a reconnect.
         *
         * @param value a nonnegative count; zero uses the available CPU count
         * @return this builder
         * @throws IllegalArgumentException if value is negative
         */
        public Builder maxConcurrentTasks(final int value) {
            if (value < 0) {
                throw new IllegalArgumentException("maxConcurrentTasks must not be negative");
            }
            this.maxConcurrentTasks = value;
            return this;
        }

        /**
         * Sets the fewest slot streams the Worker keeps open, which makes it adaptive when below
         * maxConcurrentTasks.
         *
         * <p>This value uses the same unit as {@link #maxConcurrentTasks(int)}, so it counts the
         * reserved RPC-only slots too. Zero, the default, uses the resolved maxConcurrentTasks:
         * every slot keeps a stream, the Worker is fixed, and it behaves as earlier releases did.
         *
         * <p>A value below maxConcurrentTasks makes the Worker adaptive. Use it when idle Workers
         * should hold fewer Server streams, such as a large fleet or a high maxConcurrentTasks. An
         * adaptive Worker:
         *
         * <ul>
         *   <li>starts with minOpenSlots minus reservedRpcTasks Step-and-RPC streams, and opens the
         *       RPC-only streams after the first registration;</li>
         *   <li>doubles its Step-and-RPC streams, one batch at a time, when a task takes the last
         *       idle one, up to maxConcurrentTasks;</li>
         *   <li>every 30 seconds half-closes half of the idle streams above the window's peak busy
         *       count plus 25% headroom, never below the floor and never its last idle stream;</li>
         *   <li>keeps the RPC-only streams at reservedRpcTasks.</li>
         * </ul>
         *
         * <p>A burst larger than the open streams waits a few registration round trips while the
         * Worker grows. A task that reaches a stream while it closes returns to the Server without
         * spending a Step retry, and the Server dispatches it again. When the Server rejects a
         * stream at its stream limit with RESOURCE_EXHAUSTED, an adaptive Worker treats the
         * rejection of a growth stream, or of a reconnect after readiness, as a soft limit. It
         * records no {@link PullRegistrationError} for it. After a soft rejection the Worker waits
         * for another of its registered streams to end, or for the next 30-second shrink window,
         * before it opens a stream again. While rejections continue, it grows by at most one batch
         * per window. Use {@link Worker#getPullSlotStreamState()} to observe the streams.
         *
         * <p>{@link #build()} throws unless
         * {@code reservedRpcTasks + 1 <= minOpenSlots <= maxConcurrentTasks}, using the resolved
         * values. When reservedRpcTasks is unset, its default reserves one slot only when both
         * maxConcurrentTasks and minOpenSlots are at least two. With minOpenSlots of one and no
         * explicit reserve, start logs a warning that RPCs may wait behind Steps. Worker start fails
         * at the first registration when the Server cap lowers maxConcurrentTasks below
         * minOpenSlots, or to reservedRpcTasks or less. That error names the values and the cap.
         *
         * <pre>{@code
         * // Between 8 and 64 slot streams, one of them reserved for RPCs.
         * PullWorkerOptions pull = PullWorkerOptions.newBuilder()
         *         .maxConcurrentTasks(64)
         *         .minOpenSlots(8)
         *         .build();
         * Worker worker = new Worker(registry, blobCache,
         *         WorkerOptions.newBuilder().pull(pull).build());
         * new Thread(worker::start).start();
         * }</pre>
         *
         * @param value a nonnegative count; zero uses the resolved maxConcurrentTasks
         * @return this builder
         * @throws IllegalArgumentException if value is negative; build throws if the resolved value
         *     exceeds maxConcurrentTasks or is not above reservedRpcTasks
         * @since 1.45.7
         */
        public Builder minOpenSlots(final int value) {
            if (value < 0) {
                throw new IllegalArgumentException("minOpenSlots must not be negative");
            }
            this.minOpenSlots = value;
            return this;
        }

        // Sets the shrink window and test hooks; only tests in this package call it.
        Builder slotSettingsForTest(final PullSlotSettings value) {
            this.slotSettings = value;
            return this;
        }

        /**
         * Reserves streams that accept only RPC tasks to protect RPC latency from long-running Steps.
         *
         * <p>Unset reserves one slot when the resolved task limit is at least two, otherwise zero.
         * Set zero to opt out. With one slot and no explicit reserve, Worker start logs a warning
         * about RPC starvation. The remaining streams accept Steps and RPCs. The Server routes
         * Steps only to those streams, and the Worker never declines a Step for capacity.
         * To retain eight concurrent Step handlers with one reserved slot, configure at least nine
         * total slots. RPCs may use either slot kind; the reserve does not guarantee RPC latency
         * when RPC handlers themselves fill those slots.
         *
         * <p>Changed in 1.35.0: the reserve uses RPC-only streams instead of declining Steps
         * after delivery.
         *
         * <p>Changed in 1.45.7: unset also reserves zero when {@link #minOpenSlots(int)} is one,
         * and start then warns that RPCs may wait behind Steps.
         *
         * <pre>{@code
         * PullWorkerOptions pull = PullWorkerOptions.newBuilder()
         *         .maxConcurrentTasks(8).reservedRpcTasks(2).build();
         * }</pre>
         *
         * @param value nonnegative reserved capacity below the resolved limit; null selects the default
         * @return this builder
         * @throws IllegalArgumentException from build if value is negative or reaches the task limit
         */
        public Builder reservedRpcTasks(final Integer value) {
            this.reservedRpcTasks = value;
            return this;
        }

        /**
         * Retains the legacy idle-stream option for source compatibility.
         *
         * <p>This value has no effect. Each execution slot has exactly one long-lived stream.
         * Changed in 1.35.0: this option is deprecated and ignored.
         *
         * @param value a nonnegative legacy count
         * @return this builder
         * @throws IllegalArgumentException if value is negative
         * @deprecated maxConcurrentTasks and minOpenSlots determine the stream count
         */
        @Deprecated
        public Builder idleStreamsPerQueue(final int value) {
            if (value < 0) {
                throw new IllegalArgumentException("idleStreamsPerQueue must not be negative");
            }
            return this;
        }

        /**
         * Sets the process identity used for sticky task routing.
         *
         * <p>The ID has 1-63 ASCII letters, digits, periods, underscores, or hyphens. It must
         * differ from every registered Flow type. Empty selects a hostname-derived random ID.
         * A supplied ID must be unique to the process and change after each restart. Reusing an ID
         * during a rolling deployment can retain another process's unsupported-Step routing state.
         *
         * @param value the Worker ID, or empty to generate one
         * @return this builder
         * @throws IllegalArgumentException if value is null or malformed
         */
        public Builder appWorkerID(final String value) {
            if (value == null || (!value.isEmpty() && !value.matches("[A-Za-z0-9._-]{1,63}"))) {
                throw new IllegalArgumentException("appWorkerID must contain 1-63 ASCII letters, digits, periods, underscores, or hyphens");
            }
            this.appWorkerID = value;
            return this;
        }

        /**
         * Sets the named task queue this Worker pulls from; empty pulls the default task queue.
         *
         * <p>A task queue separates which Workers execute a Flow from the Flow's type. A Flow
         * started with {@link StartFlowOptions.Builder#taskQueue(String)} runs its Steps and RPCs
         * only on Workers that pull that task queue and register its Flow type. A Flow started
         * without one runs only on Workers of the default task queue, as before. Workers on
         * different task queues never take each other's work, even for the same Flow type. Sticky
         * routing stays inside the Flow's task queue.
         *
         * <p>One Worker pulls exactly one task queue. To serve several task queues, construct one
         * Worker per task queue. The Workers can share the Registry and the blob cache. Give each
         * Worker its own appWorkerID when you set one.
         *
         * <p>A name has 1-63 ASCII letters, digits, periods, underscores, or hyphens. Names
         * starting with {@code dex.} are reserved, including {@code dex.default}, and this method
         * rejects them.
         *
         * <p>A non-empty task queue requires Server protocol 5. Worker.start throws
         * IllegalStateException before opening task streams when the Server is older. When the
         * Server confirms a stream for a different task queue, the Worker half-closes that stream
         * and treats it as a permanent rejection with the gRPC code FAILED_PRECONDITION.
         * Worker.waitPullRegistered throws a {@link PullRegistrationError} whose cause is that gRPC
         * error. With stopOnPermanentStreamRejection, Worker.start throws the same error after
         * draining. In Worker.getPullRegistrationState, the slot kind's getLastPermanentError holds
         * the gRPC error itself.
         *
         * <pre>{@code
         * Worker blueWorker = new Worker(registry, cache, WorkerOptions.newBuilder()
         *         .pull(PullWorkerOptions.newBuilder().taskQueue("blue").build()).build());
         * Worker greenWorker = new Worker(registry, cache, WorkerOptions.newBuilder()
         *         .pull(PullWorkerOptions.newBuilder().taskQueue("green").build()).build());
         * new Thread(blueWorker::start).start();
         * new Thread(greenWorker::start).start();
         * // Only blueWorker runs this Flow's Steps and RPCs.
         * client.startFlow(orderFlow, "order-42", input,
         *         StartFlowOptions.newBuilder().taskQueue("blue").build());
         * }</pre>
         *
         * @param value the task queue name, or empty for the default task queue
         * @return this builder
         * @throws IllegalArgumentException if value is null, reserved, or malformed
         * @since 1.45.1
         */
        public Builder taskQueue(final String value) {
            this.taskQueue = TaskQueueNames.validatePullWorkerTaskQueue(value);
            return this;
        }

        /**
         * Sets how long Stop waits for active pull tasks before canceling them.
         *
         * <p>A canceled handler must return before Stop completes. A stream whose final output the
         * Server has not yet confirmed gets a five-second flush grace after the timeout. The Server
         * confirms it by ending the stream or by assigning the stream's next task.
         *
         * <p>Changed in 1.35.0: Stop closes idle streams immediately and starts no new handler.
         * Running handlers can finish and send output within the drain timeout. Tasks arriving
         * after shutdown starts return to Dex without running or consuming a retry. A handler
         * canceled at the drain deadline may have run and consumes a retry. Handlers must honor
         * interruption; the drain timeout cannot force unresponsive application code to return.
         *
         * @param value a nonnegative duration; zero uses 30 seconds
         * @return this builder
         * @throws IllegalArgumentException if value is null or negative
         */
        public Builder stopDrainTimeout(final Duration value) {
            if (value == null || value.isNegative()) {
                throw new IllegalArgumentException("stopDrainTimeout must not be negative");
            }
            this.stopDrainTimeout = value;
            return this;
        }

        /**
         * Stops the Worker on a permanent rejection leaving a required slot kind without a registered stream.
         *
         * <p>Defaults to false, which retries indefinitely. When enabled, the first such rejection
         * before or after readiness drains within stopDrainTimeout, then makes Worker.start throw
         * PullRegistrationError. Other registered streams of that slot kind prevent this shutdown.
         * See the enclosing type for a complete options example.
         *
         * @param value whether to stop on an uncovered permanent stream rejection
         * @return this builder
         */
        public Builder stopOnPermanentStreamRejection(final boolean value) {
            this.stopOnPermanentStreamRejection = value;
            return this;
        }

        /**
         * Builds pull options for a Worker.
         *
         * @return immutable pull options
         */
        public PullWorkerOptions build() {
            return new PullWorkerOptions(this);
        }
    }
}
