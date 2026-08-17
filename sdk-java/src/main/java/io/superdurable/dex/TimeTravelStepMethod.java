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
 * Selects the Step method used as a {@link TimeTravelType#STEP_EXECUTION_ID} boundary.
 *
 * <p>Use {@link #WAIT_FOR} to rerun WaitFor and everything after it. Use {@link #EXECUTE} to keep
 * the selected WaitFor result and rerun Execute and everything after it.
 */
public enum TimeTravelStepMethod {
    /** Resumes before the selected Step execution's WaitFor method. */
    WAIT_FOR,

    /** Resumes before the selected Step execution's Execute method. */
    EXECUTE
}
