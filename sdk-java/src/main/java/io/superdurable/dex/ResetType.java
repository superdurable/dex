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
 * Selects the point to which a Flow is reset.
 *
 * <p>Create {@link ResetFlowOptions} with the matching selector method for the chosen value. For
 * example, {@link #HISTORY_EVENT_ID} requires
 * {@link ResetFlowOptions.Builder#historyEventId(long)} before calling
 * {@link Client#resetFlow(String, ResetFlowOptions)}.
 */
public enum ResetType {
    /** Resets to a specific history event ID. */
    HISTORY_EVENT_ID,

    /** Resets to the beginning of the Flow execution. */
    BEGINNING,

    /** Resets to a history event selected by timestamp. */
    HISTORY_EVENT_TIME,

    /** Resets to an occurrence of a Step type. */
    STEP_TYPE,

    /** Resets to a specific Step execution ID. */
    STEP_EXECUTION_ID
}
