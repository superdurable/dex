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
 * Controls whether {@link Client#startFlow} may reuse an existing Flow ID.
 *
 * <p>Set the policy with {@link StartFlowOptions.Builder#idReusePolicy}. The policy is evaluated by
 * the server against prior and currently running executions with the same Flow ID.
 */
public enum IdReusePolicy {
    /** Uses the Dex server's default Flow-ID reuse policy. */
    DEFAULT,

    /** Allows reuse only when the previous execution ended abnormally. */
    ALLOW_IF_PREVIOUS_FAILED,

    /** Allows reuse when no execution with the ID is currently running. */
    ALLOW_IF_NOT_RUNNING,

    /** Rejects every attempt to reuse the Flow ID. */
    DISALLOW,

    /** Terminates a running execution before starting the replacement. */
    TERMINATE_IF_RUNNING
}
