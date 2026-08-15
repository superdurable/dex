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
 * Represents the lifecycle status of a Flow execution.
 *
 * <p>Use {@link FlowInfo#getStatus()} for a described Flow or {@link FlowResult#getStatus()} for a
 * client wait or SubFlow condition result.
 */
public enum FlowStatus {
    /** The Flow execution can still make progress. */
    RUNNING,

    /** The Flow completed successfully. */
    COMPLETED,

    /** The Flow failed. */
    FAILED,

    /** The Flow was canceled. */
    CANCELED,

    /** The Flow was forcibly terminated. */
    TERMINATED,

    /** Reserved for backend hard-timeout reporting. Applications must not depend on this status. */
    SERVER_SIDE_TIMEOUT_INTERNAL_ONLY,

    /** The Flow continued as a new execution. */
    CONTINUED_AS_NEW
}
