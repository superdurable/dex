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
 * Provides an independent, immutable snapshot of a pull Worker's slot streams.
 *
 * <p>Obtain it with {@link Worker#getPullSlotStreamState()}, including concurrently with start,
 * stop, and running handlers. Changing the Worker later does not change a snapshot. Every count is
 * a number of slot streams or handlers, in the same unit as
 * {@link PullWorkerOptions.Builder#maxConcurrentTasks(int)}.
 *
 * <p>{@link #getOpen()}, {@link #getRegistering()}, {@link #getBusy()}, and {@link #getIdle()}
 * count Step-and-RPC streams only, the streams that accept both Steps and RPCs.
 * {@link #getRpcOnlyOpen()} counts the reserved RPC-only streams. While every stream is registered
 * and no handler outlives its stream, an adaptive Worker, whose
 * {@link PullWorkerOptions.Builder#minOpenSlots(int)} is below maxConcurrentTasks, keeps
 * getOpen() between {@link #getFloor()} and {@link #getEffectiveCeiling()}. A fixed Worker keeps
 * getOpen() equal to getEffectiveCeiling(). Reconnects, orphaned handlers, and a lowered Server cap
 * move getOpen() outside that range until the Worker converges again.
 *
 * <p>getOpen() of zero with {@link #getOrphanedHandlers()} above zero means that the Worker holds
 * no live Step-and-RPC stream and waits for running handlers to return, for example after a Server
 * restart. {@link Worker#getPullRegistrationState()} does not report that gap, because readiness
 * stays latched across reconnects.
 *
 * <pre>{@code
 * PullSlotStreamState state = worker.getPullSlotStreamState();
 * logger.info("open=" + state.getOpen() + " busy=" + state.getBusy()
 *         + " idle=" + state.getIdle() + " orphaned=" + state.getOrphanedHandlers()
 *         + " capacity=" + state.getTotalCapacity());
 * }</pre>
 *
 * @since 1.45.4
 */
public final class PullSlotStreamState {
    private final boolean isAdaptive;
    private final int open;
    private final int registering;
    private final int busy;
    private final int idle;
    private final int orphanedHandlers;
    private final int orphanedRpcOnlyHandlers;
    private final int floor;
    private final int effectiveCeiling;
    private final int growthTarget;
    private final int totalCapacity;
    private final int rpcOnlyOpen;
    private final int rpcOnlySlots;
    private final long streamsGrown;
    private final long streamsShrunk;
    private final long growRejectedAtCap;
    private final long shrinkRaceDrops;

    PullSlotStreamState(final boolean isAdaptive, final int open, final int registering, final int busy,
            final int idle, final int orphanedHandlers, final int orphanedRpcOnlyHandlers, final int floor,
            final int effectiveCeiling, final int growthTarget, final int totalCapacity, final int rpcOnlyOpen,
            final int rpcOnlySlots, final long streamsGrown, final long streamsShrunk,
            final long growRejectedAtCap, final long shrinkRaceDrops) {
        this.isAdaptive = isAdaptive;
        this.open = open;
        this.registering = registering;
        this.busy = busy;
        this.idle = idle;
        this.orphanedHandlers = orphanedHandlers;
        this.orphanedRpcOnlyHandlers = orphanedRpcOnlyHandlers;
        this.floor = floor;
        this.effectiveCeiling = effectiveCeiling;
        this.growthTarget = growthTarget;
        this.totalCapacity = totalCapacity;
        this.rpcOnlyOpen = rpcOnlyOpen;
        this.rpcOnlySlots = rpcOnlySlots;
        this.streamsGrown = streamsGrown;
        this.streamsShrunk = streamsShrunk;
        this.growRejectedAtCap = growRejectedAtCap;
        this.shrinkRaceDrops = shrinkRaceDrops;
    }

    /**
     * Reports whether the Worker varies its open Step-and-RPC streams.
     *
     * @return true when minOpenSlots is below maxConcurrentTasks
     */
    public boolean isAdaptive() {
        return isAdaptive;
    }

    /**
     * Counts the Step-and-RPC streams that are registered or registering and not closing.
     *
     * @return the open Step-and-RPC stream count
     */
    public int getOpen() {
        return open;
    }

    /**
     * Counts the Step-and-RPC streams that wait for the Server's acknowledgement.
     *
     * <p>This includes a reconnect that waits for its backoff.
     *
     * @return the registering Step-and-RPC stream count
     */
    public int getRegistering() {
        return registering;
    }

    /**
     * Counts the Step-and-RPC streams that run a task.
     *
     * @return the busy Step-and-RPC stream count
     */
    public int getBusy() {
        return busy;
    }

    /**
     * Counts the registered Step-and-RPC streams that wait for a task.
     *
     * @return the idle Step-and-RPC stream count
     */
    public int getIdle() {
        return idle;
    }

    /**
     * Counts the Step-and-RPC handlers that still run after their stream ended.
     *
     * <p>Each one keeps its slot, so the Worker does not register a stream for it until it returns.
     *
     * @return the orphaned Step-and-RPC handler count
     */
    public int getOrphanedHandlers() {
        return orphanedHandlers;
    }

    /**
     * Counts the RPC-only handlers that still run after their stream ended.
     *
     * @return the orphaned RPC-only handler count
     */
    public int getOrphanedRpcOnlyHandlers() {
        return orphanedRpcOnlyHandlers;
    }

    /**
     * Returns the number of Step-and-RPC streams the Worker keeps open at rest.
     *
     * <p>It is minOpenSlots minus the reserved RPC slots, limited by
     * {@link #getEffectiveCeiling()}.
     *
     * @return the effective Step-and-RPC floor
     */
    public int getFloor() {
        return floor;
    }

    /**
     * Returns the most Step-and-RPC streams and orphaned handlers the Worker may hold.
     *
     * <p>It is {@link #getTotalCapacity()} minus the RPC-only slots, or minus the RPC-only streams
     * and handlers when they exceed those slots.
     *
     * @return the Step-and-RPC capacity
     */
    public int getEffectiveCeiling() {
        return effectiveCeiling;
    }

    /**
     * Returns the Step-and-RPC stream count that growth may reach.
     *
     * <p>It equals {@link #getEffectiveCeiling()}, or less for the rest of a shrink window after
     * the Server rejected a growth stream at its stream limit.
     *
     * @return the soft growth target
     */
    public int getGrowthTarget() {
        return growthTarget;
    }

    /**
     * Returns the Worker's slot count.
     *
     * <p>It is maxConcurrentTasks, limited by the per-Worker cap that the Server reported in its
     * last acknowledgement.
     *
     * @return the total slot capacity, including RPC-only slots
     */
    public int getTotalCapacity() {
        return totalCapacity;
    }

    /**
     * Counts the RPC-only streams that are registered or registering and not closing.
     *
     * @return the open RPC-only stream count
     */
    public int getRpcOnlyOpen() {
        return rpcOnlyOpen;
    }

    /**
     * Returns the number of RPC-only streams the Worker keeps.
     *
     * <p>It is reservedRpcTasks, or fewer when the Server cap leaves no room for them next to one
     * Step-and-RPC stream.
     *
     * @return the effective RPC-only slot count
     */
    public int getRpcOnlySlots() {
        return rpcOnlySlots;
    }

    /**
     * Counts the Step-and-RPC streams opened by growth since the Worker started.
     *
     * @return the cumulative growth stream count
     */
    public long getStreamsGrown() {
        return streamsGrown;
    }

    /**
     * Counts the streams that the Worker half-closed because it had more than it needed.
     *
     * @return the cumulative shrink count
     */
    public long getStreamsShrunk() {
        return streamsShrunk;
    }

    /**
     * Counts the registrations that the Server rejected with RESOURCE_EXHAUSTED and that the
     * adaptive Worker treated as a soft limit instead of a permanent rejection.
     *
     * @return the cumulative soft rejection count
     */
    public long getGrowRejectedAtCap() {
        return growRejectedAtCap;
    }

    /**
     * Counts the tasks that arrived on a stream after the Worker decided to close it.
     *
     * <p>The Server returns each such task as not delivered and dispatches it again without
     * spending a retry.
     *
     * @return the cumulative count of tasks dropped by a shrink race
     */
    public long getShrinkRaceDrops() {
        return shrinkRaceDrops;
    }

    /**
     * Describes every field, for logs and test failure messages.
     *
     * @return a single-line description of the snapshot
     */
    @Override
    public String toString() {
        return "PullSlotStreamState{isAdaptive=" + isAdaptive + ", open=" + open + ", registering=" + registering
                + ", busy=" + busy + ", idle=" + idle + ", orphanedHandlers=" + orphanedHandlers
                + ", orphanedRpcOnlyHandlers=" + orphanedRpcOnlyHandlers + ", floor=" + floor
                + ", effectiveCeiling=" + effectiveCeiling + ", growthTarget=" + growthTarget
                + ", totalCapacity=" + totalCapacity + ", rpcOnlyOpen=" + rpcOnlyOpen
                + ", rpcOnlySlots=" + rpcOnlySlots + ", streamsGrown=" + streamsGrown
                + ", streamsShrunk=" + streamsShrunk + ", growRejectedAtCap=" + growRejectedAtCap
                + ", shrinkRaceDrops=" + shrinkRaceDrops + "}";
    }
}
