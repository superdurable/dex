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
 * Selects the historical point from which a Flow resumes in a new run attempt of the same run.
 *
 * <p>Create {@link TimeTravelOptions} with the matching selector method for the chosen value.
 * {@link #STEP_EXECUTION_ID} also requires a {@link TimeTravelStepMethod}.
 */
public enum TimeTravelType {
    /** Resumes at the beginning of the Flow execution. */
    BEGINNING,

    /** Resumes at a history event selected by timestamp. */
    HISTORY_EVENT_TIME,

    /** Resumes at an occurrence of a Step type. */
    STEP_TYPE,

    /** Resumes at a specific Step execution ID. */
    STEP_EXECUTION_ID,

    /**
     * Resumes right after the change that recorded a history event.
     *
     * <p>Set the event ID with {@link TimeTravelOptions.Builder#eventId(long)} or
     * {@link CloneForkFlowOptions.Builder#eventId(long)}, as the Server's history events report it.
     * Events recorded together, such as a Step completion and the Steps it starts, share one
     * point, so the restored state includes them all. An event inside a Step attempt resolves to
     * the next point where the Flow can stop. The event must still be in the run's history window.
     *
     * @since 1.45.2
     */
    EVENT_ID,

    /**
     * Selects the latest committed state of the source run's current run attempt.
     *
     * <p>Only {@link Client#cloneForkFlow(String, String, CloneForkFlowOptions)} accepts it, and it
     * is the default point of {@link CloneForkFlowOptions}. {@link Client#timeTravel} rejects it,
     * because a Flow cannot return to the state it is in.
     *
     * @since 1.45.2
     */
    CURRENT
}
