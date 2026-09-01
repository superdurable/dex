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
 * {@link Context#waitForMethodFailed()} and recover explicitly. This policy does not change
 * durability selection: a method override takes precedence over the Flow configuration, followed by
 * the synchronous server default.
 */
public enum WaitForFailurePolicy {
    /** Fails the Flow when the wait-for method cannot complete. */
    FAIL_FLOW,

    /**
     * Continues to the execute method after recording the wait-for failure.
     *
     * <p>Durability follows the normal method, Flow, and server-default precedence.
     */
    PROCEED
}
