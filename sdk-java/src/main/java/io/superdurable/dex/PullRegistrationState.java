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

import java.util.ArrayList;
import java.util.Collections;
import java.util.List;

/**
 * Provides an independent, immutable snapshot of pull slot readiness.
 *
 * <p>Use {@link Worker#getPullRegistrationState()} for readiness probes after initial startup.
 * Transient failures, reconnects, and stop preserve readiness after a STEP_AND_RPC stream and
 * any configured RPC_ONLY stream have registered once. A permanent rejection makes readiness
 * false while its slot kind has no registered stream. Readiness is never true after Worker start
 * fails because maxConcurrentTasks exceeds the Server's per-Worker limit.
 * See {@link Worker#waitPullRegistered(java.time.Duration)} for a startup example.
 */
public final class PullRegistrationState {
    private final boolean isReady;
    private final List<PullQueueRegistrationState> queues;

    PullRegistrationState(final boolean isReady, final List<PullQueueRegistrationState> queues) {
        this.isReady = isReady;
        this.queues = Collections.unmodifiableList(new ArrayList<PullQueueRegistrationState>(queues));
    }

    /**
     * Reports whether initial registration completed without an uncovered permanent rejection.
     *
     * @return true after required slot kinds registered once unless a slot kind has a permanent error
     *     and no registered stream
     */
    public boolean isReady() {
        return isReady;
    }

    /**
     * Returns slot-kind snapshots through the legacy queue collection accessor.
     *
     * <p>Changed in 1.35.0: entries describe slot kinds rather than routing queues.
     *
     * @return an immutable list ordered STEP_AND_RPC then RPC_ONLY when configured
     */
    public List<PullQueueRegistrationState> getQueues() {
        return queues;
    }
}
