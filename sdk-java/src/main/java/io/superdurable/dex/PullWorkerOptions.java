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
 * Workers hold fewer Server streams. When unset, minOpenSlots defaults to
 * min(T, max(R + 1, CPU count)), where T is maxConcurrentTasks and R is reservedRpcTasks.
 * The Worker is fixed only when the floor equals the ceiling and keeps every slot open.
 *
 * <p>Changed in 1.45.7: an unset floor usually makes a Worker adaptive. Unset maxConcurrentTasks defaults to 100
 * instead of the CPU count. Unset minOpenSlots defaults to about the CPU count instead of
 * maxConcurrentTasks. An idle Worker therefore keeps about the CPU count of streams open and
 * grows up to 100 under load. Set minOpenSlots equal to maxConcurrentTasks to keep a fixed Worker.
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
    static final int DEFAULT_MAX_CONCURRENT_TASKS = 100;

    private final int availableProcessors;
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
        this.availableProcessors = Runtime.getRuntime().availableProcessors();
        this.maxConcurrentTasks = builder.maxConcurrentTasks == 0
                ? DEFAULT_MAX_CONCURRENT_TASKS : builder.maxConcurrentTasks;
        // Resolution order: ceiling T, then reserve R, then floor; R never depends on the derived floor.
        final int resolvedReservedRpcTasks = builder.reservedRpcTasks == null
                ? defaultReservedRpcTasks(maxConcurrentTasks,
                        isMinOpenSlotsExplicit ? builder.minOpenSlots : maxConcurrentTasks)
                : builder.reservedRpcTasks;
        this.minOpenSlots = isMinOpenSlotsExplicit ? builder.minOpenSlots
                : defaultMinOpenSlots(maxConcurrentTasks, resolvedReservedRpcTasks, availableProcessors);
        if (minOpenSlots > maxConcurrentTasks) {
            throw new IllegalArgumentException("minOpenSlots " + minOpenSlots
                    + " must not exceed maxConcurrentTasks " + maxConcurrentTasks);
        }
        this.shouldWarnRpcCapacity = builder.reservedRpcTasks == null && maxConcurrentTasks == 1;
        this.shouldWarnFloorRpcCapacity = builder.reservedRpcTasks == null && maxConcurrentTasks >= 2
                && minOpenSlots == 1;
        this.reservedRpcTasks = resolvedReservedRpcTasks;
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

    // The unset floor: min(T, max(R + 1, CPU count)), so an idle Worker keeps about the CPU count open.
    static int defaultMinOpenSlots(final int maxConcurrentTasks, final int reservedRpcTasks,
            final int availableProcessors) {
        return Math.min(maxConcurrentTasks, Math.max(reservedRpcTasks + 1, availableProcessors));
    }

    /**
     * Creates a builder with a ceiling of 100 execution slots, a CPU-count floor, and a 30-second
     * drain.
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

    int getAvailableProcessors() {
        return availableProcessors;
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
         * Sets the ceiling of execution-slot streams for Step and RPC tasks.
         *
         * <p>This also sizes the Worker's handler pool. Worker start fails when a fixed Worker with
         * an explicit maxConcurrentTasks exceeds the Server's per-Worker limit,
         * matching.maxConcurrentTasksCap. Every received task
         * executes without a capacity rejection. The server limit defaults to 256. Lower this
         * option or raise the server limit before starting the Worker; this failure is not retried
         * in the background. A fixed Worker holds every slot's stream. An adaptive Worker holds
         * about its floor while idle and grows up to this ceiling under load. Budget idle streams
         * by the floor and peak streams by this ceiling, including overlap during deployments.
         *
         * <p>Changed in 1.35.0: this value is also the exact stream count, and exceeding the
         * server cap fails startup.
         *
         * <p>Changed in 1.45.7: this value is the ceiling of the slot streams, including the
         * reserved RPC-only slots. An unset {@link #minOpenSlots(int)} defaults to
         * min(T, max(R + 1, CPU count)). A Worker is fixed only when its floor equals this ceiling.
         * An adaptive Worker, whose floor is below this value, opens streams up to it only while
         * every open Step-and-RPC stream is busy. Two cases now clamp the value to the Server cap with a
         * warning that names both values, instead of failing start:
         *
         * <ul>
         *   <li>a fixed Worker with this value unset, whose default exceeds the cap;</li>
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
         * <p>Changed in 1.45.7: zero, the default, now means 100 instead of the available CPU
         * count. Because the minOpenSlots default is about the CPU count, a Worker that leaves this
         * value unset, or sets only this value above the CPU count, is adaptive. Its idle stream
         * count stays near the CPU count, as in earlier releases, and only capacity above it
         * adapts. When the Server cap is below 100, an unset value is clamped to the cap with a
         * warning that names both values, and the Worker keeps running. To keep the earlier
         * default slot count, set this value to {@code Runtime.getRuntime().availableProcessors()}.
         * To keep every slot open at all times, also set {@link #minOpenSlots(int)} to the same
         * value.
         *
         * <pre>{@code
         * // Earlier releases' defaults: CPU-count slots, every slot open.
         * int processors = Runtime.getRuntime().availableProcessors();
         * PullWorkerOptions pull = PullWorkerOptions.newBuilder()
         *         .maxConcurrentTasks(processors)
         *         .minOpenSlots(processors)
         *         .build();
         * }</pre>
         *
         * @param value a nonnegative count; zero uses 100
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
         * reserved RPC-only slots too. Zero, the default, uses
         * {@code min(maxConcurrentTasks, max(reservedRpcTasks + 1, CPU count))}, with the resolved
         * maxConcurrentTasks and reservedRpcTasks. The CPU count is
         * {@code Runtime.getRuntime().availableProcessors()}. With the default maxConcurrentTasks of
         * 100, an idle Worker therefore keeps about the CPU count of streams open, and the Worker
         * is adaptive unless the CPU count reaches 100.
         *
         * <p>Set this value equal to maxConcurrentTasks for a fixed Worker: every slot keeps a
         * stream, as in earlier releases. Use a fixed Worker when every slot must be open at all
         * times, for example to serve a burst without registration round trips.
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
         * at the first registration when the Server cap lowers maxConcurrentTasks below an
         * explicit minOpenSlots, or to reservedRpcTasks or less. That error names the values and
         * the cap. An unset minOpenSlots never fails this way: after a clamp, its default and the
         * reservedRpcTasks default come from the clamped maxConcurrentTasks. With a cap of one and
         * every option unset, the Worker runs one Step-and-RPC stream and no RPC-only stream.
         *
         * <p>Changed in 1.45.7: zero now means
         * {@code min(maxConcurrentTasks, max(reservedRpcTasks + 1, CPU count))} instead of
         * maxConcurrentTasks, so a Worker that leaves this value unset is adaptive whenever
         * maxConcurrentTasks is above that floor. Set this value equal to maxConcurrentTasks to keep
         * the earlier fixed behavior. Because the default depends on the clamped
         * maxConcurrentTasks, an adaptive Worker with this value unset opens one Step-and-RPC
         * stream first, learns the Server cap from its registration, and then opens the rest of
         * its floor. That adds one round trip to start.
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
         * @param value a nonnegative count; zero uses
         *     {@code min(maxConcurrentTasks, max(reservedRpcTasks + 1, CPU count))}
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
         * <p>Unset reserves one slot when the resolved task limit and minOpenSlots are both at
         * least two, otherwise zero.
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
         * <p>Changed in 1.45.7: unset reserves one slot whenever the resolved task limit is at
         * least two, unless minOpenSlots is explicitly one, because the minOpenSlots default is
         * then at least two as well. The default
         * task limit is now 100, so a Worker with no options set reserves one RPC-only slot even on
         * a single-CPU machine. When the Server cap clamps an unset or adaptive task limit, this
         * default comes from the clamped limit: a cap of one reserves zero. An explicit value at or
         * above the clamped limit fails start with an error that names the value and the cap.
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
