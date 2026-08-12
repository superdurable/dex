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
 * Controls what happens after a Step's wait-for method exhausts its retry policy.
 *
 * <p>Set this value with {@link StepOptions.Builder#waitForFailure}. Proceeding calls
 * {@link Step#execute(Context, Object)} after the wait-for failure, allowing user code to inspect
 * {@link Context#waitForMethodFailed()} and recover explicitly. Dex defaults to
 * {@link StepDurability#SYNC} for a wait-for method using {@link #PROCEED}; a Flow-wide durability
 * setting does not override that safety choice. Applications may explicitly select
 * {@link StepDurability#ASYNC} with {@link StepOptions.Builder#waitForDurability} when they accept
 * the possibility that the wait-for method can run again after execute has already begun.
 */
public enum WaitForFailurePolicy {
    /** Fails the Flow when the wait-for method cannot complete. */
    FAIL_FLOW,

    /**
     * Continues to the execute method after recording the wait-for failure.
     *
     * <p>Dex defaults to synchronous durability for this failure transition unless the application
     * explicitly selects a different wait-for durability in {@link StepOptions}.
     */
    PROCEED
}
