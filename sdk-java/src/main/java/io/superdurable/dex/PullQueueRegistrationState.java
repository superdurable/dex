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
 * Describes one slot kind in an immutable pull readiness snapshot.
 *
 * <p>Obtain entries from {@link PullRegistrationState#getQueues()}. A registered stream
 * clears the slot kind's last permanent error. Treat the error object as immutable.
 */
public final class PullQueueRegistrationState {
    private final String queue;
    private final boolean hasRegisteredStream;
    private final Throwable lastPermanentError;

    PullQueueRegistrationState(final String queue, final boolean hasRegisteredStream,
            final Throwable lastPermanentError) {
        this.queue = queue;
        this.hasRegisteredStream = hasRegisteredStream;
        this.lastPermanentError = lastPermanentError;
    }

    /**
     * Returns the slot kind through the legacy queue accessor.
     *
     * <p>Changed in 1.35.0: this returns a slot kind instead of a Flow type or app Worker ID.
     *
     * @return POLL_WORKER_SLOT_KIND_STEP_AND_RPC or POLL_WORKER_SLOT_KIND_RPC_ONLY
     */
    public String getQueue() {
        return queue;
    }

    /**
     * Reports whether a stream is currently registered for this slot kind.
     *
     * @return true while at least one stream has a Registered acknowledgement
     */
    public boolean hasRegisteredStream() {
        return hasRegisteredStream;
    }

    /**
     * Returns the most recent permanent rejection, cleared by re-registration.
     *
     * @return the original transport error, or null if no permanent error remains
     */
    public Throwable getLastPermanentError() {
        return lastPermanentError;
    }
}
