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
 * Controls how a SubFlow condition resolves an execution that already uses its generated Flow ID.
 *
 * <p>The server first compares normalized request IDs, making a retried start idempotent regardless
 * of this policy. It then describes the existing execution and applies the selected state machine.
 */
public enum SubFlowReusePolicy {
    /** Attaches to a running execution or returns any existing terminal result. */
    ATTACH,

    /**
     * Attaches to running or completed executions and restarts an abnormally closed execution.
     *
     * <p>This is the default. Failed, canceled, timed-out, and terminated executions are abnormal.
     */
    RESTART_IF_PREVIOUS_EXITS_ABNORMALLY,

    /** Starts a new execution and terminates an existing running execution. */
    ALWAYS_RESTART
}
