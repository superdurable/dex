/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

package io.superdurable.dex.exceptions;

/**
 * Identifies a Dex-specific category within a transport error.
 *
 * <p>Inspect this value from {@link DexServiceException#getSubStatus()} when application recovery
 * depends on more detail than the gRPC status code provides. Prefer concrete exception subclasses
 * for control flow; use the substatus for diagnostics and uncategorized service failures.
 */
public enum ErrorSubStatus {
    /** Indicates an error without a more specific Dex category. */
    UNCATEGORIZED,

    /** Indicates that the requested Flow ID is already in use. */
    FLOW_ALREADY_STARTED,

    /** Indicates that the requested Flow execution does not exist. */
    FLOW_NOT_EXISTS,

    /** Indicates that a worker invocation failed. */
    WORKER_API_ERROR,

    /** Indicates that a long-poll operation reached its wait timeout. */
    LONG_POLL_TIMEOUT,

    /** Indicates that a pending Channel message ID no longer exists. */
    CHANNEL_MESSAGE_NOT_FOUND
}
