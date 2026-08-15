/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

package io.superdurable.dex;

/**
 * Selects the historical point from which a Flow resumes in a new run.
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
    STEP_EXECUTION_ID
}
